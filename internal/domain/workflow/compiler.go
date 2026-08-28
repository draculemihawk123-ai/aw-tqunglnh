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
	}
	sort.Slice(normalized.Edges, func(i, j int) bool {
		return normalized.Edges[i].Key < normalized.Edges[j].Key
	})
	return normalized
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
