package releasesetcommit

import (
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// TestRequestReleaseSetLocalCommit_Replay_ReturnsSameResult is this task's
// own "replay" Verify-line scenario at the command layer: the same
// IdempotencyKey+RequestHash returns the exact same result, never a second
// intent/job.
func TestRequestReleaseSetLocalCommit_Replay_ReturnsSameResult(t *testing.T) {
	fx := newExecuteFixture(t)
	first := fx.request(t, "req-1", "hash-1", "record repository result")
	second := fx.request(t, "req-1", "hash-1", "record repository result")
	if first != second {
		t.Fatalf("replay result = %+v, want identical to first %+v", second, first)
	}
}

// TestRequestReleaseSetLocalCommit_ReceiptConflict_DifferentHash_Rejected
// is this task's own "concurrency" Verify-line scenario: the same
// IdempotencyKey with a genuinely different RequestHash is a conflict, not
// a replay.
func TestRequestReleaseSetLocalCommit_ReceiptConflict_DifferentHash_Rejected(t *testing.T) {
	fx := newExecuteFixture(t)
	fx.request(t, "req-1", "hash-1", "record repository result")

	_, err := RequestReleaseSetLocalCommit(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-2", ports.ProjectScope("project-1"), "RequestReleaseSetLocalCommit", 0),
		RequestReleaseSetLocalCommitRequest{
			ProjectID: "project-1", ReleaseSetID: fx.releaseSetID, ExpectedReleaseSetVersion: 1,
			RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1,
			Message: "a different message entirely", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
		})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("error = %v, want ErrReceiptConflict", err)
	}
}

// TestRequestReleaseSetLocalCommit_StaleReleaseSetVersion_Rejected is this
// task's own "stale" Verify-line scenario: a caller's captured ReleaseSet
// version fence rejects a request against a ReleaseSet that has since
// moved — a typed conflict, never silently pinning the wrong version.
func TestRequestReleaseSetLocalCommit_StaleReleaseSetVersion_Rejected(t *testing.T) {
	fx := newExecuteFixture(t)
	_, err := RequestReleaseSetLocalCommit(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-1", ports.ProjectScope("project-1"), "RequestReleaseSetLocalCommit", 0),
		RequestReleaseSetLocalCommitRequest{
			ProjectID: "project-1", ReleaseSetID: fx.releaseSetID, ExpectedReleaseSetVersion: 999,
			RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1,
			Message: "record repository result", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
		})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("error = %v, want ErrOptimisticConflict", err)
	}
}

// TestRequestReleaseSetLocalCommit_StaleWorkspaceVersion_Rejected mirrors
// the release-set version case for the target RepositoryWorkspace's own
// fence.
func TestRequestReleaseSetLocalCommit_StaleWorkspaceVersion_Rejected(t *testing.T) {
	fx := newExecuteFixture(t)
	_, err := RequestReleaseSetLocalCommit(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-1", ports.ProjectScope("project-1"), "RequestReleaseSetLocalCommit", 0),
		RequestReleaseSetLocalCommitRequest{
			ProjectID: "project-1", ReleaseSetID: fx.releaseSetID, ExpectedReleaseSetVersion: 1,
			RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 999,
			Message: "record repository result", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
		})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("error = %v, want ErrOptimisticConflict", err)
	}
}

// TestRequestReleaseSetLocalCommit_CrossProjectReleaseSet_Rejected proves
// req.ProjectID must actually own the named ReleaseSet.
func TestRequestReleaseSetLocalCommit_CrossProjectReleaseSet_Rejected(t *testing.T) {
	fx := newExecuteFixture(t)
	_, err := RequestReleaseSetLocalCommit(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-1", ports.ProjectScope("project-other"), "RequestReleaseSetLocalCommit", 0),
		RequestReleaseSetLocalCommitRequest{
			ProjectID: "project-other", ReleaseSetID: fx.releaseSetID, ExpectedReleaseSetVersion: 1,
			RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1,
			Message: "record repository result", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
		})
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("error = %v, want ErrCrossProjectReference", err)
	}
}

// TestRequestReleaseSetLocalCommit_MarkerCollision_DifferentIdempotencyKey_Rejected
// is this task's own "marker collision" Verify-line scenario: two
// genuinely different commands (distinct IdempotencyKey, so the second is
// never treated as a replay of the first) that happen to name the exact
// same ReleaseSet/version, RepositoryWorkspace/generation, actor and
// message — and therefore derive the identical deterministic operation
// marker — must never both succeed: the second is rejected with
// ports.ErrLocalCommitMarkerCollision, never silently accepted as a second,
// ambiguous intent sharing one marker.
func TestRequestReleaseSetLocalCommit_MarkerCollision_DifferentIdempotencyKey_Rejected(t *testing.T) {
	fx := newExecuteFixture(t)
	req := RequestReleaseSetLocalCommitRequest{
		ProjectID: "project-1", ReleaseSetID: fx.releaseSetID, ExpectedReleaseSetVersion: 1,
		RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1,
		Message: "record repository result", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
	}
	if _, err := RequestReleaseSetLocalCommit(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-1", ports.ProjectScope("project-1"), "RequestReleaseSetLocalCommit", 0), req); err != nil {
		t.Fatalf("first request: %v", err)
	}

	_, err := RequestReleaseSetLocalCommit(fx.ctx, fx.uow, fx.ids,
		testCommand("req-2-a-genuinely-different-command", "hash-2", ports.ProjectScope("project-1"), "RequestReleaseSetLocalCommit", 0), req)
	if !errors.Is(err, ports.ErrLocalCommitMarkerCollision) {
		t.Fatalf("second request error = %v, want ErrLocalCommitMarkerCollision", err)
	}
}

// TestComputeOperationMarker_Deterministic_DifferentInputsDifferentMarkers
// is a pure unit test of this package's own marker construction: identical
// inputs always produce the identical marker, and any single differing
// input produces a different one.
func TestComputeOperationMarker_Deterministic_DifferentInputsDifferentMarkers(t *testing.T) {
	base := computeOperationMarker("rs-1", 1, "rw-1", 1, "actor-1", "sha256:hash-a")
	again := computeOperationMarker("rs-1", 1, "rw-1", 1, "actor-1", "sha256:hash-a")
	if base != again {
		t.Fatalf("computeOperationMarker is not deterministic: %s != %s", base, again)
	}

	variants := []string{
		computeOperationMarker("rs-2", 1, "rw-1", 1, "actor-1", "sha256:hash-a"),
		computeOperationMarker("rs-1", 2, "rw-1", 1, "actor-1", "sha256:hash-a"),
		computeOperationMarker("rs-1", 1, "rw-2", 1, "actor-1", "sha256:hash-a"),
		computeOperationMarker("rs-1", 1, "rw-1", 2, "actor-1", "sha256:hash-a"),
		computeOperationMarker("rs-1", 1, "rw-1", 1, "actor-2", "sha256:hash-a"),
		computeOperationMarker("rs-1", 1, "rw-1", 1, "actor-1", "sha256:hash-b"),
	}
	for i, variant := range variants {
		if variant == base {
			t.Fatalf("variant %d unexpectedly collided with base marker %s", i, base)
		}
	}
}
