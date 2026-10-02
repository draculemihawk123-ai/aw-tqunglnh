// V9-04 — "Selector theo component, path và block có hiệu lực lúc chạy"
// (docs/design/12-v9-harness-alignment.md V9-04; gap G4 in
// docs/harness-engineering/15-doi-chieu-v9.md).
//
// A repository holds two areas, backend/ and frontend/, and the project's
// knowledge for each lives in one Layer whose resources are tagged by
// component or by path. Before V9-04 the scheduler resolved the context route
// against a context that carried only the task kind and the risk class, so
// every tagged resource was excluded for every task: the only way to give the
// backend task backend knowledge and the frontend task frontend knowledge was
// a separate agent profile (and a separate context route) per area.
//
// The scenario here keeps ONE workflow, ONE agent profile and ONE context
// route, creates two WorkItems whose effective scopes are backend and
// frontend, runs each through the real stack (the fake provider CLI as a real
// process, a real git worktree, a real sqlite store and worker pool) and reads
// what each spawned process actually received on stdin: the instruction
// artifact carries its own area's resources and nothing of the other area's.
// The Components come from the real repository probe, which creates one
// DIRECTORY Component per top-level directory (Name = Path = the directory).
//
// Nothing tells the process which resources to expect; the only thing that can
// make the two prompts differ is the scheduler's resolution of the same route
// against two different scopes, so the test fails on a build that resolves the
// route against a context without scope.
package v5accept

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers"
	"github.com/taQuangLing/agent-workflow/internal/adapters/repoprobe"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const (
	areaSharedKey         = "shared-conventions"
	areaBackendLayoutKey  = "backend-layout"
	areaFrontendLayoutKey = "frontend-layout"
	areaBackendAPIKey     = "backend-api-rules"
	areaFrontendA11yKey   = "frontend-a11y-rules"
	areaReviewKey         = "review-checklist"

	// Sentinels that appear only in one resource's content, so "the process
	// did not receive resource X" is checked against the bytes it was given,
	// not only against the keys.
	areaBackendSentinel  = "BACKEND-AREA-SENTINEL"
	areaFrontendSentinel = "FRONTEND-AREA-SENTINEL"
	areaReviewSentinel   = "REVIEW-CHECKLIST-SENTINEL"
)

// areaLayerDocument is the one Layer both WorkItems share. Four resources are
// tagged by area (two by pathTags, two by componentTags, one of those a
// HARD_CONSTRAINT so it lands in the prompt's hardConstraints section), one is
// tagged for CHECKER nodes (the workflow only has a MAKER), one is global.
func areaLayerDocument() layer.LayerDocument {
	prov := layer.Provenance{Owner: "platform-team", Source: "docs/knowledge", Revision: "v1"}
	return layer.LayerDocument{Resources: []layer.Resource{
		{Key: areaSharedKey, Convention: "Shared by every area.", Priority: definition.PriorityGuidance, Global: true, Provenance: prov},
		{Key: areaBackendLayoutKey, Convention: areaBackendSentinel + ": controllers under web, services under service.", Priority: definition.PriorityGuidance, Selector: layer.Selector{PathTags: []string{"backend"}}, Provenance: prov},
		{Key: areaFrontendLayoutKey, Convention: areaFrontendSentinel + ": components under src/components.", Priority: definition.PriorityGuidance, Selector: layer.Selector{PathTags: []string{"frontend"}}, Provenance: prov},
		{Key: areaBackendAPIKey, Convention: areaBackendSentinel + ": every endpoint validates its input.", Priority: definition.PriorityHardConstraint, Selector: layer.Selector{ComponentTags: []string{"backend"}}, Provenance: prov},
		{Key: areaFrontendA11yKey, Convention: areaFrontendSentinel + ": every control has an accessible name.", Priority: definition.PriorityHardConstraint, Selector: layer.Selector{ComponentTags: []string{"frontend"}}, Provenance: prov},
		{Key: areaReviewKey, Convention: areaReviewSentinel + ": walk the checklist.", Priority: definition.PriorityGuidance, Selector: layer.Selector{BlockKinds: []string{"CHECKER"}}, Provenance: prov},
	}}
}

func publishAreaLayer(t *testing.T, f *v5AcceptFixture, doc layer.LayerDocument) {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, f.uow, testCmd("v5a-def-v9-area-layer-def", ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: "v9-area-layer-def", Kind: definition.KindLayer, Scope: definition.GlobalScope(), Name: "layer v9-area-layer-def",
	}); err != nil {
		t.Fatalf("CreateDefinition(layer): %v", err)
	}
	if _, err := definitions.PublishDefinitionVersion(ctx, f.uow, testCmd("v5a-pub-v9-area-layer-v1", ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: "v9-area-layer-def", Kind: definition.KindLayer,
		Compile: func() (definition.VersionFields, error) {
			return layer.Compile(
				layer.LayerDefinition{ID: "v9-area-layer-def", Fields: definition.Fields{
					Kind: definition.KindLayer, Scope: definition.GlobalScope(), Name: "layer", Status: definition.StatusDraft, Version: 1,
				}},
				layer.PublishRequest{
					VersionID: "v9-area-layer-v1", VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(layer): %v", err)
	}
}

func areaLayerRefs(t *testing.T, doc layer.LayerDocument) []policy.ResourceRef {
	t.Helper()
	identities, err := layer.ResourceIdentities("v9-area-layer-v1", doc)
	if err != nil {
		t.Fatalf("layer.ResourceIdentities: %v", err)
	}
	refs := make([]policy.ResourceRef, 0, len(identities))
	for _, identity := range identities {
		refs = append(refs, policy.ResourceRef{
			OwnerVersionID: identity.Identity.OwnerVersionID, ResourceKey: identity.Identity.ResourceKey, ContentHash: identity.Identity.ContentHash,
		})
	}
	return refs
}

// commitAreaDirectories gives the fixture repository two top-level
// directories, as a real backend/frontend repository has. It runs before the
// first WorkItem, which is what provisions the worktree.
func commitAreaDirectories(t *testing.T, f *v5AcceptFixture) {
	t.Helper()
	for _, area := range []string{"backend", "frontend"} {
		dir := filepath.Join(f.repoPath, area)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(area+"\n"), 0o600); err != nil {
			t.Fatalf("write %s README: %v", area, err)
		}
		runV5AcceptGit(t, f.repoPath, "add", "--", area+"/README.md")
	}
	runV5AcceptGit(t, f.repoPath, "commit", "-m", "add the backend and frontend areas")
}

// discoverComponents runs the real repository probe over the fixture
// repository and creates the Components it proposes the way
// repositoryprobe.Handler.finishActive does (one catalog Component per
// candidate, Name/Path/Kind as discovered). It returns their names.
func discoverComponents(t *testing.T, f *v5AcceptFixture) []string {
	t.Helper()
	ctx := context.Background()
	prober, err := repoprobe.New(repoprobe.Config{})
	if err != nil {
		t.Fatalf("repoprobe.New: %v", err)
	}
	evidence, err := prober.Probe(ctx, f.repoPath, "main")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	var names []string
	for _, candidate := range evidence.Components {
		if _, err := catalog.CreateComponent(ctx, f.uow, f.ids, catalog.CreateComponentRequest{
			ProjectID: v5AcceptProjectID, RepositoryID: v5AcceptRepositoryID,
			Name: candidate.Name, Path: candidate.Path, Kind: candidate.Kind,
		}); err != nil {
			t.Fatalf("CreateComponent(%s): %v", candidate.Name, err)
		}
		names = append(names, candidate.Name)
	}
	return names
}

// areaPrompt is the part of the v2 instruction artifact this scenario reads.
type areaPrompt struct {
	SchemaVersion   int `json:"schemaVersion"`
	HardConstraints []struct {
		ResourceKey string `json:"resourceKey"`
		Priority    string `json:"priority"`
	} `json:"hardConstraints"`
	Resources []struct {
		ResourceKey string `json:"resourceKey"`
		Priority    string `json:"priority"`
	} `json:"resources"`
}

// receivedKeys is every resource key the prompt carries, sorted.
func (p areaPrompt) receivedKeys() []string {
	var keys []string
	for _, r := range p.HardConstraints {
		keys = append(keys, r.ResourceKey)
	}
	for _, r := range p.Resources {
		keys = append(keys, r.ResourceKey)
	}
	sort.Strings(keys)
	return keys
}

// areaRunResult is what one area's run produced.
type areaRunResult struct {
	invocation providers.FakeCLIInvocation
	prompt     areaPrompt
	attempt    runtimedomain.ExecutionAttempt
	snapshot   contextsnapshot.Snapshot
	// decisionInput is the persisted CONTEXT_RESOLUTION_V1 input of the
	// implement node run; workItemKind/Risk are the stored WorkItem values the
	// input echoes.
	decisionInput string
	workItemKind  string
	workItemRisk  string
}

func TestV9AcceptAreaKnowledge_OneSharedProfileGetsEachAreasLayer(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")

	commitAreaDirectories(t, f)
	if got := discoverComponents(t, f); strings.Join(got, ",") != "backend,frontend" {
		t.Fatalf("the probe discovered components %v, want backend and frontend", got)
	}

	// One Layer, one context route over all of it, ONE agent profile.
	doc := areaLayerDocument()
	publishAreaLayer(t, f, doc)
	publishPolicyVersion(t, f.uow, "v9-area-context-policy-def", "v9-area-context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context:  &policy.ContextRules{Selector: []string{"v9-area"}, Budget: policy.ContextBudget{MaxTokens: 65536}, ResourceRefs: areaLayerRefs(t, doc)},
	})
	agents := newV9AgentSetup(t, f)
	publishAgentProfileVersion(t, f.uow, "v9-area-agent-profile-def", "v9-area-agent-profile-v1", "v9-area-context-policy-def", "v9-area-context-policy-v1")
	implement := v9AgentNode("implement", agents.buildID, workflow.AgentRoleMaker, "done")
	implement.Agent.ProfileRef = definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "v9-area-agent-profile-def", VersionID: "v9-area-agent-profile-v1"}
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v9-area-workflow-def", "v9-area-workflow-v1", workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			implement,
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-end", From: "implement", Outcome: "done", To: "end"},
		},
	}, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Agent: agents.executor}
	registry := f.registerHandlersWithAgents(router, "v9area", agents.registry)
	// A generous lease: nothing here is about lease expiry, and the executor
	// runs real git commands between heartbeats.
	_, stopPool := f.startPoolWithConfig(t, registry, workerpool.Config{
		Concurrency: 1, Owner: "v9-area", LeaseTTL: 60 * time.Second,
		HeartbeatEvery: time.Second, PollInterval: 10 * time.Millisecond,
		ShutdownGrace: 5 * time.Second, RecoveryInterval: time.Second,
	})
	defer stopPool()

	root := f.createRootWorkItem(t, "v9-area-root", workdomain.RepositoryWrite)

	// runArea creates a child WorkItem scoped to area, runs the shared
	// workflow over it and reads back what its process received and what the
	// scheduler recorded.
	runArea := func(area string) areaRunResult {
		t.Helper()
		capturePath := filepath.Join(f.fixtureRoot, "capture-"+area+".json")
		t.Setenv("AGENTKIT_CAPTURE_PATH", capturePath)
		child := f.createChildWorkItemWithPathScopes(t, root.WorkItemID, "v9-area-"+area, workdomain.RepositoryWrite, []string{area})
		started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, testCmd("v9-area-start-"+area, ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun"), runtime.StartWorkflowRunRequest{
			ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
		})
		if err != nil {
			t.Fatalf("StartWorkflowRun(%s): %v", area, err)
		}
		f.waitForRunState(t, started.RunID, runtimedomain.WorkflowRunVerifying)

		var result areaRunResult
		raw, err := os.ReadFile(capturePath)
		if err != nil {
			t.Fatalf("read the fake CLI capture of %s: %v", area, err)
		}
		if err := json.Unmarshal(raw, &result.invocation); err != nil {
			t.Fatalf("decode the fake CLI capture of %s: %v", area, err)
		}
		if err := json.Unmarshal([]byte(result.invocation.Stdin), &result.prompt); err != nil {
			t.Fatalf("the prompt of %s on stdin is not JSON: %v\n%s", area, err, result.invocation.Stdin)
		}
		if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, started.RunID)
			if err != nil {
				return err
			}
			var implementNodeRun runtimedomain.NodeRun
			for _, nr := range nodeRuns {
				if nr.NodeKey == "implement" {
					implementNodeRun = nr
				}
			}
			attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, started.RunID)
			if err != nil {
				return err
			}
			for _, a := range attempts {
				if a.NodeRunID == implementNodeRun.ID {
					result.attempt = a
				}
			}
			if result.snapshot, err = tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, string(result.attempt.ID)); err != nil {
				return err
			}
			decision, err := tx.Runtime().GetDecisionArtifact(ctx, string(implementNodeRun.ID)+"-context-resolution-v1")
			if err != nil {
				return err
			}
			result.decisionInput = string(decision.Input)
			item, err := tx.Work().GetWorkItem(ctx, child.WorkItemID)
			if err != nil {
				return err
			}
			result.workItemKind, result.workItemRisk = string(item.Kind), string(item.RiskLevel)
			return nil
		}); err != nil {
			t.Fatalf("read the %s run: %v", area, err)
		}
		return result
	}

	backend := runArea("backend")
	frontend := runArea("frontend")

	for _, tc := range []struct {
		area, other string
		got         areaRunResult
		wantKeys    []string
		ownSentinel string
		notSentinel string
	}{
		{"backend", "frontend", backend, []string{areaBackendAPIKey, areaBackendLayoutKey, areaSharedKey}, areaBackendSentinel, areaFrontendSentinel},
		{"frontend", "backend", frontend, []string{areaFrontendA11yKey, areaFrontendLayoutKey, areaSharedKey}, areaFrontendSentinel, areaBackendSentinel},
	} {
		t.Run(tc.area, func(t *testing.T) {
			if tc.got.prompt.SchemaVersion != 2 {
				t.Fatalf("the %s prompt has schema version %d, want 2", tc.area, tc.got.prompt.SchemaVersion)
			}
			// The process received its own area's resources plus the shared
			// one, and nothing else (not the other area's, not the CHECKER's).
			if got := tc.got.prompt.receivedKeys(); strings.Join(got, ",") != strings.Join(tc.wantKeys, ",") {
				t.Fatalf("the %s process received resources %v, want %v", tc.area, got, tc.wantKeys)
			}
			stdin := tc.got.invocation.Stdin
			if !strings.Contains(stdin, tc.ownSentinel) {
				t.Fatalf("the %s prompt does not contain its own area's content (%s)", tc.area, tc.ownSentinel)
			}
			for _, absent := range []string{tc.notSentinel, areaReviewSentinel} {
				if strings.Contains(stdin, absent) {
					t.Fatalf("the %s prompt contains %q, which belongs to another area or to a CHECKER node", tc.area, absent)
				}
			}
			// The HARD_CONSTRAINT of the area is in the hardConstraints
			// section (V9-03), the layout in resources.
			if len(tc.got.prompt.HardConstraints) != 1 || tc.got.prompt.HardConstraints[0].Priority != "HARD_CONSTRAINT" {
				t.Fatalf("the %s prompt hardConstraints = %+v, want exactly the area's HARD_CONSTRAINT", tc.area, tc.got.prompt.HardConstraints)
			}

			// The snapshot pinned exactly those resources, and the decision
			// artifact says what the route was resolved against.
			var pinned []string
			for _, ref := range tc.got.snapshot.ResourceRefs {
				pinned = append(pinned, ref.ResourceKey)
			}
			sort.Strings(pinned)
			if strings.Join(pinned, ",") != strings.Join(tc.wantKeys, ",") {
				t.Fatalf("the %s snapshot pins %v, want %v", tc.area, pinned, tc.wantKeys)
			}
			wantInput := `{"componentTags":["` + tc.area + `"],"pathTags":["` + tc.area + `"],"wholeRepositoryScope":false,"blockKind":"MAKER","taskKind":"` +
				tc.got.workItemKind + `","riskClass":"` + tc.got.workItemRisk + `"}`
			if tc.got.decisionInput != wantInput {
				t.Fatalf("CONTEXT_RESOLUTION_V1 input of %s =\n  %s\nwant\n  %s", tc.area, tc.got.decisionInput, wantInput)
			}
			t.Logf("CONTEXT_RESOLUTION_V1 input of %s: %s", tc.area, tc.got.decisionInput)
		})
	}

	// The point of V9-04: both runs used the SAME agent profile (same pinned
	// execution profile) and the same workflow version, yet received different
	// Layer resources.
	if backend.attempt.ExecutionProfileHash == "" || backend.attempt.ExecutionProfileHash != frontend.attempt.ExecutionProfileHash {
		t.Fatalf("execution profile hashes differ (%q vs %q): the two areas must share one agent profile", backend.attempt.ExecutionProfileHash, frontend.attempt.ExecutionProfileHash)
	}
	if strings.Join(backend.prompt.receivedKeys(), ",") == strings.Join(frontend.prompt.receivedKeys(), ",") {
		t.Fatalf("both areas received the same resources %v", backend.prompt.receivedKeys())
	}
}
