package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V9-04 (gap G4, docs/design/12-v9-harness-alignment.md V9-04): these tests
// prove, against a real SQLite store, that ScheduleExecutableNodeRun resolves
// a context route against a ResolutionContext built from the WorkItem's
// effective scope (path scopes and the project's Components) and the node's
// role, so ONE agent profile and ONE context route give each code area its
// own knowledge. Before V9-04 the context carried only TaskKind and
// RiskClass, so every resource below that declares componentTags, pathTags or
// blockKinds was excluded as NOT_APPLICABLE for every task and the only
// resource ever selected was the global one.
//
// One Layer publishes seven resources; the route pins all of them; each
// scenario seeds a WorkItem's effective scope (the rows CreateChildWorkItem
// and a scope expansion write, via the same repository method the sibling
// tests use), schedules the AGENT node, and reads back what the persisted
// CONTEXT_RESOLUTION_V1 decision recorded: the reasons, the context it was
// resolved against, and the ResourceRefs the snapshot pinned.

const (
	selGlobal         = "global-rule"
	selBackendPath    = "backend-by-path"
	selFrontendPath   = "frontend-by-path"
	selBackendComp    = "backend-by-component"
	selFrontendComp   = "frontend-by-component"
	selCheckerOnly    = "checker-only"
	selBackendChecker = "backend-checker"
	selProjectID      = "project-1"
)

// selectorLayerResources is the Layer every scenario pins, in the order the
// route lists it (the order is irrelevant to the result: Resolve sorts).
func selectorLayerResources() []layer.Resource {
	prov := layer.Provenance{Owner: "team-x", Source: "doc-1", Revision: "v1"}
	return []layer.Resource{
		{Key: selGlobal, Convention: "applies everywhere", Priority: definition.PriorityGuidance, Global: true, Provenance: prov},
		{Key: selBackendPath, Convention: "backend conventions", Priority: definition.PriorityGuidance, Selector: layer.Selector{PathTags: []string{"backend"}}, Provenance: prov},
		{Key: selFrontendPath, Convention: "frontend conventions", Priority: definition.PriorityGuidance, Selector: layer.Selector{PathTags: []string{"frontend"}}, Provenance: prov},
		{Key: selBackendComp, Convention: "backend component notes", Priority: definition.PriorityGuidance, Selector: layer.Selector{ComponentTags: []string{"backend"}}, Provenance: prov},
		{Key: selFrontendComp, Convention: "frontend component notes", Priority: definition.PriorityGuidance, Selector: layer.Selector{ComponentTags: []string{"frontend"}}, Provenance: prov},
		{Key: selCheckerOnly, Convention: "review checklist", Priority: definition.PriorityGuidance, Selector: layer.Selector{BlockKinds: []string{"CHECKER"}}, Provenance: prov},
		{Key: selBackendChecker, Convention: "backend review checklist", Priority: definition.PriorityGuidance, Selector: layer.Selector{PathTags: []string{"backend"}, BlockKinds: []string{"CHECKER"}}, Provenance: prov},
	}
}

// selectorScope is one effective scope entry to seed.
type selectorScope struct {
	repositoryID string
	access       workdomain.RepositoryAccess
	paths        []string
}

type selectorScenario struct {
	name   string
	scopes []selectorScope
	role   workflow.AgentRole
	// wantSelected/wantExcluded are resource keys (the test sorts them). Every excluded
	// resource must carry the reason NOT_APPLICABLE (the budget is large).
	wantSelected []string
	wantExcluded []string
	// wantInput is the persisted decision input without the two fields that
	// come from the WorkItem row (taskKind, riskClass), which the test
	// appends from the stored WorkItem.
	wantComponentTags        string
	wantPathTags             string
	wantWholeRepositoryScope bool
	wantBlockKind            string
}

type selectorOutcome struct {
	selected      []string
	excluded      map[string]string // resource key -> reason
	excludedKeys  []string
	input         string
	snapshotKeys  []string
	workItemKind  string
	workItemRisk  string
	decisionKind  string
	policyVersion string
}

// registerSecondActiveRepositorySQLite registers repositoryID in the project
// readyFixtureSQLite already created and drives it to ACTIVE, the same
// REGISTERING->PROBING->ACTIVE walk seedActiveRepositorySQLite does after it
// creates the project.
func registerSecondActiveRepositorySQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := ports.Command{
		ID: "cmd-reg-" + repositoryID, IdempotencyKey: "idem-reg-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-reg-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID,
		RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}
}

// createSelectorComponent creates a Component through the catalog command, the
// way an operator (or the repository probe, which mints one DIRECTORY
// Component per top-level directory with Name = Path = the directory name)
// would.
func createSelectorComponent(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, name, path string) {
	t.Helper()
	if _, err := catalog.CreateComponent(context.Background(), uow, ids, catalog.CreateComponentRequest{
		ProjectID: projectID, RepositoryID: repositoryID, Name: name, Path: path, Kind: "DIRECTORY",
	}); err != nil {
		t.Fatalf("CreateComponent(%s/%s): %v", repositoryID, name, err)
	}
}

// seedScopeEntry adds one effective scope row for workItemID, the row
// CreateChildWorkItem writes for each ScopeGrantRequest.
func seedScopeEntry(t *testing.T, uow ports.UnitOfWork, workItemID string, entry selectorScope) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		scope, err := workdomain.NewRepositoryScope(
			item.FamilyID, 1, project.RepositoryID(entry.repositoryID), entry.access,
			entry.paths, "selector test", "actor-1", time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		_, err = tx.Work().AddEffectiveScope(ctx, workItemID, scope)
		return err
	})
	if err != nil {
		t.Fatalf("seed effective scope %+v: %v", entry, err)
	}
}

// runSelectorScenario schedules the AGENT node of a fresh run over a WorkItem
// with sc's effective scope and returns what was persisted.
func runSelectorScenario(t *testing.T, sc selectorScenario) selectorOutcome {
	t.Helper()
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-selector.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, selProjectID, "repo-1")
	// A second repository whose directories carry the same names: its
	// Components must never be tagged for work scoped to repo-1 only.
	registerSecondActiveRepositorySQLite(t, uow, ids, selProjectID, "repo-2")
	createSelectorComponent(t, uow, ids, selProjectID, "repo-1", "backend", "backend")
	createSelectorComponent(t, uow, ids, selProjectID, "repo-1", "frontend", "frontend")
	createSelectorComponent(t, uow, ids, selProjectID, "repo-1", "docs", "docs")
	createSelectorComponent(t, uow, ids, selProjectID, "repo-2", "payments", "backend")
	for _, entry := range sc.scopes {
		seedScopeEntry(t, uow, root.WorkItemID, entry)
	}

	// One Layer, one context route pinning every resource, one agent profile.
	layerDoc := layer.LayerDocument{Resources: selectorLayerResources()}
	publishLayerVersion(t, uow, "layer-def-1", "layer-v1", layerDoc)
	refs := make([]policy.ResourceRef, 0, len(layerDoc.Resources))
	for _, resource := range layerDoc.Resources {
		refs = append(refs, policy.ResourceRef{
			OwnerVersionID: "layer-v1", ResourceKey: resource.Key,
			ContentHash: layerResourceContentHash(t, "layer-v1", resource.Key, layerDoc),
		})
	}
	publishPolicyVersion(t, uow, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context:  &policy.ContextRules{Selector: []string{"*"}, Budget: policy.ContextBudget{MaxTokens: 65536}, ResourceRefs: refs},
	})
	publishAgentProfileVersionOnly(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	version := publishWorkflowVersionDocument(t, uow, selProjectID, "wf-def-1", "wf-v-1",
		agentExecutableDocumentWithRole("agent-profile-v1", fullyResolvablePolicyRefs(), nil, sc.role))
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope(selProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: selProjectID, WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: started.RunID, NodeRunID: started.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun: %v", err)
	}
	result, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: started.RunID, NodeRunID: hop.NextNodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}

	var outcome selectorOutcome
	outcome.excluded = map[string]string{}
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		decision, err := tx.Runtime().GetDecisionArtifact(ctx, hop.NextNodeRunID+"-context-resolution-v1")
		if err != nil {
			return err
		}
		outcome.decisionKind, outcome.policyVersion, outcome.input = decision.Kind, decision.PolicyVersion, string(decision.Input)
		var resolution struct {
			Selected []struct {
				Identity struct{ ResourceKey string }
				Reason   string
			}
			Excluded []struct {
				Identity struct{ ResourceKey string }
				Reason   string
			}
		}
		if err := json.Unmarshal(decision.Result, &resolution); err != nil {
			return err
		}
		for _, s := range resolution.Selected {
			if s.Reason != "SELECTED" {
				return fmt.Errorf("selected resource %s has reason %s", s.Identity.ResourceKey, s.Reason)
			}
			outcome.selected = append(outcome.selected, s.Identity.ResourceKey)
		}
		for _, e := range resolution.Excluded {
			outcome.excluded[e.Identity.ResourceKey] = e.Reason
			outcome.excludedKeys = append(outcome.excludedKeys, e.Identity.ResourceKey)
		}

		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, result.AttemptID)
		if err != nil {
			return err
		}
		snapshot, err := tx.ContextSnapshots().GetSnapshot(ctx, string(*attempt.ContextSnapshotID))
		if err != nil {
			return err
		}
		for _, ref := range snapshot.ResourceRefs {
			outcome.snapshotKeys = append(outcome.snapshotKeys, ref.ResourceKey)
		}
		item, err := tx.Work().GetWorkItem(ctx, root.WorkItemID)
		if err != nil {
			return err
		}
		outcome.workItemKind, outcome.workItemRisk = string(item.Kind), string(item.RiskLevel)
		return nil
	}); err != nil {
		t.Fatalf("read persisted resolution: %v", err)
	}
	sort.Strings(outcome.selected)
	sort.Strings(outcome.excludedKeys)
	sort.Strings(outcome.snapshotKeys)
	return outcome
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScheduleExecutableNodeRun_SQLite_ContextRouteSelectsByComponentPathAndBlock(t *testing.T) {
	backendPathsOnly := []string{selBackendPath, selBackendComp, selGlobal}
	frontendPathsOnly := []string{selFrontendComp, selFrontendPath, selGlobal}
	scenarios := []selectorScenario{
		{
			name:         "scope backend, MAKER: only the backend resources and the global one",
			scopes:       []selectorScope{{"repo-1", workdomain.RepositoryWrite, []string{"backend"}}},
			role:         workflow.AgentRoleMaker,
			wantSelected: backendPathsOnly,
			// The CHECKER resources are excluded for a MAKER.
			wantExcluded:      []string{selBackendChecker, selCheckerOnly, selFrontendComp, selFrontendPath},
			wantComponentTags: `["backend"]`, wantPathTags: `["backend"]`, wantBlockKind: "MAKER",
		},
		{
			name:              "scope frontend, MAKER: the reverse",
			scopes:            []selectorScope{{"repo-1", workdomain.RepositoryWrite, []string{"frontend"}}},
			role:              workflow.AgentRoleMaker,
			wantSelected:      frontendPathsOnly,
			wantExcluded:      []string{selBackendChecker, selBackendComp, selBackendPath, selCheckerOnly},
			wantComponentTags: `["frontend"]`, wantPathTags: `["frontend"]`, wantBlockKind: "MAKER",
		},
		{
			name:         "no path scopes (whole repository), MAKER: every path and component resource",
			scopes:       []selectorScope{{"repo-1", workdomain.RepositoryWrite, nil}},
			role:         workflow.AgentRoleMaker,
			wantSelected: []string{selBackendComp, selBackendPath, selFrontendComp, selFrontendPath, selGlobal},
			// Only the block kind keeps the CHECKER resources out.
			wantExcluded: []string{selBackendChecker, selCheckerOnly},
			// Every repo-1 Component, none of repo-2's (payments).
			wantComponentTags: `["backend","docs","frontend"]`, wantPathTags: `[]`, wantWholeRepositoryScope: true, wantBlockKind: "MAKER",
		},
		{
			name:         "scope backend/src still gets the resources tagged backend",
			scopes:       []selectorScope{{"repo-1", workdomain.RepositoryWrite, []string{"backend/src"}}},
			role:         workflow.AgentRoleMaker,
			wantSelected: backendPathsOnly,
			wantExcluded: []string{selBackendChecker, selCheckerOnly, selFrontendComp, selFrontendPath},
			// The Component "backend" is an ancestor of the scope path.
			wantComponentTags: `["backend"]`, wantPathTags: `["backend/src"]`, wantBlockKind: "MAKER",
		},
		{
			name:   "scope backend, CHECKER: also the checker resources, and the one needing both",
			scopes: []selectorScope{{"repo-1", workdomain.RepositoryWrite, []string{"backend"}}},
			role:   workflow.AgentRoleChecker,
			wantSelected: []string{
				selBackendChecker, selBackendComp, selBackendPath, selCheckerOnly, selGlobal,
			},
			wantExcluded:      []string{selFrontendComp, selFrontendPath},
			wantComponentTags: `["backend"]`, wantPathTags: `["backend"]`, wantBlockKind: "CHECKER",
		},
		{
			name:         "scope frontend, CHECKER: the checker resource, not the backend one that also needs the path",
			scopes:       []selectorScope{{"repo-1", workdomain.RepositoryWrite, []string{"frontend"}}},
			role:         workflow.AgentRoleChecker,
			wantSelected: []string{selCheckerOnly, selFrontendComp, selFrontendPath, selGlobal},
			wantExcluded: []string{selBackendChecker, selBackendComp, selBackendPath},
			// AND across dimensions: selBackendChecker matches the block kind
			// but not the path.
			wantComponentTags: `["frontend"]`, wantPathTags: `["frontend"]`, wantBlockKind: "CHECKER",
		},
		{
			name: "READ and WRITE entries both count",
			scopes: []selectorScope{
				{"repo-1", workdomain.RepositoryWrite, []string{"backend"}},
				{"repo-1", workdomain.RepositoryRead, []string{"frontend"}},
			},
			role:         workflow.AgentRoleMaker,
			wantSelected: []string{selBackendComp, selBackendPath, selFrontendComp, selFrontendPath, selGlobal},
			wantExcluded: []string{selBackendChecker, selCheckerOnly},
			// pathTags are sorted and deduplicated.
			wantComponentTags: `["backend","frontend"]`, wantPathTags: `["backend","frontend"]`, wantBlockKind: "MAKER",
		},
		{
			name: "one whole-repository entry next to a narrow one makes every tag apply",
			scopes: []selectorScope{
				{"repo-1", workdomain.RepositoryRead, nil},
				{"repo-1", workdomain.RepositoryWrite, []string{"backend"}},
			},
			role:         workflow.AgentRoleMaker,
			wantSelected: []string{selBackendComp, selBackendPath, selFrontendComp, selFrontendPath, selGlobal},
			wantExcluded: []string{selBackendChecker, selCheckerOnly},
			// The recorded context still lists the narrow path.
			wantComponentTags: `["backend","docs","frontend"]`, wantPathTags: `["backend"]`, wantWholeRepositoryScope: true, wantBlockKind: "MAKER",
		},
		{
			name:         "scope on another repository: Components of repo-1 are not tagged, pathTags do not name a repository",
			scopes:       []selectorScope{{"repo-2", workdomain.RepositoryWrite, []string{"backend"}}},
			role:         workflow.AgentRoleMaker,
			wantSelected: []string{selBackendPath, selGlobal},
			wantExcluded: []string{selBackendChecker, selBackendComp, selCheckerOnly, selFrontendComp, selFrontendPath},
			// Only repo-2's Component "payments" overlaps; no resource is
			// tagged for it. A pathTag cannot tell repo-1/backend from
			// repo-2/backend (documented), so selBackendPath still applies.
			wantComponentTags: `["payments"]`, wantPathTags: `["backend"]`, wantBlockKind: "MAKER",
		},
		{
			name:         "no effective scope at all: only the global resource",
			role:         workflow.AgentRoleMaker,
			wantSelected: []string{selGlobal},
			wantExcluded: []string{selBackendChecker, selBackendComp, selBackendPath, selCheckerOnly, selFrontendComp, selFrontendPath},
			// A root WorkItem has no effective scope rows.
			wantComponentTags: `[]`, wantPathTags: `[]`, wantBlockKind: "MAKER",
		},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			got := runSelectorScenario(t, sc)
			sort.Strings(sc.wantSelected)
			sort.Strings(sc.wantExcluded)

			if !equalKeys(got.selected, sc.wantSelected) {
				t.Fatalf("selected = %v, want %v", got.selected, sc.wantSelected)
			}
			if !equalKeys(got.excludedKeys, sc.wantExcluded) {
				t.Fatalf("excluded = %v, want %v", got.excludedKeys, sc.wantExcluded)
			}
			for key, reason := range got.excluded {
				if reason != "NOT_APPLICABLE" {
					t.Fatalf("resource %s excluded with reason %s, want NOT_APPLICABLE", key, reason)
				}
			}
			// The snapshot pins exactly what the decision says was selected.
			if !equalKeys(got.snapshotKeys, sc.wantSelected) {
				t.Fatalf("snapshot ResourceRefs = %v, want %v", got.snapshotKeys, sc.wantSelected)
			}

			// The recorded input: the context the resolution ran against,
			// keys in the documented order, arrays never null.
			wantInput := fmt.Sprintf(
				`{"componentTags":%s,"pathTags":%s,"wholeRepositoryScope":%t,"blockKind":%q,"taskKind":%q,"riskClass":%q}`,
				sc.wantComponentTags, sc.wantPathTags, sc.wantWholeRepositoryScope, sc.wantBlockKind, got.workItemKind, got.workItemRisk,
			)
			if got.input != wantInput {
				t.Fatalf("CONTEXT_RESOLUTION_V1 input =\n  %s\nwant\n  %s", got.input, wantInput)
			}
			t.Logf("CONTEXT_RESOLUTION_V1 input: %s", got.input)
			if got.decisionKind != "CONTEXT_RESOLUTION_V1" || got.policyVersion != "v1" {
				t.Fatalf("decision = %s/%s, want CONTEXT_RESOLUTION_V1/v1", got.decisionKind, got.policyVersion)
			}
		})
	}
}
