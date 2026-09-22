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
// WorkItemDetail's optional Contract (V6-04B) reports the contract fields
// V3-03 added to work.WorkItem (SchemaVersion, Behavior, AcceptanceCriteria,
// VerificationSpec, RiskLevel, Exclusions, WorkflowVersionID) exactly as
// stored. Until V6-04B the detail deliberately left them out: migration 0007
// gave six of their seven columns (every one but ApprovalException, which has
// no column at all) and V6-04A made createWorkItemTx/getWorkItemTx round-trip
// them, but no PUBLIC command populated a WorkItem's contract, so exposing the
// fields would have always read as empty and misrepresented "no public command
// sets this yet" as "this WorkItem genuinely has no behavior/verification
// spec". CreateRootWorkItem/CreateChildWorkItem now accept a contract at
// creation (contract.go), so a stored contract is a real fact a client can act
// on and the detail can honestly show it. The change is additive: Contract is
// a pointer with omitempty, absent for a WorkItem whose stored contract is
// entirely empty (every WorkItem created before V6-04B, or created without
// one), so those responses are byte-for-byte what they were. ApprovalException
// is still not shown — there is nothing stored to show.
// ExplainWorkItemReadiness below runs the REAL workdomain.ValidateReadinessGate
// validator against the REAL loaded WorkItem (never a fabricated result), so
// its answer always reflects whatever contract is actually stored.
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
// comment for what its optional Contract reports and why it is additive.
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
	// Contract is the stored readiness contract, nil (omitted) when nothing of
	// it is stored. Its own workflowVersionId repeats the top-level field above
	// on purpose, so a contract reads back with exactly the shape it was
	// created with; the top-level field is unchanged for existing clients.
	Contract *WorkItemContractView `json:"contract,omitempty"`
}

// AcceptanceCriterionView is one stored acceptance criterion — the response
// counterpart of AcceptanceCriterionRequest.
type AcceptanceCriterionView struct {
	Description     string `json:"description"`
	VerificationRef string `json:"verificationRef,omitempty"`
}

// WorkItemContractView is a WorkItem's stored readiness contract — the
// response counterpart of WorkItemContractRequest, with the identical JSON
// keys the create request's "contract" object uses.
type WorkItemContractView struct {
	SchemaVersion      int                       `json:"schemaVersion,omitempty"`
	Behavior           string                    `json:"behavior,omitempty"`
	AcceptanceCriteria []AcceptanceCriterionView `json:"acceptanceCriteria,omitempty"`
	VerificationSpec   string                    `json:"verificationSpec,omitempty"`
	RiskLevel          string                    `json:"riskLevel,omitempty"`
	Exclusions         []string                  `json:"exclusions,omitempty"`
	WorkflowVersionID  string                    `json:"workflowVersionId,omitempty"`
}

// workItemContractToView reports item's stored contract, or nil when none of
// it is stored (so a WorkItem without a contract serializes exactly as it did
// before V6-04B).
func workItemContractToView(item workdomain.WorkItem) *WorkItemContractView {
	view := WorkItemContractView{
		SchemaVersion: item.SchemaVersion, Behavior: item.Behavior, VerificationSpec: item.VerificationSpec,
		RiskLevel: string(item.RiskLevel),
	}
	if len(item.AcceptanceCriteria) > 0 {
		view.AcceptanceCriteria = make([]AcceptanceCriterionView, 0, len(item.AcceptanceCriteria))
		for _, criterion := range item.AcceptanceCriteria {
			view.AcceptanceCriteria = append(view.AcceptanceCriteria, AcceptanceCriterionView{
				Description: criterion.Description, VerificationRef: criterion.VerificationRef,
			})
		}
	}
	if len(item.Exclusions) > 0 {
		view.Exclusions = append([]string(nil), item.Exclusions...)
	}
	if item.WorkflowVersionID != nil {
		view.WorkflowVersionID = string(*item.WorkflowVersionID)
	}
	if view.SchemaVersion == 0 && view.Behavior == "" && len(view.AcceptanceCriteria) == 0 &&
		view.VerificationSpec == "" && view.RiskLevel == "" && len(view.Exclusions) == 0 && view.WorkflowVersionID == "" {
		return nil
	}
	return &view
}

func workItemToDetail(item workdomain.WorkItem) WorkItemDetail {
	detail := WorkItemDetail{
		WorkItemID: string(item.ID), ProjectID: string(item.ProjectID), FamilyID: string(item.FamilyID),
		Kind: string(item.Kind), Title: item.Title, Status: string(item.Status), Version: item.Version,
		ParentJoinPolicy: string(item.ParentJoinPolicy), Contract: workItemContractToView(item),
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
// this file's own top-of-file doc comment. The answer always reflects
// whatever contract is actually stored: a WorkItem created without a contract
// (the only kind that existed before V6-04B) reports the full list of
// completeness problems, one created with a complete contract reports Ready.
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
