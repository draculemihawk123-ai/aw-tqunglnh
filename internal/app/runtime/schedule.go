// ScheduleExecutableNodeRun is V4-04's own scheduling transaction
// (docs/design/06-v4-runtime-engine.md): "resolve effective scope/profile/
// context inputs và exact manifest revision; QUEUED states; idempotency by
// NodeRun activation". It is the sole consumer this codebase builds for the
// ScheduleNodeRunJobKind durable job AdvanceRun (V4-03, advance.go) enqueues
// whenever the downstream node it just activated PENDING is executable
// (AGENT/COMMAND/MACHINE_GATE) — the same "enqueue only, a separate later
// task is the sole consumer" relationship AdvanceRunJobKind itself had
// before this task built AdvanceRun.
//
// Two-phase, per ADR-027 (confirmed with the user before writing this
// file): the caller-injected ports.RuntimeExecutionConfigProvider is
// resolved OUTSIDE any database transaction (phase 1), and this function
// itself canonicalizes/hashes that snapshot — never trusting a raw hash a
// caller might supply, which would invert authority the same way a
// caller-declared ProjectID would. Only once that snapshot is in hand does
// phase 2 open ports.UnitOfWork.WithSerializedWrite and re-check every
// pin (Run/NodeRun/WorkflowVersion) fresh from storage. A missing provider
// or an invalid snapshot fails closed before phase 2 ever opens — no
// Attempt, no job, ever created from an unverifiable config.
//
// Idempotency mirrors AdvanceRun's own "idempotent early-return" discipline
// (workspaceprovision.Handler's pattern, not the Command/Receipts envelope):
// the first thing phase 2 does is read the target NodeRun's current State —
// anything other than PENDING means an earlier delivery of the same
// ScheduleNodeRunJobKind job already scheduled this NodeRun (or it was never
// eligible), so this call returns a no-op Scheduled=false result rather than
// re-resolving the profile or attempting to recreate an ExecutionAttempt the
// UNIQUE(node_run_id, attempt_no) constraint would otherwise reject as a raw
// DB error — the same "queue delivery lặp không tạo duplicate attempt" bar
// this task's own Hoàn thành khi line names.
//
// A single NODE_SCHEDULED domain event is appended in the same transaction
// as the NodeRun PENDING->QUEUED transition, the ExecutionAttempt insert and
// the EXECUTE_NODE follow-up job (GC-INV-15) — registered with
// internal/app/eventschema from this same changeset (event_schema.go), not
// deferred the way NODE_ROUTED's own registration was the first time
// (correction found during V4-03 review; this task does not repeat it).
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

var (
	// ErrRuntimeExecutionConfigProviderRequired is returned when no
	// ports.RuntimeExecutionConfigProvider was supplied at all — ADR-027's
	// own "thiếu provider... fail closed, không tạo Attempt/job".
	ErrRuntimeExecutionConfigProviderRequired = errors.New("runtime: a RuntimeExecutionConfigProvider is required to schedule an executable node run")
	// ErrRuntimeExecutionConfigUnavailable wraps whatever error the
	// provider itself returned — ADR-027's own "snapshot không hợp lệ ->
	// fail closed".
	ErrRuntimeExecutionConfigUnavailable = errors.New("runtime: runtime execution config snapshot is unavailable")
	// ErrNodeNotExecutable is a defensive, should-be-unreachable error:
	// AdvanceRun only ever enqueues a ScheduleNodeRunJobKind job for a node
	// whose type is already AGENT/COMMAND/MACHINE_GATE (isExecutableNode,
	// advance.go). This exists so a corrupted/foreign job payload or a
	// WorkflowVersion mutated out from under a NodeRun fails closed here
	// rather than panicking.
	ErrNodeNotExecutable = errors.New("runtime: node is not an executable node type (AGENT, COMMAND or MACHINE_GATE)")
	// ErrAdapterBuildUnresolved is the fail-closed rejection the round-1
	// correction review required: "V4-04 phải fail closed nếu không resolve
	// được exact build; Attempt không được tạo với build không pin". Only
	// fires when a node DECLARES an AdapterBuildID that this registry
	// cannot resolve — an AGENT node that declares none at all is a
	// legitimate, deliberately deferred Alpha state (see
	// ResolvedExecutionProfileV1.AdapterBuild's own doc comment) and is not
	// this error's concern.
	ErrAdapterBuildUnresolved = errors.New("runtime: node declares an adapter build id that could not be resolved to an exact registered build")
	// ErrAttemptPolicyRequired is this task's own fail-closed scoping
	// decision: an executable node with no ATTEMPT-category PolicyRef pins
	// no TimeoutSeconds, and ResolvedExecutionProfileV1 already requires a
	// positive timeout (GC-INV-08) — an execution that could run forever is
	// exactly the failure mode a fail-closed rejection here prevents,
	// consistent with ADR-027's own "missing/invalid input fails closed"
	// posture for this same task.
	ErrAttemptPolicyRequired = errors.New("runtime: node has no ATTEMPT-category policy pinning a timeout")
	// ErrPermissionPolicyRequired mirrors ErrAttemptPolicyRequired for
	// isolation: an executable node with no PERMISSION-category PolicyRef
	// pins no IsolationTier, and ADR-013's two tiers are both explicit,
	// deliberate trust decisions — never a silent default.
	ErrPermissionPolicyRequired = errors.New("runtime: node has no PERMISSION-category policy pinning an isolation tier")
)

// ScheduleNodeRunJobKind is the durable job AdvanceRun (V4-03) enqueues when
// the downstream node it just activated PENDING is executable. See this
// file's own package doc comment.
const ScheduleNodeRunJobKind = "SCHEDULE_NODE_RUN"

const defaultScheduleNodeRunJobMaxClaims = 3

// ScheduleNodeRunJobPayload is the exact JSON shape AdvanceRun marshals for
// a ScheduleNodeRunJobKind job and NodeSchedulingHandler unmarshals —
// defined once here so producer and consumer can never drift.
type ScheduleNodeRunJobPayload struct {
	RunID     string `json:"runId"`
	NodeRunID string `json:"nodeRunId"`
	// CorrelationID threads AdvanceRun's own req.CorrelationID through —
	// see AdvanceRunJobPayload's own doc comment for the full chain this
	// extends.
	CorrelationID string `json:"correlationId,omitempty"`
}

// ExecuteNodeJobKind is the durable job this function itself enqueues once
// it creates a NodeRun's first ExecutionAttempt — V4-05's own future
// consumer ("job claim, attempt RUNNING, ... fenced finalize"). No handler
// for it exists yet in this codebase; it sits AVAILABLE until V4-05 builds
// one, the same "enqueue only, a separate later task is the sole consumer"
// discipline this whole codebase already established repeatedly.
const ExecuteNodeJobKind = "EXECUTE_NODE"

const defaultExecuteNodeJobMaxClaims = 3

// ExecuteNodeJobPayload is the exact JSON shape this file marshals for an
// ExecuteNodeJobKind job.
type ExecuteNodeJobPayload struct {
	RunID         string `json:"runId"`
	NodeRunID     string `json:"nodeRunId"`
	AttemptID     string `json:"attemptId"`
	CorrelationID string `json:"correlationId,omitempty"`
}

// ScheduleExecutableNodeRunRequest is what a caller supplies to
// ScheduleExecutableNodeRun.
type ScheduleExecutableNodeRunRequest struct {
	RunID     string
	NodeRunID string
	// CorrelationID threads a broader command/event chain through this
	// call's own NODE_SCHEDULED event and its EXECUTE_NODE follow-up job —
	// see AdvanceRunJobPayload's own doc comment for the full chain.
	CorrelationID string
	// JobID names the ScheduleNodeRunJobKind job that drove this call
	// (NodeSchedulingHandler passes the job.ID it was handed) — go-core-
	// spec §20's own "Mọi log/event có... JobID khi có".
	JobID string
}

// ScheduleExecutableNodeRunResult reports what one scheduling call actually
// did.
type ScheduleExecutableNodeRunResult struct {
	// Scheduled is false only for the idempotent-replay no-op: the target
	// NodeRun was already scheduled (or further advanced) by an earlier
	// call/delivery.
	Scheduled bool
	NodeRunID string
	// AttemptID/ExecutionProfileHash/ExecuteJobID describe the
	// ExecutionAttempt this call created. All empty for the no-op case.
	AttemptID            string
	ExecutionProfileHash string
	ExecuteJobID         string
}

// ScheduleExecutableNodeRun performs exactly one scheduling transition:
// PENDING -> QUEUED, plus the NodeRun's first ExecutionAttempt and its
// EXECUTE_NODE follow-up job. See this file's own package doc comment for
// the full two-phase design.
func ScheduleExecutableNodeRun(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source,
	provider ports.RuntimeExecutionConfigProvider, req ScheduleExecutableNodeRunRequest,
) (ScheduleExecutableNodeRunResult, error) {
	if req.RunID == "" || req.NodeRunID == "" {
		return ScheduleExecutableNodeRunResult{}, errors.New("runtime: RunID and NodeRunID are required")
	}
	if provider == nil {
		return ScheduleExecutableNodeRunResult{}, ErrRuntimeExecutionConfigProviderRequired
	}

	// Phase 1: resolve and hash the runtime execution config snapshot
	// entirely outside any database transaction (ADR-027).
	snapshotInput, err := provider.Resolve(ctx)
	if err != nil {
		return ScheduleExecutableNodeRunResult{}, fmt.Errorf("%w: %v", ErrRuntimeExecutionConfigUnavailable, err)
	}
	_, runtimeExecutionConfigHash, err := runtimedomain.NewRuntimeExecutionConfigSnapshotV1(snapshotInput)
	if err != nil {
		return ScheduleExecutableNodeRunResult{}, fmt.Errorf("%w: %v", ErrRuntimeExecutionConfigUnavailable, err)
	}

	var result ScheduleExecutableNodeRunResult
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		// Phase 2: re-check every pin fresh from storage.
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
		if err != nil {
			return err
		}
		if string(nodeRun.RunID) != req.RunID {
			return fmt.Errorf("%w: node run %s belongs to run %s, not %s", ErrNodeRunMismatch, req.NodeRunID, nodeRun.RunID, req.RunID)
		}
		if nodeRun.State != runtimedomain.NodeRunPending {
			// Idempotent no-op — see this file's own package doc comment.
			result = ScheduleExecutableNodeRunResult{Scheduled: false, NodeRunID: req.NodeRunID}
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

		node, ok := findNode(document, nodeRun.NodeKey)
		if !ok {
			return fmt.Errorf("runtime: node %s not found in workflow version %s", nodeRun.NodeKey, run.WorkflowVersionID)
		}
		if !isExecutableNode(node.Type) {
			return fmt.Errorf("%w: node %s is type %s", ErrNodeNotExecutable, node.Key, node.Type)
		}

		profile, err := resolveExecutionProfile(ctx, tx, node, runtimeExecutionConfigHash)
		if err != nil {
			return err
		}
		normalizedProfile, profileHash, err := runtimedomain.NewResolvedExecutionProfileV1(profile)
		if err != nil {
			return err
		}

		effectiveScope, err := tx.Work().ListWorkItemEffectiveScopes(ctx, string(run.WorkItemID))
		if err != nil {
			return err
		}

		amendments, err := tx.Runtime().ListRunManifestAmendments(ctx, req.RunID)
		if err != nil {
			return err
		}
		var manifestRevision uint64
		if len(amendments) > 0 {
			manifestRevision = amendments[len(amendments)-1].Revision
		}

		manifest, err := tx.Runtime().GetExecutionManifest(ctx, req.RunID)
		if err != nil {
			return err
		}

		canonicalProfileJSON, err := json.Marshal(normalizedProfile)
		if err != nil {
			return fmt.Errorf("marshal canonical execution profile: %w", err)
		}
		decisionInput, err := json.Marshal(struct {
			RunID     string `json:"runId"`
			NodeRunID string `json:"nodeRunId"`
			NodeKey   string `json:"nodeKey"`
		}{RunID: req.RunID, NodeRunID: req.NodeRunID, NodeKey: node.Key})
		if err != nil {
			return fmt.Errorf("marshal execution profile decision input: %w", err)
		}
		// The DecisionArtifact ID is deterministic (keyed by NodeRunID), not
		// ids.NewID() — a NodeRun is scheduled at most once (this whole
		// function's own idempotent-early-return guards that), so this stays
		// collision-free, and it lets a later reader (V4-05's own execution
		// envelope, which needs this profile's own TimeoutSeconds to bound
		// its execution deadline) look the artifact up directly by NodeRunID
		// without needing a separate stored back-reference anywhere.
		decision, err := runtimedomain.NewDecisionArtifact(
			runtimedomain.DecisionArtifactID(req.NodeRunID+"-execution-profile-v1"), run.ProjectID, "EXECUTION_PROFILE_V1", "v1",
			decisionInput, canonicalProfileJSON, time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().RecordDecisionArtifact(ctx, decision); err != nil {
			return err
		}

		scheduled, err := tx.Runtime().ScheduleNodeRun(ctx, ports.ScheduleNodeRunRequest{
			NodeRunID: req.NodeRunID, ExpectedVersion: nodeRun.Version,
			EffectiveScope: effectiveScope, ExecutionProfileHash: profileHash, ManifestRevision: manifestRevision,
		})
		if err != nil {
			return err
		}

		baseRevisionSet := manifest.BaseRevisionSet
		attemptID := ids.NewID()
		attempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID(attemptID), scheduled.ID, 1, profileHash, normalizedProfile.ProviderKey, &baseRevisionSet,
		)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().CreateExecutionAttempt(ctx, attempt); err != nil {
			return err
		}

		eventPayload, err := json.Marshal(nodeScheduledEventPayload{
			RunID: req.RunID, WorkItemID: string(run.WorkItemID), NodeRunID: req.NodeRunID, NodeKey: node.Key,
			ExecutorKind: string(normalizedProfile.Executor.Kind), AttemptID: attemptID,
			ExecutionProfileHash: profileHash, JobID: req.JobID,
		})
		if err != nil {
			return fmt.Errorf("marshal NODE_SCHEDULED event payload: %w", err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: req.NodeRunID + "-scheduled", ProjectID: string(run.ProjectID),
			AggregateType: "NodeRun", AggregateID: req.NodeRunID, Sequence: int64(scheduled.Version),
			EventType: NodeScheduledEventType, SchemaVersion: NodeScheduledSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: req.CorrelationID, CreatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}

		jobPayload, err := json.Marshal(ExecuteNodeJobPayload{
			RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: attemptID, CorrelationID: req.CorrelationID,
		})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", ExecuteNodeJobKind, err)
		}
		// AggregateType/AggregateID are the ExecutionAttempt itself, not the
		// NodeRun — correction found during V4-05 scoping review: a NodeRun
		// keys ONE EXECUTE_NODE job only as long as it never has more than
		// one Attempt, but V4-06's own technical retry policy creates a NEW
		// Attempt (same NodeRun, next AttemptNumber) after a retryable
		// failure. Keying this job's own fencing identity to NodeRunID would
		// let a stale job/lease from a PRIOR attempt appear to authorize a
		// later one — the exact ambient-authority gap V4-05's own fenced
		// finalize (schedule.go's sibling, finalize.go) exists to close.
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(ids.NewID()), ProjectID: run.ProjectID, Kind: ExecuteNodeJobKind,
			AggregateType: "ExecutionAttempt", AggregateID: attemptID, Payload: jobPayload,
			MaxClaims: defaultExecuteNodeJobMaxClaims, IdempotencyKey: "execute-" + attemptID,
		})
		if err != nil {
			return err
		}

		result = ScheduleExecutableNodeRunResult{
			Scheduled: true, NodeRunID: req.NodeRunID, AttemptID: attemptID,
			ExecutionProfileHash: profileHash, ExecuteJobID: string(job.ID),
		}
		return nil
	})
	return result, err
}

// resolveExecutionProfile builds the not-yet-normalized
// ResolvedExecutionProfileV1 for node by resolving every definition/policy
// pin it declares against tx — the "resolver" ResolvedExecutionProfileV1's
// own doc comment names as this task's scope. Every LoadVersion/Get failure
// (an unresolvable pin) propagates unchanged: a node whose declared
// executor/policy/adapter build cannot be resolved fails this whole
// scheduling attempt closed, never with a partially-populated profile.
func resolveExecutionProfile(
	ctx context.Context, tx ports.Tx, node workflow.Node, runtimeExecutionConfigHash string,
) (runtimedomain.ResolvedExecutionProfileV1, error) {
	profile := runtimedomain.ResolvedExecutionProfileV1{
		SchemaVersion: 1, RuntimeExecutionConfigHash: runtimeExecutionConfigHash,
	}

	var policyRefs []definition.DependencyPin
	switch node.Type {
	case workflow.NodeAgent:
		if node.Agent == nil {
			return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("runtime: node %s declares AGENT type with no Agent config", node.Key)
		}
		executorVersion, err := tx.Definitions().LoadVersion(ctx, node.Agent.ProfileRef.VersionID)
		if err != nil {
			return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("runtime: resolve node %s agent profile: %w", node.Key, err)
		}
		agentDoc, err := decodeCompiledAgentProfile(executorVersion.CompiledSnapshot())
		if err != nil {
			return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("runtime: node %s: %w", node.Key, err)
		}
		profile.Executor = runtimedomain.ResolvedExecutorRef{
			Kind: runtimedomain.ExecutorKindAgent, DefinitionID: executorVersion.DefinitionID(),
			VersionID: executorVersion.ID(), CompiledHash: executorVersion.CompiledHash(),
		}
		profile.ProviderKey = agentDoc.ProviderKey
		profile.Model = agentDoc.Model
		profile.ToolRefs = append([]string(nil), agentDoc.ToolRefs...)
		profile.MaxTokens = agentDoc.Budget.MaxTokens
		policyRefs = node.Agent.PolicyRefs
		if node.Agent.AdapterBuildID != nil {
			build, err := tx.AdapterBuilds().Get(ctx, *node.Agent.AdapterBuildID)
			if err != nil {
				return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("%w: node %s build %s: %v", ErrAdapterBuildUnresolved, node.Key, *node.Agent.AdapterBuildID, err)
			}
			tuple := build.Tuple()
			profile.AdapterBuild = &runtimedomain.ResolvedAdapterBuildRef{
				BuildID: build.ID(), ProtocolVersion: tuple.ProtocolVersion, CapabilityHash: tuple.CapabilityManifestHash,
			}
		}
	case workflow.NodeCommand:
		if node.Command == nil {
			return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("runtime: node %s declares COMMAND type with no Command config", node.Key)
		}
		executorVersion, err := tx.Definitions().LoadVersion(ctx, node.Command.CommandRef.VersionID)
		if err != nil {
			return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("runtime: resolve node %s command: %w", node.Key, err)
		}
		profile.Executor = runtimedomain.ResolvedExecutorRef{
			Kind: runtimedomain.ExecutorKindCommand, DefinitionID: executorVersion.DefinitionID(),
			VersionID: executorVersion.ID(), CompiledHash: executorVersion.CompiledHash(),
		}
		policyRefs = node.Command.PolicyRefs
	case workflow.NodeMachineGate:
		if node.MachineGate == nil {
			return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("runtime: node %s declares MACHINE_GATE type with no MachineGate config", node.Key)
		}
		executorVersion, err := tx.Definitions().LoadVersion(ctx, node.MachineGate.GateRef.VersionID)
		if err != nil {
			return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("runtime: resolve node %s gate: %w", node.Key, err)
		}
		profile.Executor = runtimedomain.ResolvedExecutorRef{
			Kind: runtimedomain.ExecutorKindMachineGate, DefinitionID: executorVersion.DefinitionID(),
			VersionID: executorVersion.ID(), CompiledHash: executorVersion.CompiledHash(),
		}
		policyRefs = node.MachineGate.PolicyRefs
	default:
		return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("%w: node %s is type %s", ErrNodeNotExecutable, node.Key, node.Type)
	}

	var timeoutSeconds uint32
	var isolationTier policy.IsolationTier
	var allowedCapabilities []string
	var isolationPinned bool
	for _, ref := range policyRefs {
		policyVersion, err := tx.Definitions().LoadVersion(ctx, ref.VersionID)
		if err != nil {
			return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("runtime: resolve node %s policy %s: %w", node.Key, ref.VersionID, err)
		}
		policyDoc, err := decodeCompiledPolicy(policyVersion.CompiledSnapshot())
		if err != nil {
			return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("runtime: node %s: %w", node.Key, err)
		}
		profile.Policies = append(profile.Policies, runtimedomain.ResolvedPolicyRef{
			DefinitionID: policyVersion.DefinitionID(), VersionID: policyVersion.ID(),
			Category: policyDoc.Category, CompiledHash: policyVersion.CompiledHash(),
		})
		switch policyDoc.Category {
		case policy.CategoryAttempt:
			if policyDoc.Attempt != nil {
				timeoutSeconds = policyDoc.Attempt.TimeoutSeconds
			}
		case policy.CategoryPermission:
			if policyDoc.Permission != nil {
				isolationTier = policyDoc.Permission.IsolationTier
				allowedCapabilities = append(allowedCapabilities, policyDoc.Permission.GrantedCapabilities...)
				isolationPinned = true
			}
		}
	}
	// Fail closed rather than silently defaulting: an executable node with
	// no ATTEMPT/PERMISSION policy pinned has no bounded timeout / no
	// declared trust tier, exactly the unbounded-execution and ambient-
	// trust failure modes this codebase's other invariants already guard
	// against elsewhere (this task's own scoping decision, ADR-027's
	// "missing/invalid input fails closed" posture applied to policy
	// resolution).
	if timeoutSeconds == 0 {
		return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("%w: node %s", ErrAttemptPolicyRequired, node.Key)
	}
	if !isolationPinned {
		return runtimedomain.ResolvedExecutionProfileV1{}, fmt.Errorf("%w: node %s", ErrPermissionPolicyRequired, node.Key)
	}
	profile.TimeoutSeconds = timeoutSeconds
	profile.IsolationTier = isolationTier
	profile.AllowedCapabilities = allowedCapabilities
	return profile, nil
}

// decodeCompiledAgentProfile unwraps an AgentProfileVersion's own
// CompiledSnapshot — internal/domain/agentprofile.Compile's own private
// compiledAgentProfileSnapshot shape, "{document: ..., dependencies:
// ...}" — into just its AgentProfileDocument half.
// agentprofile.DecodeDocument itself is deliberately NOT reusable here: it
// strictly decodes a bare, freshly-AUTHORED AgentProfileDocument
// (authoring.DecodeStrict rejects unknown top-level fields), whereas a
// CompiledSnapshot is that document wrapped alongside its dependency
// manifest — a different, already-compiled shape only this resolver needs
// to unwrap.
func decodeCompiledAgentProfile(compiledSnapshot string) (agentprofile.AgentProfileDocument, error) {
	var wrapper struct {
		Document agentprofile.AgentProfileDocument `json:"document"`
	}
	if err := json.Unmarshal([]byte(compiledSnapshot), &wrapper); err != nil {
		return agentprofile.AgentProfileDocument{}, fmt.Errorf("decode compiled agent profile snapshot: %w", err)
	}
	return wrapper.Document, nil
}

// decodeCompiledPolicy is decodeCompiledAgentProfile's own sibling for
// internal/domain/policy.Compile's private compiledPolicySnapshot shape.
func decodeCompiledPolicy(compiledSnapshot string) (policy.PolicyDocument, error) {
	var wrapper struct {
		Document policy.PolicyDocument `json:"document"`
	}
	if err := json.Unmarshal([]byte(compiledSnapshot), &wrapper); err != nil {
		return policy.PolicyDocument{}, fmt.Errorf("decode compiled policy snapshot: %w", err)
	}
	return wrapper.Document, nil
}

// NodeScheduledEventType/NodeScheduledSchemaVersion identify NODE_SCHEDULED's
// own registered (EventType, SchemaVersion) pair in
// internal/app/eventschema's registry (RegisterEventSchemas,
// event_schema.go).
const (
	NodeScheduledEventType     = "NODE_SCHEDULED"
	NodeScheduledSchemaVersion = 1
)

// nodeScheduledEventPayload is NODE_SCHEDULED's own JSON shape.
type nodeScheduledEventPayload struct {
	RunID                string `json:"runId"`
	WorkItemID           string `json:"workItemId"`
	NodeRunID            string `json:"nodeRunId"`
	NodeKey              string `json:"nodeKey"`
	ExecutorKind         string `json:"executorKind"`
	AttemptID            string `json:"attemptId"`
	ExecutionProfileHash string `json:"executionProfileHash"`
	// JobID is the ScheduleNodeRunJobKind job that drove this call (blank
	// if the caller did not supply one via
	// ScheduleExecutableNodeRunRequest.JobID).
	JobID string `json:"jobId,omitempty"`
}

// isExecutableNode reports whether nodeType is one of the three node types
// that dispatch a real ExecutionAttempt (V4-04's own scope) rather than
// resolving structurally (START/ROUTER, AdvanceRun's own
// isStructuralRoutingNode) or waiting on some other later task's own
// dispatcher (APPROVAL/WAIT/FORK/JOIN/END).
func isExecutableNode(nodeType workflow.NodeType) bool {
	return nodeType == workflow.NodeAgent || nodeType == workflow.NodeCommand || nodeType == workflow.NodeMachineGate
}
