package gitworktree

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func TestProvider_FindLocalCommitByMarker_NotFound_ReturnsCurrentHeadAsParent(t *testing.T) {
	t.Parallel()
	provider, handle, _, baseRevision := provisionLocalCommitFixture(t)

	found, headRevision, parentVCSObjectID, err := provider.FindLocalCommitByMarker(context.Background(), handle, "sha256:not-present")
	if err != nil {
		t.Fatalf("FindLocalCommitByMarker: %v", err)
	}
	if found {
		t.Fatal("found = true, want false — no commit has this marker yet")
	}
	if headRevision.VCSObjectID != baseRevision {
		t.Fatalf("headRevision.VCSObjectID = %s, want current HEAD %s", headRevision.VCSObjectID, baseRevision)
	}
	// baseRevision is the fixture's own initial commit, which has no
	// parent at all — parentVCSObjectID must be empty, not a hallucinated
	// value.
	if parentVCSObjectID != "" {
		t.Fatalf("parentVCSObjectID = %q, want empty (base revision has no parent)", parentVCSObjectID)
	}
}

func TestProvider_FindLocalCommitByMarker_Found_ReturnsResultAndParent(t *testing.T) {
	t.Parallel()
	provider, handle, _, baseRevision := provisionLocalCommitFixture(t)
	workspacePath, err := provider.workspacePath(handle)
	if err != nil {
		t.Fatalf("workspacePath: %v", err)
	}
	writeTestFile(t, filepath.Join(workspacePath, "service.txt"), "changed\n")

	marker := "sha256:marker-under-test"
	revision, err := provider.CreateLocalCommit(context.Background(), ports.CreateLocalCommitRequest{
		Handle: handle, Message: "record result [" + ports.LocalCommitMarkerTrailerKey + ": " + marker + "]",
		AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
	})
	if err != nil {
		t.Fatalf("CreateLocalCommit: %v", err)
	}

	found, headRevision, parentVCSObjectID, err := provider.FindLocalCommitByMarker(context.Background(), handle, marker)
	if err != nil {
		t.Fatalf("FindLocalCommitByMarker: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if headRevision.VCSObjectID != revision.VCSObjectID {
		t.Fatalf("headRevision.VCSObjectID = %s, want %s", headRevision.VCSObjectID, revision.VCSObjectID)
	}
	if parentVCSObjectID != baseRevision {
		t.Fatalf("parentVCSObjectID = %s, want %s", parentVCSObjectID, baseRevision)
	}
}

func TestProvider_FindLocalCommitByMarker_DifferentMarker_NotFound(t *testing.T) {
	t.Parallel()
	provider, handle, _, _ := provisionLocalCommitFixture(t)
	workspacePath, err := provider.workspacePath(handle)
	if err != nil {
		t.Fatalf("workspacePath: %v", err)
	}
	writeTestFile(t, filepath.Join(workspacePath, "service.txt"), "changed\n")

	if _, err := provider.CreateLocalCommit(context.Background(), ports.CreateLocalCommitRequest{
		Handle: handle, Message: "record result [" + ports.LocalCommitMarkerTrailerKey + ": sha256:marker-a]",
		AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
	}); err != nil {
		t.Fatalf("CreateLocalCommit: %v", err)
	}

	found, _, _, err := provider.FindLocalCommitByMarker(context.Background(), handle, "sha256:marker-b")
	if err != nil {
		t.Fatalf("FindLocalCommitByMarker: %v", err)
	}
	if found {
		t.Fatal("found = true for a different marker than the one actually at HEAD, want false")
	}
}
