// This file is V6-10B's own composition and shared plumbing
// (docs/design/08-v6-api-projections.md V6-10B): "expose WorkspaceSet/
// repository-workspace state, lease/fence/quarantine and request actions".
// RegisterWorkspaceRoutes is the one function a composition root calls
// (mirroring V6-01's own "a later endpoint task adds its own routes.Register
// call" invitation in cmd/aw/serve.go) — it never edits route.go itself,
// only composes RouteDescriptor values from this package's own primitives
// plus workspacestate/workspacerelease/workspacereconcile's public
// application queries/commands.
//
// Every handler in workspacestate.go/workspacerelease.go/workspacereconcile.go
// follows V6-02's own documented mutating-route flow exactly (see
// commandenvelope.go/receiptreplay.go's own doc comments): authenticate via
// PrincipalFromContext (already bound by the server's own middleware chain,
// never re-derived here) -> RequireIdempotencyKey/RequireIfMatch ->
// CanonicalizeJSON -> SemanticHash -> LookupReceipt -> replay via
// WriteReceiptReplay, or ErrReceiptHashConflict, or proceed -> dispatch the
// real workspacerelease/workspacereconcile application command -> EncodeResult.
// This package still never writes or records a receipt itself
// (internal/archtest's own TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt
// already enforces that for the whole package, this file included).
//
// This task's own Không làm line — "no internal Execute command,
// Git/filesystem direct call or writer grant from HTTP" — is enforced for
// real, not just by convention, by internal/archtest's own
// TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor: no file in this
// package may import "os", "os/exec" or any internal/adapters/... package,
// or call ExecuteWorkspaceReconciliation/ExecuteWorkspaceSetRelease (the
// internal job-handler executors — V5-14's own authority, never this one's)
// or any ports.WorkspaceLifecycle/ports.WriteLeaseManager mutating method
// (QuarantineRepositoryWorkspace, ReleaseRepositoryWorkspace,
// RecreateRepositoryWorkspace, AcquireWriteLeases). The one read this package
// performs against write-lease state, HasActiveWriteLease, is a plain
// existence check already real and already tested by
// workspacerelease.RequestWorkspaceSetRelease's own eligibility gate — never
// a lease grant.
package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacestate"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// maxWorkspaceCommandBodyBytes bounds the request body DecodeJSON/
// CanonicalizeJSON accept for every route in this file — every one of them
// expects at most an empty JSON object (`{}`): FamilyID/ProjectID/
// RepositoryWorkspaceID all come from the URL path, and ExpectedVersion
// comes from the required If-Match header (commandenvelope.go's own
// VersionFromETag), never a request body field, so there is no legitimate
// payload this limit could ever need to accommodate — it exists only so a
// caller that sends something other than "{}" gets a clean, typed 400
// (WriteDecodeError) instead of an unbounded read.
const maxWorkspaceCommandBodyBytes = 1 << 16

// RegisterWorkspaceRoutes registers this task's own 4 routes into routes: 2
// read-only state queries and 2 write-intent dispatches. A composition root
// calls this once at startup, passing the same uow/ids every other route
// registration in that same process uses (cmd/aw/serve.go's own
// idsource.Random{} — never a fresh source per request, mirroring this
// package's own bootstrap/health routes).
//
// authority is constructed here, once, from uow — work.NewEligibilityAuthority
// is the real ports.ReleaseEligibilityAuthority implementation
// (internal/app/work/release_set.go), backed by a family's own real, most
// recently created ReleaseSet; there is no fake anywhere on this path.
func RegisterWorkspaceRoutes(routes *RouteRegistry, uow ports.UnitOfWork, ids idsource.Source) {
	authority := work.NewEligibilityAuthority(uow)

	routes.Register(RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/workspace-sets/{familyId}",
		OperationID: "getWorkspaceSetState", ScopeKind: ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: workspaceSetStateResponse{},
		Handler: getWorkspaceSetStateHandler(uow),
	})
	routes.Register(RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}",
		OperationID: "getRepositoryWorkspaceState", ScopeKind: ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: repositoryWorkspaceStateResponse{},
		Handler: getRepositoryWorkspaceStateHandler(uow),
	})
	routes.Register(RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/workspace-sets/{familyId}/release",
		OperationID: "requestWorkspaceSetRelease", ScopeKind: ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: workspacerelease.RequestWorkspaceSetReleaseResult{},
		Handler: requestWorkspaceSetReleaseHandler(uow, ids, authority),
	})
	routes.Register(RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/reconcile",
		OperationID: "requestWorkspaceReconciliation", ScopeKind: ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: workspacereconcile.RequestWorkspaceReconciliationResult{},
		Handler: requestWorkspaceReconciliationHandler(uow, ids),
	})
}

// newWorkspaceCommand builds the ports.Command envelope every handler in
// this file dispatches — mirrors cmd/aw/definition.go's own
// newDefinitionCommand exactly: ID/CorrelationID derived from the command
// type + idempotency key (stable and reproducible across a retry, never a
// fresh random value that would defeat log correlation across retried
// attempts). Actor/ActorRoles come from principal, the trusted
// LocalPrincipalSnapshot BindPrincipal already bound to this request's own
// context — never read from the request body or a header.
func newWorkspaceCommand(commandType, idempotencyKey string, principal LocalPrincipalSnapshot, scope ports.CommandScope, expectedVersion uint64, hash string) ports.Command {
	id := commandType + "-" + idempotencyKey
	return ports.Command{
		ID: id, IdempotencyKey: idempotencyKey, Actor: principal.Actor, ActorRoles: principal.Roles,
		CorrelationID: id, Scope: scope, ExpectedVersion: expectedVersion,
		RequestedAt: time.Now().UTC(), Type: commandType, RequestHash: hash,
	}
}

// writeWorkspaceCommandError maps every error
// workspacerelease.RequestWorkspaceSetRelease/
// workspacereconcile.RequestWorkspaceReconciliation/workspacestate's own two
// queries can return into the canonical HTTP envelope (errors.go). Not-found
// and cross-project sentinels are leakage-normalized identically
// (WriteResourceHidden — V6-10C's own established "no path leak" policy,
// applied here to the same underlying concern for a different pair of
// aggregates); every other real, named sentinel is wrapped as a typed
// *apperror.Error and handed to the existing WriteAppError/
// StatusForAppErrorCode table (errors.go) rather than this function hand-
// rolling a second, parallel status-mapping switch.
func writeWorkspaceCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound),
		errors.Is(err, ports.ErrCrossProjectReference),
		errors.Is(err, workspacestate.ErrScopeMismatch):
		WriteResourceHidden(w)

	case errors.Is(err, ports.ErrOptimisticConflict):
		WriteAppError(w, apperror.Wrap(errorcode.CodePreconditionFailed,
			"the workspace has moved past the version named by If-Match; GET the current state and retry", false, err))

	case errors.Is(err, ports.ErrReceiptConflict):
		WriteAppError(w, apperror.Wrap(errorcode.CodeIdempotencyConflict,
			"Idempotency-Key was already used for a request with a different body", false, err))

	case errors.Is(err, workspacerelease.ErrWorkspaceSetHasQuarantinedRepository):
		WriteAppError(w, apperror.Wrap(errorcode.CodeWorkspaceQuarantined,
			"the workspace set has a quarantined repository workspace and cannot be released", false, err))

	case errors.Is(err, workspacerelease.ErrWorkspaceSetHasActiveWriteLease):
		WriteAppError(w, apperror.Wrap(errorcode.CodeConflict,
			"the workspace set has a repository workspace with an active writer and cannot be released", true, err))

	case errors.Is(err, workspacerelease.ErrWorkspaceSetHasActiveJob):
		WriteAppError(w, apperror.Wrap(errorcode.CodeConflict,
			"the workspace set already has an active operation in progress", true, err))

	case errors.Is(err, workspacerelease.ErrWorkspaceSetAlreadyReleased):
		WriteAppError(w, apperror.Wrap(errorcode.CodeConflict,
			"the workspace set is already released", false, err))

	case errors.Is(err, workspacerelease.ErrReleaseNotAuthorized):
		WriteAppError(w, apperror.Wrap(errorcode.CodePolicyDenied,
			"release is not authorized until this family's release set is sealed or abandoned", false, err))

	case errors.Is(err, workspacereconcile.ErrWorkspaceNotReconcilable):
		WriteAppError(w, apperror.Wrap(errorcode.CodeConflict,
			"the repository workspace is not currently in a reconcilable state (must be READY or QUARANTINED)", false, err))

	default:
		WriteAppError(w, err)
	}
}
