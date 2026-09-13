package catalog

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// This file defines this package's own wire response shapes for every GET
// route. They are deliberately distinct types from the domain structs
// they read (project.Project/Repository/Component/ComponentPackAssignment,
// ports.RepositoryProbeAttempt) rather than serializing those directly:
// none of those domain types carry JSON tags (they are not, and must
// never become, a wire contract this package does not control — a field
// rename inside internal/domain/project must never silently rename this
// package's own JSON response), so every view type here is a small,
// explicit, camelCase-tagged mapping this package owns end to end.
//
// The four mutating routes in this package (createProject,
// registerRepository, retryRepositoryProbe, assignComponentPack) do NOT
// need an equivalent response view: internal/app/catalog's own
// CreateProjectResult/RegisterRepositoryResult/RetryRepositoryProbeResult/
// AssignComponentPackResult already carry the exact camelCase json tags a
// wire response needs, so those are encoded directly via httpapi.EncodeResult
// with no separate wrapper.

// projectView is `GET /projects` and `GET /projects/{id}`'s own wire shape
// for a project.Project.
type projectView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version uint64 `json:"version"`
}

func newProjectView(p project.Project) projectView {
	return projectView{ID: string(p.ID), Name: p.Name, Status: string(p.Status), Version: p.Version}
}

type projectListView struct {
	Projects []projectView `json:"projects"`
}

// repositoryView is `GET /repositories/{id}`'s own wire shape for a
// project.Repository — the "detail" half of V6-03A's own "repository ...
// detail/onboarding" split (see repository.go's own doc comment for
// exactly which fields each of the two GET routes returns and why they
// are two routes rather than one).
type repositoryView struct {
	ID                 string                `json:"id"`
	ProjectID          string                `json:"projectId"`
	Name               string                `json:"name"`
	RemoteLocator      string                `json:"remoteLocator"`
	DefaultRef         string                `json:"defaultRef"`
	Status             string                `json:"status"`
	LastProbeErrorCode *string               `json:"lastProbeErrorCode,omitempty"`
	Version            uint64                `json:"version"`
	ValidActions       []httpapi.ValidAction `json:"validActions,omitempty"`
}

func newRepositoryView(r project.Repository) repositoryView {
	return repositoryView{
		ID: string(r.ID), ProjectID: string(r.ProjectID), Name: r.Name,
		RemoteLocator: r.RemoteLocator, DefaultRef: r.DefaultRef,
		Status: string(r.Status), LastProbeErrorCode: r.LastProbeErrorCode, Version: r.Version,
		ValidActions: retryProbeValidActions(r),
	}
}

// retryProbeValidActions returns the one advisory httpapi.ValidAction
// V6-03A's own "Thực hiện" line describes ("retry chỉ map
// RetryRepositoryProbe khi BLOCKED") whenever r is currently BLOCKED, or
// nil otherwise — never authority (httpapi.ValidAction's own doc comment:
// "chỉ advisory"), only a hint a client uses to decide whether to show a
// retry control at all and which TargetVersion to send back as If-Match if
// it does. The real POST /repositories/{id}/retry-probe route re-derives
// and re-checks BLOCKED itself against the live row regardless of what
// this hint said a moment earlier — see repository.go's own
// retryRepositoryProbe.
func retryProbeValidActions(r project.Repository) []httpapi.ValidAction {
	if r.Status != project.RepositoryBlocked {
		return nil
	}
	return []httpapi.ValidAction{{
		OperationID: "repositoriesRetryProbe", ScopeKind: httpapi.ScopeProject, TargetVersion: int64(r.Version),
	}}
}

type repositoryListView struct {
	Repositories []repositoryView `json:"repositories"`
}

// probeAttemptView is one ports.RepositoryProbeAttempt row's own wire
// shape, embedded in onboardingView below.
type probeAttemptView struct {
	ID           string    `json:"id"`
	JobID        string    `json:"jobId"`
	State        string    `json:"state"`
	Result       *string   `json:"result,omitempty"`
	ErrorCode    *string   `json:"errorCode,omitempty"`
	ErrorMessage *string   `json:"errorMessage,omitempty"`
	BaseCommit   *string   `json:"baseCommit,omitempty"`
	Dirty        *bool     `json:"dirty,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

func newProbeAttemptView(a ports.RepositoryProbeAttempt) probeAttemptView {
	view := probeAttemptView{
		ID: a.ID, JobID: a.JobID, State: string(a.State),
		ErrorCode: a.ErrorCode, ErrorMessage: a.ErrorMessage, BaseCommit: a.BaseCommit, Dirty: a.Dirty,
		CreatedAt: a.CreatedAt,
	}
	if a.Result != nil {
		result := string(*a.Result)
		view.Result = &result
	}
	return view
}

// onboardingView is `GET /repositories/{id}/onboarding`'s own response
// shape — repository status/error/version plus its full probe-history
// evidence log, the one combined query
// docs/design/01-system-design.md §6.1's own API sketch describes ("GET
// /repositories/{id}/onboarding | trạng thái/error/probe history có thể
// hành động").
type onboardingView struct {
	RepositoryID       string                `json:"repositoryId"`
	ProjectID          string                `json:"projectId"`
	Status             string                `json:"status"`
	LastProbeErrorCode *string               `json:"lastProbeErrorCode,omitempty"`
	Version            uint64                `json:"version"`
	Attempts           []probeAttemptView    `json:"attempts"`
	ValidActions       []httpapi.ValidAction `json:"validActions,omitempty"`
}

func newOnboardingView(r project.Repository, attempts []ports.RepositoryProbeAttempt) onboardingView {
	views := make([]probeAttemptView, 0, len(attempts))
	for _, a := range attempts {
		views = append(views, newProbeAttemptView(a))
	}
	return onboardingView{
		RepositoryID: string(r.ID), ProjectID: string(r.ProjectID), Status: string(r.Status),
		LastProbeErrorCode: r.LastProbeErrorCode, Version: r.Version, Attempts: views,
		ValidActions: retryProbeValidActions(r),
	}
}

// componentView is `GET /projects/{id}/components`'s own wire shape for a
// project.Component.
type componentView struct {
	ID           string `json:"id"`
	ProjectID    string `json:"projectId"`
	RepositoryID string `json:"repositoryId"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	Version      uint64 `json:"version"`
}

func newComponentView(c project.Component) componentView {
	return componentView{
		ID: string(c.ID), ProjectID: string(c.ProjectID), RepositoryID: string(c.RepositoryID),
		Name: c.Name, Path: c.Path, Kind: c.Kind, Version: c.Version,
	}
}

type componentListView struct {
	Components []componentView `json:"components"`
}

// packAssignmentView is one project.ComponentPackAssignment row's own wire
// shape.
type packAssignmentView struct {
	ID            string    `json:"id"`
	ComponentID   string    `json:"componentId"`
	PackVersionID string    `json:"packVersionId"`
	EffectiveAt   time.Time `json:"effectiveAt"`
	Actor         string    `json:"actor"`
}

func newPackAssignmentView(a project.ComponentPackAssignment) packAssignmentView {
	return packAssignmentView{
		ID: string(a.ID), ComponentID: string(a.ComponentID), PackVersionID: string(a.PackVersionID),
		EffectiveAt: a.EffectiveAt, Actor: a.Actor,
	}
}

// packAssignmentListView is `GET /components/{id}/pack-assignments`'s own
// response shape: the full append-only history plus which one (if any) is
// effective right now — internal/app/catalog.GetEffectiveComponentPackAssignment's
// own "resolved configuration không phải suy ngầm từ UI" made concrete as
// a response field, rather than making every caller re-derive "latest
// EffectiveAt <= now" from the raw list itself. Effective is nil, not
// omitted, when the Component has never been assigned a pack yet — a
// caller must be able to tell "no assignment" apart from "field absent
// because of a schema version caller doesn't understand".
type packAssignmentListView struct {
	ComponentID string               `json:"componentId"`
	ProjectID   string               `json:"projectId"`
	Assignments []packAssignmentView `json:"assignments"`
	Effective   *packAssignmentView  `json:"effective"`
}
