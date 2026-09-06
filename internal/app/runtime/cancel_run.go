// CancelRun (V4-12B, docs/design/06-v4-runtime-engine.md, ADR-020) is a
// quiesce PROTOCOL with a durable, fenced coordinator — never a bare CAS
// straight to CANCELLED. Confirmed with the user before writing this file:
// CancelRun's own transaction (1) records the one durable
// RunCancellationIntent a Run ever has (idempotent by RunID — a duplicate
// call is a harmless no-op, never a second intent), (2) CASes the Run to
// CANCELLING and bumps CancelEpoch NULL->1 in that SAME CAS, (3) fences
// every non-terminal RUN_WORK job of this Run in that SAME transaction
// (ports.JobsRepository.FenceAndCancelRunJobs — an AVAILABLE one is CASed
// straight to CANCELLED, a LEASED one stays LEASED but fenced, so the
// worker already holding it backs off at its own two re-check checkpoints,
// execute.go), and (4) enqueues exactly one CANCEL_RUN_COORDINATOR job
// (CONTROL class, never itself fenced) that does the rest of the quiesce
// work asynchronously (this file's own CancelRunCoordinatorHandler,
// cancel_run_coordinator.go): CASing every still-ACTIVE WAIT registration,
// still-PENDING APPROVAL request, still-QUEUED ExecutionAttempt, and every
// other not-yet-started NodeRun of this Run to CANCELLED.
//
// "scheduler ngừng tạo activation/technical retry/rework mới ngay khi
// intent commit" is enforced NOT here, but at every "create new work" call
// site in this package (advanceRunTx, decideRetryOrExhaustion) checking
// the Run's own CANCELLING/CANCELLED state before routing — CancelRun
// itself never has to touch those call sites; it only has to make its own
// CAS to CANCELLING visible before any of them next runs, which SQLite's
// own full write-transaction serialization already guarantees.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// CancelRunCoordinatorJobKind is the CONTROL job CancelRun itself enqueues
// (V4-12B) — never fenced by cancel_epoch (see ports.ClassifyJobKind's own
// closed allow-list), since it is exactly the job that does the fencing
// and must keep running while every RUN_WORK job of the same Run is being
// fenced off.
const CancelRunCoordinatorJobKind = "CANCEL_RUN_COORDINATOR"

const defaultCancelRunCoordinatorJobMaxClaims = 5

// CancelRunCoordinatorJobPayload is the exact JSON shape CancelRun
// marshals for a CancelRunCoordinatorJobKind job.
type CancelRunCoordinatorJobPayload struct {
	RunID         string `json:"runId"`
	CorrelationID string `json:"correlationId,omitempty"`
}

// CancelRunRequest is what a caller supplies to CancelRun.
type CancelRunRequest struct {
	RunID         string
	Actor         string
	Reason        string
	CorrelationID string
}

// CancelRunResult is what CancelRun returns.
type CancelRunResult struct {
	RunID            string
	AlreadyRequested bool
	State            string
	// CoordinatorJobID is the CANCEL_RUN_COORDINATOR job's own ID — empty
	// when AlreadyRequested (a duplicate call never re-enqueues one).
	CoordinatorJobID string
}

// ErrRunAlreadyTerminal is returned when CancelRun targets a Run that has
// already reached a genuinely terminal state (SUCCEEDED or FAILED) —
// ADR-020's own no-op rule: "chỉ PASS và FAIL làm Run terminal... biến
// cancel thành no-op idempotent". CANCELLING/CANCELLED/VERIFYING/BLOCKED/
// CREATED are all still accepted (VERIFYING and BLOCKED are not yet
// genuinely done, so cancel must still be able to intervene; CANCELLING/
// CANCELLED are handled as the identical idempotent-duplicate case a
// pre-existing RunCancellationIntent already covers).
var ErrRunAlreadyTerminal = errors.New("runtime: workflow run is already terminal, cancel is a no-op")

// RunCancellationRequestedEventType/RunCancellationRequestedSchemaVersion
// identify RUN_CANCELLATION_REQUESTED's own registered (EventType,
// SchemaVersion) pair (V4-12B, event_schema.go) — registered from the
// same changeset that produces this event, not deferred.
const (
	RunCancellationRequestedEventType     = "RUN_CANCELLATION_REQUESTED"
	RunCancellationRequestedSchemaVersion = 1
)

// runCancellationRequestedEventPayload is RUN_CANCELLATION_REQUESTED's own
// JSON shape (V4-12B): the durable audit record of who asked for this Run
// to cancel and why, distinct from RUN_CANCELLED (completion.go, appended
// only once the Run has actually finished quiescing).
type runCancellationRequestedEventPayload struct {
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
	Actor      string `json:"actor"`
	Reason     string `json:"reason"`
}

// CancelRun implements ADR-020's own cancellation protocol entry point.
// See this file's own package doc comment for the full transaction shape.
// This top-level function only opens the transaction and delegates to
// cancelRunTx below — extracted (V4-12C) so CancelWorkItem
// (cancel_work_item.go) can drive the identical protocol for each of a
// WorkItem's own active Runs, composed inside its OWN already-open
// transaction: a ports.UnitOfWork.WithSerializedWrite call must never open a
// nested one, so CancelWorkItem cannot call this public function directly.
func CancelRun(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, req CancelRunRequest) (CancelRunResult, error) {
	runID := strings.TrimSpace(req.RunID)
	actor := strings.TrimSpace(req.Actor)
	reason := strings.TrimSpace(req.Reason)
	if runID == "" {
		return CancelRunResult{}, errors.New("runtime: RunID is required")
	}
	if actor == "" {
		return CancelRunResult{}, errors.New("runtime: Actor is required")
	}
	if reason == "" {
		return CancelRunResult{}, errors.New("runtime: Reason is required")
	}

	var result CancelRunResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		result, err = cancelRunTx(ctx, tx, ids, runID, actor, reason, req.CorrelationID)
		return err
	})
	return result, err
}

// cancelRunTx is CancelRun's own tx-scoped core (V4-12C extraction): the
// exact idempotent-duplicate check, already-terminal rejection, intent
// record, CAS to CANCELLING (bumping CancelEpoch in the same CAS), job
// fence and CANCEL_RUN_COORDINATOR enqueue CancelRun's own transaction
// always performed — unchanged in every respect from before this
// extraction. See CancelRun's own doc comment for why this needs to exist
// as a separate, tx-scoped function at all.
func cancelRunTx(ctx context.Context, tx ports.Tx, ids idsource.Source, runID, actor, reason, correlationID string) (CancelRunResult, error) {
	run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		return CancelRunResult{}, err
	}

	if _, err := tx.Runtime().GetRunCancellationIntent(ctx, runID); err == nil {
		// A second CancelRun call for the same run — ADR-020's own
		// "idempotent theo run": return the already-committed state,
		// never re-fence, never re-enqueue a second coordinator job.
		return CancelRunResult{RunID: runID, AlreadyRequested: true, State: string(run.State)}, nil
	} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
		return CancelRunResult{}, err
	}

	if run.State == runtimedomain.WorkflowRunSucceeded || run.State == runtimedomain.WorkflowRunFailed {
		return CancelRunResult{}, fmt.Errorf("%w: workflow run %s is %s", ErrRunAlreadyTerminal, runID, run.State)
	}

	intentID := ids.NewID()
	intent, err := runtimedomain.NewRunCancellationIntent(
		runtimedomain.RunCancellationIntentID(intentID), run.ProjectID, run.ID, actor, reason, time.Now().UTC(),
	)
	if err != nil {
		return CancelRunResult{}, err
	}
	if _, err := tx.Runtime().RecordRunCancellationIntent(ctx, intent); err != nil {
		return CancelRunResult{}, err
	}

	nextEpoch := uint64(1)
	updated, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
		RunID: runID, ExpectedState: run.State, ExpectedVersion: run.Version,
		NextState: runtimedomain.WorkflowRunCancelling, NextCancelEpoch: &nextEpoch,
	})
	if err != nil {
		return CancelRunResult{}, err
	}

	if _, err := tx.Jobs().FenceAndCancelRunJobs(ctx, runID); err != nil {
		return CancelRunResult{}, err
	}

	jobPayload, err := json.Marshal(CancelRunCoordinatorJobPayload{RunID: runID, CorrelationID: correlationID})
	if err != nil {
		return CancelRunResult{}, fmt.Errorf("marshal %s job payload: %w", CancelRunCoordinatorJobKind, err)
	}
	job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(ids.NewID()), ProjectID: run.ProjectID, Kind: CancelRunCoordinatorJobKind,
		AggregateType: "WorkflowRun", AggregateID: runID, Payload: jobPayload,
		MaxClaims: defaultCancelRunCoordinatorJobMaxClaims, IdempotencyKey: "cancel-run-coordinator:" + runID,
	})
	if err != nil {
		return CancelRunResult{}, err
	}

	eventPayload, err := json.Marshal(runCancellationRequestedEventPayload{
		RunID: runID, WorkItemID: string(run.WorkItemID), Actor: actor, Reason: reason,
	})
	if err != nil {
		return CancelRunResult{}, fmt.Errorf("marshal %s event payload: %w", RunCancellationRequestedEventType, err)
	}
	if err := tx.Events().Append(ctx, ports.DomainEvent{
		ID: runID + "-cancellation-requested", ProjectID: string(run.ProjectID),
		AggregateType: "WorkflowRun", AggregateID: runID, Sequence: int64(updated.Version),
		EventType: RunCancellationRequestedEventType, SchemaVersion: RunCancellationRequestedSchemaVersion,
		PayloadJSON: string(eventPayload), CorrelationID: correlationID, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return CancelRunResult{}, err
	}

	return CancelRunResult{
		RunID: runID, State: string(runtimedomain.WorkflowRunCancelling), CoordinatorJobID: string(job.ID),
	}, nil
}
