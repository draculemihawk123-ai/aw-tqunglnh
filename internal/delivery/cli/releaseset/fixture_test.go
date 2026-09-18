package releaseset_test

// This file duplicates internal/delivery/cli/workitem's own fixture_test.go
// helpers (mustCreateProject, mustCreateActiveRepository, stubProvider,
// readyWorkItemFixture, ...) rather than importing them — Go test helpers in
// a _test.go file are not exported across packages (that file's own doc
// comment). Every fixture helper here drives a REAL (in-memory)
// fake.UnitOfWork through the REAL application commands
// (catalog.RegisterRepository, work.CreateRootWorkItem,
// workspaceprovision.Handler, work.CreateReleaseSet,
// releasesetcommit.RequestReleaseSetLocalCommit) — never a hand-seeded row
// and never a mock, for the same reason those sibling packages' own
// fixtures do: this package's own commands reload real rows through real
// ports.Tx accessors, so a fixture must produce real rows for their own
// preconditions to genuinely exercise. fake.UnitOfWork (not sqlite) is used
// throughout: this package's own tests need no real durable-job WORKER
// sweep (the "crash-after-Git" Verify bullet is instead proven by directly
// forcing the ReleaseSetLocalCommit's own terminal row via
// tx.Work().TransitionReleaseSetLocalCommitToFailed/ToCommitted — the same
// "hand-seed a narrow, targeted state via a direct repository call"
// convention internal/delivery/cli/run/start_test.go's own
// forceRunSucceeded already establishes for the identical "prove --wait
// observes a state that already exists, without a real worker" concern).

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	cliReleaseSet "github.com/taQuangLing/agent-workflow/internal/delivery/cli/releaseset"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

var fixedNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

// newTestDeps builds a fresh in-memory cliReleaseSet.Dependencies for one
// test — a fake.UnitOfWork, a deterministic idsource.Sequential and a fixed
// Now, mirroring internal/delivery/cli/workitem/fixture_test.go's own
// newTestDeps. Every test gets its own isolated UnitOfWork.
func newTestDeps(t *testing.T) cliReleaseSet.Dependencies {
	t.Helper()
	return cliReleaseSet.Dependencies{
		UoW: fake.New(),
		IDs: idsource.NewSequential("id"),
		Now: func() time.Time { return fixedNow },
	}
}

func testCommand(idempotencyKey, requestHash string, scope ports.CommandScope, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: scope, RequestedAt: fixedNow,
		Type: commandType, RequestHash: requestHash,
	}
}

func mustCreateProject(t *testing.T, uow ports.UnitOfWork, id string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

func mustCreateActiveRepository(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := testCommand("idem-repo-"+repositoryID, "hash-repo-"+repositoryID, ports.ProjectScope(projectID), "RegisterRepository")
	if _, err := appcatalog.RegisterRepository(ctx, uow, ids, regCmd, appcatalog.RegisterRepositoryRequest{
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

func rootWorkItemFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) workapp.CreateRootWorkItemResult {
	t.Helper()
	mustCreateProject(t, uow, projectID)
	mustCreateActiveRepository(t, uow, ids, projectID, repositoryID)
	ctx := context.Background()

	cmd := testCommand("idem-root-"+repositoryID, "hash-root-"+repositoryID, ports.ProjectScope(projectID), "CreateRootWorkItem")
	root, err := workapp.CreateRootWorkItem(ctx, uow, ids, cmd, workapp.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Implement the thing",
		InitialScope: []workapp.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"**"}, Reason: "root task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	return root
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
	return ports.DurableJob{ID: ports.JobID(id), Kind: workapp.WorkspaceProvisionJobKind, Payload: payload}
}

// readyWorkItemFixture creates one ACTIVE repository, a root WorkItem over
// it, and drives its sole WorkspaceSet all the way to READY — mirrors
// internal/delivery/cli/run/fixture_test.go's own identical helper.
func readyWorkItemFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) workapp.CreateRootWorkItemResult {
	t.Helper()
	root := rootWorkItemFixture(t, uow, ids, projectID, repositoryID)
	ctx := context.Background()

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
	return root
}

// repositoryWorkspaceFixture returns the READY RepositoryWorkspace row
// readyWorkItemFixture provisioned for repositoryID inside workspaceSetID —
// the RepositoryWorkspaceID/Version/Generation `local-commit` tests need,
// obtained the same "read the real row back, never fabricate an ID" way
// every other fixture helper in this codebase already insists on.
func repositoryWorkspaceFixture(t *testing.T, uow ports.UnitOfWork, workspaceSetID, repositoryID string) workspace.RepositoryWorkspace {
	t.Helper()
	var found workspace.RepositoryWorkspace
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		list, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(context.Background(), workspaceSetID)
		if err != nil {
			return err
		}
		for _, rw := range list {
			if string(rw.RepositoryID) == repositoryID {
				found = rw
				return nil
			}
		}
		return errors.New("repository workspace not found for " + repositoryID)
	})
	if err != nil {
		t.Fatalf("repositoryWorkspaceFixture(%s): %v", repositoryID, err)
	}
	return found
}

// releaseSetFixture builds a fully-provisioned READY RepositoryWorkspace
// plus a real, CREATED ReleaseSet naming it — the shared starting point
// every show/seal/abandon/local-commit test builds on. Returns the project/
// family/repository-workspace identities and the fresh ReleaseSetResult.
func releaseSetFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) (workapp.CreateRootWorkItemResult, workspace.RepositoryWorkspace, workapp.ReleaseSetResult) {
	t.Helper()
	root := readyWorkItemFixture(t, uow, ids, projectID, repositoryID)
	rw := repositoryWorkspaceFixture(t, uow, root.WorkspaceSetID, repositoryID)

	ctx := context.Background()
	cmd := testCommand("idem-release-set-"+repositoryID, "hash-release-set-"+repositoryID, ports.ProjectScope(projectID), "CreateReleaseSet")
	result, err := workapp.CreateReleaseSet(ctx, uow, ids, cmd, workapp.CreateReleaseSetRequest{
		ProjectID: projectID, FamilyID: root.FamilyID,
		Repositories: []workapp.RepositoryReleaseRequest{{
			RepositoryID: repositoryID, BaseVCSObjectID: "base-1", ResultVCSObjectID: "result-1", Verdict: string(gate.VerdictPass),
		}},
	})
	if err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}
	return root, rw, result
}

// requestLocalCommitFixture drives a real
// releasesetcommit.RequestReleaseSetLocalCommit call directly (not through
// the CLI leaf) — used by tests that need an already-REQUESTED
// ReleaseSetLocalCommit row to force to a terminal state.
func requestLocalCommitFixture(
	t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID string, rs workapp.ReleaseSetResult, rw workspace.RepositoryWorkspace, idemKey string,
) releasesetcommit.RequestReleaseSetLocalCommitResult {
	t.Helper()
	cmd := testCommand(idemKey, "hash-"+idemKey, ports.ProjectScope(projectID), "RequestReleaseSetLocalCommit")
	result, err := releasesetcommit.RequestReleaseSetLocalCommit(context.Background(), uow, ids, cmd, releasesetcommit.RequestReleaseSetLocalCommitRequest{
		ProjectID: projectID, ReleaseSetID: rs.ReleaseSetID, ExpectedReleaseSetVersion: rs.Version,
		RepositoryWorkspaceID: string(rw.ID), ExpectedWorkspaceVersion: rw.Version,
		Message: "release commit", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
	})
	if err != nil {
		t.Fatalf("RequestReleaseSetLocalCommit: %v", err)
	}
	return result
}

// forceLocalCommitFailed directly transitions localCommitID (currently
// REQUESTED@1) to FAILED — the "hand-seed a narrow, targeted state via a
// direct repository call" convention
// internal/delivery/cli/run/start_test.go's own forceRunSucceeded already
// establishes, applied here so localcommit_test.go's own --wait test can
// prove the CLI leaf's polling observes a real FAILED outcome without
// needing a real worker/sqlite/gitworktree stack at all.
func forceLocalCommitFailed(uow ports.UnitOfWork, localCommitID string, reason workdomain.ReleaseSetLocalCommitFailureReason) error {
	return uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Work().TransitionReleaseSetLocalCommitToFailed(context.Background(), ports.TransitionReleaseSetLocalCommitToFailedRequest{
			ReleaseSetLocalCommitID: localCommitID, ExpectedVersion: 1, FailureReason: reason, OccurredAt: fixedNow,
		})
		return err
	})
}

// forceLocalCommitCommitted mirrors forceLocalCommitFailed for the
// COMMITTED terminal outcome.
func forceLocalCommitCommitted(uow ports.UnitOfWork, localCommitID string) error {
	return uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Work().TransitionReleaseSetLocalCommitToCommitted(context.Background(), ports.TransitionReleaseSetLocalCommitToCommittedRequest{
			ReleaseSetLocalCommitID: localCommitID, ExpectedVersion: 1,
			ParentVCSObjectID: "base-1", ResultVCSObjectID: "committed-1", OccurredAt: fixedNow,
		})
		return err
	})
}

// resultField extracts the top-level "result" object from a
// cli.ResultEnvelope-shaped JSON document — mirrors
// internal/delivery/cli/workitem/fixture_test.go's own identical helper.
func resultField(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return string(envelope.Result)
}

func replayedField(t *testing.T, body string) bool {
	t.Helper()
	var envelope struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return envelope.Replayed
}

func idempotencyKeyField(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return envelope.IdempotencyKey
}
