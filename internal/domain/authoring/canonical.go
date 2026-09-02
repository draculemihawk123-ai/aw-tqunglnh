package authoring

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CanonicalizeOptions tells Canonicalize which fields need special
// treatment beyond "marshal to JSON with alphabetically sorted keys and
// every array kept in its authored order" (encoding/json.Marshal already
// sorts map[string]any keys alphabetically — Canonicalize relies on that
// stdlib behavior rather than re-implementing key sorting).
type CanonicalizeOptions struct {
	// SetPaths lists dot-separated field paths (e.g. "tags",
	// "block.capabilities") whose array value is semantically a set: two
	// authors listing the same elements in a different order mean the
	// same thing, so Canonicalize sorts that array's elements by their
	// own canonical JSON encoding. Every array NOT listed here is
	// treated as a semantically ordered list and keeps its authored
	// order exactly — this is the default, since most authored arrays
	// (a workflow's nodes/edges, for instance) are lists, not sets.
	SetPaths map[string]bool
	// ExcludePaths lists dot-separated fields stripped entirely before
	// hashing — administrative publish metadata (e.g. "publishedBy",
	// "publishedAt") that was never part of the authored content
	// (ADR-012: SourceHash covers authored content only).
	ExcludePaths map[string]bool
}

// Canonicalize converts target (already strictly decoded, e.g. by
// DecodeStrict) into canonical JSON bytes plus their SHA256 hash
// ("sha256:<hex>"). The same semantic content always produces the exact
// same bytes and hash, regardless of source key order, YAML vs JSON
// origin, or platform (V2-03's own "cùng semantic authoring input tạo
// cùng canonical JSON/SourceHash trên Windows/Linux").
func Canonicalize(target any, opts CanonicalizeOptions) (canonicalJSON []byte, sourceHash string, err error) {
	raw, err := json.Marshal(target)
	if err != nil {
		return nil, "", fmt.Errorf("authoring: marshal for canonicalization: %w", err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, "", fmt.Errorf("authoring: decode intermediate tree: %w", err)
	}

	tree = stripExcluded(tree, nil, opts.ExcludePaths)
	tree = sortSets(tree, nil, opts.SetPaths)
	tree = normalizeLineEndings(tree)

	// Re-marshaling a map[string]any tree is what makes
	// encoding/json.Marshal sort object keys alphabetically — it does
	// not do that for a struct marshaled directly, only for map values,
	// which is exactly why target was round-tripped through `any` above
	// instead of being marshaled once.
	canonicalJSON, err = json.Marshal(tree)
	if err != nil {
		return nil, "", fmt.Errorf("authoring: marshal canonical form: %w", err)
	}
	digest := sha256.Sum256(canonicalJSON)
	return canonicalJSON, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func joinPath(path []string, segment string) []string {
	return append(append([]string(nil), path...), segment)
}

func pathKey(path []string) string {
	key := ""
	for i, segment := range path {
		if i > 0 {
			key += "."
		}
		key += segment
	}
	return key
}

func stripExcluded(node any, path []string, exclude map[string]bool) any {
	switch value := node.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			childPath := joinPath(path, key)
			if exclude[pathKey(childPath)] {
				continue
			}
			result[key] = stripExcluded(child, childPath, exclude)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, child := range value {
			result[i] = stripExcluded(child, joinPath(path, fmt.Sprintf("[%d]", i)), exclude)
		}
		return result
	default:
		return value
	}
}

func sortSets(node any, path []string, sets map[string]bool) any {
	switch value := node.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			result[key] = sortSets(child, joinPath(path, key), sets)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, child := range value {
			result[i] = sortSets(child, path, sets) // array elements share their array's own path, not an index segment, so SetPaths matches the array field itself
		}
		if sets[pathKey(path)] {
			sort.Slice(result, func(i, j int) bool {
				return canonicalLess(result[i], result[j])
			})
		}
		return result
	default:
		return value
	}
}

// normalizeLineEndings replaces CRLF with LF in every string value, so a
// multi-line field authored (or checked out) with Windows line endings
// hashes identically to the same content with Unix line endings — the
// same convention this repo's own .gitattributes already applies to
// tracked source files, applied here to authored field content instead.
func normalizeLineEndings(node any) any {
	switch value := node.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			result[key] = normalizeLineEndings(child)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, child := range value {
			result[i] = normalizeLineEndings(child)
		}
		return result
	case string:
		return strings.ReplaceAll(value, "\r\n", "\n")
	default:
		return value
	}
}

// canonicalLess orders two set elements by their own canonical JSON
// encoding — a total, deterministic order regardless of element type,
// so sorting a set never depends on map iteration order or any other
// non-deterministic factor.
func canonicalLess(a, b any) bool {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return bytes.Compare(aJSON, bJSON) < 0
}
