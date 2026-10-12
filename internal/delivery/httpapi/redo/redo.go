// Package redo is V11-10's HTTP surface (ADR-041) for running a failed task again and redoing it with the newest workflow version in one
// action each: a thin dispatch layer over internal/app/workitemredo (the preview, and the commands with their worktree handling).
//
// retryWorkItem and recreateWorkItem follow the CommandEnvelope flow: Idempotency-Key required, canonical semantic hash, and the
// command itself replays a first result or reports a conflict inside its transaction, so a double click or a retried request after a
// lost response never starts a second run or creates a second WorkItem. The project is derived from the WorkItem's own stored row
// (the routes carry no {projectId} segment), never from the request.
package redo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workitemredo"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// maxBodyBytes bounds a redo request body: a note and a reason, nothing else.
const maxBodyBytes = 1 << 16

// Dependencies is what the handlers need from the composition root.
type Dependencies struct {
	UOW     ports.UnitOfWork
	Service workitemredo.Service
	// HeadAuditor (V11-01) lets a command refuse early, with an explanation, when a worktree's HEAD is not the revision aw recorded.
	// Nil skips that preflight.
	HeadAuditor ports.WorkspaceHeadAuditor
}

// RetryRequest is the JSON body of POST /work-items/{workItemId}/retry.
type RetryRequest struct {
	// Note is an optional message for the agent, sent before the new run starts.
	Note string `json:"note,omitempty"`
	// OutOfScope is KEEP or PARK: required when a previous run left files outside the work item's scope (the preview lists them).
	OutOfScope string `json:"outOfScope,omitempty"`
}

// RetryResponse is the result of a retry (fresh or replayed).
type RetryResponse struct {
	runtime.RetryWorkItemResult
}

// RecreateRequest is the JSON body of POST /work-items/{workItemId}/recreate.
type RecreateRequest struct {
	Reason string `json:"reason"`
	Note   string `json:"note,omitempty"`
	// CarryMessages copies the operator's messages to the new work item. Omitted means true.
	CarryMessages *bool  `json:"carryMessages,omitempty"`
	OutOfScope    string `json:"outOfScope,omitempty"`
}

// RecreateResponse is the result of a redo (fresh or replayed).
type RecreateResponse struct {
	runtime.RecreateWorkItemResult
}

// PreviewResponse is GET /work-items/{workItemId}/redo-preview's body.
type PreviewResponse struct {
	workitemredo.Preview
}

// RegisterRoutes registers the three routes.
func RegisterRoutes(routes *httpapi.RouteRegistry, deps Dependencies) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/work-items/{workItemId}/redo-preview", OperationID: "getWorkItemRedoPreview",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: PreviewResponse{},
		Handler: previewHandler(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/work-items/{workItemId}/retry", OperationID: "retryWorkItem",
		ScopeKind: httpapi.ScopeProject, RequestSchema: RetryRequest{}, ResponseSchema: RetryResponse{},
		Handler: retryHandler(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/work-items/{workItemId}/recreate", OperationID: "recreateWorkItem",
		ScopeKind: httpapi.ScopeProject, RequestSchema: RecreateRequest{}, ResponseSchema: RecreateResponse{},
		Handler: recreateHandler(deps),
	})
}

func previewHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workItemID := r.PathValue("workItemId")
		projectID, ok := resolveProject(w, r.Context(), deps, workItemID)
		if !ok {
			return
		}
		preview, err := deps.Service.Preview(r.Context(), projectID, workItemID)
		if err != nil {
			writeRedoError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, PreviewResponse{Preview: preview}, "")
	}
}

type retryHash struct {
	WorkItemID string `json:"workItemId"`
	Note       string `json:"note"`
	OutOfScope string `json:"outOfScope"`
}

func retryHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		workItemID := r.PathValue("workItemId")
		projectID, ok := resolveProject(w, ctx, deps, workItemID)
		if !ok {
			return
		}
		var body RetryRequest
		cmd, ok := prepareCommand(w, r, projectID, "RetryWorkItem", &body, func() any {
			return retryHash{WorkItemID: workItemID, Note: body.Note, OutOfScope: body.OutOfScope}
		})
		if !ok {
			return
		}
		if err := workspacereconcile.CheckFamilyWorkspaceHeads(ctx, deps.UOW, deps.HeadAuditor, workItemID); err != nil {
			writeRedoError(w, err)
			return
		}
		result, err := deps.Service.Retry(ctx, cmd, workitemredo.RetryRequest{
			ProjectID: projectID, WorkItemID: workItemID, Note: body.Note, OutOfScope: body.OutOfScope,
		})
		if err != nil {
			writeRedoError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusCreated, RetryResponse{RetryWorkItemResult: result}, "")
	}
}

type recreateHash struct {
	WorkItemID    string `json:"workItemId"`
	Reason        string `json:"reason"`
	Note          string `json:"note"`
	CarryMessages bool   `json:"carryMessages"`
	OutOfScope    string `json:"outOfScope"`
}

func recreateHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		workItemID := r.PathValue("workItemId")
		projectID, ok := resolveProject(w, ctx, deps, workItemID)
		if !ok {
			return
		}
		var body RecreateRequest
		carry := func() bool { return body.CarryMessages == nil || *body.CarryMessages }
		cmd, ok := prepareCommand(w, r, projectID, "RecreateWorkItem", &body, func() any {
			return recreateHash{WorkItemID: workItemID, Reason: body.Reason, Note: body.Note, CarryMessages: carry(), OutOfScope: body.OutOfScope}
		})
		if !ok {
			return
		}
		if strings.TrimSpace(body.Reason) == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "reason is required",
				[]httpapi.ErrorDetail{{Field: "reason", Message: "is required"}})
			return
		}
		if err := workspacereconcile.CheckFamilyWorkspaceHeads(ctx, deps.UOW, deps.HeadAuditor, workItemID); err != nil {
			writeRedoError(w, err)
			return
		}
		result, err := deps.Service.Recreate(ctx, cmd, workitemredo.RecreateRequest{
			ProjectID: projectID, WorkItemID: workItemID, Reason: body.Reason, Note: body.Note,
			CarryMessages: carry(), OutOfScope: body.OutOfScope,
		})
		if err != nil {
			writeRedoError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusCreated, RecreateResponse{RecreateWorkItemResult: result}, "")
	}
}

// prepareCommand requires the Idempotency-Key, decodes the body into dst and builds the command whose request hash covers hashed().
// It writes the error response and returns false when any of that fails.
func prepareCommand(w http.ResponseWriter, r *http.Request, projectID, commandType string, dst any, hashed func() any) (ports.Command, bool) {
	ctx := r.Context()
	idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, false
	}
	if err := httpapi.DecodeJSON(r, maxBodyBytes, dst); err != nil {
		httpapi.WriteDecodeError(w, err)
		return ports.Command{}, false
	}
	payload, err := json.Marshal(hashed())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
		return ports.Command{}, false
	}
	scope := ports.ProjectScope(projectID)
	principal := httpapi.PrincipalFromContext(ctx)
	return ports.Command{
		ID: commandType + "-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: principal.Actor, ActorRoles: principal.Roles,
		CorrelationID: httpapi.CorrelationIDFromContext(ctx), Scope: scope, RequestedAt: time.Now().UTC(), Type: commandType,
		RequestHash: httpapi.SemanticHash(commandType, scope, payload, "", 0),
	}, true
}

// resolveProject reloads the WorkItem read-only to learn its project (contract point 3: the stored row is the only source). It writes
// the leakage-normalized not-found response and returns false when the WorkItem does not exist.
func resolveProject(w http.ResponseWriter, ctx context.Context, deps Dependencies, workItemID string) (string, bool) {
	var projectID string
	err := deps.UOW.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		projectID = string(item.ProjectID)
		return nil
	})
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		httpapi.WriteResourceHidden(w)
		return "", false
	case err != nil:
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
		return "", false
	}
	return projectID, true
}

// writeRedoError classifies an error from the preview or either command.
func writeRedoError(w http.ResponseWriter, err error) {
	var needsDecision *workitemredo.OutOfScopeDecisionError
	var notReady *workdomain.ReadinessError
	switch {
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key already used with a different request", nil)
	case errors.Is(err, ports.ErrPersistenceNotFound), errors.Is(err, ports.ErrScopeMismatch):
		httpapi.WriteResourceHidden(w)
	case errors.As(err, &needsDecision):
		details := []httpapi.ErrorDetail{{Field: "outOfScope", Message: "OUT_OF_SCOPE_FILES_NEED_DECISION: choose KEEP or PARK"}}
		for _, f := range needsDecision.Files {
			details = append(details, httpapi.ErrorDetail{Field: "files", Message: f.RepositoryID + ":" + f.Path})
		}
		httpapi.WriteError(w, http.StatusUnprocessableEntity, httpapi.ErrorCodeInvalidRequest, err.Error(), details)
	case errors.Is(err, workitemredo.ErrInvalidDisposition):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, httpapi.ErrorCodeInvalidRequest, err.Error(),
			[]httpapi.ErrorDetail{{Field: "outOfScope", Message: "must be KEEP or PARK"}})
	case errors.Is(err, workitemredo.ErrParkUnavailable):
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable, err.Error(), nil)
	case errors.Is(err, workitemredo.ErrWorktreeNotReadable):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case runtime.RedoRefusalCode(err) != "":
		// A typed refusal: nothing was changed. The code is in the details so a client can explain it in words.
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(),
			[]httpapi.ErrorDetail{{Field: "redo", Message: runtime.RedoRefusalCode(err)}})
	case errors.Is(err, runtime.ErrWorkItemNotReady), errors.Is(err, runtime.ErrWorkspaceNotReady), errors.Is(err, runtime.ErrBaselineNotAdmitted),
		errors.Is(err, runtime.ErrWorkItemCancellationPending), errors.Is(err, runtime.ErrWorkflowVersionMismatch),
		errors.Is(err, workspacereconcile.ErrWorkspaceHeadDrift), errors.Is(err, work.ErrEffectiveScopeExceedsFamilyScope),
		errors.Is(err, work.ErrWorkItemNotEligibleForReady):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.As(err, &notReady):
		// the replacement work item failed the readiness gate (for example a baseline that no longer passes)
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}
