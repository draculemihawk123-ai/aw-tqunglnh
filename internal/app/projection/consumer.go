package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// RowSchemaVersion is WorkItemCardRow's own current schema version — bumped
// whenever its own field shape changes (a rebuild, V6-09A, targets a NEW
// generation whenever this changes; the live consumer never migrates rows
// of an already-active generation in place). Distinct from a Reducer's own
// HandlerVersion (Catalog, catalog.go): HandlerVersion tracks a single
// event's own apply LOGIC changing; RowSchemaVersion tracks the RESULT
// shape every Reducer collectively produces changing.
const RowSchemaVersion = 1

// errNoProgress is ApplyBatch's own internal sentinel for "the scan found
// nothing past the current cursor" — not a real failure, just nothing to
// commit this round (the already-committed lease-renewal from step 0 is
// the round's only real effect).
var errNoProgress = errors.New("projection: no journal progress past current cursor")

// errPoison is ApplyBatch's own internal sentinel: returning it from the
// apply transaction's closure rolls back every row/cursor change attempted
// in that transaction (V6-08A's own "Failure rolls back rows/cursor")
// while poisonDetail (captured by the closure, read after
// WithSerializedWrite returns) carries what to record in the SEPARATE
// poison transaction that follows.
var errPoison = errors.New("projection: poison event")

// ApplyBatchRequest configures one ApplyBatch round for a single named
// projection's own single active generation.
type ApplyBatchRequest struct {
	ProjectID      string
	ProjectionName string
	// Owner identifies THIS consumer instance for lease ownership — stable
	// across a process's own lifetime (e.g. a process ID or a random
	// per-start token), never per-round.
	Owner string
	// TTL is how long an acquired/renewed lease stays valid — a caller
	// should re-invoke ApplyBatch well before TTL elapses (the
	// self-rescheduling CONTROL job's own interval, mirroring
	// internal/app/artifactsweep's own reschedule cadence, stays shorter
	// than TTL) so a healthy consumer's lease never actually expires under
	// normal operation; TTL only ever matters for detecting a genuinely
	// dead/stuck consumer.
	TTL time.Duration
	// BatchSize bounds how many journal rows one round scans — also the
	// STALE-detection signal: a batch that came back completely full
	// means strictly more work is already waiting, so this round's own
	// checkpoint advance is marked STALE instead of LIVE (self-correcting:
	// the very next round that returns a non-full batch flips back to
	// LIVE). See this file's own doc comment on why no separate "journal
	// tip" query is needed for this.
	BatchSize int
	Now       time.Time
	IDs       idsource.Source
}

// ApplyBatchOutcome summarizes what one ApplyBatch round actually did —
// for the caller's own logging/scheduling and for tests.
type ApplyBatchOutcome struct {
	Generation    uint64
	LeaseAcquired bool
	EventsScanned int
	RowsApplied   int
	// NewCursor is the checkpoint's own cursor AFTER this round — unchanged
	// from before the round when EventsScanned is 0 or when Poisoned.
	NewCursor uint64
	Poisoned  bool
	// PoisonReason is populated exactly when Poisoned is true.
	PoisonReason string
}

// ApplyBatch runs exactly one atomic scan-and-apply round for
// (req.ProjectID, req.ProjectionName)'s own active generation
// (docs/design/08-v6-api-projections.md V6-08A). Three possible
// transaction shapes, matching V6-08A's own spec text exactly:
//  1. Lease acquire/renew (its own transaction, always commits if it
//     succeeds at all — a legitimate "still alive" heartbeat even when
//     nothing else in the round makes progress). ErrOptimisticConflict
//     here (another consumer holds a live lease — the "two consumers"
//     case) is returned to the caller as-is; a caller backs off and
//     retries later.
//  2. Scan-and-apply (one serialized transaction: re-scans from the
//     lease's own Cursor, APPLY/IGNOREs every in-project event, upserts
//     rows, then CASes the checkpoint forward fenced on BOTH the lease's
//     own Cursor and FenceToken — "verifies active generation/fence/
//     cursor... updates rows then CASes checkpoint"). A poison event
//     anywhere in the batch rolls back the ENTIRE transaction (no partial
//     row/cursor progress ever commits alongside a poison — V6-08A's own
//     "Failure rolls back rows/cursor").
//  3. Poison record (only when step 2 hit a poison event): its own
//     separate transaction records the poison row and CASes the
//     checkpoint to DEGRADED at the SAME last-good Cursor step 1 already
//     committed (fenced on the SAME FenceToken step 1 acquired) —
//     "separate tx records poison and DEGRADED/STALE at last-good
//     cursor."
//
// Because rows and cursor always commit together in step 2's own single
// transaction, "cursor never exceeds applied data" (V6-08A's own "Hoàn
// thành khi") holds by construction — there is no possible partial state
// where rows were applied but the cursor was not advanced, or vice versa,
// regardless of when a crash happens.
func ApplyBatch(ctx context.Context, uow ports.UnitOfWork, catalog *Catalog, req ApplyBatchRequest) (ApplyBatchOutcome, error) {
	var outcome ApplyBatchOutcome
	var generation uint64
	var lease ports.ConsumerLease

	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		gen, ok, err := tx.Projections().GetActiveGeneration(ctx, req.ProjectID, req.ProjectionName)
		if err != nil {
			return err
		}
		if !ok {
			gen = 1
			if err := tx.Projections().EnsureGeneration(ctx, req.ProjectID, req.ProjectionName, gen, RowSchemaVersion, req.Now); err != nil {
				return err
			}
		}
		generation = gen
		lease, err = tx.Projections().AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: req.ProjectID, ProjectionName: req.ProjectionName, Generation: generation,
			Owner: req.Owner, TTL: req.TTL, Now: req.Now,
		})
		return err
	})
	if err != nil {
		return ApplyBatchOutcome{}, err
	}
	outcome.Generation = generation
	outcome.LeaseAcquired = true
	outcome.NewCursor = lease.Cursor

	// A DEGRADED generation stays frozen until a rebuild (V6-09A) cuts
	// over to a fresh one — the live consumer's own job here is simply to
	// never silently resume past the poison, only to keep its own lease
	// alive so a human/operator tool can still see who (if anyone) is
	// watching this generation.
	if lease.Status == ports.ProjectionDegraded {
		return outcome, nil
	}

	var poisonEvent ports.JournalEvent
	var poisonReason string
	var newCursor uint64
	var eventsScanned, rowsApplied int

	applyErr := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		// eventsScanned/rowsApplied are local to this closure, deliberately
		// NOT written into outcome until after WithSerializedWrite confirms
		// this transaction actually committed — a plain Go variable
		// mutation inside this closure is NOT rolled back just because the
		// SQL transaction is; writing straight into outcome here would
		// report rows as "applied" even for a batch that poisoned and
		// rolled back every one of them.
		events, err := tx.Events().ScanJournal(ctx, lease.Cursor, req.BatchSize)
		if err != nil {
			return err
		}
		eventsScanned = len(events)
		if len(events) == 0 {
			return errNoProgress
		}

		newCursor = lease.Cursor
		for _, event := range events {
			newCursor = event.JournalPosition
			if event.ProjectID != req.ProjectID {
				// Foreign project (or an installation-scoped event, empty
				// ProjectID) — V6-08A's own "Foreign-project positions
				// advance scan cursor without row changes."
				continue
			}

			key := eventschema.EventKey{EventType: event.EventType, SchemaVersion: event.SchemaVersion}
			classification, known := catalog.Classify(key)
			if !known {
				// Real production safety net: TestCatalog_ClassifiesEveryRegisteredEventKey
				// already fails CI closed on this at build time, so reaching
				// this at runtime means a schema/catalog version skew — V6-08's
				// own "relevant unknown schema" gap.
				poisonEvent, poisonReason = event, fmt.Sprintf("unregistered/unclassified event %s v%d", event.EventType, event.SchemaVersion)
				return errPoison
			}
			if classification.Outcome == Ignore {
				continue
			}

			entityKey, resolveErr := resolveEntityKey(ctx, tx, req.ProjectID, req.ProjectionName, generation, classification, event.PayloadJSON)
			if resolveErr != nil {
				poisonEvent, poisonReason = event, resolveErr.Error()
				return errPoison
			}

			prior, err := loadRow(ctx, tx, req.ProjectID, req.ProjectionName, generation, entityKey)
			if err != nil {
				return err
			}

			newRow, reduceErr := classification.Reducer(prior, event.PayloadJSON)
			if reduceErr != nil {
				poisonEvent, poisonReason = event, "deterministic reducer failure: "+reduceErr.Error()
				return errPoison
			}
			payloadJSON, hashErr := newRow.CanonicalJSON()
			if hashErr != nil {
				return fmt.Errorf("canonicalize projection row: %w", hashErr)
			}
			if err := tx.Projections().UpsertProjectionRow(ctx, ports.ProjectionRow{
				ProjectID: req.ProjectID, ProjectionName: req.ProjectionName, Generation: generation,
				EntityKey: entityKey, PayloadJSON: string(payloadJSON),
				LastAppliedJournalPosition: event.JournalPosition, UpdatedAt: req.Now,
			}); err != nil {
				return err
			}
			rowsApplied++
		}

		newStatus := ports.ProjectionLive
		if len(events) == req.BatchSize {
			// The batch came back completely full: strictly more work is
			// already waiting past newCursor. See ApplyBatchRequest's own
			// BatchSize doc comment for why this is STALE-detection's own
			// signal, not a separate "journal tip" query.
			newStatus = ports.ProjectionStale
		}
		expectedCursor, expectedFence := lease.Cursor, lease.FenceToken
		return tx.Projections().UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: req.ProjectID, ProjectionName: req.ProjectionName, Generation: generation,
			ExpectedCursor: &expectedCursor, ExpectedFenceToken: &expectedFence,
			NewCursor: newCursor, NewStatus: newStatus, UpdatedAt: req.Now,
		})
	})

	// EventsScanned is purely informational (what the scan step read, not
	// what committed) so it is safe to report regardless of how this round
	// ended. RowsApplied stays 0 (outcome's own zero value) unless the
	// apply transaction actually committed (the applyErr == nil case
	// below) — a poisoned or otherwise-failed round applied nothing
	// durable, however many rows its own now-rolled-back closure iterated
	// over.
	outcome.EventsScanned = eventsScanned

	switch {
	case applyErr == nil:
		outcome.RowsApplied = rowsApplied
		outcome.NewCursor = newCursor
		return outcome, nil
	case errors.Is(applyErr, errNoProgress):
		return outcome, nil
	case errors.Is(applyErr, errPoison):
		expectedCursor, expectedFence := lease.Cursor, lease.FenceToken
		poisonErr := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			if err := tx.Projections().RecordProjectionPoison(ctx, ports.ProjectionPoisonRecord{
				ID: req.IDs.NewID(), ProjectID: req.ProjectID, ProjectionName: req.ProjectionName, Generation: generation,
				JournalPosition: poisonEvent.JournalPosition, EventType: poisonEvent.EventType,
				SchemaVersion: poisonEvent.SchemaVersion, Reason: poisonReason, RecordedAt: req.Now,
			}); err != nil {
				return err
			}
			return tx.Projections().UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
				ProjectID: req.ProjectID, ProjectionName: req.ProjectionName, Generation: generation,
				ExpectedCursor: &expectedCursor, ExpectedFenceToken: &expectedFence,
				NewCursor: lease.Cursor, NewStatus: ports.ProjectionDegraded, UpdatedAt: req.Now,
			})
		})
		if poisonErr != nil {
			return outcome, fmt.Errorf("record poison for %s v%d at position %d: %w", poisonEvent.EventType, poisonEvent.SchemaVersion, poisonEvent.JournalPosition, poisonErr)
		}
		outcome.Poisoned = true
		outcome.PoisonReason = poisonReason
		return outcome, nil
	default:
		return outcome, applyErr
	}
}

// resolveEntityKey resolves classification's own target WorkItemCardRow
// EntityKey for one event's payload — the direct EntityKeyOf path, or
// (when that returns ok=false) FallbackMatch's own predicate applied over
// every row in the current generation. Zero or more-than-one match is
// itself a poison condition (missing referenced authority / an internal
// consistency violation this projection's own invariants should never
// allow), returned as an error for the caller to record.
func resolveEntityKey(ctx context.Context, tx ports.Tx, projectID, projectionName string, generation uint64, classification Classification, payloadJSON string) (string, error) {
	if classification.EntityKeyOf != nil {
		key, ok, err := classification.EntityKeyOf(payloadJSON)
		if err != nil {
			return "", fmt.Errorf("corrupt payload: %w", err)
		}
		if ok {
			return key, nil
		}
	}
	if classification.FallbackMatch == nil {
		return "", errors.New("no EntityKeyOf match and no FallbackMatch registered — this is a Catalog registration bug, not a runtime data problem")
	}
	predicate, err := classification.FallbackMatch(payloadJSON)
	if err != nil {
		return "", fmt.Errorf("corrupt payload: %w", err)
	}
	rows, err := tx.Projections().ListProjectionRows(ctx, projectID, projectionName, generation)
	if err != nil {
		return "", err
	}
	var matched []string
	for _, row := range rows {
		var decoded WorkItemCardRow
		if err := json.Unmarshal([]byte(row.PayloadJSON), &decoded); err != nil {
			return "", fmt.Errorf("decode existing row %s while resolving fallback match: %w", row.EntityKey, err)
		}
		if predicate(decoded) {
			matched = append(matched, row.EntityKey)
		}
	}
	switch len(matched) {
	case 0:
		return "", errors.New("missing referenced authority: no row matched this event's own FallbackMatch predicate")
	case 1:
		return matched[0], nil
	default:
		return "", fmt.Errorf("aggregate-sequence violation: %d rows matched this event's own FallbackMatch predicate, want exactly 1", len(matched))
	}
}

// loadRow returns entityKey's own current row decoded as a WorkItemCardRow
// — the zero WorkItemCardRow{} (Exists() == false) if no row exists yet
// for that key, never an error for that specific case (a legitimate,
// expected state for the very first event ever applied to a new entity).
func loadRow(ctx context.Context, tx ports.Tx, projectID, projectionName string, generation uint64, entityKey string) (WorkItemCardRow, error) {
	existing, err := tx.Projections().GetProjectionRow(ctx, projectID, projectionName, generation, entityKey)
	if errors.Is(err, ports.ErrPersistenceNotFound) {
		return WorkItemCardRow{}, nil
	}
	if err != nil {
		return WorkItemCardRow{}, err
	}
	var row WorkItemCardRow
	if err := json.Unmarshal([]byte(existing.PayloadJSON), &row); err != nil {
		return WorkItemCardRow{}, fmt.Errorf("decode existing row %s: %w", entityKey, err)
	}
	return row, nil
}
