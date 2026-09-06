// ResolveWorkItemBlocker (V4-12C, docs/design/06-v4-runtime-engine.md,
// ADR-020) is the one public command with authority to transition a
// WorkItemBlocker OPEN -> RESOLVED|WAIVED. It does not require the blocker
// be resolved already — the real precondition is: the blocker is currently
// OPEN, no Run belonging to the WorkItem is non-terminal, and no repository
// workspace in the WorkItem's own family is QUARANTINED. A blocker already
// RESOLVED or WAIVED is a no-op, idempotent success, never an error
// (ADR-020's own "blocker đã RESOLVED|WAIVED là no-op idempotent").
//
// Mode MUST be supplied explicitly — there is no default:
//
//   - RESOLVED requires Actor and Reason (this command's own baseline);
//     valid for every blocker type ResolvableViaCommand allows.
//   - WAIVED additionally requires PolicyGrantRef (an opaque reference
//     naming the policy grant that authorizes this waiver — no policy-grant
//     registry exists yet anywhere in this codebase to validate its real
//     content against, so this is checked only for non-blank presence, the
//     same "loosely-specified vocabulary, minimal defensible reading"
//     treatment work.JoinPolicy's own doc comment already establishes for an
//     identical gap) and always records an immutable DecisionArtifact
//     (ADR-020: "WAIVED cần actor, reason, policy grant và một
//     DecisionArtifact"). WAIVED is only ever valid for a Waivable blocker
//     type (workdomain.BlockerType.Waivable) — RUN_CANCELLED and
//     COMPLETION_POLICY_FAILED, ADR-020's own closed table; every other
//     type, including all four admission reasons and
//     SCOPE_EXPANSION_REQUIRED, rejects WAIVED outright.
//
// SCOPE_EXPANSION_REQUIRED rejects BOTH modes unconditionally
// (workdomain.BlockerType.ResolvableViaCommand) — see that method's own doc
// comment for why: only the real approval/reconcile flow may ever resolve
// one.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ResolutionMode is ResolveWorkItemBlockerRequest's own required,
// no-default mode selector.
type ResolutionMode string

const (
	ResolutionModeResolved ResolutionMode = "RESOLVED"
	ResolutionModeWaived   ResolutionMode = "WAIVED"
)

// ResolveWorkItemBlockerRequest is what a caller supplies to
// ResolveWorkItemBlocker.
type ResolveWorkItemBlockerRequest struct {
	BlockerID string
	Mode      ResolutionMode
	Actor     string
	Reason    string
	// PolicyGrantRef is required exactly when Mode is WAIVED — see this
	// file's own package doc comment.
	PolicyGrantRef string
	CorrelationID  string
}

// ResolveWorkItemBlockerResult is what ResolveWorkItemBlocker returns.
type ResolveWorkItemBlockerResult struct {
	BlockerID         string
	AlreadyResolved   bool
	State             string
	WorkItemUnblocked bool
	WorkItemStatus    string
}

var (
	// ErrResolutionModeRequired is returned when Mode is neither RESOLVED
	// nor WAIVED — "Payload MUST chọn resolution mode tường minh, không có
	// mặc định" (this task's own Thực hiện line).
	ErrResolutionModeRequired = errors.New("runtime: resolution Mode must be RESOLVED or WAIVED")
	// ErrBlockerNotResolvableViaCommand is returned for a blocker type this
	// command has no authority over at all (SCOPE_EXPANSION_REQUIRED).
	ErrBlockerNotResolvableViaCommand = errors.New("runtime: this blocker type can only be resolved through its own owning flow")
	// ErrBlockerNotWaivable is returned when Mode is WAIVED for a blocker
	// type ADR-020's own table never allows to be waived.
	ErrBlockerNotWaivable = errors.New("runtime: this blocker type can never be waived")
	// ErrWaiveRequiresPolicyGrant is returned when Mode is WAIVED and
	// PolicyGrantRef is blank.
	ErrWaiveRequiresPolicyGrant = errors.New("runtime: WAIVED requires Actor, Reason and PolicyGrantRef")
	// ErrWorkItemHasNonTerminalRun is returned when the WorkItem still has a
	// non-terminal WorkflowRun — resolving a blocker must never race ahead
	// of a Run that could still fail/cancel/complete and change the picture.
	ErrWorkItemHasNonTerminalRun = errors.New("runtime: work item has a non-terminal workflow run")
	// ErrWorkspaceQuarantined is returned when the WorkItem's own family has
	// a QUARANTINED repository workspace (V3-10) — an operator must
	// reconcile that first; resolving a blocker must never paper over a
	// workspace integrity problem.
	ErrWorkspaceQuarantined = errors.New("runtime: work item's family has a quarantined repository workspace")
)

// workItemBlockerWaiverInput/workItemBlockerWaiverResult are the WAIVED
// DecisionArtifact's own JSON shapes (V4-12C).
type workItemBlockerWaiverInput struct {
	BlockerID   string `json:"blockerId"`
	BlockerType string `json:"blockerType"`
	Actor       string `json:"actor"`
	Reason      string `json:"reason"`
}

type workItemBlockerWaiverResult struct {
	Mode string `json:"mode"`
}

// ResolveWorkItemBlocker implements ADR-020's own blocker-resolution entry
// point. See this file's own package doc comment for the full contract. It
// takes no idsource.Source — unlike CancelRun/CancelWorkItem, this command
// mints no new top-level ID: BlockerID is caller-supplied and a WAIVED
// resolution's own DecisionArtifact ID is deterministic from it (the same
// "no ids parameter unless the command actually mints one" precedent
// internal/app/work.RejectScopeExpansion/WithdrawScopeExpansion already
// establish next to RequestScopeExpansion/ApproveScopeExpansion, which do).
func ResolveWorkItemBlocker(ctx context.Context, uow ports.UnitOfWork, req ResolveWorkItemBlockerRequest) (ResolveWorkItemBlockerResult, error) {
	blockerID := strings.TrimSpace(req.BlockerID)
	actor := strings.TrimSpace(req.Actor)
	reason := strings.TrimSpace(req.Reason)
	if blockerID == "" {
		return ResolveWorkItemBlockerResult{}, errors.New("runtime: BlockerID is required")
	}
	if req.Mode != ResolutionModeResolved && req.Mode != ResolutionModeWaived {
		return ResolveWorkItemBlockerResult{}, ErrResolutionModeRequired
	}
	if actor == "" {
		return ResolveWorkItemBlockerResult{}, errors.New("runtime: Actor is required")
	}
	if reason == "" {
		return ResolveWorkItemBlockerResult{}, errors.New("runtime: Reason is required")
	}
	policyGrantRef := strings.TrimSpace(req.PolicyGrantRef)
	if req.Mode == ResolutionModeWaived && policyGrantRef == "" {
		return ResolveWorkItemBlockerResult{}, ErrWaiveRequiresPolicyGrant
	}

	var result ResolveWorkItemBlockerResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		blocker, err := tx.Work().GetWorkItemBlocker(ctx, blockerID)
		if err != nil {
			return err
		}

		if blocker.State != workdomain.BlockerOpen {
			item, err := tx.Work().GetWorkItem(ctx, string(blocker.WorkItemID))
			if err != nil {
				return err
			}
			result = ResolveWorkItemBlockerResult{
				BlockerID: blockerID, AlreadyResolved: true, State: string(blocker.State), WorkItemStatus: string(item.Status),
			}
			return nil
		}

		if !blocker.Type.ResolvableViaCommand() {
			return fmt.Errorf("%w: blocker type %s", ErrBlockerNotResolvableViaCommand, blocker.Type)
		}

		var nextState workdomain.BlockerState
		var decisionArtifactID string
		switch req.Mode {
		case ResolutionModeResolved:
			nextState = workdomain.BlockerResolved
		case ResolutionModeWaived:
			if !blocker.Type.Waivable() {
				return fmt.Errorf("%w: blocker type %s", ErrBlockerNotWaivable, blocker.Type)
			}
			nextState = workdomain.BlockerWaived
			decisionArtifactID = blockerID + "-waiver"
			inputJSON, err := json.Marshal(workItemBlockerWaiverInput{
				BlockerID: blockerID, BlockerType: string(blocker.Type), Actor: actor, Reason: reason,
			})
			if err != nil {
				return fmt.Errorf("marshal work item blocker waiver decision input: %w", err)
			}
			resultJSON, err := json.Marshal(workItemBlockerWaiverResult{Mode: string(workdomain.BlockerWaived)})
			if err != nil {
				return fmt.Errorf("marshal work item blocker waiver decision result: %w", err)
			}
			artifact, err := runtimedomain.NewDecisionArtifact(
				runtimedomain.DecisionArtifactID(decisionArtifactID), blocker.ProjectID, "WorkItemBlockerWaiver",
				policyGrantRef, inputJSON, resultJSON, time.Now().UTC(),
			)
			if err != nil {
				return err
			}
			if _, err := tx.Runtime().RecordDecisionArtifact(ctx, artifact); err != nil {
				return err
			}
		}

		// Precondition: no non-terminal Run for this WorkItem.
		runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, string(blocker.WorkItemID))
		if err != nil {
			return err
		}
		for _, run := range runs {
			if !isWorkflowRunTerminal(run.State) {
				return fmt.Errorf("%w: work item %s run %s is %s", ErrWorkItemHasNonTerminalRun, blocker.WorkItemID, run.ID, run.State)
			}
		}

		// Precondition: no QUARANTINED repository workspace in this
		// WorkItem's own family.
		item, err := tx.Work().GetWorkItem(ctx, string(blocker.WorkItemID))
		if err != nil {
			return err
		}
		family, err := tx.Work().GetTaskFamily(ctx, string(item.FamilyID))
		if err != nil {
			return err
		}
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, string(family.ID))
		if err != nil {
			return err
		}
		repoWorkspaces, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, string(set.ID))
		if err != nil {
			return err
		}
		for _, rw := range repoWorkspaces {
			if rw.State == workspace.RepositoryWorkspaceQuarantined {
				return fmt.Errorf("%w: repository workspace %s", ErrWorkspaceQuarantined, rw.ID)
			}
		}

		closed, err := closeWorkItemBlockerTx(
			ctx, tx, blocker, nextState, actor, reason, decisionArtifactID, workdomain.WorkItemReady, req.CorrelationID, "",
		)
		if err != nil {
			return err
		}

		finalItem, err := tx.Work().GetWorkItem(ctx, string(blocker.WorkItemID))
		if err != nil {
			return err
		}
		result = ResolveWorkItemBlockerResult{
			BlockerID: blockerID, State: string(closed.Blocker.State), WorkItemUnblocked: closed.Unblocked,
			WorkItemStatus: string(finalItem.Status),
		}
		return nil
	})
	if err != nil {
		return ResolveWorkItemBlockerResult{}, err
	}
	return result, nil
}
