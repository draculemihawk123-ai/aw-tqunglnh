package workspaceprovision_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// stubProvider is a scriptable ports.WorkspaceProvider test double: no real
// git/filesystem I/O, just fixed Provision/CaptureRevision outcomes and
// call counters, so a handler test can assert exactly when (and how many
// times) each provider call actually ran — mirroring
// internal/app/repositoryprobe's own stubProber.
type stubProvider struct {
	provisionErr   error
	handle         ports.WorkspaceHandle
	captureErr     error
	revision       workspace.Revision
	provisionCalls int
	captureCalls   int
}

func (s *stubProvider) Provision(context.Context, ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	s.provisionCalls++
	if s.provisionErr != nil {
		return ports.WorkspaceHandle{}, s.provisionErr
	}
	return s.handle, nil
}

func (s *stubProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, errors.New("stub: Inspect must not be called by this handler")
}

func (s *stubProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	s.captureCalls++
	if s.captureErr != nil {
		return workspace.Revision{}, s.captureErr
	}
	return s.revision, nil
}

func (s *stubProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return ports.WorkspaceDiff{}, errors.New("stub: Diff must not be called by this handler")
}

func (s *stubProvider) Release(context.Context, ports.WorkspaceHandle) error {
	return errors.New("stub: Release must not be called by this handler")
}

func (s *stubProvider) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return "", errors.New("stub: WorkingDirectory must not be called by this handler")
}

func mustHandle(token string) ports.WorkspaceHandle {
	h, err := ports.NewWorkspaceHandle(token)
	if err != nil {
		panic(err)
	}
	return h
}

// mustSeedActiveRepository registers repositoryID under projectID and
// drives it all the way to ACTIVE (bypassing the real V3-02 probe worker,
// out of this package's own scope) so
// internal/app/work.CreateRootWorkItem's own same-project/ACTIVE-repository
// validation passes.
func mustSeedActiveRepository(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) project.Repository {
	t.Helper()
	ctx := context.Background()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	cmd := ports.Command{
		ID: "cmd-register-" + repositoryID, IdempotencyKey: "idem-register-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), Type: "RegisterRepository",
		RequestHash: "hash-register-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc-" + repositoryID,
		RemoteLocator: "/fixture/" + repositoryID, DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}

	var active project.Repository
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		probing, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		})
		if err != nil {
			return err
		}
		active, err = tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: probing.Version,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("drive repository %s to ACTIVE: %v", repositoryID, err)
	}
	return active
}

// mustCreateRootWorkItem grants initial scope over every repositoryID via
// internal/app/work.CreateRootWorkItem — the real command that enqueues one
// WORKSPACE_PROVISION job per repository this test's own handler.Handle
// call then processes, so the job payload shape a test builds always
// matches production exactly.
func mustCreateRootWorkItem(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID string, repositoryIDs ...string) appwork.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	grants := make([]appwork.ScopeGrantRequest, len(repositoryIDs))
	for i, repositoryID := range repositoryIDs {
		grants[i] = appwork.ScopeGrantRequest{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"**"}, Reason: "root task",
		}
	}
	cmd := ports.Command{
		ID: "cmd-root-1", IdempotencyKey: "idem-root-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope(projectID), Type: "CreateRootWorkItem", RequestHash: "hash-root-1",
		RequestedAt: time.Now().UTC(),
	}
	result, err := appwork.CreateRootWorkItem(ctx, uow, ids, cmd, appwork.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Root task", InitialScope: grants,
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	return result
}

func provisionJob(id, workItemID, projectID, familyID, workspaceSetID, repositoryID string) ports.DurableJob {
	payload, _ := json.Marshal(struct {
		WorkItemID     string `json:"workItemId"`
		ProjectID      string `json:"projectId"`
		FamilyID       string `json:"familyId"`
		WorkspaceSetID string `json:"workspaceSetId"`
		RepositoryID   string `json:"repositoryId"`
	}{
		WorkItemID: workItemID, ProjectID: projectID, FamilyID: familyID,
		WorkspaceSetID: workspaceSetID, RepositoryID: repositoryID,
	})
	return ports.DurableJob{ID: ports.JobID(id), Kind: appwork.WorkspaceProvisionJobKind, Payload: payload}
}

func TestHandle_SoleRepository_ReadyProvisionsBothRepositoryAndSetToReady(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1")

	provider := &stubProvider{
		handle:   mustHandle("handle-repo-1"),
		revision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: "cafebabecafebabecafebabecafebabecafebabe", WorkspaceGeneration: 1},
	}
	handler := workspaceprovision.New(uow, ids, provider)

	job := provisionJob(root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-1")
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if provider.provisionCalls != 1 || provider.captureCalls != 1 {
		t.Fatalf("provisionCalls=%d captureCalls=%d, want 1 and 1", provider.provisionCalls, provider.captureCalls)
	}

	rw, err := uow.Snapshot.Work().GetRepositoryWorkspace(ctx, root.WorkspaceSetID, "repo-1", 1)
	if err != nil {
		t.Fatalf("GetRepositoryWorkspace: %v", err)
	}
	if rw.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("RepositoryWorkspace.State = %q, want READY", rw.State)
	}
	if rw.Locator != "handle-repo-1" || rw.BaseRevision != "cafebabecafebabecafebabecafebabecafebabe" {
		t.Fatalf("rw = %+v, want Locator=handle-repo-1 BaseRevision=cafebabe...", rw)
	}
	if rw.LastProvisionErrorCode != nil {
		t.Fatalf("LastProvisionErrorCode = %v, want nil", rw.LastProvisionErrorCode)
	}

	set, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	if set.State != workspace.WorkspaceSetReady {
		t.Fatalf("WorkspaceSet.State = %q, want READY", set.State)
	}
	if set.Version != 3 { // 1 (REQUESTED) -> 2 (PROVISIONING, begin step) -> 3 (READY, finish step)
		t.Fatalf("WorkspaceSet.Version = %d, want 3", set.Version)
	}
	if set.BaseRevisionSet == nil {
		t.Fatal("WorkspaceSet.BaseRevisionSet is nil, want a computed base revision set")
	}
	revision, ok := set.BaseRevisionSet.RevisionFor("repo-1")
	if !ok || revision.VCSObjectID != "cafebabecafebabecafebabecafebabecafebabe" {
		t.Fatalf("BaseRevisionSet.RevisionFor(repo-1) = %+v, %v, want the captured commit", revision, ok)
	}
}

func TestHandle_ProviderFailure_RecordsFailedAndBlocksSet(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1")

	provider := &stubProvider{provisionErr: errors.New("boom: local path unreachable")}
	handler := workspaceprovision.New(uow, ids, provider)

	job := provisionJob(root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-1")
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v (a business/environment provisioning failure must still be a job SUCCESS)", err)
	}
	if provider.captureCalls != 0 {
		t.Fatalf("captureCalls = %d, want 0 (CaptureRevision must never run after Provision itself failed)", provider.captureCalls)
	}

	rw, err := uow.Snapshot.Work().GetRepositoryWorkspace(ctx, root.WorkspaceSetID, "repo-1", 1)
	if err != nil {
		t.Fatalf("GetRepositoryWorkspace: %v", err)
	}
	if rw.State != workspace.RepositoryWorkspaceFailed {
		t.Fatalf("RepositoryWorkspace.State = %q, want FAILED", rw.State)
	}
	if rw.LastProvisionErrorCode == nil || *rw.LastProvisionErrorCode != "PROVISION_FAILED" {
		t.Fatalf("LastProvisionErrorCode = %v, want PROVISION_FAILED", rw.LastProvisionErrorCode)
	}

	set, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	if set.State != workspace.WorkspaceSetBlocked {
		t.Fatalf("WorkspaceSet.State = %q, want BLOCKED", set.State)
	}
	if set.BaseRevisionSet != nil {
		t.Fatalf("BaseRevisionSet = %+v, want nil (a blocked set must never get a base revision set)", set.BaseRevisionSet)
	}
}

func TestHandle_RepositoryNoLongerActive_RecordsFailedWithoutCallingProvider(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1")

	// Simulate the repository being disabled in the window between
	// CreateRootWorkItem's own ACTIVE-repository validation and this job
	// actually running.
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryActive, ExpectedVersion: 3,
			NextStatus: project.RepositoryDisabled,
		})
		return err
	}); err != nil {
		t.Fatalf("disable repository: %v", err)
	}

	provider := &stubProvider{}
	handler := workspaceprovision.New(uow, ids, provider)
	job := provisionJob(root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-1")
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if provider.provisionCalls != 0 {
		t.Fatalf("provisionCalls = %d, want 0 (no real I/O against a no-longer-ACTIVE repository)", provider.provisionCalls)
	}

	rw, err := uow.Snapshot.Work().GetRepositoryWorkspace(ctx, root.WorkspaceSetID, "repo-1", 1)
	if err != nil {
		t.Fatalf("GetRepositoryWorkspace: %v", err)
	}
	if rw.State != workspace.RepositoryWorkspaceFailed {
		t.Fatalf("RepositoryWorkspace.State = %q, want FAILED", rw.State)
	}
	if rw.LastProvisionErrorCode == nil || *rw.LastProvisionErrorCode != "REPOSITORY_NOT_ACTIVE" {
		t.Fatalf("LastProvisionErrorCode = %v, want REPOSITORY_NOT_ACTIVE", rw.LastProvisionErrorCode)
	}
}

// TestHandle_TwoRepositories_SetReachesReadyOnlyAfterBoth is this task's
// own "Hoàn thành khi: family chỉ ready khi mọi required repository ready"
// bar, proven directly: after only the first of two required repositories'
// own jobs runs, the WorkspaceSet must still be PROVISIONING (never READY);
// only after the second one also runs does it reach READY, with a base
// RevisionSet naming both repositories.
func TestHandle_TwoRepositories_SetReachesReadyOnlyAfterBoth(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-2")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1", "repo-2")

	jobFor := func(repositoryID string) ports.DurableJob {
		for _, p := range root.ProvisionedRepositories {
			if p.RepositoryID == repositoryID {
				return provisionJob(p.ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, repositoryID)
			}
		}
		t.Fatalf("no provisioned repository entry for %s", repositoryID)
		return ports.DurableJob{}
	}

	provider1 := &stubProvider{handle: mustHandle("handle-repo-1"), revision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: "1111111111111111111111111111111111111a", WorkspaceGeneration: 1}}
	handler1 := workspaceprovision.New(uow, ids, provider1)
	if err := handler1.Handle(ctx, jobFor("repo-1")); err != nil {
		t.Fatalf("Handle(repo-1): %v", err)
	}

	set, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID after repo-1: %v", err)
	}
	if set.State != workspace.WorkspaceSetProvisioning {
		t.Fatalf("WorkspaceSet.State after only repo-1 ready = %q, want PROVISIONING (must never be READY with a still-provisioning sibling)", set.State)
	}

	provider2 := &stubProvider{handle: mustHandle("handle-repo-2"), revision: workspace.Revision{RepositoryID: "repo-2", VCSObjectID: "2222222222222222222222222222222222222b", WorkspaceGeneration: 1}}
	handler2 := workspaceprovision.New(uow, ids, provider2)
	if err := handler2.Handle(ctx, jobFor("repo-2")); err != nil {
		t.Fatalf("Handle(repo-2): %v", err)
	}

	set, err = uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID after repo-2: %v", err)
	}
	if set.State != workspace.WorkspaceSetReady {
		t.Fatalf("WorkspaceSet.State after both ready = %q, want READY", set.State)
	}
	if set.BaseRevisionSet == nil {
		t.Fatal("BaseRevisionSet is nil, want both repositories' revisions")
	}
	for repositoryID, want := range map[project.RepositoryID]string{
		"repo-1": "1111111111111111111111111111111111111a",
		"repo-2": "2222222222222222222222222222222222222b",
	} {
		got, ok := set.BaseRevisionSet.RevisionFor(repositoryID)
		if !ok || got.VCSObjectID != want {
			t.Fatalf("BaseRevisionSet.RevisionFor(%s) = %+v, %v, want VCSObjectID=%s", repositoryID, got, ok, want)
		}
	}
}

// TestHandle_TwoRepositories_OneFailsOneSucceeds_SetBlockedButSuccessRowKept
// is "failure partial state" made concrete (this task's own explicit
// Verify-line requirement): the successful repository's own
// RepositoryWorkspace row must survive its sibling's failure, never
// discarded, even though the WorkspaceSet itself ends BLOCKED, not READY.
func TestHandle_TwoRepositories_OneFailsOneSucceeds_SetBlockedButSuccessRowKept(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-2")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1", "repo-2")

	jobFor := func(repositoryID string) ports.DurableJob {
		for _, p := range root.ProvisionedRepositories {
			if p.RepositoryID == repositoryID {
				return provisionJob(p.ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, repositoryID)
			}
		}
		t.Fatalf("no provisioned repository entry for %s", repositoryID)
		return ports.DurableJob{}
	}

	okProvider := &stubProvider{handle: mustHandle("handle-repo-1"), revision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: "1111111111111111111111111111111111111a", WorkspaceGeneration: 1}}
	if err := workspaceprovision.New(uow, ids, okProvider).Handle(ctx, jobFor("repo-1")); err != nil {
		t.Fatalf("Handle(repo-1): %v", err)
	}
	failProvider := &stubProvider{provisionErr: errors.New("boom")}
	if err := workspaceprovision.New(uow, ids, failProvider).Handle(ctx, jobFor("repo-2")); err != nil {
		t.Fatalf("Handle(repo-2): %v", err)
	}

	set, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	if set.State != workspace.WorkspaceSetBlocked {
		t.Fatalf("WorkspaceSet.State = %q, want BLOCKED", set.State)
	}

	successRow, err := uow.Snapshot.Work().GetRepositoryWorkspace(ctx, root.WorkspaceSetID, "repo-1", 1)
	if err != nil {
		t.Fatalf("GetRepositoryWorkspace(repo-1): %v (the successful repository's own row must not be discarded just because repo-2 failed)", err)
	}
	if successRow.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("repo-1 RepositoryWorkspace.State = %q, want READY (kept, unaffected by repo-2's own failure)", successRow.State)
	}

	failedRow, err := uow.Snapshot.Work().GetRepositoryWorkspace(ctx, root.WorkspaceSetID, "repo-2", 1)
	if err != nil {
		t.Fatalf("GetRepositoryWorkspace(repo-2): %v", err)
	}
	if failedRow.State != workspace.RepositoryWorkspaceFailed {
		t.Fatalf("repo-2 RepositoryWorkspace.State = %q, want FAILED", failedRow.State)
	}
}

// TestHandle_OrderReversed_SameOutcome proves the partial-failure outcome
// above does not depend on processing order: repo-2 (the one that fails)
// processed first must reach the identical BLOCKED-set/kept-READY-row
// outcome as repo-1-first above.
func TestHandle_OrderReversed_SameOutcome(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-2")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1", "repo-2")

	jobFor := func(repositoryID string) ports.DurableJob {
		for _, p := range root.ProvisionedRepositories {
			if p.RepositoryID == repositoryID {
				return provisionJob(p.ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, repositoryID)
			}
		}
		t.Fatalf("no provisioned repository entry for %s", repositoryID)
		return ports.DurableJob{}
	}

	failProvider := &stubProvider{provisionErr: errors.New("boom")}
	if err := workspaceprovision.New(uow, ids, failProvider).Handle(ctx, jobFor("repo-2")); err != nil {
		t.Fatalf("Handle(repo-2): %v", err)
	}
	okProvider := &stubProvider{handle: mustHandle("handle-repo-1"), revision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: "1111111111111111111111111111111111111a", WorkspaceGeneration: 1}}
	if err := workspaceprovision.New(uow, ids, okProvider).Handle(ctx, jobFor("repo-1")); err != nil {
		t.Fatalf("Handle(repo-1): %v", err)
	}

	set, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	if set.State != workspace.WorkspaceSetBlocked {
		t.Fatalf("WorkspaceSet.State = %q, want BLOCKED", set.State)
	}
	successRow, err := uow.Snapshot.Work().GetRepositoryWorkspace(ctx, root.WorkspaceSetID, "repo-1", 1)
	if err != nil || successRow.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("repo-1 RepositoryWorkspace = %+v, %v, want a kept READY row", successRow, err)
	}
}

func TestHandle_AlreadyReady_IdempotentNoOp(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1")

	provider := &stubProvider{
		handle:   mustHandle("handle-repo-1"),
		revision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: "cafebabecafebabecafebabecafebabecafebabe", WorkspaceGeneration: 1},
	}
	handler := workspaceprovision.New(uow, ids, provider)
	job := provisionJob(root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-1")
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle (first): %v", err)
	}

	// A crash between the finish transaction's own commit and workerpool
	// marking the job SUCCEEDED reclaims the same job. Handle must detect
	// the RepositoryWorkspace already reached READY and return nil without
	// ever calling the provider again.
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle (reclaim): %v, want nil (idempotent no-op)", err)
	}
	if provider.provisionCalls != 1 || provider.captureCalls != 1 {
		t.Fatalf("provisionCalls=%d captureCalls=%d after reclaim, want 1 and 1 (must never re-run against an already-READY repository workspace)",
			provider.provisionCalls, provider.captureCalls)
	}
}

func TestHandle_AlreadyFailed_IdempotentNoOp(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1")

	provider := &stubProvider{provisionErr: errors.New("boom")}
	handler := workspaceprovision.New(uow, ids, provider)
	job := provisionJob(root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-1")
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle (first): %v", err)
	}
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle (reclaim): %v, want nil (idempotent no-op)", err)
	}
	if provider.provisionCalls != 1 {
		t.Fatalf("provisionCalls = %d after reclaim, want 1 (must never re-run against an already-FAILED repository workspace)", provider.provisionCalls)
	}
}

func TestHandle_ZeroHandleFromProvider_ReturnsErrorForRetry(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1")

	provider := &stubProvider{} // Provision succeeds (nil error) but leaves handle at its zero value.
	handler := workspaceprovision.New(uow, ids, provider)
	job := provisionJob(root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-1")
	if err := handler.Handle(ctx, job); err == nil {
		t.Fatal("Handle succeeded for a zero WorkspaceHandle contract violation, want an error")
	}
	if provider.captureCalls != 0 {
		t.Fatalf("captureCalls = %d, want 0", provider.captureCalls)
	}
	if _, err := uow.Snapshot.Work().GetRepositoryWorkspace(ctx, root.WorkspaceSetID, "repo-1", 1); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetRepositoryWorkspace error = %v, want ErrPersistenceNotFound (no row on a genuine handler failure)", err)
	}
}

func TestHandle_CaptureRevisionFailure_ReturnsErrorForRetry(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedActiveRepository(t, uow, ids, "project-1", "repo-1")
	root := mustCreateRootWorkItem(t, uow, ids, "project-1", "repo-1")

	provider := &stubProvider{handle: mustHandle("handle-repo-1"), captureErr: errors.New("boom: inspect failed")}
	handler := workspaceprovision.New(uow, ids, provider)
	job := provisionJob(root.ProvisionedRepositories[0].ProvisionJobID, root.WorkItemID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-1")
	if err := handler.Handle(ctx, job); err == nil {
		t.Fatal("Handle succeeded despite a CaptureRevision failure, want an error left for retry")
	}
	if _, err := uow.Snapshot.Work().GetRepositoryWorkspace(ctx, root.WorkspaceSetID, "repo-1", 1); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetRepositoryWorkspace error = %v, want ErrPersistenceNotFound", err)
	}
}

func TestHandle_MalformedPayload_ReturnsError(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	handler := workspaceprovision.New(uow, ids, &stubProvider{})

	job := ports.DurableJob{ID: "job-bad", Kind: appwork.WorkspaceProvisionJobKind, Payload: []byte(`{not-json`)}
	if err := handler.Handle(ctx, job); err == nil {
		t.Fatal("Handle succeeded for a malformed payload, want an error")
	}
}

func TestHandle_MissingRequiredField_ReturnsError(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	handler := workspaceprovision.New(uow, ids, &stubProvider{})

	job := ports.DurableJob{ID: "job-bad", Kind: appwork.WorkspaceProvisionJobKind, Payload: []byte(`{"projectId":"project-1","familyId":"family-1","workspaceSetId":"set-1"}`)}
	if err := handler.Handle(ctx, job); err == nil {
		t.Fatal("Handle succeeded for a payload missing repositoryId, want an error")
	}
}
