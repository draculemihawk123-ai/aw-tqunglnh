package ports

import (
	"context"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
)

// ArtifactRepository is V5-01's Tx accessor for the durable Artifact
// metadata row (docs/design/07-v5-execution-evidence.md V5-01) — the
// database-backed layer this task adds on top of the existing V1
// ArtifactStore (artifact.go, content-addressed bytes only, no metadata/
// retention/ownership concept of its own). It is deliberately its own
// accessor, not folded into an existing one: no other Tx concern owns
// evidence/attachment lifecycle, and every later V5 task that needs to
// reference an artifact (Message attachments V5-02, checkpoint diff
// capture V5-08A, gate evidence V5-10, ...) composes InsertArtifact inside
// ITS OWN transaction alongside whatever else it writes — this accessor is
// a building block those tasks call, not a command of its own.
//
// Every method here is a real, populated-now method (the same
// "AdapterBuilds/Readiness/Wait/Approvals get real methods from the start"
// treatment ports.Tx's own doc comment describes) — V5-01 owns this
// concern end to end, even though no caller composes it inside a LARGER
// transaction yet.
type ArtifactRepository interface {
	// InsertArtifact persists a new Artifact row exactly as constructed —
	// including whatever AttachState the caller already resolved (Orphan
	// or Attached both insert through this one method; there is no
	// separate "insert as orphan" method, since AttachState is just
	// another field, not a different write path). Idempotent by ID: a
	// duplicate insert of the identical ID returns the already-stored row
	// rather than erroring (the same discipline
	// createWorkItemBlockerTx/RecordRunCancellationIntent already
	// establish), so a caller retrying after an ambiguous failure never
	// risks a second row.
	//
	// It is the CALLER's own responsibility to have already called
	// ArtifactStore.Put AND Verify on a.Locator BEFORE this call — this
	// method never touches ArtifactStore itself and never runs outside a
	// caller's own transaction, so it cannot enforce that ordering on its
	// own (docs/architecture/04-go-core-spec.md §11.1: "Không gọi...
	// filesystem artifact store... trong transaction" — Put/Verify MUST
	// already be durable by the time this runs; V5-01's own Done-when bar,
	// "evidence bắt buộc không commit trước artifact durable/hash
	// verified", is enforced by this ordering, not by this method owning
	// both halves).
	//
	// a.ProjectID must name a Project that actually exists —
	// ErrPersistenceNotFound otherwise.
	//
	// V5-14: also refuses with ErrPersistenceAlreadyExists if a.Locator
	// currently has an open ClaimArtifactLocatorForPurge claim — the other
	// half of this task's own TOCTOU protection (see that method's own doc
	// comment): a sweep that is mid-way through deleting a Locator's real
	// bytes must never let a fresh insert start depending on content that
	// is about to disappear underneath it.
	InsertArtifact(ctx context.Context, a artifact.Artifact) (artifact.Artifact, error)
	// GetArtifact returns the Artifact with the given ID, or
	// ErrPersistenceNotFound.
	GetArtifact(ctx context.Context, id string) (artifact.Artifact, error)
	// TransitionArtifactAttachState is the fenced CAS that promotes a row
	// from Orphan to Attached (or, symmetrically, could record a rejected
	// worker transaction's output as Orphan after it was optimistically
	// inserted Attached, the reverse direction). V5-08B's own fenced
	// finalize (validateAndAttachFinalizationEvidenceTx, internal/app/runtime/finalize.go)
	// confirmed this is Orphan->Attached too, the same direction every
	// other caller already exercises — its own locked decision text
	// describes inserting diff-manifest artifacts as ORPHAN first, then
	// promoting them to ATTACHED inside the finalize transaction, never
	// the reverse; an earlier draft of this comment speculated V5-08B
	// might need Attached->Orphan, which turned out not to be the case.
	// ExpectedVersion mismatch (including a row no longer in ExpectedState)
	// is ErrOptimisticConflict, ErrPersistenceNotFound for an unknown ID —
	// the same CAS discipline every other transition in this codebase
	// already uses.
	TransitionArtifactAttachState(ctx context.Context, req TransitionArtifactAttachStateRequest) (artifact.Artifact, error)
	// SetArtifactHold is the fenced CAS that flips Hold independent of
	// AttachState/RetentionClass — a governance action (ADR-017) with no
	// state-machine legality check of its own (Hold can be set or cleared
	// from either AttachState, any number of times). ExpectedVersion
	// mismatch is ErrOptimisticConflict, ErrPersistenceNotFound for an
	// unknown ID.
	SetArtifactHold(ctx context.Context, req SetArtifactHoldRequest) (artifact.Artifact, error)
	// ListOrphanedArtifacts returns every Artifact currently AttachState
	// Orphan with CreatedAt <= olderThan, oldest-CreatedAt-first — the
	// candidate set a future retention sweeper (V5-14) reconciles/cleans
	// up. Deliberately unfiltered by Hold (classification is the caller's
	// own job, the same discipline RuntimeRepository.ListNodeRunsForRun's
	// own doc comment already establishes for this codebase).
	ListOrphanedArtifacts(ctx context.Context, olderThan time.Time) ([]artifact.Artifact, error)
	// ListArtifactsByLocator returns every Artifact row (any Project) that
	// currently shares locator — the retention sweeper's own group-
	// eligibility/refcount read (V5-14, ADR-017/go-core-spec §19: "Sweeper
	// phải check reference/hold atomically trước xóa"): the same
	// content-addressed Locator MAY back more than one row
	// (0027_artifacts.sql's own "content_hash is deliberately NOT unique"),
	// so a real ports.ArtifactStore.Delete against that Locator is only
	// ever safe once EVERY row this returns is independently confirmed
	// purge-eligible. Ordered by (id) for a stable, deterministic result a
	// test can assert on exactly.
	ListArtifactsByLocator(ctx context.Context, locator string) ([]artifact.Artifact, error)
	// ClaimArtifactLocatorForPurge atomically inserts a durable deletion
	// intent for locator — the fence that closes the TOCTOU gap between
	// "confirmed every row sharing this Locator is purge-eligible" and
	// "actually deleted the real bytes" (this task's own contract: "Sweeper
	// nên atomically claim Locator bằng durable deletion intent"). A
	// second claim attempt against a Locator already claimed is
	// ErrPersistenceAlreadyExists — the same sentinel EnqueueJob's own
	// duplicate-idempotency-key path already uses for an identical
	// "someone already claimed this" conflict. InsertArtifact (below) MUST
	// itself refuse a new artifact whose Locator currently has an open
	// claim, so a fresh Put of byte-identical content can never race a
	// sweep that is mid-delete of that exact content.
	ClaimArtifactLocatorForPurge(ctx context.Context, locator, claimOwner string, claimedAt time.Time) error
	// ReleaseArtifactLocatorClaim removes locator's own claim row — called
	// once a purge attempt has either committed (bytes deleted, rows
	// marked Purged) or been abandoned (group turned out ineligible after
	// all). Idempotent: releasing a Locator with no open claim is a no-op,
	// never an error, the same "already resolved" discipline every other
	// real-I/O operation in this codebase follows.
	ReleaseArtifactLocatorClaim(ctx context.Context, locator string) error
	// GetArtifactSweepState reads the one, singleton artifact_sweep_state
	// row (migration 35) — mirrors RuntimeRepository.GetRecoveryReaperState
	// exactly, for the sibling ARTIFACT_SWEEP self-rescheduling CONTROL job
	// (internal/app/artifactsweep). Returns ErrPersistenceNotFound only if
	// migration 35 itself somehow never ran.
	GetArtifactSweepState(ctx context.Context) (ArtifactSweepState, error)
	// AdvanceArtifactSweepGeneration is the fenced CAS the sweep's own
	// self-rescheduling job uses to bump Generation by exactly one,
	// mirroring RuntimeRepository.AdvanceRecoveryReaperGeneration.
	// ErrOptimisticConflict on a stale caller.
	AdvanceArtifactSweepGeneration(ctx context.Context, req AdvanceArtifactSweepGenerationRequest) (ArtifactSweepState, error)
	// SetArtifactSweepDryRun is the fenced CAS an explicit operator action
	// uses to flip DryRun independent of Generation — this task's own
	// contract: "Dry-run và report là mặc định; thao tác thật phải
	// explicit." ErrOptimisticConflict on a stale caller.
	SetArtifactSweepDryRun(ctx context.Context, req SetArtifactSweepDryRunRequest) (ArtifactSweepState, error)
}

// ArtifactSweepState is the retention sweeper's own singleton generation
// cursor plus its DryRun mode (V5-14, migration 35) — mirrors
// RecoveryReaperState's own identical generation-cursor shape, with DryRun
// added since this coordinator, unlike RecoveryReaper, has a real/no-op
// distinction an operator must explicitly cross.
type ArtifactSweepState struct {
	Generation uint64
	DryRun     bool
	Version    uint64
}

// AdvanceArtifactSweepGenerationRequest is the CAS request for
// ArtifactRepository.AdvanceArtifactSweepGeneration (V5-14).
type AdvanceArtifactSweepGenerationRequest struct {
	ExpectedGeneration uint64
	ExpectedVersion    uint64
}

// SetArtifactSweepDryRunRequest is the CAS request for
// ArtifactRepository.SetArtifactSweepDryRun (V5-14).
type SetArtifactSweepDryRunRequest struct {
	DryRun          bool
	ExpectedVersion uint64
}

// TransitionArtifactAttachStateRequest is the CAS request for
// ArtifactRepository.TransitionArtifactAttachState (V5-01).
type TransitionArtifactAttachStateRequest struct {
	ArtifactID      string
	ExpectedState   artifact.AttachState
	ExpectedVersion uint64
	NextState       artifact.AttachState
}

// SetArtifactHoldRequest is the CAS request for
// ArtifactRepository.SetArtifactHold (V5-01).
type SetArtifactHoldRequest struct {
	ArtifactID      string
	ExpectedVersion uint64
	Hold            bool
}
