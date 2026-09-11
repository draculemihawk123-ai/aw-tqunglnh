// V5-11 PR1: CompletionPolicy service — the authority reconcileRunTerminalityTx's
// own doc comment (completion.go) already named as deferred ("WorkItem chỉ
// giữ ACTIVE/BLOCKED cho tới verification service V5"). A WorkflowRun sitting
// in VERIFYING is a completion CANDIDATE (ADR-011); only EvaluateCompletionCandidate
// below may ever move it to SUCCEEDED, BLOCKED or FAILED, and only it may ever
// move the owning WorkItem to DONE or BLOCKED from here.
//
// Scope locked with the user before writing this file (2026-09-10, two
// contracts given verbatim in chat, full text preserved in
// baocaov5checklist.md's own "V5-11" sections — re-read those before
// changing this file's own behavior, this comment only summarizes):
//
//  1. CompletionPolicyVersion is pinned at workflow.WorkflowDocument's own
//     root (CompletionPolicyRef, V5-11 PR0) — resolved against the Run's own
//     immutable ExecutionManifest, never re-trusted from a bare re-read of
//     the WorkflowVersion.
//  2. For one completion candidate (RunID, EndNodeRunID), exactly one
//     immutable DecisionArtifact may ever commit, and every effect of that
//     decision commits in the SAME transaction or none of them do. Replay
//     check happens BEFORE revalidating Run state (a successful first call
//     already changed that state) — a decisionArtifactID recomputed from the
//     exact same candidate always finds the exact same row, and this
//     function returns its stored result rather than re-deciding. Every
//     derived ID (the DecisionArtifact itself, its event, the FAIL blocker)
//     is a sha256 content hash, never a plain string concatenation —
//     deterministicJoinNodeRunID (advance.go) is the precedent this mirrors.
//
// PR1 covered PASS, BLOCK and FAIL. PR2 (this update) adds REWORK: when the
// ladder is unsatisfied AND the reached END node has a published
// COMPLETION_REWORK edge (V5-10B) whose own ReworkPolicy budget is not yet
// exhausted, the candidate resolves to REWORK instead of BLOCK — Run
// VERIFYING->RUNNING, WorkItem left untouched (still ACTIVE), and exactly
// one new NodeRun activation created for the edge's own target node, all in
// the same transaction as everything else. Getting that new NodeRun
// actually SCHEDULED (execution-profile resolution, EXECUTE_NODE job
// enqueue via ScheduleExecutableNodeRun) is deliberately NOT done here — it
// is created PENDING, exactly the same state advanceRunTx itself already
// leaves every freshly-created executable-node NodeRun in for an ordinary
// hop (see that function's own body: none of autoAdvance/isEndNode/
// isWaitNode/isApprovalNode/isForkNode apply to a plain executable node, so
// it is never scheduled inline either) — production wiring that calls
// ScheduleExecutableNodeRun after a hop is a pre-existing, already-accepted
// gap this codebase has had since V4, not something REWORK needs to solve
// itself.
package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// CompletionOutcome is ADR-021's own closed four-outcome vocabulary.
type CompletionOutcome string

const (
	CompletionOutcomePass   CompletionOutcome = "PASS"
	CompletionOutcomeRework CompletionOutcome = "REWORK"
	CompletionOutcomeBlock  CompletionOutcome = "BLOCK"
	CompletionOutcomeFail   CompletionOutcome = "FAIL"
)

// CompletionDecisionKind is the DecisionArtifact.Kind every CompletionPolicy
// evaluation produces (contract 2, 2026-09-10) — "COMPLETION_DECISION_V1" is
// the exact name that contract itself uses throughout.
const CompletionDecisionKind = "COMPLETION_DECISION_V1"

// Typed BLOCK/FAIL reasons this evaluator itself produces. Free-text detail
// (which evidence kind, which role, which sibling run) is appended after
// these in the actual Reason string this package returns — these constants
// exist so a future caller can pattern-match the stable prefix without
// parsing prose.
const (
	ReasonWorkItemRunStateInconsistent = "WORK_ITEM_RUN_STATE_INCONSISTENT"
	ReasonReleaseSetNotSealed          = "RELEASE_SET_NOT_SEALED"
	ReasonCompletionRequirementsUnmet  = "COMPLETION_REQUIREMENTS_UNMET"
	ReasonNoCompletionPolicyPinned     = "NO_COMPLETION_POLICY_PINNED"
	ReasonCompletionPolicyUnresolvable = "COMPLETION_POLICY_UNRESOLVABLE"
	// ReasonReworkBudgetExhausted is BLOCK's own reason (2026-09-10, PR2)
	// when a COMPLETION_REWORK edge exists for the reached END but its own
	// ReworkPolicy.MaxIterations budget is already spent — ADR-021's own
	// "REWORK không có rework edge hợp lệ -> BLOCK" fallback, extended the
	// one way that ADR itself anticipates (GC-INV-29): a budget-exhausted
	// edge is treated identically to no edge at all.
	ReasonReworkBudgetExhausted = "REWORK_BUDGET_EXHAUSTED"
)

// ErrNoCompletionCandidate is returned when RunID has not actually reached
// an END node yet — EvaluateCompletionCandidate is never the authority that
// discovers a completion candidate, only the one that decides one already
// recorded (summary.ReachedEndNodeRunID, the same signal
// transitionRunToVerifyingTx itself already required to move the Run to
// VERIFYING in the first place).
var ErrNoCompletionCandidate = errors.New("runtime: workflow run has not reached a completion candidate")

// ErrRunNotVerifying is returned when RunID is not (or no longer) VERIFYING
// at evaluation time and this is not a replay of an already-decided
// candidate (see this file's own package doc comment on replay-first
// ordering).
var ErrRunNotVerifying = errors.New("runtime: workflow run is not VERIFYING")

// ErrCompletionDecisionConflict is returned when a DecisionArtifact already
// exists for this exact (RunID, EndNodeRunID) candidate but its own
// recorded Input differs from what this call just recomputed — the
// candidate's own durable inputs (evidence, approvals, policy pin) somehow
// changed between two evaluations of what should be the identical
// candidate. This is a real conflict, never silently resolved either way.
var ErrCompletionDecisionConflict = errors.New("runtime: a completion decision already exists for this candidate with different inputs")

// EvaluateCompletionCandidateRequest identifies which completion candidate
// to decide. Per this task's own design doc ("completion command chỉ nhận
// identity/expected-version guard"), everything else the service needs —
// the pinned CompletionPolicy, evidence, approvals, ReleaseSet — is loaded
// internally; the caller never supplies or overrides any of it.
// ExpectedVersion is cmd.ExpectedVersion (the WorkflowRun version the
// caller observed), not a separate field — the same convention every other
// fenced command in this codebase already follows (e.g.
// internal/app/work.CreateReleaseSet's own use of cmd.ExpectedVersion).
type EvaluateCompletionCandidateRequest struct {
	RunID string
}

// CompletionDecisionResult is both EvaluateCompletionCandidate's own return
// value and the exact JSON shape persisted as the DecisionArtifact's own
// Result — a replay decodes this same struct back out of that stored JSON.
type CompletionDecisionResult struct {
	DecisionArtifactID string            `json:"decisionArtifactId"`
	Outcome            CompletionOutcome `json:"outcome"`
	Reason             string            `json:"reason,omitempty"`
	RunID              string            `json:"runId"`
	WorkItemID         string            `json:"workItemId"`
	EndNodeRunID       string            `json:"endNodeRunId"`
	// ReworkNodeRunID/ReworkNodeKey are populated only when Outcome ==
	// REWORK — the freshly created NodeRun this decision activated on the
	// published COMPLETION_REWORK edge's own target.
	ReworkNodeRunID string `json:"reworkNodeRunId,omitempty"`
	ReworkNodeKey   string `json:"reworkNodeKey,omitempty"`
}

// completionCandidateInput is the deterministic snapshot of everything this
// evaluation actually considered — marshaled once and used two ways: (1) as
// DecisionArtifact.Input, satisfying ADR-021's "persist ... input evidence
// và exact RevisionSet/ReleaseSet đã xét"; (2) as the replay-comparison
// value itself (contract 2: same candidate ID + matching Input -> replay
// the stored Result; same ID + different Input -> ErrCompletionDecisionConflict).
// Every slice is sorted before marshaling so two evaluations of a truly
// unchanged candidate always produce byte-identical JSON.
type completionCandidateInput struct {
	RunID                string   `json:"runId"`
	EndNodeRunID         string   `json:"endNodeRunId"`
	WorkflowVersionID    string   `json:"workflowVersionId"`
	CompiledSnapshotHash string   `json:"compiledSnapshotHash"`
	CompletionPolicyRef  *string  `json:"completionPolicyRef,omitempty"`
	EvidenceIDs          []string `json:"evidenceIds"`
	ApprovalRequestIDs   []string `json:"approvalRequestIds"`
	ReleaseSetID         string   `json:"releaseSetId,omitempty"`
}

// EvaluateCompletionCandidate is the ONLY entry point that may ever move a
// VERIFYING WorkflowRun to SUCCEEDED/BLOCKED/FAILED, or the owning WorkItem
// to DONE/BLOCKED (ADR-011, ADR-021). See this file's own package doc
// comment for the full contract this implements.
func EvaluateCompletionCandidate(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock, cmd ports.Command, req EvaluateCompletionCandidateRequest,
) (CompletionDecisionResult, error) {
	if strings.TrimSpace(req.RunID) == "" {
		return CompletionDecisionResult{}, errors.New("runtime: EvaluateCompletionCandidate requires RunID")
	}
	if cmd.ExpectedVersion == 0 {
		return CompletionDecisionResult{}, errors.New("runtime: EvaluateCompletionCandidate requires cmd.ExpectedVersion (the WorkflowRun version the caller observed)")
	}

	var result CompletionDecisionResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		existingReceipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existingReceipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(existingReceipt.ResultJSON), &result)
		}

		decided, err := evaluateCompletionCandidateTx(ctx, tx, ids, clk, cmd, req)
		if err != nil {
			return err
		}
		result = decided

		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal completion decision receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON), CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}

func evaluateCompletionCandidateTx(
	ctx context.Context, tx ports.Tx, ids idsource.Source, clk clock.Clock, cmd ports.Command, req EvaluateCompletionCandidateRequest,
) (CompletionDecisionResult, error) {
	run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
	if err != nil {
		return CompletionDecisionResult{}, err
	}
	manifest, err := tx.Runtime().GetExecutionManifest(ctx, req.RunID)
	if err != nil {
		return CompletionDecisionResult{}, err
	}
	version, err := tx.Definitions().GetWorkflowVersion(ctx, string(manifest.WorkflowVersionID))
	if err != nil {
		return CompletionDecisionResult{}, err
	}
	document := version.Document()

	summary, err := computeRunNodeStateSummary(ctx, tx, req.RunID, document)
	if err != nil {
		return CompletionDecisionResult{}, err
	}
	if summary.ReachedEndNodeRunID == "" {
		return CompletionDecisionResult{}, ErrNoCompletionCandidate
	}

	decisionArtifactID := deterministicCompletionDecisionID(req.RunID, summary.ReachedEndNodeRunID)

	nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, req.RunID)
	if err != nil {
		return CompletionDecisionResult{}, err
	}
	evidence, approvals, err := gatherCompletionCandidateEvidence(ctx, tx, req.RunID, nodeRuns)
	if err != nil {
		return CompletionDecisionResult{}, err
	}
	releaseGate, err := loadLatestReleaseSetGate(ctx, tx, string(run.FamilyID))
	if err != nil {
		return CompletionDecisionResult{}, err
	}

	candidateInput := buildCompletionCandidateInput(manifest, document, summary, evidence, approvals, releaseGate.releaseSetID)
	inputJSON, err := json.Marshal(candidateInput)
	if err != nil {
		return CompletionDecisionResult{}, fmt.Errorf("marshal completion candidate input: %w", err)
	}

	// Replay check FIRST — before revalidating Run state below, since a
	// successful first call already changed that state (contract 2).
	existing, err := tx.Runtime().GetDecisionArtifact(ctx, string(decisionArtifactID))
	switch {
	case err == nil:
		if !bytes.Equal([]byte(existing.Input), inputJSON) {
			return CompletionDecisionResult{}, ErrCompletionDecisionConflict
		}
		var replayed CompletionDecisionResult
		if err := json.Unmarshal(existing.Result, &replayed); err != nil {
			return CompletionDecisionResult{}, fmt.Errorf("decode replayed completion decision result: %w", err)
		}
		return replayed, nil
	case errors.Is(err, ports.ErrPersistenceNotFound):
		// Fresh candidate — continue to a real evaluation below.
	default:
		return CompletionDecisionResult{}, err
	}

	if run.State != runtimedomain.WorkflowRunVerifying {
		return CompletionDecisionResult{}, ErrRunNotVerifying
	}
	if run.Version != cmd.ExpectedVersion {
		return CompletionDecisionResult{}, ports.ErrOptimisticConflict
	}

	workItem, err := tx.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		return CompletionDecisionResult{}, err
	}

	outcome, reason, plan, err := decideCompletionOutcome(ctx, tx, run, manifest, document, summary, nodeRuns, evidence, approvals, releaseGate)
	if err != nil {
		return CompletionDecisionResult{}, err
	}

	applied, err := applyCompletionOutcomeTx(ctx, tx, ids, run, workItem, outcome, reason, decisionArtifactID, plan, cmd.CorrelationID)
	if err != nil {
		return CompletionDecisionResult{}, err
	}

	result := CompletionDecisionResult{
		DecisionArtifactID: string(decisionArtifactID), Outcome: outcome, Reason: reason,
		RunID: req.RunID, WorkItemID: string(run.WorkItemID), EndNodeRunID: summary.ReachedEndNodeRunID,
		ReworkNodeRunID: applied.reworkNodeRunID, ReworkNodeKey: applied.reworkNodeKey,
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return CompletionDecisionResult{}, fmt.Errorf("marshal completion decision result: %w", err)
	}

	policyVersionID := "none"
	if document.CompletionPolicyRef != nil {
		policyVersionID = document.CompletionPolicyRef.VersionID
	}
	artifact, err := runtimedomain.NewDecisionArtifact(
		decisionArtifactID, run.ProjectID, CompletionDecisionKind, policyVersionID, inputJSON, resultJSON, clk.Now(),
	)
	if err != nil {
		return CompletionDecisionResult{}, err
	}
	if _, err := tx.Runtime().RecordDecisionArtifact(ctx, artifact); err != nil {
		return CompletionDecisionResult{}, err
	}

	if err := appendCompletionDecidedEvent(ctx, tx, applied.run, decisionArtifactID, result, cmd.CorrelationID); err != nil {
		return CompletionDecisionResult{}, err
	}
	return result, nil
}

// isNonTerminalRunState reports whether state means "something could still
// be actively happening" for this WorkflowRun — the sibling-run invariant
// check below (join contract, 2026-09-10) treats only these as blocking;
// SUCCEEDED/FAILED/CANCELLED are obviously done, and BLOCKED is treated as
// "not actively progressing" too — a sibling Run this task's own
// CompletionPolicy (or FAIL's own blocker) already stopped can never
// itself race this evaluation.
func isNonTerminalRunState(state runtimedomain.WorkflowRunState) bool {
	switch state {
	case runtimedomain.WorkflowRunRunning, runtimedomain.WorkflowRunWaiting,
		runtimedomain.WorkflowRunVerifying, runtimedomain.WorkflowRunCancelling:
		return true
	default:
		return false
	}
}

// releaseSetGate is loadLatestReleaseSetGate's own result — see that
// function's doc comment for what each field means.
type releaseSetGate struct {
	releaseSetID string
	satisfied    bool
	detail       string
}

// loadLatestReleaseSetGate loads every ReleaseSet for familyID and reports
// release-readiness for completion purposes. A family with NO ReleaseSet at
// all is treated as release-gating not applicable — a deliberate PR1 scope
// decision (not something contract 2 spelled out explicitly): a workflow
// that never did any mutating COMMAND work would otherwise be forced
// through V5-10A's own ReleaseSet flow just to ever complete, which nothing
// in ADR-021/contract 2 actually requires. When ReleaseSets DO exist, only
// the LATEST one (by creation order — mirrors
// internal/app/work.EligibilityAuthority.IsReleaseAuthorized's own indexing
// convention) matters, and it must be SEALED; ABANDONED or still CREATED
// both fail this gate.
func loadLatestReleaseSetGate(ctx context.Context, tx ports.Tx, familyID string) (releaseSetGate, error) {
	releaseSets, err := tx.Work().ListReleaseSetsForFamily(ctx, familyID)
	if err != nil {
		return releaseSetGate{}, err
	}
	if len(releaseSets) == 0 {
		return releaseSetGate{satisfied: true}, nil
	}
	latest := releaseSets[len(releaseSets)-1]
	if latest.State != workdomain.ReleaseSetSealed {
		return releaseSetGate{detail: fmt.Sprintf(
			"release set %s for task family %s is %s, not SEALED", latest.ID, familyID, latest.State,
		)}, nil
	}
	return releaseSetGate{releaseSetID: string(latest.ID), satisfied: true}, nil
}

// reworkPlan is decideCompletionOutcome's own output when it resolves to
// REWORK — everything applyCompletionOutcomeTx needs to actually create the
// new activation, computed here (read-only) rather than there, so the
// decision logic stays in one place.
type reworkPlan struct {
	targetNodeKey      string
	activationSequence uint64
	iteration          uint32
}

// decideCompletionOutcome is the pure decision logic (no side effects, no
// writes) — every input it needs has already been loaded/gathered by its
// caller. Order matters: the sibling-run invariant is checked first (an
// invariant violation, never a policy failure), then the completion policy
// pin is resolved (a FAIL-worthy configuration problem if it cannot be),
// then the ReleaseSet/ladder gates (ordinary BLOCK-worthy "not ready yet"
// outcomes) — and only once the ladder is found unsatisfied does REWORK-
// vs-BLOCK get decided, per ADR-021's own "no valid rework edge -> BLOCK"
// rule (extended, GC-INV-29, to "or its own budget is exhausted -> BLOCK").
func decideCompletionOutcome(
	ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, manifest runtimedomain.ExecutionManifest, document workflow.WorkflowDocument,
	summary RunNodeStateSummary, nodeRuns []runtimedomain.NodeRun,
	evidence []runtimedomain.Evidence, approvals []runtimedomain.ApprovalRequest, releaseGate releaseSetGate,
) (CompletionOutcome, string, *reworkPlan, error) {
	siblings, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		return "", "", nil, err
	}
	for _, sibling := range siblings {
		if sibling.ID == run.ID {
			continue
		}
		if isNonTerminalRunState(sibling.State) {
			return CompletionOutcomeBlock, fmt.Sprintf(
				"%s: workflow run %s belongs to the same work item and is still %s",
				ReasonWorkItemRunStateInconsistent, sibling.ID, sibling.State,
			), nil, nil
		}
	}

	completionRules, resolveDetail, err := resolveCompletionPolicy(ctx, tx, manifest, document)
	if err != nil {
		return "", "", nil, err
	}
	if resolveDetail != "" {
		return CompletionOutcomeFail, resolveDetail, nil, nil
	}

	if !releaseGate.satisfied {
		return CompletionOutcomeBlock, fmt.Sprintf("%s: %s", ReasonReleaseSetNotSealed, releaseGate.detail), nil, nil
	}

	satisfied, unsatisfiedDetail := evaluateCompletionRules(completionRules, evidence, approvals)
	if satisfied {
		return CompletionOutcomePass, "", nil, nil
	}

	edge, hasReworkEdge := findCompletionReworkEdge(document, summary.ReachedEndNodeKey)
	if !hasReworkEdge {
		return CompletionOutcomeBlock, fmt.Sprintf("%s: %s", ReasonCompletionRequirementsUnmet, unsatisfiedDetail), nil, nil
	}
	round := countEndReaches(nodeRuns, summary.ReachedEndNodeKey)
	if round > edge.ReworkPolicy.MaxIterations {
		return CompletionOutcomeBlock, fmt.Sprintf(
			"%s: rework edge %s already used %d of %d allowed rounds",
			ReasonReworkBudgetExhausted, edge.Key, round-1, edge.ReworkPolicy.MaxIterations,
		), nil, nil
	}
	targetNode, ok := findNode(document, edge.To)
	if !ok {
		// Unreachable in practice: V5-10B's own validateNormalizedDocument
		// already requires a COMPLETION_REWORK edge's own To to resolve to a
		// real, non-END node before the document can ever publish. Fails
		// closed rather than panicking if that invariant is ever somehow
		// violated (a corrupted/foreign WorkflowVersion, the same
		// discipline CycleMembership's own doc comment already documents
		// elsewhere in this codebase).
		return CompletionOutcomeFail, fmt.Sprintf(
			"%s: rework edge %s targets unknown node %s", ReasonCompletionPolicyUnresolvable, edge.Key, edge.To,
		), nil, nil
	}
	priorMax, found, err := tx.Runtime().GetMaxNodeIteration(ctx, string(run.ID), targetNode.Key)
	if err != nil {
		return "", "", nil, err
	}
	iteration := uint32(0)
	if found {
		iteration = priorMax + 1
	}
	plan := &reworkPlan{
		targetNodeKey: targetNode.Key, activationSequence: maxActivationSequence(nodeRuns) + 1, iteration: iteration,
	}
	return CompletionOutcomeRework, "", plan, nil
}

// findCompletionReworkEdge finds the (at most one, per V5-10B's own
// validation) COMPLETION_REWORK edge published from endNodeKey.
func findCompletionReworkEdge(document workflow.WorkflowDocument, endNodeKey string) (workflow.Edge, bool) {
	for _, edge := range document.Edges {
		if edge.Kind == workflow.EdgeCompletionRework && edge.From == endNodeKey {
			return edge, true
		}
	}
	return workflow.Edge{}, false
}

// countEndReaches counts every SUCCEEDED NodeRun for endNodeKey — since END
// is terminal-once-reached (never superseded/retried) and a Run only ever
// cycles VERIFYING->RUNNING back to an END-reachable state via THIS file's
// own REWORK outcome, this count IS the current round number (1-indexed:
// the very first reach is round 1, with zero prior rework rounds).
func countEndReaches(nodeRuns []runtimedomain.NodeRun, endNodeKey string) uint32 {
	var count uint32
	for _, nodeRun := range nodeRuns {
		if nodeRun.NodeKey == endNodeKey && nodeRun.State == runtimedomain.NodeRunSucceeded {
			count++
		}
	}
	return count
}

// maxActivationSequence returns the highest ActivationSequence among
// nodeRuns — mirrors advance.go's own "nextSequence := current.
// ActivationSequence + 1" convention (this Run-wide counter is never
// per-lineage), generalized to "the run's own current max" since REWORK's
// new activation has no single upstream "current" NodeRun the way an
// ordinary hop does.
func maxActivationSequence(nodeRuns []runtimedomain.NodeRun) uint64 {
	var max uint64
	for _, nodeRun := range nodeRuns {
		if nodeRun.ActivationSequence > max {
			max = nodeRun.ActivationSequence
		}
	}
	return max
}

// resolveCompletionPolicy resolves document's own root CompletionPolicyRef
// (V5-11 PR0) against manifest's own immutable DependencyManifest (contract
// 1: "đối chiếu ID/version/hash trong ExecutionManifest trước khi load
// policy thật" — never trust a bare re-read of the WorkflowVersion alone),
// then loads and decodes the real PolicyVersion. A non-empty detail string
// means resolution failed for a reason this evaluation must treat as FAIL
// (a configuration problem, not "requirements not yet met"); err is only
// for genuine unexpected repository errors.
func resolveCompletionPolicy(
	ctx context.Context, tx ports.Tx, manifest runtimedomain.ExecutionManifest, document workflow.WorkflowDocument,
) (policy.CompletionRules, string, error) {
	ref := document.CompletionPolicyRef
	if ref == nil {
		return policy.CompletionRules{}, ReasonNoCompletionPolicyPinned + ": this workflow's compiled document has no completionPolicyRef", nil
	}

	var pinnedHash string
	found := false
	for _, pin := range manifest.DependencyManifest.Pins {
		if pin.Kind == string(ref.Kind) && pin.Key == ref.DefinitionID && pin.Version == ref.VersionID {
			pinnedHash = pin.Hash
			found = true
			break
		}
	}
	if !found {
		return policy.CompletionRules{}, fmt.Sprintf(
			"%s: completionPolicyRef %s/%s/%s is not present in this run's own pinned ExecutionManifest",
			ReasonCompletionPolicyUnresolvable, ref.Kind, ref.DefinitionID, ref.VersionID,
		), nil
	}

	fields, loadErr := tx.Definitions().LoadVersion(ctx, ref.VersionID)
	if errors.Is(loadErr, ports.ErrDefinitionVersionNotFound) {
		return policy.CompletionRules{}, fmt.Sprintf(
			"%s: completionPolicyRef %s/%s does not resolve to any published version",
			ReasonCompletionPolicyUnresolvable, ref.DefinitionID, ref.VersionID,
		), nil
	}
	if loadErr != nil {
		return policy.CompletionRules{}, "", loadErr
	}
	if fields.CompiledHash() != pinnedHash {
		return policy.CompletionRules{}, fmt.Sprintf(
			"%s: completionPolicyRef %s/%s resolved to compiled hash %s, but this run's own ExecutionManifest pinned %s",
			ReasonCompletionPolicyUnresolvable, ref.DefinitionID, ref.VersionID, fields.CompiledHash(), pinnedHash,
		), nil
	}

	var doc policy.PolicyDocument
	if err := json.Unmarshal([]byte(fields.CanonicalSource()), &doc); err != nil {
		return policy.CompletionRules{}, fmt.Sprintf("%s: completionPolicyRef document could not be decoded: %v", ReasonCompletionPolicyUnresolvable, err), nil
	}
	if doc.Category != policy.CategoryCompletion || doc.Completion == nil {
		return policy.CompletionRules{}, fmt.Sprintf(
			"%s: completionPolicyRef %s/%s does not resolve to a COMPLETION-category policy with completion rules",
			ReasonCompletionPolicyUnresolvable, ref.DefinitionID, ref.VersionID,
		), nil
	}
	return *doc.Completion, "", nil
}

// isPassingVerdict reports whether an Evidence row's own Verdict counts as
// satisfying a required evidence kind. NOT_APPLICABLE counts as passing —
// it is only ever reachable through gate.Criterion.AllowNotApplicable's own
// authoring-time authorization (Part B of the 2026-09-10 combined V5-09/
// V5-10 gap remediation), i.e. an already-authorized exemption, never a
// silent skip.
func isPassingVerdict(verdict string) bool {
	return verdict == runtimedomain.EvidenceVerdictSucceeded ||
		verdict == string(gate.VerdictPass) ||
		verdict == string(gate.VerdictNotApplicable)
}

// evaluateCompletionRules evaluates rules (V1 flat or V2 ladder — mutually
// exclusive per CompletionRules's own doc comment, so exactly one branch
// below ever actually runs for a real document) against the evidence/
// approvals this candidate's own Run actually produced. The V2 ladder is
// walked in policy.AssuranceLevelOrder()'s own canonical order, never a
// document's own RequiredAssurance array order (2026-09-10 contract) — this
// only affects which detail string surfaces first when multiple levels are
// unsatisfied, never correctness (every declared level is still checked;
// none is skipped by a satisfied HIGHER level, since AssuranceRequirement's
// own doc comment already establishes levels as non-cumulative-compensating).
func evaluateCompletionRules(rules policy.CompletionRules, evidence []runtimedomain.Evidence, approvals []runtimedomain.ApprovalRequest) (bool, string) {
	passingKinds := make(map[string]bool, len(evidence))
	for _, e := range evidence {
		if isPassingVerdict(e.Verdict) {
			passingKinds[e.Kind] = true
		}
	}
	decidedRoles := make(map[string]bool, len(approvals))
	for _, a := range approvals {
		if a.State == runtimedomain.ApprovalRequestDecided {
			decidedRoles[a.DecidedRole] = true
		}
	}

	if len(rules.RequiredAssurance) == 0 {
		for _, kind := range rules.RequiredEvidenceKinds {
			if !passingKinds[kind] {
				return false, fmt.Sprintf("missing or non-passing evidence for required kind %q", kind)
			}
		}
		return true, ""
	}

	byLevel := make(map[policy.AssuranceLevel]policy.AssuranceRequirement, len(rules.RequiredAssurance))
	for _, requirement := range rules.RequiredAssurance {
		byLevel[requirement.Level] = requirement
	}
	for _, level := range policy.AssuranceLevelOrder() {
		requirement, declared := byLevel[level]
		if !declared {
			continue
		}
		for _, kind := range requirement.RequiredEvidenceKinds {
			if !passingKinds[kind] {
				return false, fmt.Sprintf("level %s: missing or non-passing evidence for required kind %q", level, kind)
			}
		}
		for _, approvalReq := range requirement.RequiredApprovals {
			if !anyRoleDecided(decidedRoles, approvalReq.AuthorizedRoles) {
				return false, fmt.Sprintf("level %s: no decided approval from an authorized role %v", level, approvalReq.AuthorizedRoles)
			}
		}
	}
	return true, ""
}

func anyRoleDecided(decidedRoles map[string]bool, authorizedRoles []string) bool {
	for _, role := range authorizedRoles {
		if decidedRoles[role] {
			return true
		}
	}
	return false
}

// gatherCompletionCandidateEvidence collects every Evidence row and every
// ApprovalRequest this Run's own LATEST activation of each node produced —
// "latest" meaning the highest ActivationSequence per (NodeKey,
// BranchTokenID) lineage (mirroring computeRunNodeStateSummary's own
// dedup, completion.go) and, within that NodeRun, the highest AttemptNumber
// (a technical retry's own earlier, superseded Attempt never contributes
// stale evidence toward this candidate — this is this PR's own reading of
// "evidence phải ... còn fresh," 2026-09-10). Approvals are returned
// unfiltered (ListApprovalRequestsForRun is already Run-scoped, and at most
// one ever exists per NodeRunID, V4-09's own invariant), sorted by ID for
// deterministic candidate-input marshaling.
func gatherCompletionCandidateEvidence(
	ctx context.Context, tx ports.Tx, runID string, nodeRuns []runtimedomain.NodeRun,
) ([]runtimedomain.Evidence, []runtimedomain.ApprovalRequest, error) {
	latestNodeRunIDs := latestNodeRunIDsByLineage(nodeRuns)

	attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	latestAttemptPerNodeRun := make(map[runtimedomain.NodeRunID]runtimedomain.ExecutionAttempt, len(attempts))
	for _, attempt := range attempts {
		if !latestNodeRunIDs[attempt.NodeRunID] {
			continue
		}
		current, ok := latestAttemptPerNodeRun[attempt.NodeRunID]
		if !ok || attempt.AttemptNumber > current.AttemptNumber {
			latestAttemptPerNodeRun[attempt.NodeRunID] = attempt
		}
	}

	var evidence []runtimedomain.Evidence
	for _, attempt := range latestAttemptPerNodeRun {
		attemptEvidence, err := tx.Runtime().ListEvidenceForAttempt(ctx, string(attempt.ID))
		if err != nil {
			return nil, nil, err
		}
		evidence = append(evidence, attemptEvidence...)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })

	approvals, err := tx.Approvals().ListApprovalRequestsForRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(approvals, func(i, j int) bool { return approvals[i].ID < approvals[j].ID })

	return evidence, approvals, nil
}

// latestNodeRunIDsByLineage returns the set of NodeRunIDs that are each
// (NodeKey, BranchTokenID) lineage's own latest (highest ActivationSequence)
// activation — the identical dedup rule computeRunNodeStateSummary
// (completion.go) already applies, factored out here so this file never
// drifts from that one's own definition of "latest."
func latestNodeRunIDsByLineage(nodeRuns []runtimedomain.NodeRun) map[runtimedomain.NodeRunID]bool {
	type lineageKey struct {
		nodeKey       string
		branchTokenID string
	}
	latest := make(map[lineageKey]runtimedomain.NodeRun, len(nodeRuns))
	for _, nodeRun := range nodeRuns {
		var branchTokenID string
		if nodeRun.BranchTokenID != nil {
			branchTokenID = string(*nodeRun.BranchTokenID)
		}
		key := lineageKey{nodeKey: nodeRun.NodeKey, branchTokenID: branchTokenID}
		if current, ok := latest[key]; !ok || nodeRun.ActivationSequence > current.ActivationSequence {
			latest[key] = nodeRun
		}
	}
	ids := make(map[runtimedomain.NodeRunID]bool, len(latest))
	for _, nodeRun := range latest {
		ids[nodeRun.ID] = true
	}
	return ids
}

// buildCompletionCandidateInput assembles the deterministic snapshot both
// persisted as DecisionArtifact.Input and used for the replay-conflict
// comparison — see that type's own doc comment.
func buildCompletionCandidateInput(
	manifest runtimedomain.ExecutionManifest, document workflow.WorkflowDocument, summary RunNodeStateSummary,
	evidence []runtimedomain.Evidence, approvals []runtimedomain.ApprovalRequest, releaseSetID string,
) completionCandidateInput {
	evidenceIDs := make([]string, 0, len(evidence))
	for _, e := range evidence {
		evidenceIDs = append(evidenceIDs, string(e.ID))
	}
	sort.Strings(evidenceIDs)

	approvalIDs := make([]string, 0, len(approvals))
	for _, a := range approvals {
		approvalIDs = append(approvalIDs, string(a.ID))
	}
	sort.Strings(approvalIDs)

	var completionPolicyRef *string
	if document.CompletionPolicyRef != nil {
		ref := document.CompletionPolicyRef.DefinitionID + "/" + document.CompletionPolicyRef.VersionID
		completionPolicyRef = &ref
	}

	return completionCandidateInput{
		RunID: string(manifest.RunID), EndNodeRunID: summary.ReachedEndNodeRunID,
		WorkflowVersionID: string(manifest.WorkflowVersionID), CompiledSnapshotHash: manifest.CompiledSnapshotHash,
		CompletionPolicyRef: completionPolicyRef, EvidenceIDs: evidenceIDs, ApprovalRequestIDs: approvalIDs,
		ReleaseSetID: releaseSetID,
	}
}

// appliedCompletionOutcome is applyCompletionOutcomeTx's own result: the
// freshly-updated WorkflowRun (so its caller can use the new Version as the
// COMPLETION_DECIDED event's own Sequence, mirroring
// transitionRunToVerifyingTx's own convention, completion.go), plus the new
// NodeRun's identity when outcome is REWORK (zero values otherwise).
type appliedCompletionOutcome struct {
	run             runtimedomain.WorkflowRun
	reworkNodeRunID string
	reworkNodeKey   string
}

// applyCompletionOutcomeTx writes outcome's own atomic state transitions
// (ADR-021's own outcome table). BLOCK deliberately opens no WorkItemBlocker
// row — ADR-021's own outcome table names one only for FAIL ("kèm blocker
// COMPLETION_POLICY_FAILED"), never for BLOCK; the two state transitions
// (Run BLOCKED, WorkItem BLOCKED) are themselves the durable signal. REWORK
// leaves WorkItem completely untouched (ADR-021: "WorkItem giữ ACTIVE") and
// creates exactly one new PENDING NodeRun per plan — see this file's own
// package doc comment for why getting it actually scheduled is deliberately
// out of scope here.
func applyCompletionOutcomeTx(
	ctx context.Context, tx ports.Tx, ids idsource.Source, run runtimedomain.WorkflowRun, workItem workdomain.WorkItem,
	outcome CompletionOutcome, reason string, decisionArtifactID runtimedomain.DecisionArtifactID, plan *reworkPlan, correlationID string,
) (appliedCompletionOutcome, error) {
	switch outcome {
	case CompletionOutcomePass:
		updated, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
			RunID: string(run.ID), ExpectedState: runtimedomain.WorkflowRunVerifying, ExpectedVersion: run.Version,
			NextState: runtimedomain.WorkflowRunSucceeded,
		})
		if err != nil {
			return appliedCompletionOutcome{}, err
		}
		if _, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: string(workItem.ID), ExpectedStatus: workdomain.WorkItemActive, ExpectedVersion: workItem.Version,
			NextStatus: workdomain.WorkItemDone,
		}); err != nil {
			return appliedCompletionOutcome{}, err
		}
		return appliedCompletionOutcome{run: updated}, nil

	case CompletionOutcomeBlock:
		updated, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
			RunID: string(run.ID), ExpectedState: runtimedomain.WorkflowRunVerifying, ExpectedVersion: run.Version,
			NextState: runtimedomain.WorkflowRunBlocked,
		})
		if err != nil {
			return appliedCompletionOutcome{}, err
		}
		if _, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: string(workItem.ID), ExpectedStatus: workdomain.WorkItemActive, ExpectedVersion: workItem.Version,
			NextStatus: workdomain.WorkItemBlocked,
		}); err != nil {
			return appliedCompletionOutcome{}, err
		}
		return appliedCompletionOutcome{run: updated}, nil

	case CompletionOutcomeFail:
		updated, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
			RunID: string(run.ID), ExpectedState: runtimedomain.WorkflowRunVerifying, ExpectedVersion: run.Version,
			NextState: runtimedomain.WorkflowRunFailed,
		})
		if err != nil {
			return appliedCompletionOutcome{}, err
		}
		if _, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: string(workItem.ID), ExpectedStatus: workdomain.WorkItemActive, ExpectedVersion: workItem.Version,
			NextStatus: workdomain.WorkItemBlocked,
		}); err != nil {
			return appliedCompletionOutcome{}, err
		}
		blockerID := deterministicCompletionFailBlockerID(decisionArtifactID)
		if _, err := openWorkItemBlockerTx(
			ctx, tx, run.ProjectID, string(run.WorkItemID), blockerID, workdomain.BlockerCompletionPolicyFailed,
			string(run.ID), "", "", reason, correlationID, "",
		); err != nil {
			return appliedCompletionOutcome{}, err
		}
		return appliedCompletionOutcome{run: updated}, nil

	case CompletionOutcomeRework:
		if plan == nil {
			return appliedCompletionOutcome{}, fmt.Errorf("runtime: REWORK outcome for run %s has no activation plan", run.ID)
		}
		updated, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
			RunID: string(run.ID), ExpectedState: runtimedomain.WorkflowRunVerifying, ExpectedVersion: run.Version,
			NextState: runtimedomain.WorkflowRunRunning,
		})
		if err != nil {
			return appliedCompletionOutcome{}, err
		}
		nodeRunID := ids.NewID()
		nodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID(nodeRunID), run.ID, plan.targetNodeKey, plan.activationSequence, plan.iteration, nil,
			canonicalStateHash(run.SharedState), "",
		)
		if err != nil {
			return appliedCompletionOutcome{}, err
		}
		if _, err := tx.Runtime().CreateNodeRun(ctx, nodeRun); err != nil {
			return appliedCompletionOutcome{}, err
		}
		// WorkItem is deliberately left untouched — ADR-021: "WorkItem giữ
		// ACTIVE" for REWORK, and it already is (checked by this file's
		// own caller before ever reaching decideCompletionOutcome).
		return appliedCompletionOutcome{run: updated, reworkNodeRunID: nodeRunID, reworkNodeKey: plan.targetNodeKey}, nil

	default:
		return appliedCompletionOutcome{}, fmt.Errorf("runtime: unknown completion outcome %q", outcome)
	}
}

// CompletionDecidedEventType/CompletionDecidedSchemaVersion identify
// COMPLETION_DECIDED's own registered (EventType, SchemaVersion) pair.
const (
	CompletionDecidedEventType     = "COMPLETION_DECIDED"
	CompletionDecidedSchemaVersion = 1
)

type completionDecidedEventPayload struct {
	RunID              string `json:"runId"`
	WorkItemID         string `json:"workItemId"`
	EndNodeRunID       string `json:"endNodeRunId"`
	DecisionArtifactID string `json:"decisionArtifactId"`
	Outcome            string `json:"outcome"`
	Reason             string `json:"reason,omitempty"`
	// ReworkNodeRunID/ReworkNodeKey are populated only when Outcome ==
	// REWORK (PR2) — this event's own record of the new activation this
	// decision created is what CompletionDecisionResult itself already
	// exposes; the payload just mirrors it into the durable event too.
	ReworkNodeRunID string `json:"reworkNodeRunId,omitempty"`
	ReworkNodeKey   string `json:"reworkNodeKey,omitempty"`
}

func appendCompletionDecidedEvent(
	ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, decisionArtifactID runtimedomain.DecisionArtifactID,
	result CompletionDecisionResult, correlationID string,
) error {
	payload, err := json.Marshal(completionDecidedEventPayload{
		RunID: result.RunID, WorkItemID: result.WorkItemID, EndNodeRunID: result.EndNodeRunID,
		DecisionArtifactID: string(decisionArtifactID), Outcome: string(result.Outcome), Reason: result.Reason,
		ReworkNodeRunID: result.ReworkNodeRunID, ReworkNodeKey: result.ReworkNodeKey,
	})
	if err != nil {
		return fmt.Errorf("marshal %s event payload: %w", CompletionDecidedEventType, err)
	}
	return tx.Events().Append(ctx, ports.DomainEvent{
		ID: deterministicCompletionEventID(decisionArtifactID), ProjectID: string(run.ProjectID),
		AggregateType: "WorkflowRun", AggregateID: string(run.ID), Sequence: int64(run.Version),
		EventType: CompletionDecidedEventType, SchemaVersion: CompletionDecidedSchemaVersion, PayloadJSON: string(payload),
		CorrelationID: correlationID, CreatedAt: time.Now().UTC(),
	})
}

// deterministicCompletionDecisionID/deterministicCompletionEventID/
// deterministicCompletionFailBlockerID implement contract 2's own
// (2026-09-10) sha256-content-hash ID scheme verbatim:
//
//	DecisionArtifact = hash("completion-decision", RunID, EndNodeRunID)
//	Event            = hash(DecisionArtifactID, "recorded")
//	FailBlocker      = hash(DecisionArtifactID, "failed-blocker")
//
// mirroring deterministicJoinNodeRunID's own exact shape (advance.go):
// sha256 over NUL-joined parts, hex-encoded, truncated to 16 bytes,
// human-readable prefix. REWORK's own new NodeRun deliberately does NOT get
// a deterministic ID the same way — ids.NewID() is used instead
// (applyCompletionOutcomeTx), since NodeRun identity in this codebase is
// always minted, never content-derived (every other activation in
// advance.go/schedule.go mints one via ids.NewID() too); the REPLAY safety
// contract 2 requires comes from decisionArtifactID/eventID alone, not from
// the activation's own id.
func deterministicCompletionDecisionID(runID, endNodeRunID string) runtimedomain.DecisionArtifactID {
	sum := sha256.Sum256([]byte("completion-decision" + "\x00" + runID + "\x00" + endNodeRunID))
	return runtimedomain.DecisionArtifactID("completion-decision-" + hex.EncodeToString(sum[:16]))
}

func deterministicCompletionEventID(decisionArtifactID runtimedomain.DecisionArtifactID) string {
	sum := sha256.Sum256([]byte(string(decisionArtifactID) + "\x00" + "recorded"))
	return "completion-decided-" + hex.EncodeToString(sum[:16])
}

func deterministicCompletionFailBlockerID(decisionArtifactID runtimedomain.DecisionArtifactID) string {
	sum := sha256.Sum256([]byte(string(decisionArtifactID) + "\x00" + "failed-blocker"))
	return "completion-failed-blocker-" + hex.EncodeToString(sum[:16])
}
