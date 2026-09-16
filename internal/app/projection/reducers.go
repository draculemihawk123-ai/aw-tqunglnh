package projection

import "encoding/json"

// This file's own local payload structs are DELIBERATE duplicates of each
// source package's own unexported eventXxxPayload struct (internal/app/work,
// internal/app/runtime) — never a cross-package import of an unexported
// type (not possible in Go) and never an exported alias added to the
// source package on this task's behalf (V6-08's own "Không làm" line does
// not touch those packages at all). This mirrors an already-established
// idiom in this exact codebase: cmd/aw/definition.go's own
// workflowVersionFields duplicates two OTHER packages' own unexported
// conversion logic for the identical reason, its own doc comment states
// outright. Only the fields this package's own Reducers actually read are
// declared — decoding into a narrower local struct silently ignores any
// JSON field this package does not need, exactly like every decoder in
// internal/app/eventschema already behaves for a v1 payload it decodes.

type rootWorkItemCreatedPayload struct {
	WorkItemID     string `json:"workItemId"`
	ProjectID      string `json:"projectId"`
	FamilyID       string `json:"familyId"`
	WorkspaceSetID string `json:"workspaceSetId"`
	Title          string `json:"title"`
}

type childWorkItemCreatedPayload struct {
	WorkItemID       string `json:"workItemId"`
	ProjectID        string `json:"projectId"`
	FamilyID         string `json:"familyId"`
	ParentWorkItemID string `json:"parentWorkItemId"`
	Title            string `json:"title"`
}

type workItemMarkedReadyPayload struct {
	WorkItemID string `json:"workItemId"`
}

type scopeExpansionRequestedPayload struct {
	FamilyID             string `json:"familyId"`
	ReferencedWorkItemID string `json:"referencedWorkItemId"`
}

type scopeExpansionApprovedPayload struct {
	FamilyID             string `json:"familyId"`
	ReferencedWorkItemID string `json:"referencedWorkItemId"`
}

type scopeExpansionRejectedPayload struct {
	FamilyID string `json:"familyId"`
}

type scopeExpansionWithdrawnPayload struct {
	FamilyID string `json:"familyId"`
}

type workflowRunStartedPayload struct {
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
}

type runCancellationRequestedPayload struct {
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
}

type runCompletionRequestedPayload struct {
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
}

type runFailedPayload struct {
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
}

type runCancelledPayload struct {
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
}

type workflowRunFinalizedPayload struct {
	RunID         string `json:"runId"`
	TerminalState string `json:"terminalState"`
}

type workItemBlockedPayload struct {
	WorkItemID  string `json:"workItemId"`
	BlockerType string `json:"blockerType"`
}

type workItemBlockerResolvedPayload struct {
	WorkItemID        string `json:"workItemId"`
	WorkItemUnblocked bool   `json:"workItemUnblocked"`
	NewWorkItemStatus string `json:"newWorkItemStatus"`
}

type workItemCancelledPayload struct {
	WorkItemID string `json:"workItemId"`
}

func decode[T any](payloadJSON string) (T, error) {
	var value T
	err := json.Unmarshal([]byte(payloadJSON), &value)
	return value, err
}

// reduceRootWorkItemCreated is RootWorkItemCreated v1's own Reducer
// (handlerVersion 1): the ONLY reducer (along with
// reduceChildWorkItemCreated) that ever sets Status from empty. Every
// other Reducer in this file assumes prior.Exists() is already true —
// applying one of them to a row that does not exist yet would be a real
// bug in the caller's own apply ordering (V6-08A's own job to never let
// happen), not a case this package silently tolerates.
func reduceRootWorkItemCreated(_ WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[rootWorkItemCreatedPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	return WorkItemCardRow{
		WorkItemID: p.WorkItemID, ProjectID: p.ProjectID, FamilyID: p.FamilyID,
		Title: p.Title, IsRoot: true, WorkspaceSetID: p.WorkspaceSetID, Status: statusBacklog,
	}, nil
}

// reduceChildWorkItemCreated is ChildWorkItemCreated v1's own Reducer
// (handlerVersion 1).
func reduceChildWorkItemCreated(_ WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[childWorkItemCreatedPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	return WorkItemCardRow{
		WorkItemID: p.WorkItemID, ProjectID: p.ProjectID, FamilyID: p.FamilyID,
		Title: p.Title, ParentWorkItemID: p.ParentWorkItemID, IsRoot: false, Status: statusBacklog,
	}, nil
}

// reduceWorkItemMarkedReady is WORK_ITEM_MARKED_READY v1's own Reducer
// (handlerVersion 1): BACKLOG -> READY. Guarded by isTerminal (and, more
// narrowly, only fires from BACKLOG) since MarkWorkItemReady's own
// authoritative precondition already enforces this is only ever emitted
// for a real BACKLOG->READY transition — this Reducer trusts that
// precondition rather than re-deriving it, exactly like the AK-ARCH-026/
// ADR-025 discipline every other event-sourced reducer in this codebase
// follows (a persisted event is always already-valid history).
func reduceWorkItemMarkedReady(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	if _, err := decode[workItemMarkedReadyPayload](payloadJSON); err != nil {
		return WorkItemCardRow{}, err
	}
	if prior.isTerminal() {
		return prior, nil
	}
	prior.Status = statusReady
	return prior, nil
}

// reduceScopeExpansionRequested is ScopeExpansionRequested v1's own
// Reducer (handlerVersion 1).
func reduceScopeExpansionRequested(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	if _, err := decode[scopeExpansionRequestedPayload](payloadJSON); err != nil {
		return WorkItemCardRow{}, err
	}
	prior.PendingScopeExpansionCount++
	return prior, nil
}

// reduceScopeExpansionApproved is ScopeExpansionApproved v1's own Reducer
// (handlerVersion 1).
func reduceScopeExpansionApproved(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	if _, err := decode[scopeExpansionApprovedPayload](payloadJSON); err != nil {
		return WorkItemCardRow{}, err
	}
	return decrementPendingScopeExpansion(prior), nil
}

// reduceScopeExpansionRejected is ScopeExpansionRejected v1's own Reducer
// (handlerVersion 1).
func reduceScopeExpansionRejected(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	if _, err := decode[scopeExpansionRejectedPayload](payloadJSON); err != nil {
		return WorkItemCardRow{}, err
	}
	return decrementPendingScopeExpansion(prior), nil
}

// reduceScopeExpansionWithdrawn is ScopeExpansionWithdrawn v1's own Reducer
// (handlerVersion 1).
func reduceScopeExpansionWithdrawn(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	if _, err := decode[scopeExpansionWithdrawnPayload](payloadJSON); err != nil {
		return WorkItemCardRow{}, err
	}
	return decrementPendingScopeExpansion(prior), nil
}

func decrementPendingScopeExpansion(row WorkItemCardRow) WorkItemCardRow {
	if row.PendingScopeExpansionCount > 0 {
		row.PendingScopeExpansionCount--
	}
	return row
}

// reduceWorkflowRunStarted is WorkflowRunStarted v1's own Reducer
// (handlerVersion 1): READY/BACKLOG -> ACTIVE, records the new active Run.
func reduceWorkflowRunStarted(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[workflowRunStartedPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	if !prior.isTerminal() {
		prior.Status = statusActive
	}
	prior.ActiveRunID = p.RunID
	prior.ActiveRunStatus = runStatusActive
	return prior, nil
}

// reduceRunCancellationRequested is RUN_CANCELLATION_REQUESTED v1's own
// Reducer (handlerVersion 1) — a transient "cancelling" badge on the
// currently active Run; never itself moves Status (Screen 5's own spec has
// no "cancelling" column, only the terminal CANCELLED one WORK_ITEM_CANCELLED
// or a resolved RUN_CANCELLED drives).
func reduceRunCancellationRequested(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[runCancellationRequestedPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	if prior.ActiveRunID == p.RunID {
		prior.ActiveRunStatus = runStatusCancelling
	}
	return prior, nil
}

// reduceRunCompletionRequested is RUN_COMPLETION_REQUESTED v1's own
// Reducer (handlerVersion 1) — a transient "completing" badge; the
// authoritative terminal outcome (success -> DONE) arrives later via
// WORKFLOW_RUN_FINALIZED (see reduceWorkflowRunFinalized's own doc
// comment for why no bare "run completed successfully" event exists on
// its own).
func reduceRunCompletionRequested(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[runCompletionRequestedPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	if prior.ActiveRunID == p.RunID {
		prior.ActiveRunStatus = runStatusCompleting
	}
	return prior, nil
}

// reduceRunFailed is RUN_FAILED v1's own Reducer (handlerVersion 1). Maps
// to Status=BLOCKED: work.WorkItemStatus is a closed six-value enum with
// no dedicated "FAILED" member, so a Run-level failure that needs human
// attention surfaces on the SAME column WORK_ITEM_BLOCKED already owns —
// ActiveRunStatus still distinguishes "failed" from a real blocker badge
// for the operator (TopBlockerType stays whatever it already was; a Run
// failure is not itself a WorkItemBlocker record).
func reduceRunFailed(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[runFailedPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	if !prior.isTerminal() {
		prior.Status = statusBlocked
	}
	if prior.ActiveRunID == p.RunID {
		prior.ActiveRunStatus = runStatusFailed
	}
	return prior, nil
}

// reduceRunCancelled is RUN_CANCELLED v1's own Reducer (handlerVersion 1):
// a Run-level cancel (CancelRun) is distinct from a WorkItem-level cancel
// (CancelWorkItem, WORK_ITEM_CANCELLED) — Screen 7's own spec keeps these
// as two separate buttons/authorities. This Reducer only clears the active
// Run and, if the WorkItem itself is not otherwise terminal, returns the
// card to READY (its own Run is gone, but the WorkItem is still valid and
// startable again) — never CANCELLED, which stays WORK_ITEM_CANCELLED's
// own exclusive authority to set.
func reduceRunCancelled(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[runCancelledPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	if prior.ActiveRunID != p.RunID {
		return prior, nil
	}
	prior.ActiveRunID = ""
	prior.ActiveRunStatus = runStatusCancelled
	if prior.Status == statusActive {
		prior.Status = statusReady
	}
	return prior, nil
}

// reduceWorkflowRunFinalized is WORKFLOW_RUN_FINALIZED v1's own Reducer
// (handlerVersion 1) — the durable job-lease finalize confirmation
// (internal/adapters/sqlite/workflow_store.go's own FinalizeWorkflowRun)
// and, critically, the ONLY event in this codebase that ever reports a
// SUCCESSFUL Run completion (there is no standalone "RunCompleted" event —
// RUN_COMPLETION_REQUESTED is only the two-phase intent). TerminalState is
// mapped onto WorkItemStatus's own closed enum: COMPLETED -> DONE,
// CANCELLED -> stays whatever RUN_CANCELLED/WORK_ITEM_CANCELLED already
// resolved (never regresses a card OUT of CANCELLED, matching this event's
// own late/echo-like arrival relative to those), anything else (FAILED and
// any value this Reducer does not specifically recognize) -> BLOCKED, the
// same "no dedicated FAILED status" reasoning reduceRunFailed already uses.
func reduceWorkflowRunFinalized(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[workflowRunFinalizedPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	if prior.ActiveRunID == p.RunID {
		prior.ActiveRunID = ""
	}
	if prior.isTerminal() {
		return prior, nil
	}
	switch p.TerminalState {
	case "COMPLETED":
		prior.Status = statusDone
		prior.ActiveRunStatus = ""
	case "CANCELLED":
		prior.Status = statusCancelled
		prior.ActiveRunStatus = ""
	default:
		prior.Status = statusBlocked
		prior.ActiveRunStatus = runStatusFailed
	}
	return prior, nil
}

// reduceWorkItemBlocked is WORK_ITEM_BLOCKED v1's own Reducer
// (handlerVersion 1).
func reduceWorkItemBlocked(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[workItemBlockedPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	if !prior.isTerminal() {
		prior.Status = statusBlocked
	}
	prior.BlockerCount++
	prior.TopBlockerType = p.BlockerType
	return prior, nil
}

// reduceWorkItemBlockerResolved is WORK_ITEM_BLOCKER_RESOLVED v1's own
// Reducer (handlerVersion 1). Trusts the event's own NewWorkItemStatus
// when WorkItemUnblocked is true — the authoritative blocker-resolution
// handler (internal/app/runtime/blocker.go) has already computed the
// correct post-unblock status with full context this projection does not
// have (e.g. whether a Run is still active); re-deriving it here from
// BlockerCount alone would risk disagreeing with that authority, exactly
// what contract rule 5 ("Projection không là authority") forbids.
func reduceWorkItemBlockerResolved(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	p, err := decode[workItemBlockerResolvedPayload](payloadJSON)
	if err != nil {
		return WorkItemCardRow{}, err
	}
	if prior.BlockerCount > 0 {
		prior.BlockerCount--
	}
	if prior.BlockerCount == 0 {
		prior.TopBlockerType = ""
	}
	if p.WorkItemUnblocked && p.NewWorkItemStatus != "" && !prior.isTerminal() {
		prior.Status = p.NewWorkItemStatus
	}
	return prior, nil
}

// reduceWorkItemCancelled is WORK_ITEM_CANCELLED v1's own Reducer
// (handlerVersion 1) — the WorkItem-level cancellation authority
// (CancelWorkItem, distinct from a Run-level RUN_CANCELLED). Always wins:
// CANCELLED is terminal, and this is the one event allowed to SET it (not
// just guarded against regressing out of it).
func reduceWorkItemCancelled(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error) {
	if _, err := decode[workItemCancelledPayload](payloadJSON); err != nil {
		return WorkItemCardRow{}, err
	}
	prior.Status = statusCancelled
	prior.ActiveRunID = ""
	prior.ActiveRunStatus = ""
	return prior, nil
}
