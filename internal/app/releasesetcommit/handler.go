package releasesetcommit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// maxFailureDetailBytes bounds the failure detail stored with an EXECUTION_ERROR failure.
const maxFailureDetailBytes = 1000

// Handler implements workerpool.Handler for ReleaseSetLocalCommitJobKind,
// mirroring workspacerelease.Handler's identical shape: depends only on
// ExecuteReleaseSetLocalCommitDeps's own port interfaces, never a concrete
// adapter package directly.
type Handler struct {
	deps ExecuteReleaseSetLocalCommitDeps
}

// NewHandler returns a ready-to-register Handler.
func NewHandler(deps ExecuteReleaseSetLocalCommitDeps) *Handler {
	return &Handler{deps: deps}
}

var _ workerpool.Handler = (*Handler)(nil)

// Handle implements workerpool.Handler: delegate to
// ExecuteReleaseSetLocalCommit. Any non-nil error here leaves the job
// un-completed for retry — exactly like workspacerelease.Handler.Handle's
// identical discipline; a terminal, typed outcome
// (WORKSPACE_QUARANTINED/MARKER_DRIFT/NO_CHANGES/HEAD_DRIFT/CHANGESET_DRIFT) is
// instead recorded by ExecuteReleaseSetLocalCommit itself via its own
// fenced failTerminal transaction (which also completes the job), so
// Handle returns nil for that case.
//
// V12-01: on the job's LAST claim (ClaimCount has reached MaxClaims) an error is no longer left
// for the lease to expire — that made the job DEAD and left the operation REQUESTED for ever with
// no word of why (chochuyennha, 2026-11-02). The operation closes FAILED/EXECUTION_ERROR with the
// error as its failure detail, in the same fenced transaction that completes the job, so the API,
// CLI and UI can show it and the operator can request the commit again. A lost job lease is the
// exception: another owner holds the job and decides.
func (h *Handler) Handle(ctx context.Context, job ports.DurableJob) error {
	err := ExecuteReleaseSetLocalCommit(ctx, h.deps, job)
	if err == nil {
		return nil
	}
	wrapped := fmt.Errorf("releasesetcommit: execute local commit for job %s: %w", job.ID, err)
	if job.MaxClaims == 0 || job.ClaimCount < job.MaxClaims || errors.Is(err, ports.ErrJobLeaseLost) || job.LeaseUntil == nil {
		return wrapped
	}
	if closeErr := h.closeAfterLastClaim(ctx, job, err); closeErr != nil {
		return errors.Join(wrapped, fmt.Errorf("releasesetcommit: close operation after the last claim of job %s: %w", job.ID, closeErr))
	}
	return nil
}

func (h *Handler) closeAfterLastClaim(ctx context.Context, job ports.DurableJob, cause error) error {
	if err := h.deps.validate(); err != nil {
		return err
	}
	var payload localCommitJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ReleaseSetLocalCommitID == "" {
		return fmt.Errorf("job %s payload names no operation", job.ID)
	}
	intent, err := loadIntent(ctx, h.deps.UnitOfWork, payload.ReleaseSetLocalCommitID)
	if err != nil {
		return err
	}
	if intent.State != workdomain.ReleaseSetLocalCommitRequested {
		return nil
	}
	jobLease := ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken, LeaseUntil: *job.LeaseUntil}
	detail := failureDetail(h.deps.Redact, fmt.Sprintf("claim %d of %d failed: %v", job.ClaimCount, job.MaxClaims, cause))
	return failTerminalWithDetail(context.WithoutCancel(ctx), h.deps, jobLease, intent, workdomain.FailureExecutionError, detail, time.Now().UTC())
}

// CloseLocalCommitsWithDeadJob (V12-01) closes FAILED/EXECUTION_ERROR every operation still
// REQUESTED whose job is DEAD — the job's lease kept expiring (a worker that crashed or hung on
// every claim) so Handle never saw a last claim end. It returns how many it closed. The worker
// calls it on start and periodically; it is idempotent (a closed operation is no longer listed).
func CloseLocalCommitsWithDeadJob(ctx context.Context, deps ExecuteReleaseSetLocalCommitDeps, now time.Time) (int, error) {
	if deps.UnitOfWork == nil {
		return 0, errors.New("releasesetcommit: CloseLocalCommitsWithDeadJob requires a UnitOfWork")
	}
	closed := 0
	err := deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		dead, err := tx.Work().ListLocalCommitsWithDeadJob(ctx)
		if err != nil {
			return err
		}
		for _, item := range dead {
			reason := item.LastErrorCode
			if reason == "" {
				reason = "unknown"
			}
			detail := failureDetail(deps.Redact, fmt.Sprintf(
				"job %s is DEAD after %d claims (last error %s): no worker finished the commit; see the worker log", item.JobID, item.ClaimCount, reason))
			if err := closeFailedTx(ctx, tx, deps, item.Intent, workdomain.FailureExecutionError, detail, string(item.JobID), now); err != nil {
				return err
			}
			closed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return closed, nil
}

func failureDetail(redact func(string) string, detail string) string {
	if redact != nil {
		detail = redact(detail)
	}
	if len(detail) <= maxFailureDetailBytes {
		return detail
	}
	cut := maxFailureDetailBytes
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut] + "…"
}
