package releasesetcommit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V11-07: a local commit that commits only the files an approver selected — the engine side of
// "approve and commit in one action" — against a real sqlite store and a real Git worktree.

func fileHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func (fx *executeFixture) requestSelective(t *testing.T, key, message string, selection *LocalCommitSelectionInput) RequestReleaseSetLocalCommitResult {
	t.Helper()
	result, err := RequestReleaseSetLocalCommit(fx.ctx, fx.uow, fx.ids,
		testCommand(key, "hash-"+key, ports.ProjectScope("project-1"), "RequestReleaseSetLocalCommit", 0),
		RequestReleaseSetLocalCommitRequest{
			ProjectID: "project-1", ReleaseSetID: fx.releaseSetID, ExpectedReleaseSetVersion: 1,
			RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1,
			Message: message, AuthorName: "Reviewer", AuthorEmail: "reviewer@example.invalid", Selection: selection,
		})
	if err != nil {
		t.Fatalf("RequestReleaseSetLocalCommit: %v", err)
	}
	return result
}

// fileSelection writes the files into the worktree and returns the selection of the first
// selectedCount of them, each with the hash of what was written (what the approver "saw").
func (fx *executeFixture) writeAndSelect(t *testing.T, files map[string]string, selected ...string) *LocalCommitSelectionInput {
	t.Helper()
	for name, content := range files {
		writeTestFile(t, filepath.Join(fx.workspacePath, filepath.FromSlash(name)), content)
	}
	selection := &LocalCommitSelectionInput{Unselected: workdomain.UnselectedKeep}
	for _, name := range selected {
		status := "ADDED"
		if name == "service.txt" {
			status = "MODIFIED"
		}
		selection.Files = append(selection.Files, workdomain.LocalCommitFile{Path: name, Status: status, ContentHash: fileHash(files[name])})
	}
	return selection
}

func (fx *executeFixture) headFiles(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, name := range strings.Split(strings.Trim(fx.runGit(t, "show", "--name-only", "-z", "--format=", "HEAD"), "\x00"), "\x00") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (fx *executeFixture) status(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(fx.runGit(t, "status", "--porcelain=v1", "--untracked-files=all"))
}

func (fx *executeFixture) loadSelection(t *testing.T, operationID string) (workdomain.ReleaseSetLocalCommitSelection, bool) {
	t.Helper()
	selection, found, err := loadSelection(fx.ctx, fx.uow, operationID)
	if err != nil {
		t.Fatalf("loadSelection: %v", err)
	}
	return selection, found
}

func TestSelectiveLocalCommit_CommitsOnlyTheSelectedFilesAndKeepsTheRest(t *testing.T) {
	fx := newExecuteFixture(t)
	selection := fx.writeAndSelect(t, map[string]string{
		"service.txt": "line-1\nchanged by the agent\n", "docs/spec.md": "# spec\n", "scratch/notes.txt": "not for the commit\n",
	}, "service.txt", "docs/spec.md")
	result := fx.request2(t, "req-sel", "keep the rest", selection)

	job := fx.claim(t, "worker-1", 10*time.Minute)
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteReleaseSetLocalCommit: %v", err)
	}
	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if intent.State != workdomain.ReleaseSetLocalCommitCommitted {
		t.Fatalf("state = %s (reason %q), want COMMITTED", intent.State, intent.FailureReason)
	}
	if got, want := fx.headFiles(t), []string{"docs/spec.md", "service.txt"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("HEAD holds %v, want %v", got, want)
	}
	if status := fx.status(t); status != "?? scratch/notes.txt" {
		t.Fatalf("worktree status = %q, want only the unselected untracked file (KEEP)", status)
	}
	stored, found := fx.loadSelection(t, result.ReleaseSetLocalCommitID)
	if !found || stored.Unselected != workdomain.UnselectedKeep || len(stored.Files) != 2 || stored.ChangeSetDigest != SelectionDigest(selection.Files) {
		t.Fatalf("stored selection = %+v (found %v)", stored, found)
	}
	// the recorded revision follows the commit, as for any local commit
	if record := fx.loadRepositoryWorkspace(t); record.Workspace.CurrentRevision != fx.headCommit(t) {
		t.Fatalf("current revision = %s, want the new HEAD %s", record.Workspace.CurrentRevision, fx.headCommit(t))
	}
}

// request2 is requestSelective, named so tests read as a sentence.
func (fx *executeFixture) request2(t *testing.T, key, message string, selection *LocalCommitSelectionInput) RequestReleaseSetLocalCommitResult {
	return fx.requestSelective(t, key, message, selection)
}

func TestSelectiveLocalCommit_DriftAfterTheReviewFailsBeforeAnythingIsCommitted(t *testing.T) {
	fx := newExecuteFixture(t)
	selection := fx.writeAndSelect(t, map[string]string{"service.txt": "line-1\nwhat the approver saw\n"}, "service.txt")
	result := fx.request2(t, "req-drift", "drift", selection)

	// somebody edits the file between the review and the commit
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "line-1\nswapped after the review\n")
	head := fx.headCommit(t)

	var hookReason workdomain.ReleaseSetLocalCommitFailureReason
	deps := fx.deps()
	deps.OnFailed = func(_ context.Context, _ ports.Tx, intent workdomain.ReleaseSetLocalCommit, reason workdomain.ReleaseSetLocalCommitFailureReason) error {
		hookReason = reason
		if string(intent.ID) != result.ReleaseSetLocalCommitID {
			t.Errorf("hook got operation %s, want %s", intent.ID, result.ReleaseSetLocalCommitID)
		}
		return nil
	}
	job := fx.claim(t, "worker-1", 10*time.Minute)
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, job); err != nil {
		t.Fatalf("a drift is a determinate terminal outcome, not a retryable error: %v", err)
	}
	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if intent.State != workdomain.ReleaseSetLocalCommitFailed || intent.FailureReason != workdomain.FailureChangeSetDrift {
		t.Fatalf("intent = %s/%q, want FAILED/CHANGESET_DRIFT", intent.State, intent.FailureReason)
	}
	if hookReason != workdomain.FailureChangeSetDrift {
		t.Fatalf("OnFailed reason = %q, want CHANGESET_DRIFT", hookReason)
	}
	if fx.headCommit(t) != head {
		t.Fatal("a drifted selection must not create a commit")
	}
	assertJobStates(t, fx, map[string]int{"SUCCEEDED": 1})

	// The operator reviews again and re-requests the same message: it must not collide with the
	// failed operation's marker (V9-09's attempt ordinal now covers drift too).
	retry := fx.writeAndSelect(t, map[string]string{"service.txt": "line-1\nswapped after the review\n"}, "service.txt")
	again := fx.request2(t, "req-drift-retry", "drift", retry)
	if again.Marker == result.Marker {
		t.Fatal("the retry after a CHANGESET_DRIFT must get a fresh marker")
	}
}

func TestSelectiveLocalCommit_ParkMovesTheRestOutOfTheWorktreeAndRecordsIt(t *testing.T) {
	fx := newExecuteFixture(t)
	selection := fx.writeAndSelect(t, map[string]string{
		"service.txt": "line-1\nchosen\n", "scratch/a.txt": "parked a\n", "scratch/b.txt": "parked b\n",
	}, "service.txt")
	selection.Unselected = workdomain.UnselectedPark
	result := fx.request2(t, "req-park", "park the rest", selection)

	deps := fx.deps()
	deps.Parker = fx.provider
	job := fx.claim(t, "worker-1", 10*time.Minute)
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, job); err != nil {
		t.Fatalf("ExecuteReleaseSetLocalCommit: %v", err)
	}
	if got := fx.headFiles(t); strings.Join(got, ",") != "service.txt" {
		t.Fatalf("HEAD holds %v, want only service.txt", got)
	}
	if status := fx.status(t); status != "" {
		t.Fatalf("worktree after PARK = %q, want clean so a read-only node can run", status)
	}
	stored, _ := fx.loadSelection(t, result.ReleaseSetLocalCommitID)
	if stored.ParkedRef != ParkedRef(workdomain.ReleaseSetID(fx.releaseSetID)) || strings.Join(stored.ParkedPaths, ",") != "scratch/a.txt,scratch/b.txt" {
		t.Fatalf("recorded parked = %q %v", stored.ParkedRef, stored.ParkedPaths)
	}
	var parked []workdomain.ParkedChange
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		var err error
		parked, err = tx.Work().ListParkedChanges(fx.ctx, "family-1")
		return err
	}); err != nil || len(parked) != 1 || parked[0].Ref != stored.ParkedRef {
		t.Fatalf("ListParkedChanges = %+v, %v; want the one parked set", parked, err)
	}

	// restore puts both files back
	if _, err := fx.provider.RestoreParkedChanges(fx.ctx, mustHandle(t, fx), stored.ParkedRef); err != nil {
		t.Fatalf("RestoreParkedChanges: %v", err)
	}
	if status := fx.status(t); status != "?? scratch/a.txt\n?? scratch/b.txt" && status != "?? scratch/" {
		// git collapses an all-untracked directory without --untracked-files=all; we pass it, so both appear
		t.Fatalf("worktree after restore = %q, want both parked files back as untracked", status)
	}
}

func mustHandle(t *testing.T, fx *executeFixture) ports.WorkspaceHandle {
	t.Helper()
	record := fx.loadRepositoryWorkspace(t)
	handle, err := ports.NewWorkspaceHandle(record.Workspace.Locator)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle: %v", err)
	}
	return handle
}

// A crash after the commit but before the parked paths were recorded: the retry reuses the commit
// (marker found) and still parks the rest, and still records it.
func TestSelectiveLocalCommit_ParkSurvivesACrashBetweenCommitAndFinalize(t *testing.T) {
	fx := newExecuteFixture(t)
	selection := fx.writeAndSelect(t, map[string]string{"service.txt": "line-1\nchosen\n", "scratch/a.txt": "parked\n"}, "service.txt")
	selection.Unselected = workdomain.UnselectedPark
	result := fx.request2(t, "req-crash", "park after a crash", selection)

	flaky := &flakyParker{UnselectedChangeParker: fx.provider, failFirst: true}
	deps := fx.deps()
	deps.Parker = flaky
	first := fx.claim(t, "worker-1", 600*time.Millisecond)
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, first); err == nil {
		t.Fatal("the first run must fail: the parker is down")
	}
	committed := fx.headCommit(t)
	if intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID); intent.State != workdomain.ReleaseSetLocalCommitRequested {
		t.Fatalf("state after the crash = %s, want still REQUESTED", intent.State)
	}

	// the lease of the failed worker lapses; another claims the job
	time.Sleep(1500 * time.Millisecond)
	if _, err := fx.store.RecoverExpiredJobs(fx.ctx); err != nil {
		t.Fatalf("RecoverExpiredJobs: %v", err)
	}
	second := fx.claim(t, "worker-2", 10*time.Minute)
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, second); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if fx.headCommit(t) != committed || fx.commitCount(t) != "2" {
		t.Fatalf("the retry must reuse the commit, not make another (head %s -> %s, count %s)", committed, fx.headCommit(t), fx.commitCount(t))
	}
	if status := fx.status(t); status != "" {
		t.Fatalf("the retry must finish parking: status %q", status)
	}
	stored, _ := fx.loadSelection(t, result.ReleaseSetLocalCommitID)
	if strings.Join(stored.ParkedPaths, ",") != "scratch/a.txt" {
		t.Fatalf("parked paths recorded after the retry = %v", stored.ParkedPaths)
	}
}

type flakyParker struct {
	ports.UnselectedChangeParker
	failFirst bool
}

func (p *flakyParker) ParkRemainingChanges(ctx context.Context, handle ports.WorkspaceHandle, ref string) (ports.ParkedChanges, error) {
	if p.failFirst {
		p.failFirst = false
		return ports.ParkedChanges{}, errors.New("parker is down")
	}
	return p.UnselectedChangeParker.ParkRemainingChanges(ctx, handle, ref)
}

func TestSelectiveLocalCommit_ParkWithoutAParkerFailsRetryablyAndLeavesTheOperationOpen(t *testing.T) {
	fx := newExecuteFixture(t)
	selection := fx.writeAndSelect(t, map[string]string{"service.txt": "line-1\nchosen\n", "scratch/a.txt": "x\n"}, "service.txt")
	selection.Unselected = workdomain.UnselectedPark
	result := fx.request2(t, "req-noparker", "needs a parker", selection)
	job := fx.claim(t, "worker-1", 10*time.Minute)
	err := ExecuteReleaseSetLocalCommit(fx.ctx, fx.deps(), job)
	if err == nil || !strings.Contains(err.Error(), "no parker") {
		t.Fatalf("err = %v, want a configuration error naming the missing parker", err)
	}
	if intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID); intent.State != workdomain.ReleaseSetLocalCommitRequested {
		t.Fatalf("state = %s, want REQUESTED", intent.State)
	}
}

// ---- approval-bound request

func (fx *executeFixture) approvalBound(t *testing.T, mutate func(*ApprovalBoundRequest)) (ApprovalBoundResult, error) {
	t.Helper()
	req := ApprovalBoundRequest{
		ProjectID: "project-1", FamilyID: "family-1", WorkItemID: "work-root", RunID: "run-1", ApprovalRequestID: "approval-1",
		Message: "record the reviewed contract",
		Repositories: []ApprovalBoundRepository{{
			RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1, AuthorName: "Reviewer", AuthorEmail: "reviewer@example.invalid",
			Files:      []workdomain.LocalCommitFile{{Path: "service.txt", Status: "MODIFIED", ContentHash: fileHash("x")}},
			Unselected: workdomain.UnselectedKeep, Patch: []byte("diff --git a/service.txt b/service.txt\n"),
		}},
	}
	if mutate != nil {
		mutate(&req)
	}
	var result ApprovalBoundResult
	err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		var inner error
		result, inner = RequestApprovalBoundCommitsTx(fx.ctx, tx, fx.ids,
			testCommand("resolve-1", "hash-resolve-1", ports.ProjectScope("project-1"), "ResolveApproval", 0), req)
		return inner
	})
	return result, err
}

func TestRequestApprovalBoundCommits_CreatesASealedReleaseSetAndOneRequestedOperationPerRepository(t *testing.T) {
	fx := newExecuteFixture(t)
	result, err := fx.approvalBound(t, nil)
	if err != nil {
		t.Fatalf("RequestApprovalBoundCommitsTx: %v", err)
	}
	if result.ReleaseSetID == "" || len(result.Operations) != 1 || result.Operations[0].JobID == "" || result.Operations[0].Marker == "" {
		t.Fatalf("result = %+v", result)
	}
	var releaseSet workdomain.ReleaseSet
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		var inner error
		releaseSet, inner = tx.Work().GetReleaseSet(fx.ctx, result.ReleaseSetID)
		return inner
	}); err != nil {
		t.Fatal(err)
	}
	if releaseSet.State != workdomain.ReleaseSetSealed || releaseSet.Version != 2 {
		t.Fatalf("release set = %s v%d, want SEALED v2", releaseSet.State, releaseSet.Version)
	}
	entry, ok := releaseSet.ReleaseFor("repo-1")
	if !ok || entry.BaseVCSObjectID != fx.baseRevision || entry.ResultVCSObjectID != fx.baseRevision {
		t.Fatalf("release entry = %+v (%v), want base = result = the recorded revision", entry, ok)
	}
	operation := fx.loadIntent(t, result.Operations[0].ReleaseSetLocalCommitID)
	if operation.State != workdomain.ReleaseSetLocalCommitRequested || operation.AuthorName != "Reviewer" || operation.ExpectedReleaseSetVersion != 2 {
		t.Fatalf("operation = %+v", operation)
	}
	stored, found := fx.loadSelection(t, string(operation.ID))
	if !found || stored.ApprovalRequestID != "approval-1" || stored.WorkItemID != "work-root" || stored.RunID != "run-1" || string(stored.Patch) == "" {
		t.Fatalf("selection = %+v (found %v), want it tied to the approval, the work item, the run, with the reviewed patch", stored, found)
	}
}

func TestRequestApprovalBoundCommits_Refusals(t *testing.T) {
	cases := map[string]struct {
		mutate func(*ApprovalBoundRequest)
		want   error
	}{
		"no author name": {func(r *ApprovalBoundRequest) { r.Repositories[0].AuthorName = " " }, ErrCommitIdentityMissing},
		"no author email": {func(r *ApprovalBoundRequest) { r.Repositories[0].AuthorEmail = "" }, ErrCommitIdentityMissing},
		"stale workspace": {func(r *ApprovalBoundRequest) { r.Repositories[0].ExpectedWorkspaceVersion = 99 }, ports.ErrOptimisticConflict},
		"other family":    {func(r *ApprovalBoundRequest) { r.FamilyID = "family-other" }, ports.ErrCrossProjectReference},
		"duplicate repository": {func(r *ApprovalBoundRequest) {
			r.Repositories = append(r.Repositories, r.Repositories[0])
		}, ErrDuplicateRepository},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newExecuteFixture(t)
			if _, err := fx.approvalBound(t, tc.mutate); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			// nothing was written: the transaction rolled back
			if got := fx.job(t); got != 0 {
				t.Fatalf("jobs after a refused request = %d, want 0", got)
			}
		})
	}
}

func TestRequestApprovalBoundCommits_NothingToCommitIsAnEmptyResult(t *testing.T) {
	fx := newExecuteFixture(t)
	result, err := fx.approvalBound(t, func(r *ApprovalBoundRequest) { r.Repositories = nil })
	if err != nil || result.ReleaseSetID != "" || len(result.Operations) != 0 {
		t.Fatalf("result = %+v, %v; want nothing created", result, err)
	}
}

// job counts the durable jobs of the operation kind (a refused request must enqueue none).
func (fx *executeFixture) job(t *testing.T) int {
	t.Helper()
	count := 0
	for {
		job, _, err := fx.store.ClaimJob(fx.ctx, "counter", time.Minute)
		if err != nil || job.ID == "" {
			return count
		}
		count++
	}
}
