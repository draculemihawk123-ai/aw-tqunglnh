// Package artifactsweep is V5-14's own second half: the retention sweeper
// (docs/design/07-v5-execution-evidence.md V5-14; ADR-017;
// docs/architecture/04-go-core-spec.md §19) that actually purges owned
// Artifact content — the system's first real destructive filesystem
// operation. Built against the user's own binding contract (recorded
// verbatim in baocaov5checklist.md's "V5-14 nửa 2" section and
// agent-kit-v5-14-artifact-purge-contract.md): PR2a already landed the
// schema/port foundation this package is built on (artifact.Purged,
// ports.ArtifactStore.Delete, artifact_locator_purge_claims +
// ArtifactRepository's Claim/Release/ListByLocator methods) — this package
// is the actual sweep job/handler, PR2b.
//
// Scope, deliberately narrow (see agent-kit-v5-14-artifact-purge-contract.md
// for the full reasoning): only AttachState=Orphan rows past a grace period
// are ever purge candidates. Every current writer of an artifact-reference
// (Message, Checkpoint, Evidence) only ever points to Attached rows —
// internal/app/runtime/finalize.go promotes Orphan->Attached atomically
// with the reference write — so an Orphan row can never be referenced by
// any of them, making "no logical reference" automatically true for this
// scope without a JSON-scanning reverse-index. Purging an Attached-but-
// expired RAW_OUTPUT_TEMP row is deliberately deferred to a later pass.
//
// Mirrors internal/app/runtime.RecoveryReaperHandler's own shape almost
// exactly: a single, global, installation-global self-rescheduling
// JobClass=CONTROL job (ArtifactSweepJobKind, fenced against the
// artifact_sweep_state singleton row, migration 35 — the same generation-
// cursor pattern recovery_reaper_state uses), never scoped to one Project
// (a content-addressed Locator can be shared by Artifact rows across
// different Projects, 0027_artifacts.sql's own "content_hash is
// deliberately NOT unique" — the same reason RECOVERY_REAPER itself is
// installation-global).
//
// # Group-eligibility and the reserve/delete/finalize protocol
//
// Every Artifact row sharing a Locator (ArtifactRepository.
// ListArtifactsByLocator, real query across every Project) is classified
// together, never independently — classifyLocatorGroup is the pure
// decision core (unit-tested without I/O). A group is only ever eligible
// for real deletion when EVERY row in it is Orphan, not Hold, and past
// grace; any Attached, Held, or too-new row blocks the WHOLE group (the
// user's own contract: "chỉ xóa blob khi mọi Artifact row có cùng Locator
// đều thuộc tập đủ điều kiện xóa"). A content_hash/size disagreement within
// one Locator's own group is a corruption signal, never silently resolved —
// the group is reported CORRUPT and left untouched.
//
// Real deletion follows the same three-phase reserve -> real-I/O-outside-
// any-Tx -> finalize protocol every other destructive/crash-sensitive
// operation in this codebase already uses (mirroring
// internal/app/runtime.consumeFreshStart's identical shape for V5-13's own
// recovery executor): (1) ClaimArtifactLocatorForPurge — a durable
// deletion intent, the TOCTOU fence that also makes a concurrent
// InsertArtifact against this exact Locator fail closed; (2)
// ports.ArtifactStore.Delete, entirely outside any transaction; (3) a
// SEPARATE transaction that transitions every row in the group to Purged
// and releases the Locator claim. Because this is a singleton job (only
// one worker ever holds ARTIFACT_SWEEP's own lease at a time), a claim
// this sweep's own earlier, crashed attempt already made is never a
// foreign conflict to abort on — ClaimArtifactLocatorForPurge returning
// ErrPersistenceAlreadyExists here is treated as "resume my own prior
// claim," not "someone else got there first": Delete is already
// idempotent (a no-op if the bytes are already gone, exactly what a crash
// between phase 2 and phase 3 would leave behind), so replaying phases 2
// and 3 against an already-claimed Locator is always safe.
//
// # Dry-run
//
// artifact_sweep_state.dry_run defaults true (migration 35) — the user's
// own contract: "Dry-run và report là mặc định; thao tác thật phải
// explicit." While true, every group still runs through the identical
// classification and is recorded in the sweep's own durable manifest
// (ARTIFACT_SWEEP_COMPLETED domain event) as WOULD_PURGE/BLOCKED/CORRUPT,
// but ExecuteArtifactSweep never calls ClaimArtifactLocatorForPurge or
// ports.ArtifactStore.Delete. Flipping to a real run requires an explicit
// SetArtifactSweepDryRun(false) call — a real, populated-now port method
// (ports.ArtifactRepository) with no application-layer caller wired yet,
// the same "real method from the start, no caller needed yet" discipline
// V5-01's own ArtifactRepository already established for SetArtifactHold.
package artifactsweep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
)

// ArtifactSweepJobKind is the durable, self-rescheduling CONTROL job this
// file's own handler serves (V5-14) — joined to durable_jobs.job_class's
// own CONTROL allow-list and installation-global exemption in migration 34,
// mirroring RECOVERY_REAPER's own identical treatment (migration 25).
const ArtifactSweepJobKind = "ARTIFACT_SWEEP"

const defaultArtifactSweepJobMaxClaims = 10

// orphanGrace is this sweep's own "how old before an Orphan row is
// considered abandoned, not merely fresh" cutoff. No separate "orphan
// grace" duration is specified anywhere in this task's own design doc or
// ADR-017 (only the 7-day RAW_OUTPUT_TEMP TTL default) — reusing that same
// value here is a self-decided default, not a guess hidden from the
// record: both describe "how long before owned, unconfirmed content is
// abandoned," and ADR-017 itself frames 7 days as the platform's one
// stated default for exactly that kind of question.
const orphanGrace = 7 * 24 * time.Hour

// ArtifactSweepJobPayload is the exact JSON shape every ARTIFACT_SWEEP job
// carries — Generation names the exact artifact_sweep_state generation
// this job instance is entitled to advance from, mirroring
// RecoveryReaperJobPayload.Generation's identical fencing role.
type ArtifactSweepJobPayload struct {
	Generation    uint64 `json:"generation"`
	CorrelationID string `json:"correlationId,omitempty"`
}

// StartupArtifactSweep enqueues the first ARTIFACT_SWEEP job for the
// current artifact_sweep_state generation, if one is not already pending —
// mirrors StartupRecoveryScan exactly, including relying on EnqueueJob's
// own idempotent-insert-or-return-existing contract (IdempotencyKey
// "artifact-sweep:<generation>") to guarantee two racing callers only ever
// produce one job for the same generation.
func StartupArtifactSweep(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source) error {
	return uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		state, err := tx.Artifacts().GetArtifactSweepState(ctx)
		if err != nil {
			return err
		}
		return enqueueArtifactSweepJobTx(ctx, tx, ids, state.Generation, "", time.Time{})
	})
}

// enqueueArtifactSweepJobTx enqueues the ARTIFACT_SWEEP job for generation.
// availableAt is the earliest time a worker may claim it; the zero time means
// "immediately" (the very first job, at worker startup). Every self-rescheduled
// successor passes now+Interval instead - without that delay a completed sweep
// makes the next one claimable at once, and an idle worker re-runs the sweep
// (and appends one ARTIFACT_SWEEP_COMPLETED event each time) hundreds of times
// per second for as long as it is up.
func enqueueArtifactSweepJobTx(ctx context.Context, tx ports.Tx, ids idsource.Source, generation uint64, correlationID string, availableAt time.Time) error {
	payload, err := json.Marshal(ArtifactSweepJobPayload{Generation: generation, CorrelationID: correlationID})
	if err != nil {
		return fmt.Errorf("marshal %s job payload: %w", ArtifactSweepJobKind, err)
	}
	_, err = tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(ids.NewID()), Kind: ArtifactSweepJobKind,
		AggregateType: "ArtifactSweep", AggregateID: "singleton", Payload: payload,
		AvailableAt: availableAt,
		MaxClaims:   defaultArtifactSweepJobMaxClaims, IdempotencyKey: fmt.Sprintf("artifact-sweep:%d", generation),
	})
	if errors.Is(err, ports.ErrPersistenceAlreadyExists) {
		// Another coordinator already enqueued this exact generation's own
		// job — idempotent no-op, never a second job for the same
		// generation.
		return nil
	}
	return err
}

// locatorGroupDecision is classifyLocatorGroup's own pure, closed
// three-way outcome for one Locator's full set of sharing Artifact rows.
type locatorGroupDecision string

const (
	// locatorGroupPurge: every row sharing this Locator is Orphan, not
	// Held, and past orphanGrace — the whole group is eligible for real
	// deletion.
	locatorGroupPurge locatorGroupDecision = "PURGE"
	// locatorGroupBlocked: at least one row sharing this Locator is
	// Attached, Held, or not yet past grace — the WHOLE group is refused,
	// never a partial delete.
	locatorGroupBlocked locatorGroupDecision = "BLOCKED"
	// locatorGroupCorrupt: rows sharing this Locator disagree on their own
	// ContentHash/Size — a data-integrity signal, never silently resolved.
	locatorGroupCorrupt locatorGroupDecision = "CORRUPT"
)

// classifyLocatorGroup is the pure decision core, deliberately independent
// of any real ArtifactStore/ArtifactRepository call so it is unit-testable
// without I/O — mirroring workspacereconcile.ClassifyReconciliation's own
// identical style. rows is every Artifact row currently sharing one
// Locator (ArtifactRepository.ListArtifactsByLocator's own result,
// verbatim); olderThan is the grace cutoff (now minus orphanGrace).
func classifyLocatorGroup(rows []artifact.Artifact, olderThan time.Time) (locatorGroupDecision, string) {
	if len(rows) == 0 {
		return locatorGroupBlocked, "no artifact rows reference this locator"
	}
	contentHash, size := rows[0].ContentHash, rows[0].Size
	for _, row := range rows {
		if row.ContentHash != contentHash || row.Size != size {
			return locatorGroupCorrupt, "content_hash/size mismatch among rows sharing this locator"
		}
	}
	for _, row := range rows {
		if row.Hold {
			return locatorGroupBlocked, "held"
		}
		if row.AttachState != artifact.Orphan {
			return locatorGroupBlocked, fmt.Sprintf("attach state %s is not eligible for purge", row.AttachState)
		}
		if row.CreatedAt.After(olderThan) {
			return locatorGroupBlocked, "not yet past grace"
		}
	}
	return locatorGroupPurge, ""
}

// LocatorGroupResult is one row of a sweep run's own durable manifest —
// what ExecuteArtifactSweep decided (and, for a real run, did) about one
// distinct Locator among this run's own candidates.
type LocatorGroupResult struct {
	Locator     string   `json:"locator"`
	ArtifactIDs []string `json:"artifactIds"`
	// Decision is "PURGED" (real run, bytes actually deleted),
	// "WOULD_PURGE" (dry run, would have been eligible), "BLOCKED", or
	// "CORRUPT".
	Decision   string `json:"decision"`
	Reason     string `json:"reason,omitempty"`
	BytesFreed int64  `json:"bytesFreed,omitempty"`
}

// SweepManifest is ExecuteArtifactSweep's own durable record of one sweep
// run — the user's own contract: "Dry-run và actual run cùng tạo durable
// sweep manifest." Persisted as an ARTIFACT_SWEEP_COMPLETED domain event
// (AggregateType "ArtifactSweep", AggregateID "singleton" — the same
// installation-global identity artifact_sweep_state itself uses), never a
// bespoke new table: this codebase's own domain_events table already is
// the durable, queryable, per-aggregate-sequenced ledger every other
// coordinator-level record in this codebase uses.
type SweepManifest struct {
	Generation uint64               `json:"generation"`
	DryRun     bool                 `json:"dryRun"`
	Groups     []LocatorGroupResult `json:"groups"`
}

const (
	ArtifactSweepCompletedEventType     = "ARTIFACT_SWEEP_COMPLETED"
	ArtifactSweepCompletedSchemaVersion = 1
)

// ExecuteArtifactSweepDeps bundles ExecuteArtifactSweep's collaborators.
type ExecuteArtifactSweepDeps struct {
	UnitOfWork ports.UnitOfWork
	IDs        idsource.Source
	Clock      clock.Clock
	Store      ports.ArtifactStore
	// Interval is how long the self-rescheduled successor job waits before a
	// worker may claim it. Zero keeps the historical behaviour (claimable at
	// once), which unit tests that drive the loop by hand rely on; a real worker
	// composition must set it (see DefaultInterval).
	Interval time.Duration
}

// DefaultInterval is the pause a production worker leaves between two
// artifact sweeps. The sweep is dry-run by default and only ever considers
// Orphan rows older than orphanGrace (7 days), so an hourly cadence loses
// nothing while keeping the journal quiet.
const DefaultInterval = time.Hour

// ExecuteArtifactSweep is one full sweep pass: list Orphan candidates past
// grace, classify each distinct shared-Locator group, act (real run) or
// merely record (dry run), append the run's own durable manifest, then
// self-reschedule. See this package's own doc comment for the full
// protocol.
func ExecuteArtifactSweep(ctx context.Context, deps ExecuteArtifactSweepDeps, generation uint64, correlationID string) (SweepManifest, error) {
	if deps.UnitOfWork == nil || deps.IDs == nil || deps.Clock == nil || deps.Store == nil {
		return SweepManifest{}, errors.New("artifactsweep: ExecuteArtifactSweep requires UnitOfWork/IDs/Clock/Store")
	}

	var state ports.ArtifactSweepState
	if err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		state, err = tx.Artifacts().GetArtifactSweepState(ctx)
		return err
	}); err != nil {
		return SweepManifest{}, fmt.Errorf("load artifact sweep state: %w", err)
	}
	if state.Generation != generation {
		// A later generation already advanced past this one (this exact
		// job was redelivered after a successor already ran) — no-op,
		// mirroring RecoveryReaperHandler.Handle's identical
		// stale-generation guard.
		return SweepManifest{Generation: generation, DryRun: state.DryRun}, nil
	}

	now := deps.Clock.Now()
	olderThan := now.Add(-orphanGrace)

	var candidates []artifact.Artifact
	if err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		candidates, err = tx.Artifacts().ListOrphanedArtifacts(ctx, olderThan)
		return err
	}); err != nil {
		return SweepManifest{}, fmt.Errorf("list orphaned artifact candidates: %w", err)
	}

	manifest := SweepManifest{Generation: generation, DryRun: state.DryRun}
	seenLocators := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if seenLocators[candidate.Locator] {
			continue
		}
		seenLocators[candidate.Locator] = true

		result, err := processLocatorGroup(ctx, deps, candidate.Locator, olderThan, state.DryRun, correlationID)
		if err != nil {
			return SweepManifest{}, err
		}
		manifest.Groups = append(manifest.Groups, result)
	}

	if err := recordSweepManifestTx(ctx, deps, manifest, correlationID); err != nil {
		return SweepManifest{}, err
	}

	return manifest, deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		fresh, err := tx.Artifacts().GetArtifactSweepState(ctx)
		if err != nil {
			return err
		}
		if fresh.Generation != generation {
			return nil
		}
		advanced, err := tx.Artifacts().AdvanceArtifactSweepGeneration(ctx, ports.AdvanceArtifactSweepGenerationRequest{
			ExpectedGeneration: fresh.Generation, ExpectedVersion: fresh.Version,
		})
		if err != nil {
			return err
		}
		var availableAt time.Time
		if deps.Interval > 0 {
			availableAt = deps.Clock.Now().Add(deps.Interval)
		}
		return enqueueArtifactSweepJobTx(ctx, tx, deps.IDs, advanced.Generation, correlationID, availableAt)
	})
}

// processLocatorGroup classifies one distinct Locator's full sharing group
// and, for a real (non-dry-run) PURGE decision, performs the real
// three-phase reserve/delete/finalize.
func processLocatorGroup(
	ctx context.Context, deps ExecuteArtifactSweepDeps, locator string, olderThan time.Time, dryRun bool, correlationID string,
) (LocatorGroupResult, error) {
	var group []artifact.Artifact
	if err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		group, err = tx.Artifacts().ListArtifactsByLocator(ctx, locator)
		return err
	}); err != nil {
		return LocatorGroupResult{}, fmt.Errorf("list artifacts sharing locator %s: %w", locator, err)
	}

	artifactIDs := make([]string, len(group))
	for i, row := range group {
		artifactIDs[i] = string(row.ID)
	}

	decision, reason := classifyLocatorGroup(group, olderThan)
	switch decision {
	case locatorGroupBlocked, locatorGroupCorrupt:
		return LocatorGroupResult{Locator: locator, ArtifactIDs: artifactIDs, Decision: string(decision), Reason: reason}, nil
	}

	if dryRun {
		return LocatorGroupResult{Locator: locator, ArtifactIDs: artifactIDs, Decision: "WOULD_PURGE"}, nil
	}
	return purgeLocatorGroup(ctx, deps, locator, group, artifactIDs, correlationID)
}

// purgeLocatorGroup performs the real three-phase reserve/delete/finalize
// for one Locator's own full sharing group — see this package's own doc
// comment for why ClaimArtifactLocatorForPurge returning
// ErrPersistenceAlreadyExists here means "resume my own prior claim," not
// "abort."
func purgeLocatorGroup(
	ctx context.Context, deps ExecuteArtifactSweepDeps, locator string, group []artifact.Artifact, artifactIDs []string, correlationID string,
) (LocatorGroupResult, error) {
	claimErr := deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Artifacts().ClaimArtifactLocatorForPurge(ctx, locator, "artifact-sweep:singleton", deps.Clock.Now())
	})
	if claimErr != nil && !errors.Is(claimErr, ports.ErrPersistenceAlreadyExists) {
		return LocatorGroupResult{}, fmt.Errorf("claim locator %s for purge: %w", locator, claimErr)
	}

	ref := ports.ArtifactRef{Locator: locator, SHA256: group[0].ContentHash, Size: group[0].Size}
	if err := deps.Store.Delete(ctx, ref); err != nil {
		return LocatorGroupResult{}, fmt.Errorf("delete artifact content at locator %s: %w", locator, err)
	}

	if err := deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		for _, row := range group {
			if row.AttachState == artifact.Purged {
				continue
			}
			if _, err := tx.Artifacts().TransitionArtifactAttachState(ctx, ports.TransitionArtifactAttachStateRequest{
				ArtifactID: string(row.ID), ExpectedState: artifact.Orphan, ExpectedVersion: row.Version, NextState: artifact.Purged,
			}); err != nil {
				return fmt.Errorf("finalize purged artifact %s: %w", row.ID, err)
			}
		}
		return tx.Artifacts().ReleaseArtifactLocatorClaim(ctx, locator)
	}); err != nil {
		return LocatorGroupResult{}, err
	}

	return LocatorGroupResult{Locator: locator, ArtifactIDs: artifactIDs, Decision: "PURGED", BytesFreed: group[0].Size}, nil
}

// recordSweepManifestTx appends this sweep run's own durable manifest as an
// ARTIFACT_SWEEP_COMPLETED domain event: AggregateType "ArtifactSweep",
// AggregateID "singleton" — reused across every run, so Sequence must be a
// real, collision-free running counter (domain_events' own
// UNIQUE(aggregate_type, aggregate_id, sequence)), never a hardcoded 1.
// Rather than adding a new "peek next sequence" port method, this reuses
// manifest.Generation itself: artifact_sweep_state.Generation is already a
// real, CAS-fenced, exactly-one-per-run counter for this identical
// singleton aggregate, and exactly one manifest event is ever recorded per
// run — so Generation+1 (Generation starts at 0; domain_events' own CHECK
// requires sequence > 0) is already a correct, unique Sequence with no
// separate allocation needed.
func recordSweepManifestTx(ctx context.Context, deps ExecuteArtifactSweepDeps, manifest SweepManifest, correlationID string) error {
	payload, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal artifact sweep manifest: %w", err)
	}
	return deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Events().Append(ctx, ports.DomainEvent{
			ID: deps.IDs.NewID(), AggregateType: "ArtifactSweep", AggregateID: "singleton",
			Sequence:  int64(manifest.Generation) + 1,
			EventType: ArtifactSweepCompletedEventType, SchemaVersion: ArtifactSweepCompletedSchemaVersion,
			PayloadJSON: string(payload), CorrelationID: correlationID, CreatedAt: deps.Clock.Now(),
		})
	})
}

// Handler implements workerpool.Handler for ArtifactSweepJobKind, mirroring
// RecoveryReaperHandler's own shape.
type Handler struct {
	deps ExecuteArtifactSweepDeps
}

// HandlerOption customizes NewHandler.
type HandlerOption func(*Handler)

// WithInterval sets how long the next sweep waits after this one (see
// ExecuteArtifactSweepDeps.Interval).
func WithInterval(interval time.Duration) HandlerOption {
	return func(h *Handler) { h.deps.Interval = interval }
}

// NewHandler returns a ready-to-register Handler.
func NewHandler(uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock, store ports.ArtifactStore, options ...HandlerOption) *Handler {
	h := &Handler{deps: ExecuteArtifactSweepDeps{UnitOfWork: uow, IDs: ids, Clock: clk, Store: store}}
	for _, option := range options {
		option(h)
	}
	return h
}

var _ workerpool.Handler = (*Handler)(nil)

// Handle implements workerpool.Handler: unmarshal the job payload and
// delegate to ExecuteArtifactSweep.
func (h *Handler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload ArtifactSweepJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("artifactsweep: unmarshal job %s payload: %w", job.ID, err)
	}
	_, err := ExecuteArtifactSweep(ctx, h.deps, payload.Generation, payload.CorrelationID)
	if err != nil {
		return fmt.Errorf("artifactsweep: execute sweep for job %s: %w", job.ID, err)
	}
	return nil
}
