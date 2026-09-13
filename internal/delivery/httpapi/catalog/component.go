package catalog

import (
	"errors"
	"net/http"
	"time"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// listProjectComponents handles `GET /projects/{id}/components` — "catalog
// component đã được repository onboarding/probe discover"
// (docs/design/01-system-design.md §6's own API sketch). This package
// exposes no create/mutate route for a Component at all: V6-03A's own
// "Không làm" line explicitly forbids "expose helper CreateComponent" —
// every Component this route can ever return was inserted by V3-02's own
// onboarding-probe worker (internal/app/repositoryprobe's finishActive),
// never by anything reachable from this package (proven by
// internal/archtest's own TestHTTPAPICatalogNeverCallsCreateComponent).
// The Project is reloaded first for the same reason
// listProjectRepositories does: ListComponents itself only filters by the
// stored project_id column and does not itself verify projectID names a
// real Project.
func (h *handler) listProjectComponents(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := appcatalog.GetProject(r.Context(), h.uow, ports.ProjectScope(projectID), projectID); err != nil {
		writeCatalogError(w, err)
		return
	}
	components, err := appcatalog.ListComponents(r.Context(), h.uow, projectID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	views := make([]componentView, 0, len(components))
	for _, c := range components {
		views = append(views, newComponentView(c))
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, componentListView{Components: views}, "")
}

// listComponentPackAssignments handles
// `GET /components/{id}/pack-assignments`. The Component is reloaded
// first (by ID alone, like getRepository — see
// appcatalog.GetComponent's own doc comment) both to learn its ProjectID
// for the response envelope and to give a nonexistent componentID a real
// 404 rather than a silently empty assignment list.
func (h *handler) listComponentPackAssignments(w http.ResponseWriter, r *http.Request) {
	componentID := r.PathValue("id")
	component, err := appcatalog.GetComponent(r.Context(), h.uow, componentID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	assignments, err := appcatalog.ListComponentPackAssignments(r.Context(), h.uow, componentID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	views := make([]packAssignmentView, 0, len(assignments))
	for _, a := range assignments {
		views = append(views, newPackAssignmentView(a))
	}

	// GetEffectiveComponentPackAssignment returns ports.ErrPersistenceNotFound
	// when the Component has simply never been assigned a pack yet — that
	// is an expected, common state (every Component starts this way, per
	// V3-01's own topology-discovery-before-pack-assignment ordering), not
	// a route failure; Effective stays nil for that one specific case and
	// only a genuinely different error is surfaced as a real failure.
	var effectiveView *packAssignmentView
	effective, err := appcatalog.GetEffectiveComponentPackAssignment(r.Context(), h.uow, componentID, time.Now().UTC())
	switch {
	case err == nil:
		view := newPackAssignmentView(effective)
		effectiveView = &view
	case errors.Is(err, ports.ErrPersistenceNotFound):
		// No assignment effective as of now — leave effectiveView nil.
	default:
		writeCatalogError(w, err)
		return
	}

	_ = httpapi.EncodeResult(w, http.StatusOK, packAssignmentListView{
		ComponentID: componentID, ProjectID: string(component.ProjectID),
		Assignments: views, Effective: effectiveView,
	}, "")
}

// assignComponentPackRequestBody is `POST /components/{id}/pack-assignments`'s
// own request shape. ComponentID/ProjectID/Actor are deliberately not
// fields here: ComponentID comes from the path, ProjectID is reloaded (see
// assignComponentPack below), and Actor is authentication context read
// from the bound principal (ADR-028: "Actor và ActorRoles ... HTTP body/
// header ... MUST NOT được phép override actor/roles") — never a value a
// caller could set through the body. EffectiveAt is optional: an omitted
// value defaults to the moment this request is processed ("assign this
// pack effective right now"), matching the common case; an explicit value
// lets a caller pin a pack version effective in the past or scheduled for
// the future, which project.NewComponentPackAssignment already allows
// (component.go's own doc comment: "component_pack_assignments lưu ...
// effective time ... để resolved configuration không phải suy ngầm từ
// UI").
type assignComponentPackRequestBody struct {
	PackVersionID string     `json:"packVersionId"`
	EffectiveAt   *time.Time `json:"effectiveAt,omitempty"`
}

// assignComponentPack handles `POST /components/{id}/pack-assignments` —
// "assignment pin exact version" (V6-03A's own "Thực hiện" line):
// PackVersionID is stored and returned verbatim, exactly as
// project.ComponentPackAssignment already guarantees (never resolved
// against "latest" or any other indirection this package could
// introduce).
func (h *handler) assignComponentPack(w http.ResponseWriter, r *http.Request) {
	componentID := r.PathValue("id")
	component, err := appcatalog.GetComponent(r.Context(), h.uow, componentID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	scope := ports.ProjectScope(string(component.ProjectID))
	var body assignComponentPackRequestBody
	cmd, ok := h.beginMutation(w, r, scope, "AssignComponentPack", 0, &body)
	if !ok {
		return
	}

	effectiveAt := cmd.RequestedAt
	if body.EffectiveAt != nil {
		effectiveAt = body.EffectiveAt.UTC()
	}
	principal := httpapi.PrincipalFromContext(r.Context())

	result, err := appcatalog.AssignComponentPack(r.Context(), h.uow, h.ids, cmd, appcatalog.AssignComponentPackRequest{
		ProjectID: string(component.ProjectID), ComponentID: componentID,
		PackVersionID: body.PackVersionID, EffectiveAt: effectiveAt, Actor: principal.Actor,
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusCreated, result, "")
}
