package workitem

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// requestScopeExpansionBody is
// POST /projects/{projectId}/task-families/{familyId}/scope-expansions' own
// wire request shape — deliberately without familyId (the path's own
// {familyId}) or requestId: work.RequestScopeExpansionRequest's own doc
// comment is explicit that "A public/UI-facing caller must never be allowed
// to choose its own RequestID", so this DTO simply has no field for one —
// the application command always mints it itself for an HTTP-originated
// call.
type requestScopeExpansionBody struct {
	RequestedGrants      []scopeGrantBody `json:"requestedGrants"`
	Reason               string           `json:"reason"`
	ReferencedWorkItemID string           `json:"referencedWorkItemId,omitempty"`
}

// handleRequestScopeExpansion implements
// POST /projects/{projectId}/task-families/{familyId}/scope-expansions
// (operationId requestScopeExpansion): dispatches internal/app/work.
// RequestScopeExpansion. The family named by {familyId} is reloaded via
// workapp.GetTaskFamily FIRST — the same "reload the route's real target
// before Idempotency-Key/body are even read" discipline every mutating
// handler in this package follows.
func handleRequestScopeExpansion(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		familyID := r.PathValue("familyId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(familyID) == "" {
			writeValidationError(w, "familyId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		if _, err := workapp.GetTaskFamily(r.Context(), deps.UnitOfWork, scope, familyID); err != nil {
			writeQueryError(w, err)
			return
		}

		var body requestScopeExpansionBody
		cmd, ok := prepareCreateCommand(w, r, deps, "RequestScopeExpansion", scope, &body)
		if !ok {
			return
		}
		if strings.TrimSpace(body.Reason) == "" {
			writeValidationError(w, "reason", "is required")
			return
		}
		if !validateScopeGrantBodies(w, "requestedGrants", body.RequestedGrants) {
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}

		result, err := workapp.RequestScopeExpansion(r.Context(), deps.UnitOfWork, deps.IDs, cmd, workapp.RequestScopeExpansionRequest{
			FamilyID: familyID, RequestedGrants: toScopeGrantRequests(body.RequestedGrants),
			Reason: body.Reason, ReferencedWorkItemID: body.ReferencedWorkItemID,
			// RequestID deliberately left blank — see this file's own
			// requestScopeExpansionBody doc comment.
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		// A freshly created ScopeExpansionRequest always starts at Version 1
		// (work.NewScopeExpansionRequest hardcodes it).
		_ = httpapi.EncodeResult(w, http.StatusCreated, result, httpapi.ETagFromVersion(1))
	}
}

// emptyBody is the wire shape of a mutation with no payload beyond the
// path/headers themselves (Approve/WithdrawScopeExpansion) — the caller
// still sends a literal `{}` JSON object body, kept uniform with every other
// mutating route's own httpapi.CanonicalizeJSON decode step rather than a
// special-cased bodyless branch.
type emptyBody struct{}

// loadScopeExpansionRequestForUpdate reloads requestID via workapp.
// GetScopeExpansionRequest — which already enforces "must belong to scope's
// own project", folding both "not found" and "wrong project" into
// ports.ErrScopeMismatch/ErrPersistenceNotFound alike (queries.go's own
// leakage-normalized scopeMismatch). Every one of Approve/Reject/
// WithdrawScopeExpansion's own handlers calls this exactly once, reusing the
// same loaded detail both to authorize (this call) and, further down, to
// check the caller's own If-Match precondition — never a second reload.
func loadScopeExpansionRequestForUpdate(w http.ResponseWriter, r *http.Request, deps Dependencies, scope ports.CommandScope, requestID string) (workapp.ScopeExpansionRequestDetail, bool) {
	detail, err := workapp.GetScopeExpansionRequest(r.Context(), deps.UnitOfWork, scope, requestID)
	if err != nil {
		writeQueryError(w, err)
		return workapp.ScopeExpansionRequestDetail{}, false
	}
	return detail, true
}

// handleApproveScopeExpansion implements
// POST /projects/{projectId}/scope-expansions/{requestId}/approve
// (operationId approveScopeExpansion): dispatches internal/app/work.
// ApproveScopeExpansion.
func handleApproveScopeExpansion(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		requestID := r.PathValue("requestId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(requestID) == "" {
			writeValidationError(w, "requestId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		detail, ok := loadScopeExpansionRequestForUpdate(w, r, deps, scope, requestID)
		if !ok {
			return
		}

		var body emptyBody
		cmd, expectedVersion, ok := prepareUpdateCommand(w, r, deps, "ApproveScopeExpansion", scope, &body)
		if !ok {
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}
		// "nếu absent mới kiểm current version" (V6-02's own flow) — checked
		// here, AFTER the replay decision, against the target already
		// reloaded above.
		if detail.Version != expectedVersion {
			writePreconditionFailed(w, "If-Match does not match the current version; reload and retry")
			return
		}

		result, err := workapp.ApproveScopeExpansion(r.Context(), deps.UnitOfWork, deps.IDs, cmd, workapp.ApproveScopeExpansionRequest{
			RequestID: requestID,
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
	}
}

// rejectScopeExpansionBody is
// POST /projects/{projectId}/scope-expansions/{requestId}/reject's own wire
// request shape.
type rejectScopeExpansionBody struct {
	DecisionNote string `json:"decisionNote"`
}

// handleRejectScopeExpansion implements
// POST /projects/{projectId}/scope-expansions/{requestId}/reject
// (operationId rejectScopeExpansion): dispatches internal/app/work.
// RejectScopeExpansion.
func handleRejectScopeExpansion(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		requestID := r.PathValue("requestId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(requestID) == "" {
			writeValidationError(w, "requestId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		detail, ok := loadScopeExpansionRequestForUpdate(w, r, deps, scope, requestID)
		if !ok {
			return
		}

		var body rejectScopeExpansionBody
		cmd, expectedVersion, ok := prepareUpdateCommand(w, r, deps, "RejectScopeExpansion", scope, &body)
		if !ok {
			return
		}
		if strings.TrimSpace(body.DecisionNote) == "" {
			writeValidationError(w, "decisionNote", "is required")
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}
		if detail.Version != expectedVersion {
			writePreconditionFailed(w, "If-Match does not match the current version; reload and retry")
			return
		}

		result, err := workapp.RejectScopeExpansion(r.Context(), deps.UnitOfWork, cmd, workapp.RejectScopeExpansionRequest{
			RequestID: requestID, DecisionNote: body.DecisionNote,
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
	}
}

// handleWithdrawScopeExpansion implements
// POST /projects/{projectId}/scope-expansions/{requestId}/withdraw
// (operationId withdrawScopeExpansion): dispatches internal/app/work.
// WithdrawScopeExpansion — this task's own explicit responsibility
// ("WithdrawScopeExpansion thuộc task này, chỉ khi pending và không tạo
// grant/amendment"). The underlying command is itself idempotent at the
// business level (a request already WITHDRAWN returns the same success, not
// an error) — this handler adds no special casing on top of that; the
// version precondition below still applies uniformly like every other
// update, since a caller with a stale If-Match genuinely does not know the
// current state yet.
func handleWithdrawScopeExpansion(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		requestID := r.PathValue("requestId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(requestID) == "" {
			writeValidationError(w, "requestId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		detail, ok := loadScopeExpansionRequestForUpdate(w, r, deps, scope, requestID)
		if !ok {
			return
		}

		var body emptyBody
		cmd, expectedVersion, ok := prepareUpdateCommand(w, r, deps, "WithdrawScopeExpansion", scope, &body)
		if !ok {
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}
		if detail.Version != expectedVersion {
			writePreconditionFailed(w, "If-Match does not match the current version; reload and retry")
			return
		}

		result, err := workapp.WithdrawScopeExpansion(r.Context(), deps.UnitOfWork, cmd, workapp.WithdrawScopeExpansionRequest{
			RequestID: requestID,
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
	}
}
