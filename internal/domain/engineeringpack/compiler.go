package engineeringpack

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// documentSetPaths marks EngineeringPackDocument's dependencies as a set
// rather than an ordered list: lec-04's own Merge policy describes the
// effective composition as "Resource list được hợp nhất, khử trùng theo
// stable ID/version và sắp theo thứ tự xác định" — merged, deduplicated
// by stable ID/version and sorted into a deterministic order — which
// only makes sense if the authored order of dependency pins never
// carried meaning in the first place. Two authors listing the same
// Skill/Layer/Pack dependencies in a different order compose the exact
// same pack.
var documentSetPaths = map[string]bool{
	"dependencies": true,
}

// compiledEngineeringPackSnapshot is the resolved runtime payload
// ADR-012's CompiledSnapshotHash covers: the authored document plus the
// exact dependency pins it was published with (mirrors
// internal/domain/block's own compiledBlockSnapshot). For an Engineering
// Pack, Dependencies here is the resolver's own resolved manifest (e.g.
// from a future graph/dependency compiler, V2-09) rather than a
// duplicate of Document.Dependencies — the same split
// internal/domain/block already established between a document's own
// authored pins and PublishRequest.Dependencies' separately-resolved
// manifest.
type compiledEngineeringPackSnapshot struct {
	Document     EngineeringPackDocument    `json:"document"`
	Dependencies []definition.DependencyPin `json:"dependencies,omitempty"`
}

var compiledSetPaths = map[string]bool{
	"document.dependencies": true,
	"dependencies":          true,
}

// EngineeringPackDefinition is an Engineering Pack's mutable Definition
// identity — its own kind-safe ID alongside the kind-agnostic lifecycle
// state every DefinitionKind shares (internal/domain/definition.Fields).
type EngineeringPackDefinition struct {
	ID     EngineeringPackDefinitionID
	Fields definition.Fields
}

// PublishRequest is what a caller supplies to Compile.
type PublishRequest struct {
	VersionID     EngineeringPackVersionID
	VersionNumber uint64
	SchemaVersion int
	Document      EngineeringPackDocument
	Dependencies  definition.DependencyManifest
	PublishedBy   string
	PublishedAt   time.Time
}

// DecodeDocument strictly decodes data (JSON or YAML) into an
// EngineeringPackDocument via internal/domain/authoring.DecodeStrict —
// unknown fields, duplicate keys and (YAML) implicit ambiguous booleans
// are all rejected, never silently accepted.
func DecodeDocument(data []byte, format authoring.Format) (EngineeringPackDocument, error) {
	var doc EngineeringPackDocument
	if err := authoring.DecodeStrict(data, format, &doc); err != nil {
		return EngineeringPackDocument{}, err
	}
	return doc, nil
}

// Compile validates req.Document against every EngineeringPackVersion
// rule and, if it passes, produces the definition.VersionFields this
// Engineering Pack publishes as — the exact generic type V2-02's shared
// publish path already accepts for every non-Workflow DefinitionKind.
//
// Compile only ever checks doc's own directly-authored pins (shape,
// allowed Kind, duplicates, self-reference) — it never fetches or walks
// any other pack's dependency data, and therefore cannot itself detect a
// multi-pack composition cycle or a cross-pack hard-constraint conflict.
// That is ResolvePackGraph and CheckResourceConflicts' job (graph.go),
// operating on data a real caller (e.g. a future graph/dependency
// compiler) has already fetched — matching this package's own doc
// comment: install/resolve never happens inside this domain layer with
// direct repository access.
func Compile(def EngineeringPackDefinition, req PublishRequest) (definition.VersionFields, error) {
	if strings.TrimSpace(string(def.ID)) == "" {
		return definition.VersionFields{}, errors.New("engineeringpack: definition id is required")
	}
	if err := definition.CanPublish(def.Fields.Status); err != nil {
		return definition.VersionFields{}, err
	}
	if strings.TrimSpace(string(req.VersionID)) == "" || req.VersionNumber == 0 {
		return definition.VersionFields{}, errors.New("engineeringpack: version id and version number are required")
	}
	req.PublishedBy = strings.TrimSpace(req.PublishedBy)
	if req.PublishedBy == "" || req.PublishedAt.IsZero() {
		return definition.VersionFields{}, errors.New("engineeringpack: publisher and publish timestamp are required")
	}

	diags := ValidateDocument(req.Document)
	diags = append(diags, validateNoSelfDependency(string(def.ID), req.Document)...)
	if diags.HasProblems() {
		return definition.VersionFields{}, diags.AsError()
	}

	sourceJSON, sourceHash, err := authoring.Canonicalize(req.Document, authoring.CanonicalizeOptions{
		SetPaths: documentSetPaths,
	})
	if err != nil {
		return definition.VersionFields{}, err
	}

	compiledJSON, compiledHash, err := authoring.Canonicalize(compiledEngineeringPackSnapshot{
		Document:     req.Document,
		Dependencies: append([]definition.DependencyPin(nil), req.Dependencies.Pins...),
	}, authoring.CanonicalizeOptions{SetPaths: compiledSetPaths})
	if err != nil {
		return definition.VersionFields{}, err
	}

	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID:               string(req.VersionID),
		DefinitionID:     string(def.ID),
		Kind:             definition.KindEngineeringPack,
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
func CompileFrom(def EngineeringPackDefinition, rawDocument []byte, format authoring.Format, req PublishRequest) (definition.VersionFields, error) {
	document, err := DecodeDocument(rawDocument, format)
	if err != nil {
		return definition.VersionFields{}, err
	}
	req.Document = document
	return Compile(def, req)
}
