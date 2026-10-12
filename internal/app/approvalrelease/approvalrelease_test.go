package approvalrelease

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V11-07: the shared application layer of approve-and-commit, against a real sqlite store and a real
// Git worktree.

type fixture struct {
	ctx       context.Context
	store     *sqlite.Store
	uow       ports.UnitOfWork
	ids       idsource.Source
	provider  *gitworktree.Provider
	handle    ports.WorkspaceHandle
	path      string
	base      string
	identity  ports.GitIdentity
	identErr  error
	identityR *stubIdentities
}

type stubIdentities struct {
	identity ports.GitIdentity
	err      error
}

func (s *stubIdentities) ReadGitIdentity(context.Context, ports.WorkspaceHandle) (ports.GitIdentity, error) {
	return s.identity, s.err
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, source, "init", "--initial-branch=main")
	git(t, source, "config", "user.name", "Agent Kit Test")
	git(t, source, "config", "user.email", "agent-kit@example.invalid")
	git(t, source, "config", "core.autocrlf", "false")
	write(t, filepath.Join(source, "service.txt"), "base\n")
	git(t, source, "add", "-A")
	git(t, source, "commit", "-m", "initial fixture")
	base := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))

	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(root, "workspaces")})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-1"), LocalRepository: source, BaseRef: base,
		FamilyID: workdomain.TaskFamilyID("family-1"), WorkspaceSetID: workspace.WorkspaceSetID("set-1"), Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := provider.WorkingDirectory(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-root"); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspaceWithLocatorAtRevision(ctx, store, "project-1", "family-1", "set-1", "repo-1", "rw-1", handle.String(), base); err != nil {
		t.Fatal(err)
	}
	return &fixture{
		ctx: ctx, store: store, uow: sqlite.NewUnitOfWork(store), ids: idsource.NewSequential("id"), provider: provider,
		handle: handle, path: path, base: base,
		identityR: &stubIdentities{identity: ports.GitIdentity{Name: "Reviewer", Email: "reviewer@example.test", NameSource: "global", EmailSource: "global"}},
	}
}

func (fx *fixture) preparer() Preparer {
	return Preparer{UOW: fx.uow, Identities: fx.identityR, Inspection: fx.provider}
}

func TestPrepare_ReadsTheIdentityAndTheReviewedPatchOutsideAnyTransaction(t *testing.T) {
	fx := newFixture(t)
	write(t, filepath.Join(fx.path, "service.txt"), "base\nchanged\n")
	commit, err := fx.preparer().Prepare(fx.ctx, CommitInput{
		Message: "  record the change  ",
		Repositories: []RepositoryInput{{
			RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1,
			Files: []FileInput{{Path: "service.txt", Status: "MODIFIED", ContentHash: "h"}}, Unselected: "park",
		}},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if commit.Message != "record the change" || len(commit.Repositories) != 1 {
		t.Fatalf("commit = %+v", commit)
	}
	r := commit.Repositories[0]
	if r.AuthorName != "Reviewer" || r.AuthorEmail != "reviewer@example.test" || r.Unselected != workdomain.UnselectedPark || len(r.Files) != 1 {
		t.Fatalf("repository = %+v", r)
	}
	if !strings.Contains(string(r.Patch), "+changed") {
		t.Fatalf("reviewed patch = %q, want the working-tree change", r.Patch)
	}
}

func TestPrepare_EmptyRepositoriesIsTheNothingToCommitAnswer(t *testing.T) {
	fx := newFixture(t)
	commit, err := fx.preparer().Prepare(fx.ctx, CommitInput{})
	if err != nil || commit == nil || len(commit.Repositories) != 0 {
		t.Fatalf("Prepare = %+v, %v; want an empty commit", commit, err)
	}
}

func TestPrepare_AMissingIdentityIsReportedPerRepositoryWithTheFix(t *testing.T) {
	fx := newFixture(t)
	fx.identityR.identity = ports.GitIdentity{Name: "Only Name"}
	_, err := fx.preparer().Prepare(fx.ctx, CommitInput{
		Message:      "m",
		Repositories: []RepositoryInput{{RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1, Files: []FileInput{{Path: "a.txt"}}}},
	})
	var missing *IdentityMissingError
	if !errors.As(err, &missing) || !errors.Is(err, releasesetcommit.ErrCommitIdentityMissing) {
		t.Fatalf("err = %v, want an IdentityMissingError matching ErrCommitIdentityMissing", err)
	}
	if len(missing.Missing) != 1 || missing.Missing[0].RepositoryID != "repo-1" || !missing.Missing[0].MissingEmail || missing.Missing[0].MissingName {
		t.Fatalf("missing = %+v", missing.Missing)
	}
	for _, want := range []string{"COMMIT_IDENTITY_MISSING", "repo-1", "user.email", "git config --global"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestPrepare_RejectsMalformedSpecifications(t *testing.T) {
	fx := newFixture(t)
	repo := func(files ...FileInput) []RepositoryInput {
		return []RepositoryInput{{RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1, Files: files}}
	}
	cases := map[string]CommitInput{
		"no message":        {Repositories: repo(FileInput{Path: "a"})},
		"empty path":        {Message: "m", Repositories: repo(FileInput{Path: " "})},
		"bad handling":      {Message: "m", Repositories: []RepositoryInput{{RepositoryWorkspaceID: "rw-1", Files: []FileInput{{Path: "a"}}, Unselected: "DELETE"}}},
		"message with NUL":  {Message: "m\x00", Repositories: repo(FileInput{Path: "a"})},
		"path with NUL":     {Message: "m", Repositories: repo(FileInput{Path: "a\x00b"})},
		"too many in total": {Message: "m", Repositories: make([]RepositoryInput, maxRepositories+1)},
	}
	for name, input := range cases {
		if _, err := fx.preparer().Prepare(fx.ctx, input); !errors.Is(err, ErrInvalidCommit) {
			t.Errorf("%s: err = %v, want ErrInvalidCommit", name, err)
		}
	}
}

// parkOne commits nothing: it requests an approval-bound PARK operation, parks the leftover change with
// the real provider and records it, the way the commit worker does.
func (fx *fixture) parkOne(t *testing.T) (operationID string) {
	t.Helper()
	write(t, filepath.Join(fx.path, "leftover.txt"), "to be parked\n")
	var bound releasesetcommit.ApprovalBoundResult
	if err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		var err error
		bound, err = releasesetcommit.RequestApprovalBoundCommitsTx(fx.ctx, tx, fx.ids, ports.Command{
			ID: "cmd-1", Actor: "operator-1", RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}, releasesetcommit.ApprovalBoundRequest{
			ProjectID: "project-1", FamilyID: "family-1", WorkItemID: "work-root", RunID: "run-1", ApprovalRequestID: "approval-1", Message: "m",
			Repositories: []releasesetcommit.ApprovalBoundRepository{{
				RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1, AuthorName: "Reviewer", AuthorEmail: "r@example.test",
				Files: []workdomain.LocalCommitFile{{Path: "service.txt"}}, Unselected: workdomain.UnselectedPark, Patch: []byte("patch"),
			}},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	operationID = bound.Operations[0].ReleaseSetLocalCommitID
	ref := releasesetcommit.ParkedRef(workdomain.ReleaseSetID(bound.ReleaseSetID))
	parked, err := fx.provider.ParkRemainingChanges(fx.ctx, fx.handle, ref)
	if err != nil || len(parked.Paths) != 1 {
		t.Fatalf("park = %+v, %v", parked, err)
	}
	if err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		return tx.Work().RecordReleaseSetLocalCommitParked(fx.ctx, operationID, parked.Ref, parked.Paths)
	}); err != nil {
		t.Fatal(err)
	}
	return operationID
}

func TestFamilyReleaseContext_ListsTheIdentityAndTheParkedSets(t *testing.T) {
	fx := newFixture(t)
	operationID := fx.parkOne(t)
	q := Queries{UOW: fx.uow, Identities: fx.identityR}
	got, err := q.FamilyReleaseContext(fx.ctx, "project-1", "family-1")
	if err != nil {
		t.Fatalf("FamilyReleaseContext: %v", err)
	}
	if len(got.Repositories) != 1 || got.Repositories[0].RepositoryID != "repo-1" || !got.Repositories[0].Identity.Ready ||
		got.Repositories[0].Identity.Name != "Reviewer" || got.Repositories[0].Identity.NameSource != "global" {
		t.Fatalf("repositories = %+v", got.Repositories)
	}
	if len(got.Parked) != 1 || got.Parked[0].ReleaseSetLocalCommitID != operationID || len(got.Parked[0].Paths) != 1 || got.Parked[0].Paths[0] != "leftover.txt" {
		t.Fatalf("parked = %+v", got.Parked)
	}

	fx.identityR.identity = ports.GitIdentity{}
	got, err = q.FamilyReleaseContext(fx.ctx, "project-1", "family-1")
	if err != nil || got.Repositories[0].Identity.Ready || !strings.Contains(got.Repositories[0].Identity.Problem, "user.name and user.email") {
		t.Fatalf("identity view without a git identity = %+v, %v", got.Repositories[0].Identity, err)
	}
	if _, err := q.FamilyReleaseContext(fx.ctx, "project-other", "family-1"); !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("a foreign project: err = %v, want ErrCrossProjectReference", err)
	}
}

// The happy path of ApprovalRelease needs a real approval request row; it is covered end to end by the HTTP
// test of the route (httpapi/decision), which starts a run through an APPROVAL node.
func TestApprovalRelease_AnUnknownApprovalIsNotFound(t *testing.T) {
	fx := newFixture(t)
	fx.parkOne(t)
	if _, err := (Queries{UOW: fx.uow}).ApprovalRelease(fx.ctx, "run-1", "approval-1"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ErrPersistenceNotFound (no approval request row exists in this fixture)", err)
	}
}

func restoreCommand(key string) ports.Command {
	return ports.Command{
		ID: "cmd-" + key, IdempotencyKey: key, Actor: "operator-1", Scope: ports.ProjectScope("project-1"),
		RequestedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Type: "RestoreParkedChanges", RequestHash: "hash-" + key,
	}
}

func TestRestoreParked_PutsTheFilesBackOnceAndIsIdempotent(t *testing.T) {
	fx := newFixture(t)
	operationID := fx.parkOne(t)
	if _, err := os.Stat(filepath.Join(fx.path, "leftover.txt")); !os.IsNotExist(err) {
		t.Fatal("the file should be parked away")
	}
	deps := RestoreDeps{UOW: fx.uow, Parker: fx.provider}
	req := RestoreRequest{ProjectID: "project-1", RepositoryWorkspaceID: "rw-1", ReleaseSetLocalCommitID: operationID}
	first, err := RestoreParked(fx.ctx, deps, restoreCommand("restore-1"), req)
	if err != nil {
		t.Fatalf("RestoreParked: %v", err)
	}
	if len(first.RestoredPaths) != 1 || first.RestoredPaths[0] != "leftover.txt" {
		t.Fatalf("result = %+v", first)
	}
	if data, err := os.ReadFile(filepath.Join(fx.path, "leftover.txt")); err != nil || string(data) != "to be parked\n" {
		t.Fatalf("restored file = %q, %v", data, err)
	}
	replay, err := RestoreParked(fx.ctx, deps, restoreCommand("restore-1"), req)
	if err != nil || len(replay.RestoredPaths) != 1 {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	if _, err := RestoreParked(fx.ctx, deps, restoreCommand("restore-2"), req); !errors.Is(err, ErrNothingToRestore) {
		t.Fatalf("a second restore: err = %v, want ErrNothingToRestore", err)
	}
	if _, err := RestoreParked(fx.ctx, deps, ports.Command{
		ID: "cmd-x", IdempotencyKey: "restore-1", Actor: "operator-1", Scope: ports.ProjectScope("project-1"),
		RequestedAt: time.Now(), Type: "RestoreParkedChanges", RequestHash: "another-hash",
	}, req); !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("same key, different request: err = %v, want ErrReceiptConflict", err)
	}
	q := Queries{UOW: fx.uow, Identities: fx.identityR}
	if ctxResult, err := q.FamilyReleaseContext(fx.ctx, "project-1", "family-1"); err != nil || len(ctxResult.Parked) != 0 {
		t.Fatalf("parked after restore = %+v, %v; want none", ctxResult.Parked, err)
	}
}

func TestRestoreParked_RefusesWhenAnotherProjectOrTheWrongWorkspaceOrNothingParked(t *testing.T) {
	fx := newFixture(t)
	operationID := fx.parkOne(t)
	deps := RestoreDeps{UOW: fx.uow, Parker: fx.provider}
	if _, err := RestoreParked(fx.ctx, deps, restoreCommand("r1"), RestoreRequest{ProjectID: "project-other", RepositoryWorkspaceID: "rw-1", ReleaseSetLocalCommitID: operationID}); !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("foreign project: err = %v", err)
	}
	if _, err := RestoreParked(fx.ctx, deps, restoreCommand("r2"), RestoreRequest{ProjectID: "project-1", RepositoryWorkspaceID: "rw-1", ReleaseSetLocalCommitID: "no-such-operation"}); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("unknown operation: err = %v", err)
	}
	if _, err := RestoreParked(fx.ctx, deps, restoreCommand("r3"), RestoreRequest{}); err == nil {
		t.Fatal("an empty request must be refused")
	}
	if _, err := os.Stat(filepath.Join(fx.path, "leftover.txt")); !os.IsNotExist(err) {
		t.Fatal("a refused restore must write nothing")
	}
}
