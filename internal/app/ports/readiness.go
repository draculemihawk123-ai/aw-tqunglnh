package ports

import (
	"context"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
)

// WorkspaceDirectoryResolver resolves an opaque WorkspaceHandle (see
// WorkspaceHandle's own doc comment: "opaque, path-free reference... only
// the WorkspaceProvider that issued it may interpret it") to the real,
// spawnable filesystem working directory for internal/app/readinesscheck's
// own baseline-recipe subprocess execution.
//
// This is deliberately its own narrow port, not a widening of
// WorkspaceProvider (workspace.go): internal/adapters/gitworktree.Provider
// already exposes exactly this method — WorkingDirectory, whose own doc
// comment names it "the one sanctioned bridge from an opaque handle to a
// spawnable cwd... for starting the real AgentExecutor/helper process a
// workspace exists to support" — for exactly this purpose. Declaring a
// second, narrower interface here that Provider already structurally
// satisfies (Go's structural typing needs no change to
// internal/adapters/gitworktree itself) lets
// internal/app/readinesscheck depend on only the one capability it
// actually needs, per internal/archtest.TestDomainAppNeverImportAdapters's
// "domain/app must depend only on ports, never a concrete adapter package"
// — without widening the shared WorkspaceProvider interface every existing
// caller (internal/app/workspaceprovision and its own already-merged test
// doubles) would otherwise have to grow a new method for.
type WorkspaceDirectoryResolver interface {
	WorkingDirectory(ctx context.Context, handle WorkspaceHandle) (string, error)
}

// ReadinessRepository is V3-07's Tx accessor
// (docs/design/05-v3-project-workspace.md V3-07): the canonical
// setup/verification recipe (readiness.Profile), append-only pre-change
// baseline evidence (BaselineAttempt) and this task's own narrow, typed
// environment blocker (EnvironmentBlocker) — see this task's own PR
// description for why a narrow, RepositoryWorkspace-scoped blocker rather
// than the shared cross-cutting `blockers` table
// docs/design/01-system-design.md §6.1 sketches (that table is keyed to
// work_item/run/node, none of which exist in this codebase yet, and its
// own listed `type` values are all runtime/V4-era concepts no code here
// can populate today).
type ReadinessRepository interface {
	// SetReadinessProfile inserts or replaces the canonical
	// setup/verification recipe for profile.RepositoryID after verifying
	// it names a Repository that actually exists — ErrPersistenceNotFound
	// otherwise. A single row per Repository: the latest call always wins
	// (a plain upsert, not a CAS transition) — no citation in this task's
	// own scope asks for a versioned recipe history the way
	// project.ComponentPackAssignment's own append-only "what was in
	// effect at time T" requirement does, and nothing in this task's own
	// scope has two concurrent writers racing to set the same repository's
	// own recipe.
	SetReadinessProfile(ctx context.Context, profile readiness.Profile) (readiness.Profile, error)
	// GetReadinessProfile returns the Profile for repositoryID, or
	// ErrPersistenceNotFound if none has ever been set.
	GetReadinessProfile(ctx context.Context, repositoryID string) (readiness.Profile, error)

	// RecordBaselineAttempt appends one row to the append-only baseline
	// evidence log — never updated or replaced once written, mirroring
	// CatalogRepository.RecordRepositoryProbeAttempt's own identical
	// discipline for a different aggregate. req.JobID is UNIQUE at the
	// storage layer: the same durable job can never produce two attempt
	// rows, the same "active probe idempotent" guarantee applied to a
	// baseline check instead of an onboarding probe.
	RecordBaselineAttempt(ctx context.Context, req RecordBaselineAttemptRequest) (BaselineAttempt, error)
	// GetBaselineAttemptByJobID returns the BaselineAttempt already
	// recorded for jobID, or ErrPersistenceNotFound — the crash-recovery
	// idempotency check internal/app/readinesscheck's own Handle runs
	// first, mirroring repositoryprobe.Handler/workspaceprovision.Handler's
	// identical "already resolved" reclaim check for their own aggregates.
	GetBaselineAttemptByJobID(ctx context.Context, jobID string) (BaselineAttempt, error)
	// ListBaselineAttempts returns every BaselineAttempt for
	// repositoryWorkspaceID, oldest-CreatedAt-first — the append-only
	// evidence history a caller reads back to tell "clean baseline",
	// "known pre-existing failure" and "we couldn't even check" apart.
	ListBaselineAttempts(ctx context.Context, repositoryWorkspaceID string) ([]BaselineAttempt, error)

	// OpenEnvironmentBlocker opens a new OPEN environment blocker for
	// req.RepositoryWorkspaceID, or — if one is already OPEN for that
	// RepositoryWorkspace — returns the existing row unchanged rather than
	// inserting a duplicate (mirroring
	// AdapterBuildRepository.InsertIfAbsent's own "already existed" return
	// shape): at most one OPEN blocker per RepositoryWorkspace at a time.
	OpenEnvironmentBlocker(ctx context.Context, req OpenEnvironmentBlockerRequest) (blocker EnvironmentBlocker, alreadyOpen bool, err error)
	// ResolveOpenEnvironmentBlocker resolves repositoryWorkspaceID's own
	// OPEN blocker (if any) at resolvedAt — a safe no-op if none is
	// currently OPEN, mirroring project.CanTransitionRepositoryStatus's
	// own "no-op is not an error" discipline for an already-settled state.
	ResolveOpenEnvironmentBlocker(ctx context.Context, repositoryWorkspaceID string, resolvedAt time.Time) error
	// GetOpenEnvironmentBlocker returns the currently OPEN blocker for
	// repositoryWorkspaceID, or ErrPersistenceNotFound if none is open —
	// the read a future writer-gating check (V4+, out of this task's own
	// scope) uses to decide whether a RepositoryWorkspace is blocked.
	GetOpenEnvironmentBlocker(ctx context.Context, repositoryWorkspaceID string) (EnvironmentBlocker, error)
}

// RecordBaselineAttemptRequest is what a caller supplies to
// ReadinessRepository.RecordBaselineAttempt. ExitCode is populated only
// for Outcome GREEN/RED (the command genuinely ran to completion);
// ErrorCode/ErrorMessage are populated only for Outcome
// ENVIRONMENT_ERROR — never both, mirroring
// RecordRepositoryProbeAttemptRequest's own identical "exactly one of
// (ErrorCode/ErrorMessage) or (result fields)" discipline.
type RecordBaselineAttemptRequest struct {
	ID                    string
	ProjectID             string
	RepositoryWorkspaceID string
	RepositoryID          string
	JobID                 string
	Stage                 readiness.Stage
	Outcome               readiness.BaselineOutcome
	ExitCode              *int
	DurationMS            int64
	StdoutExcerpt         string
	StderrExcerpt         string
	ErrorCode             *string
	ErrorMessage          *string
}

// BaselineAttempt is one append-only row of the pre-change baseline
// evidence log (HE-06-M03, HE-12-M03).
type BaselineAttempt struct {
	ID                    string
	ProjectID             string
	RepositoryWorkspaceID string
	RepositoryID          string
	JobID                 string
	Stage                 readiness.Stage
	Outcome               readiness.BaselineOutcome
	ExitCode              *int
	DurationMS            int64
	StdoutExcerpt         string
	StderrExcerpt         string
	ErrorCode             *string
	ErrorMessage          *string
	CreatedAt             time.Time
}

// OpenEnvironmentBlockerRequest is what a caller supplies to
// ReadinessRepository.OpenEnvironmentBlocker.
type OpenEnvironmentBlockerRequest struct {
	ID                    string
	ProjectID             string
	RepositoryWorkspaceID string
	RepositoryID          string
	JobID                 string
	Reason                string
}

// EnvironmentBlocker is this task's own narrow, typed environment blocker
// — see ReadinessRepository's own doc comment for why this is not the
// shared cross-cutting `blockers` table. Type is always
// readiness.EnvironmentBlockerType (this task's own one, fixed reason);
// carried as a field (rather than left implicit) so a caller reading this
// struct back never has to assume which package produced it.
type EnvironmentBlocker struct {
	ID                    string
	ProjectID             string
	RepositoryWorkspaceID string
	RepositoryID          string
	JobID                 string
	Type                  string
	Reason                string
	Status                readiness.BlockerStatus
	CreatedAt             time.Time
	ResolvedAt            *time.Time
}
