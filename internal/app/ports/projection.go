package ports

import (
	"context"
	"errors"
	"time"
)

// ProjectionStatus is a projection generation's own freshness/health
// signal (V6-08, docs/design/08-v6-api-projections.md V6-08, AK-ARCH-022:
// "poison event đưa projection sang DEGRADED/STALE thay vì bỏ qua"). It is
// never the runtime/readiness authority — V6-08's own "Không làm" line and
// contract rule 5 ("Projection không là authority") both forbid any reader
// of this status from ever feeding it back into a command's own
// authorization or readiness decision.
type ProjectionStatus string

const (
	// ProjectionLive is a healthy projection: the live consumer (V6-08A)
	// is caught up to (or within its own configured lag budget of) the
	// current journal tip, with no unresolved poison event for this
	// generation.
	ProjectionLive ProjectionStatus = "LIVE"
	// ProjectionDegraded is set once a poison event has been recorded for
	// this generation (RecordProjectionPoison) — the checkpoint freezes at
	// its last-good cursor and the live consumer (V6-08A) does not
	// silently skip past it. Only a rebuild (V6-09/V6-09A) onto a fresh
	// generation clears this, never an in-place status flip.
	ProjectionDegraded ProjectionStatus = "DEGRADED"
	// ProjectionStale is set by the live consumer (V6-08A) once observed
	// lag exceeds its own configured freshness threshold — distinct from
	// DEGRADED (which means "cannot make progress at all"): a STALE
	// projection is still correct, just behind, and is expected to recover
	// to LIVE on its own once the consumer catches back up.
	ProjectionStale ProjectionStatus = "STALE"
)

// ProjectionRow is one entity's own current row inside a single named
// projection's active (or in-progress-rebuild) generation — V6-08's own
// "rows key (ProjectID, ProjectionName, Generation, EntityKey)". PayloadJSON
// is that entity's canonical (stable field order/whitespace) JSON encoding
// (see internal/app/projection's own CanonicalRowHash, which this exact
// string feeds) — the repository itself never interprets PayloadJSON's
// shape; only the projector catalog (internal/app/projection) that wrote it
// does.
type ProjectionRow struct {
	ProjectID                  string
	ProjectionName             string
	Generation                 uint64
	EntityKey                  string
	PayloadJSON                string
	LastAppliedJournalPosition uint64
	// UpdatedAt is caller-supplied (every write path in this codebase
	// threads a clock.Clock through rather than a repository ever calling
	// time.Now() itself) — a Reducer's own caller (V6-08A, not yet built)
	// supplies it from the same clock it derives the applied event's own
	// processing time from.
	UpdatedAt time.Time
}

// ProjectionCheckpoint is one (ProjectID, ProjectionName, Generation)'s own
// live-consumer watermark — V6-08's own "Cursor is greatest scanned global
// JournalPosition."
type ProjectionCheckpoint struct {
	ProjectID      string
	ProjectionName string
	Generation     uint64
	Cursor         uint64
	Status         ProjectionStatus
	// FenceToken is V6-08A's own consumer-lease fence (migration 0040) —
	// it increments on every successful AcquireOrRenewConsumerLease call
	// (including a steal of an expired lease) and never resets. A caller
	// that re-validates FenceToken inside its own later apply transaction
	// and finds it changed knows its lease was stolen and must abort
	// without committing — see AcquireOrRenewConsumerLease's own doc
	// comment for the full acquire-or-steal-if-expired mechanics.
	FenceToken uint64
	UpdatedAt  time.Time
}

// ProjectionPoisonRecord is one event the live consumer (V6-08A) could not
// apply deterministically — V6-08's own exhaustive "Gap chỉ là missing
// referenced authority, aggregate-sequence violation, corrupt payload,
// relevant unknown schema hoặc deterministic reducer failure" list; Reason
// names which of those this specific record is, never a generic string.
type ProjectionPoisonRecord struct {
	ID              string
	ProjectID       string
	ProjectionName  string
	Generation      uint64
	JournalPosition uint64
	EventType       string
	SchemaVersion   int
	Reason          string
	RecordedAt      time.Time
}

// UpsertProjectionCheckpointRequest is the CAS request for
// ProjectionRepository.UpsertProjectionCheckpoint. ExpectedCursor is the
// caller's own last-observed Cursor for this (ProjectID, ProjectionName,
// Generation) — nil means "this is the first checkpoint row for this
// generation, insert it" (the same "zero value means create" convention
// AttachmentClaimRepository.ClaimAttachmentUpload already establishes for
// its own first-insert case, adapted here for a CAS rather than an
// insert-or-find). A non-nil ExpectedCursor that does not match the
// currently stored Cursor is ErrOptimisticConflict — never a silent
// overwrite, since two concurrent apply attempts racing to advance the
// same generation's cursor must never let a stale one win.
type UpsertProjectionCheckpointRequest struct {
	ProjectID      string
	ProjectionName string
	Generation     uint64
	ExpectedCursor *uint64
	NewCursor      uint64
	NewStatus      ProjectionStatus
	// ExpectedFenceToken, when non-nil, additionally requires the stored
	// FenceToken to match before the advance succeeds — the "stale fence"
	// check V6-08A's own apply transaction runs alongside the cursor CAS,
	// so a consumer whose lease was stolen mid-batch (AcquireOrRenewConsumerLease
	// incremented FenceToken for a new holder) can never commit a batch
	// applied under its own now-invalid lease. nil skips the fence check
	// entirely (a caller with no lease concept of its own, if one is ever
	// added later, is unaffected).
	ExpectedFenceToken *uint64
	UpdatedAt          time.Time
}

// AcquireOrRenewConsumerLeaseRequest is the request for
// ProjectionRepository.AcquireOrRenewConsumerLease.
type AcquireOrRenewConsumerLeaseRequest struct {
	ProjectID      string
	ProjectionName string
	Generation     uint64
	Owner          string
	TTL            time.Duration
	Now            time.Time
}

// ConsumerLease is the result of a successful AcquireOrRenewConsumerLease
// call — the exact checkpoint state (cursor/status/fence) the caller's own
// scan-and-apply batch must build on top of, read back atomically in the
// SAME statement that granted the lease (never a separate, potentially
// stale, follow-up read).
type ConsumerLease struct {
	FenceToken uint64
	Cursor     uint64
	Status     ProjectionStatus
	LeaseUntil time.Time
}

// ProjectionRepository is V6-08's own Tx accessor for the frozen,
// generation-aware Kanban/task-detail projection schema
// (docs/design/08-v6-api-projections.md V6-08). It is deliberately pure
// CRUD/CAS — no method here decides WHAT to apply for a given domain event
// (that is internal/app/projection's own Catalog/Reducer concern) or WHEN
// to apply it (that is V6-08A's own live-scanner concern, not yet built).
// See migration 0039_projection_schema.sql's own doc comment for the full
// generation/checkpoint/poison design.
type ProjectionRepository interface {
	// GetActiveGeneration returns the currently active generation number
	// for (projectID, projectionName), or (0, false, nil) if no generation
	// has ever been created for it yet (a brand-new projection name with no
	// rows anywhere).
	GetActiveGeneration(ctx context.Context, projectID, projectionName string) (generation uint64, ok bool, err error)
	// EnsureGeneration atomically creates generation as the active
	// generation for (projectID, projectionName) if none exists yet
	// (GetActiveGeneration returned ok=false) — idempotent: calling it
	// again with the SAME generation number that is already active is a
	// no-op, never an error (mirroring ClaimAttachmentUpload's own
	// "duplicate insert of the identical value is never an error"
	// discipline). Calling it with a DIFFERENT generation number than the
	// one already active is ErrOptimisticConflict — swapping the active
	// generation to a NEW number is CutoverProjectionGeneration's own job
	// (V6-09A, not yet built), never this method's.
	EnsureGeneration(ctx context.Context, projectID, projectionName string, generation uint64, schemaVersion int, updatedAt time.Time) error
	// UpsertProjectionRow inserts or replaces one entity's own row —
	// PRIMARY KEY (ProjectID, ProjectionName, Generation, EntityKey) means a
	// second call for the same key always replaces the row wholesale
	// (never a partial merge): the caller (a Reducer) always supplies the
	// row's own complete new PayloadJSON, computed from its own prior read
	// plus the event being applied, exactly like every APPLY handler in
	// this codebase's event-sourced write side already works.
	UpsertProjectionRow(ctx context.Context, row ProjectionRow) error
	// GetProjectionRow returns one entity's own row, or
	// ErrPersistenceNotFound if no row exists yet for that
	// (ProjectID, ProjectionName, Generation, EntityKey).
	GetProjectionRow(ctx context.Context, projectID, projectionName string, generation uint64, entityKey string) (ProjectionRow, error)
	// ListProjectionRows returns every row for (projectID, projectionName,
	// generation), EntityKey ascending — the full read-model scan V6-10's
	// own filter/paging layer (or V6-09A's own snapshot/diff verify step)
	// builds on top of. This repository method itself applies no filter
	// beyond the three key columns: filtering by status/repository/
	// component (Screen 5's own "filter theo repository/component") is
	// V6-10's own HTTP-layer concern, reading PayloadJSON's decoded fields.
	ListProjectionRows(ctx context.Context, projectID, projectionName string, generation uint64) ([]ProjectionRow, error)
	// GetProjectionCheckpoint returns (ProjectID, ProjectionName,
	// Generation)'s own checkpoint, or ErrPersistenceNotFound if
	// UpsertProjectionCheckpoint has never been called for it yet.
	GetProjectionCheckpoint(ctx context.Context, projectID, projectionName string, generation uint64) (ProjectionCheckpoint, error)
	// UpsertProjectionCheckpoint is the fenced CAS that advances (or first
	// creates) a generation's own checkpoint — see
	// UpsertProjectionCheckpointRequest's own doc comment for the CAS rule.
	UpsertProjectionCheckpoint(ctx context.Context, req UpsertProjectionCheckpointRequest) error
	// RecordProjectionPoison durably records one unresolved event
	// (ProjectionPoisonRecord.ID is caller-minted, mirroring every other
	// event-sourced write in this codebase's own "application code always
	// creates the ID before the first write" convention, internal/app/idsource's
	// own package doc comment). Never mutates or deletes an existing row —
	// V6-08's own "Không làm: ... sửa raw history" discipline applies to a
	// recorded poison record exactly as it does to a domain_events row: it
	// is a permanent fact about what once could not be applied, even after
	// a later rebuild moves the active generation past it.
	RecordProjectionPoison(ctx context.Context, record ProjectionPoisonRecord) error
	// ListProjectionPoison returns every poison record for (projectID,
	// projectionName, generation), JournalPosition ascending.
	ListProjectionPoison(ctx context.Context, projectID, projectionName string, generation uint64) ([]ProjectionPoisonRecord, error)
	// AcquireOrRenewConsumerLease is V6-08A's own single-statement
	// acquire-or-steal-if-expired CAS, mirroring write_leases' own
	// acquireWriteLeasesOnce exactly (internal/adapters/sqlite/scheduling.go):
	// if no checkpoint row exists yet for (ProjectID, ProjectionName,
	// Generation), one is created (Cursor 0, Status LIVE, FenceToken 1,
	// this call's own Owner/lease). If a row already exists, this call
	// succeeds — incrementing FenceToken and replacing Owner/LeaseUntil —
	// ONLY when the CURRENTLY stored lease_until is already <= Now (i.e.
	// unheld, or held by an owner whose lease already expired); otherwise
	// it fails with ErrOptimisticConflict (someone else holds a live
	// lease — this is the "two consumers" case V6-08A's own Verify line
	// names: only one can ever hold an unexpired lease at a time).
	AcquireOrRenewConsumerLease(ctx context.Context, req AcquireOrRenewConsumerLeaseRequest) (ConsumerLease, error)

	// CutoverProjectionGeneration is V6-09A's own fenced generation swap —
	// EnsureGeneration's own doc comment above already named this method
	// ("swapping the active generation to a NEW number is
	// CutoverProjectionGeneration's own job"). Design choice, documented
	// here per V6-09A's own brief (confirmed against migration
	// 0039_projection_schema.sql's own schema, which gives
	// projection_generations no version/fence column of its own):
	// req.ExpectedGeneration IS the fence — a plain
	// `WHERE active_generation = ExpectedGeneration` compare-and-swap needs
	// no new column, exactly mirroring EnsureGeneration's own
	// already-established "second call with a different number is
	// ErrOptimisticConflict" CAS shape one row over. This is safe under
	// this Store's global BEGIN-IMMEDIATE write serialization
	// (txrunner.go, the same guarantee migration 0041's own doc comment
	// already leans on for projection_rebuild_operations): two concurrent
	// cutover attempts can never interleave their own read-then-swap, so
	// the FIRST commit to actually change active_generation away from
	// ExpectedGeneration makes every OTHER (including a stale/losing
	// worker's own) attempt targeting the SAME ExpectedGeneration affect
	// zero rows and fail closed — "a stale worker cannot swap" (V6-09A's
	// own Thực hiện line) holds by construction, not by a separate lock.
	// req.ExpectedGeneration nil means "no active generation exists yet
	// for this (ProjectID, ProjectionName)" — a plain first-ever insert
	// (mirroring EnsureGeneration's own idempotent first-create), for a
	// rebuild requested before the live consumer ever lazily created
	// generation 1. ErrOptimisticConflict when a non-nil ExpectedGeneration
	// no longer matches what is actually stored (someone else already cut
	// over, or bootstrap raced against a live consumer's own first
	// EnsureGeneration call).
	CutoverProjectionGeneration(ctx context.Context, req CutoverProjectionGenerationRequest) error

	// DiscardGeneration is V6-09A's own "shadow cleanup" primitive
	// (docs/design/08-v6-api-projections.md V6-09A Phạm vi: "resume and
	// shadow cleanup") — permanently deletes every projection_rows and
	// projection_checkpoints row for one (ProjectID, ProjectionName,
	// Generation), for a generation that a rebuild built into but never
	// (or no longer) needs: a FAILED operation's own never-activated
	// shadow, or an abandoned/orphaned shadow discarded only after a grace
	// period (see internal/app/projectionrebuildworker's own cleanup
	// step). This method itself refuses — ErrCannotDiscardActiveGeneration
	// — to delete THE generation currently named by
	// projection_generations.active_generation for this (ProjectID,
	// ProjectionName), checked fresh inside this SAME call's own
	// transaction: "never clear the active generation" (V6-09A's own
	// Không làm line) is enforced here, at the lowest layer, as defense in
	// depth beneath whatever eligibility check a caller already performed
	// — a caller bug above this method can never actually delete live
	// data. projection_poison rows for the discarded generation are
	// deliberately left untouched (never deleted) — the same "permanent
	// fact, never mutated or deleted" discipline
	// ProjectionRepository.RecordProjectionPoison's own doc comment already
	// establishes for the ACTIVE generation's poison history, kept
	// identical here rather than carving out a special case for a
	// generation that merely stopped being live.
	DiscardGeneration(ctx context.Context, projectID, projectionName string, generation uint64) error
}

// CutoverProjectionGenerationRequest is the fenced CAS request for
// ProjectionRepository.CutoverProjectionGeneration (V6-09A) — see that
// method's own doc comment for the full CAS/bootstrap contract.
type CutoverProjectionGenerationRequest struct {
	ProjectID      string
	ProjectionName string
	// ExpectedGeneration is the OLD (currently active) generation this
	// swap is fenced on — nil for the "no active generation exists yet"
	// bootstrap case.
	ExpectedGeneration *uint64
	NewGeneration      uint64
	SchemaVersion      int
	UpdatedAt          time.Time
}

// ErrCannotDiscardActiveGeneration is returned by
// ProjectionRepository.DiscardGeneration when the requested generation is
// still the currently active one — see that method's own doc comment.
var ErrCannotDiscardActiveGeneration = errors.New("projection: cannot discard the currently active generation")
