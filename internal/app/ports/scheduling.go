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
	ID             JobID
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
}

type EnqueueJobRequest struct {
	ID             JobID
	ProjectID      project.ProjectID
	Kind           string
	AggregateType  string
	AggregateID    string
	Payload        json.RawMessage
	AvailableAt    time.Time
	Priority       int
	MaxClaims      uint32
	IdempotencyKey string
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
