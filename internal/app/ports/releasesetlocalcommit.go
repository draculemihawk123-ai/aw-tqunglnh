package ports

import (
	"context"
	"errors"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// LocalCommitMarkerTrailerKey is the Git trailer key a real commit message
// carries its own deterministic operation marker under (e.g.
// "Release-Set-Local-Commit-Marker: sha256:..."). Shared between whoever
// builds the real commit message (internal/app/releasesetcommit) and
// whoever searches for it (LocalCommitMarkerReader's own real adapter,
// internal/adapters/gitworktree) so the two sides can never drift apart.
const LocalCommitMarkerTrailerKey = "Release-Set-Local-Commit-Marker"

// LocalCommitMarkerReader is a narrow, read-only companion to
// LocalCommitCreator (V6-10E, docs/design/08-v6-api-projections.md V6-10E)
// declared in its own file — never as an addition to localcommit.go, an
// already-merged V5-10A file whose own doc comment states "CreateLocalCommit
// is the only Git-mutating method this port ... declares"; that claim stays
// literally true (FindLocalCommitByMarker never mutates anything), but this
// task leaves that file untouched rather than editing a merged file's own
// text for a claim that would still hold either way.
//
// Before ever calling LocalCommitCreator.CreateLocalCommit,
// internal/app/releasesetcommit's own worker must check whether an earlier,
// crashed attempt of THIS EXACT operation already created the real commit —
// "reconciles existing exact marker before create" (V6-10E's own Thực hiện
// line).
type LocalCommitMarkerReader interface {
	// FindLocalCommitByMarker inspects handle's own CURRENT HEAD commit —
	// never any other commit in its history — and ALWAYS returns its real
	// revision and its own immediate first parent commit id, regardless of
	// found. Checking HEAD alone is sufficient (not merely convenient):
	// ports.LocalCommitCreator.CreateLocalCommit is the only Git-mutating
	// method this codebase's entire port surface declares (localcommit.go's
	// own doc comment), so under this operation's own write-lease
	// exclusivity nothing else can ever have advanced HEAD in this
	// workspace between two calls.
	//
	// found reports whether HEAD's own commit message carries marker as an
	// exact trailer line — found=false means this exact operation has not
	// yet created its own commit (headRevision is then the parent the
	// caller's own next CreateLocalCommit call should build on top of);
	// found=true means HEAD IS this operation's own earlier, possibly
	// crash-interrupted result (headRevision is that result, and
	// parentVCSObjectID is its own parent — let the caller confirm that
	// matches whatever it itself pinned before ever trusting this as a
	// genuine reuse rather than a colliding marker, V6-10E's own "drift"
	// Verify case).
	FindLocalCommitByMarker(ctx context.Context, handle WorkspaceHandle, marker string) (found bool, headRevision workspace.Revision, parentVCSObjectID string, err error)
}

// ErrLocalCommitWriteLeaseConflict/ErrLocalCommitWriteLeaseLost mirror
// ErrWriteLeaseConflict/ErrWriteLeaseLost's own naming (scheduling.go) for
// this task's own separate, job-holder-only lease mechanism — see
// LocalCommitWriteLeaseManager's own doc comment for why this is not simply
// WriteLeaseManager itself.
var (
	ErrLocalCommitWriteLeaseConflict = errors.New("repository workspace already has an active local-commit writer")
	ErrLocalCommitWriteLeaseLost     = errors.New("repository workspace local-commit write lease is no longer authoritative")
)

// LocalCommitWriteLeaseTarget names the one RepositoryWorkspace generation a
// local-commit write lease is acquired against.
type LocalCommitWriteLeaseTarget struct {
	RepositoryID          project.RepositoryID
	RepositoryWorkspaceID workspace.RepositoryWorkspaceID
	Generation            uint64
}

// AcquireLocalCommitWriteLeaseRequest is what a caller supplies to
// LocalCommitWriteLeaseManager.AcquireLocalCommitWriteLease.
type AcquireLocalCommitWriteLeaseRequest struct {
	JobLease JobLease
	Target   LocalCommitWriteLeaseTarget
	TTL      time.Duration
}

// LocalCommitWriteLeaseGrant is a fencing proof, not just a worker label —
// the identical role WriteLeaseGrant plays for the AGENT-attempt-scoped
// mechanism (scheduling.go), minus HolderAttemptID: this lease's own holder
// is a JobLease alone (see LocalCommitWriteLeaseManager's own doc comment).
type LocalCommitWriteLeaseGrant struct {
	RepositoryID          project.RepositoryID
	RepositoryWorkspaceID workspace.RepositoryWorkspaceID
	Generation            uint64
	FenceToken            uint64
	HolderJobID           JobID
	HolderJobLeaseToken   uint64
	Owner                 string
	LeaseUntil            time.Time
}

// LocalCommitWriteLeaseManager is V6-10E's own narrow mutual-exclusion
// mechanism over a RepositoryWorkspace's real Git state — deliberately NOT
// a reuse of WriteLeaseManager (scheduling.go). Every WriteLeaseManager
// method hard-requires a live runtime.ExecutionAttemptID (AcquireWriteLeases
// rejects an empty one outright, and the real sqlite implementation's own
// HeartbeatWriteLeases/ValidateWriteLease queries INNER JOIN
// execution_attempts WHERE state = 'RUNNING') — that table is scoped to
// AGENT node execution attempts, a concept RequestReleaseSetLocalCommit's
// own CONTROL-class job has no instance of. Reusing WriteLeaseManager here
// would mean either fabricating a fake execution_attempts row (a real
// domain concept this operation is not) or loosening that mechanism's own
// attempt-coupling for every existing AGENT caller — both rejected: this is
// the same "narrow port for a new capability, zero change to the existing
// implementer" precedent ports.LocalCommitCreator (localcommit.go) and
// ports.WorkspaceInspectionReader (workspaceinspection.go) already
// established for their own new capabilities.
//
// The discipline this mirrors is otherwise identical to WriteLeaseManager's
// own: acquire before any mutating I/O, revalidate/reconcile before ever
// committing a result, release only after the terminal commit — idempotent
// and fence-aware (see internal/app/releasesetcommit's own doc comment for
// exactly how this task's worker composes it with the job's own JobLease).
type LocalCommitWriteLeaseManager interface {
	AcquireLocalCommitWriteLease(ctx context.Context, req AcquireLocalCommitWriteLeaseRequest) (LocalCommitWriteLeaseGrant, error)
	ValidateLocalCommitWriteLease(ctx context.Context, grant LocalCommitWriteLeaseGrant) error
	ReleaseLocalCommitWriteLease(ctx context.Context, grant LocalCommitWriteLeaseGrant) error
}
