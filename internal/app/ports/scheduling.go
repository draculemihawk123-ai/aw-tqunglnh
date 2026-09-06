package ports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

var (
	ErrNoJobAvailable     = errors.New("no durable job is available")
	ErrJobLeaseLost       = errors.New("durable job lease is no longer authoritative")
	ErrWriteLeaseConflict = errors.New("repository workspace already has an active writer")
	ErrWriteLeaseLost     = errors.New("repository workspace write lease is no longer authoritative")
)

type JobID string

type JobState string

const (
	JobAvailable JobState = "AVAILABLE"
	JobLeased    JobState = "LEASED"
	JobSucceeded JobState = "SUCCEEDED"
	JobFailed    JobState = "FAILED"
	JobDead      JobState = "DEAD"
	JobCancelled JobState = "CANCELLED"
)

type DurableJob struct {
	ID JobID
	// ProjectID is required for every job kind except RecoveryReaperJobKind
	// ("RECOVERY_REAPER", V4-13, ValidateJobScope) — that one job is
	// genuinely installation-global (sweeps every project's own orphaned
	// attempts and stranded cancellation intents in one pass), so it is
	// enqueued with no owning Project at all rather than against a
	// synthetic "system" Project row (confirmed with the user: a sentinel
	// Project would leak into every project-scoped query, authorization
	// check, export and UI as a fake project every one of them has to
	// remember to filter out). Empty means "no owning Project", persisted
	// as a real SQL NULL (durable_jobs.project_id, nullable since
	// migration 25), never the empty string — mirrors RunID's own
	// identical empty-means-absent convention below.
	ProjectID      project.ProjectID
	Kind           string
	AggregateType  string
	AggregateID    string
	Payload        json.RawMessage
	State          JobState
	AvailableAt    time.Time
	Priority       int
	ClaimCount     uint32
	MaxClaims      uint32
	LeaseOwner     string
	LeaseToken     uint64
	LeaseUntil     *time.Time
	HeartbeatAt    *time.Time
	IdempotencyKey string
	LastErrorCode  string
	Version        uint64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// RunID is populated now (V4-12B): the WorkflowRun this job belongs to,
	// empty for a job with no single owning Run (WORKSPACE_PROVISION,
	// REPOSITORY_PROBE, BASELINE_EVIDENCE — all family/workspace-scoped,
	// not run-scoped). Read-only here — set at enqueue time via
	// EnqueueJobRequest.RunID, never changed afterward.
	RunID string
	// JobClass is populated now (V4-12B): always derived from Kind by
	// ClassifyJobKind at enqueue time (never a caller-supplied value — see
	// that function's own doc comment), persisted so the claim query can
	// filter on it without recomputing the mapping on every read.
	JobClass JobClass
	// CancelEpoch is populated now (V4-12B): nil until this job's own
	// owning Run's cancellation intent commits, then set to match that
	// Run's own WorkflowRun.CancelEpoch in the SAME transaction — for a
	// RUN_WORK job this both removes it from the claim CAS's own
	// candidate set (see the claim query's own WHERE clause) and, for a
	// job a worker already holds LEASED, gives that worker's own two
	// re-check checkpoints (before QUEUED->RUNNING, immediately before
	// starting real execution) something durable to observe. Always nil
	// for a CONTROL job — CONTROL is never fenced.
	CancelEpoch *uint64
}

type EnqueueJobRequest struct {
	ID JobID
	// ProjectID is validated by ValidateJobScope at enqueue time: required
	// for every job kind except "RECOVERY_REAPER" (V4-13), which must
	// leave this blank — see DurableJob.ProjectID's own doc comment for
	// why. Persisted as SQL NULL when blank, never the empty string.
	ProjectID      project.ProjectID
	Kind           string
	AggregateType  string
	AggregateID    string
	Payload        json.RawMessage
	AvailableAt    time.Time
	Priority       int
	MaxClaims      uint32
	IdempotencyKey string
	// RunID is populated now (V4-12B): the WorkflowRun this job belongs
	// to, for a job whose Kind classifies as JobClassRunWork (see
	// ClassifyJobKind) — leave blank for a job with no single owning Run.
	// When non-blank and the Kind is RUN_WORK, the enqueue itself is
	// fenced: it fails with ErrRunCancelling if that Run's own state is
	// already CANCELLING or CANCELLED ("Enqueue RUN_WORK mới CAS rằng Run
	// còn non-cancelling"). There is deliberately no JobClass field here
	// for a caller to set — JobClass is always derived from Kind
	// server-side (confirmed with the user: a caller must never be able
	// to declare its own job CONTROL). ValidateJobScope also requires this
	// blank for "RECOVERY_REAPER" — that job is installation-global, not
	// run-scoped.
	RunID string
}

// JobLease is a fencing proof, not just a worker label. Every mutation made by
// a worker must present the exact owner and monotonically increasing token.
type JobLease struct {
	JobID      JobID
	Owner      string
	Token      uint64
	LeaseUntil time.Time
}

type JobQueue interface {
	EnqueueJob(context.Context, EnqueueJobRequest) (DurableJob, error)
	ClaimJob(context.Context, string, time.Duration) (DurableJob, JobLease, error)
	HeartbeatJob(context.Context, JobLease, time.Duration) (JobLease, error)
	CompleteJob(context.Context, JobLease) error
	RecoverExpiredJobs(context.Context) (int64, error)
}

type WorkspaceLeaseTarget struct {
	RepositoryID          project.RepositoryID
	RepositoryWorkspaceID workspace.RepositoryWorkspaceID
	Generation            uint64
}

type AcquireWriteLeasesRequest struct {
	JobLease  JobLease
	AttemptID runtime.ExecutionAttemptID
	Targets   []WorkspaceLeaseTarget
	TTL       time.Duration
}

type WriteLeaseGrant struct {
	RepositoryID          project.RepositoryID
	RepositoryWorkspaceID workspace.RepositoryWorkspaceID
	Generation            uint64
	FenceToken            uint64
	HolderJobID           JobID
	HolderJobLeaseToken   uint64
	HolderAttemptID       runtime.ExecutionAttemptID
	Owner                 string
	LeaseUntil            time.Time
}

type WriteLeaseManager interface {
	AcquireWriteLeases(context.Context, AcquireWriteLeasesRequest) ([]WriteLeaseGrant, error)
	HeartbeatWriteLeases(context.Context, []WriteLeaseGrant, time.Duration) ([]WriteLeaseGrant, error)
	ValidateWriteLease(context.Context, WriteLeaseGrant) error
	ReleaseWriteLeases(context.Context, []WriteLeaseGrant) error
}
