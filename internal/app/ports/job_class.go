package ports

import (
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

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
// RECOVERY_REAPER joined this allow-list in V4-13 (docs/design/06-v4-runtime-engine.md,
// confirmed with the user before making this change): it is now a real,
// self-rescheduling durable job internal/app/runtime owns
// (RecoveryReaperJobKind), no longer the bare ticker-driven
// Store.RecoverExpiredJobs sweep V4-12B's own doc comment described as
// having "no durable_jobs row, lease, claim, or RunID of its own" — that
// description predates this task. durable_jobs.job_class's own CHECK
// constraint (migration 25) enforces the identical four-kind allow-list at
// the database level; TestClassifyJobKind_EverythingElseDefaultsRunWork's
// own negative-space assertions cover the three OLD kinds plus this new
// one, so a rename mismatch between this Go map and that CHECK's own SQL
// text fails a test immediately (mirroring migration 23's own doc comment
// on why the CHECK exists as a second authority, not just this map).
//
// ARTIFACT_SWEEP joined in V5-14: the retention sweeper's own
// self-rescheduling CONTROL job (internal/app/artifactsweep,
// ArtifactSweepJobKind), mirroring RECOVERY_REAPER's own shape exactly —
// migration 34 widens the identical CHECK constraint again.
var controlJobKinds = map[string]bool{
	"CANCEL_RUN_COORDINATOR":   true,
	"WORKSPACE_RECONCILIATION": true,
	"WORKSPACE_SET_RELEASE":    true,
	"RECOVERY_REAPER":          true,
	"ARTIFACT_SWEEP":           true,
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

// installationGlobalJobKinds is the closed set of job kinds that must be
// enqueued with NO owning Project or Run at all (V4-13, confirmed with the
// user): today, only RecoveryReaperJobKind ("RECOVERY_REAPER",
// internal/app/runtime) — a sweep over every project's own orphaned
// attempts and stranded cancellation intents, not scoped to any one of
// them. The user explicitly rejected seeding a synthetic "system" Project
// row to satisfy durable_jobs.project_id's own FK instead: that would
// leave a fake project every project-scoped query, authorization check,
// export and UI has to remember to filter out, which is a worse and more
// permanent liability than one narrow, explicit exception in this map (the
// same "closed allow-list, fail toward the common case" shape
// controlJobKinds above already uses). Every OTHER CONTROL kind
// (CANCEL_RUN_COORDINATOR, WORKSPACE_RECONCILIATION,
// WORKSPACE_SET_RELEASE) still requires a real ProjectID — CONTROL-ness
// and installation-global-ness are independent axes, not the same thing.
//
// ARTIFACT_SWEEP (V5-14) joins this exemption for the identical reason: a
// content-addressed Locator can be shared by Artifact rows across
// different Projects (0027_artifacts.sql's own "content_hash is
// deliberately NOT unique"), so the sweep itself cannot be pinned to any
// one Project any more than RECOVERY_REAPER's own sweep can.
var installationGlobalJobKinds = map[string]bool{
	"RECOVERY_REAPER": true,
	"ARTIFACT_SWEEP":  true,
}

// ValidateJobScope enforces EnqueueJobRequest's own Project/Run scoping
// invariant server-side, the second authority (alongside durable_jobs' own
// migration-25 CHECK constraint) for the exact same rule — a mismatch
// between this map and that SQL CHECK's own kind list fails a test
// immediately, mirroring ClassifyJobKind's own "CHECK in SQL, map in Go"
// discipline. Both internal/adapters/sqlite's enqueueJobTx and
// internal/app/ports/fake's JobsRepository.EnqueueJob call this, so the two
// backends reject the identical malformed requests rather than one
// silently accepting what the other rejects.
func ValidateJobScope(kind string, projectID project.ProjectID, runID string) error {
	if installationGlobalJobKinds[kind] {
		if projectID != "" {
			return fmt.Errorf("durable job kind %s is installation-global and must not carry a ProjectID", kind)
		}
		if runID != "" {
			return fmt.Errorf("durable job kind %s is installation-global and must not carry a RunID", kind)
		}
		return nil
	}
	if projectID == "" {
		return fmt.Errorf("durable job kind %s requires a ProjectID", kind)
	}
	return nil
}
