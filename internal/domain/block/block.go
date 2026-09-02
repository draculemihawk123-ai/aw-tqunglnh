// Package block is the BlockVersion contract
// (docs/design/04-v2-definition-plane.md V2-04, HE-14-M02): every graph
// node that actually does work (an AGENT/COMMAND/MACHINE_GATE/HUMAN_TASK
// node, in HE-14's node-type vocabulary) references a published
// BlockVersion, never an ad-hoc inline config — HE-14-M02's "typed node
// contract" requires every node to carry type, responsibility,
// input/output schema, allowed outcomes, tool/permission policy, timeout
// and done condition, and this package is where that contract actually
// lives as a versioned, hashable artifact.
//
// A Block never contains executable content itself: it only ever pins
// (via ExecutorRef) the one other DefinitionKind that is actually
// permitted to run — a Block, a Command, a Gate, or an Agent Profile
// (docs/design/01-system-design.md's "Quyền thực thi" column). Skill,
// Layer and Engineering Pack are architecturally passive resources
// (docs/00-start-here.md requirement 6): a script sitting inside a
// Skill/Layer resource stays inert data until a separate,
// policy-granted executable definition references its exact hash
// (docs/architecture/03-system-architecture.md §10.2) — so a Block's
// ExecutorRef must never be allowed to name one directly. That is the
// one concrete rule this whole package exists to enforce.
//
// Block plugs directly into the generic definition.VersionFields
// contract V2-02 already built for every non-Workflow DefinitionKind —
// there is no separate BlockVersion wrapper type here, unlike
// internal/domain/workflow's historical WorkflowVersion (which predates
// that shared contract and keeps its own dedicated tables). Compile
// produces a definition.VersionFields directly; a BlockDocument is
// reconstructed later (when needed) by decoding its CanonicalSource or
// CompiledSnapshot JSON back into BlockDocument, the same way every
// other shared-table kind's payload round-trips.
package block

import (
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// BlockDefinitionID identifies a Block's mutable Definition row, kept as
// its own named type per this codebase's kind-safe-ID convention (each
// kind declares its own ID type rather than sharing a generic phantom
// type — see internal/domain/definition's own package doc for why).
type BlockDefinitionID string

// BlockVersionID identifies one immutable, published BlockVersion.
type BlockVersionID string

// NodeType is one of HE-14's node types that actually executes
// something and therefore can reference a Block as its executor
// (docs/harness-engineering/14-lec-14-do-thi-dieu-phoi.md's node-type
// vocabulary). Purely structural/control-flow node types from that same
// vocabulary (START, END, ROUTER, FORK, JOIN, WAIT/SIGNAL,
// SPAWN_WORK_ITEMS, SUBFLOW) are deliberately not included here: they
// never carry an executor reference, so a Block can never declare
// compatibility with one.
type NodeType string

const (
	NodeTypeAgent       NodeType = "AGENT"
	NodeTypeCommand     NodeType = "COMMAND"
	NodeTypeMachineGate NodeType = "MACHINE_GATE"
	NodeTypeHumanTask   NodeType = "HUMAN_TASK"
)

var validNodeTypes = map[NodeType]bool{
	NodeTypeAgent: true, NodeTypeCommand: true, NodeTypeMachineGate: true, NodeTypeHumanTask: true,
}

// ScopeAccess is the kind of repository access a Block's ScopeSelector
// declares it needs — the authoring-time counterpart of the runtime
// RepositoryScope grant (docs/architecture/04-go-core-spec.md) a
// compiler later checks this declaration against.
type ScopeAccess string

const (
	ScopeAccessRead  ScopeAccess = "READ"
	ScopeAccessWrite ScopeAccess = "WRITE"
)

// ScopeSelector is a Block's declared repository-scope requirement:
// which access level it needs and which path patterns it touches. It is
// a requirement to validate against, never a grant itself — the actual
// grant is a runtime RepositoryScope, resolved and checked elsewhere
// (docs/design/04-v2-definition-plane.md's later graph/dependency
// compiler task, V2-09).
type ScopeSelector struct {
	Access     ScopeAccess `json:"access" yaml:"access"`
	PathScopes []string    `json:"pathScopes,omitempty" yaml:"pathScopes,omitempty"`
}

// DoneCondition names which of a Block's declared Outcomes its execution
// reached (HE-14-M07's "model output MUST be normalized/validated into a
// typed outcome"). This package only requires it be present: the
// expression language it's written in, and evaluating it against a real
// execution result, belongs to whichever engine runs the node (V4/V5),
// never to this authoring-time schema.
type DoneCondition string

// BlockDocument is a Block's complete authored content — everything
// HE-14-M02 requires a typed node contract to carry, except the
// input/output schema shape itself (deferred to whichever later task
// gives BlockDocument a typed payload schema; V2-04's own scope is the
// node-contract metadata Thực hiện literally lists: node compatibility,
// required capability, timeout, scope selector and done condition).
type BlockDocument struct {
	// CompatibleNodeTypes is which HE-14 node type(s) this Block may be
	// used as the executor for. At least one is required.
	CompatibleNodeTypes []NodeType `json:"compatibleNodeTypes" yaml:"compatibleNodeTypes"`
	// RequiredCapabilities is the set of named capabilities a run must
	// grant before this Block may execute — e.g.
	// "INTEGRATION_MULTI_REPOSITORY_WRITE"
	// (docs/architecture/02-architecture-decisions.md ADR-013), the one
	// capability value already referenced elsewhere in this repo's
	// accepted architecture. Order never carries meaning, which is
	// exactly why canonical.go's own SetPaths doc comment uses
	// "block.capabilities" as its example set path.
	RequiredCapabilities []string `json:"requiredCapabilities,omitempty" yaml:"requiredCapabilities,omitempty"`
	// TimeoutSeconds is how long a single execution attempt of this
	// Block may run before it is considered failed.
	TimeoutSeconds uint32 `json:"timeoutSeconds" yaml:"timeoutSeconds"`
	// ScopeSelector is the repository-scope requirement this Block
	// declares (see ScopeSelector's own doc comment).
	ScopeSelector ScopeSelector `json:"scopeSelector" yaml:"scopeSelector"`
	// DoneCondition decides which declared Outcome an execution reached.
	DoneCondition DoneCondition `json:"doneCondition" yaml:"doneCondition"`
	// ExecutorRef pins the one other definition that actually executes
	// on this Block's behalf. Its Kind must be one that this repo's
	// design actually grants execution authority to (Block, Command,
	// Gate, or Agent Profile) — never Skill or Layer, which are
	// architecturally passive resources; see ValidateDocument.
	ExecutorRef definition.DependencyPin `json:"executorRef" yaml:"executorRef"`
	// PolicyRefs pins the attempt/completion/permission/context/cleanup
	// policies this Block runs under. Every pin's Kind must be
	// definition.KindPolicy.
	PolicyRefs []definition.DependencyPin `json:"policyRefs,omitempty" yaml:"policyRefs,omitempty"`
	// Outcomes is the complete, closed set of outcome names this Block's
	// execution may resolve to (mirrors internal/domain/workflow.Node's
	// own Outcomes — a node can only route on an outcome its executor
	// actually declares). At least one is required. Order never carries
	// meaning: two authors listing the same outcomes in a different
	// order mean the same Block.
	Outcomes []string `json:"outcomes" yaml:"outcomes"`
}
