package decision_test

// This file duplicates internal/delivery/cli/run's own fixture_test.go
// idiom (itself a duplicate of internal/app/runtime's own approval_test.go/
// wait_test.go helpers) rather than importing them — Go test helpers in a
// _test.go file are not exported across packages. Every fixture helper
// here drives a REAL *sqlite.Store through the REAL application commands
// (catalog.RegisterRepository, work.CreateRootWorkItem,
// workspaceprovision.Handler, workflow.Compile/PublishWorkflowVersion,
// runtime.StartWorkflowRun/AdvanceRun) — never a hand-seeded row and never
// a mock — and uses real sqlite rather than fake.UnitOfWork specifically
// because this package's own concurrent-decision/signal tests need real
// cross-goroutine transaction serialization (internal/adapters/sqlite's
// own _txlock=immediate connections) — fake.UnitOfWork's own
// WithSerializedWrite rejects a genuinely concurrent second caller outright
// (ErrNestedTransaction) rather than blocking and serializing it, so it
// cannot stand in for that here.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/decision"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func openDecisionCLITestStore(t *testing.T, name string) (*sqlite.Store, ports.UnitOfWork) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, sqlite.NewUnitOfWork(store)
}

func newTestDeps(uow ports.UnitOfWork) decision.Dependencies {
	fixedNow := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	return decision.Dependencies{
		UOW: uow, IDs: idsource.NewSequential("id"),
		Now: func() time.Time { return fixedNow },
	}
}

func seedProject(t *testing.T, uow ports.UnitOfWork, projectID string) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", projectID, err)
	}
}

func seedActiveRepository(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
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
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
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
	})
	if err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}
}

type stubProvider struct {
	handle   ports.WorkspaceHandle
	revision workspace.Revision
}

func (s *stubProvider) Provision(context.Context, ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	return s.handle, nil
}
func (s *stubProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, nil
}
func (s *stubProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	return s.revision, nil
}
func (s *stubProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return ports.WorkspaceDiff{}, nil
}
func (s *stubProvider) Release(context.Context, ports.WorkspaceHandle) error { return nil }
func (s *stubProvider) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return "", nil
}

func mustHandle(t *testing.T, token string) ports.WorkspaceHandle {
	t.Helper()
	h, err := ports.NewWorkspaceHandle(token)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle(%s): %v", token, err)
	}
	return h
}

func provisionJob(id, workItemID, projectID, familyID, workspaceSetID, repositoryID string) ports.DurableJob {
	payload := []byte(`{"workItemId":"` + workItemID + `","projectId":"` + projectID +
		`","familyId":"` + familyID + `","workspaceSetId":"` + workspaceSetID + `","repositoryId":"` + repositoryID + `"}`)
	return ports.DurableJob{ID: ports.JobID(id), Kind: work.WorkspaceProvisionJobKind, Payload: payload}
}

// readyWorkItemFixture creates one ACTIVE repository, a root WorkItem over
// it, drives its sole WorkspaceSet all the way to READY, then forces the
// WorkItem itself BACKLOG->READY — mirrors
// internal/delivery/cli/run/fixture_test.go's own identical helper.
func readyWorkItemFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) work.CreateRootWorkItemResult {
	t.Helper()
	seedProject(t, uow, projectID)
	ctx := context.Background()
	seedActiveRepository(t, uow, ids, projectID, repositoryID)

	cmd := ports.Command{
		ID: "cmd-root-" + repositoryID, IdempotencyKey: "idem-root-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-" + repositoryID,
	}
	root, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, work.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Implement the thing",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"**"}, Reason: "root task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}

	provider := &stubProvider{
		handle:   mustHandle(t, "handle-"+repositoryID),
		revision: workspace.Revision{RepositoryID: project.RepositoryID(repositoryID), VCSObjectID: "cafebabecafebabecafebabecafebabecafebabe", WorkspaceGeneration: 1},
	}
	handler := workspaceprovision.New(uow, ids, provider)
	if err := handler.Handle(ctx, provisionJob(
		root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, projectID, root.FamilyID, root.WorkspaceSetID, repositoryID,
	)); err != nil {
		t.Fatalf("workspaceprovision.Handle: %v", err)
	}

	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: root.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: 1,
			NextStatus: workdomain.WorkItemReady,
		})
		return err
	})
	if err != nil {
		t.Fatalf("force work item %s READY: %v", root.WorkItemID, err)
	}
	return root
}

func publishTestWorkflowVersion(t *testing.T, uow ports.UnitOfWork, projectID, definitionID, versionID string, document workflow.WorkflowDocument) workflow.WorkflowVersion {
	t.Helper()
	pid := project.ProjectID(projectID)
	definition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(definitionID), ProjectID: &pid,
		Name: "workflow " + definitionID, Status: workflow.DefinitionActive, Version: 1,
	}
	candidate, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1, Document: document,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "1", Hash: "sha256:dependency-1"},
		}},
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow %s: %v", versionID, err)
	}
	var published workflow.WorkflowVersion
	ctx := context.Background()
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		p, err := tx.Definitions().PublishWorkflowVersion(ctx, definition, candidate)
		published = p
		return err
	})
	if err != nil {
		t.Fatalf("publish workflow version %s: %v", versionID, err)
	}
	return published
}

// approvalDocument is start -> gate(APPROVAL) -> end_approved|end_rejected|
// end_escalated — mirrors internal/app/runtime/approval_test.go's own
// identical fixture document exactly.
func approvalDocument(timeoutSeconds uint32) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "gate", Type: workflow.NodeApproval, Outcomes: []string{"approved", "rejected", "escalated"}, Approval: &workflow.ApprovalNodeConfig{
				AuthorizedRoles: []string{"reviewer"}, TimeoutSeconds: timeoutSeconds, EscalationOutcome: "escalated",
				RequestedEvidenceKinds: []string{"test-plan"},
			}},
			{Key: "end_approved", Type: workflow.NodeEnd},
			{Key: "end_rejected", Type: workflow.NodeEnd},
			{Key: "end_escalated", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-gate", From: "start", Outcome: "next", To: "gate"},
			{Key: "gate-to-approved", From: "gate", Outcome: "approved", To: "end_approved"},
			{Key: "gate-to-rejected", From: "gate", Outcome: "rejected", To: "end_rejected"},
			{Key: "gate-to-escalated", From: "gate", Outcome: "escalated", To: "end_escalated"},
		},
	}
}

// waitSignalDocument is start -> pause(WAIT, SIGNAL) -> end_resumed|
// end_expired — mirrors internal/app/runtime/wait_test.go's own identical
// fixture document exactly.
func waitSignalDocument(timeoutSeconds uint32) workflow.WorkflowDocument {
	waitCfg := &workflow.WaitNodeConfig{
		Mode: workflow.WaitModeSignal, SignalName: "ci-passed", TimeoutSeconds: timeoutSeconds,
		CompletionOutcome: "resumed",
	}
	outcomes := []string{"resumed", "expired"}
	if timeoutSeconds > 0 {
		waitCfg.TimeoutOutcome = "expired"
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "pause", Type: workflow.NodeWait, Outcomes: outcomes, Wait: waitCfg},
			{Key: "end_resumed", Type: workflow.NodeEnd},
			{Key: "end_expired", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-pause", From: "start", Outcome: "next", To: "pause"},
			{Key: "pause-to-end-resumed", From: "pause", Outcome: "resumed", To: "end_resumed"},
			{Key: "pause-to-end-expired", From: "pause", Outcome: "expired", To: "end_expired"},
		},
	}
}

// approvalFixture starts a run over approvalDocument(timeoutSeconds) and
// advances it exactly one hop (start -> gate) — mirrors
// internal/app/runtime/approval_test.go's own identical fixture, adapted
// for a real sqlite.Store.
func approvalFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, timeoutSeconds uint32) (runID string, hop runtime.AdvanceRunResult) {
	t.Helper()
	ctx := context.Background()
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", approvalDocument(timeoutSeconds))

	startCmd := ports.Command{
		ID: "cmd-start-1", IdempotencyKey: "idem-start-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-1",
	}
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	result, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->gate): %v", err)
	}
	if result.NextApprovalRequestID == "" {
		t.Fatalf("hop = %+v, want a minted NextApprovalRequestID", result)
	}
	return startResult.RunID, result
}

// waitFixture starts a run over waitSignalDocument(timeoutSeconds) and
// advances it exactly one hop (start -> pause) — mirrors
// internal/app/runtime/wait_test.go's own identical fixture, adapted for a
// real sqlite.Store.
func waitFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, timeoutSeconds uint32) (runID string, hop runtime.AdvanceRunResult) {
	t.Helper()
	ctx := context.Background()
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", waitSignalDocument(timeoutSeconds))

	startCmd := ports.Command{
		ID: "cmd-start-1", IdempotencyKey: "idem-start-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-1",
	}
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	result, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->pause): %v", err)
	}
	if result.NextWaitRegistrationID == "" {
		t.Fatalf("hop = %+v, want a minted NextWaitRegistrationID", result)
	}
	return startResult.RunID, result
}

// writePrincipalFile writes a minimal trusted local-principal config file
// (config.LoadLocalPrincipalFile's own wire shape) to a temp file and
// returns its path — this package's own shared --principal-config fixture.
func writePrincipalFile(t *testing.T, actor string, roles []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "principal.json")
	rolesJSON := `[]`
	if len(roles) > 0 {
		rolesJSON = `["` + roles[0] + `"`
		for _, r := range roles[1:] {
			rolesJSON += `,"` + r + `"`
		}
		rolesJSON += `]`
	}
	content := `{"localPrincipal": {"actor": "` + actor + `", "roles": ` + rolesJSON + `}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write principal file: %v", err)
	}
	return path
}
