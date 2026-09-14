// This file is V6-05's own genuine gap-closer: no Diff query existed
// anywhere in this codebase before this task (confirmed by reading
// internal/app/definitions/commands.go, ports.DefinitionsRepository and
// cmd/aw/definition.go's own CLI 'diff' subcommand, which builds this
// exact comparison ad hoc, locally, rather than calling a shared query —
// there was none to call). This is a fourth independent copy of that
// shape (versionSummaryView/diffLineView/versionDiffView/diffLines/
// prettyJSONLines all mirror cmd/aw/definition.go's own identically-named
// values line for line), kept local to this package rather than promoted
// to internal/app/definitions for the same reason
// internal/app/definitions/commands.go's own workflowVersionToVersionFields
// doc comment already gives for its own triplicated conversion: this is
// response-shaping code for one specific caller, not a reusable
// application-layer query.
package definitions

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// handleDiffDefinitionVersions implements
// GET /definitions/versions/diff?a=<versionId>&b=<versionId> (operationId
// diffDefinitionVersions).
func handleDiffDefinitionVersions(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		diffDefinitionVersionsCore(w, r, deps, definition.GlobalScope())
	}
}

// handleDiffProjectDefinitionVersions implements
// GET /projects/{projectId}/definitions/versions/diff?a=&b= (operationId
// diffProjectDefinitionVersions): the project-scoped half.
func handleDiffProjectDefinitionVersions(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		diffDefinitionVersionsCore(w, r, deps, definitionScopeFromProjectID(projectID))
	}
}

func diffDefinitionVersionsCore(w http.ResponseWriter, r *http.Request, deps Dependencies, routeScope definition.Scope) {
	versionIDA := r.URL.Query().Get("a")
	versionIDB := r.URL.Query().Get("b")
	if strings.TrimSpace(versionIDA) == "" {
		writeValidationError(w, "a", "is required")
		return
	}
	if strings.TrimSpace(versionIDB) == "" {
		writeValidationError(w, "b", "is required")
		return
	}

	// Diff operands must be the same scope (V6-05's own "Thực hiện: diff
	// operands phải cùng scope") — checking each independently against
	// THIS route's own single fixed routeScope is strictly stronger than a
	// pairwise A==B comparison would be: it also refuses either operand
	// belonging to some OTHER scope neither the caller's route nor the
	// other operand names, not just a mismatch between the two.
	a, ok := loadVersionInScope(r.Context(), w, deps, versionIDA, routeScope)
	if !ok {
		return
	}
	b, ok := loadVersionInScope(r.Context(), w, deps, versionIDB, routeScope)
	if !ok {
		return
	}
	if a.Kind() != b.Kind() {
		writeValidationError(w, "b", "must be the same Kind as a")
		return
	}

	_ = httpapi.EncodeResult(w, http.StatusOK, newVersionDiffView(a, b), "")
}

// versionSummaryView is diff's own compact per-side identity/hash summary
// — mirrors cmd/aw/definition.go's own versionSummaryView.
type versionSummaryView struct {
	ID            string          `json:"id"`
	DefinitionID  string          `json:"definitionId"`
	Kind          definition.Kind `json:"kind"`
	VersionNumber uint64          `json:"versionNumber"`
	SourceHash    string          `json:"sourceHash"`
	CompiledHash  string          `json:"compiledHash"`
	PublishedBy   string          `json:"publishedBy"`
}

func newVersionSummaryView(v definition.VersionFields) versionSummaryView {
	return versionSummaryView{
		ID: v.ID(), DefinitionID: v.DefinitionID(), Kind: v.Kind(), VersionNumber: v.VersionNumber(),
		SourceHash: v.SourceHash(), CompiledHash: v.CompiledHash(), PublishedBy: v.PublishedBy(),
	}
}

// diffLineView is one line of a unified line diff: Op is "equal", "add"
// (present in B, not A) or "remove" (present in A, not B) — mirrors
// cmd/aw/definition.go's own diffLineView.
type diffLineView struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

type versionDiffView struct {
	A versionSummaryView `json:"a"`
	B versionSummaryView `json:"b"`
	// Identical is true when a and b compiled to the exact same content
	// (CompiledHash equal) — AK-ARCH-005B's own dedup key, so this is the
	// same notion of "no real difference" PublishDefinitionVersion itself
	// already uses to decide whether republishing is a no-op.
	Identical  bool           `json:"identical"`
	SourceDiff []diffLineView `json:"sourceDiff"`
}

func newVersionDiffView(a, b definition.VersionFields) versionDiffView {
	return versionDiffView{
		A: newVersionSummaryView(a), B: newVersionSummaryView(b),
		Identical:  a.CompiledHash() == b.CompiledHash(),
		SourceDiff: diffLines(prettyJSONLines(a.CanonicalSource()), prettyJSONLines(b.CanonicalSource())),
	}
}

// prettyJSONLines re-indents compact canonical JSON into multiple lines
// before diffing — mirrors cmd/aw/definition.go's own prettyJSONLines
// exactly (see that function's own doc comment for why a single-line diff
// would be useless).
func prettyJSONLines(canonicalJSON string) []string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(canonicalJSON), "", "  "); err != nil {
		return strings.Split(canonicalJSON, "\n")
	}
	return strings.Split(buf.String(), "\n")
}

// diffLines is a standard LCS-based line diff — mirrors
// cmd/aw/definition.go's own diffLines exactly (see that function's own
// doc comment for the algorithm/complexity rationale). It never panics on
// any input, including empty a/b.
func diffLines(a, b []string) []diffLineView {
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

	result := make([]diffLineView, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			result = append(result, diffLineView{Op: "equal", Text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			result = append(result, diffLineView{Op: "remove", Text: a[i]})
			i++
		default:
			result = append(result, diffLineView{Op: "add", Text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		result = append(result, diffLineView{Op: "remove", Text: a[i]})
	}
	for ; j < m; j++ {
		result = append(result, diffLineView{Op: "add", Text: b[j]})
	}
	return result
}
