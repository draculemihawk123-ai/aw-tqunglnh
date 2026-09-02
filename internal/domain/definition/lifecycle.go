// Package definition is the identity/lifecycle model every DefinitionKind
// (Workflow, Block, Skill, Layer, Engineering Pack, Agent Profile,
// Command, Gate, Policy) reuses (docs/design/04-v2-definition-plane.md
// V2-01, AK-ARCH-001, HE-14-M01): a Definition is mutable — its Status
// and own optimistic-concurrency generation change over time — while a
// published Version is immutable forever once it exists. This package
// never mixes the two, and never carries a kind's own runtime payload
// (that stays in each kind's own domain package — e.g.
// internal/domain/workflow's WorkflowDocument): it only ever expresses
// the lifecycle rules and identity/metadata shape every kind shares, so
// a new kind reuses proven behavior instead of re-deriving (and possibly
// getting wrong) the same rules independently.
package definition

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// Kind identifies which of the nine DefinitionKinds a Definition/Version
// belongs to. AdapterBuildVersion is deliberately never one of these
// (ADR-022): it is an operational registry entry with its own identity,
// never authored through this publish contract.
type Kind string

const (
	KindWorkflow        Kind = "WORKFLOW"
	KindBlock           Kind = "BLOCK"
	KindSkill           Kind = "SKILL"
	KindLayer           Kind = "LAYER"
	KindEngineeringPack Kind = "ENGINEERING_PACK"
	KindAgentProfile    Kind = "AGENT_PROFILE"
	KindCommand         Kind = "COMMAND"
	KindGate            Kind = "GATE"
	KindPolicy          Kind = "POLICY"
)

var allKinds = map[Kind]bool{
	KindWorkflow: true, KindBlock: true, KindSkill: true, KindLayer: true,
	KindEngineeringPack: true, KindAgentProfile: true, KindCommand: true,
	KindGate: true, KindPolicy: true,
}

// Valid reports whether k is one of the nine defined DefinitionKinds.
func (k Kind) Valid() bool { return allKinds[k] }

// Status is a Definition's own mutable lifecycle state. A Version has no
// Status of its own — once published it simply exists, immutably; only
// the Definition it belongs to moves through DRAFT/ACTIVE/ARCHIVED.
type Status string

const (
	StatusDraft    Status = "DRAFT"
	StatusActive   Status = "ACTIVE"
	StatusArchived Status = "ARCHIVED"
)

// ErrIllegalTransition is returned by CanTransition for any Status pair
// that is not one of the lifecycle's explicitly allowed edges.
var ErrIllegalTransition = errors.New("definition: illegal status transition")

// legalTransitions is the complete, closed set of Status transitions any
// DefinitionKind's lifecycle may ever make: DRAFT can move to ACTIVE or
// directly to ARCHIVED (a draft that never shipped can still be retired);
// ACTIVE can only move to ARCHIVED; ARCHIVED is terminal. Existing once
// here is the whole point of this package — a new kind reuses this map
// instead of writing its own ad-hoc status checks.
var legalTransitions = map[Status]map[Status]bool{
	StatusDraft:    {StatusActive: true, StatusArchived: true},
	StatusActive:   {StatusArchived: true},
	StatusArchived: {},
}

// CanTransition reports whether from -> to is a legal Status transition.
func CanTransition(from, to Status) error {
	edges, known := legalTransitions[from]
	if !known {
		return fmt.Errorf("%w: unknown status %q", ErrIllegalTransition, from)
	}
	if from == to {
		return fmt.Errorf("%w: %s -> %s is a no-op, not a transition", ErrIllegalTransition, from, to)
	}
	if edges[to] {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
}

// CanPublish reports whether a Definition currently at status may accept
// a newly published Version. ARCHIVED is the one terminal status nothing
// can ever publish against again — DRAFT and ACTIVE both may.
func CanPublish(status Status) error {
	if _, known := legalTransitions[status]; !known {
		return fmt.Errorf("definition: unknown status %q", status)
	}
	if status == StatusArchived {
		return errors.New("definition: cannot publish a version for an archived definition")
	}
	return nil
}

// Scope is where a Definition lives: exactly one project, or
// installation-wide ("global"). A nil ProjectID means global — the same
// nil-means-installation-wide convention already used elsewhere in this
// codebase (e.g. workflow.WorkflowDefinition.ProjectID).
type Scope struct {
	ProjectID *project.ProjectID
}

// ProjectScope returns a Scope bound to one project.
func ProjectScope(id project.ProjectID) Scope { return Scope{ProjectID: &id} }

// GlobalScope returns the installation-wide Scope.
func GlobalScope() Scope { return Scope{} }

// IsGlobal reports whether s is the installation-wide scope.
func (s Scope) IsGlobal() bool { return s.ProjectID == nil }

// Fields is the kind-agnostic mutable state every DefinitionKind's own
// Definition type carries — Version here is this row's own
// optimistic-concurrency generation (incremented on every mutation to
// the Definition itself: a status change, a rename), never a published
// Version's sequence number, which is a wholly separate concept each
// kind's own publish contract tracks (V2-02). A kind wraps or embeds
// Fields alongside its own kind-safe typed ID and its own payload type —
// this package never defines either, since a kind-safe ID needs to be a
// distinct Go type per kind to be worth anything, and payload shape is
// exactly what makes one kind different from another.
type Fields struct {
	Kind    Kind
	Scope   Scope
	Name    string
	Status  Status
	Version uint64
}

// CreateRequest is what a caller supplies to Create.
type CreateRequest struct {
	Kind  Kind
	Scope Scope
	Name  string
}

// Create returns the Fields a newly created Definition starts with:
// always StatusDraft, always generation 1 — a Definition is never
// created directly into ACTIVE or ARCHIVED.
func Create(req CreateRequest) (Fields, error) {
	if !req.Kind.Valid() {
		return Fields{}, fmt.Errorf("definition: unknown kind %q", req.Kind)
	}
	if strings.TrimSpace(req.Name) == "" {
		return Fields{}, errors.New("definition: name is required")
	}
	return Fields{Kind: req.Kind, Scope: req.Scope, Name: req.Name, Status: StatusDraft, Version: 1}, nil
}

// Archive transitions current to StatusArchived, incrementing its
// generation, or returns the exact CanTransition error if current.Status
// cannot legally reach ARCHIVED.
func Archive(current Fields) (Fields, error) {
	if err := CanTransition(current.Status, StatusArchived); err != nil {
		return Fields{}, err
	}
	next := current
	next.Status = StatusArchived
	next.Version++
	return next, nil
}

// Activate transitions current to StatusActive, incrementing its
// generation, or returns the exact CanTransition error if current.Status
// cannot legally reach ACTIVE.
func Activate(current Fields) (Fields, error) {
	if err := CanTransition(current.Status, StatusActive); err != nil {
		return Fields{}, err
	}
	next := current
	next.Status = StatusActive
	next.Version++
	return next, nil
}

// VersionFields is the kind-agnostic identity/content/metadata every
// published Version carries — never its kind-specific payload (that
// stays in each kind's own domain package, e.g.
// workflow.WorkflowVersion.Document()). CanonicalSource/SourceHash is
// the exact authored content and its hash (ADR-012's SourceHash);
// CompiledSnapshot/CompiledHash is the resolved runtime payload plus
// dependency manifest and its own hash (ADR-012's CompiledSnapshotHash)
// — the two differ because a dependency's own version can change what a
// definition compiles to even when its own authored source is untouched.
// VersionFields has no exported way to mutate any field once
// constructed: a published Version is immutable forever (V2-01's own
// "không có update/delete public trên Version").
type VersionFields struct {
	id               string
	definitionID     string
	kind             Kind
	versionNumber    uint64
	schemaVersion    int
	canonicalSource  string
	sourceHash       string
	compiledSnapshot string
	compiledHash     string
	dependencies     DependencyManifest
	publishedBy      string
	publishedAt      time.Time
}

// NewVersionFieldsRequest is what a caller supplies to NewVersionFields.
type NewVersionFieldsRequest struct {
	ID               string
	DefinitionID     string
	Kind             Kind
	VersionNumber    uint64
	SchemaVersion    int
	CanonicalSource  string
	SourceHash       string
	CompiledSnapshot string
	CompiledHash     string
	Dependencies     DependencyManifest
	PublishedBy      string
	PublishedAt      time.Time
}

// NewVersionFields validates and constructs an immutable VersionFields.
func NewVersionFields(req NewVersionFieldsRequest) (VersionFields, error) {
	if strings.TrimSpace(req.ID) == "" {
		return VersionFields{}, errors.New("definition: version id is required")
	}
	if strings.TrimSpace(req.DefinitionID) == "" {
		return VersionFields{}, errors.New("definition: version's definition id is required")
	}
	if !req.Kind.Valid() {
		return VersionFields{}, fmt.Errorf("definition: unknown kind %q", req.Kind)
	}
	if req.VersionNumber == 0 {
		return VersionFields{}, errors.New("definition: version number must be positive")
	}
	if req.SchemaVersion <= 0 {
		return VersionFields{}, errors.New("definition: schema version must be positive")
	}
	if strings.TrimSpace(req.CanonicalSource) == "" {
		return VersionFields{}, errors.New("definition: canonical source is required")
	}
	if strings.TrimSpace(req.SourceHash) == "" {
		return VersionFields{}, errors.New("definition: source hash is required")
	}
	if strings.TrimSpace(req.CompiledSnapshot) == "" {
		return VersionFields{}, errors.New("definition: compiled snapshot is required")
	}
	if strings.TrimSpace(req.CompiledHash) == "" {
		return VersionFields{}, errors.New("definition: compiled hash is required")
	}
	if strings.TrimSpace(req.PublishedBy) == "" {
		return VersionFields{}, errors.New("definition: publishedBy is required")
	}
	if req.PublishedAt.IsZero() {
		return VersionFields{}, errors.New("definition: publishedAt is required")
	}
	return VersionFields{
		id: req.ID, definitionID: req.DefinitionID, kind: req.Kind,
		versionNumber: req.VersionNumber, schemaVersion: req.SchemaVersion,
		canonicalSource: req.CanonicalSource, sourceHash: req.SourceHash,
		compiledSnapshot: req.CompiledSnapshot, compiledHash: req.CompiledHash,
		dependencies: req.Dependencies.Clone(),
		publishedBy:  req.PublishedBy, publishedAt: req.PublishedAt,
	}, nil
}

func (v VersionFields) ID() string                       { return v.id }
func (v VersionFields) DefinitionID() string             { return v.definitionID }
func (v VersionFields) Kind() Kind                       { return v.kind }
func (v VersionFields) VersionNumber() uint64            { return v.versionNumber }
func (v VersionFields) SchemaVersion() int               { return v.schemaVersion }
func (v VersionFields) CanonicalSource() string          { return v.canonicalSource }
func (v VersionFields) SourceHash() string               { return v.sourceHash }
func (v VersionFields) CompiledSnapshot() string         { return v.compiledSnapshot }
func (v VersionFields) CompiledHash() string             { return v.compiledHash }
func (v VersionFields) Dependencies() DependencyManifest { return v.dependencies.Clone() }
func (v VersionFields) PublishedBy() string              { return v.publishedBy }
func (v VersionFields) PublishedAt() time.Time           { return v.publishedAt }
