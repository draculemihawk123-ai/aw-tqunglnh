package workflow

import (
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// AgentNodeConfig is an AGENT node's typed config
// (docs/design/04-v2-definition-plane.md V2-08's own design guidance):
// it pins the exact AgentProfileVersion this node runs under, plus the
// policies (e.g. an ATTEMPT-category Policy — see this field's own doc
// comment) that govern one execution attempt. This is the concrete
// replacement for the old bare ExecutorRef string: a compiled
// WorkflowVersion now carries the exact definition ID + version ID a
// runtime resolves, never a string a runtime would have to reinterpret.
type AgentNodeConfig struct {
	// ProfileRef pins the exact AgentProfileVersion this node executes
	// under. Kind must be definition.KindAgentProfile — see this
	// package's own validation.go for why V2-08 pins the concrete
	// AgentProfileVersion directly rather than mediating through a
	// Block (internal/domain/block's own package doc comment frames
	// every executing node as referencing a BlockVersion; V2-08's own
	// design guidance instead names the AgentProfileVersion/
	// CommandVersion/GateVersion directly per node type, and this
	// package follows that guidance literally to keep the pin target
	// unambiguous for Alpha).
	ProfileRef definition.DependencyPin `json:"profileRef"`
	// PolicyRefs pins the policies (attempt/permission/etc.) this node's
	// execution runs under. Every pin's Kind must be
	// definition.KindPolicy — the same PolicyRefs convention
	// internal/domain/block.BlockDocument and
	// internal/domain/command.CommandDocument already use. This package
	// cannot additionally verify a pinned Policy's own Category (e.g.
	// that an "attempt policy" pin's Category is actually ATTEMPT) at
	// authoring time — the same limitation
	// internal/domain/agentprofile.AgentProfileDocument.ContextPolicyRef
	// already documents; resolving and checking that is V2-09's job.
	PolicyRefs []definition.DependencyPin `json:"policyRefs,omitempty"`
	// AdapterBuildID optionally pins the exact, content-addressed
	// AdapterBuildVersion (internal/domain/adapterbuild.Build.ID()) this
	// node's provider must run — ADR-012's "Run pin expected version" bar
	// for the provider-adapter axis, finally reachable from a node
	// (V2-07A/B built the registry and its resolvable Build.ID() identity
	// but deliberately never gave any authoring schema a field
	// referencing one; V2-09's own "resolve exact ... AdapterBuildVersion
	// pins" line is the first task whose scope actually calls for one).
	// It is NOT a definition.DependencyPin: AdapterBuildVersion is
	// deliberately not a DefinitionKind (ADR-022) and is keyed by a
	// content hash of its own measured tuple, never a DefinitionID+
	// VersionID pair, so reusing DependencyPin's shape here would imply a
	// resolution mechanism that does not exist. Left optional (a nil
	// value is not validated further) rather than required: not pinning
	// one is a legitimate, deliberately deferred Alpha state — the
	// registry itself is optional infrastructure until an operator
	// actually registers a build (V2-07B) and a workflow author chooses
	// to pin it.
	AdapterBuildID *string `json:"adapterBuildId,omitempty"`
}

func (c *AgentNodeConfig) clone() *AgentNodeConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.PolicyRefs = append([]definition.DependencyPin(nil), c.PolicyRefs...)
	if c.AdapterBuildID != nil {
		id := *c.AdapterBuildID
		cloned.AdapterBuildID = &id
	}
	return &cloned
}

// CommandNodeConfig is a COMMAND node's typed config: it pins the exact
// CommandVersion this node runs, plus this node's own policy refs. See
// AgentNodeConfig's doc comment for the same reasoning applied here.
type CommandNodeConfig struct {
	// CommandRef pins the exact CommandVersion this node executes. Kind
	// must be definition.KindCommand.
	CommandRef definition.DependencyPin `json:"commandRef"`
	// PolicyRefs pins the policies this node's execution runs under.
	// Every pin's Kind must be definition.KindPolicy.
	PolicyRefs []definition.DependencyPin `json:"policyRefs,omitempty"`
}

func (c *CommandNodeConfig) clone() *CommandNodeConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.PolicyRefs = append([]definition.DependencyPin(nil), c.PolicyRefs...)
	return &cloned
}

// MachineGateNodeConfig is a MACHINE_GATE node's typed config: it pins
// the exact GateVersion this node evaluates, plus this node's own policy
// refs. See AgentNodeConfig's doc comment for the same reasoning applied
// here.
type MachineGateNodeConfig struct {
	// GateRef pins the exact GateVersion this node evaluates. Kind must
	// be definition.KindGate.
	GateRef definition.DependencyPin `json:"gateRef"`
	// PolicyRefs pins the policies this node's evaluation runs under.
	// Every pin's Kind must be definition.KindPolicy.
	PolicyRefs []definition.DependencyPin `json:"policyRefs,omitempty"`
}

func (c *MachineGateNodeConfig) clone() *MachineGateNodeConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.PolicyRefs = append([]definition.DependencyPin(nil), c.PolicyRefs...)
	return &cloned
}

// ApprovalNodeConfig is an APPROVAL node's (HE-14's HUMAN_TASK/APPROVAL)
// typed config. An approval is never backed by a published executable
// definition — there is nothing to pin a definition.DependencyPin at —
// so this package instead models the vocabulary
// docs/harness-engineering/14-lec-14-do-thi-dieu-phoi.md's HE-14-S03
// names for a human node: "requested evidence, authorized roles, timeout
// và escalation." HE-14-S03 is a SHOULD, not a MUST, but it is the only
// concrete vocabulary the lecture text gives for a human task, so this
// package adopts it as APPROVAL's minimal, defensible typed config
// rather than inventing an unfounded shape.
type ApprovalNodeConfig struct {
	// AuthorizedRoles is the closed, non-empty set of role names allowed
	// to resolve this approval (HE-14-S03's "authorized roles"). At
	// least one is required: an approval nobody is authorized to
	// resolve could never complete.
	AuthorizedRoles []string `json:"authorizedRoles"`
	// TimeoutSeconds is how long this approval waits for an authorized
	// role to respond before EscalationOutcome applies (HE-14-S03's
	// "timeout"). Required, and must be positive — an approval with no
	// timeout could wait forever, exactly the kind of unbounded wait
	// docs/harness-engineering/14-lec-14-do-thi-dieu-phoi.md's failure
	// modes warn against.
	TimeoutSeconds uint32 `json:"timeoutSeconds"`
	// EscalationOutcome is which of this node's declared Outcomes fires
	// when TimeoutSeconds elapses without a response (HE-14-S03's
	// "escalation"). Required now (V4-09, correction found while building
	// this task's own real consumer — this field used to be documented as
	// optional): TimeoutSeconds is itself already mandatory, so a declared
	// timeout with nowhere to route once it actually fires would silently
	// reintroduce the exact unbounded wait requiring TimeoutSeconds exists
	// to prevent — there is no "standardized timeout outcome" fallback
	// convention anywhere in this codebase to lean on instead. Must name
	// an outcome this node itself declares (validation.go checks this the
	// same way CyclePolicy's own EscalationOutcome already does).
	EscalationOutcome string `json:"escalationOutcome"`
	// RequestedEvidenceKinds is the set of Evidence.Kind values shown to
	// an authorized approver alongside the request (HE-14-S03's
	// "requested evidence") — the same Evidence.Kind vocabulary
	// internal/domain/policy.CompletionRules.RequiredEvidenceKinds
	// already references. Optional: an approval may need no supporting
	// evidence beyond the request itself.
	RequestedEvidenceKinds []string `json:"requestedEvidenceKinds,omitempty"`
}

func (c *ApprovalNodeConfig) clone() *ApprovalNodeConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.AuthorizedRoles = append([]string(nil), c.AuthorizedRoles...)
	cloned.RequestedEvidenceKinds = append([]string(nil), c.RequestedEvidenceKinds...)
	return &cloned
}

// WaitMode is which of the two things a WAIT node
// (HE-14's WAIT/SIGNAL) concretely waits on.
type WaitMode string

const (
	// WaitModeDuration waits a fixed span of time before proceeding.
	WaitModeDuration WaitMode = "DURATION"
	// WaitModeSignal waits for a named external signal to arrive — the
	// "SIGNAL" half of HE-14's "WAIT/SIGNAL" node type.
	WaitModeSignal WaitMode = "SIGNAL"
)

// WaitNodeConfig is a WAIT node's typed config. HE-14's lecture text
// only ever lists "WAIT/SIGNAL" as one paired node-type name, without
// elaborating a concrete field shape; this package's own minimal,
// defensible reading of that pairing is that a WAIT node waits on
// exactly one of two things — a fixed duration, or a named external
// signal — and models both as one closed Mode choice rather than two
// separate node types, matching V2-08's own Mục tiêu line naming a
// single "WAIT" type.
type WaitNodeConfig struct {
	Mode WaitMode `json:"mode"`
	// DurationSeconds is how long to wait when Mode == DURATION.
	// Required (and must be positive) for DURATION; must be zero for
	// SIGNAL, where TimeoutSeconds (not this field) is the wait's own
	// ceiling.
	DurationSeconds uint32 `json:"durationSeconds,omitempty"`
	// SignalName is the external signal name to wait for when
	// Mode == SIGNAL. Required (non-empty) for SIGNAL; must be empty for
	// DURATION.
	SignalName string `json:"signalName,omitempty"`
	// TimeoutSeconds is an optional ceiling on how long a SIGNAL wait
	// may run before it is considered timed out (mirroring
	// ApprovalNodeConfig.TimeoutSeconds' same reasoning: an unbounded
	// wait for a signal that never arrives is the same structural
	// failure mode this package guards against elsewhere). Meaningless
	// for DURATION, whose own DurationSeconds is already its ceiling —
	// must be zero there.
	TimeoutSeconds uint32 `json:"timeoutSeconds,omitempty"`
	// CompletionOutcome/TimeoutOutcome are populated now (V4-08, confirmed
	// with the user before writing this task's code): which of this
	// node's own declared Outcomes fires on normal completion (DURATION
	// reaching its own DurationSeconds; SIGNAL receiving the expected
	// signal) versus on a SIGNAL wait's own TimeoutSeconds elapsing —
	// mirroring the exact ApprovalNodeConfig.EscalationOutcome pattern
	// this package already established, rather than inventing a
	// convention-based/"magic string" outcome name. Both are optional
	// ONLY when this node declares exactly one Outcome (unambiguous by
	// construction); validateWaitConfig requires an explicit,
	// already-declared value for either field the moment this node
	// declares more than one Outcome AND that field's own completion path
	// is actually reachable (TimeoutOutcome only when Mode is SIGNAL and
	// TimeoutSeconds > 0). This ambiguity is rejected at compile/publish
	// time, never deferred to schedule/signal time — the runtime that
	// eventually routes a resolved WAIT (SignalWait, the timer job) never
	// chooses an outcome itself; it only races a CAS on the wait's own
	// registration, and the winner routes using whichever of these two
	// fields this already-immutable, published WorkflowVersion pinned.
	CompletionOutcome string `json:"completionOutcome,omitempty"`
	// TimeoutOutcome must stay empty for DURATION (reaching the duration
	// is normal completion, never a timeout) and for a SIGNAL wait with no
	// TimeoutSeconds ceiling (nothing to time out against) — see
	// CompletionOutcome's own doc comment above for the shared contract.
	TimeoutOutcome string `json:"timeoutOutcome,omitempty"`
}

func (c *WaitNodeConfig) clone() *WaitNodeConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	return &cloned
}

// JoinMode is the closed set of join semantics
// docs/harness-engineering/14-lec-14-do-thi-dieu-phoi.md's "Fork/join và
// task family" section names: "Join phải khai báo ALL, ANY, QUORUM hoặc
// policy tùy chỉnh có version." This package implements the three named
// built-in modes; the lecture's fourth option — "a custom versioned
// policy" — is deliberately left out of V2-08's own scope (it would need
// its own DependencyPin-pinned policy shape, and "graph designer không
// phải mục tiêu alpha đầu tiên" — a fully general custom join policy is
// exactly the kind of elaborate, not-yet-grounded mechanism this
// session's design guidance says to avoid inventing).
type JoinMode string

const (
	// JoinModeAll requires every forked branch to complete before this
	// JOIN proceeds.
	JoinModeAll JoinMode = "ALL"
	// JoinModeAny requires only the first forked branch to complete.
	JoinModeAny JoinMode = "ANY"
	// JoinModeQuorum requires exactly QuorumCount forked branches to
	// complete.
	JoinModeQuorum JoinMode = "QUORUM"
)

// JoinNodeConfig is a JOIN node's typed config: which of the branches
// fanned out by a FORK must complete before this JOIN proceeds (this
// task's own "join policies" requirement). What happens to branches
// still running once the threshold is met (e.g. whether they are
// cancelled or left to finish) is deliberately not modeled here — that
// is scheduling/runtime behavior for whichever engine executes the
// graph (V4/V5), not an authoring-time policy declaration.
type JoinNodeConfig struct {
	Mode JoinMode `json:"mode"`
	// QuorumCount is how many branches must complete when
	// Mode == QUORUM. Required (and must be positive) for QUORUM; must
	// be zero for ALL and ANY, whose thresholds are implied by Mode
	// alone. This package deliberately does not cross-check QuorumCount
	// against this JOIN node's actual incoming-edge count: that is
	// graph-topology-dependent fork/join semantics validation
	// (HE-14-M06), which docs/design/04-v2-definition-plane.md's own
	// V2-09 "Graph/dependency compiler" section explicitly scopes to
	// itself ("fork-join" is one of the checks V2-09's own Thực hiện
	// line lists), not V2-08's authoring-schema validation.
	QuorumCount uint32 `json:"quorumCount,omitempty"`
}

func (c *JoinNodeConfig) clone() *JoinNodeConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	return &cloned
}

// SharedStateFieldType is the closed set of value types a declared
// shared-state field may carry (HE-14-M04's "mỗi field MUST có type").
type SharedStateFieldType string

const (
	SharedStateTypeString  SharedStateFieldType = "STRING"
	SharedStateTypeNumber  SharedStateFieldType = "NUMBER"
	SharedStateTypeBoolean SharedStateFieldType = "BOOLEAN"
	SharedStateTypeObject  SharedStateFieldType = "OBJECT"
	SharedStateTypeArray   SharedStateFieldType = "ARRAY"
	// SharedStateTypeArtifactRef is HE-14-M04's own carve-out: "artifact
	// lớn lưu bằng reference" ("large artifacts stored by reference").
	// A field of this type never carries the artifact's own content —
	// only a reference (e.g. an Evidence/artifact identifier) a runtime
	// resolves separately. This package only declares the type; the
	// runtime enforcement that a field's actual value is a reference,
	// never inlined content, is out of this authoring-schema task's own
	// scope (see this package's own top-level doc comment on
	// SharedStateField).
	SharedStateTypeArtifactRef SharedStateFieldType = "ARTIFACT_REF"
)

var validSharedStateFieldTypes = map[SharedStateFieldType]bool{
	SharedStateTypeString: true, SharedStateTypeNumber: true, SharedStateTypeBoolean: true,
	SharedStateTypeObject: true, SharedStateTypeArray: true, SharedStateTypeArtifactRef: true,
}

// MergeRule is the closed set of merge semantics a shared-state field
// declares for when more than one writer touches it (HE-14-M04's "merge
// rule"; "Shared state không có merge semantics" is explicitly listed
// among docs/harness-engineering/14-lec-14-do-thi-dieu-phoi.md's
// structural failure modes to avoid). The lecture text names no specific
// enum of merge rules, so this package's own minimal, defensible choice
// covers the three semantics HE-14-M04's own field vocabulary implies
// are needed: overwrite, accumulate, or refuse a conflicting write.
// Implementing the merge logic itself is a runtime concern (V4); this
// package only declares which rule governs each field.
type MergeRule string

const (
	// MergeRuleLastWriteWins overwrites the field with whichever write
	// lands last.
	MergeRuleLastWriteWins MergeRule = "LAST_WRITE_WINS"
	// MergeRuleAppend accumulates every write rather than overwriting.
	MergeRuleAppend MergeRule = "APPEND"
	// MergeRuleRejectOnConflict fails a write that would overwrite a
	// value a different writer already set, rather than silently
	// picking one.
	MergeRuleRejectOnConflict MergeRule = "REJECT_ON_CONFLICT"
)

var validMergeRules = map[MergeRule]bool{
	MergeRuleLastWriteWins: true, MergeRuleAppend: true, MergeRuleRejectOnConflict: true,
}

// SharedStateField is one declared field in a WorkflowDocument's shared
// state (HE-14-M04: "mỗi field MUST có type, owner/writers/readers và
// merge rule"). This is purely an authoring-time contract: this package
// declares the schema runtime shared state (today an opaque JSON blob —
// see internal/domain/runtime.WorkflowRun.SharedState) must honor later;
// it implements neither the merge logic nor any read/write enforcement
// itself (docs/design/04-v2-definition-plane.md V2-08's own scope is
// authoring schema, not runtime behavior).
type SharedStateField struct {
	// Name is this field's unique key within the document's shared
	// state. Required, and must be unique across SharedState.
	Name string `json:"name"`
	// Type is this field's declared value type.
	Type SharedStateFieldType `json:"type"`
	// Owner is the node key primarily responsible for this field —
	// HE-14-M04's "owner". Required, must name a node this document
	// actually declares, and must appear in Writers (an owner that
	// cannot even write its own field is a contradiction).
	Owner string `json:"owner"`
	// Writers is the closed allowlist of node keys permitted to write
	// this field — HE-14-M04's "writers", modeled as a closed allowlist
	// the same way internal/domain/command.CommandDocument.EnvAllowlist
	// already is. At least one (Owner itself) is required.
	Writers []string `json:"writers"`
	// Readers is the closed allowlist of node keys permitted to read
	// this field — HE-14-M04's "readers", the same closed-allowlist
	// convention as Writers. At least one is required: a field nothing
	// may ever read could never influence any node's behavior, and an
	// implicit "empty means everyone" default would silently widen
	// access every time a new node is added to the graph, exactly the
	// kind of ambient authority this codebase's allowlist convention
	// exists to avoid.
	Readers []string `json:"readers"`
	// MergeRule is this field's declared merge semantics for concurrent
	// writers.
	MergeRule MergeRule `json:"mergeRule"`
}

func cloneSharedStateFields(fields []SharedStateField) []SharedStateField {
	if fields == nil {
		return nil
	}
	cloned := make([]SharedStateField, len(fields))
	for i, field := range fields {
		cloned[i] = field
		cloned[i].Writers = append([]string(nil), field.Writers...)
		cloned[i].Readers = append([]string(nil), field.Readers...)
	}
	return cloned
}
