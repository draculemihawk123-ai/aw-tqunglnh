package workflow

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

type WorkflowDefinitionID string
type WorkflowVersionID string

// DefinitionStatus is a type alias (not a distinct type) for
// definition.Status — Workflow is the first DefinitionKind to reuse the
// shared lifecycle model (docs/design/04-v2-definition-plane.md V2-01):
// every existing comparison, switch case and struct field below keeps
// compiling and behaving identically, since an alias is the exact same
// type as what it aliases, not a new one requiring a conversion.
type DefinitionStatus = definition.Status

const (
	DefinitionDraft    = definition.StatusDraft
	DefinitionActive   = definition.StatusActive
	DefinitionArchived = definition.StatusArchived
)

type WorkflowDefinition struct {
	ID        WorkflowDefinitionID
	ProjectID *project.ProjectID
	Name      string
	Status    DefinitionStatus
	Version   uint64
}

// NodeType is the Alpha node-type vocabulary
// (docs/harness-engineering/14-lec-14-do-thi-dieu-phoi.md's "Node types
// nền" list, docs/design/04-v2-definition-plane.md V2-08's own Mục tiêu
// line). HE-14's full lecture vocabulary additionally lists
// COMMAND/SERVICE_CALL as one entry, HUMAN_TASK/APPROVAL as one entry and
// WAIT/SIGNAL as one entry, plus SPAWN_WORK_ITEMS and SUBFLOW
// ("khi có nhu cầu thật" — only once there is real need). V2-08's own
// Mục tiêu line names exactly nine: START, END, AGENT, COMMAND,
// MACHINE_GATE, APPROVAL, WAIT, ROUTER, FORK, JOIN — SERVICE_CALL and
// SIGNAL are folded into COMMAND and WAIT respectively (this package
// picks one canonical name per lecture pairing rather than authoring two
// synonymous type strings), and SPAWN_WORK_ITEMS/SUBFLOW are left out
// entirely: the lecture text itself gates SUBFLOW behind "real need" and
// groups SPAWN_WORK_ITEMS with it in the same deferred tier, so adding
// either here would be scope creep this task's own Mục tiêu line never
// asked for. A later task can extend this vocabulary; nothing about this
// package's design forecloses that.
type NodeType string

const (
	NodeStart       NodeType = "START"
	NodeEnd         NodeType = "END"
	NodeAgent       NodeType = "AGENT"
	NodeCommand     NodeType = "COMMAND"
	NodeMachineGate NodeType = "MACHINE_GATE"
	NodeApproval    NodeType = "APPROVAL"
	NodeWait        NodeType = "WAIT"
	NodeRouter      NodeType = "ROUTER"
	NodeFork        NodeType = "FORK"
	NodeJoin        NodeType = "JOIN"
)

// CyclePolicy is a node's iteration policy: how many times a bounded
// cycle through this node may repeat before validateBoundedCycles
// requires the declared EscalationOutcome to route out of the cycle
// (see validation.go). This predates V2-08 and is unchanged by it — it
// is this package's own existing precedent for an "iteration policy",
// reused as-is per this task's own design guidance.
type CyclePolicy struct {
	MaxIterations     uint32 `json:"maxIterations"`
	EscalationOutcome string `json:"escalationOutcome"`
}

// Node is one typed graph node (HE-14-M02's "typed node contract").
// Exactly the config field matching Type may be populated — Agent only
// for an AGENT node, Command only for COMMAND, MachineGate only for
// MACHINE_GATE, Approval only for APPROVAL, Wait only for WAIT, Join
// only for JOIN — and validateNormalizedDocument rejects any node whose
// populated config fields don't match its Type exactly (including zero
// populated fields for the purely structural types START, END, ROUTER
// and FORK, none of which ever reference an executable, a human task, a
// wait condition or a join policy). This mirrors
// internal/domain/policy.PolicyDocument's own "exactly one of five"
// closed-choice shape (see policy.go's package doc comment) applied to
// node configuration instead of policy category.
//
// Replacing the old bare ExecutorRef string field is the concrete change
// V2-08 makes here: every executable node type now pins its executor by
// an exact definition.DependencyPin (definition ID + version ID + kind)
// instead of an untyped, unstructured string, so a compiled
// WorkflowVersion carries everything a runtime needs to resolve the
// pinned dependency without re-parsing the original authoring file
// (docs/design/04-v2-definition-plane.md V2-08's own "Hoàn thành khi").
type Node struct {
	Key      string   `json:"key"`
	Type     NodeType `json:"type"`
	Outcomes []string `json:"outcomes,omitempty"`

	// Agent is this node's typed config when Type == AGENT; nil for
	// every other Type.
	Agent *AgentNodeConfig `json:"agent,omitempty"`
	// Command is this node's typed config when Type == COMMAND; nil for
	// every other Type.
	Command *CommandNodeConfig `json:"command,omitempty"`
	// MachineGate is this node's typed config when Type == MACHINE_GATE;
	// nil for every other Type.
	MachineGate *MachineGateNodeConfig `json:"machineGate,omitempty"`
	// Approval is this node's typed config when Type == APPROVAL; nil
	// for every other Type.
	Approval *ApprovalNodeConfig `json:"approval,omitempty"`
	// Wait is this node's typed config when Type == WAIT; nil for every
	// other Type.
	Wait *WaitNodeConfig `json:"wait,omitempty"`
	// Join is this node's typed config when Type == JOIN; nil for every
	// other Type. ROUTER and FORK deliberately have no typed config of
	// their own: a ROUTER's whole job is picking which already-declared
	// outcome fires (HE-14-M07's "deterministic transition boundary" —
	// how a model-assisted suggestion gets normalized into one of this
	// node's Outcomes — is runtime behavior, not an authoring-schema
	// concern this package models), and a FORK's parallel branches are
	// simply its declared Outcomes/Edges (HE-14-M06's fork/join
	// topology validation — e.g. that a FORK's branches actually
	// reconverge at a JOIN — is explicitly V2-09's "Graph/dependency
	// compiler" scope, docs/design/04-v2-definition-plane.md, not
	// V2-08's).
	Join *JoinNodeConfig `json:"join,omitempty"`

	CyclePolicy *CyclePolicy `json:"cyclePolicy,omitempty"`
}

type Edge struct {
	Key     string `json:"key"`
	From    string `json:"from"`
	Outcome string `json:"outcome"`
	To      string `json:"to"`
}

type WorkflowDocument struct {
	SchemaVersion string `json:"schemaVersion"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
	// SharedState is this document's declared shared-state schema
	// (HE-14-M04: "mỗi field MUST có type, owner/writers/readers và
	// merge rule; artifact lớn lưu bằng reference" — see
	// SharedStateField's own doc comment). Optional: a workflow with no
	// nodes that exchange typed shared state simply declares none.
	SharedState []SharedStateField `json:"sharedState,omitempty"`
}

type DependencyPin struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Version string `json:"version"`
	Hash    string `json:"hash"`
}

type DependencyManifest struct {
	Pins []DependencyPin `json:"pins,omitempty"`
}

type PublishRequest struct {
	VersionID     WorkflowVersionID
	VersionNumber uint64
	Document      WorkflowDocument
	Dependencies  DependencyManifest
	PublishedBy   string
	PublishedAt   time.Time
}

// WorkflowVersion is immutable from outside this package. Accessors return copies
// for all slice-backed values, and the type intentionally has no mutation API.
type WorkflowVersion struct {
	id               WorkflowVersionID
	definitionID     WorkflowDefinitionID
	versionNumber    uint64
	schemaVersion    string
	document         WorkflowDocument
	canonicalContent []byte
	contentHash      string
	dependencies     DependencyManifest
	publishedBy      string
	publishedAt      time.Time
}

func (v WorkflowVersion) ID() WorkflowVersionID              { return v.id }
func (v WorkflowVersion) DefinitionID() WorkflowDefinitionID { return v.definitionID }
func (v WorkflowVersion) VersionNumber() uint64              { return v.versionNumber }
func (v WorkflowVersion) SchemaVersion() string              { return v.schemaVersion }
func (v WorkflowVersion) ContentHash() string                { return v.contentHash }
func (v WorkflowVersion) PublishedBy() string                { return v.publishedBy }
func (v WorkflowVersion) PublishedAt() time.Time             { return v.publishedAt }
func (v WorkflowVersion) CanonicalContent() []byte           { return append([]byte(nil), v.canonicalContent...) }
func (v WorkflowVersion) Document() WorkflowDocument         { return cloneDocument(v.document) }
func (v WorkflowVersion) Dependencies() DependencyManifest   { return cloneManifest(v.dependencies) }

func (v WorkflowVersion) SameContent(other WorkflowVersion) bool {
	return v.contentHash == other.contentHash
}

func cloneDocument(document WorkflowDocument) WorkflowDocument {
	cloned := WorkflowDocument{
		SchemaVersion: document.SchemaVersion,
		Nodes:         make([]Node, len(document.Nodes)),
		Edges:         append([]Edge(nil), document.Edges...),
		SharedState:   cloneSharedStateFields(document.SharedState),
	}
	for index, node := range document.Nodes {
		cloned.Nodes[index] = node
		cloned.Nodes[index].Outcomes = append([]string(nil), node.Outcomes...)
		if node.CyclePolicy != nil {
			policy := *node.CyclePolicy
			cloned.Nodes[index].CyclePolicy = &policy
		}
		cloned.Nodes[index].Agent = node.Agent.clone()
		cloned.Nodes[index].Command = node.Command.clone()
		cloned.Nodes[index].MachineGate = node.MachineGate.clone()
		cloned.Nodes[index].Approval = node.Approval.clone()
		cloned.Nodes[index].Wait = node.Wait.clone()
		cloned.Nodes[index].Join = node.Join.clone()
	}
	return cloned
}

func cloneManifest(manifest DependencyManifest) DependencyManifest {
	return DependencyManifest{Pins: append([]DependencyPin(nil), manifest.Pins...)}
}
