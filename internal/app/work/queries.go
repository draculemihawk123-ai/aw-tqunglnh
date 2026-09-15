// Authoritative (non-projected) WorkItem/TaskFamily/ScopeExpansionRequest
// read queries (V6-04, docs/design/08-v6-api-projections.md V6-04's own
// "Phạm vi: create/list/authoritative detail/child/readiness"). Every
// function in this file is read-only (uow.WithReadOnly, never
// WithSerializedWrite) and returns an HTTP-ready DTO — never a raw domain
// value — the same "command Result struct with json tags lives beside the
// command that produces it" convention CreateRootWorkItemResult/
// CreateChildWorkItemResult/RequestScopeExpansionResult above already
// establish, applied here to a query instead of a command.
//
// Every query here is project-scoped: ADR-025's own closed installation-scope
// table (docs/architecture/02-architecture-decisions.md §27) lists only
// health/doctor, ListProjects, safe-settings read and adapter-build
// list/detail as installation-scoped queries — WorkItem/TaskFamily/
// ScopeExpansionRequest are not among them, so every function below rejects
// the installation scope and any project scope that does not match the
// loaded target's own real, stored ProjectID, both via the shared
// ports.ErrScopeMismatch sentinel V6-03's own GetProject already established
// for exactly this shape ("resolve từ stored row, không tin request" applied
// to authorization instead of referential integrity). A caller (the HTTP
// layer) that finds ErrScopeMismatch treats it identically to
// ErrPersistenceNotFound — V6-02A's own leakage-normalization policy, so a
// guessed cross-project ID and a genuinely nonexistent one are
// indistinguishable to the caller.
//
// This is deliberately the AUTHORITATIVE half only, distinct from V6-10's
// own projected card/detail (docs/design/08-v6-api-projections.md V6-10:
// "projection không decide readiness/ValidAction or mutate state"): nothing
// here reads a projection table, and nothing in V6-10 is allowed to
// authorize a mutation — see this task's own "Không làm: không dùng projected
// detail để authorize" line, satisfied by construction (V6-10 does not exist
// yet, and every command in commands.go/scope_expansion.go already reloads
// its own target fresh from tx.Work(), never from anything this file
// returns).
//
// WorkItemDetail deliberately excludes the contract fields V3-03 added to
// work.WorkItem (SchemaVersion, Behavior, AcceptanceCriteria,
// VerificationSpec, RiskLevel, Exclusions, ApprovalException). Migration
// 0007 added six of their seven columns (every one but ApprovalException,
// which has no column at all); createWorkItemTx/getWorkItemTx
// (internal/adapters/sqlite/work.go) now round-trip those six (V6-04A's own
// necessary, minimal prerequisite for MarkWorkItemReady to ever transition a
// REAL sqlite-backed WorkItem to READY — see that function's own doc
// comment), but no PUBLIC command in this codebase populates a WorkItem's
// contract today: neither CreateRootWorkItem's nor CreateChildWorkItem's own
// domain constructor (work.NewRootWorkItem/work.NewChildWorkItem) ever
// touches these fields (this package's own commands.go doc comment says so
// explicitly: "A root WorkItem created via this command starts in BACKLOG
// with an empty contract; filling in the contract ... is a separate, later
// step no V3 task in this repository's own doc set builds yet"), so every
// WorkItem either of those two commands creates still persists (and reads
// back) with an entirely empty contract, exactly as before. Exposing those
// fields on the wire here would therefore still always read as empty for
// every WorkItem any real HTTP/CLI caller can create today — which would
// misrepresent "no public command sets this yet" as "this WorkItem genuinely
// has no behavior/verification spec set" (a real business fact a client
// could reasonably act on) — so this DTO still leaves them out. A future
// task that adds a real contract-authoring command is what would first make
// exposing them here honest; that command is still nobody's job yet in this
// codebase's own doc set. ExplainWorkItemReadiness below still runs the REAL
// workdomain.ValidateReadinessGate validator against the REAL loaded
// WorkItem (never a fabricated result) — its answer is honest about today's
// actual system state (every WorkItem any public command can create still
// fails the same completeness checks, though a caller that builds a
// work.WorkItem value directly — e.g. a test, per work.go's own "sets the
// exported fields directly" escape hatch — and persists it via
// tx.Work().CreateWorkItem now gets a row that genuinely round-trips and can
// genuinely pass).
package work

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// requireProjectScope validates scope actually names one project (never the
// installation scope) and returns that project ID — every query in this
// file needs this same precondition before it ever touches storage,
// mirroring internal/app/catalog.GetProject's own identical enforcement for
// its own single concern.
func requireProjectScope(scope ports.CommandScope) (string, error) {
	projectID, ok := scope.ProjectID()
	if !ok {
		return "", fmt.Errorf("work: %w: this query is project-scoped, not installation-scoped", ports.ErrScopeMismatch)
	}
	return projectID, nil
}

// scopeMismatch wraps ports.ErrScopeMismatch with a message naming what was
// loaded but rejected — used by every query below the moment a freshly
// loaded row's own stored ProjectID disagrees with the caller's scope.
func scopeMismatch(kind, id string) error {
	return fmt.Errorf("work: %w: %s %s belongs to another project", ports.ErrScopeMismatch, kind, id)
}

// --- WorkItem ---

// WorkItemDetail is the authoritative WorkItem detail GetWorkItem/
// ListWorkItems/ListChildWorkItems all return — see this file's own doc
// comment for why the V3-03 contract fields are deliberately absent.
type WorkItemDetail struct {
	WorkItemID        string `json:"workItemId"`
	ProjectID         string `json:"projectId"`
	FamilyID          string `json:"familyId"`
	Kind              string `json:"kind"`
	ParentWorkItemID  string `json:"parentWorkItemId,omitempty"`
	Title             string `json:"title"`
	Status            string `json:"status"`
	Version           uint64 `json:"version"`
	ParentJoinPolicy  string `json:"parentJoinPolicy,omitempty"`
	SourceNodeRunID   string `json:"sourceNodeRunId,omitempty"`
	WorkflowVersionID string `json:"workflowVersionId,omitempty"`
}

func workItemToDetail(item workdomain.WorkItem) WorkItemDetail {
	detail := WorkItemDetail{
		WorkItemID: string(item.ID), ProjectID: string(item.ProjectID), FamilyID: string(item.FamilyID),
		Kind: string(item.Kind), Title: item.Title, Status: string(item.Status), Version: item.Version,
		ParentJoinPolicy: string(item.ParentJoinPolicy),
	}
	if item.ParentID != nil {
		detail.ParentWorkItemID = string(*item.ParentID)
	}
	if item.SourceNodeRunID != nil {
		detail.SourceNodeRunID = string(*item.SourceNodeRunID)
	}
	if item.WorkflowVersionID != nil {
		detail.WorkflowVersionID = string(*item.WorkflowVersionID)
	}
	return detail
}

// GetWorkItem returns workItemID's own authoritative detail — distinct from
// V6-10's future projected detail (see this file's own doc comment).
func GetWorkItem(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, workItemID string) (WorkItemDetail, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return WorkItemDetail{}, err
	}
	var detail WorkItemDetail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		if string(item.ProjectID) != projectID {
			return scopeMismatch("work item", workItemID)
		}
		detail = workItemToDetail(item)
		return nil
	})
	return detail, err
}

// ListWorkItems returns every WorkItem (every Kind, every Status) in
// scope's own project, ordered by (CreatedAt, ID) — the authoritative,
// unfiltered list; a caller wanting only root items or only one family's own
// items filters client-side (this task's own "Phạm vi" line asks for "list",
// not a filter vocabulary V6-02A never froze for this concern).
func ListWorkItems(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope) ([]WorkItemDetail, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return nil, err
	}
	var result []WorkItemDetail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		items, err := tx.Work().ListWorkItemsByProject(ctx, projectID)
		if err != nil {
			return err
		}
		result = make([]WorkItemDetail, 0, len(items))
		for _, item := range items {
			result = append(result, workItemToDetail(item))
		}
		return nil
	})
	return result, err
}

// ListChildWorkItems returns parentWorkItemID's own direct children — the
// parent itself is reloaded and scope-checked first (never trusting the
// caller's claimed project for an ID that turns out to belong to another
// project), then every child is listed; a child's own ProjectID always
// equals its parent's by construction (GC-INV-01), so this deliberately does
// not re-check each child individually.
func ListChildWorkItems(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, parentWorkItemID string) ([]WorkItemDetail, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return nil, err
	}
	var result []WorkItemDetail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		parent, err := tx.Work().GetWorkItem(ctx, parentWorkItemID)
		if err != nil {
			return err
		}
		if string(parent.ProjectID) != projectID {
			return scopeMismatch("work item", parentWorkItemID)
		}
		children, err := tx.Work().ListChildWorkItems(ctx, parentWorkItemID)
		if err != nil {
			return err
		}
		result = make([]WorkItemDetail, 0, len(children))
		for _, child := range children {
			result = append(result, workItemToDetail(child))
		}
		return nil
	})
	return result, err
}

// --- TaskFamily ---

// TaskFamilyDetail is the authoritative TaskFamily detail GetTaskFamily
// returns.
type TaskFamilyDetail struct {
	FamilyID       string `json:"familyId"`
	ProjectID      string `json:"projectId"`
	RootWorkItemID string `json:"rootWorkItemId"`
	ScopeVersion   uint64 `json:"scopeVersion"`
	Status         string `json:"status"`
	Version        uint64 `json:"version"`
}

// GetTaskFamily returns familyID's own authoritative detail.
func GetTaskFamily(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, familyID string) (TaskFamilyDetail, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return TaskFamilyDetail{}, err
	}
	var detail TaskFamilyDetail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		family, err := tx.Work().GetTaskFamily(ctx, familyID)
		if err != nil {
			return err
		}
		if string(family.ProjectID) != projectID {
			return scopeMismatch("task family", familyID)
		}
		detail = TaskFamilyDetail{
			FamilyID: string(family.ID), ProjectID: string(family.ProjectID), RootWorkItemID: string(family.RootWorkItemID),
			ScopeVersion: family.ScopeVersion, Status: string(family.Status), Version: family.Version,
		}
		return nil
	})
	return detail, err
}

// --- ScopeExpansionRequest ---

// RequestedGrantView is one RequestedGrant's own wire shape — mirrors
// workdomain.RequestedGrant's fields exactly, with json tags, since the
// domain type itself carries none.
type RequestedGrantView struct {
	RepositoryID string   `json:"repositoryId"`
	Access       string   `json:"access"`
	PathScopes   []string `json:"pathScopes,omitempty"`
	Reason       string   `json:"reason"`
}

// ScopeExpansionRequestDetail is the authoritative ScopeExpansionRequest
// detail GetScopeExpansionRequest returns — everything a caller needs to
// decide (approve/reject) or to withdraw it, including its own current
// Version for the If-Match precondition those three mutations require.
type ScopeExpansionRequestDetail struct {
	RequestID            string               `json:"requestId"`
	FamilyID             string               `json:"familyId"`
	ProjectID            string               `json:"projectId"`
	RequestedGrants      []RequestedGrantView `json:"requestedGrants"`
	Reason               string               `json:"reason"`
	ReferencedWorkItemID string               `json:"referencedWorkItemId,omitempty"`
	Status               string               `json:"status"`
	RequestedBy          string               `json:"requestedBy"`
	RequestedAt          time.Time            `json:"requestedAt"`
	DecidedBy            string               `json:"decidedBy,omitempty"`
	DecidedAt            *time.Time           `json:"decidedAt,omitempty"`
	DecisionNote         string               `json:"decisionNote,omitempty"`
	ApprovedScopeVersion *uint64              `json:"approvedScopeVersion,omitempty"`
	Version              uint64               `json:"version"`
}

func scopeExpansionRequestToDetail(req workdomain.ScopeExpansionRequest) ScopeExpansionRequestDetail {
	grants := make([]RequestedGrantView, 0, len(req.RequestedGrants))
	for _, grant := range req.RequestedGrants {
		grants = append(grants, RequestedGrantView{
			RepositoryID: string(grant.RepositoryID), Access: string(grant.Access),
			PathScopes: grant.PathScopes, Reason: grant.Reason,
		})
	}
	detail := ScopeExpansionRequestDetail{
		RequestID: string(req.ID), FamilyID: string(req.FamilyID), ProjectID: string(req.ProjectID),
		RequestedGrants: grants, Reason: req.Reason, Status: string(req.Status),
		RequestedBy: req.RequestedBy, RequestedAt: req.RequestedAt, DecidedBy: req.DecidedBy,
		DecidedAt: req.DecidedAt, DecisionNote: req.DecisionNote, ApprovedScopeVersion: req.ApprovedScopeVersion,
		Version: req.Version,
	}
	if req.ReferencedWorkItemID != nil {
		detail.ReferencedWorkItemID = string(*req.ReferencedWorkItemID)
	}
	return detail
}

// GetScopeExpansionRequest returns requestID's own authoritative detail.
func GetScopeExpansionRequest(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, requestID string) (ScopeExpansionRequestDetail, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return ScopeExpansionRequestDetail{}, err
	}
	var detail ScopeExpansionRequestDetail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		req, err := tx.Work().GetScopeExpansionRequest(ctx, requestID)
		if err != nil {
			return err
		}
		if string(req.ProjectID) != projectID {
			return scopeMismatch("scope expansion request", requestID)
		}
		detail = scopeExpansionRequestToDetail(req)
		return nil
	})
	return detail, err
}

// --- Readiness ---

// WorkItemReadiness is ExplainWorkItemReadiness's own result: whether
// workItemID currently passes workdomain.ValidateReadinessGate, and every
// concrete reason it does not, if any. This is a pure explanation — it never
// mutates the WorkItem and never itself attempts the BACKLOG->READY
// transition (that narrow, named MarkWorkItemReady command is explicitly
// V6-04A's own separate job, not this task's — see this task's own "Không
// làm: không generic status/family/workspace setter" line).
type WorkItemReadiness struct {
	WorkItemID string   `json:"workItemId"`
	Status     string   `json:"status"`
	Version    uint64   `json:"version"`
	Ready      bool     `json:"ready"`
	Problems   []string `json:"problems,omitempty"`
}

// ExplainWorkItemReadiness loads workItemID's own real, current WorkItem and
// runs the real workdomain.ValidateReadinessGate validator against it — see
// this file's own top-of-file doc comment for why every real WorkItem today
// reports the identical set of problems (no command in this codebase yet
// populates a WorkItem's own contract fields), and why that is this
// function's own honest, correct answer rather than a bug to work around
// here.
func ExplainWorkItemReadiness(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, workItemID string) (WorkItemReadiness, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return WorkItemReadiness{}, err
	}
	var result WorkItemReadiness
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		if string(item.ProjectID) != projectID {
			return scopeMismatch("work item", workItemID)
		}
		result = WorkItemReadiness{WorkItemID: string(item.ID), Status: string(item.Status), Version: item.Version, Ready: true}
		gateErr := workdomain.ValidateReadinessGate(item)
		if gateErr == nil {
			return nil
		}
		var readinessErr *workdomain.ReadinessError
		if errors.As(gateErr, &readinessErr) {
			result.Ready = false
			result.Problems = readinessErr.Problems
			return nil
		}
		// ValidateReadinessGate's own contract only ever returns nil or a
		// *workdomain.ReadinessError (see that function's own doc comment) —
		// this branch is unreachable today, kept only so a future change to
		// that contract fails loudly here instead of silently swallowing a
		// new error shape.
		return fmt.Errorf("work: unexpected readiness validation error: %w", gateErr)
	})
	return result, err
}
