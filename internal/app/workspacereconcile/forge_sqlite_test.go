package workspacereconcile_test

// V12-28 (ADR-060): forgerelease against real sqlite and a real Git worktree with a bare origin; the forge (GitHub) is a stand-in.
// The fixture lives in this package (realFixture); the commands under test are internal/app/forgerelease.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/forgerelease"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// approvedTx overrides only which commits went through an approval gate (that SQL is tested on its own); everything else is sqlite.
type approvedTx struct {
	ports.Tx
	approved *[]string
}

func (t approvedTx) Forge() ports.ForgeRepository { return approvedForge{t.Tx.Forge(), t.approved} }

type approvedForge struct {
	ports.ForgeRepository
	approved *[]string
}

func (f approvedForge) ApprovedCommitRevisions(context.Context, string) ([]string, error) {
	return append([]string(nil), *f.approved...), nil
}

type approvedUoW struct {
	inner    ports.UnitOfWork
	approved *[]string
}

func (u approvedUoW) WithReadOnly(ctx context.Context, fn func(ports.Tx) error) error {
	return u.inner.WithReadOnly(ctx, func(tx ports.Tx) error { return fn(approvedTx{tx, u.approved}) })
}

func (u approvedUoW) WithSerializedWrite(ctx context.Context, fn func(ports.Tx) error) error {
	return u.inner.WithSerializedWrite(ctx, func(tx ports.Tx) error { return fn(approvedTx{tx, u.approved}) })
}

type fakeForge struct {
	created []ports.CreatePullRequestRequest
	pr      ports.PullRequest
	merged  []string
}

func (f *fakeForge) CreatePullRequest(_ context.Context, req ports.CreatePullRequestRequest) (ports.PullRequest, error) {
	f.created = append(f.created, req)
	f.pr = ports.PullRequest{Number: 5, URL: "https://github.com/o/r/pull/5", State: "OPEN", Draft: req.Draft, Head: req.Head, Base: req.Base, Title: req.Title, Checks: []ports.ForgeCheck{}}
	return f.pr, nil
}

func (f *fakeForge) GetPullRequest(context.Context, string, int) (ports.PullRequest, error) { return f.pr, nil }

func (f *fakeForge) MergePullRequest(_ context.Context, _ string, number int, method, head string) error {
	f.merged = append(f.merged, method+":"+head)
	f.pr.State = "MERGED"
	return nil
}

func forgeCommand(f *realFixture, key, typ string) ports.Command {
	cmd := f.adoptCommand(key, f.rw.Version)
	cmd.Type, cmd.ExpectedVersion = typ, 0
	return cmd
}

func TestForgeRelease_PushOpenAndMergeUnderTheRules(t *testing.T) {
	f := newRealFixture(t, "forge.db")
	ctx := context.Background()
	origin := filepath.Join(t.TempDir(), "origin.git")
	runReconcileFixtureGit(t, "", "init", "-q", "--bare", origin)
	runReconcileFixtureGit(t, f.repoPath, "remote", "add", "origin", origin)
	runReconcileFixtureGit(t, f.repoPath, "push", "-q", "origin", "main")
	runReconcileFixtureGit(t, f.repoPath, "fetch", "-q", "origin")

	approved := []string{}
	forge := &fakeForge{}
	deps := forgerelease.Deps{UnitOfWork: approvedUoW{f.uow, &approved}, Auditor: f.provider, Git: f.provider, Forge: forge}

	// A commit made outside aw and adopted: it did not go through an approval gate, so it is not pushed.
	head := f.commitOutsideAw(t, "work.txt", "task work")
	adopted, err := f.adopt(t, "adopt-1", f.rw.Version)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	f.rw.Version = adopted.Version
	_, err = forgerelease.Push(ctx, deps, forgeCommand(f, "push-0", "PushFamilyBranch"), forgerelease.PushRequest{ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID)})
	if !errors.Is(err, forgerelease.ErrUnapprovedCommits) {
		t.Fatalf("push of an unapproved commit: err = %v", err)
	}
	if _, err := forgerelease.Push(ctx, deps, forgeCommand(f, "push-main", "PushFamilyBranch"), forgerelease.PushRequest{
		ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID), RemoteBranch: "main"}); err == nil {
		t.Fatal("pushing to the base branch must be refused")
	}

	// The commit went through the gate.
	approved = []string{head}
	// A branch already on origin that aw did not create is never overwritten.
	runReconcileFixtureGit(t, f.repoPath, "push", "-q", "origin", "main:refs/heads/someone-elses")
	if _, err := forgerelease.Push(ctx, deps, forgeCommand(f, "push-taken", "PushFamilyBranch"), forgerelease.PushRequest{
		ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID), RemoteBranch: "someone-elses"}); !errors.Is(err, ports.ErrRemoteBranchExists) {
		t.Fatalf("push onto a branch aw did not create: err = %v", err)
	}
	// Pushed under the chosen name.
	pushed, err := forgerelease.Push(ctx, deps, forgeCommand(f, "push-1", "PushFamilyBranch"), forgerelease.PushRequest{
		ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID), RemoteBranch: "feature/order-status"})
	if err != nil || pushed.Pushed != head || !pushed.Created || pushed.RemoteBranch != "feature/order-status" {
		t.Fatalf("Push = %+v, %v", pushed, err)
	}
	if got := strings.TrimSpace(runReconcileFixtureGit(t, origin, "rev-parse", "refs/heads/feature/order-status")); got != head {
		t.Fatalf("origin has %s, want %s", got, head)
	}
	if _, err := forgerelease.Push(ctx, deps, forgeCommand(f, "push-2", "PushFamilyBranch"), forgerelease.PushRequest{
		ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID), RemoteBranch: "other"}); !errors.Is(err, forgerelease.ErrBranchFixed) {
		t.Fatalf("renaming the remote branch: err = %v", err)
	}

	pr, err := forgerelease.CreatePullRequest(ctx, deps, forgeCommand(f, "pr-1", "CreateWorkspacePullRequest"), forgerelease.CreatePullRequestRequest{
		ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID), Title: "feat: order status"})
	if err != nil || pr.Number != 5 || forge.created[0].Base != "main" || forge.created[0].Head != "feature/order-status" || forge.created[0].Draft {
		t.Fatalf("CreatePullRequest = %+v, %v (%+v)", pr, err, forge.created)
	}

	// No CI on the repository: the merge needs the explicit acknowledgment; then it merges with a merge commit on the pushed head.
	forge.pr.HeadSHA = head
	view, err := forgerelease.GetRemote(ctx, deps, f.projectID, string(f.rw.ID))
	if err != nil || !view.NoChecks || len(view.MergeBlockers) != 0 || !view.Pushed {
		t.Fatalf("GetRemote = %+v, %v", view, err)
	}
	if _, err := forgerelease.Merge(ctx, deps, forgeCommand(f, "merge-0", "MergeWorkspacePullRequest"), forgerelease.MergeRequest{
		ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID)}); !errors.Is(err, forgerelease.ErrNoChecksNotAcked) {
		t.Fatalf("merge without CI and without acknowledgment: err = %v", err)
	}
	forge.pr.Checks = []ports.ForgeCheck{{Name: "build", Status: "COMPLETED", Conclusion: "FAILURE"}}
	if _, err := forgerelease.Merge(ctx, deps, forgeCommand(f, "merge-red", "MergeWorkspacePullRequest"), forgerelease.MergeRequest{
		ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID)}); !errors.Is(err, forgerelease.ErrChecksNotGreen) {
		t.Fatalf("merge with a red check: err = %v", err)
	}
	forge.pr.Checks = []ports.ForgeCheck{}
	merged, err := forgerelease.Merge(ctx, deps, forgeCommand(f, "merge-1", "MergeWorkspacePullRequest"), forgerelease.MergeRequest{
		ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID), AcknowledgeNoChecks: true})
	if err != nil || merged.Method != "merge" || !merged.NoChecks || len(forge.merged) != 1 || forge.merged[0] != "merge:"+head {
		t.Fatalf("Merge = %+v, %v (%v)", merged, err, forge.merged)
	}
	// A replay of the same command does not merge twice.
	if _, err := forgerelease.Merge(ctx, deps, forgeCommand(f, "merge-1", "MergeWorkspacePullRequest"), forgerelease.MergeRequest{
		ProjectID: f.projectID, RepositoryWorkspaceID: string(f.rw.ID), AcknowledgeNoChecks: true}); err != nil || len(forge.merged) != 1 {
		t.Fatalf("replayed merge: err %v, merges %d", err, len(forge.merged))
	}
}

// The approved-commits query itself, on the real schema.
func TestForgeRepository_ApprovedCommitRevisionsQueryRuns(t *testing.T) {
	f := newRealFixture(t, "forge-sql.db")
	if err := f.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		got, err := tx.Forge().ApprovedCommitRevisions(context.Background(), string(f.rw.ID))
		if err == nil && len(got) != 0 {
			t.Fatalf("approved = %v, want none", got)
		}
		return err
	}); err != nil {
		t.Fatalf("ApprovedCommitRevisions: %v", err)
	}
}
