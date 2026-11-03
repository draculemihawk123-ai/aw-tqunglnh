package gitworktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// V12-28 (ADR-060): push to a real bare origin, never forced.
func TestForgeGit_PushesHeadAndNeverForces(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	ctx := context.Background()
	origin := filepath.Join(t.TempDir(), "origin.git")
	runTestGit(t, "", "init", "-q", "--bare", origin)
	runTestGit(t, f.sourceRepo, "remote", "add", "origin", origin)
	runTestGit(t, f.sourceRepo, "push", "-q", "origin", "main")

	if url, err := f.provider.RemoteURL(ctx, f.handle, "origin"); err != nil || url != origin {
		t.Fatalf("RemoteURL = %q, %v", url, err)
	}
	if _, err := f.provider.RemoteURL(ctx, f.handle, "upstream"); err == nil {
		t.Fatal("an unknown remote must be an error")
	}
	if _, exists, err := f.provider.RemoteBranchHead(ctx, f.handle, "origin", "agentkit/w-1"); err != nil || exists {
		t.Fatalf("RemoteBranchHead before the push = %v, %v", exists, err)
	}

	head := f.externalCommit(t, "feature.txt", "x\n", "family work")
	pushed, err := f.provider.PushHead(ctx, f.handle, "origin", "agentkit/w-1")
	if err != nil || pushed != head {
		t.Fatalf("PushHead = %q, %v; want %s", pushed, err, head)
	}
	if sha, exists, err := f.provider.RemoteBranchHead(ctx, f.handle, "origin", "agentkit/w-1"); err != nil || !exists || sha != head {
		t.Fatalf("RemoteBranchHead = %q %v %v", sha, exists, err)
	}
	if ahead, err := f.provider.CommitsAhead(ctx, f.handle, "origin/main"); err != nil || len(ahead) != 1 || ahead[0] != head {
		t.Fatalf("CommitsAhead = %v, %v", ahead, err)
	}

	// Someone else moves the remote branch: a push that would drop their commit is refused (no force).
	other := filepath.Join(t.TempDir(), "other")
	runTestGit(t, "", "clone", "-q", "--branch", "agentkit/w-1", origin, other)
	runTestGit(t, other, "-c", "user.name=X", "-c", "user.email=x@example.invalid", "commit", "-q", "--allow-empty", "-m", "theirs")
	runTestGit(t, other, "push", "-q", "origin", "agentkit/w-1")
	f.externalCommit(t, "feature2.txt", "y\n", "more family work")
	if _, err := f.provider.PushHead(ctx, f.handle, "origin", "agentkit/w-1"); err == nil {
		t.Fatal("a non-fast-forward push must be refused")
	}
	if _, err := f.provider.PushHead(ctx, f.handle, "origin", "-delete"); err == nil || !strings.Contains(err.Error(), "branch") {
		t.Fatalf("a branch name that reads as an option must be refused, got %v", err)
	}
}
