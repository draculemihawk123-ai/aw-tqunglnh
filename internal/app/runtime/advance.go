// AdvanceRun is V4-03's own routing core
// (docs/design/06-v4-runtime-engine.md): "persisted transition kích đúng
// downstream node từ allow-listed outcome". It is the sole consumer this
// codebase builds for the AdvanceRunJobKind durable job StartWorkflowRun
// (V4-02) already enqueues for START and enqueues again, here, for every
// downstream node it can immediately resolve itself.
//
// Scope decision on ROUTER (confirmed with the user before writing this
// file): the V2-08 compiled schema gives ROUTER zero typed config —
// workflow.Node's own doc comment says a ROUTER's outcome-selection "is
// runtime behavior", but no ADR/design doc/harness-engineering lecture
// anywhere defines what that deterministic rule actually looks like for a
// ROUTER with more than one declared outcome. Inventing one here would be
// authoring new schema/runtime semantics with no ADR backing. So this task
// auto-resolves a routing decision ONLY for a node that is genuinely
// deterministic without any external input: START, and ROUTER with EXACTLY
// ONE declared outcome (trivially deterministic — no real "rule" needed,
// identical treatment to a linear pass-through). A ROUTER with zero or two-
// plus outcomes and no externally supplied Outcome gets ErrOutcomeRequired,
// a typed error, not a guess. Multi-outcome ROUTER resolution is left for a
// future task/ADR that actually specifies the rule.
//
// This same auto-derivation is deliberately NEVER applied to AGENT/COMMAND/
// MACHINE_GATE/APPROVAL/WAIT/FORK/JOIN, even when one happens to declare
// only one Outcome: those node types require real work (an Attempt, a
// signal, an operator decision, a fork) before they have a genuine outcome
// to route on — auto-completing one without running it would silently skip
// execution, not just skip a trivial choice. AdvanceRun's own Outcome
// request field exists so a FUTURE caller (V4-04's attempt finalize, V4-09's
// approval decision, ...) can supply that real, already-determined outcome
// explicitly; this task itself never calls AdvanceRun that way, since no
// such producer exists yet in this codebase.
//
// One hop per call, matching go-core-spec §7 rule 5 ("Commit outcome cập
// nhật ... và downstream job atomically" — one outcome commit, one
// downstream job, not a multi-hop loop inside one transaction): AdvanceRun
// completes exactly the NodeRun it is asked to route away from and creates
// exactly one downstream NodeRun activation. If that downstream node is
// itself auto-resolvable (ROUTER, one outcome), AdvanceRun enqueues another
// AdvanceRunJobKind job for it — to be processed by a SEPARATE subsequent
// call — rather than recursing; if it is any other type, AdvanceRun creates
// its NodeRun PENDING and stops: no consumer exists yet for that node type
// (V4-04 for executable nodes, V4-08 WAIT, V4-09 APPROVAL, V4-10 FORK, V4-12
// END), the same "enqueue only, a separate later task is the sole consumer"
// discipline this codebase already established for REPOSITORY_PROBE/
// WORKSPACE_PROVISION.
//
// Idempotency mirrors workspaceprovision.Handler's own "idempotent early-
// return" discipline, not the Command/Receipts envelope V4-02's
// StartWorkflowRun uses: AdvanceRun is never a public command, only an
// internal scheduler step a durable job drives, so there is no
// IdempotencyKey/RequestHash to replay against. Instead, the very first
// thing this function does is read the target NodeRun's current State —
// anything other than RUNNING means an earlier delivery of the same job
// already completed this hop (or, for a node type V4-03 does not own, that
// it was never eligible to advance in the first place), and it returns a
// no-op Advanced=false result instead of re-deriving the outcome or
// attempting to recreate a downstream NodeRun the UNIQUE(run_id, node_key,
// activation_sequence) constraint would otherwise reject as a raw DB error.
//
// No domain event is appended here, matching workspaceprovision.Handler and
// repositoryprobe's own job handlers (neither appends one): the CAS
// transition's own version/updated_at is this hop's audit trail, the same
// as every other job-driven state transition in this codebase.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

var (
	// ErrOutcomeRequired is returned when the NodeRun being advanced has no
	// externally supplied Outcome AND cannot auto-derive one: either its
	// node type is not one of the two AdvanceRun resolves on its own
	// (START, ROUTER), or it is a ROUTER without exactly one declared
	// outcome (this task's own confirmed ROUTER scope decision — see this
	// file's own package doc comment).
	ErrOutcomeRequired = errors.New("runtime: node outcome is required and cannot be auto-derived")
	// ErrOutcomeNotAllowed is GC-INV-11's own enforcement point: the
	// resolved outcome (auto-derived or explicitly supplied) is not in the
	// node's declared allow-list.
	ErrOutcomeNotAllowed = errors.New("runtime: outcome is not in the node's declared allow-list")
	// ErrRouteNotFound is a defensive, should-be-unreachable error: the
	// workflow compiler already rejects any document with a declared
	// outcome that has no matching edge (route coverage validation) before
	// it can ever be published. This exists so a corrupted/foreign
	// WorkflowVersion fails closed here rather than panicking.
	ErrRouteNotFound = errors.New("runtime: no edge declared for this node and outcome")
	// ErrNodeRunMismatch is returned when NodeRunID does not belong to
	// RunID — a defensive cross-check against a malformed job payload.
	ErrNodeRunMismatch = errors.New("runtime: node run does not belong to the given workflow run")
)

// AdvanceRunJobPayload is the exact JSON shape internal/app/runtime's own
// StartWorkflowRun (V4-02) and AdvanceRun (V4-03, this file) both marshal
// for an AdvanceRunJobKind job: {"runId", "nodeRunId"}. It is defined once
// here, not duplicated as an anonymous struct at each producer, so a
// consumer (Scheduler.Handle, below) always unmarshals the exact shape
// every producer writes.
type AdvanceRunJobPayload struct {
	RunID     string `json:"runId"`
	NodeRunID string `json:"nodeRunId"`
}

const defaultAdvanceRunJobMaxClaimsFollowUp = 3

// AdvanceRunRequest is what a caller supplies to AdvanceRun.
type AdvanceRunRequest struct {
	RunID     string
	NodeRunID string
	// Outcome is the caller-already-determined outcome for NodeRunID, or
	// blank to let AdvanceRun auto-derive it — only possible for a
	// structural node (START, or ROUTER with exactly one declared outcome).
	// See this file's own package doc comment for the full scope decision.
	Outcome string
}

// AdvanceRunResult reports what one hop actually did.
type AdvanceRunResult struct {
	// Advanced is false only for the idempotent-replay no-op: the target
	// NodeRun was already routed away from by an earlier call/delivery.
	Advanced bool
	// CompletedNodeRunID/SelectedOutcome describe the NodeRun this call
	// routed away from (req.NodeRunID) and the outcome it resolved.
	CompletedNodeRunID string
	SelectedOutcome    string
	// NextNodeRunID/NextNodeKey describe the downstream NodeRun this call
	// created. Both are empty for the idempotent no-op case.
	NextNodeRunID string
	NextNodeKey   string
	// NextAutoAdvanced reports whether the downstream node was itself
	// immediately resolvable (structural, single outcome) — true iff
	// NextJobID is non-empty.
	NextAutoAdvanced bool
	// NextJobID is the AdvanceRunJobKind job id enqueued for the downstream
	// node, or "" if the downstream node is not one this task advances
	// further (left PENDING for a later task's own dispatcher).
	NextJobID string
}

// AdvanceRun performs exactly one routing hop. See this file's own package
// doc comment for the full design and scope decisions.
func AdvanceRun(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, req AdvanceRunRequest) (AdvanceRunResult, error) {
	if req.RunID == "" || req.NodeRunID == "" {
		return AdvanceRunResult{}, errors.New("runtime: RunID and NodeRunID are required")
	}

	var result AdvanceRunResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		current, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
		if err != nil {
			return err
		}
		if string(current.RunID) != req.RunID {
			return fmt.Errorf("%w: node run %s belongs to run %s, not %s", ErrNodeRunMismatch, req.NodeRunID, current.RunID, req.RunID)
		}
		if current.State != runtimedomain.NodeRunRunning {
			// Idempotent no-op — see this file's own package doc comment.
			result = AdvanceRunResult{
				Advanced: false, CompletedNodeRunID: req.NodeRunID, SelectedOutcome: current.SelectedOutcome,
			}
			return nil
		}

		run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
		if err != nil {
			return err
		}

		version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
		if err != nil {
			return err
		}
		document := version.Document()

		node, ok := findNode(document, current.NodeKey)
		if !ok {
			return fmt.Errorf("runtime: node %s not found in workflow version %s", current.NodeKey, run.WorkflowVersionID)
		}

		outcome := req.Outcome
		if outcome == "" {
			if !isStructuralRoutingNode(node.Type) || len(node.Outcomes) != 1 {
				return fmt.Errorf("%w: node %s (type %s, %d declared outcomes)", ErrOutcomeRequired, node.Key, node.Type, len(node.Outcomes))
			}
			outcome = node.Outcomes[0]
		}
		if !outcomeDeclared(node, outcome) {
			return fmt.Errorf("%w: node %s outcome %q", ErrOutcomeNotAllowed, node.Key, outcome)
		}
		edge, ok := findEdge(document, node.Key, outcome)
		if !ok {
			return fmt.Errorf("%w: node %s outcome %q", ErrRouteNotFound, node.Key, outcome)
		}
		downstreamNode, ok := findNode(document, edge.To)
		if !ok {
			return fmt.Errorf("runtime: edge %s targets unknown node %s", edge.Key, edge.To)
		}

		if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
			NodeRunID: req.NodeRunID, ExpectedState: runtimedomain.NodeRunRunning, ExpectedVersion: current.Version,
			NextState: runtimedomain.NodeRunSucceeded, SelectedOutcome: outcome,
		}); err != nil {
			return err
		}

		nextSequence := current.ActivationSequence + 1
		nextID := ids.NewID()
		nextNodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID(nextID), run.ID, downstreamNode.Key, nextSequence, 0, nil,
			canonicalStateHash(run.SharedState), "",
		)
		if err != nil {
			return err
		}

		autoAdvance := isStructuralRoutingNode(downstreamNode.Type) && len(downstreamNode.Outcomes) == 1
		if autoAdvance {
			nextNodeRun.State = runtimedomain.NodeRunRunning
		}
		if _, err := tx.Runtime().CreateNodeRun(ctx, nextNodeRun); err != nil {
			return err
		}

		result = AdvanceRunResult{
			Advanced: true, CompletedNodeRunID: req.NodeRunID, SelectedOutcome: outcome,
			NextNodeRunID: nextID, NextNodeKey: downstreamNode.Key, NextAutoAdvanced: autoAdvance,
		}

		if !autoAdvance {
			return nil
		}

		jobID := ids.NewID()
		payload, err := json.Marshal(AdvanceRunJobPayload{RunID: string(run.ID), NodeRunID: nextID})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", AdvanceRunJobKind, err)
		}
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(jobID), ProjectID: run.ProjectID, Kind: AdvanceRunJobKind,
			AggregateType: "WorkflowRun", AggregateID: string(run.ID), Payload: payload,
			MaxClaims: defaultAdvanceRunJobMaxClaimsFollowUp, IdempotencyKey: "advance-" + nextID,
		})
		if err != nil {
			return err
		}
		result.NextJobID = string(job.ID)
		return nil
	})
	return result, err
}

// isStructuralRoutingNode reports whether nodeType is one of the two node
// types AdvanceRun may auto-derive an outcome for without any external
// input: START (always exactly one implicit path) and ROUTER (only when it
// also declares exactly one outcome — see this file's own package doc
// comment for the ROUTER scope decision). Every other node type always
// requires either a real Attempt/signal/decision (AGENT, COMMAND,
// MACHINE_GATE, APPROVAL, WAIT, FORK, JOIN) or is terminal with nothing to
// route further (END) — AdvanceRun never resolves those on its own,
// regardless of how many outcomes they declare.
func isStructuralRoutingNode(nodeType workflow.NodeType) bool {
	return nodeType == workflow.NodeStart || nodeType == workflow.NodeRouter
}

func findNode(document workflow.WorkflowDocument, key string) (workflow.Node, bool) {
	for _, node := range document.Nodes {
		if node.Key == key {
			return node, true
		}
	}
	return workflow.Node{}, false
}

func findEdge(document workflow.WorkflowDocument, from, outcome string) (workflow.Edge, bool) {
	for _, edge := range document.Edges {
		if edge.From == from && edge.Outcome == outcome {
			return edge, true
		}
	}
	return workflow.Edge{}, false
}

func outcomeDeclared(node workflow.Node, outcome string) bool {
	for _, declared := range node.Outcomes {
		if declared == outcome {
			return true
		}
	}
	return false
}
