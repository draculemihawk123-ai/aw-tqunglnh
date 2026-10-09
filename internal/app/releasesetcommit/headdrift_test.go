package releasesetcommit

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/work"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V11-01: a local commit must not be built on a HEAD that is not the revision aw
// recorded for the workspace. Someone committed in the worktree outside aw (an
// IDE, a terminal); committing on top of that HEAD would fold the outside
// commits into aw's history with no audit and no gate. The operation closes
// FAILED/HEAD_DRIFT before any `git commit`, and the job completes (determinate,
// so it is never retried to DEAD).
func TestExecuteReleaseSetLocalCommit_HeadDrift_FailsTerminalBeforeAnyGitCommit(t *testing.T) {
	fx := newExecuteFixture(t)
	// An outside commit moves HEAD past the recorded base revision...
	writeTestFile(t, filepath.Join(fx.workspacePath, "by-hand.txt"), "by hand\n")
	runTestGitCommand(t, fx.workspacePath, "add", "-A")
	runTestGitCommand(t, fx.workspacePath, "-c", "user.name=Ide User", "-c", "user.email=ide@example.invalid", "commit", "-q", "-m", "commit tay")
	driftedHead := fx.headCommit(t)
	// ...and the worktree also has aw's own, still uncommitted change.
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed by the task\n")

	result := fx.request(t, "req-drift", "hash-drift", "record repository result")
	job := fx.claim(t, "worker-1", 10*time.Minute)
	spy := &spyCreator{LocalCommitCreator: fx.provider}
	deps := fx.deps()
	deps.Creator = spy
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, job); err != nil {
		t.Fatalf("ExecuteReleaseSetLocalCommit = %v, want nil: a drifted HEAD is a determinate terminal outcome, not a retryable error", err)
	}
	if spy.calls != 0 {
		t.Fatalf("CreateLocalCommit calls = %d, want 0 — nothing may be committed on top of an unrecorded HEAD", spy.calls)
	}

	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if intent.State != workdomain.ReleaseSetLocalCommitFailed || intent.FailureReason != workdomain.FailureHeadDrift {
		t.Fatalf("intent = state %s reason %q, want FAILED/HEAD_DRIFT", intent.State, intent.FailureReason)
	}
	status, err := work.GetReleaseSetLocalCommitStatus(fx.ctx, fx.uow, result.ReleaseSetLocalCommitID)
	if err != nil {
		t.Fatalf("GetReleaseSetLocalCommitStatus: %v", err)
	}
	if status.State != "FAILED" || status.FailureReason != "HEAD_DRIFT" {
		t.Fatalf("status = %s/%q, want FAILED/HEAD_DRIFT", status.State, status.FailureReason)
	}
	if head := fx.headCommit(t); head != driftedHead {
		t.Fatalf("workspace HEAD = %s, want unchanged %s — the command must not commit", head, driftedHead)
	}
	assertJobStates(t, fx, map[string]int{"SUCCEEDED": 1})
}
