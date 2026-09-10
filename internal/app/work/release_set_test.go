package work_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// seedReleaseSetFamily creates a real TaskFamily (via CreateRootWorkItem,
// the same public command every other test in this package exercises) and
// one ACTIVE repository per repositoryID, returning the minted FamilyID —
// everything CreateReleaseSet's own family/repository-existence checks
// require.
func seedReleaseSetFamily(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID string, repositoryIDs ...string) string {
	t.Helper()
	mustCreateProject(t, uow, projectID)
	grants := make([]work.ScopeGrantRequest, 0, len(repositoryIDs))
	for _, repositoryID := range repositoryIDs {
		mustCreateActiveRepository(t, uow, ids, projectID, repositoryID)
		grants = append(grants, grant(repositoryID))
	}
	cmd := testCommand("idem-root-"+projectID, "hash-root-"+projectID, ports.ProjectScope(projectID), "CreateRootWorkItem")
	result, err := work.CreateRootWorkItem(context.Background(), uow, ids, cmd, baseRequest(projectID, grants...))
	if err != nil {
		t.Fatalf("seed CreateRootWorkItem: %v", err)
	}
	return result.FamilyID
}

func releaseSetCommand(idempotencyKey, requestHash string, scope ports.CommandScope, expectedVersion uint64) ports.Command {
	cmd := testCommand(idempotencyKey, requestHash, scope, "ReleaseSet")
	cmd.ExpectedVersion = expectedVersion
	return cmd
}

func oneRepoRequest(projectID, familyID, repositoryID, base, result, verdict string) work.CreateReleaseSetRequest {
	return work.CreateReleaseSetRequest{
		ProjectID: projectID, FamilyID: familyID,
		Repositories: []work.RepositoryReleaseRequest{
			{RepositoryID: repositoryID, BaseVCSObjectID: base, ResultVCSObjectID: result, Verdict: verdict},
		},
	}
}

func TestCreateReleaseSet_PersistsCreatedReleaseSet(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")

	cmd := releaseSetCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), 0)
	result, err := work.CreateReleaseSet(ctx, uow, ids, cmd, oneRepoRequest("project-1", familyID, "repo-1", "base-1", "result-1", string(gate.VerdictPass)))
	if err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}
	if result.State != string(workdomain.ReleaseSetCreated) || result.Version != 1 || result.ReleaseSetID == "" {
		t.Fatalf("result = %+v, want CREATED/version 1 with a minted ReleaseSetID", result)
	}

	stored, err := uow.Snapshot.Work().GetReleaseSet(ctx, result.ReleaseSetID)
	if err != nil {
		t.Fatalf("GetReleaseSet: %v", err)
	}
	release, ok := stored.ReleaseFor("repo-1")
	if !ok || release.Verdict != gate.VerdictPass || release.BaseVCSObjectID != "base-1" || release.ResultVCSObjectID != "result-1" {
		t.Fatalf("stored release for repo-1 = %+v, ok=%v, want PASS/base-1/result-1", release, ok)
	}
}

// TestCreateReleaseSet_MixedVerdicts_PartialResult is this task's own
// "partial result" Verify-line scenario: not every repository in a
// ReleaseSet needs to share the same verdict.
func TestCreateReleaseSet_MixedVerdicts_PartialResult(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-pass", "repo-fail")

	cmd := releaseSetCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), 0)
	req := work.CreateReleaseSetRequest{
		ProjectID: "project-1", FamilyID: familyID,
		Repositories: []work.RepositoryReleaseRequest{
			{RepositoryID: "repo-pass", BaseVCSObjectID: "base-pass", ResultVCSObjectID: "result-pass", Verdict: string(gate.VerdictPass)},
			{RepositoryID: "repo-fail", BaseVCSObjectID: "base-fail", ResultVCSObjectID: "result-fail", Verdict: string(gate.VerdictFail)},
		},
	}
	result, err := work.CreateReleaseSet(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}

	stored, err := uow.Snapshot.Work().GetReleaseSet(ctx, result.ReleaseSetID)
	if err != nil {
		t.Fatalf("GetReleaseSet: %v", err)
	}
	pass, ok := stored.ReleaseFor("repo-pass")
	if !ok || pass.Verdict != gate.VerdictPass {
		t.Fatalf("repo-pass release = %+v, ok=%v, want PASS", pass, ok)
	}
	fail, ok := stored.ReleaseFor("repo-fail")
	if !ok || fail.Verdict != gate.VerdictFail {
		t.Fatalf("repo-fail release = %+v, ok=%v, want FAIL", fail, ok)
	}
}

func TestCreateReleaseSet_Replay_ReturnsCachedResult(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")

	cmd := releaseSetCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), 0)
	req := oneRepoRequest("project-1", familyID, "repo-1", "base-1", "result-1", string(gate.VerdictPass))
	first, err := work.CreateReleaseSet(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first CreateReleaseSet: %v", err)
	}
	second, err := work.CreateReleaseSet(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("replayed CreateReleaseSet: %v", err)
	}
	if first.ReleaseSetID != second.ReleaseSetID {
		t.Fatalf("replay minted a different ReleaseSetID: first=%s second=%s", first.ReleaseSetID, second.ReleaseSetID)
	}

	all, err := uow.Snapshot.Work().ListReleaseSetsForFamily(ctx, familyID)
	if err != nil {
		t.Fatalf("ListReleaseSetsForFamily: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("release sets after replay = %d, want exactly 1 (replay must never create a second one)", len(all))
	}
}

func TestCreateReleaseSet_ReceiptConflict_DifferentHash(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")

	req := oneRepoRequest("project-1", familyID, "repo-1", "base-1", "result-1", string(gate.VerdictPass))
	first := releaseSetCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), 0)
	if _, err := work.CreateReleaseSet(ctx, uow, ids, first, req); err != nil {
		t.Fatalf("first CreateReleaseSet: %v", err)
	}
	second := releaseSetCommand("idem-1", "hash-b", ports.ProjectScope("project-1"), 0)
	if _, err := work.CreateReleaseSet(ctx, uow, ids, second, req); !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("err = %v, want ports.ErrReceiptConflict", err)
	}
}

func TestCreateReleaseSet_CrossProjectFamily_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")

	cmd := releaseSetCommand("idem-1", "hash-a", ports.ProjectScope("project-2"), 0)
	req := oneRepoRequest("project-2", familyID, "repo-1", "base-1", "result-1", string(gate.VerdictPass))
	_, err := work.CreateReleaseSet(ctx, uow, ids, cmd, req)
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("err = %v, want ports.ErrCrossProjectReference", err)
	}
}

func mustCreateReleaseSet(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, familyID, repositoryID string) string {
	t.Helper()
	cmd := releaseSetCommand("idem-create-"+familyID, "hash-create-"+familyID, ports.ProjectScope(projectID), 0)
	result, err := work.CreateReleaseSet(context.Background(), uow, ids, cmd, oneRepoRequest(projectID, familyID, repositoryID, "base-1", "result-1", string(gate.VerdictPass)))
	if err != nil {
		t.Fatalf("mustCreateReleaseSet: %v", err)
	}
	return result.ReleaseSetID
}

func TestSealReleaseSet_ClosesLifecycle(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")
	releaseSetID := mustCreateReleaseSet(t, uow, ids, "project-1", familyID, "repo-1")

	cmd := releaseSetCommand("idem-seal-1", "hash-seal-a", ports.ProjectScope("project-1"), 1)
	result, err := work.SealReleaseSet(ctx, uow, cmd, work.SealReleaseSetRequest{ReleaseSetID: releaseSetID})
	if err != nil {
		t.Fatalf("SealReleaseSet: %v", err)
	}
	if result.State != string(workdomain.ReleaseSetSealed) || result.Version != 2 {
		t.Fatalf("result = %+v, want SEALED/version 2", result)
	}

	stored, err := uow.Snapshot.Work().GetReleaseSet(ctx, releaseSetID)
	if err != nil {
		t.Fatalf("GetReleaseSet: %v", err)
	}
	if stored.State != workdomain.ReleaseSetSealed || stored.SealedAt == nil {
		t.Fatalf("stored = %+v, want SEALED with SealedAt set", stored)
	}
}

func TestAbandonReleaseSet_ClosesLifecycle(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")
	releaseSetID := mustCreateReleaseSet(t, uow, ids, "project-1", familyID, "repo-1")

	cmd := releaseSetCommand("idem-abandon-1", "hash-abandon-a", ports.ProjectScope("project-1"), 1)
	result, err := work.AbandonReleaseSet(ctx, uow, cmd, work.AbandonReleaseSetRequest{ReleaseSetID: releaseSetID})
	if err != nil {
		t.Fatalf("AbandonReleaseSet: %v", err)
	}
	if result.State != string(workdomain.ReleaseSetAbandoned) || result.Version != 2 {
		t.Fatalf("result = %+v, want ABANDONED/version 2", result)
	}

	stored, err := uow.Snapshot.Work().GetReleaseSet(ctx, releaseSetID)
	if err != nil {
		t.Fatalf("GetReleaseSet: %v", err)
	}
	if stored.State != workdomain.ReleaseSetAbandoned || stored.AbandonedAt == nil {
		t.Fatalf("stored = %+v, want ABANDONED with AbandonedAt set", stored)
	}
}

// TestSealReleaseSet_DuplicateSeal_Rejected is this task's own "duplicate
// seal" Verify-line scenario: a second, genuinely distinct seal attempt
// (a different IdempotencyKey, not a replay of the first) against an
// already-sealed ReleaseSet must fail loudly with a specific error, not
// silently no-op or re-transition.
func TestSealReleaseSet_DuplicateSeal_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")
	releaseSetID := mustCreateReleaseSet(t, uow, ids, "project-1", familyID, "repo-1")

	first := releaseSetCommand("idem-seal-1", "hash-seal-a", ports.ProjectScope("project-1"), 1)
	if _, err := work.SealReleaseSet(ctx, uow, first, work.SealReleaseSetRequest{ReleaseSetID: releaseSetID}); err != nil {
		t.Fatalf("first SealReleaseSet: %v", err)
	}

	second := releaseSetCommand("idem-seal-2", "hash-seal-b", ports.ProjectScope("project-1"), 1)
	_, err := work.SealReleaseSet(ctx, uow, second, work.SealReleaseSetRequest{ReleaseSetID: releaseSetID})
	if !errors.Is(err, work.ErrReleaseSetNotOpen) {
		t.Fatalf("err = %v, want work.ErrReleaseSetNotOpen", err)
	}
}

// TestSealReleaseSet_StaleExpectedVersion_Rejected is this task's own
// "stale revision" Verify-line scenario for ReleaseSet's own optimistic
// concurrency fence.
func TestSealReleaseSet_StaleExpectedVersion_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")
	releaseSetID := mustCreateReleaseSet(t, uow, ids, "project-1", familyID, "repo-1")

	cmd := releaseSetCommand("idem-seal-1", "hash-seal-a", ports.ProjectScope("project-1"), 99)
	_, err := work.SealReleaseSet(ctx, uow, cmd, work.SealReleaseSetRequest{ReleaseSetID: releaseSetID})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("err = %v, want ports.ErrOptimisticConflict", err)
	}
}

func TestSealReleaseSet_MissingExpectedVersion_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	cmd := releaseSetCommand("idem-seal-1", "hash-seal-a", ports.ProjectScope("project-1"), 0)
	_, err := work.SealReleaseSet(ctx, uow, cmd, work.SealReleaseSetRequest{ReleaseSetID: "release-1"})
	if err == nil {
		t.Fatal("SealReleaseSet with ExpectedVersion=0 was accepted")
	}
}

func TestIsCleanupEligible(t *testing.T) {
	t.Parallel()
	now := time.Now()
	created, err := workdomain.NewReleaseSet("release-1", "project-1", "family-1", []workdomain.RepositoryRelease{
		{RepositoryID: "repo-1", BaseVCSObjectID: "base-1", ResultVCSObjectID: "result-1", Verdict: gate.VerdictPass},
	}, now)
	if err != nil {
		t.Fatalf("NewReleaseSet: %v", err)
	}
	if work.IsCleanupEligible(created) {
		t.Fatal("a CREATED release set was reported cleanup-eligible")
	}
	sealed := created
	sealed.State = workdomain.ReleaseSetSealed
	if !work.IsCleanupEligible(sealed) {
		t.Fatal("a SEALED release set was reported not cleanup-eligible")
	}
	abandoned := created
	abandoned.State = workdomain.ReleaseSetAbandoned
	if !work.IsCleanupEligible(abandoned) {
		t.Fatal("an ABANDONED release set was reported not cleanup-eligible")
	}
}

func TestEligibilityAuthority_IsReleaseAuthorized(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")
	authority := work.NewEligibilityAuthority(uow)

	authorized, reason, err := authority.IsReleaseAuthorized(ctx, familyID)
	if err != nil {
		t.Fatalf("IsReleaseAuthorized (no release set): %v", err)
	}
	if authorized || reason == "" {
		t.Fatalf("authorized=%v reason=%q, want unauthorized with a reason (no release set exists yet)", authorized, reason)
	}

	releaseSetID := mustCreateReleaseSet(t, uow, ids, "project-1", familyID, "repo-1")
	authorized, reason, err = authority.IsReleaseAuthorized(ctx, familyID)
	if err != nil {
		t.Fatalf("IsReleaseAuthorized (CREATED): %v", err)
	}
	if authorized || reason == "" {
		t.Fatalf("authorized=%v reason=%q, want unauthorized while release set is still CREATED", authorized, reason)
	}

	sealCmd := releaseSetCommand("idem-seal-1", "hash-seal-a", ports.ProjectScope("project-1"), 1)
	if _, err := work.SealReleaseSet(ctx, uow, sealCmd, work.SealReleaseSetRequest{ReleaseSetID: releaseSetID}); err != nil {
		t.Fatalf("SealReleaseSet: %v", err)
	}
	authorized, _, err = authority.IsReleaseAuthorized(ctx, familyID)
	if err != nil {
		t.Fatalf("IsReleaseAuthorized (SEALED): %v", err)
	}
	if !authorized {
		t.Fatal("a SEALED release set did not authorize release")
	}
}
