package ports

import "errors"

// JobClass is V4-12B's own cancel-fence discriminator
// (docs/design/06-v4-runtime-engine.md, ADR-020): a RUN_WORK job is fenced
// by its owning WorkflowRun's own cancel_epoch and stops being claimable
// (and is CASed straight to CANCELLED if never claimed) the moment that
// Run's own cancellation intent commits. A CONTROL job is never fenced —
// it is infrastructure that must keep running even while every RUN_WORK
// job of the same Run is being fenced off, most importantly the very
// CANCEL_RUN_COORDINATOR job that does the fencing.
type JobClass string

const (
	JobClassRunWork JobClass = "RUN_WORK"
	JobClassControl JobClass = "CONTROL"
)

// ErrRunCancelling is returned by an attempt to enqueue a new RUN_WORK job
// for a WorkflowRun whose own cancellation intent has already committed
// (state CANCELLING or CANCELLED) — "Enqueue RUN_WORK mới CAS rằng Run còn
// non-cancelling" (docs/design/06-v4-runtime-engine.md). This is a
// defense-in-depth backstop: every real call site in internal/app/runtime
// already checks the Run's own state before attempting to create new work
// at all (so this should never fire in the normal flow), but the INSERT
// itself is still the one place this invariant is actually enforced,
// never a convention every future caller has to remember to honor on its
// own.
var ErrRunCancelling = errors.New("workflow run is cancelling or cancelled: no new RUN_WORK job may be enqueued for it")

// controlJobKinds is Alpha's own closed CONTROL allow-list (V4-12B,
// confirmed with the user before writing this file): every Kind NOT in
// this set defaults to RUN_WORK, fail-closed. The literal string values
// are duplicated here rather than importing each owning package's own Kind
// constant, to avoid this low-level ports package depending on
// higher-layer app packages (internal/app/runtime owns
// CancelRunCoordinatorJobKind, internal/app/workspacereconcile owns
// WorkspaceReconciliationJobKind="WORKSPACE_RECONCILIATION",
// internal/app/workspacerelease owns
// WorkspaceSetReleaseJobKind="WORKSPACE_SET_RELEASE").
//
// RECOVERY_REAPER (named in the ADR's own CONTROL allow-list) is
// deliberately excluded: internal/app/workerpool.Pool's own recovery
// sweep (Store.RecoverExpiredJobs) is a direct, ticker-driven maintenance
// operation with no durable_jobs row, lease, claim, or RunID of its own —
// JobClass/cancel_epoch fencing has nothing to attach to. Turning it into
// a real durable job is a separate, future decision, never an existing
// Alpha capability this mapping should pretend already exists.
var controlJobKinds = map[string]bool{
	"CANCEL_RUN_COORDINATOR":   true,
	"WORKSPACE_RECONCILIATION": true,
	"WORKSPACE_SET_RELEASE":    true,
}

// ClassifyJobKind derives a job's JobClass from its Kind via this fixed,
// fail-closed allow-list — never a caller-supplied field (confirmed with
// the user: "Không cho caller tự khai JobClass=CONTROL"), so
// EnqueueJobRequest itself carries no JobClass field at all; the
// persistence layer (internal/adapters/sqlite, internal/app/ports/fake)
// calls this at write time to compute the one it actually stores.
func ClassifyJobKind(kind string) JobClass {
	if controlJobKinds[kind] {
		return JobClassControl
	}
	return JobClassRunWork
}
