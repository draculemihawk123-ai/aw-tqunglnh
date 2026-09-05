// FinalizeExecutionAttempt is V4-05's own sole, fenced entry point from a
// RUNNING ExecutionAttempt to a terminal state (GC-INV-17/18: "Kết quả
// worker chỉ được accept khi JobLease và mọi WriteLease liên quan còn đúng
// fencing token"; "Worker mất lease không được commit success dù external
// process trả exit code 0"). It is the ONLY function that may compose
// TransitionExecutionAttempt/ValidateWriteLeaseFencing/CompleteJob together
// — ExecuteNodeHandler (execute.go), the one caller this codebase gives it,
// never chains those primitives itself; every acceptance decision goes
// through this one function so the fencing/atomicity discipline below can
// never be partially applied.
//
// Everything commits or rolls back as one ports.UnitOfWork.WithSerializedWrite
// transaction (confirmed with the user before writing this file, since the
// closest existing precedent — internal/adapters/sqlite's own spike-era
// FinalizeWorkflowRun — is a flat, sqlite-only method with no fake
// counterpart, which would have made V4-05's own required "success/
// failure/timeout/cancel/lease-loss" tests far more expensive to write than
// every other V4 task's fake-first testing discipline):
//
//  1. Validate the driving JobLease is still active and names this exact
//     ExecutionAttempt as its aggregate (fail fast, before touching the
//     Attempt's own row at all).
//  2. Load the Attempt, confirm it still belongs to req.NodeRunID.
//  3. Validate every WriteLease fencing proof the caller supplies.
//  4. CAS the Attempt RUNNING -> req.NextState (ErrOptimisticConflict if
//     stale — this method performs no idempotent early-return of its own;
//     ExecuteNodeHandler's own "not RUNNING" no-op check is what keeps a
//     duplicate job delivery from ever reaching this function twice for
//     the same real completion).
//  5. Append EXECUTION_ATTEMPT_FINALIZED in the same transaction (GC-INV-15).
//  6. When req.NextState is SUCCEEDED, route the NodeRun forward via
//     advanceRunTx — composed inside THIS SAME transaction, never a second
//     transaction opened after this one commits (confirmed with the user:
//     two separate transactions would leave a crash gap — "Attempt
//     SUCCEEDED + EXECUTE_NODE completed" could commit, the process could
//     then crash, and nothing durable would ever trigger the routing step,
//     leaving the NodeRun stuck). FAILED/TIMED_OUT/CANCELLED never route —
//     V4-06's own technical retry policy decides what happens next for
//     those.
//  7. Complete the driving EXECUTE_NODE job using the exact JobLease, last
//     — so any earlier step's failure rolls this back too, and a lease
//     that was valid at step 1 but expired by now still gets one final,
//     authoritative fencing check right at the point of acceptance.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ErrUnsupportedFinalizeState is returned when a caller asks
// FinalizeExecutionAttempt to CAS into a NextState this function does not
// recognize as a valid RUNNING->terminal target.
var ErrUnsupportedFinalizeState = errors.New("runtime: unsupported execution attempt finalize state")

// FinalizeExecutionAttemptRequest is what ExecuteNodeHandler supplies.
type FinalizeExecutionAttemptRequest struct {
	RunID           string
	NodeRunID       string
	AttemptID       string
	ExpectedVersion uint64
	// NextState must be SUCCEEDED, FAILED, TIMED_OUT or CANCELLED —
	// runtime.ExecutionAttemptState's own closed enum restricted to the
	// RUNNING->terminal transitions this method is a fenced path for.
	// V4-05's own ExecuteNodeHandler only ever calls this with SUCCEEDED
	// or FAILED (see execute.go's own doc comment for why TIMED_OUT/
	// CANCELLED are never proposed as terminal outcomes by V4-05 itself,
	// even though this type accepts them as legitimate CAS targets for a
	// later caller, e.g. V4-12B).
	NextState         runtimedomain.ExecutionAttemptState
	TerminationReason runtimedomain.TerminationReason
	// SelectedOutcome is the NodeRun outcome to route on — required when
	// NextState is SUCCEEDED, forwarded to advanceRunTx unchanged (which
	// still enforces GC-INV-11's own allow-list check; this method never
	// pre-validates it).
	SelectedOutcome  string
	SharedStatePatch map[string]json.RawMessage
	JobLease         ports.JobLease
	WriteLeases      []ports.WriteLeaseGrant
	CorrelationID    string
}

// FinalizeExecutionAttemptResult reports what one fenced finalize actually
// did.
type FinalizeExecutionAttemptResult struct {
	Attempt runtimedomain.ExecutionAttempt
	// Advanced/AdvanceResult are populated only when NextState was
	// SUCCEEDED — see this file's own package doc comment step 6.
	Advanced      bool
	AdvanceResult AdvanceRunResult
}

const (
	ExecutionAttemptFinalizedEventType     = "EXECUTION_ATTEMPT_FINALIZED"
	ExecutionAttemptFinalizedSchemaVersion = 1
)

type executionAttemptFinalizedEventPayload struct {
	RunID             string `json:"runId"`
	WorkItemID        string `json:"workItemId"`
	NodeRunID         string `json:"nodeRunId"`
	AttemptID         string `json:"attemptId"`
	NextState         string `json:"nextState"`
	TerminationReason string `json:"terminationReason"`
	JobID             string `json:"jobId,omitempty"`
}

// FinalizeExecutionAttempt performs exactly one fenced terminal transition.
// See this file's own package doc comment for the full seven-step design.
func FinalizeExecutionAttempt(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, req FinalizeExecutionAttemptRequest) (FinalizeExecutionAttemptResult, error) {
	if req.RunID == "" || req.NodeRunID == "" || req.AttemptID == "" {
		return FinalizeExecutionAttemptResult{}, errors.New("runtime: RunID, NodeRunID and AttemptID are required")
	}
	if !isFinalizableExecutionAttemptState(req.NextState) {
		return FinalizeExecutionAttemptResult{}, fmt.Errorf("%w: %q", ErrUnsupportedFinalizeState, req.NextState)
	}
	if req.TerminationReason == "" {
		return FinalizeExecutionAttemptResult{}, errors.New("runtime: TerminationReason is required")
	}
	if req.NextState == runtimedomain.ExecutionAttemptSucceeded && req.SelectedOutcome == "" {
		return FinalizeExecutionAttemptResult{}, errors.New("runtime: SelectedOutcome is required when NextState is SUCCEEDED")
	}

	var result FinalizeExecutionAttemptResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		// Step 1: job-lease fencing first — fail fast before touching the
		// Attempt's own row.
		if err := tx.Jobs().ValidateActiveJob(ctx, req.JobLease, "ExecutionAttempt", req.AttemptID); err != nil {
			return err
		}

		// Step 2: load and cross-check the Attempt.
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, req.AttemptID)
		if err != nil {
			return err
		}
		if string(attempt.NodeRunID) != req.NodeRunID {
			return fmt.Errorf("runtime: execution attempt %s belongs to node run %s, not %s", req.AttemptID, attempt.NodeRunID, req.NodeRunID)
		}

		// Step 3: validate every WriteLease fencing proof.
		for _, grant := range req.WriteLeases {
			if err := tx.Runtime().ValidateWriteLeaseFencing(ctx, req.JobLease, grant); err != nil {
				return err
			}
		}

		// Step 4: CAS the Attempt to its terminal state.
		updatedAttempt, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: req.AttemptID, ExpectedState: runtimedomain.ExecutionAttemptRunning, ExpectedVersion: req.ExpectedVersion,
			NextState: req.NextState, TerminationReason: req.TerminationReason,
		})
		if err != nil {
			return err
		}

		run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
		if err != nil {
			return err
		}

		// Step 5: domain event, same transaction (GC-INV-15).
		eventPayload, err := json.Marshal(executionAttemptFinalizedEventPayload{
			RunID: req.RunID, WorkItemID: string(run.WorkItemID), NodeRunID: req.NodeRunID, AttemptID: req.AttemptID,
			NextState: string(req.NextState), TerminationReason: string(req.TerminationReason), JobID: string(req.JobLease.JobID),
		})
		if err != nil {
			return fmt.Errorf("marshal %s event payload: %w", ExecutionAttemptFinalizedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: req.AttemptID + "-finalized", ProjectID: string(run.ProjectID),
			AggregateType: "ExecutionAttempt", AggregateID: req.AttemptID, Sequence: int64(updatedAttempt.Version),
			EventType: ExecutionAttemptFinalizedEventType, SchemaVersion: ExecutionAttemptFinalizedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: req.CorrelationID, CreatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}

		// Step 6: route the NodeRun forward, in the SAME transaction, only
		// on SUCCEEDED.
		if req.NextState == runtimedomain.ExecutionAttemptSucceeded {
			advanceResult, err := advanceRunTx(ctx, tx, ids, AdvanceRunRequest{
				RunID: req.RunID, NodeRunID: req.NodeRunID, Outcome: req.SelectedOutcome,
				SharedStatePatch: req.SharedStatePatch, CorrelationID: req.CorrelationID, JobID: string(req.JobLease.JobID),
			})
			if err != nil {
				return err
			}
			result.Advanced = true
			result.AdvanceResult = advanceResult
		}

		// Step 7: complete the driving job last.
		if err := tx.Jobs().CompleteJob(ctx, req.JobLease); err != nil {
			return err
		}

		result.Attempt = updatedAttempt
		return nil
	})
	return result, err
}

func isFinalizableExecutionAttemptState(state runtimedomain.ExecutionAttemptState) bool {
	switch state {
	case runtimedomain.ExecutionAttemptSucceeded, runtimedomain.ExecutionAttemptFailed,
		runtimedomain.ExecutionAttemptTimedOut, runtimedomain.ExecutionAttemptCancelled:
		return true
	default:
		return false
	}
}
