package ports

import (
	"errors"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// ErrCrossProjectReference is returned when a catalog record's declared
// ProjectID does not match the actual project of the parent row it
// references (a Component referencing a Repository owned by a different
// Project; a ComponentPackAssignment referencing a Component owned by a
// different Project) — the Catalog-concern counterpart of
// ErrCrossProjectDependency, which is specific to a DefinitionKind's own
// dependency pins. The referenced row's actual project is always resolved
// by the repository itself, never trusted from the request, the same
// discipline ErrCrossProjectDependency's own doc comment describes.
var ErrCrossProjectReference = errors.New("ports: catalog record references a parent that belongs to a different project")

// CreateProjectRequest is what a caller supplies to
// CatalogRepository.CreateProject.
type CreateProjectRequest struct {
	ID   string
	Name string
}

// RegisterRepositoryRequest is what a caller supplies to
// CatalogRepository.RegisterRepository (persistence) and
// internal/app/catalog.RegisterRepository (the command). ID is the new
// Repository's own identity — caller-supplied, mirroring
// CreateDefinitionRequest.DefinitionID's own convention, since (unlike
// the probe job this same command also enqueues) a caller registering a
// repository already names the identity it wants, rather than the
// application layer minting one on its behalf.
type RegisterRepositoryRequest struct {
	ID            string
	ProjectID     string
	Name          string
	RemoteLocator string
	DefaultRef    string
}

// CreateComponentRequest is what a caller supplies to
// CatalogRepository.CreateComponent.
type CreateComponentRequest struct {
	ID           string
	ProjectID    string
	RepositoryID string
	Name         string
	Path         string
	Kind         string
}

// AssignComponentPackRequest is what a caller supplies to
// CatalogRepository.AssignComponentPack.
type AssignComponentPackRequest struct {
	ID            string
	ProjectID     string
	ComponentID   string
	PackVersionID string
	EffectiveAt   time.Time
	Actor         string
}

// TransitionRepositoryStatusRequest is an optimistic compare-and-swap
// request for a Repository's own status (V3-02,
// docs/design/05-v3-project-workspace.md), mirroring
// WorkflowRunTransition's exact CAS shape (persistence.go) at the
// Repository aggregate: a stale caller (one that observed an older
// ExpectedStatus/ExpectedVersion than what is currently stored) is
// rejected with ErrOptimisticConflict instead of silently overwriting a
// transition another worker already committed — this is this codebase's
// first real call site for ports.Command.ExpectedVersion
// (docs/architecture/04-go-core-spec.md §3's "Mọi command mutation mang
// ... ExpectedVersion khi sửa aggregate đã tồn tại"), since V3-01 itself
// never had an update path at all.
//
// LastProbeErrorCode is always written exactly as given — nil clears the
// column, a non-nil pointer sets it — there is no third "leave unchanged"
// state: every legal Repository status transition this task drives
// (REGISTERING->PROBING, BLOCKED->PROBING via RetryRepositoryProbe,
// PROBING->ACTIVE, PROBING->BLOCKED) already knows exactly what it wants
// the column to become (nil while a fresh probe is in flight or once it
// succeeds; a specific apperror.Code string once a probe fails), so a
// third mode would be unused complexity.
type TransitionRepositoryStatusRequest struct {
	RepositoryID       string
	ExpectedStatus     project.RepositoryStatus
	ExpectedVersion    uint64
	NextStatus         project.RepositoryStatus
	LastProbeErrorCode *string
}

// RepositoryProbeAttemptState is the durable job's own execution outcome
// for one REPOSITORY_PROBE attempt — distinct from Result, which is the
// business verdict the attempt reached about the Repository itself (see
// RepositoryProbeAttempt's own doc comment for why these are two separate
// fields rather than one).
type RepositoryProbeAttemptState string

const (
	// RepositoryProbeAttemptSucceeded means the durable job ran the probe
	// to completion and reached a definitive Result (ACTIVE or BLOCKED) —
	// the only value any code in this task ever writes: per V3-02's own
	// "job success vs. business outcome" distinction, a probe that
	// determines a repository is unusable is the job succeeding at its one
	// job, not the job failing.
	RepositoryProbeAttemptSucceeded RepositoryProbeAttemptState = "SUCCEEDED"
	// RepositoryProbeAttemptFailed is reserved schema headroom for a
	// future change that also wants to log a crashed/errored attempt (one
	// that never reached a Result at all) — no code in this task ever
	// writes this value; a job that crashes before reaching a verdict is
	// simply retried (or eventually DEAD) with no attempt row of its own.
	RepositoryProbeAttemptFailed RepositoryProbeAttemptState = "FAILED"
)

// RecordRepositoryProbeAttemptRequest is what a caller supplies to
// CatalogRepository.RecordRepositoryProbeAttempt. Exactly one of
// (ErrorCode/ErrorMessage) or (BaseCommit/Dirty) is populated, matching
// whichever RepositoryStatus Result names — never both, never neither,
// for a State == RepositoryProbeAttemptSucceeded row (the only state this
// task's own code ever constructs).
type RecordRepositoryProbeAttemptRequest struct {
	ID           string
	ProjectID    string
	RepositoryID string
	JobID        string
	State        RepositoryProbeAttemptState
	Result       *project.RepositoryStatus
	ErrorCode    *string
	ErrorMessage *string
	BaseCommit   *string
	Dirty        *bool
}

// RepositoryProbeAttempt is one append-only row of the onboarding-probe
// evidence log (docs/design/01-system-design.md §6.1's
// repository_probe_attempts: "repository/job/state/result/error/time").
type RepositoryProbeAttempt struct {
	ID           string
	ProjectID    string
	RepositoryID string
	JobID        string
	State        RepositoryProbeAttemptState
	Result       *project.RepositoryStatus
	ErrorCode    *string
	ErrorMessage *string
	BaseCommit   *string
	Dirty        *bool
	CreatedAt    time.Time
}
