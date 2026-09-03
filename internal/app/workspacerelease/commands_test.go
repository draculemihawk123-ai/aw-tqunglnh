package workspacerelease_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// fakeReleaseAuthority is a local, in-package test double for
// ports.ReleaseEligibilityAuthority (V3-11's own "V3 test bằng fake
// eligibility authority" line) — mirroring
// internal/app/workspacereconcile/handler_test.go's own
// stubUnreachableProvider/stubUnreachableLifecycle pattern of a small local
// fake for a port with no shared internal/app/ports/fake counterpart. Calls
// is exported so a test can assert a pure replay never re-consults it (see
// this package's own commands.go doc comment on why).
type fakeReleaseAuthority struct {
	Authorized bool
	Reason     string
	Err        error
	Calls      int
}

func (f *fakeReleaseAuthority) IsReleaseAuthorized(_ context.Context, _ string) (bool, string, error) {
	f.Calls++
	return f.Authorized, f.Reason, f.Err
}

func authorizedFake() *fakeReleaseAuthority { return &fakeReleaseAuthority{Authorized: true} }

// repoSpec describes one RepositoryWorkspace mustSeedWorkspaceSet seeds
// alongside the WorkspaceSet.
type repoSpec struct {
	RepositoryID string
	State        workspace.RepositoryWorkspaceState
}

// seededSet bundles everything a test needs to address one real
// WorkspaceSet row and its RepositoryWorkspace children, seeded directly
// (bypassing internal/app/work.CreateRootWorkItem's own real command,
// unlike internal/app/workspacereconcile's own commands_test.go helper):
// CreateRootWorkItem also enqueues its own WORKSPACE_PROVISION job
// (AggregateType "WorkspaceSet", AggregateID = the WorkspaceSet's own ID),
// which this fake never completes (no ClaimJob/CompleteJob exists in
// fake.JobsRepository) — so it would sit "active" forever and make every
// happy-path eligibility test here fail its own "no active job" check for
// a reason that has nothing to do with what the test is exercising. Seeding
// TaskFamily/WorkspaceSet/RepositoryWorkspace directly via their own domain
// constructors (work.NewRootWorkItem/NewTaskFamily,
// workspace.NewWorkspaceSet/NewRepositoryWorkspace) — the exact same
// constructors CreateRootWorkItem itself calls — avoids that job entirely
// while keeping every persisted row exactly as real.
type seededSet struct {
	ProjectID              string
	FamilyID               string
	WorkspaceSetID         string
	Version                uint64
	RepositoryWorkspaceIDs []string
}

func mustSeedWorkspaceSet(t *testing.T, uow ports.UnitOfWork, projectID, familyID string, repos []repoSpec) seededSet {
	t.Helper()
	ctx := context.Background()
	var result seededSet
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID}); err != nil {
			return err
		}
		root, err := workdomain.NewRootWorkItem(workdomain.WorkItemID("root-"+familyID), project.ProjectID(projectID), workdomain.TaskFamilyID(familyID), "Root task")
		if err != nil {
			return err
		}
		family, err := workdomain.NewTaskFamily(workdomain.TaskFamilyID(familyID), root)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkItem(ctx, root); err != nil {
			return err
		}

		set, err := workspace.NewWorkspaceSet(workspace.WorkspaceSetID("set-"+familyID), family)
		if err != nil {
			return err
		}
		created, err := tx.Work().CreateWorkspaceSet(ctx, set)
		if err != nil {
			return err
		}

		repositoryWorkspaceIDs := make([]string, 0, len(repos))
		for i, spec := range repos {
			if _, err := tx.Catalog().RegisterRepository(ctx, ports.RegisterRepositoryRequest{
				ID: spec.RepositoryID, ProjectID: projectID, Name: "svc-" + spec.RepositoryID,
				RemoteLocator: "/fixture/" + spec.RepositoryID, DefaultRef: "main",
			}); err != nil {
				return err
			}
			repo, err := tx.Catalog().GetRepository(ctx, spec.RepositoryID)
			if err != nil {
				return err
			}
			rw, err := workspace.NewRepositoryWorkspace(
				workspace.RepositoryWorkspaceID(fmt.Sprintf("rw-%s-%d", familyID, i)), created, repo, 1,
				"handle-"+spec.RepositoryID, "", "base-sha",
			)
			if err != nil {
				return err
			}
			rw.State = spec.State
			rw.CurrentRevision = "base-sha"
			persisted, err := tx.Work().CreateRepositoryWorkspace(ctx, rw)
			if err != nil {
				return err
			}
			repositoryWorkspaceIDs = append(repositoryWorkspaceIDs, string(persisted.ID))
		}

		result = seededSet{
			ProjectID: projectID, FamilyID: familyID, WorkspaceSetID: string(created.ID),
			Version: created.Version, RepositoryWorkspaceIDs: repositoryWorkspaceIDs,
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed workspace set: %v", err)
	}
	return result
}

// releaseJobs filters uow's own enqueued jobs down to
// WorkspaceSetReleaseJobKind alone.
func releaseJobs(uow *fake.UnitOfWork) []ports.EnqueueJobRequest {
	var result []ports.EnqueueJobRequest
	for _, job := range uow.Snapshot.Jobs().(*fake.JobsRepository).Items() {
		if job.Kind == workspacerelease.WorkspaceSetReleaseJobKind {
			result = append(result, job)
		}
	}
	return result
}

func releaseCommand(idempotencyKey, requestHash, projectID string, expectedVersion uint64) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), ExpectedVersion: expectedVersion,
		RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type:        "RequestWorkspaceSetRelease", RequestHash: requestHash,
	}
}

func TestRequestWorkspaceSetRelease_Eligible_EnqueuesJobAndWritesEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
		{RepositoryID: "repo-2", State: workspace.RepositoryWorkspaceReady},
	})
	authority := authorizedFake()

	cmd := releaseCommand("idem-1", "hash-a", "project-1", seeded.Version)
	result, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authority, cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if err != nil {
		t.Fatalf("RequestWorkspaceSetRelease: %v", err)
	}
	if result.WorkspaceSetID != seeded.WorkspaceSetID || result.FamilyID != "family-1" {
		t.Fatalf("result = %+v, want WorkspaceSetID=%s FamilyID=family-1", result, seeded.WorkspaceSetID)
	}
	if result.ReleaseJobID == "" {
		t.Fatal("result.ReleaseJobID is empty, want a minted job id")
	}
	if authority.Calls != 1 {
		t.Fatalf("authority.Calls = %d, want exactly 1", authority.Calls)
	}

	jobs := releaseJobs(uow)
	if len(jobs) != 1 {
		t.Fatalf("enqueued release jobs = %d, want exactly 1", len(jobs))
	}
	if jobs[0].AggregateType != "WorkspaceSet" || jobs[0].AggregateID != seeded.WorkspaceSetID {
		t.Fatalf("job = %+v, want AggregateType=WorkspaceSet AggregateID=%s", jobs[0], seeded.WorkspaceSetID)
	}

	var releaseEvents int
	for _, event := range uow.Snapshot.Events().(*fake.EventsRepository).Items() {
		if event.EventType == "WorkspaceSetReleaseRequested" {
			releaseEvents++
		}
	}
	if releaseEvents != 1 {
		t.Fatalf("WorkspaceSetReleaseRequested events = %d, want exactly 1", releaseEvents)
	}
}

// TestRequestWorkspaceSetRelease_PayloadEnumeratesEveryRepositoryWorkspace
// is this task's own "partial release" Verify-line scenario framed per the
// judgment-call boundary: V3-11 builds no executor, so there is no real
// partial result to produce here — what this layer owns is proving its own
// job payload correctly enumerates every RepositoryWorkspace in the set, so
// a future V5-14 executor COULD produce one later.
func TestRequestWorkspaceSetRelease_PayloadEnumeratesEveryRepositoryWorkspace(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
		{RepositoryID: "repo-2", State: workspace.RepositoryWorkspaceReady},
		{RepositoryID: "repo-3", State: workspace.RepositoryWorkspaceReady},
	})

	cmd := releaseCommand("idem-1", "hash-a", "project-1", seeded.Version)
	if _, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	}); err != nil {
		t.Fatalf("RequestWorkspaceSetRelease: %v", err)
	}

	jobs := releaseJobs(uow)
	if len(jobs) != 1 {
		t.Fatalf("enqueued release jobs = %d, want exactly 1", len(jobs))
	}
	var payload struct {
		RepositoryWorkspaces []struct {
			RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
			RepositoryID          string `json:"repositoryId"`
			Generation            uint64 `json:"generation"`
		} `json:"repositoryWorkspaces"`
	}
	if err := json.Unmarshal(jobs[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal job payload: %v", err)
	}
	if len(payload.RepositoryWorkspaces) != 3 {
		t.Fatalf("payload.RepositoryWorkspaces = %d entries, want 3 (one per seeded repository workspace)", len(payload.RepositoryWorkspaces))
	}
	seenIDs := map[string]bool{}
	for _, entry := range payload.RepositoryWorkspaces {
		seenIDs[entry.RepositoryWorkspaceID] = true
		if entry.Generation != 1 {
			t.Fatalf("entry %+v Generation = %d, want 1", entry, entry.Generation)
		}
	}
	for _, id := range seeded.RepositoryWorkspaceIDs {
		if !seenIDs[id] {
			t.Fatalf("payload.RepositoryWorkspaces missing repository workspace %s", id)
		}
	}
}

func TestRequestWorkspaceSetRelease_QuarantinedRepository_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
		{RepositoryID: "repo-2", State: workspace.RepositoryWorkspaceQuarantined},
	})

	cmd := releaseCommand("idem-1", "hash-a", "project-1", seeded.Version)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if !errors.Is(err, workspacerelease.ErrWorkspaceSetHasQuarantinedRepository) {
		t.Fatalf("RequestWorkspaceSetRelease() error = %v, want ErrWorkspaceSetHasQuarantinedRepository", err)
	}
	if jobs := releaseJobs(uow); len(jobs) != 0 {
		t.Fatalf("enqueued release jobs after quarantine rejection = %d, want 0", len(jobs))
	}
}

// TestRequestWorkspaceSetRelease_ActiveJob_Rejected exercises the "active
// job" eligibility check via the fake's own faithful-enough model (every
// job it has ever seen stays "active" — see seededSet's own doc comment):
// pre-enqueuing an unrelated durable job whose AggregateID names one of the
// set's own repository workspaces must block the release request.
func TestRequestWorkspaceSetRelease_ActiveJob_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
	})
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: "job-in-flight", ProjectID: project.ProjectID("project-1"), Kind: "WORKSPACE_RECONCILIATION",
			AggregateType: "RepositoryWorkspace", AggregateID: seeded.RepositoryWorkspaceIDs[0],
			IdempotencyKey: "workspace-reconciliation:in-flight",
		})
		return err
	}); err != nil {
		t.Fatalf("seed in-flight job: %v", err)
	}

	cmd := releaseCommand("idem-1", "hash-a", "project-1", seeded.Version)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if !errors.Is(err, workspacerelease.ErrWorkspaceSetHasActiveJob) {
		t.Fatalf("RequestWorkspaceSetRelease() error = %v, want ErrWorkspaceSetHasActiveJob", err)
	}
	if jobs := releaseJobs(uow); len(jobs) != 0 {
		t.Fatalf("enqueued release jobs after active-job rejection = %d, want 0", len(jobs))
	}
}

func TestRequestWorkspaceSetRelease_NotAuthorized_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
	})
	authority := &fakeReleaseAuthority{Authorized: false, Reason: "ReleaseSet is still open"}

	cmd := releaseCommand("idem-1", "hash-a", "project-1", seeded.Version)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authority, cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if !errors.Is(err, workspacerelease.ErrReleaseNotAuthorized) {
		t.Fatalf("RequestWorkspaceSetRelease() error = %v, want ErrReleaseNotAuthorized", err)
	}
	if jobs := releaseJobs(uow); len(jobs) != 0 {
		t.Fatalf("enqueued release jobs after not-authorized rejection = %d, want 0", len(jobs))
	}
}

func TestRequestWorkspaceSetRelease_AuthorityError_Propagated(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
	})
	wantErr := errors.New("authority unreachable")
	authority := &fakeReleaseAuthority{Err: wantErr}

	cmd := releaseCommand("idem-1", "hash-a", "project-1", seeded.Version)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authority, cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RequestWorkspaceSetRelease() error = %v, want wrapping %v", err, wantErr)
	}
}

func TestRequestWorkspaceSetRelease_StaleExpectedVersion_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
	})

	staleVersion := seeded.Version + 41
	cmd := releaseCommand("idem-1", "hash-a", "project-1", staleVersion)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("RequestWorkspaceSetRelease() error = %v, want ErrOptimisticConflict", err)
	}
}

func TestRequestWorkspaceSetRelease_CrossProject_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
	})

	cmd := releaseCommand("idem-1", "hash-a", "project-other", seeded.Version)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-other",
	})
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("RequestWorkspaceSetRelease() error = %v, want ErrCrossProjectReference", err)
	}
}

func TestRequestWorkspaceSetRelease_AlreadyReleased_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", nil)

	// Drive the WorkspaceSet all the way to RELEASED directly via its own
	// real CAS transition — the same transition a real V5-14 executor
	// would eventually perform, out of this package's own scope to
	// exercise any other way.
	var releasedVersion uint64
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		provisioning, err := tx.Work().TransitionWorkspaceSetState(ctx, ports.TransitionWorkspaceSetStateRequest{
			WorkspaceSetID: seeded.WorkspaceSetID, ExpectedState: workspace.WorkspaceSetRequested, ExpectedVersion: seeded.Version,
			NextState: workspace.WorkspaceSetProvisioning,
		})
		if err != nil {
			return err
		}
		ready, err := tx.Work().TransitionWorkspaceSetState(ctx, ports.TransitionWorkspaceSetStateRequest{
			WorkspaceSetID: seeded.WorkspaceSetID, ExpectedState: workspace.WorkspaceSetProvisioning, ExpectedVersion: provisioning.Version,
			NextState: workspace.WorkspaceSetReady,
		})
		if err != nil {
			return err
		}
		releasing, err := tx.Work().TransitionWorkspaceSetState(ctx, ports.TransitionWorkspaceSetStateRequest{
			WorkspaceSetID: seeded.WorkspaceSetID, ExpectedState: workspace.WorkspaceSetReady, ExpectedVersion: ready.Version,
			NextState: workspace.WorkspaceSetReleasing,
		})
		if err != nil {
			return err
		}
		released, err := tx.Work().TransitionWorkspaceSetState(ctx, ports.TransitionWorkspaceSetStateRequest{
			WorkspaceSetID: seeded.WorkspaceSetID, ExpectedState: workspace.WorkspaceSetReleasing, ExpectedVersion: releasing.Version,
			NextState: workspace.WorkspaceSetReleased,
		})
		if err != nil {
			return err
		}
		releasedVersion = released.Version
		return nil
	}); err != nil {
		t.Fatalf("drive workspace set to RELEASED: %v", err)
	}

	cmd := releaseCommand("idem-1", "hash-a", "project-1", releasedVersion)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if !errors.Is(err, workspacerelease.ErrWorkspaceSetAlreadyReleased) {
		t.Fatalf("RequestWorkspaceSetRelease() error = %v, want ErrWorkspaceSetAlreadyReleased", err)
	}
}

func TestRequestWorkspaceSetRelease_DuplicateSameCommand_ReplaysWithoutNewJobEventOrAuthorityRecall(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
	})
	authority := authorizedFake()

	cmd := releaseCommand("idem-1", "hash-a", "project-1", seeded.Version)
	req := workspacerelease.RequestWorkspaceSetReleaseRequest{FamilyID: "family-1", ProjectID: "project-1"}
	first, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authority, cmd, req)
	if err != nil {
		t.Fatalf("first RequestWorkspaceSetRelease: %v", err)
	}
	second, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authority, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) RequestWorkspaceSetRelease: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}
	if jobs := releaseJobs(uow); len(jobs) != 1 {
		t.Fatalf("enqueued release jobs after replay = %d, want 1 (a replay must never redo the mutation)", len(jobs))
	}
	if authority.Calls != 1 {
		t.Fatalf("authority.Calls after replay = %d, want exactly 1 (a pure replay must never re-consult authority)", authority.Calls)
	}
}

// TestRequestWorkspaceSetRelease_SecondDistinctRequest_AlreadyOpen_Rejected
// is this task's own "idempotent primitive" Verify-line requirement at the
// REQUEST layer: two DIFFERENT commands for the same WorkspaceSet at the
// same still-current version must not both succeed in enqueuing a job.
func TestRequestWorkspaceSetRelease_SecondDistinctRequest_AlreadyOpen_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
	})
	req := workspacerelease.RequestWorkspaceSetReleaseRequest{FamilyID: "family-1", ProjectID: "project-1"}

	first := releaseCommand("idem-1", "hash-a", "project-1", seeded.Version)
	if _, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), first, req); err != nil {
		t.Fatalf("first RequestWorkspaceSetRelease: %v", err)
	}

	second := releaseCommand("idem-2-different-caller", "hash-b", "project-1", seeded.Version)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), second, req)
	// The second, distinct request is rejected twice over: the "active
	// job" eligibility check now also sees the first request's own
	// still-open WORKSPACE_SET_RELEASE job (this fake never completes a
	// job — see seededSet's own doc comment), so it never even reaches
	// the job-idempotency-key collision the real sqlite adapter's own
	// UNIQUE constraint would additionally enforce.
	if !errors.Is(err, workspacerelease.ErrWorkspaceSetHasActiveJob) {
		t.Fatalf("second distinct RequestWorkspaceSetRelease() error = %v, want ErrWorkspaceSetHasActiveJob", err)
	}
	if jobs := releaseJobs(uow); len(jobs) != 1 {
		t.Fatalf("enqueued release jobs after a second, distinct in-flight request = %d, want exactly 1", len(jobs))
	}
}

func TestRequestWorkspaceSetRelease_MissingExpectedVersion_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
	})
	_ = seeded

	cmd := releaseCommand("idem-1", "hash-a", "project-1", 0)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if err == nil {
		t.Fatal("RequestWorkspaceSetRelease() with cmd.ExpectedVersion=0 error = nil, want an error")
	}
}

func TestRequestWorkspaceSetRelease_NilAuthority_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
	})

	cmd := releaseCommand("idem-1", "hash-a", "project-1", seeded.Version)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, nil, cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if err == nil {
		t.Fatal("RequestWorkspaceSetRelease() with nil authority error = nil, want an error")
	}
}
