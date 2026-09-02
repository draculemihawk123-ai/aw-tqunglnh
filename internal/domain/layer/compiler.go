package layer

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// documentSetPaths marks every LayerDocument field whose array value is
// semantically a set rather than an ordered list — identical reasoning
// to internal/domain/skill's own documentSetPaths (see that package's
// doc comment).
var documentSetPaths = map[string]bool{
	"resources":                        true,
	"resources.selector.componentTags": true,
	"resources.selector.pathTags":      true,
	"resources.selector.taskKinds":     true,
	"resources.selector.blockKinds":    true,
	"resources.selector.riskClasses":   true,
}

// compiledLayerSnapshot is the resolved runtime payload ADR-012's
// CompiledSnapshotHash covers: the authored document plus the exact
// dependency pins it was published with.
type compiledLayerSnapshot struct {
	Document     LayerDocument              `json:"document"`
	Dependencies []definition.DependencyPin `json:"dependencies,omitempty"`
}

var compiledSetPaths = map[string]bool{
	"document.resources":                        true,
	"document.resources.selector.componentTags": true,
	"document.resources.selector.pathTags":      true,
	"document.resources.selector.taskKinds":     true,
	"document.resources.selector.blockKinds":    true,
	"document.resources.selector.riskClasses":   true,
	"dependencies":                              true,
}

// LayerDefinition is a Layer's mutable Definition identity — its own
// kind-safe ID alongside the kind-agnostic lifecycle state every
// DefinitionKind shares (internal/domain/definition.Fields).
type LayerDefinition struct {
	ID     LayerDefinitionID
	Fields definition.Fields
}

// PublishRequest is what a caller supplies to Compile.
type PublishRequest struct {
	VersionID     LayerVersionID
	VersionNumber uint64
	SchemaVersion int
	Document      LayerDocument
	Dependencies  definition.DependencyManifest
	PublishedBy   string
	PublishedAt   time.Time
}

// DecodeDocument strictly decodes data (JSON or YAML) into a
// LayerDocument via internal/domain/authoring.DecodeStrict — unknown
// fields, duplicate keys and (YAML) implicit ambiguous booleans are all
// rejected, never silently accepted.
func DecodeDocument(data []byte, format authoring.Format) (LayerDocument, error) {
	var doc LayerDocument
	if err := authoring.DecodeStrict(data, format, &doc); err != nil {
		return LayerDocument{}, err
	}
	return doc, nil
}

// Compile validates req.Document against every LayerVersion rule and, if
// it passes, produces the definition.VersionFields this Layer publishes
// as — the exact generic type V2-02's shared publish path already
// accepts for every non-Workflow DefinitionKind.
func Compile(def LayerDefinition, req PublishRequest) (definition.VersionFields, error) {
	if strings.TrimSpace(string(def.ID)) == "" {
		return definition.VersionFields{}, errors.New("layer: definition id is required")
	}
	if err := definition.CanPublish(def.Fields.Status); err != nil {
		return definition.VersionFields{}, err
	}
	if strings.TrimSpace(string(req.VersionID)) == "" || req.VersionNumber == 0 {
		return definition.VersionFields{}, errors.New("layer: version id and version number are required")
	}
	req.PublishedBy = strings.TrimSpace(req.PublishedBy)
	if req.PublishedBy == "" || req.PublishedAt.IsZero() {
		return definition.VersionFields{}, errors.New("layer: publisher and publish timestamp are required")
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

	compiledJSON, compiledHash, err := authoring.Canonicalize(compiledLayerSnapshot{
		Document:     req.Document,
		Dependencies: append([]definition.DependencyPin(nil), req.Dependencies.Pins...),
	}, authoring.CanonicalizeOptions{SetPaths: compiledSetPaths})
	if err != nil {
		return definition.VersionFields{}, err
	}

	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID:               string(req.VersionID),
		DefinitionID:     string(def.ID),
		Kind:             definition.KindLayer,
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
func CompileFrom(def LayerDefinition, rawDocument []byte, format authoring.Format, req PublishRequest) (definition.VersionFields, error) {
	document, err := DecodeDocument(rawDocument, format)
	if err != nil {
		return definition.VersionFields{}, err
	}
	req.Document = document
	return Compile(def, req)
}
