package run_test

// This file duplicates internal/delivery/httpapi/rundetail's own
// fixture_test.go (itself a duplicate of internal/delivery/httpapi/run's
// own fixture_test.go, itself a duplicate of internal/app/runtime's own
// commands_sqlite_test.go helpers) rather than importing them — Go test
// helpers in a _test.go file are not exported across packages (that file's
// own doc comment). Every fixture helper here drives a REAL *sqlite.Store
// through the REAL application commands (catalog.RegisterRepository,
// work.CreateRootWorkItem, workspaceprovision.Handler,
// workflow.Compile/PublishWorkflowVersion) — never a hand-seeded row and
// never a mock, for exactly the same reason those sibling packages' own
// fixtures do: this package's own commands (runtime.StartWorkflowRun,
// runtime.CancelRun, ...) reload real rows through real ports.Tx
// accessors, so a fixture must produce real rows for their own
// preconditions to genuinely exercise.
//
// internal/delivery/cli's own archtest guarantee
// (internal/archtest/cli_boundary_test.go,
// TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters) checks this
// package's own NON-TEST dependency closure only (`go list -json` without
// -test), so importing sqlite here, in a _test.go file, does not violate
// it — confirmed by reading that archtest file's own doc comment before
// relying on this.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func openRunCLITestStore(t *testing.T, name string) (*sqlite.Store, ports.UnitOfWork) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, sqlite.NewUnitOfWork(store)
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
	return ports.WorkspaceInspection{}, errors.New("stub: Inspect must not be called")
}
func (s *stubProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	return s.revision, nil
}
func (s *stubProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return ports.WorkspaceDiff{}, errors.New("stub: Diff must not be called")
}
func (s *stubProvider) Release(context.Context, ports.WorkspaceHandle) error {
	return errors.New("stub: Release must not be called")
}
func (s *stubProvider) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return "", errors.New("stub: WorkingDirectory must not be called")
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
// internal/delivery/httpapi/run/fixture_test.go's own identical helper.
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

// workflowDocumentV1 is a minimal, always-compilable two-node graph — a
// START and an END joined by one outcome edge.
func workflowDocumentV1() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-end", From: "start", Outcome: "next", To: "end"},
		},
	}
}

// routerChainDocument is start -> router1 -> router2 -> end, each ROUTER
// declaring exactly one outcome — used to drive multiple separate, real
// runtime.AdvanceRun hops (one new NodeRun per call) so a pagination test
// can insert a genuinely new NodeRun into an already-started walk between
// page fetches, mirroring internal/delivery/httpapi/rundetail's own
// identical fixture.
func routerChainDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "router1", Type: workflow.NodeRouter, Outcomes: []string{"next"}},
			{Key: "router2", Type: workflow.NodeRouter, Outcomes: []string{"next"}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-router1", From: "start", Outcome: "next", To: "router1"},
			{Key: "router1-to-router2", From: "router1", Outcome: "next", To: "router2"},
			{Key: "router2-to-end", From: "router2", Outcome: "next", To: "end"},
		},
	}
}

// seedBlockedNodeRun hand-seeds ONE extra, BLOCKED NodeRun with a
// BlockReason exactly matching a known secret — test setup for this
// package's own narrow BlockReason-redaction assertion, mirroring
// internal/delivery/httpapi/rundetail's own identical
// seedBlockedNodeRun/TestGetRunGraph_RedactsBlockReason precedent.
func seedBlockedNodeRun(ctx context.Context, uow ports.UnitOfWork, runID, nodeKey, blockReason string) error {
	return uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		nodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID(nodeKey+"-run"), runtimedomain.WorkflowRunID(runID),
			nodeKey, 99, 0, nil, "input-hash", "",
		)
		if err != nil {
			return err
		}
		nodeRun.State = runtimedomain.NodeRunBlocked
		nodeRun.BlockReason = blockReason
		_, err = tx.Runtime().CreateNodeRun(ctx, nodeRun)
		return err
	})
}

// forceRunSucceeded transitions runID straight to SUCCEEDED via the raw
// repository CAS, bypassing the full ADR-021 completion-policy/evidence/
// approval-gate pipeline (runtime.EvaluateCompletionCandidate) entirely —
// a test-only shortcut for a test that means to exercise `run start
// --wait`'s own polling mechanics, not the (separately, already heavily
// tested elsewhere in this codebase) completion-policy machinery itself.
// Mirrors this file's own seedBlockedNodeRun: a direct, narrowly-scoped
// repository seed for one targeted state, not a hand-rolled second
// implementation of a real command.
func forceRunSucceeded(uow ports.UnitOfWork, runID string) error {
	ctx := context.Background()
	return uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
			RunID: runID, ExpectedState: run.State, ExpectedVersion: run.Version,
			NextState: runtimedomain.WorkflowRunSucceeded,
		})
		return err
	})
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
