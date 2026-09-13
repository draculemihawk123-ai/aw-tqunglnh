package catalog

import (
	"fmt"
	"net/http"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// This file splits "repository detail" into two routes, matching V6-03A's
// own Phạm vi line verbatim ("repository register/list/detail/onboarding/
// probe-history/retry" — six distinct concerns, not four):
//   - GET /repositories/{id} (getRepository) returns the Repository's own
//     REGISTRATION identity — name/remoteLocator/defaultRef — plus its
//     current status/version, the "detail" concern.
//   - GET /repositories/{id}/onboarding (getRepositoryOnboarding) returns
//     status/error/version again, PLUS the full probe-history evidence log
//     — the exact combined query docs/design/01-system-design.md §6.1's own
//     API sketch describes for this one path ("trạng thái/error/probe
//     history có thể hành động"), which is why "onboarding" and
//     "probe-history" fold into one route rather than two: the system
//     design doc never sketches a separate probe-history-only path, and
//     onboarding status without its own evidence log would not itself be
//     "actionable" the way that sketch requires.
//
// Both getRepository and getRepositoryOnboarding, plus retryRepositoryProbe
// below, reach their target by RepositoryID alone (none of the three
// routes nests under /projects/{id}) — each one calls
// appcatalog.GetRepository FIRST to learn the Repository's own ProjectID
// before building any ports.CommandScope, per that function's own doc
// comment in internal/app/catalog/commands.go.

// getRepository handles `GET /repositories/{id}`.
func (h *handler) getRepository(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	repo, err := appcatalog.GetRepository(r.Context(), h.uow, id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, newRepositoryView(repo), httpapi.ETagFromVersion(repo.Version))
}

// getRepositoryOnboarding handles `GET /repositories/{id}/onboarding`.
func (h *handler) getRepositoryOnboarding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	repo, err := appcatalog.GetRepository(r.Context(), h.uow, id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	attempts, err := appcatalog.ListRepositoryProbeAttempts(r.Context(), h.uow, id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, newOnboardingView(repo, attempts), httpapi.ETagFromVersion(repo.Version))
}

// retryRepositoryProbeRequestBody is deliberately empty: retry-probe takes
// no caller-supplied content at all (RepositoryID comes from the path,
// ProjectID is reloaded, not trusted from any request) — a client still
// sends a literal `{}` body, the same "every mutation carries a real,
// strictly-decoded JSON body" discipline every other route in this
// package follows, rather than a special-cased empty-body allowance.
type retryRepositoryProbeRequestBody struct{}

// retryRepositoryProbe handles `POST /repositories/{id}/retry-probe`.
//
// Order of operations matters here and is deliberate: GetRepository (to
// learn ProjectID for scope) and RequireIfMatch/VersionFromETag (to learn
// ExpectedVersion) both run BEFORE beginMutation's own receipt lookup,
// because both are needed to construct the very scope/hash a receipt
// lookup keys on. But the "is this Repository actually BLOCKED"
// precondition check runs AFTER beginMutation returns ok — V6-02's own
// flow line "nếu absent mới kiểm current version/external prework/dispatch"
// (commandenvelope.go's own doc comment): a genuine replay of an earlier
// successful retry (the same Idempotency-Key resubmitted) must return the
// original stored result regardless of what status the Repository has
// moved to since, never be rejected by a status check that only makes
// sense for a genuinely NEW attempt.
func (h *handler) retryRepositoryProbe(w http.ResponseWriter, r *http.Request) {
	repositoryID := r.PathValue("id")
	repo, err := appcatalog.GetRepository(r.Context(), h.uow, repositoryID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	ifMatch, err := httpapi.RequireIfMatch(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return
	}
	expectedVersion, err := httpapi.VersionFromETag(ifMatch)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return
	}

	scope := ports.ProjectScope(string(repo.ProjectID))
	var body retryRepositoryProbeRequestBody
	cmd, ok := h.beginMutation(w, r, scope, "RetryRepositoryProbe", expectedVersion, &body)
	if !ok {
		return
	}

	// V6-03A's own "Thực hiện" line: "retry chỉ map RetryRepositoryProbe
	// khi BLOCKED". The real CAS inside RetryRepositoryProbe itself
	// (project.CanTransitionRepositoryStatus / TransitionRepositoryStatus's
	// ExpectedStatus=BLOCKED check) remains the sole AUTHORITATIVE
	// enforcement of this — this is a route-level fast-reject only, so an
	// obviously-wrong request never even reaches a doomed transaction
	// attempt.
	if repo.Status != project.RepositoryBlocked {
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			fmt.Sprintf("repository %s is %s, not BLOCKED — retry-probe is only valid for a blocked repository", repositoryID, repo.Status),
			nil)
		return
	}

	result, err := appcatalog.RetryRepositoryProbe(r.Context(), h.uow, h.ids, cmd, appcatalog.RetryRepositoryProbeRequest{
		RepositoryID: repositoryID, ProjectID: string(repo.ProjectID),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	// internal/adapters/sqlite's own transitionRepositoryStatusTx doc
	// comment: "On success, version increments by exactly one" — a
	// deterministic guarantee, so the new ETag needs no second read.
	etag := httpapi.ETagFromVersion(expectedVersion + 1)
	_ = httpapi.EncodeResult(w, http.StatusAccepted, result, etag)
}

// registerRepositoryRequestBody is `POST /projects/{id}/repositories`'s
// own request shape. RepositoryID is caller-chosen (mirroring
// appcatalog.RegisterRepositoryRequest's own doc comment: unlike a
// Project, a Repository's identity is named by its caller, not minted by
// this application) — ProjectID is deliberately NOT a field here: it
// comes from the URL path alone, never from the body, the same "route
// derives scope, không tin payload" discipline the design doc's own
// general contract (§1.3) requires everywhere.
type registerRepositoryRequestBody struct {
	RepositoryID  string `json:"repositoryId"`
	Name          string `json:"name"`
	RemoteLocator string `json:"remoteLocator"`
	DefaultRef    string `json:"defaultRef"`
}

// registerRepository handles `POST /projects/{id}/repositories`. Scope is
// built directly from the path — this route, unlike getRepository/
// getRepositoryOnboarding/retryRepositoryProbe above, already nests under
// /projects/{id}, so there is no ambiguous target to reload first; project
// existence is still verified, just by RegisterRepository's own
// persistence layer inside its one real transaction
// (registerRepositoryTx's own "project_id must name a Project that
// already exists" check), not by a redundant extra read here.
func (h *handler) registerRepository(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	scope := ports.ProjectScope(projectID)

	var body registerRepositoryRequestBody
	cmd, ok := h.beginMutation(w, r, scope, "RegisterRepository", 0, &body)
	if !ok {
		return
	}

	result, err := appcatalog.RegisterRepository(r.Context(), h.uow, h.ids, cmd, appcatalog.RegisterRepositoryRequest{
		RepositoryID: body.RepositoryID, ProjectID: projectID, Name: body.Name,
		RemoteLocator: body.RemoteLocator, DefaultRef: body.DefaultRef,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	// "register trả REGISTERING" (V6-03A's own "Thực hiện" line): never
	// simulated — result.Status is whatever project.NewRepository actually
	// set (always REGISTERING, RegisterRepository's own doc comment),
	// encoded verbatim, never overridden to look more "done" than it is.
	_ = httpapi.EncodeResult(w, http.StatusCreated, result, "")
}

// listProjectRepositories handles `GET /projects/{id}/repositories`. The
// Project is reloaded first so a nonexistent projectID reports a real 404
// rather than a silently empty list — ListProjectRepositories itself only
// ever filters by the stored project_id foreign-key column (its own doc
// comment in commands.go) and does not itself verify projectID names a
// real Project.
func (h *handler) listProjectRepositories(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := appcatalog.GetProject(r.Context(), h.uow, ports.ProjectScope(projectID), projectID); err != nil {
		writeCatalogError(w, err)
		return
	}
	repos, err := appcatalog.ListProjectRepositories(r.Context(), h.uow, projectID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	views := make([]repositoryView, 0, len(repos))
	for _, repo := range repos {
		views = append(views, newRepositoryView(repo))
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, repositoryListView{Repositories: views}, "")
}
