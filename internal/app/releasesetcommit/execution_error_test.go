package releasesetcommit

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V12-01 (UX-22, chochuyennha 2026-11-02): a RELEASE_SET_LOCAL_COMMIT job was claimed five times,
// each claim ended LEASE_EXPIRED, the job went DEAD and the operation stayed REQUESTED for ever —
// no error in the UI, the CLI or the worker log. These tests reproduce the hang with a creator
// that always fails (the handler returns an error on every claim) and prove the operation now
// closes FAILED/EXECUTION_ERROR with the error as its detail.

// failingCreator stands in for whatever made the real worker fail on every claim.
type failingCreator struct{ calls int }

func (c *failingCreator) CreateLocalCommit(context.Context, ports.CreateLocalCommitRequest) (workspace.Revision, error) {
	c.calls++
	return workspace.Revision{}, errors.New("git commit: simulated failure (index.lock exists)")
}

func TestHandler_ErrorOnEveryClaim_ClosesFailedWithDetailOnLastClaim(t *testing.T) {
	fx := newExecuteFixture(t)
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")
	result := fx.request(t, "req-1", "hash-1", "record repository result")
	creator := &failingCreator{}
	deps := fx.deps()
	deps.Creator = creator
	handler := NewHandler(deps)

	claims := 0
	for claims < defaultLocalCommitJobMaxClaims+1 {
		job, _, err := fx.store.ClaimJob(fx.ctx, "worker-1", 10*time.Minute)
		if errors.Is(err, ports.ErrNoJobAvailable) {
			break
		}
		if err != nil {
			t.Fatalf("ClaimJob #%d: %v", claims+1, err)
		}
		claims++
		handleErr := handler.Handle(fx.ctx, job)
		if claims < defaultLocalCommitJobMaxClaims {
			if handleErr == nil {
				t.Fatalf("Handle on claim %d = nil, want the creator's error (the job must still be retried)", claims)
			}
			if intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID); intent.State != workdomain.ReleaseSetLocalCommitRequested {
				t.Fatalf("after claim %d the operation is %s, want still REQUESTED", claims, intent.State)
			}
			if err := sqlite.ExpireJobLeaseForTest(fx.ctx, fx.store, string(job.ID)); err != nil {
				t.Fatal(err)
			}
			if _, err := fx.store.RecoverExpiredJobs(fx.ctx); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if handleErr != nil {
			t.Fatalf("Handle on the last claim = %v, want nil (the operation is closed instead)", handleErr)
		}
	}
	if claims != defaultLocalCommitJobMaxClaims {
		t.Fatalf("claims = %d, want %d", claims, defaultLocalCommitJobMaxClaims)
	}
	if creator.calls != defaultLocalCommitJobMaxClaims {
		t.Fatalf("creator calls = %d, want %d", creator.calls, defaultLocalCommitJobMaxClaims)
	}

	status, err := work.GetReleaseSetLocalCommitStatus(fx.ctx, fx.uow, result.ReleaseSetLocalCommitID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "FAILED" || status.FailureReason != "EXECUTION_ERROR" {
		t.Fatalf("status = %s/%q, want FAILED/EXECUTION_ERROR", status.State, status.FailureReason)
	}
	if !strings.Contains(status.FailureDetail, "index.lock exists") || !strings.Contains(status.FailureDetail, "claim 5 of 5") {
		t.Fatalf("failureDetail = %q, want the last error and the claim count", status.FailureDetail)
	}
	assertJobStates(t, fx, map[string]int{"SUCCEEDED": 1})

	// The operator retries after fixing the cause: the identical request gets a fresh marker
	// instead of colliding with the failed one.
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed again\n")
	retry := fx.request(t, "req-2", "hash-2", "record repository result")
	if retry.Marker == result.Marker {
		t.Fatal("the retried request reused the failed operation's marker")
	}
	job := fx.claim(t, "worker-1", 10*time.Minute)
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("retried commit: %v", err)
	}
	if intent := fx.loadIntent(t, retry.ReleaseSetLocalCommitID); intent.State != workdomain.ReleaseSetLocalCommitCommitted {
		t.Fatalf("retried operation = %s, want COMMITTED", intent.State)
	}
}

func TestCloseLocalCommitsWithDeadJob_ClosesOperationWhoseJobWentDead(t *testing.T) {
	fx := newExecuteFixture(t)
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")
	result := fx.request(t, "req-1", "hash-1", "record repository result")

	// The worker hangs (or crashes) on every claim: no handler ever returns, each lease expires.
	for i := 0; i < defaultLocalCommitJobMaxClaims; i++ {
		job := fx.claim(t, "worker-1", 10*time.Minute)
		if err := sqlite.ExpireJobLeaseForTest(fx.ctx, fx.store, string(job.ID)); err != nil {
			t.Fatal(err)
		}
		if _, err := fx.store.RecoverExpiredJobs(fx.ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertJobStates(t, fx, map[string]int{"DEAD": 1})
	if intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID); intent.State != workdomain.ReleaseSetLocalCommitRequested {
		t.Fatalf("precondition: operation = %s, want REQUESTED (the hang being reproduced)", intent.State)
	}

	var failedWith workdomain.ReleaseSetLocalCommitFailureReason
	deps := ExecuteReleaseSetLocalCommitDeps{
		UnitOfWork: fx.uow,
		OnFailed: func(_ context.Context, _ ports.Tx, intent workdomain.ReleaseSetLocalCommit, reason workdomain.ReleaseSetLocalCommitFailureReason) error {
			failedWith = reason
			if intent.FailureDetail == "" {
				t.Error("OnFailed got no failure detail")
			}
			return nil
		},
	}
	closed, err := CloseLocalCommitsWithDeadJob(fx.ctx, deps, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if closed != 1 || failedWith != workdomain.FailureExecutionError {
		t.Fatalf("closed = %d, OnFailed reason = %q; want 1, EXECUTION_ERROR", closed, failedWith)
	}
	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if intent.State != workdomain.ReleaseSetLocalCommitFailed || intent.FailureReason != workdomain.FailureExecutionError {
		t.Fatalf("operation = %s/%s, want FAILED/EXECUTION_ERROR", intent.State, intent.FailureReason)
	}
	if !strings.Contains(intent.FailureDetail, "DEAD after 5 claims") || !strings.Contains(intent.FailureDetail, "LEASE_EXPIRED") {
		t.Fatalf("failureDetail = %q", intent.FailureDetail)
	}
	// Idempotent: a second sweep finds nothing.
	if closed, err := CloseLocalCommitsWithDeadJob(fx.ctx, deps, time.Now().UTC()); err != nil || closed != 0 {
		t.Fatalf("second sweep = %d, %v; want 0, nil", closed, err)
	}
}

func TestFailureDetail_RedactsAndBounds(t *testing.T) {
	got := failureDetail(func(s string) string { return strings.ReplaceAll(s, "s3cr3t", "***") }, "token s3cr3t "+strings.Repeat("é", 2000))
	if strings.Contains(got, "s3cr3t") {
		t.Fatalf("detail not redacted: %q", got[:40])
	}
	if len(got) > maxFailureDetailBytes+len("…") {
		t.Fatalf("detail is %d bytes, want at most %d", len(got), maxFailureDetailBytes)
	}
	if !strings.HasSuffix(got, "…") || !utf8Valid(got) {
		t.Fatal("bounded detail must end with an ellipsis and stay valid UTF-8")
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }
