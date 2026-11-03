package gitworktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// commitOnSource commits a file on the source repository's main branch (a merge done elsewhere, e.g. a PR merged on GitHub).
func commitOnSource(t *testing.T, repo, file, content string) string {
	t.Helper()
	writeTestFile(t, filepath.Join(repo, file), content)
	runTestGit(t, repo, "add", "--", file)
	runTestGit(t, repo, "commit", "-q", "-m", "on main: "+file)
	return strings.TrimSpace(runTestGit(t, repo, "rev-parse", "HEAD"))
}

func TestSyncWithBase_FastForwardsAfterTheFamilyWorkWasMerged(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	ctx := context.Background()
	familyHead := f.externalCommit(t, "feature.txt", "x\n", "family work")

	status, err := f.provider.BaseStatus(ctx, ports.BaseSyncRequest{Handle: f.handle, BaseRef: "main"})
	if err != nil {
		t.Fatalf("BaseStatus: %v", err)
	}
	if status.Merged || status.Ahead != 1 || status.Behind != 0 || status.Outcome != ports.BaseSyncUpToDate {
		t.Fatalf("status before the merge = %+v, want 1 ahead, not merged", status)
	}

	// The PR is merged into main, and main moves on.
	runTestGit(t, f.sourceRepo, "merge", "--ff-only", "-q", familyHead)
	tip := commitOnSource(t, f.sourceRepo, "other.txt", "y\n")

	result, err := f.provider.SyncWithBase(ctx, ports.BaseSyncRequest{Handle: f.handle, BaseRef: "refs/heads/main", Fetch: true})
	if err != nil {
		t.Fatalf("SyncWithBase: %v", err)
	}
	if result.Outcome != ports.BaseSyncFastForwarded || result.Head != tip || !result.Merged || result.Behind != 0 || result.Fetched {
		t.Fatalf("result = %+v, want fast-forwarded to %s, merged, no fetch (no origin)", result, tip)
	}
}

func TestSyncWithBase_ReportsADivergedBranchAndDoesNotMoveIt(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	ctx := context.Background()
	familyHead := f.externalCommit(t, "feature.txt", "x\n", "family work")
	commitOnSource(t, f.sourceRepo, "other.txt", "y\n")

	result, err := f.provider.SyncWithBase(ctx, ports.BaseSyncRequest{Handle: f.handle, BaseRef: "main"})
	if err != nil {
		t.Fatalf("SyncWithBase: %v", err)
	}
	if result.Outcome != ports.BaseSyncNeedsMerge || result.Head != familyHead || result.Ahead != 1 || result.Behind != 1 || result.Merged {
		t.Fatalf("result = %+v, want NEEDS_MERGE with HEAD unchanged", result)
	}
}

func TestSyncWithBase_FetchesTheBaseBranchFromOrigin(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	ctx := context.Background()
	// origin is a clone of the source that moved on (the GitHub side).
	origin := filepath.Join(t.TempDir(), "origin")
	runTestGit(t, "", "clone", "-q", f.sourceRepo, origin)
	runTestGit(t, origin, "config", "user.name", "Origin")
	runTestGit(t, origin, "config", "user.email", "origin@example.invalid")
	tip := commitOnSource(t, origin, "merged-pr.txt", "z\n")
	runTestGit(t, f.sourceRepo, "remote", "add", "origin", origin)

	result, err := f.provider.SyncWithBase(ctx, ports.BaseSyncRequest{Handle: f.handle, BaseRef: "main", Fetch: true})
	if err != nil {
		t.Fatalf("SyncWithBase: %v", err)
	}
	if !result.Fetched || result.BaseRevision != tip || result.Outcome != ports.BaseSyncFastForwarded || result.Head != tip {
		t.Fatalf("result = %+v, want origin's main fetched and the worktree fast-forwarded to %s", result, tip)
	}
	if local := strings.TrimSpace(runTestGit(t, f.sourceRepo, "rev-parse", "refs/heads/main")); local == tip {
		t.Fatal("the local main branch must not be changed: only the remote-tracking ref is fetched")
	}
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	out := runTestGit(t, repo, "for-each-ref", "--format=%(refname)", "refs/heads/"+branch)
	return strings.TrimSpace(out) != ""
}

func TestRelease_DeletesTheBranchOnlyWhenItsWorkIsMerged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	merged := newInspectionFixture(t)
	head := merged.externalCommit(t, "feature.txt", "x\n", "family work")
	runTestGit(t, merged.sourceRepo, "merge", "--ff-only", "-q", head)
	metadata, err := merged.provider.readMetadata(merged.handle)
	if err != nil {
		t.Fatalf("readMetadata: %v", err)
	}
	if err := merged.provider.Release(ctx, merged.handle); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if branchExists(t, merged.sourceRepo, metadata.BranchRef) {
		t.Fatalf("branch %s should be deleted once main contains its work", metadata.BranchRef)
	}

	unmerged := newInspectionFixture(t)
	unmerged.externalCommit(t, "feature.txt", "x\n", "family work not merged")
	metadata, err = unmerged.provider.readMetadata(unmerged.handle)
	if err != nil {
		t.Fatalf("readMetadata: %v", err)
	}
	if err := unmerged.provider.Release(ctx, unmerged.handle); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !branchExists(t, unmerged.sourceRepo, metadata.BranchRef) {
		t.Fatalf("branch %s holds commits no other ref has: it must be kept", metadata.BranchRef)
	}
}
