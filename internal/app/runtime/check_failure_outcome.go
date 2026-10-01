// V9-02 (ADR-031) — "Outcome cho kết quả fail chức năng của COMMAND và
// MACHINE_GATE". This file holds the pieces the COMMAND and MACHINE_GATE
// executors share when their node declares a failureOutcome:
//
//   - which outcome a successful check selects, and which one a functional
//     failure selects (a check has no marker protocol, so the choice is made
//     by its own deterministic result, never by anyone proposing it);
//   - how the NEXT activation after a failing check learns about the failure:
//     when a MAKER AGENT NodeRun is activated through the edge leaving a
//     check's failureOutcome, its ContextSnapshot carries the Evidence rows of
//     that failing check attempt (gatherCheckFailureEvidenceRefs), and
//     AssembleAgentExecutionRequest renders them into the prompt as a short
//     WHAT/WHY/FIX summary (check_failure_context.go).
//
// The three result classes themselves (success / functional failure /
// technical error, never mapped into each other) are decided in each
// executor's classify, next to the code that already distinguishes timeout,
// cancellation and spawn failure; this file only supplies the vocabulary.
package runtime

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// checkFailureOutcomeOf returns node's failureOutcome ("" when the node
// declares none) after checking that it is one of the outcomes the check can
// select (allowedOutcomes: every declared outcome except the node's own
// cyclePolicy escalation outcome). A WorkflowVersion that passed
// workflow.Compile always satisfies this (validateFailureOutcome); the check
// exists for one built some other way, so the executor fails closed instead of
// routing to an outcome the node cannot take.
func checkFailureOutcomeOf(node workflow.Node, allowedOutcomes []string) (string, error) {
	failureOutcome := node.CheckFailureOutcome()
	if failureOutcome == "" {
		return "", nil
	}
	for _, outcome := range allowedOutcomes {
		if outcome == failureOutcome {
			return failureOutcome, nil
		}
	}
	return "", fmt.Errorf("runtime: node %s declares failureOutcome %q, which is not among its selectable outcomes %v", node.Key, failureOutcome, allowedOutcomes)
}

// checkSuccessOutcomes returns the outcomes a SUCCESSFUL check may select:
// allowedOutcomes without failureOutcome. For a node with no failureOutcome it
// is allowedOutcomes itself, so the existing single-outcome derivation
// (resolveSelectedOutcome) is unchanged; for one with a failureOutcome the
// workflow validation guarantees exactly one remains.
func checkSuccessOutcomes(allowedOutcomes []string, failureOutcome string) []string {
	if failureOutcome == "" {
		return allowedOutcomes
	}
	success := make([]string, 0, len(allowedOutcomes))
	for _, outcome := range allowedOutcomes {
		if outcome != failureOutcome {
			success = append(success, outcome)
		}
	}
	return success
}

// resolveCheckSuccessOutcome is resolveSelectedOutcome for a check that
// succeeded. Without a failureOutcome it is exactly resolveSelectedOutcome
// (derived from the single allowed outcome, source DERIVED_SINGLE_ALLOWED).
// With one, the success outcome is the one that remains once the
// failureOutcome is set aside, and the proposal records that it was derived
// from the check's own verdict.
func resolveCheckSuccessOutcome(allowedOutcomes []string, failureOutcome string) (string, *ports.AgentProposedOutcome, error) {
	selected, proposed, err := resolveSelectedOutcome(nil, checkSuccessOutcomes(allowedOutcomes, failureOutcome))
	if err != nil {
		return "", nil, err
	}
	if failureOutcome != "" {
		proposed.Source = ports.AgentOutcomeDerivedCheckVerdict
	}
	return selected, proposed, nil
}

// checkFailureProposal is the proposal for a functional failure that routes to
// failureOutcome. FinalizeExecutionAttempt only requires its Value to equal the
// SelectedOutcome it accompanies.
func checkFailureProposal(failureOutcome string) *ports.AgentProposedOutcome {
	return &ports.AgentProposedOutcome{Value: failureOutcome, Source: ports.AgentOutcomeDerivedCheckVerdict, SchemaVersion: 1}
}

// gatherCheckFailureEvidenceRefs resolves the EvidenceRefs of a MAKER AGENT
// NodeRun that was activated through the edge leaving a check's
// failureOutcome (ADR-031 decision 6: "Maker ở vòng sau nhận evidence của lần
// fail"): the Evidence rows of the failing check's own terminal attempt, so the
// maker can read WHY the check failed instead of the operator re-running the
// test by hand in a fresh worktree.
//
// Which check activated nodeRun is read from the run's own history rather than
// stored anywhere new. AdvanceRun gives every activation ActivationSequence
// previous+1 within a lineage, so the check NodeRun that routed here is the one
// that is
//
//   - SUCCEEDED with SelectedOutcome == its node's failureOutcome,
//   - ActivationSequence exactly one before nodeRun's,
//   - in the same lineage (same BranchTokenID, so parallel FORK branches never
//     borrow each other's failures), and
//   - whose failureOutcome edge actually leads to nodeRun's node.
//
// Anything else — the very first activation of the maker, an activation
// through another edge, a scope-expansion reactivation — yields nil, and the
// maker's snapshot is exactly what it was before V9-02. Within that NodeRun
// only the LATEST SUCCEEDED attempt counts (an earlier technical retry of the
// check left no usable verdict), mirroring gatherCheckerEvidenceRefs.
func gatherCheckFailureEvidenceRefs(
	ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, document workflow.WorkflowDocument, nodeRun runtimedomain.NodeRun,
) ([]contextsnapshot.EvidenceRef, error) {
	if nodeRun.ActivationSequence == 0 {
		return nil, nil
	}
	nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, string(run.ID))
	if err != nil {
		return nil, err
	}
	var failing *runtimedomain.NodeRun
	for i := range nodeRuns {
		candidate := nodeRuns[i]
		if candidate.State != runtimedomain.NodeRunSucceeded || candidate.ActivationSequence+1 != nodeRun.ActivationSequence {
			continue
		}
		if !sameBranchLineage(candidate.BranchTokenID, nodeRun.BranchTokenID) {
			continue
		}
		checkNode, ok := findNode(document, candidate.NodeKey)
		if !ok {
			continue
		}
		failureOutcome := checkNode.CheckFailureOutcome()
		if failureOutcome == "" || candidate.SelectedOutcome != failureOutcome {
			continue
		}
		if edge, ok := findEdge(document, checkNode.Key, failureOutcome); !ok || edge.To != nodeRun.NodeKey {
			continue
		}
		failing = &candidate
		break
	}
	if failing == nil {
		return nil, nil
	}

	attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, string(run.ID))
	if err != nil {
		return nil, err
	}
	var latest *runtimedomain.ExecutionAttempt
	for i := range attempts {
		attempt := attempts[i]
		if attempt.NodeRunID != failing.ID || attempt.State != runtimedomain.ExecutionAttemptSucceeded {
			continue
		}
		if latest == nil || attempt.AttemptNumber > latest.AttemptNumber {
			latest = &attempt
		}
	}
	if latest == nil {
		return nil, nil
	}
	evidence, err := tx.Runtime().ListEvidenceForAttempt(ctx, string(latest.ID))
	if err != nil {
		return nil, err
	}
	refs := make([]contextsnapshot.EvidenceRef, 0, len(evidence))
	for _, row := range evidence {
		refs = append(refs, contextsnapshot.EvidenceRef{EvidenceID: string(row.ID)})
	}
	return refs, nil
}

// sameBranchLineage reports whether two NodeRuns belong to the same FORK
// branch (or both to none).
func sameBranchLineage(a, b *runtimedomain.BranchTokenID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
