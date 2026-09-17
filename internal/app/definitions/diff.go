// This file is V6-15E's own promotion of what was, until this task, a
// deliberately-not-shared response-shaping helper:
// internal/delivery/httpapi/definitions/diff.go's own versionSummaryView/
// diffLineView/versionDiffView/newVersionDiffView/prettyJSONLines/
// diffLines (that file's own doc comment explains why V6-05 kept it local
// to the HTTP package rather than promoting it here — "response-shaping
// code for one specific caller, not a reusable application-layer query").
// That reasoning held only as long as HTTP was the only caller. V6-15E
// adds a second, independent caller (`aw version diff`,
// internal/delivery/cli/definition) that needs the identical comparison —
// same identity/hash summary per side, same Identical bool, same
// LCS-based line diff of re-indented CanonicalSource — so this is no
// longer "one specific caller"'s own shaping code; it is shared behavior
// two independent delivery mechanisms both need to produce byte-identical
// results for. Every function here is a pure, no-I/O transformation of two
// already-loaded definition.VersionFields (nothing here ever touches
// ports.UnitOfWork itself), so promoting it is a pure move, not a
// behavior change: internal/delivery/httpapi/definitions/diff.go now
// calls DiffVersions instead of carrying its own copy, and
// internal/delivery/cli/definition does the same — one implementation,
// two callers, byte-identical JSON shape preserved on the HTTP side (same
// field names/tags: a/b/identical/sourceDiff, id/definitionId/kind/
// versionNumber/sourceHash/compiledHash/publishedBy, op/text).
package definitions

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// VersionSummary is one side of a VersionDiff's own compact identity/hash
// summary — mirrors what internal/delivery/httpapi/definitions/diff.go's
// own versionSummaryView carried before promotion, field for field.
type VersionSummary struct {
	ID            string          `json:"id"`
	DefinitionID  string          `json:"definitionId"`
	Kind          definition.Kind `json:"kind"`
	VersionNumber uint64          `json:"versionNumber"`
	SourceHash    string          `json:"sourceHash"`
	CompiledHash  string          `json:"compiledHash"`
	PublishedBy   string          `json:"publishedBy"`
}

func newVersionSummary(v definition.VersionFields) VersionSummary {
	return VersionSummary{
		ID: v.ID(), DefinitionID: v.DefinitionID(), Kind: v.Kind(), VersionNumber: v.VersionNumber(),
		SourceHash: v.SourceHash(), CompiledHash: v.CompiledHash(), PublishedBy: v.PublishedBy(),
	}
}

// DiffLine is one line of a unified line diff: Op is "equal", "add"
// (present in B, not A) or "remove" (present in A, not B).
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// VersionDiff is DiffVersions' own result shape.
type VersionDiff struct {
	A VersionSummary `json:"a"`
	B VersionSummary `json:"b"`
	// Identical is true when a and b compiled to the exact same content
	// (CompiledHash equal) — AK-ARCH-005B's own dedup key, so this is the
	// same notion of "no real difference" PublishDefinitionVersion itself
	// already uses to decide whether republishing is a no-op.
	Identical  bool       `json:"identical"`
	SourceDiff []DiffLine `json:"sourceDiff"`
}

// DiffVersions computes a's and b's own compact identity/hash summaries
// plus a line-oriented diff of their pretty-printed CanonicalSource — a
// pure, no-I/O comparison of two already-loaded VersionFields (the caller
// is responsible for loading and scope-checking both, e.g. via
// LoadAnyVersion; this function never itself resolves an ID or touches a
// UnitOfWork). Callers needing the two versions to share the same Kind
// (V6-05's own "diff operands phải cùng scope"/same-Kind rule) must check
// a.Kind() == b.Kind() themselves before calling this — DiffVersions
// itself has no opinion on that; it will happily diff two different Kinds'
// CanonicalSource if asked to.
func DiffVersions(a, b definition.VersionFields) VersionDiff {
	return VersionDiff{
		A: newVersionSummary(a), B: newVersionSummary(b),
		Identical:  a.CompiledHash() == b.CompiledHash(),
		SourceDiff: diffLines(prettyJSONLines(a.CanonicalSource()), prettyJSONLines(b.CanonicalSource())),
	}
}

// prettyJSONLines re-indents compact canonical JSON into multiple lines
// before diffing — a single-line diff of the compact form would report
// "everything changed" for even a one-field edit, since the whole
// canonical document is one line.
func prettyJSONLines(canonicalJSON string) []string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(canonicalJSON), "", "  "); err != nil {
		return strings.Split(canonicalJSON, "\n")
	}
	return strings.Split(buf.String(), "\n")
}

// diffLines is a standard LCS-based line diff (see e.g. "An O(ND) Difference
// Algorithm and Its Variations", Myers 1986, for the general technique this
// simpler O(n*m) DP table follows). It never panics on any input, including
// empty a/b.
func diffLines(a, b []string) []DiffLine {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	result := make([]DiffLine, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			result = append(result, DiffLine{Op: "equal", Text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			result = append(result, DiffLine{Op: "remove", Text: a[i]})
			i++
		default:
			result = append(result, DiffLine{Op: "add", Text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		result = append(result, DiffLine{Op: "remove", Text: a[i]})
	}
	for ; j < m; j++ {
		result = append(result, DiffLine{Op: "add", Text: b[j]})
	}
	return result
}
