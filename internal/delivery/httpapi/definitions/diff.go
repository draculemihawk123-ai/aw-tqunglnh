// This file is V6-05's own diff route. The comparison logic itself
// (identity/hash summary per side, LCS-based line diff of pretty-printed
// CanonicalSource) was promoted to internal/app/definitions.DiffVersions by
// V6-15E once a second independent caller (`aw version diff`,
// internal/delivery/cli/definition) needed byte-identical behavior — see
// that function's own doc comment for the full reasoning. This file now
// only derives the two scoped Version operands and encodes the shared
// result; versionSummaryView/diffLineView/versionDiffView and the diff
// algorithm itself no longer have a copy here.
package definitions

import (
	"net/http"
	"strings"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
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

	_ = httpapi.EncodeResult(w, http.StatusOK, appdefinitions.DiffVersions(a, b), "")
}
