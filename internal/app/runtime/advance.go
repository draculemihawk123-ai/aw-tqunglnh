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
// A single NODE_ROUTED domain event is appended in the same transaction as
// the NodeRun transition, the downstream NodeRun activation and the
// follow-up job — GC-INV-15's own "mọi state transition tạo domain event
// trong cùng database transaction" (correction found during review: this
// file's first pass reasoned from workspaceprovision.Handler/
// repositoryprobe's job handlers, neither of which appends one, but that
// precedent does not override an explicit invariant).
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

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

	// ErrSharedStateFieldNotDeclared is HE-14-M04's own enforcement point
	// (correction found during V4-03 review): AdvanceRunRequest.SharedStatePatch
	// named a field the pinned WorkflowVersion's own SharedState schema
	// never declared.
	ErrSharedStateFieldNotDeclared = errors.New("runtime: shared state field is not declared in the workflow document")
	// ErrSharedStateWriterNotAllowed is returned when the node being routed
	// away from is not in a patched field's own declared Writers allowlist.
	ErrSharedStateWriterNotAllowed = errors.New("runtime: node is not an allowed writer for this shared state field")
	// ErrSharedStateTypeMismatch is returned when a patched value's JSON
	// kind does not match the field's declared SharedStateFieldType.
	ErrSharedStateTypeMismatch = errors.New("runtime: shared state value does not match the field's declared type")
	// ErrSharedStateMergeConflict is MergeRuleRejectOnConflict's own
	// enforcement point: the field already carries a value, so this write
	// is rejected rather than silently overwriting it.
	ErrSharedStateMergeConflict = errors.New("runtime: shared state field already has a value and its merge rule rejects the conflicting write")
)

// AdvanceRunJobPayload is the exact JSON shape internal/app/runtime's own
// StartWorkflowRun (V4-02) and AdvanceRun (V4-03, this file) both marshal
// for an AdvanceRunJobKind job. It is defined once here, not duplicated as
// an anonymous struct at each producer, so a consumer (Scheduler.Handle,
// below) always unmarshals the exact shape every producer writes.
type AdvanceRunJobPayload struct {
	RunID     string `json:"runId"`
	NodeRunID string `json:"nodeRunId"`
	// CorrelationID threads the originating command's own correlation
	// chain through every hop (correction found during review, go-core-
	// spec §20's own "Mọi log/event có CorrelationID... khi có"): V4-02's
	// StartWorkflowRun seeds this from cmd.CorrelationID on the very first
	// job; each subsequent AdvanceRun hop that enqueues a follow-up job
	// carries its own req.CorrelationID forward unchanged, so the whole
	// run's own chain of NODE_ROUTED events shares one CorrelationID.
	CorrelationID string `json:"correlationId,omitempty"`
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
	// SharedStatePatch is a set of typed writes to the WorkflowRun's own
	// shared state (HE-14-M04), attributed to the node this call routes
	// away from (the NodeRun named by NodeRunID). Every key must name a
	// field the pinned WorkflowVersion's own WorkflowDocument.SharedState
	// schema declares, with that node's key listed in the field's own
	// Writers; each value's JSON kind must match the field's declared
	// Type; the actual merge follows the field's own declared MergeRule.
	// Nil/empty applies no write at all — most hops (every one this task's
	// own auto-derivation ever drives) write nothing.
	SharedStatePatch map[string]json.RawMessage
	// CorrelationID threads a broader command/event chain through this
	// hop's own NODE_ROUTED event and, when this hop enqueues a follow-up
	// job, forward into that job's own AdvanceRunJobPayload — see that
	// type's own doc comment for the full chain. Blank when nothing
	// upstream has one.
	CorrelationID string
	// JobID names the durable job that drove this call (Scheduler.Handle
	// passes the job.ID it was handed) — go-core-spec §20's own "Mọi log/
	// event có... JobID khi có". Blank for a caller that is not itself
	// job-driven (none in this codebase today; every real caller goes
	// through Scheduler).
	JobID string
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
	// NextScheduleJobID is the ScheduleNodeRunJobKind job id enqueued when
	// the downstream node is executable (AGENT/COMMAND/MACHINE_GATE,
	// V4-04's own schedule.go) — "" otherwise. Mutually exclusive with
	// NextJobID: a downstream node is either auto-advanced (structural),
	// scheduled for execution (executable), or left PENDING for a still-
	// later task's own dispatcher (WAIT/APPROVAL/FORK/JOIN/END) — never
	// more than one of the three.
	NextScheduleJobID string
}

// AdvanceRun performs exactly one routing hop. See this file's own package
// doc comment for the full design and scope decisions. It is a thin
// WithSerializedWrite wrapper around advanceRunTx — the actual routing
// logic is a Tx-composable function of its own (V4-05 scoping review) so
// that a caller already holding an open transaction (schedule.go's own
// ScheduleExecutableNodeRun today; finalize.go's own FinalizeExecutionAttempt
// from V4-05 onward) can route a SUCCEEDED Attempt's own NodeRun forward
// atomically with that same commit — never as a second, separate
// transaction after the first one already committed, which would leave a
// durable job/event stuck mid-flight if the process crashed in between.
func AdvanceRun(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, req AdvanceRunRequest) (AdvanceRunResult, error) {
	if req.RunID == "" || req.NodeRunID == "" {
		return AdvanceRunResult{}, errors.New("runtime: RunID and NodeRunID are required")
	}
	var result AdvanceRunResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		result, err = advanceRunTx(ctx, tx, ids, req)
		return err
	})
	return result, err
}

// advanceRunTx is AdvanceRun's own routing logic, composed against an
// already-open ports.Tx rather than opening its own transaction — see
// AdvanceRun's own doc comment for why this split exists.
func advanceRunTx(ctx context.Context, tx ports.Tx, ids idsource.Source, req AdvanceRunRequest) (AdvanceRunResult, error) {
	var result AdvanceRunResult
	current, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
	if err != nil {
		return AdvanceRunResult{}, err
	}
	if string(current.RunID) != req.RunID {
		return AdvanceRunResult{}, fmt.Errorf("%w: node run %s belongs to run %s, not %s", ErrNodeRunMismatch, req.NodeRunID, current.RunID, req.RunID)
	}
	if current.State != runtimedomain.NodeRunRunning {
		// Idempotent no-op — see this file's own package doc comment.
		return AdvanceRunResult{
			Advanced: false, CompletedNodeRunID: req.NodeRunID, SelectedOutcome: current.SelectedOutcome,
		}, nil
	}

	run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
	if err != nil {
		return AdvanceRunResult{}, err
	}

	version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
	if err != nil {
		return AdvanceRunResult{}, err
	}
	document := version.Document()

	node, ok := findNode(document, current.NodeKey)
	if !ok {
		return AdvanceRunResult{}, fmt.Errorf("runtime: node %s not found in workflow version %s", current.NodeKey, run.WorkflowVersionID)
	}

	outcome := req.Outcome
	if outcome == "" {
		if !isStructuralRoutingNode(node.Type) || len(node.Outcomes) != 1 {
			return AdvanceRunResult{}, fmt.Errorf("%w: node %s (type %s, %d declared outcomes)", ErrOutcomeRequired, node.Key, node.Type, len(node.Outcomes))
		}
		outcome = node.Outcomes[0]
	}
	if !outcomeDeclared(node, outcome) {
		return AdvanceRunResult{}, fmt.Errorf("%w: node %s outcome %q", ErrOutcomeNotAllowed, node.Key, outcome)
	}
	edge, ok := findEdge(document, node.Key, outcome)
	if !ok {
		return AdvanceRunResult{}, fmt.Errorf("%w: node %s outcome %q", ErrRouteNotFound, node.Key, outcome)
	}
	downstreamNode, ok := findNode(document, edge.To)
	if !ok {
		return AdvanceRunResult{}, fmt.Errorf("runtime: edge %s targets unknown node %s", edge.Key, edge.To)
	}

	newSharedState := run.SharedState
	if len(req.SharedStatePatch) > 0 {
		merged, err := applySharedStatePatch(document, node.Key, run.SharedState, req.SharedStatePatch)
		if err != nil {
			return AdvanceRunResult{}, err
		}
		newSharedState = merged
		if _, err := tx.Runtime().UpdateWorkflowRunSharedState(ctx, ports.UpdateWorkflowRunSharedStateRequest{
			RunID: req.RunID, ExpectedVersion: run.Version, SharedState: newSharedState,
		}); err != nil {
			return AdvanceRunResult{}, err
		}
	}

	completedNodeRun, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
		NodeRunID: req.NodeRunID, ExpectedState: runtimedomain.NodeRunRunning, ExpectedVersion: current.Version,
		NextState: runtimedomain.NodeRunSucceeded, SelectedOutcome: outcome,
	})
	if err != nil {
		return AdvanceRunResult{}, err
	}

	nextSequence := current.ActivationSequence + 1
	nextID := ids.NewID()
	nextNodeRun, err := runtimedomain.NewNodeRun(
		runtimedomain.NodeRunID(nextID), run.ID, downstreamNode.Key, nextSequence, 0, nil,
		canonicalStateHash(newSharedState), "",
	)
	if err != nil {
		return AdvanceRunResult{}, err
	}

	autoAdvance := isStructuralRoutingNode(downstreamNode.Type) && len(downstreamNode.Outcomes) == 1
	if autoAdvance {
		nextNodeRun.State = runtimedomain.NodeRunRunning
	}
	if _, err := tx.Runtime().CreateNodeRun(ctx, nextNodeRun); err != nil {
		return AdvanceRunResult{}, err
	}

	result = AdvanceRunResult{
		Advanced: true, CompletedNodeRunID: req.NodeRunID, SelectedOutcome: outcome,
		NextNodeRunID: nextID, NextNodeKey: downstreamNode.Key, NextAutoAdvanced: autoAdvance,
	}

	// GC-INV-15: append the domain event in the same transaction as
	// the transition/activation/job above, not as an afterthought.
	// Sequence is completedNodeRun.Version (the NodeRun row's own
	// post-transition optimistic version), not a hardcoded 1 —
	// correction found during review: V4-04 will add further
	// transitions (PENDING->QUEUED->RUNNING) on this SAME NodeRun
	// aggregate, each also needing its own event per GC-INV-15, and a
	// hardcoded Sequence would collide with domain_events' own UNIQUE
	// (aggregate_type, aggregate_id, sequence) constraint the moment a
	// second one landed. Tying Sequence to the aggregate's own version
	// needs no extra query/allocation step and is exactly monotonic
	// with the transitions that produce each event, one per version.
	eventPayload, err := json.Marshal(nodeRoutedEventPayload{
		RunID: string(run.ID), WorkItemID: string(run.WorkItemID), NodeRunID: req.NodeRunID,
		NodeKey: node.Key, SelectedOutcome: outcome,
		NextNodeRunID: nextID, NextNodeKey: downstreamNode.Key, JobID: req.JobID,
	})
	if err != nil {
		return AdvanceRunResult{}, fmt.Errorf("marshal NODE_ROUTED event payload: %w", err)
	}
	if err := tx.Events().Append(ctx, ports.DomainEvent{
		ID: req.NodeRunID + "-routed", ProjectID: string(run.ProjectID),
		AggregateType: "NodeRun", AggregateID: req.NodeRunID, Sequence: int64(completedNodeRun.Version),
		EventType: NodeRoutedEventType, SchemaVersion: NodeRoutedSchemaVersion, PayloadJSON: string(eventPayload),
		CorrelationID: req.CorrelationID, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return AdvanceRunResult{}, err
	}

	switch {
	case autoAdvance:
		payload, err := json.Marshal(AdvanceRunJobPayload{RunID: string(run.ID), NodeRunID: nextID, CorrelationID: req.CorrelationID})
		if err != nil {
			return AdvanceRunResult{}, fmt.Errorf("marshal %s job payload: %w", AdvanceRunJobKind, err)
		}
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(ids.NewID()), ProjectID: run.ProjectID, Kind: AdvanceRunJobKind,
			AggregateType: "WorkflowRun", AggregateID: string(run.ID), Payload: payload,
			MaxClaims: defaultAdvanceRunJobMaxClaimsFollowUp, IdempotencyKey: "advance-" + nextID,
		})
		if err != nil {
			return AdvanceRunResult{}, err
		}
		result.NextJobID = string(job.ID)

	case isExecutableNode(downstreamNode.Type):
		// V4-04: an executable downstream node needs its own scheduling
		// transaction (ResolvedExecutionProfileV1 resolution, effective
		// scope, exact manifest revision) that this call's own
		// transaction cannot perform inline — ADR-027 requires
		// resolving the RuntimeExecutionConfigProvider's snapshot
		// OUTSIDE any database transaction, which this hop is already
		// inside of. So, like the auto-advance case above, this hands
		// off to a separate durable job/handler (schedule.go's
		// ScheduleExecutableNodeRun via NodeSchedulingHandler) rather
		// than resolving inline — enqueued atomically here, alongside
		// the NodeRun activation and NODE_ROUTED event, so the handoff
		// itself can never be lost.
		payload, err := json.Marshal(ScheduleNodeRunJobPayload{RunID: string(run.ID), NodeRunID: nextID, CorrelationID: req.CorrelationID})
		if err != nil {
			return AdvanceRunResult{}, fmt.Errorf("marshal %s job payload: %w", ScheduleNodeRunJobKind, err)
		}
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(ids.NewID()), ProjectID: run.ProjectID, Kind: ScheduleNodeRunJobKind,
			AggregateType: "NodeRun", AggregateID: nextID, Payload: payload,
			MaxClaims: defaultScheduleNodeRunJobMaxClaims, IdempotencyKey: "schedule-" + nextID,
		})
		if err != nil {
			return AdvanceRunResult{}, err
		}
		result.NextScheduleJobID = string(job.ID)
	}
	return result, nil
}

// NodeRoutedEventType/NodeRoutedSchemaVersion identify NODE_ROUTED's own
// registered (EventType, SchemaVersion) pair in internal/app/eventschema's
// registry (RegisterEventSchemas, event_schema.go) — exported so that
// registration and the golden-fixture test can both reference the exact
// same constants AdvanceRun itself appends with, rather than each
// hardcoding the string/int a second time.
const (
	NodeRoutedEventType     = "NODE_ROUTED"
	NodeRoutedSchemaVersion = 1
)

// nodeRoutedEventPayload is NODE_ROUTED's own JSON shape.
type nodeRoutedEventPayload struct {
	RunID           string `json:"runId"`
	WorkItemID      string `json:"workItemId"`
	NodeRunID       string `json:"nodeRunId"`
	NodeKey         string `json:"nodeKey"`
	SelectedOutcome string `json:"selectedOutcome"`
	NextNodeRunID   string `json:"nextNodeRunId"`
	NextNodeKey     string `json:"nextNodeKey"`
	// JobID is the durable job that drove this hop (blank if the caller
	// did not supply one via AdvanceRunRequest.JobID).
	JobID string `json:"jobId,omitempty"`
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

// applySharedStatePatch validates and applies patch to currentState per the
// document's own declared SharedState schema (HE-14-M04): every patched
// field must be declared, writerNodeKey must be one of that field's own
// Writers, each value's JSON kind must match the field's declared Type,
// and the actual merge follows the field's own MergeRule. It returns the
// new canonical shared-state JSON — currentState itself unchanged when
// patch is empty.
func applySharedStatePatch(
	document workflow.WorkflowDocument, writerNodeKey string,
	currentState json.RawMessage, patch map[string]json.RawMessage,
) (json.RawMessage, error) {
	if len(patch) == 0 {
		return currentState, nil
	}
	fieldsByName := make(map[string]workflow.SharedStateField, len(document.SharedState))
	for _, field := range document.SharedState {
		fieldsByName[field.Name] = field
	}

	current := map[string]json.RawMessage{}
	if len(currentState) > 0 {
		if err := json.Unmarshal(currentState, &current); err != nil {
			return nil, fmt.Errorf("runtime: current shared state is not a JSON object: %w", err)
		}
	}

	// Sort patch keys before validating — correction found during review:
	// Go's own map iteration order is randomized, so a request with more
	// than one invalid field used to surface a different error on every
	// call, making a caller's own retry-on-error behavior nondeterministic.
	names := make([]string, 0, len(patch))
	for name := range patch {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		value := patch[name]
		field, declared := fieldsByName[name]
		if !declared {
			return nil, fmt.Errorf("%w: field %q", ErrSharedStateFieldNotDeclared, name)
		}
		writerAllowed := false
		for _, writer := range field.Writers {
			if writer == writerNodeKey {
				writerAllowed = true
				break
			}
		}
		if !writerAllowed {
			return nil, fmt.Errorf("%w: node %q, field %q", ErrSharedStateWriterNotAllowed, writerNodeKey, name)
		}
		if !jsonValueMatchesSharedStateType(value, field.Type) {
			return nil, fmt.Errorf("%w: field %q declared type %s", ErrSharedStateTypeMismatch, name, field.Type)
		}
		merged, err := mergeSharedStateValue(field, current[name], value)
		if err != nil {
			return nil, err
		}
		current[name] = merged
	}

	merged, err := json.Marshal(current)
	if err != nil {
		return nil, fmt.Errorf("runtime: marshal merged shared state: %w", err)
	}
	return merged, nil
}

// jsonValueMatchesSharedStateType reports whether value's own JSON kind
// matches fieldType — ARTIFACT_REF is represented as a plain string
// identifier (HE-14-M04's "artifact lớn lưu bằng reference": the field
// never carries the artifact's own content, only a reference a runtime
// resolves separately, and a string identifier is the minimal shape that
// can name one).
func jsonValueMatchesSharedStateType(value json.RawMessage, fieldType workflow.SharedStateFieldType) bool {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return false
	}
	switch fieldType {
	case workflow.SharedStateTypeString, workflow.SharedStateTypeArtifactRef:
		_, ok := decoded.(string)
		return ok
	case workflow.SharedStateTypeNumber:
		_, ok := decoded.(float64)
		return ok
	case workflow.SharedStateTypeBoolean:
		_, ok := decoded.(bool)
		return ok
	case workflow.SharedStateTypeObject:
		_, ok := decoded.(map[string]any)
		return ok
	case workflow.SharedStateTypeArray:
		_, ok := decoded.([]any)
		return ok
	default:
		return false
	}
}

// mergeSharedStateValue applies field's own declared MergeRule between its
// existing value (may be absent/empty) and an already type-checked
// incoming value.
func mergeSharedStateValue(field workflow.SharedStateField, existing, incoming json.RawMessage) (json.RawMessage, error) {
	switch field.MergeRule {
	case workflow.MergeRuleLastWriteWins:
		return incoming, nil
	case workflow.MergeRuleAppend:
		var existingArr []json.RawMessage
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &existingArr); err != nil {
				return nil, fmt.Errorf("runtime: field %q existing value is not an array for APPEND merge: %w", field.Name, err)
			}
		}
		var incomingArr []json.RawMessage
		if err := json.Unmarshal(incoming, &incomingArr); err != nil {
			return nil, fmt.Errorf("%w: field %q APPEND merge requires an array value", ErrSharedStateTypeMismatch, field.Name)
		}
		mergedArr, err := json.Marshal(append(existingArr, incomingArr...))
		if err != nil {
			return nil, fmt.Errorf("runtime: marshal appended field %q: %w", field.Name, err)
		}
		return mergedArr, nil
	case workflow.MergeRuleRejectOnConflict:
		if len(existing) > 0 && string(existing) != "null" {
			return nil, fmt.Errorf("%w: field %q", ErrSharedStateMergeConflict, field.Name)
		}
		return incoming, nil
	default:
		return nil, fmt.Errorf("runtime: field %q has unsupported merge rule %q", field.Name, field.MergeRule)
	}
}
