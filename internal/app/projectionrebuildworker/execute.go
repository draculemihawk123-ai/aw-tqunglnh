// Package projectionrebuildworker is V6-09A's own rebuild worker, fenced
// cutover and recovery (docs/design/08-v6-api-projections.md V6-09A) — the
// "not-yet-built worker" internal/app/projectionrebuild's own package doc
// comment and internal/app/ports/projectionrebuild.go's own
// ProjectionRebuildPhase doc comment both already anticipated: this
// package is the ONLY writer of every phase past REQUESTED
// (SNAPSHOTTING/BUILDING/CUTTING_OVER/SUCCEEDED/FAILED). Deliberately its
// own package, importing internal/app/projectionrebuild (for
// ProjectionRebuildJobKind and the operation model) and
// internal/app/projection (for the Catalog/Reducer inventory and the
// shared scan-and-apply core, ReplayGenerationBatch) rather than being
// folded into either — those two packages' own doc comments both
// explicitly commit to never depending on this one.
//
// # The big picture: snapshot, build, cutover
//
// A rebuild's own operation record (ports.ProjectionRebuildOperation,
// migration 0041) walks a fixed phase sequence, this package's own
// ExecuteProjectionRebuild the sole driver of every step:
//
//  1. SNAPSHOTTING (snapshotStep): in ONE SQLite transaction, read the
//     CURRENTLY ACTIVE generation's own checkpoint cursor as W0, copy
//     every one of its rows wholesale into a brand-new shadow generation
//     number (oldGeneration+1, or 1 if no active generation exists yet),
//     seed the shadow's own checkpoint at cursor=W0, and persist
//     W0/ShadowGeneration on the operation row. See this file's own
//     "Design decision: snapshot = copy, not a second read mechanism"
//     section below for why copying rows is the "authoritative snapshot"
//     the design brief asks for, not a new authoritative-table read.
//  2. BUILDING (buildRound, phase=BUILDING): repeated, bounded
//     ReplayGenerationBatch rounds — the EXACT SAME scan/classify/reduce/
//     upsert/checkpoint-CAS core the live consumer (internal/app/projection's
//     own ApplyBatch) uses, via applyLeasedBatch shared between them —
//     replay every event with JournalPosition > W0 into the shadow
//     generation, checkpointing ShadowCursor onto the operation row after
//     every round (V6-09A's own "checkpoint progress"). Continues until a
//     round returns fewer events than BatchSize (caught up). EVERY round
//     (BUILDING's own repeated ones AND CUTTING_OVER's own bounded
//     catch-up ones) first revalidates the driving job's own lease
//     (validateJobLease) BEFORE ever touching the shadow generation's own
//     consumer lease — an already-dead job claim must never be able to
//     acquire or even harmlessly renew the shadow lease, since doing so
//     would otherwise let it briefly block (for up to Deps.ShadowLeaseTTL)
//     a legitimate successor's own takeover attempt.
//  3. CUTTING_OVER (buildRound again, phase=CUTTING_OVER, then cutover):
//     a small, BOUNDED number of additional catch-up rounds (Deps.MaxCutoverCatchUpRounds)
//     absorb any events that arrived while BUILDING was finishing, then
//     ONE atomic transaction (cutover) re-validates every fence fresh and
//     performs the real swap. See this file's own "The cutover
//     transaction" section below — this is the function the reviewer
//     should read most carefully.
//
// A poison event encountered during BUILDING or CUTTING_OVER's own
// catch-up rounds marks the operation FAILED (failOperation) and stops —
// the OLD (still active) generation is never touched by any of the above,
// so "poison keeps the old generation active" holds trivially: this
// worker simply never had a reason to write to it.
//
// # Design decision: snapshot = copy, not a second read mechanism
//
// The design brief's own "Thực hiện" line asks for capturing "an
// authoritative snapshot + starting watermark W0 in ONE SQLite read
// snapshot," then building the shadow by replaying events ">W0". Two
// designs could satisfy this: (a) read every relevant AUTHORITATIVE table
// (work items, runs, blockers, scope-expansion requests, ...) directly and
// materialize shadow rows from that snapshot, or (b) copy the CURRENTLY
// ACTIVE generation's own already-correct rows (which are, by V6-08's own
// invariant, already a byte-exact deterministic function of the event
// journal up to their own checkpoint cursor) as the starting point, using
// that checkpoint's own cursor as W0.
//
// This package chooses (b). Design (a) would require inventing a SECOND,
// parallel "read current state directly" mechanism for every one of the
// Catalog's ~20 Apply entries, alongside the event-driven Reducer this
// codebase already has for each — directly contradicting V6-08's own
// "Hoàn thành khi: live consumer/rebuild dùng cùng frozen schema and
// reducer set WITHOUT NEW DESIGN CHOICE." Design (b) needs no new
// materialization logic at all: the active generation's rows ARE the
// snapshot (they cannot be anything else and still be correct, by V6-08A's
// own already-proven invariants), and W0 = that generation's own
// checkpoint cursor is exactly "the starting watermark" the brief asks
// for — the row copy plus a freshly-seeded checkpoint at cursor=W0
// (instead of the usual cursor=0 a brand-new generation would otherwise
// start at) is what makes ReplayGenerationBatch's very next round correctly
// resume scanning from W0 forward rather than re-applying events 1..W0 a
// second time. "In ONE SQLite read snapshot" is satisfied because the
// read (old rows + old checkpoint cursor) and the write (new rows + new
// checkpoint) all happen inside ONE WithSerializedWrite transaction
// (snapshotStep) — SQLite's own transaction isolation makes that read
// trivially consistent, with no separate isolation mechanism needed.
//
// The one accepted tradeoff: this makes SNAPSHOTTING one single,
// unbounded-size transaction proportional to the active generation's own
// row count (never chunked/bounded the way BUILDING's own rounds are) —
// documented here as a deliberate, scale-bounded-by-real-data-size choice,
// not an oversight; a projection large enough for this to matter is
// already well beyond what this codebase's Alpha-stage projection schema
// targets.
//
// # The cutover transaction (cutover, below)
//
// This is the function most worth reading in full before merging. Every
// fence is revalidated FRESH, inside this ONE transaction, immediately
// before the real, externally-visible state transition — mirroring
// internal/app/releasesetcommit/execute.go's own "Two leases, both
// revalidated, both required" discipline exactly, with a THIRD fence this
// operation additionally needs:
//
//  1. The operation's own row is reloaded fresh (never the copy the
//     caller passed in) and checked for CUTTING_OVER — any other phase
//     (including a terminal one another worker already reached) stops
//     this transaction before it touches anything.
//  2. The JOB's own JobLease is revalidated (tx.Jobs().ValidateActiveJob)
//     — proves THIS worker instance still legitimately owns processing
//     this job, exactly like every other fenced finalize in this
//     codebase.
//  3. The "cutover lease" — see this package's own "Design decision: the
//     cutover lease IS the projection consumer lease" section below — is
//     revalidated by calling AcquireOrRenewConsumerLease again, fresh,
//     against the SHADOW generation's own checkpoint row, using an Owner
//     string unique to this exact job CLAIM (see deriveOwner below). A
//     stale worker whose claim was reclaimed, or whose shadow lease was
//     stolen by a genuinely live builder, gets ErrOptimisticConflict here
//     and the WHOLE transaction aborts before step 4.
//  4. ONLY once all three fences above hold does this function read the
//     CURRENT active generation (fresh, inside this same transaction) and
//     CAS it to the shadow generation
//     (ports.ProjectionRepository.CutoverProjectionGeneration) — a single
//     UPDATE of a single row, so a reader observes the OLD generation or
//     the NEW one, never a mix (SQLite's own atomic single-statement
//     visibility).
//  5. The operation's own phase CASes to SUCCEEDED with CutoverCursor set
//     to the shadow lease's own current cursor (W1) — ExpectedVersion
//     fenced against the SAME fresh read from step 1, so a version bump
//     by ANY other concurrent actor between step 1 and here aborts the
//     whole transaction rather than silently overwriting it.
//  6. The driving job is completed, in the SAME transaction — never
//     released/completed separately, so a crash between 4/5 succeeding
//     and 6 committing is impossible: either the WHOLE transaction
//     commits (generation swapped, operation SUCCEEDED, job done) or NONE
//     of it does (old generation still active, operation still
//     CUTTING_OVER, job still claimable) — "kill mid-transaction is
//     impossible so verify no partial state can exist by construction"
//     (the same approach V6-09 itself already used), which is exactly why
//     this whole function is ONE uow.WithSerializedWrite call and nothing
//     inside it is ever allowed to be a separate transaction.
//
// # Design decision: the cutover lease IS the projection consumer lease
//
// The design brief asks for "a per-project cutover lease shared with the
// live consumer." There is no separate lease table or method for this
// today. Re-reading internal/app/projection/consumer.go's own lease-acquire
// code (ApplyBatch, and now applyLeasedBatch/ReplayGenerationBatch which
// this package calls) confirms the reading the brief itself already
// suggests is sound: ProjectionRepository.AcquireOrRenewConsumerLease is
// ALREADY keyed by (ProjectID, ProjectionName, Generation) — not "the
// active generation" specifically, just whatever Generation a caller
// names. The live consumer only ever calls it with the ACTIVE generation
// (resolved via GetActiveGeneration); this package only ever calls it with
// the SHADOW generation (an explicit, never-active number, via
// ReplayGenerationBatch during BUILDING/CUTTING_OVER and directly inside
// cutover's own final revalidation). The two can therefore never collide
// on the SAME row — by construction, since a generation can never be both
// "active" and "the shadow a rebuild is building" at once — while still
// going through the IDENTICAL acquire-or-steal-if-expired mechanism,
// giving this worker's own build/cutover process the exact same
// "stale/dead builder detection" ApplyBatch's own doc comment describes
// for the live consumer, with zero new lease infrastructure. This is why
// "shared with the live consumer" is true in the sense that matters: same
// mechanism, same fencing guarantees, same code path — not the same ROW,
// which would be actively wrong (the live consumer must never be blocked
// by, or itself block, a rebuild's own shadow-generation lease).
//
// One consequence, accepted and documented rather than worked around: the
// live consumer's OWN first post-cutover AcquireOrRenewConsumerLease call
// against the NEWLY-active generation's checkpoint (created by
// snapshotStep, already carrying this worker's own lease_owner/lease_until
// from its last BUILDING/cutover round) can be briefly blocked until that
// lease's own TTL naturally elapses — there is no separate "release"
// method for a projection consumer lease anywhere in this codebase
// (ProjectionRepository's own interface never had one, by design, even
// before this task). Deps.ShadowLeaseTTL is deliberately short (see its
// own doc comment) specifically to bound this handoff gap tightly; it is
// a known, bounded, self-healing behavior, not a bug.
package projectionrebuildworker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
)

// aggregateType MUST match internal/app/projectionrebuild's own unexported
// aggregateType constant exactly — the value RequestProjectionRebuild
// (that package's own commands.go) already enqueues every
// PROJECTION_REBUILD job's own AggregateType as, and the value
// ValidateActiveJob/CompleteJob below fence every write in this package
// against. Duplicated as a literal (rather than exported from that
// package) because that package's own doc comment deliberately commits to
// having no dependency on this one; this package depending on a literal
// string it does not own is the accepted, narrower coupling.
const aggregateType = "ProjectionRebuildOperation"

// poisonErrorCode is the safe (never a raw internal cause) ErrorCode this
// package records on a FAILED operation when BUILDING or CUTTING_OVER's
// own catch-up hits a poison event — mirrors
// ports.ProjectionRebuildOperation.ErrorCode's own "safe... summary"
// discipline; ports.ApplyBatchOutcome.PoisonReason (surfaced as
// ErrorMessage) is itself already a controlled, hand-written diagnostic
// string from this codebase's own poison-classification vocabulary
// (internal/app/projection/consumer.go), never a raw driver/SQL error.
const poisonErrorCode = "POISON_EVENT"

// abandonedErrorCode is cleanup.go's own ErrorCode for an operation this
// package's own CleanupOrphanedShadowGeneration marks FAILED because no
// worker made progress on it within the configured grace period.
const abandonedErrorCode = "WORKER_ABANDONED"

// Deps bundles ExecuteProjectionRebuild's collaborators — the same
// UnitOfWork/IDs/port-not-adapter shape every other worker Deps struct in
// this codebase already establishes (see
// internal/app/releasesetcommit.ExecuteReleaseSetLocalCommitDeps for the
// closest sibling).
type Deps struct {
	UnitOfWork ports.UnitOfWork
	IDs        idsource.Source
	// Catalog is the SAME *projection.Catalog instance a caller's own live
	// consumer already uses — never a second, independently-constructed
	// one: reusing the identical Catalog value is what this package's own
	// "never invent a second classification pass" discipline (the design
	// brief's own words) means concretely.
	Catalog *projection.Catalog
	// BatchSize bounds how many journal rows one BUILDING/CUTTING_OVER
	// round scans — mirrors projection.ApplyBatchRequest.BatchSize
	// exactly; also this package's own "caught up" signal (a round
	// returning FEWER than BatchSize events means nothing more is waiting
	// right now).
	BatchSize int
	// ShadowLeaseTTL bounds how long this worker's own claim on the
	// shadow generation's own checkpoint lease stays valid without a
	// renewal. Deliberately independent from (and typically much SHORTER
	// than) whatever TTL a live consumer uses for the ACTIVE generation —
	// see this package's own doc comment ("Design decision: the cutover
	// lease IS the projection consumer lease") for why a short value here
	// tightly bounds the live consumer's own post-cutover handoff wait,
	// and why "dead builder" detection needs this to be short enough that
	// a genuinely stuck/crashed worker's own shadow lease is recoverable
	// within a reasonable time.
	ShadowLeaseTTL time.Duration
	// MaxCutoverCatchUpRounds bounds CUTTING_OVER's own "catch up a
	// bounded delta to W1" step (V6-09A's own Thực hiện line): this many
	// additional BUILDING-style rounds run, absorbing whatever events
	// arrived while the main BUILDING phase was finishing, before this
	// worker freezes wherever it has reached as W1 and attempts the swap
	// regardless of whether MORE events keep arriving — any event after
	// W1 is simply picked up normally by the live consumer once cutover
	// completes.
	MaxCutoverCatchUpRounds int
}

func (d Deps) validate() (Deps, error) {
	if d.UnitOfWork == nil || d.IDs == nil || d.Catalog == nil {
		return d, errors.New("projectionrebuildworker: Deps requires UnitOfWork/IDs/Catalog")
	}
	if d.BatchSize <= 0 {
		d.BatchSize = 500
	}
	if d.ShadowLeaseTTL <= 0 {
		d.ShadowLeaseTTL = 30 * time.Second
	}
	if d.MaxCutoverCatchUpRounds <= 0 {
		d.MaxCutoverCatchUpRounds = 3
	}
	return d, nil
}

// ExecuteProjectionRebuild is this package's own internal worker entry
// point — see this file's own doc comment for the full phase-by-phase
// contract. job must be a currently-LEASED DurableJob of
// projectionrebuild.ProjectionRebuildJobKind (Handler.Handle, this
// package's only production caller, guarantees this) whose AggregateID is
// the ProjectionRebuildOperation's own ID (RequestProjectionRebuild's own
// EnqueueJob call already sets this, see internal/app/projectionrebuild/commands.go).
//
// Every phase transition below re-loads the operation FRESH from storage
// immediately before deciding what to do — never trusting a copy read in
// an earlier transaction or an earlier loop iteration — and every write is
// version-fenced (AdvanceOperation) or lease-fenced (JobLease, the shadow
// generation's own consumer lease) or both. A fenced CAS losing to another
// worker's own concurrent, more-current advance of the SAME operation
// (ErrOptimisticConflict from AdvanceOperation specifically) is always
// treated as "nothing more for THIS invocation to do" and returns nil —
// never an error — since it means some OTHER worker's transaction has
// already committed further progress. A lease conflict while trying to
// make NEW progress (ReplayGenerationBatch's own AcquireOrRenewConsumerLease
// call, or the same call inside cutover's own final revalidation) is
// instead propagated as a real, retryable error, since it may simply mean
// a genuinely live competing attempt currently holds the lease.
func ExecuteProjectionRebuild(ctx context.Context, deps Deps, job ports.DurableJob) error {
	deps, err := deps.validate()
	if err != nil {
		return err
	}
	if job.LeaseUntil == nil {
		return errors.New("projectionrebuildworker: job has no active lease")
	}
	jobLease := ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken, LeaseUntil: *job.LeaseUntil}
	operationID := job.AggregateID
	if operationID == "" {
		return fmt.Errorf("projectionrebuildworker: job %s has no AggregateID", job.ID)
	}
	owner := deriveOwner(jobLease)

	op, err := loadOperation(ctx, deps.UnitOfWork, operationID)
	if err != nil {
		return fmt.Errorf("projectionrebuildworker: load operation %s: %w", operationID, err)
	}

	cutoverRounds := 0
	for {
		if op.Phase.IsTerminal() {
			// Already SUCCEEDED or FAILED — a redelivered/duplicate job
			// claim (replay), or another worker finished it first. Nothing
			// more to do, and no second cutover/failure is ever attempted.
			return nil
		}

		switch op.Phase {
		case ports.ProjectionRebuildRequested:
			updated, stopped, err := snapshotStep(ctx, deps, jobLease, op)
			if err != nil {
				return fmt.Errorf("projectionrebuildworker: snapshot operation %s: %w", operationID, err)
			}
			if stopped {
				return nil
			}
			op = updated

		case ports.ProjectionRebuildSnapshotting, ports.ProjectionRebuildBuilding:
			result, err := buildRound(ctx, deps, jobLease, op, ports.ProjectionRebuildBuilding, owner)
			if err != nil {
				return fmt.Errorf("projectionrebuildworker: build operation %s: %w", operationID, err)
			}
			if result.stopped || result.poisoned {
				return nil
			}
			op = result.operation
			if result.caughtUp {
				updated, stopped, err := enterCuttingOver(ctx, deps, jobLease, op)
				if err != nil {
					return fmt.Errorf("projectionrebuildworker: enter cutting-over for operation %s: %w", operationID, err)
				}
				if stopped {
					return nil
				}
				op = updated
			}

		case ports.ProjectionRebuildCuttingOver:
			if cutoverRounds < deps.MaxCutoverCatchUpRounds {
				result, err := buildRound(ctx, deps, jobLease, op, ports.ProjectionRebuildCuttingOver, owner)
				if err != nil {
					return fmt.Errorf("projectionrebuildworker: cutover catch-up for operation %s: %w", operationID, err)
				}
				if result.stopped || result.poisoned {
					return nil
				}
				op = result.operation
				cutoverRounds++
				if !result.caughtUp {
					continue
				}
			}
			if err := cutover(ctx, deps, jobLease, op, owner); err != nil {
				return fmt.Errorf("projectionrebuildworker: cutover operation %s: %w", operationID, err)
			}
			return nil

		default:
			return fmt.Errorf("projectionrebuildworker: operation %s has unexpected phase %q", operationID, op.Phase)
		}
	}
}

// deriveOwner derives this exact job CLAIM's own unique lease-owner string
// for the shadow generation's own consumer lease — jobLease.Owner ALONE
// (stable per worker PROCESS across every job it ever claims,
// workerpool.Pool's own "%s-%d" Owner/workerIndex convention) is not
// enough: it would treat a crashed-and-reclaimed-by-the-SAME-process retry
// as "the same owner renewing its own lease" (AcquireOrRenewConsumerLease's
// own "same owner may always renew" branch), which defeats the whole point
// of fencing a STALE claim out. Appending jobLease.Token — which
// ClaimJob/RecoverExpiredJobs increments on every distinct claim,
// including a reclaim of an expired lease by the exact same
// owner/process — makes every separate claim of the same job a distinct
// lease owner for this purpose: a stale worker's OWN token never matches
// whatever token a later, fresh claim was granted, so it can only ever
// RENEW a lease it is still actively using, never silently inherit one a
// newer claim already holds.
func deriveOwner(jobLease ports.JobLease) string {
	return fmt.Sprintf("%s:%d", jobLease.Owner, jobLease.Token)
}

func loadOperation(ctx context.Context, uow ports.UnitOfWork, operationID string) (ports.ProjectionRebuildOperation, error) {
	var op ports.ProjectionRebuildOperation
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.ProjectionRebuilds().GetOperation(ctx, operationID)
		op = loaded
		return err
	})
	return op, err
}

// validateJobLease is a plain, read-only fencing check (ValidateActiveJob
// is documented as read-only, see ports.JobsRepository's own doc comment)
// — buildRound's own first step, checked fresh every round; see that
// function's own comment on why this must happen BEFORE the shadow
// generation's own consumer lease is ever touched.
func validateJobLease(ctx context.Context, uow ports.UnitOfWork, jobLease ports.JobLease, operationID string) error {
	return uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		return tx.Jobs().ValidateActiveJob(ctx, jobLease, aggregateType, operationID)
	})
}

// snapshotStep performs SNAPSHOTTING (see this file's own doc comment,
// "Design decision: snapshot = copy, not a second read mechanism", for the
// full design) — the only step that ever transitions an operation away
// from REQUESTED. Idempotent-resume-safe: if op has already moved past
// REQUESTED by the time this transaction actually runs (another worker
// raced ahead), this returns that fresh state with stopped=false so the
// top-level loop simply re-dispatches on it next iteration, never
// double-snapshotting.
func snapshotStep(ctx context.Context, deps Deps, jobLease ports.JobLease, op ports.ProjectionRebuildOperation) (ports.ProjectionRebuildOperation, bool, error) {
	now := time.Now().UTC()
	var result ports.ProjectionRebuildOperation
	var stopped bool

	err := deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		fresh, err := tx.ProjectionRebuilds().GetOperation(ctx, op.ID)
		if err != nil {
			return err
		}
		if fresh.Phase.IsTerminal() {
			result, stopped = fresh, true
			return nil
		}
		if fresh.Phase != ports.ProjectionRebuildRequested {
			// Someone else already snapshotted (or moved further) —
			// nothing for THIS call to do; return the fresh state and let
			// the top-level loop re-dispatch on it.
			result = fresh
			return nil
		}
		if err := tx.Jobs().ValidateActiveJob(ctx, jobLease, aggregateType, op.ID); err != nil {
			return err
		}

		oldGeneration, oldOK, err := tx.Projections().GetActiveGeneration(ctx, op.ProjectID, op.ProjectionName)
		if err != nil {
			return err
		}

		var w0, shadowGeneration uint64
		if oldOK {
			shadowGeneration = oldGeneration + 1
			checkpoint, err := tx.Projections().GetProjectionCheckpoint(ctx, op.ProjectID, op.ProjectionName, oldGeneration)
			if err != nil && !errors.Is(err, ports.ErrPersistenceNotFound) {
				return err
			}
			if err == nil {
				w0 = checkpoint.Cursor
			}
			// Copy the active generation's own rows wholesale — see this
			// file's own doc comment for why this IS "capturing an
			// authoritative snapshot," not a shortcut around it.
			rows, err := tx.Projections().ListProjectionRows(ctx, op.ProjectID, op.ProjectionName, oldGeneration)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if err := tx.Projections().UpsertProjectionRow(ctx, ports.ProjectionRow{
					ProjectID: op.ProjectID, ProjectionName: op.ProjectionName, Generation: shadowGeneration,
					EntityKey: row.EntityKey, PayloadJSON: row.PayloadJSON,
					LastAppliedJournalPosition: row.LastAppliedJournalPosition, UpdatedAt: now,
				}); err != nil {
					return err
				}
			}
		} else {
			// No active generation exists yet (a rebuild requested before
			// the live consumer ever lazily created generation 1) —
			// nothing to copy, W0=0, shadow starts as generation 1.
			shadowGeneration = 1
		}

		// Seed the shadow's own checkpoint starting AT w0 (never the usual
		// cursor=0 a brand-new generation's first AcquireOrRenewConsumerLease
		// call would otherwise default to) — this is what makes BUILDING's
		// very first ReplayGenerationBatch round correctly resume scanning
		// from w0 forward, rather than re-applying events 1..w0 on top of
		// rows that already reflect them.
		if err := tx.Projections().UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: op.ProjectID, ProjectionName: op.ProjectionName, Generation: shadowGeneration,
			ExpectedCursor: nil, NewCursor: w0, NewStatus: ports.ProjectionLive, UpdatedAt: now,
		}); err != nil {
			return err
		}

		updated, err := tx.ProjectionRebuilds().AdvanceOperation(ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: op.ID, ExpectedVersion: fresh.Version, NextPhase: ports.ProjectionRebuildSnapshotting,
			NextW0: &w0, NextShadowGeneration: &shadowGeneration, UpdatedAt: now,
		})
		if err != nil {
			if errors.Is(err, ports.ErrOptimisticConflict) {
				latest, getErr := tx.ProjectionRebuilds().GetOperation(ctx, op.ID)
				if getErr != nil {
					return getErr
				}
				result, stopped = latest, true
				return nil
			}
			return err
		}
		result = updated
		return nil
	})
	if err != nil {
		return ports.ProjectionRebuildOperation{}, false, err
	}
	return result, stopped, nil
}

// buildRoundResult is buildRound's own outcome.
type buildRoundResult struct {
	operation ports.ProjectionRebuildOperation
	// caughtUp is true when this round scanned fewer than Deps.BatchSize
	// events — nothing more is waiting right now.
	caughtUp bool
	// poisoned is true when this round hit a poison event — the operation
	// has already been marked FAILED (failOperation) by the time this is
	// set; the caller has nothing more to do.
	poisoned bool
	// stopped is true when this round lost a version-fenced race to
	// another worker that has already advanced this SAME operation
	// further — safe, never an error.
	stopped bool
}

// buildRound runs exactly one ReplayGenerationBatch round into op's own
// ShadowGeneration and checkpoints its progress onto the operation row —
// BUILDING's own repeated step, and CUTTING_OVER's own bounded catch-up
// step (phase distinguishes which; the replay/checkpoint mechanics are
// identical either way).
func buildRound(
	ctx context.Context, deps Deps, jobLease ports.JobLease, op ports.ProjectionRebuildOperation,
	phase ports.ProjectionRebuildPhase, owner string,
) (buildRoundResult, error) {
	if op.ShadowGeneration == nil {
		return buildRoundResult{}, fmt.Errorf("projectionrebuildworker: operation %s has no shadow generation to build into (phase %s)", op.ID, op.Phase)
	}
	// Revalidate the job lease FIRST, in its own cheap read-only check,
	// BEFORE ever touching the shadow generation's own consumer lease — a
	// job claim that is already gone must never be able to acquire or
	// renew the shadow lease at all (not even harmlessly), since doing so
	// would otherwise let an already-dead claim contest — and briefly
	// block, for up to ShadowLeaseTTL — a legitimate successor's own
	// attempt to take it over. Checked fresh on EVERY round (not just
	// once per Execute call), the same "never trusted merely because it
	// was valid earlier" discipline this package's own doc comment
	// already commits to for cutover's three fences.
	if err := validateJobLease(ctx, deps.UnitOfWork, jobLease, op.ID); err != nil {
		return buildRoundResult{}, err
	}
	now := time.Now().UTC()
	outcome, err := projection.ReplayGenerationBatch(ctx, deps.UnitOfWork, deps.Catalog, projection.ReplayGenerationBatchRequest{
		ProjectID: op.ProjectID, ProjectionName: op.ProjectionName, Generation: *op.ShadowGeneration,
		Owner: owner, TTL: deps.ShadowLeaseTTL, BatchSize: deps.BatchSize, Now: now, IDs: deps.IDs,
	})
	if err != nil {
		// Contention over the shadow generation's own lease — propagate as
		// a real, retryable error (mirrors ApplyBatch's own identical
		// documented contract for this exact error: "returned to the
		// caller as-is; a caller backs off and retries later"), never
		// silently swallowed.
		return buildRoundResult{}, err
	}
	if outcome.Poisoned {
		if err := failOperation(ctx, deps, jobLease, op, poisonErrorCode, outcome.PoisonReason); err != nil {
			return buildRoundResult{}, err
		}
		return buildRoundResult{poisoned: true}, nil
	}

	cursor := outcome.NewCursor
	updated, stopped, err := advanceOperation(ctx, deps, jobLease, op, ports.AdvanceProjectionRebuildOperationRequest{
		NextPhase: phase, NextShadowCursor: &cursor, UpdatedAt: now,
	}, false)
	if err != nil {
		return buildRoundResult{}, err
	}
	if stopped {
		return buildRoundResult{operation: updated, stopped: true}, nil
	}
	return buildRoundResult{operation: updated, caughtUp: outcome.EventsScanned < deps.BatchSize}, nil
}

func enterCuttingOver(ctx context.Context, deps Deps, jobLease ports.JobLease, op ports.ProjectionRebuildOperation) (ports.ProjectionRebuildOperation, bool, error) {
	return advanceOperation(ctx, deps, jobLease, op, ports.AdvanceProjectionRebuildOperationRequest{
		NextPhase: ports.ProjectionRebuildCuttingOver, UpdatedAt: time.Now().UTC(),
	}, false)
}

// failOperation marks op FAILED with a safe code/message and completes the
// driving job, in one fenced transaction — mirrors
// internal/app/releasesetcommit's own failTerminal precedent (transition
// then CompleteJob, same transaction).
func failOperation(ctx context.Context, deps Deps, jobLease ports.JobLease, op ports.ProjectionRebuildOperation, code, message string) error {
	_, _, err := advanceOperation(ctx, deps, jobLease, op, ports.AdvanceProjectionRebuildOperationRequest{
		NextPhase: ports.ProjectionRebuildFailed, NextErrorCode: &code, NextErrorMessage: &message, UpdatedAt: time.Now().UTC(),
	}, true)
	return err
}

// advanceOperation performs ONE fenced operation-phase transition inside
// its own transaction: reload the operation fresh (never trust a
// caller-supplied copy across a transaction boundary), stop early if it is
// already terminal (another worker finished it entirely), re-validate the
// driving job's own lease fresh, apply the version-fenced CAS via
// AdvanceOperation, and — only when completeJob is true (a terminal
// transition) — complete the job in the SAME transaction. Returns
// (updated, stopped=true, nil) — never an error — when the CAS lost to
// another worker that has already advanced this SAME operation further:
// always safe, never a failure.
func advanceOperation(
	ctx context.Context, deps Deps, jobLease ports.JobLease, op ports.ProjectionRebuildOperation,
	req ports.AdvanceProjectionRebuildOperationRequest, completeJob bool,
) (updated ports.ProjectionRebuildOperation, stopped bool, err error) {
	txErr := deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		fresh, err := tx.ProjectionRebuilds().GetOperation(ctx, op.ID)
		if err != nil {
			return err
		}
		if fresh.Phase.IsTerminal() {
			updated, stopped = fresh, true
			return nil
		}
		if err := tx.Jobs().ValidateActiveJob(ctx, jobLease, aggregateType, op.ID); err != nil {
			return err
		}
		req.ID = op.ID
		req.ExpectedVersion = fresh.Version
		result, err := tx.ProjectionRebuilds().AdvanceOperation(ctx, req)
		if err != nil {
			if errors.Is(err, ports.ErrOptimisticConflict) {
				latest, getErr := tx.ProjectionRebuilds().GetOperation(ctx, op.ID)
				if getErr != nil {
					return getErr
				}
				updated, stopped = latest, true
				return nil
			}
			return err
		}
		updated = result
		if completeJob {
			return tx.Jobs().CompleteJob(ctx, jobLease)
		}
		return nil
	})
	if txErr != nil {
		return ports.ProjectionRebuildOperation{}, false, txErr
	}
	return updated, stopped, nil
}

// cutover performs the ONE atomic swap transaction — see this file's own
// doc comment, "The cutover transaction," for the full six-step contract
// and why every fence is revalidated fresh inside this exact function
// rather than trusted from an earlier round.
func cutover(ctx context.Context, deps Deps, jobLease ports.JobLease, op ports.ProjectionRebuildOperation, owner string) error {
	if op.ShadowGeneration == nil {
		return fmt.Errorf("projectionrebuildworker: operation %s reached CUTTING_OVER with no shadow generation", op.ID)
	}
	shadowGeneration := *op.ShadowGeneration
	now := time.Now().UTC()

	return deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		// Step 1: reload fresh.
		fresh, err := tx.ProjectionRebuilds().GetOperation(ctx, op.ID)
		if err != nil {
			return err
		}
		if fresh.Phase.IsTerminal() {
			// Another worker already finished this operation entirely
			// (success or failure) — safe no-op.
			return nil
		}
		if fresh.Phase != ports.ProjectionRebuildCuttingOver {
			return fmt.Errorf("projectionrebuildworker: operation %s expected CUTTING_OVER, found %s", op.ID, fresh.Phase)
		}

		// Step 2: the job's own worker lease.
		if err := tx.Jobs().ValidateActiveJob(ctx, jobLease, aggregateType, op.ID); err != nil {
			return err
		}

		// Step 3: the cutover/shadow-generation lease, revalidated fresh —
		// see this package's own doc comment, "Design decision: the
		// cutover lease IS the projection consumer lease."
		lease, err := tx.Projections().AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: op.ProjectID, ProjectionName: op.ProjectionName, Generation: shadowGeneration,
			Owner: owner, TTL: deps.ShadowLeaseTTL, Now: now,
		})
		if err != nil {
			return err
		}
		w1 := lease.Cursor

		// Step 4: the atomic generation swap — a reader sees the OLD
		// generation or the NEW one, never a mix, since this is one
		// UPDATE of one row.
		oldGeneration, oldOK, err := tx.Projections().GetActiveGeneration(ctx, op.ProjectID, op.ProjectionName)
		if err != nil {
			return err
		}
		var expectedOldGeneration *uint64
		if oldOK {
			expectedOldGeneration = &oldGeneration
		}
		if err := tx.Projections().CutoverProjectionGeneration(ctx, ports.CutoverProjectionGenerationRequest{
			ProjectID: op.ProjectID, ProjectionName: op.ProjectionName,
			ExpectedGeneration: expectedOldGeneration, NewGeneration: shadowGeneration,
			SchemaVersion: projection.RowSchemaVersion, UpdatedAt: now,
		}); err != nil {
			return err
		}

		// Step 5: the operation's own terminal transition, fenced on the
		// SAME fresh.Version read in step 1.
		if _, err := tx.ProjectionRebuilds().AdvanceOperation(ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: op.ID, ExpectedVersion: fresh.Version, NextPhase: ports.ProjectionRebuildSucceeded,
			NextCutoverCursor: &w1, UpdatedAt: now,
		}); err != nil {
			return err
		}

		// Step 6: complete the driving job, in the SAME transaction.
		return tx.Jobs().CompleteJob(ctx, jobLease)
	})
}
