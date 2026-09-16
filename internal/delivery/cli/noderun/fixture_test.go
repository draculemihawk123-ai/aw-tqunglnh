package noderun_test

// This file duplicates internal/delivery/httpapi/run's own fixture_test.go
// helpers (readyWorkItemFixture, publishTestWorkflowVersion, ...) for the
// same reason every sibling package in this codebase duplicates them
// rather than importing across package boundaries (Go test helpers are
// unexported; see internal/delivery/httpapi/rundetail/fixture_test.go's
// own doc comment), PLUS a narrow, hand-seeded admission-BLOCKED NodeRun
// fixture (blockedAdmissionNodeRunFixture) — deliberately NOT the full
// internal/app/runtime's own admission_test.go-style scenario (real
// workflow/agent-profile/policy documents, a real spawned executable
// probed for capabilities, ...): this package's own test only needs to
// prove `aw node-run retry-blocked` correctly dispatches to
// runtime.RetryBlockedActivationHandler.Retry and reports its real,
// already-tested result back out as JSON — not to re-prove admission's own
// four-check logic, which internal/app/runtime/admission_test.go already
// covers exhaustively. Choosing a COMMAND-kind executor profile with no
// AdapterBuildID pin (a legitimate case runAdmissionProbePhase's own
// "COMMAND/MACHINE_GATE nodes have no AdapterBuildVersion concept" branch
// documents) keeps this fixture minimal: no adapter build, no agent
// executor, no agent profile/policy document needed at all — just a real
// isolation tier fake.IsolationEnforcementChecker can satisfy or refuse on
// command, which is exactly the one axis this package's own test cares
// about (does a real revalidation PASS turn into Retried=true, does a real
// revalidation FAIL leave the blocker OPEN).

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func openNodeRunCLITestStore(t *testing.T, name string) (*sqlite.Store, ports.UnitOfWork) {
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

func startTestRun(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, workItemID, workflowVersionID string) runtime.StartWorkflowRunResult {
	t.Helper()
	cmd := ports.Command{
		ID: "cmd-start-" + workItemID, IdempotencyKey: "idem-start-" + workItemID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-" + workItemID,
	}
	result, err := runtime.StartWorkflowRun(context.Background(), uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: projectID, WorkItemID: workItemID, WorkflowVersionID: workflowVersionID,
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	return result
}

// executionProfileFixture is the minimal JSON shape
// internal/app/runtime's own unexported resolvedExecutionProfileView
// decodes (execute.go) — reproduced here field-for-field since that type
// is unexported and this package cannot construct one directly. A COMMAND
// executor with no AdapterBuildID pin needs no adapter build/agent
// registry entry at all (see this file's own top doc comment) — only a
// real, non-empty IsolationTier (runAdmissionProbePhase's own hard
// requirement: "node run %s pins no isolation tier" is a technical error,
// not a business BLOCKED outcome, when this is empty).
type executionProfileFixture struct {
	Executor struct {
		Kind         string `json:"kind"`
		DefinitionID string `json:"definitionId"`
		VersionID    string `json:"versionId"`
		CompiledHash string `json:"compiledHash"`
	} `json:"executor"`
	TimeoutSeconds uint32 `json:"timeoutSeconds"`
	IsolationTier  string `json:"isolationTier"`
}

// blockedAdmissionNodeRunFixture hand-seeds ONE extra NodeRun, its own
// BLOCKED ExecutionAttempt, the durable execution-profile DecisionArtifact
// RetryBlockedActivationHandler.Retry reloads, and the one WorkItemBlocker
// deterministically keyed off that Attempt's own ID
// ("<attemptId>-admission-blocker", retry_blocked_activation.go's own
// loadForRetryTx doc comment) — everything
// RetryBlockedActivationHandler.Retry needs to find and act on a real
// admission-blocked activation, without driving the full admission
// pipeline through a real scheduled AGENT node (this file's own top doc
// comment explains why that is unnecessary for what this package's own
// test means to prove).
func blockedAdmissionNodeRunFixture(
	t *testing.T, uow ports.UnitOfWork, projectID string, run runtime.StartWorkflowRunResult, isolationTier string,
) (nodeRunID string) {
	t.Helper()
	ctx := context.Background()
	nodeRunID = "blocked-node-run-1"
	attemptID := "blocked-attempt-1"

	profile := executionProfileFixture{TimeoutSeconds: 60, IsolationTier: isolationTier}
	profile.Executor.Kind = "COMMAND"
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal execution profile fixture: %v", err)
	}

	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		nodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID(nodeRunID), runtimedomain.WorkflowRunID(run.RunID),
			"command-node", 50, 0, nil, "input-hash", "",
		)
		if err != nil {
			return err
		}
		nodeRun.State = runtimedomain.NodeRunBlocked
		if _, err := tx.Runtime().CreateNodeRun(ctx, nodeRun); err != nil {
			return err
		}

		attempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID(attemptID), runtimedomain.NodeRunID(nodeRunID), 1, "profile-hash-1", "", nil,
		)
		if err != nil {
			return err
		}
		// NewExecutionAttempt's own zero value is QUEUED
		// (runtime.ExecutionAttemptQueued) — CreateExecutionAttempt's own
		// sqlite INSERT has no termination_reason column at all (confirmed
		// by reading schedule_node_run.go's own createExecutionAttemptTx
		// before writing this fixture): TerminationReason is only ever
		// persisted by a SEPARATE TransitionExecutionAttempt UPDATE, the
		// same QUEUED->BLOCKED CAS admission.go's own blockAdmission
		// performs for a real admission failure (that package's own doc
		// comment: "each ending the Attempt at QUEUED->BLOCKED").
		if _, err := tx.Runtime().CreateExecutionAttempt(ctx, attempt); err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: attempt.Version,
			NextState: runtimedomain.ExecutionAttemptBlocked, TerminationReason: runtimedomain.TerminationReasonIsolationEnforcementUnavailable,
		}); err != nil {
			return err
		}

		decision, err := runtimedomain.NewDecisionArtifact(
			runtimedomain.DecisionArtifactID(nodeRunID+"-execution-profile-v1"), project.ProjectID(projectID),
			"EXECUTION_PROFILE_V1", "v1", []byte(`{}`), profileJSON, time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().RecordDecisionArtifact(ctx, decision); err != nil {
			return err
		}

		blocker, err := workdomain.NewWorkItemBlocker(
			workdomain.BlockerID(attemptID+"-admission-blocker"), project.ProjectID(projectID),
			workdomain.WorkItemID(run.WorkItemID), workdomain.BlockerIsolationEnforcementUnavailable,
			run.RunID, nodeRunID, attemptID, "isolation unavailable at schedule time", time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		_, err = tx.Work().CreateWorkItemBlocker(ctx, blocker)
		return err
	})
	if err != nil {
		t.Fatalf("seed blocked admission node run: %v", err)
	}
	return nodeRunID
}
