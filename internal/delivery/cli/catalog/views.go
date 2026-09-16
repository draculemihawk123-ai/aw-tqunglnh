package catalog

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// This file defines this package's own JSON view shapes for every
// read-only Run* function's own cli.EncodeQueryResult output — mirroring
// internal/delivery/httpapi/catalog/views.go's own reasoning exactly:
// project.Project/Repository/Component/ComponentPackAssignment and
// ports.RepositoryProbeAttempt carry no JSON tags of their own (they are
// not, and must never become, a wire contract this package does not
// control), so every view type here is a small, explicit, camelCase-tagged
// mapping this package owns end to end. The four mutating Run* functions
// (project create, repository register, repository retry-probe,
// pack-assignment assign) need no equivalent view: internal/app/catalog's
// own CreateProjectResult/RegisterRepositoryResult/
// RetryRepositoryProbeResult/AssignComponentPackResult already carry the
// exact camelCase json tags a CLI response needs, so those are encoded
// directly via cli.EncodeCommandResult with no separate wrapper.

// projectView is `aw project list`/`aw project show`'s own wire shape for
// a project.Project.
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

// repositoryView is `aw repository list`'s own wire shape for a
// project.Repository.
type repositoryView struct {
	ID                 string  `json:"id"`
	ProjectID          string  `json:"projectId"`
	Name               string  `json:"name"`
	RemoteLocator      string  `json:"remoteLocator"`
	DefaultRef         string  `json:"defaultRef"`
	Status             string  `json:"status"`
	LastProbeErrorCode *string `json:"lastProbeErrorCode,omitempty"`
	Version            uint64  `json:"version"`
}

func newRepositoryView(r project.Repository) repositoryView {
	return repositoryView{
		ID: string(r.ID), ProjectID: string(r.ProjectID), Name: r.Name,
		RemoteLocator: r.RemoteLocator, DefaultRef: r.DefaultRef,
		Status: string(r.Status), LastProbeErrorCode: r.LastProbeErrorCode, Version: r.Version,
	}
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

// onboardingView is `aw repository onboarding <id>`'s own response shape —
// repository status/error/version plus its full probe-history evidence
// log, composing appcatalog.GetRepository + appcatalog.ListRepositoryProbeAttempts
// exactly like internal/delivery/httpapi/catalog/repository.go's own
// getRepositoryOnboarding does for HTTP (see repository.go in this
// package). Status alone already distinguishes an onboarding still in
// flight (REGISTERING/PROBING, Attempts empty — no probe has completed
// yet) from one that has reached a verdict (ACTIVE/BLOCKED, Attempts
// carrying the attempt(s) that produced it) — the "async onboarding" Verify
// bullet this task's own brief names, made concrete.
type onboardingView struct {
	RepositoryID       string             `json:"repositoryId"`
	ProjectID          string             `json:"projectId"`
	Status             string             `json:"status"`
	LastProbeErrorCode *string            `json:"lastProbeErrorCode,omitempty"`
	Version            uint64             `json:"version"`
	Attempts           []probeAttemptView `json:"attempts"`
}

func newOnboardingView(r project.Repository, attempts []ports.RepositoryProbeAttempt) onboardingView {
	views := make([]probeAttemptView, 0, len(attempts))
	for _, a := range attempts {
		views = append(views, newProbeAttemptView(a))
	}
	return onboardingView{
		RepositoryID: string(r.ID), ProjectID: string(r.ProjectID), Status: string(r.Status),
		LastProbeErrorCode: r.LastProbeErrorCode, Version: r.Version, Attempts: views,
	}
}

// componentView is `aw component list <projectId>`'s own wire shape for a
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

// packAssignmentListView is `aw pack-assignment list <componentId>`'s own
// response shape: the full append-only history plus which one (if any) is
// effective right now — appcatalog.GetEffectiveComponentPackAssignment's
// own "resolved configuration không phải suy ngầm từ UI" made concrete as
// a response field, the exact "exact pack pin" Verify bullet this task's
// own brief names: Effective reflects version-pinned-AT-A-POINT-IN-TIME
// semantics (GetEffectiveComponentPackAssignment's own `at time.Time`
// parameter), never simply "the most recently assigned row" — the two can
// differ whenever an assignment's own EffectiveAt is in the future.
// Effective is nil, not omitted, when the Component has never been
// assigned a pack yet — a caller must be able to tell "no assignment"
// apart from "field absent because of a schema version caller doesn't
// understand", the same discipline
// internal/delivery/httpapi/catalog/views.go's own packAssignmentListView
// already documents.
type packAssignmentListView struct {
	ComponentID string               `json:"componentId"`
	ProjectID   string               `json:"projectId"`
	Assignments []packAssignmentView `json:"assignments"`
	Effective   *packAssignmentView  `json:"effective"`
}
