package policy

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// documentSetPaths marks every PolicyDocument field whose array value is
// semantically a set (order never carries meaning) rather than an
// ordered list, matching authoring.Canonicalize's own SetPaths
// convention. context.order is deliberately absent: it is the one array
// in this whole document whose order is the point (see ContextRules'
// own doc comment).
var documentSetPaths = map[string]bool{
	"attempt.retryableErrorCodes":      true,
	"completion.requiredEvidenceKinds": true,
	"permission.grantedCapabilities":   true,
	"context.selector":                 true,
	"context.resourceRefs":             true,
}

// compiledPolicySnapshot is the resolved runtime payload ADR-012's
// CompiledSnapshotHash covers: the authored document plus the exact
// dependency pins it was published with. A Policy document rarely (if
// ever) needs a cross-kind dependency pin of its own at V2-07's scope,
// but the field exists for the same reason every other kind's compiled
// snapshot carries one: uniformity with the shared publish contract, and
// room for a future category (or a future field within an existing one)
// that does need to pin something.
type compiledPolicySnapshot struct {
	Document     PolicyDocument             `json:"document"`
	Dependencies []definition.DependencyPin `json:"dependencies,omitempty"`
}

var compiledSetPaths = map[string]bool{
	"document.attempt.retryableErrorCodes":      true,
	"document.completion.requiredEvidenceKinds": true,
	"document.permission.grantedCapabilities":   true,
	"document.context.selector":                 true,
	"document.context.resourceRefs":             true,
	"dependencies":                              true,
}

// PolicyDefinition is a Policy's mutable Definition identity — its own
// kind-safe ID alongside the kind-agnostic lifecycle state every
// DefinitionKind shares (internal/domain/definition.Fields).
type PolicyDefinition struct {
	ID     PolicyDefinitionID
	Fields definition.Fields
}

// PublishRequest is what a caller supplies to Compile.
type PublishRequest struct {
	VersionID     PolicyVersionID
	VersionNumber uint64
	SchemaVersion int
	Document      PolicyDocument
	Dependencies  definition.DependencyManifest
	PublishedBy   string
	PublishedAt   time.Time
}

// DecodeDocument strictly decodes data (JSON or YAML) into a
// PolicyDocument via internal/domain/authoring.DecodeStrict — unknown
// fields, duplicate keys and (YAML) implicit ambiguous booleans are all
// rejected, never silently accepted.
func DecodeDocument(data []byte, format authoring.Format) (PolicyDocument, error) {
	var doc PolicyDocument
	if err := authoring.DecodeStrict(data, format, &doc); err != nil {
		return PolicyDocument{}, err
	}
	return doc, nil
}

// Compile validates req.Document against every PolicyVersion rule and,
// if it passes, produces the definition.VersionFields this Policy
// publishes as — the exact generic type V2-02's shared publish path
// already accepts for every non-Workflow DefinitionKind.
func Compile(def PolicyDefinition, req PublishRequest) (definition.VersionFields, error) {
	if strings.TrimSpace(string(def.ID)) == "" {
		return definition.VersionFields{}, errors.New("policy: definition id is required")
	}
	if err := definition.CanPublish(def.Fields.Status); err != nil {
		return definition.VersionFields{}, err
	}
	if strings.TrimSpace(string(req.VersionID)) == "" || req.VersionNumber == 0 {
		return definition.VersionFields{}, errors.New("policy: version id and version number are required")
	}
	req.PublishedBy = strings.TrimSpace(req.PublishedBy)
	if req.PublishedBy == "" || req.PublishedAt.IsZero() {
		return definition.VersionFields{}, errors.New("policy: publisher and publish timestamp are required")
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

	compiledJSON, compiledHash, err := authoring.Canonicalize(compiledPolicySnapshot{
		Document:     req.Document,
		Dependencies: append([]definition.DependencyPin(nil), req.Dependencies.Pins...),
	}, authoring.CanonicalizeOptions{SetPaths: compiledSetPaths})
	if err != nil {
		return definition.VersionFields{}, err
	}

	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID:               string(req.VersionID),
		DefinitionID:     string(def.ID),
		Kind:             definition.KindPolicy,
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
func CompileFrom(def PolicyDefinition, rawDocument []byte, format authoring.Format, req PublishRequest) (definition.VersionFields, error) {
	document, err := DecodeDocument(rawDocument, format)
	if err != nil {
		return definition.VersionFields{}, err
	}
	req.Document = document
	return Compile(def, req)
}
