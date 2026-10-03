package catalog

import (
	"net/http"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// V9-08 (gap G8, docs/design/12-v9-harness-alignment.md): the repository's
// readiness profile and its baseline, over HTTP. Like getRepository and
// retryRepositoryProbe these routes reach their target by RepositoryID alone,
// so each reloads the Repository first to learn its ProjectID before building
// any CommandScope. The commands and the query are internal/app/readinesscheck's
// (operator.go); the `aw repository readiness ...` leaves call the same
// functions.

// setReadinessProfileRequestBody is `PUT /repositories/{id}/readiness-profile`'s
// request: the verification command every profile has, and an optional setup
// command that runs before it. Commands are an executable plus argv, never a
// shell string.
type setReadinessProfileRequestBody struct {
	Setup        *readinesscheck.CommandInput `json:"setup,omitempty"`
	Verification readinesscheck.CommandInput  `json:"verification"`
}

// verifyReadinessRequestBody is deliberately empty: `verify` takes no content.
type verifyReadinessRequestBody struct{}

// acceptBaselineExceptionRequestBody names the failed attempt being accepted
// and the operator's reason.
type acceptBaselineExceptionRequestBody struct {
	BaselineAttemptID string `json:"baselineAttemptId"`
	Reason            string `json:"reason"`
}

// getRepositoryReadiness handles `GET /repositories/{id}/readiness`.
func (h *handler) getRepositoryReadiness(w http.ResponseWriter, r *http.Request) {
	repositoryID := r.PathValue("id")
	repo, err := appcatalog.GetRepository(r.Context(), h.uow, repositoryID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	view, err := readinesscheck.GetRepositoryReadiness(r.Context(), h.uow, string(repo.ProjectID), repositoryID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, view, "")
}

// setRepositoryReadinessProfile handles `PUT /repositories/{id}/readiness-profile`.
func (h *handler) setRepositoryReadinessProfile(w http.ResponseWriter, r *http.Request) {
	repositoryID := r.PathValue("id")
	repo, err := appcatalog.GetRepository(r.Context(), h.uow, repositoryID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	var body setReadinessProfileRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(string(repo.ProjectID)), "SetRepositoryReadinessProfile", 0, &body)
	if !ok {
		return
	}
	result, err := readinesscheck.SetRepositoryReadinessProfile(r.Context(), h.uow, h.ids, cmd, readinesscheck.SetRepositoryReadinessProfileRequest{
		ProjectID: string(repo.ProjectID), RepositoryID: repositoryID, Setup: body.Setup, Verification: body.Verification,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
}

// verifyRepositoryReadiness handles `POST /repositories/{id}/readiness/verify`:
// it enqueues the baseline again; the result of the check is read back with
// getRepositoryReadiness once the worker has run it.
func (h *handler) verifyRepositoryReadiness(w http.ResponseWriter, r *http.Request) {
	repositoryID := r.PathValue("id")
	repo, err := appcatalog.GetRepository(r.Context(), h.uow, repositoryID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	var body verifyReadinessRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(string(repo.ProjectID)), "RequestBaselineCheck", 0, &body)
	if !ok {
		return
	}
	result, err := readinesscheck.RequestBaselineCheck(r.Context(), h.uow, h.ids, cmd, readinesscheck.RequestBaselineCheckRequest{
		ProjectID: string(repo.ProjectID), RepositoryID: repositoryID,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusAccepted, result, "")
}

// acceptRepositoryBaselineException handles
// `POST /repositories/{id}/readiness/exceptions`.
func (h *handler) acceptRepositoryBaselineException(w http.ResponseWriter, r *http.Request) {
	repositoryID := r.PathValue("id")
	repo, err := appcatalog.GetRepository(r.Context(), h.uow, repositoryID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	var body acceptBaselineExceptionRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(string(repo.ProjectID)), "AcceptBaselineException", 0, &body)
	if !ok {
		return
	}
	result, err := readinesscheck.AcceptBaselineException(r.Context(), h.uow, h.ids, cmd, readinesscheck.AcceptBaselineExceptionRequest{
		ProjectID: string(repo.ProjectID), RepositoryID: repositoryID, BaselineAttemptID: body.BaselineAttemptID, Reason: body.Reason,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusCreated, result, "")
}
