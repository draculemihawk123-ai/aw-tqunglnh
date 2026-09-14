package definitions

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workflowcompiler"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// writeQueryError maps a read-only query's own error
// (internal/app/definitions.GetDefinition/LoadVersion/LoadAnyVersion/
// ListVersions) onto the canonical envelope. ports.ErrPersistenceNotFound
// and ports.ErrDefinitionVersionNotFound both fold into the identical
// leakage-normalized WriteResourceHidden response — V6-02A's own policy,
// the same "does not exist" vs "exists in a scope the caller cannot see"
// indistinguishability workitem's own writeQueryError already establishes,
// applied here to the two distinct not-found sentinels this package's own
// two repository tables can produce.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrDefinitionVersionNotFound) {
		httpapi.WriteResourceHidden(w)
		return
	}
	writeAppOrInternal(w, err)
}

// writeCommandError maps every error internal/app/definitions.CreateDefinition/
// ValidateDraft/PublishDefinitionVersion (and the nine domain packages'
// own Compile/CompileFrom/CompileAndResolve they wrap) can actually
// return, enumerated by reading every one of those functions' own source:
//
//   - ports.ErrPersistenceNotFound / ports.ErrDefinitionVersionNotFound:
//     a dependency pin, or the Definition itself, does not exist —
//     leakage-normalized like every query above.
//   - ports.ErrReceiptConflict: a genuine concurrent race past this
//     package's own pre-dispatch LookupReceipt fast path.
//   - ports.ErrPersistenceAlreadyExists: PublishDefinitionVersion's own
//     Workflow path (ensureWorkflowDefinition) found the reused
//     DefinitionID's stored identity disagrees with what this route just
//     reloaded and supplied — a real, visible conflict, never a leakage
//     concern (the caller already has this exact DefinitionID).
//   - ports.ErrCrossProjectDependency: an author-declared dependency pin
//     (this route's own request body, for the eight non-Workflow kinds) or
//     a resolved workflow node reference names a definition outside the
//     Definition's own project — a request-shape problem, not a race.
//   - authoring.Diagnostics: one of the eight shared kinds' own
//     ValidateDocument/DecodeStrict found real problems in the authored
//     document — each Diagnostic's own Line/Column/Path becomes one
//     ErrorDetail, so a client sees exactly where in the source the
//     problem is (V6-05's own "Verify: location diagnostics" bullet).
//   - *workflow.ValidationError / *workflowcompiler.ResolutionError /
//     *workflowcompiler.AgentRoleValidationError: the Workflow-only
//     structural/dependency-resolution/agent-role equivalents — no source
//     position (workflow documents are decoded as plain JSON, never
//     through authoring.DecodeStrict), so each Problem string becomes one
//     ErrorDetail with no Field.
//
// Every unmatched error falls through to writeAppOrInternal.
func writeCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound), errors.Is(err, ports.ErrDefinitionVersionNotFound):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, ports.ErrPersistenceAlreadyExists):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, ports.ErrCrossProjectDependency):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	default:
		var diags authoring.Diagnostics
		if errors.As(err, &diags) {
			writeDiagnostics(w, diags)
			return
		}
		var workflowErr *workflow.ValidationError
		if errors.As(err, &workflowErr) {
			writeProblems(w, workflowErr.Problems)
			return
		}
		var resolutionErr *workflowcompiler.ResolutionError
		if errors.As(err, &resolutionErr) {
			writeProblems(w, resolutionErr.Problems)
			return
		}
		var agentRoleErr *workflowcompiler.AgentRoleValidationError
		if errors.As(err, &agentRoleErr) {
			writeProblems(w, agentRoleErr.Problems)
			return
		}
		writeAppOrInternal(w, err)
	}
}

// writeDiagnostics writes one 400 INVALID_REQUEST carrying one ErrorDetail
// per authoring.Diagnostic — Field is the diagnostic's own dot-separated
// Path (e.g. "nodes[2].outcomes"), Message embeds Line/Column plus the
// WHAT/WHY/FIX prose Diagnostic.Error() already formats, so a client never
// has to re-parse Diagnostic.Error()'s own string shape to recover the
// source position.
func writeDiagnostics(w http.ResponseWriter, diags authoring.Diagnostics) {
	details := make([]httpapi.ErrorDetail, 0, len(diags))
	for _, d := range diags {
		message := d.Error()
		if d.Line > 0 {
			message = fmt.Sprintf("line %d, column %d: %s", d.Line, d.Column, message)
		}
		details = append(details, httpapi.ErrorDetail{Field: d.Path, Message: message})
	}
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "document validation failed", details)
}

// writeProblems writes one 400 INVALID_REQUEST carrying one field-less
// ErrorDetail per free-text problem string (Workflow's own
// ValidationError/ResolutionError/AgentRoleValidationError, none of which
// carry a source position — see writeCommandError's own doc comment for
// why).
func writeProblems(w http.ResponseWriter, problems []string) {
	details := make([]httpapi.ErrorDetail, 0, len(problems))
	for _, p := range problems {
		details = append(details, httpapi.ErrorDetail{Message: p})
	}
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "workflow validation failed", details)
}

// writeValidationError writes a single field-level 400 — this package's
// own pre-dispatch body validation (done before ever building a command
// envelope or touching the database).
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}

// writeReceiptHashConflict writes the response for httpapi.
// ErrReceiptHashConflict (the same Idempotency-Key reused with a
// semantically different request), caught before any real command ever
// dispatches.
func writeReceiptHashConflict(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key reused with a different request", nil)
}

// writeAppOrInternal writes a real *apperror.Error (e.g. a wrapped sqlite
// failure — MapSQLiteError's own two outcomes, CodeUnavailable or
// CodeInternal) through the shared status table, or a bare 500 INTERNAL
// for anything else this package's own pre-dispatch validation should have
// already caught.
func writeAppOrInternal(w http.ResponseWriter, err error) {
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		httpapi.WriteAppError(w, err)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}
