package ports

import (
	"context"
	"time"
)

// ProjectionRebuildPhase is a projection rebuild operation's own closed
// lifecycle (V6-09, docs/design/08-v6-api-projections.md V6-09/V6-09A).
// RequestProjectionRebuild (internal/app/projectionrebuild) only ever
// writes ProjectionRebuildRequested — every later phase is written
// exclusively by V6-09A's own not-yet-built rebuild worker, mapping
// phase-by-phase onto that task's own Thực hiện line: ProjectionRebuildSnapshotting
// ("capture authoritative snapshot + W0 in one SQLite read snapshot"),
// ProjectionRebuildBuilding ("Build shadow, replay >W0; checkpoint
// progress"), ProjectionRebuildCuttingOver ("Acquire per-project cutover
// lease ... catch up bounded delta to W1, then one tx CASes active
// generation/cursor, operation and job under lease fence"). This task
// (V6-09) never writes SNAPSHOTTING/BUILDING/CUTTING_OVER/SUCCEEDED/FAILED
// — they exist here now only so the closed set/CHECK constraint (migration
// 0041_projection_rebuild_operations.sql) and GetProjectionRebuildStatus's
// own public surface never need a breaking schema change once V6-09A
// starts writing them.
type ProjectionRebuildPhase string

const (
	// ProjectionRebuildRequested is the one phase this task ever writes:
	// intent+job+event+receipt durably recorded, no worker has touched it
	// yet.
	ProjectionRebuildRequested ProjectionRebuildPhase = "REQUESTED"
	// ProjectionRebuildSnapshotting is V6-09A's own "capture authoritative
	// snapshot + W0" phase.
	ProjectionRebuildSnapshotting ProjectionRebuildPhase = "SNAPSHOTTING"
	// ProjectionRebuildBuilding is V6-09A's own "build shadow, replay >W0"
	// phase.
	ProjectionRebuildBuilding ProjectionRebuildPhase = "BUILDING"
	// ProjectionRebuildCuttingOver is V6-09A's own "acquire per-project
	// cutover lease ... CASes active generation/cursor" phase.
	ProjectionRebuildCuttingOver ProjectionRebuildPhase = "CUTTING_OVER"
	// ProjectionRebuildSucceeded is the terminal success outcome —
	// cutover committed, the shadow generation is now the active one.
	ProjectionRebuildSucceeded ProjectionRebuildPhase = "SUCCEEDED"
	// ProjectionRebuildFailed is the terminal failure outcome — see
	// ProjectionRebuildOperation.ErrorCode/ErrorMessage for the safe
	// summary of why.
	ProjectionRebuildFailed ProjectionRebuildPhase = "FAILED"
)

// NonterminalProjectionRebuildPhases is the exhaustive "in progress, not
// yet REQUESTED-only clarification: includes REQUESTED itself" set this
// package's own active-operation conflict check and migration
// 0041's own partial unique index both key on — kept as one shared,
// named slice (rather than each site re-deriving IsTerminal's own
// negation) specifically so the SQL migration's own WHERE clause has one
// obvious Go-side source of truth to stay in sync with; the migration
// itself still hardcodes the same four values (SQL cannot reference a Go
// slice), so a change here must be mirrored there by hand.
var NonterminalProjectionRebuildPhases = []ProjectionRebuildPhase{
	ProjectionRebuildRequested, ProjectionRebuildSnapshotting, ProjectionRebuildBuilding, ProjectionRebuildCuttingOver,
}

// IsTerminal reports whether p is one no further transition ever leaves
// (SUCCEEDED/FAILED) — the "nonterminal" vocabulary V6-09's own design
// line uses for RequestProjectionRebuild's own active-conflict check.
func (p ProjectionRebuildPhase) IsTerminal() bool {
	return p == ProjectionRebuildSucceeded || p == ProjectionRebuildFailed
}

// ProjectionRebuildOperation is one requested-or-in-progress-or-finished
// rebuild of a single (ProjectID, ProjectionName)'s own projection onto a
// fresh generation — V6-09's own "exact-operation status" record.
// RequestProjectionRebuild (internal/app/projectionrebuild) only ever
// writes a fresh row at Phase ProjectionRebuildRequested with every field
// below W0 at its own zero value; W0/ShadowGeneration/ShadowCursor/
// CutoverCursor/ErrorCode/ErrorMessage are populated only by V6-09A's own
// not-yet-built worker, as it actually progresses through the later
// phases.
type ProjectionRebuildOperation struct {
	ID             string
	ProjectID      string
	ProjectionName string
	Phase          ProjectionRebuildPhase
	// W0 is the starting watermark (global JournalPosition) V6-09A's own
	// snapshot step captures — nil until SNAPSHOTTING sets it.
	W0 *uint64
	// ShadowGeneration is the brand-new generation number V6-09A's own
	// worker builds into, distinct from projection_generations' own
	// currently-active generation for this (ProjectID, ProjectionName)
	// until cutover swaps it in — nil until SNAPSHOTTING assigns it.
	ShadowGeneration *uint64
	// ShadowCursor is the shadow generation's own build-progress
	// watermark — nil until BUILDING makes its first checkpointed
	// progress.
	ShadowCursor *uint64
	// CutoverCursor is the exact cursor position CUTTING_OVER's own
	// atomic swap commits — the shadow's caught-up-to-W1 position — nil
	// until CUTTING_OVER reaches that swap.
	CutoverCursor *uint64
	// ErrorCode/ErrorMessage are the safe (never a raw internal cause,
	// stack trace or SQL error string) failure summary for a FAILED
	// operation — mirrors apperror.Error's own "Message/Details ... safe
	// to log, return over the API, or show an operator" discipline. Both
	// empty for every phase before FAILED; this task never populates
	// either.
	ErrorCode    string
	ErrorMessage string
	// JobID is the durable job RequestProjectionRebuild enqueued
	// alongside this operation, atomically, in the same transaction —
	// V6-09A's own worker claims exactly this job to begin the rebuild.
	JobID       string
	RequestedAt time.Time
	UpdatedAt   time.Time
	Version     uint64
}

// ProjectionRebuildRepository is V6-09's own Tx accessor for
// projection_rebuild_operations (migration 0041) — deliberately narrow,
// mirroring ProjectionRepository's own "pure CRUD/CAS, no decision"
// discipline: no method here ever performs real rebuild work (V6-09A's
// own worker's job, not yet built) or reads/writes projection_rows/
// projection_checkpoints/projection_generations/projection_poison (V6-08's
// schema, migration 0039) — those stay ProjectionRepository's own,
// unchanged by this task.
type ProjectionRebuildRepository interface {
	// CreateOperation inserts a brand-new operation row. Idempotent by ID
	// only (mirrors WorkRepository.CreateReleaseSetLocalCommit's own
	// discipline): a duplicate insert of the identical already-minted ID
	// returns the already-stored row unchanged rather than erroring — the
	// caller's own receipt-replay check (RequestProjectionRebuild) is what
	// actually prevents this from mattering in practice, this is only the
	// same defensive symmetry every other Create* method in this codebase
	// already keeps.
	CreateOperation(ctx context.Context, op ProjectionRebuildOperation) (ProjectionRebuildOperation, error)
	// GetOperation returns exactly one operation by ID, or
	// ErrPersistenceNotFound — V6-09's own "exact operation lookup" Verify
	// line; GetProjectionRebuildStatus's own only data source.
	GetOperation(ctx context.Context, id string) (ProjectionRebuildOperation, error)
	// GetActiveOperation returns the current nonterminal
	// (NonterminalProjectionRebuildPhases) operation for (projectID,
	// projectionName), if any — (zero value, false, nil) when none
	// exists. RequestProjectionRebuild's own eligibility check is the only
	// caller: a fresh request (new IdempotencyKey, no existing receipt)
	// while one is already active must return a typed conflict carrying
	// that operation's own ID (V6-09's own Thực hiện line), never let a
	// second, concurrently racing rebuild get created.
	GetActiveOperation(ctx context.Context, projectID, projectionName string) (ProjectionRebuildOperation, bool, error)
}
