package evidence

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// writeQueryError maps a query-side error from internal/app/runtime's own
// queries.go (ListEvidenceForWorkItem/GetEvidence/ListArtifactsForEvidence/
// ResolveEvidenceArtifactContent/GetContextSnapshot) to the shared httpapi
// envelope — mirrors internal/delivery/httpapi/workitem and
// internal/delivery/httpapi/message's own identical writeQueryError: both
// "genuinely does not exist" (ports.ErrPersistenceNotFound) and "exists but
// belongs to a project/work item/evidence row the caller's own path scope
// does not name" (ports.ErrScopeMismatch — queries.go's own scopeMismatch
// helper, also used for "this Artifact ID is not one of this Evidence row's
// own ArtifactReferences") fold into the identical WriteResourceHidden
// response, V6-02A's own leakage-normalization policy.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrScopeMismatch) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeContentError maps an error from ports.ArtifactStore.Verify/Open
// (getArtifactContent's own read path, after authorization has already
// succeeded via writeQueryError's identical sibling checks above) through
// httpapi.WriteAppError: a missing/purged object already comes back as a
// typed apperror.CodeNotFound (internal/adapters/artifactstore's own
// Verify), which WriteAppError maps to a real 404; a tampered object (bytes
// no longer matching their own recorded hash) is a plain, non-apperror
// error, which WriteAppError's own documented fallback maps to 500
// INTERNAL — a typed error either way, never a silently-served response,
// this task's own "tamper... surfaced as a typed error, not served
// silently" Verify bullet.
func writeContentError(w http.ResponseWriter, err error) {
	httpapi.WriteAppError(w, err)
}

// writeValidationError writes a single field-level 400 — this package's own
// pre-dispatch path-parameter validation.
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}
