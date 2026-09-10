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
// transaction:
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
//  6. Branch on req.NextState:
//     - SUCCEEDED: route the NodeRun forward via advanceRunTx — composed
//     inside THIS SAME transaction, never a second transaction opened
//     after this one commits (two transactions would leave a crash gap:
//     "Attempt SUCCEEDED + job completed" could commit, the process
//     could then crash, and nothing durable would ever trigger routing,
//     leaving the NodeRun stuck).
//     - FAILED / TIMED_OUT (V4-06, Technical retry policy): decide retry
//     vs exhaustion from req.FailureCode and the pinned ATTEMPT policy
//     (re-resolved fresh from the SAME immutable PolicyVersion V4-04's
//     own scheduling already pinned — re-loading an immutable, content-
//     hashed version can never drift, so this is not a second, looser
//     authority). Retryable AND budget remains: create the next
//     ExecutionAttempt (AttemptNumber+1) and enqueue its own
//     EXECUTE_NODE job with AvailableAt = now + BackoffSeconds — all in
//     this SAME transaction. Non-retryable, OR retryable but budget
//     exhausted: CAS the NodeRun RUNNING -> FAILED and append
//     NODE_RUN_FAILED — never a fabricated blocker record (blockers
//     does not exist as a table yet, V4-12A/C's own scope) and never
//     NodeRun.BLOCKED (that state means something else entirely —
//     ADR-011's own scope-amendment flow). WorkflowRun is deliberately
//     NEVER auto-failed here — Run-level failure aggregation is V4-12's
//     own scope.
//     - CANCELLED: no further action here (forward-compatible target for
//     a future caller, e.g. V4-12B — V4-06 never produces this state).
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

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// ErrUnsupportedFinalizeState is returned when a caller asks
// FinalizeExecutionAttempt to CAS into a NextState this function does not
// recognize as a valid RUNNING->terminal target.
var ErrUnsupportedFinalizeState = errors.New("runtime: unsupported execution attempt finalize state")

// ErrFailureCodeRequired is returned when NextState is FAILED or TIMED_OUT
// and the caller did not supply a FailureCode — GC-INV-27's own "Attempt
// terminal luôn có TerminationReason typed", extended by V4-06 to also
// require the exact errorcode.Code a retry decision keys off, never
// inferred later by parsing anything.
var ErrFailureCodeRequired = errors.New("runtime: FailureCode is required when NextState is FAILED or TIMED_OUT")

// FinalizeExecutionAttemptRequest is what ExecuteNodeHandler supplies.
type FinalizeExecutionAttemptRequest struct {
	RunID           string
	NodeRunID       string
	AttemptID       string
	ExpectedVersion uint64
	// NextState must be SUCCEEDED, FAILED, TIMED_OUT or CANCELLED —
	// runtime.ExecutionAttemptState's own closed enum restricted to the
	// RUNNING->terminal transitions this method is a fenced path for.
	// V4-05/V4-06's own ExecuteNodeHandler only ever calls this with
	// SUCCEEDED, FAILED or TIMED_OUT (see execute.go's own doc comment for
	// why CANCELLED is never proposed as a terminal outcome by this task
	// itself, even though this type accepts it as a legitimate CAS target
	// for a later caller, e.g. V4-12B).
	NextState         runtimedomain.ExecutionAttemptState
	TerminationReason runtimedomain.TerminationReason
	// FailureCode is required exactly when NextState is FAILED or
	// TIMED_OUT (ErrFailureCodeRequired otherwise) — see
	// runtime.ExecutionAttempt.FailureCode's own doc comment.
	FailureCode errorcode.Code
	// SelectedOutcome is the NodeRun outcome to route on — required when
	// NextState is SUCCEEDED, forwarded to advanceRunTx unchanged (which
	// still enforces GC-INV-11's own allow-list check; this method never
	// pre-validates it).
	SelectedOutcome  string
	SharedStatePatch map[string]json.RawMessage
	// RequestedScopeExpansion is populated now (V4-12A): required exactly
	// when NextState is ExecutionAttemptBlocked and TerminationReason is
	// TerminationReasonScopeExpansionRequired, forbidden otherwise — the
	// same strict per-NextState mutual exclusivity SelectedOutcome/
	// FailureCode already enforce below, confirmed with the user before
	// writing this file: a malformed shape is ErrOutcomeShapeInvalid,
	// never something that creates a ScopeExpansionOrigin/durable BLOCKED
	// state.
	RequestedScopeExpansion *runtimedomain.ScopeExpansionProposal
	JobLease                ports.JobLease
	WriteLeases             []ports.WriteLeaseGrant
	CorrelationID           string
	// Evidence is populated now (V5-08B) exactly when the caller is the
	// NodeExecutor->AgentExecutor bridge proposing a real AGENT
	// completion — required when NextState is SUCCEEDED and the caller is
	// that bridge, nil for every other pre-V5-09 caller (the existing fake
	// NodeExecutor): this method's own evidence-fencing checks below run
	// ONLY when non-nil, so every such caller is entirely unaffected. Since
	// the V5-09/V5-10 acceptance-gap remediation (2026-09-10), COMMAND/
	// MACHINE_GATE populate it on SUCCEEDED too, and MACHINE_GATE also
	// populates it on FAILED/TIMED_OUT for its own non-PASS verdict (see
	// ports.NodeExecutionResult.Evidence's own doc comment for exactly
	// which callers populate it and when). See
	// ports.AttemptFinalizationEvidence's own doc comment for the full
	// contract this method re-validates.
	Evidence *ports.AttemptFinalizationEvidence
}

// FinalizeExecutionAttemptResult reports what one fenced finalize actually
// did.
type FinalizeExecutionAttemptResult struct {
	Attempt runtimedomain.ExecutionAttempt
	// Advanced/AdvanceResult are populated only when NextState was
	// SUCCEEDED — see this file's own package doc comment step 6.
	Advanced      bool
	AdvanceResult AdvanceRunResult
	// Retried/NextAttemptID are populated only when a FAILED/TIMED_OUT
	// Attempt was retryable and budget remained (V4-06).
	Retried       bool
	NextAttemptID string
	// NodeRunFailed is populated only when a FAILED/TIMED_OUT Attempt was
	// non-retryable, or retryable but exhausted its budget (V4-06).
	NodeRunFailed bool
	// ScopeExpansionRequested/ScopeExpansionRequestID are populated only
	// when NextState was BLOCKED (V4-12A): a ScopeExpansionOrigin was
	// created linking this Attempt to the RESERVED RequestID a
	// REQUEST_SCOPE_EXPANSION job will use to raise the real
	// work.ScopeExpansionRequest.
	ScopeExpansionRequested bool
	ScopeExpansionRequestID string
	ScopeExpansionJobID     string
}

const (
	ExecutionAttemptFinalizedEventType     = "EXECUTION_ATTEMPT_FINALIZED"
	ExecutionAttemptFinalizedSchemaVersion = 1

	// NodeRunFailedEventType/NodeRunFailedSchemaVersion identify V4-06's
	// own NODE_RUN_FAILED event — see nodeRunFailedEventPayload's own doc
	// comment.
	NodeRunFailedEventType     = "NODE_RUN_FAILED"
	NodeRunFailedSchemaVersion = 1
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

// nodeRunFailedFailureKind is NODE_RUN_FAILED's own closed "why" vocabulary
// (V4-06, confirmed with the user before writing this file): a technical
// retry either exhausts its own budget, or the failure was never eligible
// for retry at all — two structurally different reasons a later consumer
// (V4-12A/C, UI) needs to tell apart.
type nodeRunFailedFailureKind string

const (
	NodeRunFailureKindRetryExhausted      nodeRunFailedFailureKind = "RETRY_EXHAUSTED"
	NodeRunFailureKindNonRetryableFailure nodeRunFailedFailureKind = "NON_RETRYABLE_FAILURE"
)

// nodeRunFailedEventPayload is NODE_RUN_FAILED's own JSON shape (V4-06,
// confirmed with the user before writing this file): the typed
// "escalation" this task's own scope stops at — never a fabricated
// blocker record (the blockers table does not exist yet, V4-12A/C's own
// scope) and never NodeRun.BLOCKED (a different, unrelated state). The
// LAST Attempt's own TerminationReason/FailureCode are deliberately left
// unchanged by exhaustion — RETRY_EXHAUSTED/NON_RETRYABLE_FAILURE are
// NodeRun-level classifications, not a second Attempt-level reason
// overwriting what that Attempt actually failed with.
type nodeRunFailedEventPayload struct {
	RunID                  string `json:"runId"`
	WorkItemID             string `json:"workItemId"`
	NodeRunID              string `json:"nodeRunId"`
	FailureKind            string `json:"failureKind"`
	LastAttemptID          string `json:"lastAttemptId"`
	AttemptsUsed           uint32 `json:"attemptsUsed"`
	MaxAttempts            uint32 `json:"maxAttempts"`
	LastErrorCode          string `json:"lastErrorCode"`
	AttemptPolicyVersionID string `json:"attemptPolicyVersionId"`
	JobID                  string `json:"jobId,omitempty"`
}

// FinalizeExecutionAttempt performs exactly one fenced terminal transition.
// See this file's own package doc comment for the full seven-step design.
// clk is V1-02's own clock.Clock (this task is that package's first real
// caller) — every backoff/"now" computation in the retry path goes through
// it, never time.Now() directly, so V4-06's own required "fake clock"
// tests can control backoff timing deterministically.
func FinalizeExecutionAttempt(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock, req FinalizeExecutionAttemptRequest) (FinalizeExecutionAttemptResult, error) {
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
	needsFailureCode := req.NextState == runtimedomain.ExecutionAttemptFailed || req.NextState == runtimedomain.ExecutionAttemptTimedOut
	if needsFailureCode && req.FailureCode == "" {
		return FinalizeExecutionAttemptResult{}, ErrFailureCodeRequired
	}
	if !needsFailureCode && req.FailureCode != "" {
		return FinalizeExecutionAttemptResult{}, fmt.Errorf("runtime: FailureCode must be empty unless NextState is FAILED or TIMED_OUT, got %q for %q", req.FailureCode, req.NextState)
	}
	// V4-12A: BLOCKED requires exactly TerminationReasonScopeExpansionRequired
	// and a well-formed RequestedScopeExpansion — the strict mutual-
	// exclusivity matrix confirmed with the user before writing this file
	// (SUCCEEDED has an outcome; FAILED/TIMED_OUT has a FailureCode;
	// BLOCKED has a scope proposal — never more than one of the three). A
	// caller with a malformed proposal must never reach here with
	// NextState BLOCKED at all — ExecuteNodeHandler's own translation
	// layer (execute.go) rejects that shape as FAILED/OUTCOME_REJECTED
	// before ever calling this function, so this is this function's own
	// defensive, should-be-unreachable-in-practice re-check, not the
	// primary enforcement point.
	if req.NextState == runtimedomain.ExecutionAttemptBlocked {
		if req.TerminationReason != runtimedomain.TerminationReasonScopeExpansionRequired {
			return FinalizeExecutionAttemptResult{}, fmt.Errorf("runtime: BLOCKED requires TerminationReason=%s, got %q", runtimedomain.TerminationReasonScopeExpansionRequired, req.TerminationReason)
		}
		if err := req.RequestedScopeExpansion.Validate(); err != nil {
			return FinalizeExecutionAttemptResult{}, err
		}
	} else if req.RequestedScopeExpansion != nil {
		return FinalizeExecutionAttemptResult{}, fmt.Errorf("runtime: RequestedScopeExpansion must be empty unless NextState is BLOCKED, got %q", req.NextState)
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
			NextState: req.NextState, TerminationReason: req.TerminationReason, FailureCode: req.FailureCode,
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

		// Step 6: branch on outcome.
		switch req.NextState {
		case runtimedomain.ExecutionAttemptSucceeded:
			// V5-08B's own locked decision #2, protocol phase 3: when the
			// caller is the NodeExecutor->AgentExecutor bridge (req.Evidence
			// != nil), re-validate its entire proposal and commit the
			// evidence it names — atomically, in this SAME transaction,
			// before ever routing the NodeRun forward. Every pre-existing
			// SUCCEEDED caller (the fake NodeExecutor V4-05's own tests
			// still use, any future COMMAND/MACHINE_GATE executor) leaves
			// Evidence nil and is entirely unaffected.
			if req.Evidence != nil {
				if err := validateAndAttachFinalizationEvidenceTx(ctx, tx, clk, req, attempt, run); err != nil {
					return err
				}
			}
			advanceResult, err := advanceRunTx(ctx, tx, ids, AdvanceRunRequest{
				RunID: req.RunID, NodeRunID: req.NodeRunID, Outcome: req.SelectedOutcome,
				SharedStatePatch: req.SharedStatePatch, CorrelationID: req.CorrelationID, JobID: string(req.JobLease.JobID),
			})
			if err != nil {
				return err
			}
			result.Advanced = true
			result.AdvanceResult = advanceResult

		case runtimedomain.ExecutionAttemptFailed, runtimedomain.ExecutionAttemptTimedOut:
			if err := decideRetryOrExhaustion(ctx, tx, ids, clk, req, updatedAttempt, run, &result); err != nil {
				return err
			}

		case runtimedomain.ExecutionAttemptBlocked:
			if err := requestScopeExpansionTx(ctx, tx, ids, req, run, &result); err != nil {
				return err
			}

		case runtimedomain.ExecutionAttemptCancelled:
			if err := decideCancelledOutcomeTx(ctx, tx, ids, req, run); err != nil {
				return err
			}
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

// validateAndAttachEvidenceArtifactsTx is V5-08B's own locked decision #2,
// protocol phase 3's own artifact-and-Evidence half ("...chuyển artifact
// ORPHAN -> ATTACHED..."), extracted as its own terminal-state-neutral
// helper by the V5-09/V5-10 acceptance-gap remediation PR2 (2026-09-10) so
// both the SUCCEEDED branch (validateAndAttachFinalizationEvidenceTx,
// below — which also builds/inserts the completion Checkpoint, a
// SUCCEEDED-only concept) and the FAILED/TIMED_OUT branch
// (decideRetryOrExhaustion, this file's own MACHINE_GATE non-PASS case)
// can share the identical validation: the terminal event genuinely
// exists, the diff-manifest set is complete against this NodeRun's own
// EffectiveScope, every named artifact (diff manifest AND output) is
// still ORPHAN before being promoted to ATTACHED, and every EvidenceEntry
// names only artifacts this function has itself just promoted — never a
// fresh, unlisted one. JobLease/WriteLease fencing already ran in Steps
// 1/3 of FinalizeExecutionAttempt before this is ever reached. The diff
// CONTENT itself (scope violations) was already checked by the executor
// outside this transaction, via scopeguard.ValidateDiffs, before it ever
// proposed a terminal outcome at all — this function re-checks
// completeness (every scoped repository produced evidence, none missing,
// none extra), never re-derives correctness from raw diff bytes (that
// would require an ArtifactStore call inside a transaction, exactly what
// docs/architecture/04-go-core-spec.md §11.1 forbids).
func validateAndAttachEvidenceArtifactsTx(
	ctx context.Context, tx ports.Tx, clk clock.Clock, req FinalizeExecutionAttemptRequest, run runtimedomain.WorkflowRun,
) error {
	evidence := req.Evidence

	events, err := tx.AgentEvents().ListByAttempt(ctx, req.AttemptID)
	if err != nil {
		return err
	}
	found := false
	for _, event := range events {
		if event.Sequence == evidence.TerminalEventSequence {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("runtime: finalization evidence names terminal event sequence %d, but no such agent_events row exists for attempt %s", evidence.TerminalEventSequence, req.AttemptID)
	}

	nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
	if err != nil {
		return err
	}
	expectedRepositories := make(map[project.RepositoryID]struct{}, len(nodeRun.EffectiveScope))
	for _, scope := range nodeRun.EffectiveScope {
		expectedRepositories[scope.RepositoryID()] = struct{}{}
	}
	actualRepositories := make(map[project.RepositoryID]struct{}, len(evidence.DiffManifestArtifacts))
	for _, ref := range evidence.DiffManifestArtifacts {
		actualRepositories[ref.RepositoryID] = struct{}{}
	}
	if len(expectedRepositories) != len(actualRepositories) {
		return fmt.Errorf("runtime: finalization evidence names %d diff manifest artifacts, want exactly one per effective-scope repository (%d)", len(actualRepositories), len(expectedRepositories))
	}
	for repositoryID := range expectedRepositories {
		if _, ok := actualRepositories[repositoryID]; !ok {
			return fmt.Errorf("runtime: finalization evidence is missing a diff manifest artifact for effective-scope repository %s", repositoryID)
		}
	}

	// Promote every named artifact ORPHAN -> ATTACHED — only now, after
	// every other check above has already passed.
	for _, ref := range evidence.DiffManifestArtifacts {
		record, err := tx.Artifacts().GetArtifact(ctx, ref.ArtifactID)
		if err != nil {
			return fmt.Errorf("runtime: load diff manifest artifact %s: %w", ref.ArtifactID, err)
		}
		if _, err := tx.Artifacts().TransitionArtifactAttachState(ctx, ports.TransitionArtifactAttachStateRequest{
			ArtifactID: ref.ArtifactID, ExpectedState: artifact.Orphan, ExpectedVersion: record.Version, NextState: artifact.Attached,
		}); err != nil {
			return fmt.Errorf("runtime: attach diff manifest artifact %s: %w", ref.ArtifactID, err)
		}
	}

	// Promote every OutputArtifactRefs entry ORPHAN -> ATTACHED too (V5-09/
	// V5-10 acceptance-gap remediation): previously CommandNodeExecutor/
	// GateNodeExecutor inserted this artifact directly ATTACHED, in its own
	// transaction, before finalize ever ran — an unfenced write nothing
	// here ever re-validated. It is now staged ORPHAN by the executor
	// (Put+Verify outside any transaction, insert ORPHAN in its own short
	// transaction — buildEvidence's own Phase 1+2, applied to output
	// artifacts too) and promoted here, atomically with everything else
	// this transaction commits. Deduplicated: more than one EvidenceEntry
	// may name the SAME shared artifact (a MACHINE_GATE's own GateResult
	// artifact covers every criterion at once), and an artifact already
	// promoted must never be re-transitioned a second time.
	promotedOutputArtifacts := make(map[string]bool, len(evidence.OutputArtifactRefs))
	for _, artifactID := range evidence.OutputArtifactRefs {
		if promotedOutputArtifacts[artifactID] {
			continue
		}
		record, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		if err != nil {
			return fmt.Errorf("runtime: load output artifact %s: %w", artifactID, err)
		}
		if _, err := tx.Artifacts().TransitionArtifactAttachState(ctx, ports.TransitionArtifactAttachStateRequest{
			ArtifactID: artifactID, ExpectedState: artifact.Orphan, ExpectedVersion: record.Version, NextState: artifact.Attached,
		}); err != nil {
			return fmt.Errorf("runtime: attach output artifact %s: %w", artifactID, err)
		}
		promotedOutputArtifacts[artifactID] = true
	}

	// Write one Evidence row per proposed entry (V5-09/V5-10 acceptance-gap
	// remediation) — idempotent by deterministic ID (AttemptID+Kind, see
	// runtime.Evidence's own doc comment), so a redelivered finalize never
	// creates a duplicate. Every entry's own ArtifactReferences must
	// already be named in OutputArtifactRefs — never a fresh, unlisted
	// artifact this function has not itself just promoted above.
	for _, entry := range evidence.EvidenceEntries {
		if len(entry.ArtifactReferences) == 0 {
			return fmt.Errorf("runtime: evidence entry %q names no artifact reference", entry.Kind)
		}
		for _, artifactID := range entry.ArtifactReferences {
			if !promotedOutputArtifacts[artifactID] {
				return fmt.Errorf("runtime: evidence entry %q names artifact %s, which is not in OutputArtifactRefs", entry.Kind, artifactID)
			}
		}
		evidenceRow, err := runtimedomain.NewEvidence(
			runtimedomain.EvidenceID(req.AttemptID+":"+entry.Kind), run.ProjectID, run.WorkItemID,
			runtimedomain.WorkflowRunID(req.RunID), runtimedomain.NodeRunID(req.NodeRunID), runtimedomain.ExecutionAttemptID(req.AttemptID),
			entry.Kind, entry.Verdict, entry.ArtifactReferences, evidence.FinalRevisionSet, entry.PolicyVersion, clk.Now(),
		)
		if err != nil {
			return fmt.Errorf("runtime: build evidence entry %q: %w", entry.Kind, err)
		}
		if _, err := tx.Runtime().CreateEvidence(ctx, evidenceRow); err != nil {
			return fmt.Errorf("runtime: persist evidence entry %q: %w", entry.Kind, err)
		}
	}
	return nil
}

// validateAndAttachFinalizationEvidenceTx is the SUCCEEDED-only half of
// V5-08B's own locked decision #2, protocol phase 3: on top of
// validateAndAttachEvidenceArtifactsTx's own terminal-state-neutral checks
// (above), a SUCCEEDED completion also requires evidence.ProposedOutcome
// to match req.SelectedOutcome, and — only once every other check has
// passed — builds and inserts the real completion Checkpoint (a concept
// meaningless for FAILED/TIMED_OUT, which never advances a NodeRun to a
// next node — see decideRetryOrExhaustion's own call to
// validateAndAttachEvidenceArtifactsTx directly, no Checkpoint involved).
func validateAndAttachFinalizationEvidenceTx(
	ctx context.Context, tx ports.Tx, clk clock.Clock, req FinalizeExecutionAttemptRequest,
	attempt runtimedomain.ExecutionAttempt, run runtimedomain.WorkflowRun,
) error {
	evidence := req.Evidence
	if evidence.ProposedOutcome == nil || evidence.ProposedOutcome.Value != req.SelectedOutcome {
		return fmt.Errorf("runtime: finalization evidence proposed outcome %+v does not match SelectedOutcome %q", evidence.ProposedOutcome, req.SelectedOutcome)
	}
	if err := validateAndAttachEvidenceArtifactsTx(ctx, tx, clk, req, run); err != nil {
		return err
	}

	// attempt.ContextSnapshotID (despite its legacy-checkpoint-recovery
	// type name) is exactly the V5-04 contextsnapshot.ID a V5-08B0-assembled
	// Attempt is bound to — gatherAssembledRequestInputs already resolves
	// it the identical way (string-cast through this same field) before
	// ever calling tx.ContextSnapshots().GetSnapshot.
	if attempt.ContextSnapshotID == nil {
		return fmt.Errorf("runtime: attempt %s has no bound context snapshot, cannot build a completion checkpoint", req.AttemptID)
	}
	artifactReferences := make([]string, 0, len(evidence.DiffManifestArtifacts)+len(evidence.OutputArtifactRefs))
	for _, ref := range evidence.DiffManifestArtifacts {
		artifactReferences = append(artifactReferences, ref.ArtifactID)
	}
	artifactReferences = append(artifactReferences, evidence.OutputArtifactRefs...)
	// Checkpoint.Sequence uses evidence.TerminalEventSequence directly
	// (rather than continuing agentevents.Sink's own separate, much
	// smaller checkpointSeq counter, which this transaction has no safe
	// way to read — LoadLatestCheckpoint is CheckpointStore's own legacy
	// autocommit call, unsafe to invoke against a second connection while
	// this transaction already holds the write lock): the terminal
	// event's own sequence is always the attempt's own highest, so it can
	// never collide with an earlier mid-run checkpoint's own Sequence: a
	// real UNIQUE(attempt_id, sequence) conflict would surface as an
	// ordinary error here, exactly as InsertCheckpoint's own doc comment
	// describes, rather than silently colliding.
	checkpoint, err := runtimedomain.NewCheckpoint(
		runtimedomain.CheckpointID(evidence.CompletionCheckpointID), runtimedomain.WorkflowRunID(req.RunID),
		runtimedomain.NodeRunID(req.NodeRunID), runtimedomain.ExecutionAttemptID(req.AttemptID),
		evidence.TerminalEventSequence, evidence.TerminalEventSequence, runtimedomain.ContextSnapshotID(string(*attempt.ContextSnapshotID)),
		evidence.FinalRevisionSet, canonicalStateHash(run.SharedState), artifactReferences, clk.Now(),
	)
	if err != nil {
		return fmt.Errorf("runtime: build completion checkpoint: %w", err)
	}
	if err := tx.Checkpoints().InsertCheckpoint(ctx, checkpoint); err != nil {
		return fmt.Errorf("runtime: insert completion checkpoint: %w", err)
	}
	return nil
}

// decideCancelledOutcomeTx is V4-12B's own Step 6 branch (finalize.go's
// own switch, above): a RUNNING Attempt that ExecuteNodeHandler's own
// second worker re-check checkpoint (execute.go) caught before real
// execution ever started — CAS the owning NodeRun RUNNING->CANCELLED
// (mirroring how the SUCCEEDED/FAILED cases each transition their own
// NodeRun), terminalize its own BranchToken and re-evaluate the JOIN if
// it belonged to a FORK branch (the identical "a branch dying mid-flight
// may prove the JOIN's own policy impossible" logic
// decideRetryOrExhaustion's own FAILED branch already applies — evaluateJoinTx
// already tallies BranchTokenCancelled the same way it tallies FAILED),
// and reconcile — this may be exactly the event that lets a CANCELLING
// Run finally close out to CANCELLED (reconcileCancellingRunTx's own
// "zero live" check, completion.go).
func decideCancelledOutcomeTx(ctx context.Context, tx ports.Tx, ids idsource.Source, req FinalizeExecutionAttemptRequest, run runtimedomain.WorkflowRun) error {
	nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
	if err != nil {
		return err
	}
	if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
		NodeRunID: req.NodeRunID, ExpectedState: runtimedomain.NodeRunRunning, ExpectedVersion: nodeRun.Version,
		NextState: runtimedomain.NodeRunCancelled,
	}); err != nil {
		return err
	}
	version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
	if err != nil {
		return err
	}
	document := version.Document()

	if err := terminalizeBranchTokenForCancelledNodeRunTx(ctx, tx, ids, run, document, nodeRun, req.CorrelationID, string(req.JobLease.JobID)); err != nil {
		return err
	}

	return reconcileRunTerminalityTx(ctx, tx, run, document, req.CorrelationID, string(req.JobLease.JobID))
}

// terminalizeBranchTokenForCancelledNodeRunTx is shared by
// decideCancelledOutcomeTx (above) and CancelRunCoordinatorHandler's own
// sweep (cancel_run_coordinator.go): when a NodeRun belonging to a FORK
// branch is cancelled, its own BranchToken must be terminalized in the
// SAME transaction and the JOIN's own ALL/ANY/QUORUM policy re-evaluated —
// the identical "a branch dying mid-flight may prove the JOIN's own
// policy impossible" logic decideRetryOrExhaustion's own FAILED branch
// already applies (evaluateJoinTx already tallies BranchTokenCancelled
// the same way it tallies FAILED). A no-op for a nodeRun outside any FORK
// branch (BranchTokenID nil).
func terminalizeBranchTokenForCancelledNodeRunTx(
	ctx context.Context, tx ports.Tx, ids idsource.Source, run runtimedomain.WorkflowRun, document workflow.WorkflowDocument,
	nodeRun runtimedomain.NodeRun, correlationID, jobID string,
) error {
	if nodeRun.BranchTokenID == nil {
		return nil
	}
	branchToken, err := tx.Runtime().GetBranchTokenByID(ctx, string(*nodeRun.BranchTokenID))
	if err != nil {
		return err
	}
	if _, err := tx.Runtime().TransitionBranchToken(ctx, ports.TransitionBranchTokenRequest{
		BranchTokenID: string(branchToken.ID), ExpectedVersion: branchToken.Version,
		NextState: runtimedomain.BranchTokenCancelled, NextCurrentNodeKey: nodeRun.NodeKey,
	}); err != nil {
		return err
	}
	forkRun, err := tx.Runtime().GetNodeRun(ctx, string(branchToken.ForkNodeRunID))
	if err != nil {
		return err
	}
	forkNode, ok := findNode(document, forkRun.NodeKey)
	if !ok {
		return fmt.Errorf("runtime: fork node %s not found in workflow version %s", forkRun.NodeKey, run.WorkflowVersionID)
	}
	joinNode, ok := resolveForkJoinNode(document, forkNode)
	if !ok {
		return fmt.Errorf("%w: fork %s", ErrForkJoinNotFound, forkNode.Key)
	}
	_, err = evaluateJoinTx(ctx, tx, ids, run, document, run.SharedState, string(branchToken.ForkNodeRunID), joinNode, correlationID, jobID)
	return err
}

// decideRetryOrExhaustion is V4-06's own retry policy: re-resolve the
// pinned ATTEMPT policy (the exact immutable PolicyVersion V4-04's own
// scheduling already pinned — re-loading it can never drift, since a
// published PolicyVersion never changes), then either create the next
// ExecutionAttempt (retryable, budget remains) or CAS the NodeRun to
// FAILED and append NODE_RUN_FAILED (non-retryable, or budget exhausted).
// Composed inside FinalizeExecutionAttempt's own transaction — see that
// function's own doc comment step 6.
func decideRetryOrExhaustion(
	ctx context.Context, tx ports.Tx, ids idsource.Source, clk clock.Clock,
	req FinalizeExecutionAttemptRequest, attempt runtimedomain.ExecutionAttempt, run runtimedomain.WorkflowRun,
	result *FinalizeExecutionAttemptResult,
) error {
	// V5-09/V5-10 acceptance-gap remediation PR2 (2026-09-10 post-merge
	// review): a MACHINE_GATE's own non-PASS verdict proposes Evidence here
	// too (GateNodeExecutor's own classify, non-PASS branch) — validated
	// and attached before this function commits any retry/exhaustion
	// decision, using the SAME terminal-state-neutral helper the SUCCEEDED
	// branch uses (FinalizeExecutionAttempt's own switch, above), minus the
	// Checkpoint/ProposedOutcome concerns that belong only to SUCCEEDED.
	// nil for every other FAILED/TIMED_OUT caller (AGENT/COMMAND's own
	// generic failure — never required to carry criteria Evidence).
	if req.Evidence != nil {
		if err := validateAndAttachEvidenceArtifactsTx(ctx, tx, clk, req, run); err != nil {
			return err
		}
	}

	attemptRules, attemptPolicyVersionID, err := resolvePinnedAttemptRules(ctx, tx, req.NodeRunID)
	if err != nil {
		return err
	}

	retryable := !req.FailureCode.NeverRetryable() && retryableErrorCodeDeclared(attemptRules.RetryableErrorCodes, req.FailureCode)
	budgetRemains := uint32(attempt.AttemptNumber) < attemptRules.MaxAttempts
	// V4-12B (ADR-020): "scheduler ngừng tạo activation/technical retry/
	// rework mới ngay khi intent commit" — a Run already CANCELLING/
	// CANCELLED never gets a new retry Attempt/job, regardless of
	// remaining budget; this NodeRun instead falls through to the
	// existing FAILED path below exactly as a genuinely non-retryable or
	// budget-exhausted failure already does, and reconcileRunTerminalityTx's
	// own tail call (this function's own last line) is what eventually
	// closes the Run out to CANCELLED once nothing live remains.
	runCancelling := run.State == runtimedomain.WorkflowRunCancelling || run.State == runtimedomain.WorkflowRunCancelled

	if retryable && budgetRemains && !runCancelling {
		nextAttemptID := ids.NewID()
		nextAttempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID(nextAttemptID), attempt.NodeRunID, attempt.AttemptNumber+1,
			attempt.ExecutionProfileHash, attempt.ProviderKey, attempt.InputRevisionSet,
		)
		if err != nil {
			return err
		}

		// V5-04: a technical retry's new Attempt gets its OWN cloned
		// snapshot — same message/resource refs and revision set as the
		// attempt it replaces, bound to the NEW AttemptID — never a
		// shared/reused row (each attempt_context_snapshots row has
		// exactly one owning attempt_id). Gracefully skipped (nextAttempt
		// stays unbound) when the PREVIOUS attempt itself has no bound
		// snapshot — true for any attempt created before this task's own
		// schedule.go change, or by a test fixture that never went
		// through it; there is nothing to clone in that case, the same
		// state such an attempt was already in. The clone is only
		// INSERTED after nextAttempt itself is (see below) —
		// attempt_context_snapshots.attempt_id's own foreign key requires
		// the Attempt row to already exist.
		var clonedSnapshot contextsnapshot.Snapshot
		haveClone := false
		previousSnapshot, err := tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, string(attempt.ID))
		if err != nil && !errors.Is(err, ports.ErrPersistenceNotFound) {
			return err
		}
		if err == nil {
			nextSnapshotID := contextsnapshot.ID(ids.NewID())
			clonedSnapshot, err = contextsnapshot.NewSnapshot(
				nextSnapshotID, previousSnapshot.ProjectID, previousSnapshot.WorkItemID, contextsnapshot.AttemptID(nextAttemptID),
				previousSnapshot.MessageRefs, previousSnapshot.ResourceRefs, previousSnapshot.Revisions, clk.Now(),
			)
			if err != nil {
				return err
			}
			nextAttempt.ContextSnapshotID = &nextSnapshotID
			haveClone = true
		}

		if _, err := tx.Runtime().CreateExecutionAttempt(ctx, nextAttempt); err != nil {
			return err
		}
		if haveClone {
			if _, err := tx.ContextSnapshots().CreateSnapshot(ctx, clonedSnapshot); err != nil {
				return err
			}
		}
		jobPayload, err := json.Marshal(ExecuteNodeJobPayload{
			RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: nextAttemptID, CorrelationID: req.CorrelationID,
		})
		if err != nil {
			return fmt.Errorf("marshal %s retry job payload: %w", ExecuteNodeJobKind, err)
		}
		if _, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(ids.NewID()), ProjectID: run.ProjectID, Kind: ExecuteNodeJobKind,
			AggregateType: "ExecutionAttempt", AggregateID: nextAttemptID, Payload: jobPayload,
			AvailableAt: clk.Now().Add(time.Duration(attemptRules.BackoffSeconds) * time.Second),
			MaxClaims:   defaultExecuteNodeJobMaxClaims, IdempotencyKey: "execute-" + nextAttemptID,
		}); err != nil {
			return err
		}
		result.Retried = true
		result.NextAttemptID = nextAttemptID
		return nil
	}

	// Non-retryable, or retryable but budget exhausted: CAS the NodeRun to
	// FAILED and append NODE_RUN_FAILED — never a fabricated blocker, never
	// NodeRun.BLOCKED, never an automatic WorkflowRun failure (V4-12's own
	// scope).
	nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
	if err != nil {
		return err
	}
	if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
		NodeRunID: req.NodeRunID, ExpectedState: runtimedomain.NodeRunRunning, ExpectedVersion: nodeRun.Version,
		NextState: runtimedomain.NodeRunFailed,
	}); err != nil {
		return err
	}
	// V4-12: document is needed unconditionally now (not just for the
	// BranchTokenID case below) — reconcileRunTerminalityTx's own tail
	// call needs it to recognize an END-type node among any SUCCEEDED
	// NodeRun.
	version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
	if err != nil {
		return err
	}
	document := version.Document()

	// V4-10: a NodeRun belonging to a FORK branch that FAILS terminally
	// (non-retryable, or retryable but budget exhausted) must terminalize
	// its own BranchToken in this SAME transaction — locked with the user
	// ("NodeRun trong branch FAILED/CANCELLED phải terminalize token
	// tương ứng trong cùng transaction") so a JOIN's own later ALL/ANY/
	// QUORUM evaluation (V4-11's own scope) never waits forever on a
	// branch whose own work already died.
	if nodeRun.BranchTokenID != nil {
		branchToken, err := tx.Runtime().GetBranchTokenByID(ctx, string(*nodeRun.BranchTokenID))
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionBranchToken(ctx, ports.TransitionBranchTokenRequest{
			BranchTokenID: string(branchToken.ID), ExpectedVersion: branchToken.Version,
			NextState: runtimedomain.BranchTokenFailed, NextCurrentNodeKey: nodeRun.NodeKey,
		}); err != nil {
			return err
		}
		// V4-11: a branch dying mid-flight (never reaching its own JOIN at
		// all) may be the exact event that proves the JOIN's own ALL/ANY/
		// QUORUM policy has become impossible — evaluated here, in this
		// SAME transaction, the identical "every token-state-change
		// re-evaluates" discipline advanceRunTx's own JOIN-arrival path
		// already follows (confirmed with the user before writing this
		// task's code).
		forkRun, err := tx.Runtime().GetNodeRun(ctx, string(branchToken.ForkNodeRunID))
		if err != nil {
			return err
		}
		forkNode, ok := findNode(document, forkRun.NodeKey)
		if !ok {
			return fmt.Errorf("runtime: fork node %s not found in workflow version %s", forkRun.NodeKey, run.WorkflowVersionID)
		}
		joinNode, ok := resolveForkJoinNode(document, forkNode)
		if !ok {
			return fmt.Errorf("%w: fork %s", ErrForkJoinNotFound, forkNode.Key)
		}
		if _, err := evaluateJoinTx(
			ctx, tx, ids, run, document, run.SharedState, string(branchToken.ForkNodeRunID), joinNode,
			req.CorrelationID, string(req.JobLease.JobID),
		); err != nil {
			return err
		}
	}
	failureKind := NodeRunFailureKindNonRetryableFailure
	if retryable {
		failureKind = NodeRunFailureKindRetryExhausted
	}
	eventPayload, err := json.Marshal(nodeRunFailedEventPayload{
		RunID: req.RunID, WorkItemID: string(run.WorkItemID), NodeRunID: req.NodeRunID,
		FailureKind: string(failureKind), LastAttemptID: req.AttemptID, AttemptsUsed: attempt.AttemptNumber,
		MaxAttempts: attemptRules.MaxAttempts, LastErrorCode: string(req.FailureCode),
		AttemptPolicyVersionID: attemptPolicyVersionID, JobID: string(req.JobLease.JobID),
	})
	if err != nil {
		return fmt.Errorf("marshal %s event payload: %w", NodeRunFailedEventType, err)
	}
	if err := tx.Events().Append(ctx, ports.DomainEvent{
		ID: req.NodeRunID + "-failed", ProjectID: string(run.ProjectID),
		AggregateType: "NodeRun", AggregateID: req.NodeRunID, Sequence: int64(nodeRun.Version + 1),
		EventType: NodeRunFailedEventType, SchemaVersion: NodeRunFailedSchemaVersion, PayloadJSON: string(eventPayload),
		CorrelationID: req.CorrelationID, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	result.NodeRunFailed = true

	// V4-12: this NodeRun's own terminal failure — whether or not it
	// belonged to a FORK branch — may be exactly the event that leaves
	// the Run with no live NodeRun anywhere and no END ever reached;
	// reconcileRunTerminalityTx re-derives the Run's own overall
	// terminality fresh and CASes it to FAILED when that is so (confirmed
	// with the user before writing this task's code: this is the real
	// Run-level failure aggregation this file's own earlier "never an
	// automatic WorkflowRun failure — V4-12's own scope" comment always
	// deferred to).
	return reconcileRunTerminalityTx(ctx, tx, run, document, req.CorrelationID, string(req.JobLease.JobID))
}

// requestScopeExpansionTx is V4-12A's own BLOCKED branch (confirmed with
// the user before writing this file): CAS the NodeRun to BLOCKED alongside
// the Attempt (both already CASed the same way decideRetryOrExhaustion's
// own FAILED branch CASes the NodeRun — RUNNING -> BLOCKED here, never
// touching any BranchToken this NodeRun might belong to, since BLOCKED is
// "paused", not a terminal branch outcome the JOIN's own ALL/ANY/QUORUM
// policy needs to observe), reserve the ScopeExpansionRequestID a
// REQUEST_SCOPE_EXPANSION job will use to raise the real
// work.RequestScopeExpansion command with that exact ID, and persist the
// durable ScopeExpansionOrigin link — all inside this SAME fenced
// transaction, before the job is ever claimed.
func requestScopeExpansionTx(
	ctx context.Context, tx ports.Tx, ids idsource.Source,
	req FinalizeExecutionAttemptRequest, run runtimedomain.WorkflowRun, result *FinalizeExecutionAttemptResult,
) error {
	nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
	if err != nil {
		return err
	}
	if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
		NodeRunID: req.NodeRunID, ExpectedState: runtimedomain.NodeRunRunning, ExpectedVersion: nodeRun.Version,
		NextState: runtimedomain.NodeRunBlocked,
	}); err != nil {
		return err
	}

	requestID := ids.NewID()
	origin, err := runtimedomain.NewScopeExpansionOrigin(
		runtimedomain.ScopeExpansionOriginID(req.AttemptID), runtimedomain.NodeRunID(req.NodeRunID), run.ID,
		string(run.WorkItemID), string(run.FamilyID), requestID, *req.RequestedScopeExpansion,
	)
	if err != nil {
		return err
	}
	if _, err := tx.Runtime().CreateScopeExpansionOrigin(ctx, origin); err != nil {
		return err
	}

	// V4-12C: open the WorkItem-authority half of this block — ADR-020's own
	// blocker-type table names SCOPE_EXPANSION_REQUIRED explicitly, and
	// ScopeExpansionOriginID already equals req.AttemptID, so this blocker's
	// own deterministic ID (minted from the same AttemptID) is naturally
	// idempotent against a redelivered finalize the identical way the
	// ScopeExpansionOrigin row itself already is. Its own closing half —
	// resolving OPEN -> RESOLVED and unblocking the WorkItem back to ACTIVE —
	// is reactivateBlockedNodeRunTx's own job (scope_expansion.go), the only
	// path with authority to ever resolve this specific blocker type
	// (workdomain.BlockerType.ResolvableViaCommand's own doc comment).
	blockerID := req.AttemptID + "-scope-expansion-blocker"
	if _, err := openWorkItemBlockerTx(
		ctx, tx, run.ProjectID, string(run.WorkItemID), blockerID, workdomain.BlockerScopeExpansionRequired,
		req.RunID, req.NodeRunID, req.AttemptID, req.RequestedScopeExpansion.Reason, req.CorrelationID, string(req.JobLease.JobID),
	); err != nil {
		return err
	}

	jobPayload, err := json.Marshal(RequestScopeExpansionJobPayload{
		RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID, CorrelationID: req.CorrelationID,
	})
	if err != nil {
		return fmt.Errorf("marshal %s job payload: %w", RequestScopeExpansionJobKind, err)
	}
	job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(ids.NewID()), ProjectID: run.ProjectID, Kind: RequestScopeExpansionJobKind,
		AggregateType: "ExecutionAttempt", AggregateID: req.AttemptID, Payload: jobPayload,
		MaxClaims: defaultRequestScopeExpansionJobMaxClaims, IdempotencyKey: "scope-expansion:" + req.AttemptID,
	})
	if err != nil {
		return err
	}

	result.ScopeExpansionRequested = true
	result.ScopeExpansionRequestID = requestID
	result.ScopeExpansionJobID = string(job.ID)

	// V4-12: a BLOCKED NodeRun is recoverable-stalled, never itself a
	// reason to fail the Run — but reconciling here anyway (harmless,
	// almost always a no-op) keeps this transition consistent with every
	// other "could have removed the Run's last live activation" call
	// site.
	version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
	if err != nil {
		return err
	}
	return reconcileRunTerminalityTx(ctx, tx, run, version.Document(), req.CorrelationID, string(req.JobLease.JobID))
}

// resolvePinnedAttemptRules re-loads the exact ATTEMPT-category PolicyRef
// V4-04's own ScheduleExecutableNodeRun pinned into the NodeRun's own
// DecisionArtifact ("<nodeRunId>-execution-profile-v1") — this is safe to
// re-load rather than caching, since a published PolicyVersion is
// immutable and content-hashed; re-reading it can never observe a
// different value than what was originally pinned.
func resolvePinnedAttemptRules(ctx context.Context, tx ports.Tx, nodeRunID string) (policy.AttemptRules, string, error) {
	decision, err := tx.Runtime().GetDecisionArtifact(ctx, nodeRunID+"-execution-profile-v1")
	if err != nil {
		return policy.AttemptRules{}, "", fmt.Errorf("runtime: load execution profile decision for node run %s: %w", nodeRunID, err)
	}
	var profile struct {
		Policies []struct {
			VersionID string `json:"versionId"`
			Category  string `json:"category"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(decision.Result, &profile); err != nil {
		return policy.AttemptRules{}, "", fmt.Errorf("runtime: decode execution profile decision for node run %s: %w", nodeRunID, err)
	}
	var attemptPolicyVersionID string
	for _, p := range profile.Policies {
		if p.Category == string(policy.CategoryAttempt) {
			attemptPolicyVersionID = p.VersionID
			break
		}
	}
	if attemptPolicyVersionID == "" {
		return policy.AttemptRules{}, "", fmt.Errorf("runtime: node run %s has no pinned ATTEMPT policy", nodeRunID)
	}
	policyVersion, err := tx.Definitions().LoadVersion(ctx, attemptPolicyVersionID)
	if err != nil {
		return policy.AttemptRules{}, "", fmt.Errorf("runtime: resolve node run %s attempt policy: %w", nodeRunID, err)
	}
	policyDoc, err := decodeCompiledPolicy(policyVersion.CompiledSnapshot())
	if err != nil {
		return policy.AttemptRules{}, "", fmt.Errorf("runtime: node run %s: %w", nodeRunID, err)
	}
	if policyDoc.Attempt == nil {
		return policy.AttemptRules{}, "", fmt.Errorf("runtime: node run %s pinned policy %s has no Attempt rules", nodeRunID, attemptPolicyVersionID)
	}
	return *policyDoc.Attempt, attemptPolicyVersionID, nil
}

func retryableErrorCodeDeclared(declared []errorcode.Code, code errorcode.Code) bool {
	for _, c := range declared {
		if c == code {
			return true
		}
	}
	return false
}

func isFinalizableExecutionAttemptState(state runtimedomain.ExecutionAttemptState) bool {
	switch state {
	case runtimedomain.ExecutionAttemptSucceeded, runtimedomain.ExecutionAttemptFailed,
		runtimedomain.ExecutionAttemptTimedOut, runtimedomain.ExecutionAttemptCancelled,
		runtimedomain.ExecutionAttemptBlocked:
		return true
	default:
		return false
	}
}
