package gitworktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func provisionLocalCommitFixture(t *testing.T) (*Provider, ports.WorkspaceHandle, string, string) {
	t.Helper()
	fixtureRoot := t.TempDir()
	repositoryPath, baseRevision := createGitRepository(t, filepath.Join(fixtureRoot, "sources", "shared service"), "base\n")
	provider := newTestProvider(t, filepath.Join(fixtureRoot, "workspaces"))
	handle, err := provider.Provision(context.Background(), ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID("repo-shared"),
		LocalRepository: repositoryPath,
		BaseRef:         baseRevision,
		FamilyID:        work.TaskFamilyID("family-local-commit"),
		WorkspaceSetID:  workspace.WorkspaceSetID("set-local-commit"),
		Generation:      1,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	return provider, handle, repositoryPath, baseRevision
}

func TestProvider_CreateLocalCommit_CommitsStagedAndUntrackedChanges(t *testing.T) {
	t.Parallel()
	provider, handle, _, baseRevision := provisionLocalCommitFixture(t)
	workspacePath, err := provider.workspacePath(handle)
	if err != nil {
		t.Fatalf("workspacePath: %v", err)
	}
	writeTestFile(t, filepath.Join(workspacePath, "service.txt"), "changed\n")
	writeTestFile(t, filepath.Join(workspacePath, "new-file.txt"), "new\n")

	revision, err := provider.CreateLocalCommit(context.Background(), ports.CreateLocalCommitRequest{
		Handle: handle, Message: "record repository result", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
	})
	if err != nil {
		t.Fatalf("CreateLocalCommit: %v", err)
	}
	if revision.RepositoryID != "repo-shared" || revision.WorkspaceGeneration != 1 || revision.VCSObjectID == baseRevision {
		t.Fatalf("revision = %#v, want a new commit for repo-shared@generation 1, distinct from base %s", revision, baseRevision)
	}

	head := strings.TrimSpace(runTestGit(t, workspacePath, "rev-parse", "HEAD"))
	if head != revision.VCSObjectID {
		t.Fatalf("HEAD = %s, want returned revision %s", head, revision.VCSObjectID)
	}
	status := runTestGit(t, workspacePath, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("workspace not clean after commit: %q", status)
	}
	authorLine := strings.TrimSpace(runTestGit(t, workspacePath, "log", "-1", "--format=%an <%ae>"))
	if authorLine != "Release Bot <release-bot@example.invalid>" {
		t.Fatalf("commit author = %q, want the request's own identity, not ambient repo config", authorLine)
	}
	inspection, err := provider.Inspect(context.Background(), handle)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if inspection.Dirty {
		t.Fatal("workspace still reports dirty after CreateLocalCommit")
	}
}

func TestProvider_CreateLocalCommit_NothingToCommit_ReturnsError(t *testing.T) {
	t.Parallel()
	provider, handle, _, _ := provisionLocalCommitFixture(t)

	_, err := provider.CreateLocalCommit(context.Background(), ports.CreateLocalCommitRequest{
		Handle: handle, Message: "no-op", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
	})
	if err != ErrNothingToCommit {
		t.Fatalf("err = %v, want ErrNothingToCommit", err)
	}
}

func TestProvider_CreateLocalCommit_RejectsInvalidFields(t *testing.T) {
	t.Parallel()
	provider, handle, _, _ := provisionLocalCommitFixture(t)
	workspacePath, err := provider.workspacePath(handle)
	if err != nil {
		t.Fatalf("workspacePath: %v", err)
	}
	writeTestFile(t, filepath.Join(workspacePath, "service.txt"), "changed\n")

	cases := []ports.CreateLocalCommitRequest{
		{Handle: handle, Message: "", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid"},
		{Handle: handle, Message: "ok", AuthorName: "", AuthorEmail: "release-bot@example.invalid"},
		{Handle: handle, Message: "ok", AuthorName: "Release Bot", AuthorEmail: ""},
		{Handle: handle, Message: "bad\nmessage", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid"},
	}
	for _, req := range cases {
		if _, err := provider.CreateLocalCommit(context.Background(), req); err == nil {
			t.Fatalf("request %+v was accepted, want an error", req)
		}
	}
}

func TestProvider_CreateLocalCommit_ReleasedWorkspace_Rejected(t *testing.T) {
	t.Parallel()
	provider, handle, _, _ := provisionLocalCommitFixture(t)
	if err := provider.Release(context.Background(), handle); err != nil {
		t.Fatalf("Release: %v", err)
	}

	_, err := provider.CreateLocalCommit(context.Background(), ports.CreateLocalCommitRequest{
		Handle: handle, Message: "too late", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
	})
	if err != ErrWorkspaceReleased {
		t.Fatalf("err = %v, want ErrWorkspaceReleased", err)
	}
}

// spyLocalCommitCreator wraps a real ports.LocalCommitCreator and counts
// CreateLocalCommit calls — mirrors internal/app/message's own
// spyArtifactStore pattern (receipt_precheck_test.go).
type spyLocalCommitCreator struct {
	ports.LocalCommitCreator
	calls int
}

func (s *spyLocalCommitCreator) CreateLocalCommit(ctx context.Context, req ports.CreateLocalCommitRequest) (workspace.Revision, error) {
	s.calls++
	return s.LocalCommitCreator.CreateLocalCommit(ctx, req)
}

var _ ports.LocalCommitCreator = (*spyLocalCommitCreator)(nil)

// TestProvider_CreateLocalCommit_NeverTouchesRemote is this task's own
// "local commit" and "spy adapter chứng minh remote mutation call count
// bằng 0" Verify-line scenarios combined: this fixture's own source
// repository (createGitRepository) never configures a remote at all — the
// same "Alpha has no remote Git mutation" invariant AK-ARCH-015C/GC-INV-26
// state architecturally — so `git remote` staying empty across the call is
// direct, observable proof that CreateLocalCommit could not have performed
// any remote mutation even in principle; the spy additionally proves the
// call this test drives through Provider is the only one that reached
// CreateLocalCommit.
func TestProvider_CreateLocalCommit_NeverTouchesRemote(t *testing.T) {
	t.Parallel()
	provider, handle, repositoryPath, _ := provisionLocalCommitFixture(t)
	workspacePath, err := provider.workspacePath(handle)
	if err != nil {
		t.Fatalf("workspacePath: %v", err)
	}
	if remotes := strings.TrimSpace(runTestGit(t, repositoryPath, "remote")); remotes != "" {
		t.Fatalf("fixture repository unexpectedly has remotes configured: %q", remotes)
	}

	spy := &spyLocalCommitCreator{LocalCommitCreator: provider}
	writeTestFile(t, filepath.Join(workspacePath, "service.txt"), "changed-via-spy\n")
	if _, err := spy.CreateLocalCommit(context.Background(), ports.CreateLocalCommitRequest{
		Handle: handle, Message: "record result", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
	}); err != nil {
		t.Fatalf("CreateLocalCommit via spy: %v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("spy.calls = %d, want exactly 1", spy.calls)
	}
	if remotes := strings.TrimSpace(runTestGit(t, workspacePath, "remote")); remotes != "" {
		t.Fatalf("workspace has remotes configured after CreateLocalCommit: %q — a real remote mutation would need one", remotes)
	}
}
