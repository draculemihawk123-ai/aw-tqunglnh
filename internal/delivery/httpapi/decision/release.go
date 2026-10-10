package decision

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/approvalrelease"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// This file is the read side and the restore command of V11-07's approve-and-commit (ADR-040): what the
// approval dialog needs before it offers "Commit" (who the author would be, what is parked), how a decision's
// commits went, and putting parked files back. All of it is a thin dispatch over internal/app/approvalrelease.

// maxRestoreParkedBodyBytes bounds RestoreParkedChangesBody, one short id.
const maxRestoreParkedBodyBytes = 1 << 12

// RestoreParkedChangesBody names the commit operation whose parked files come back.
type RestoreParkedChangesBody struct {
	ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
}

// GetApprovalReleaseHandler is GET /runs/{runId}/approval-requests/{approvalRequestId}/release
// (operationId getApprovalRelease): the commits a decision requested, with how each went and the patch the
// approver reviewed.
func GetApprovalReleaseHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := deps.queries().ApprovalRelease(r.Context(), r.PathValue("runId"), r.PathValue("approvalRequestId"))
		if err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				httpapi.WriteResourceHidden(w)
				return
			}
			httpapi.WriteAppError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
	}
}

// GetFamilyReleaseContextHandler is GET /projects/{projectId}/task-families/{familyId}/release-context
// (operationId getFamilyReleaseContext): per repository, the git author a commit would carry (or why there is
// none), plus the parked sets not restored yet.
func GetFamilyReleaseContextHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := deps.queries().FamilyReleaseContext(r.Context(), r.PathValue("projectId"), r.PathValue("familyId"))
		if err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrCrossProjectReference) {
				httpapi.WriteResourceHidden(w)
				return
			}
			httpapi.WriteAppError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
	}
}

// RestoreParkedChangesHandler is POST /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/restore-parked
// (operationId restoreParkedChanges): put the files a PARK commit moved out of the worktree back. It needs an
// Idempotency-Key and no If-Match: it changes the worktree, not the workspace row.
func RestoreParkedChangesHandler(deps Dependencies) http.HandlerFunc {
	const commandType = "RestoreParkedChanges"
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := strings.TrimSpace(r.PathValue("projectId"))
		repositoryWorkspaceID := strings.TrimSpace(r.PathValue("repositoryWorkspaceId"))
		idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
			return
		}
		var body RestoreParkedChangesBody
		canonical, err := httpapi.CanonicalizeJSON(r, maxRestoreParkedBodyBytes, &body)
		if err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		principal := httpapi.PrincipalFromContext(r.Context())
		scope := ports.ProjectScope(projectID)
		hash := httpapi.SemanticHash(commandType, scope, append([]byte(repositoryWorkspaceID+"\x00"), canonical...), "", 0)

		receipt, found, lookupErr := httpapi.LookupReceipt(r.Context(), deps.UOW, principal.Actor, scope, idempotencyKey, commandType)
		if lookupErr != nil {
			httpapi.WriteAppError(w, lookupErr)
			return
		}
		if found {
			if reconcileErr := httpapi.ReconcileReceipt(receipt, hash); reconcileErr != nil {
				httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "Idempotency-Key was already used for a request with a different body", nil)
				return
			}
			httpapi.WriteReceiptReplay(w, receipt)
			return
		}
		cmd := ports.Command{
			ID: commandType + "-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: principal.Actor, ActorRoles: principal.Roles,
			CorrelationID: httpapi.CorrelationIDFromContext(r.Context()), Scope: scope,
			RequestedAt: time.Now().UTC(), Type: commandType, RequestHash: hash,
		}
		result, err := approvalrelease.RestoreParked(r.Context(), approvalrelease.RestoreDeps{UOW: deps.UOW, Parker: deps.Release.Parker}, cmd,
			approvalrelease.RestoreRequest{ProjectID: projectID, RepositoryWorkspaceID: repositoryWorkspaceID, ReleaseSetLocalCommitID: body.ReleaseSetLocalCommitID})
		if err != nil {
			writeRestoreError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
	}
}

func writeRestoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound), errors.Is(err, ports.ErrCrossProjectReference):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, approvalrelease.ErrNoParker):
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable,
			"this server has no Git workspace provider, so it cannot restore parked changes", nil)
	case errors.Is(err, approvalrelease.ErrOperationRequired):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "releaseSetLocalCommitId is required", nil)
	case errors.Is(err, approvalrelease.ErrNothingToRestore):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "nothing is parked for this commit operation, or it was already restored", nil)
	case errors.Is(err, approvalrelease.ErrWorkspaceBusy):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"an attempt is writing to this repository workspace; wait for it to finish or cancel it", nil)
	case errors.Is(err, ports.ErrParkedRestoreConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"restoring would overwrite newer changes in the worktree; commit or discard them first", nil)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key already used with a different request", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}
