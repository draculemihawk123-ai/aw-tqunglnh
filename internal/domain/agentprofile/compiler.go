package agentprofile

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// documentSetPaths marks every AgentProfileDocument field whose array
// value is semantically a set (order never carries meaning) rather than
// an ordered list, matching authoring.Canonicalize's own SetPaths
// convention. toolRefs, compatibility.os, compatibility.toolchain and
// requiredCapabilities are all sets by this package's own doc comments.
var documentSetPaths = map[string]bool{
	"toolRefs":                true,
	"compatibility.os":        true,
	"compatibility.toolchain": true,
	"requiredCapabilities":    true,
}

// compiledAgentProfileSnapshot is the resolved runtime payload ADR-012's
// CompiledSnapshotHash covers: the authored document plus the exact
// dependency pins it was published with. This is exactly what makes
// V2-07's own "Hoàn thành khi: effective profile canonical/hash được"
// concrete — an "effective" profile is its own authored document
// combined with the exact resolved dependency manifest (which, once
// V2-09 exists, includes the resolved ContextPolicyRef pin among
// others); CompiledHash is that combination's canonical hash.
type compiledAgentProfileSnapshot struct {
	Document     AgentProfileDocument       `json:"document"`
	Dependencies []definition.DependencyPin `json:"dependencies,omitempty"`
}

var compiledSetPaths = map[string]bool{
	"document.toolRefs":                true,
	"document.compatibility.os":        true,
	"document.compatibility.toolchain": true,
	"document.requiredCapabilities":    true,
	"dependencies":                     true,
}

// AgentProfileDefinition is an Agent Profile's mutable Definition
// identity — its own kind-safe ID alongside the kind-agnostic lifecycle
// state every DefinitionKind shares (internal/domain/definition.Fields).
type AgentProfileDefinition struct {
	ID     AgentProfileDefinitionID
	Fields definition.Fields
}

// PublishRequest is what a caller supplies to Compile.
type PublishRequest struct {
	VersionID     AgentProfileVersionID
	VersionNumber uint64
	SchemaVersion int
	Document      AgentProfileDocument
	Dependencies  definition.DependencyManifest
	PublishedBy   string
	PublishedAt   time.Time
}

// DecodeDocument strictly decodes data (JSON or YAML) into an
// AgentProfileDocument via internal/domain/authoring.DecodeStrict —
// unknown fields, duplicate keys and (YAML) implicit ambiguous booleans
// are all rejected, never silently accepted.
func DecodeDocument(data []byte, format authoring.Format) (AgentProfileDocument, error) {
	var doc AgentProfileDocument
	if err := authoring.DecodeStrict(data, format, &doc); err != nil {
		return AgentProfileDocument{}, err
	}
	return doc, nil
}

// Compile validates req.Document against every AgentProfileVersion rule
// and, if it passes, produces the definition.VersionFields this Agent
// Profile publishes as — the exact generic type V2-02's shared publish
// path already accepts for every non-Workflow DefinitionKind. "Profile
// chỉ tham chiếu published dependencies" (V2-07's own completion bar) is
// satisfied by construction here: the only way this document ever names
// another definition is through ContextPolicyRef, a definition.DependencyPin
// naming an exact VersionID — never an inline value or a version range —
// so there is structurally nothing else a caller could reference.
// Verifying that the pinned VersionID actually exists and is published
// is V2-09's job, not this package's.
func Compile(def AgentProfileDefinition, req PublishRequest) (definition.VersionFields, error) {
	if strings.TrimSpace(string(def.ID)) == "" {
		return definition.VersionFields{}, errors.New("agentprofile: definition id is required")
	}
	if err := definition.CanPublish(def.Fields.Status); err != nil {
		return definition.VersionFields{}, err
	}
	if strings.TrimSpace(string(req.VersionID)) == "" || req.VersionNumber == 0 {
		return definition.VersionFields{}, errors.New("agentprofile: version id and version number are required")
	}
	req.PublishedBy = strings.TrimSpace(req.PublishedBy)
	if req.PublishedBy == "" || req.PublishedAt.IsZero() {
		return definition.VersionFields{}, errors.New("agentprofile: publisher and publish timestamp are required")
	}

	if diags := ValidateDocument(req.Document); diags.HasProblems() {
		return definition.VersionFields{}, diags.AsError()
	}

	sourceJSON, sourceHash, err := authoring.Canonicalize(req.Document, authoring.CanonicalizeOptions{
		SetPaths: documentSetPaths,
	})
	if err != nil {
		return definition.VersionFields{}, err
	}

	compiledJSON, compiledHash, err := authoring.Canonicalize(compiledAgentProfileSnapshot{
		Document:     req.Document,
		Dependencies: append([]definition.DependencyPin(nil), req.Dependencies.Pins...),
	}, authoring.CanonicalizeOptions{SetPaths: compiledSetPaths})
	if err != nil {
		return definition.VersionFields{}, err
	}

	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID:               string(req.VersionID),
		DefinitionID:     string(def.ID),
		Kind:             definition.KindAgentProfile,
		VersionNumber:    req.VersionNumber,
		SchemaVersion:    req.SchemaVersion,
		CanonicalSource:  string(sourceJSON),
		SourceHash:       sourceHash,
		CompiledSnapshot: string(compiledJSON),
		CompiledHash:     compiledHash,
		Dependencies:     req.Dependencies,
		PublishedBy:      req.PublishedBy,
		PublishedAt:      req.PublishedAt,
	})
}

// CompileFrom decodes rawDocument (JSON or YAML) via DecodeDocument and
// compiles it in one step.
func CompileFrom(def AgentProfileDefinition, rawDocument []byte, format authoring.Format, req PublishRequest) (definition.VersionFields, error) {
	document, err := DecodeDocument(rawDocument, format)
	if err != nil {
		return definition.VersionFields{}, err
	}
	req.Document = document
	return Compile(def, req)
}
