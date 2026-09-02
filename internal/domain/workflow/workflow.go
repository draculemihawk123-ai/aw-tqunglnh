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

type NodeType string

const (
	NodeStart   NodeType = "START"
	NodeAgent   NodeType = "AGENT"
	NodeCommand NodeType = "COMMAND"
	NodeEnd     NodeType = "END"
)

type CyclePolicy struct {
	MaxIterations     uint32 `json:"maxIterations"`
	EscalationOutcome string `json:"escalationOutcome"`
}

type Node struct {
	Key         string       `json:"key"`
	Type        NodeType     `json:"type"`
	Outcomes    []string     `json:"outcomes,omitempty"`
	ExecutorRef string       `json:"executorRef,omitempty"`
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
	}
	for index, node := range document.Nodes {
		cloned.Nodes[index] = node
		cloned.Nodes[index].Outcomes = append([]string(nil), node.Outcomes...)
		if node.CyclePolicy != nil {
			policy := *node.CyclePolicy
			cloned.Nodes[index].CyclePolicy = &policy
		}
	}
	return cloned
}

func cloneManifest(manifest DependencyManifest) DependencyManifest {
	return DependencyManifest{Pins: append([]DependencyPin(nil), manifest.Pins...)}
}
