package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

type canonicalWorkflow struct {
	Document     WorkflowDocument `json:"document"`
	Dependencies []DependencyPin  `json:"dependencies,omitempty"`
}

func Compile(definition WorkflowDefinition, request PublishRequest) (WorkflowVersion, error) {
	if definition.ID == "" {
		return WorkflowVersion{}, errors.New("workflow definition id is required")
	}
	if definition.Status == DefinitionArchived {
		return WorkflowVersion{}, errors.New("cannot publish an archived workflow definition")
	}
	if request.VersionID == "" || request.VersionNumber == 0 {
		return WorkflowVersion{}, errors.New("workflow version id and version number are required")
	}
	request.PublishedBy = strings.TrimSpace(request.PublishedBy)
	if request.PublishedBy == "" || request.PublishedAt.IsZero() {
		return WorkflowVersion{}, errors.New("workflow publisher and publish timestamp are required")
	}

	normalizedDocument := normalizeDocument(request.Document)
	if err := validateNormalizedDocument(normalizedDocument); err != nil {
		return WorkflowVersion{}, err
	}
	normalizedManifest, err := normalizeManifest(request.Dependencies)
	if err != nil {
		return WorkflowVersion{}, err
	}

	canonical, err := json.Marshal(canonicalWorkflow{
		Document:     normalizedDocument,
		Dependencies: normalizedManifest.Pins,
	})
	if err != nil {
		return WorkflowVersion{}, fmt.Errorf("marshal canonical workflow: %w", err)
	}
	digest := sha256.Sum256(canonical)

	return WorkflowVersion{
		id:               request.VersionID,
		definitionID:     definition.ID,
		versionNumber:    request.VersionNumber,
		schemaVersion:    normalizedDocument.SchemaVersion,
		document:         cloneDocument(normalizedDocument),
		canonicalContent: append([]byte(nil), canonical...),
		contentHash:      "sha256:" + hex.EncodeToString(digest[:]),
		dependencies:     cloneManifest(normalizedManifest),
		publishedBy:      request.PublishedBy,
		publishedAt:      request.PublishedAt.UTC(),
	}, nil
}

func CompileJSON(
	definition WorkflowDefinition,
	rawDocument []byte,
	request PublishRequest,
) (WorkflowVersion, error) {
	decoder := json.NewDecoder(bytes.NewReader(rawDocument))
	decoder.DisallowUnknownFields()
	var document WorkflowDocument
	if err := decoder.Decode(&document); err != nil {
		return WorkflowVersion{}, fmt.Errorf("decode workflow document: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return WorkflowVersion{}, err
	}
	request.Document = document
	return Compile(definition, request)
}

func ValidateDocument(document WorkflowDocument) error {
	return validateNormalizedDocument(normalizeDocument(document))
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("workflow document contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing workflow content: %w", err)
	}
	return nil
}

func normalizeDocument(document WorkflowDocument) WorkflowDocument {
	normalized := cloneDocument(document)
	sort.Slice(normalized.Nodes, func(i, j int) bool {
		return normalized.Nodes[i].Key < normalized.Nodes[j].Key
	})
	for index := range normalized.Nodes {
		sort.Strings(normalized.Nodes[index].Outcomes)
		normalizeNodeConfig(&normalized.Nodes[index])
	}
	sort.Slice(normalized.Edges, func(i, j int) bool {
		return normalized.Edges[i].Key < normalized.Edges[j].Key
	})
	sort.Slice(normalized.SharedState, func(i, j int) bool {
		return normalized.SharedState[i].Name < normalized.SharedState[j].Name
	})
	for index := range normalized.SharedState {
		sort.Strings(normalized.SharedState[index].Writers)
		sort.Strings(normalized.SharedState[index].Readers)
	}
	return normalized
}

// sortDependencyPins sorts a set-like []definition.DependencyPin slice
// in place by Kind then DefinitionID then VersionID, the same
// order-never-carries-meaning normalization normalizeManifest already
// applies to this package's own DependencyPin — two authors listing the
// same pins in a different order must canonicalize identically.
func sortDependencyPins(pins []definition.DependencyPin) {
	sort.Slice(pins, func(i, j int) bool {
		left, right := pins[i], pins[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.DefinitionID != right.DefinitionID {
			return left.DefinitionID < right.DefinitionID
		}
		return left.VersionID < right.VersionID
	})
}

// normalizeNodeConfig sorts every set-like field inside node's typed
// config in place. At most one of the config pointers is non-nil (Type
// determines which — see validateNormalizedDocument), so this only ever
// touches the one config the node actually declares.
func normalizeNodeConfig(node *Node) {
	switch {
	case node.Agent != nil:
		sortDependencyPins(node.Agent.PolicyRefs)
	case node.Command != nil:
		sortDependencyPins(node.Command.PolicyRefs)
	case node.MachineGate != nil:
		sortDependencyPins(node.MachineGate.PolicyRefs)
	case node.Approval != nil:
		sort.Strings(node.Approval.AuthorizedRoles)
		sort.Strings(node.Approval.RequestedEvidenceKinds)
	}
}

func normalizeManifest(manifest DependencyManifest) (DependencyManifest, error) {
	normalized := cloneManifest(manifest)
	sort.Slice(normalized.Pins, func(i, j int) bool {
		left := normalized.Pins[i]
		right := normalized.Pins[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Key < right.Key
	})
	for index, pin := range normalized.Pins {
		if strings.TrimSpace(pin.Kind) == "" || strings.TrimSpace(pin.Key) == "" ||
			strings.TrimSpace(pin.Version) == "" || strings.TrimSpace(pin.Hash) == "" {
			return DependencyManifest{}, errors.New("dependency kind, key, version and hash are required")
		}
		if index > 0 && normalized.Pins[index-1].Kind == pin.Kind && normalized.Pins[index-1].Key == pin.Key {
			return DependencyManifest{}, fmt.Errorf("duplicate dependency %q/%q", pin.Kind, pin.Key)
		}
	}
	return normalized, nil
}
