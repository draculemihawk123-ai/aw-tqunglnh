package block

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// documentSetPaths marks every BlockDocument field whose array value is
// semantically a set (order never carries meaning) rather than an
// ordered list, matching authoring.Canonicalize's own SetPaths
// convention. compatibleNodeTypes, requiredCapabilities,
// scopeSelector.pathScopes and outcomes are all sets by this package's
// own doc comments; policyRefs is a set of pins, ordered the same way.
var documentSetPaths = map[string]bool{
	"compatibleNodeTypes":      true,
	"requiredCapabilities":     true,
	"scopeSelector.pathScopes": true,
	"outcomes":                 true,
	"policyRefs":               true,
}

// compiledBlockSnapshot is the resolved runtime payload ADR-012's
// CompiledSnapshotHash covers: the authored document plus the exact
// dependency pins it was published with. At V2-04's own scope (a single
// Block, not a whole graph) "resolved" means exactly this — verifying
// those pins actually exist and are mutually compatible is the
// graph/dependency compiler's job (V2-09), not this package's.
type compiledBlockSnapshot struct {
	Document     BlockDocument              `json:"document"`
	Dependencies []definition.DependencyPin `json:"dependencies,omitempty"`
}

var compiledSetPaths = map[string]bool{
	"document.compatibleNodeTypes":      true,
	"document.requiredCapabilities":     true,
	"document.scopeSelector.pathScopes": true,
	"document.outcomes":                 true,
	"document.policyRefs":               true,
	"dependencies":                      true,
}

// BlockDefinition is a Block's mutable Definition identity — its own
// kind-safe ID alongside the kind-agnostic lifecycle state every
// DefinitionKind shares (internal/domain/definition.Fields).
type BlockDefinition struct {
	ID     BlockDefinitionID
	Fields definition.Fields
}

// PublishRequest is what a caller supplies to Compile.
type PublishRequest struct {
	VersionID     BlockVersionID
	VersionNumber uint64
	SchemaVersion int
	Document      BlockDocument
	Dependencies  definition.DependencyManifest
	PublishedBy   string
	PublishedAt   time.Time
}

// DecodeDocument strictly decodes data (JSON or YAML) into a
// BlockDocument via internal/domain/authoring.DecodeStrict — unknown
// fields, duplicate keys and (YAML) implicit ambiguous booleans are all
// rejected, never silently accepted.
func DecodeDocument(data []byte, format authoring.Format) (BlockDocument, error) {
	var doc BlockDocument
	if err := authoring.DecodeStrict(data, format, &doc); err != nil {
		return BlockDocument{}, err
	}
	return doc, nil
}

// Compile validates req.Document against every BlockVersion rule and,
// if it passes, produces the definition.VersionFields this Block
// publishes as — the exact generic type V2-02's shared publish path
// already accepts for every non-Workflow DefinitionKind, so a Block
// plugs into ports.DefinitionPublisher with no adapter change needed.
func Compile(def BlockDefinition, req PublishRequest) (definition.VersionFields, error) {
	if strings.TrimSpace(string(def.ID)) == "" {
		return definition.VersionFields{}, errors.New("block: definition id is required")
	}
	if err := definition.CanPublish(def.Fields.Status); err != nil {
		return definition.VersionFields{}, err
	}
	if strings.TrimSpace(string(req.VersionID)) == "" || req.VersionNumber == 0 {
		return definition.VersionFields{}, errors.New("block: version id and version number are required")
	}
	req.PublishedBy = strings.TrimSpace(req.PublishedBy)
	if req.PublishedBy == "" || req.PublishedAt.IsZero() {
		return definition.VersionFields{}, errors.New("block: publisher and publish timestamp are required")
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

	compiledJSON, compiledHash, err := authoring.Canonicalize(compiledBlockSnapshot{
		Document:     req.Document,
		Dependencies: append([]definition.DependencyPin(nil), req.Dependencies.Pins...),
	}, authoring.CanonicalizeOptions{SetPaths: compiledSetPaths})
	if err != nil {
		return definition.VersionFields{}, err
	}

	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID:               string(req.VersionID),
		DefinitionID:     string(def.ID),
		Kind:             definition.KindBlock,
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
func CompileFrom(def BlockDefinition, rawDocument []byte, format authoring.Format, req PublishRequest) (definition.VersionFields, error) {
	document, err := DecodeDocument(rawDocument, format)
	if err != nil {
		return definition.VersionFields{}, err
	}
	req.Document = document
	return Compile(def, req)
}
