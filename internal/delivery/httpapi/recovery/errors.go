package recovery

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// writeRetryBlockedActivationError classifies a non-nil error from
// RetryBlockedActivationHandler.Retry — enumerated by reading
// retry_blocked_activation.go's own source (plus the two real-I/O
// dependencies it calls into, admission.go and internal/app/agentregistry),
// not guessed. AlreadyRetried and a still-failing FailureReason are NOT
// errors at all (retry.go's own doc comment) — this function only ever sees
// the genuine failure cases.
func writeRetryBlockedActivationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		// loadForRetryTx's own tx.Runtime().GetNodeRun/GetWorkflowRun calls,
		// unwrapped — a genuinely unknown NodeRunID (or, defensively, its
		// owning WorkflowRun somehow missing).
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, runtime.ErrNodeRunNotBlocked):
		// The NodeRun exists but is not currently BLOCKED (already retried
		// and moved on, never blocked at all, or blocked with no matching
		// admission-blocker row) — a real state mismatch the caller should
		// reload before deciding what to do next, never a 404 (the NodeRun
		// itself is real).
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the node run is not currently blocked by an admission check", nil)
	case errors.Is(err, runtime.ErrNotAnAdmissionBlockerReason):
		// BLOCKED, but for a reason this command has no authority over
		// (most notably SCOPE_EXPANSION_REQUIRED — see
		// retry_blocked_activation.go's own package doc comment for why
		// that has its own dedicated resolution path instead).
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the node run's own blocked reason is not an admission check this command can retry", nil)
	case errors.Is(err, runtime.ErrRunNotRetryable):
		// The owning WorkflowRun already left RUNNING/WAITING (CANCELLING/
		// CANCELLED included) — nothing left for a fresh activation to
		// route into.
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the node run's own workflow run is not in a retryable state (must be RUNNING or WAITING)", nil)
	case errors.Is(err, agentregistry.ErrUnknownProvider), errors.Is(err, agentregistry.ErrMissingCapability):
		// This server's own composition root has no real, live
		// ports.AgentExecutor registered (or one lacking a required
		// capability) for the pinned AdapterBuildVersion's own ProviderKey
		// — cmd/aw/serve.go's own --claude-executable/--codex-executable
		// flags are how an operator opts a provider in; see this package's
		// own doc comment and baocaov6checklist.md's V6-06D section for the
		// full reasoning. A genuine deployment/configuration condition, not
		// a client mistake — 503, not 409/500, so a caller/operator can
		// tell "retry once the server is restarted with that provider
		// configured" apart from either a business conflict or a bug.
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable,
			"this server has no real agent provider configured for the pinned adapter build's provider", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}

// writeCancelWorkItemError classifies a non-nil error from
// runtime.CancelWorkItem — enumerated by reading cancel_work_item.go's own
// source. AlreadyRequested is NOT an error (cancelworkitem.go's own doc
// comment) — this function only ever sees the genuine failure cases.
func writeCancelWorkItemError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		// tx.Work().GetWorkItem's own not-found, unwrapped — a genuinely
		// unknown WorkItemID.
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, runtime.ErrWorkItemAlreadyTerminal):
		// ADR-020's own "PASS trước → no-op idempotent" — a genuinely
		// terminal (DONE/CANCELLED) WorkItem refuses cancellation outright,
		// never conflated with the AlreadyRequested no-op case, which is
		// not an error.
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the work item has already reached a terminal status and cannot be cancelled", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}

// writeResolveWorkItemBlockerError classifies a non-nil error from
// runtime.ResolveWorkItemBlocker — enumerated by reading
// resolve_work_item_blocker.go's own source, including its own package doc
// comment's closed BlockerType × resolution-mode table. AlreadyResolved is
// NOT an error (resolveblocker.go's own doc comment) — this function only
// ever sees the genuine failure cases.
func writeResolveWorkItemBlockerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		// tx.Work().GetWorkItemBlocker's own not-found, unwrapped — a
		// genuinely unknown BlockerID.
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, runtime.ErrResolutionModeRequired):
		// Mode is neither "RESOLVED" nor "WAIVED" — a request-shape
		// problem, not a state conflict.
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest,
			"mode must be RESOLVED or WAIVED", nil)
	case errors.Is(err, runtime.ErrWaiveRequiresPolicyGrant):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest,
			"policyGrantRef is required when mode is WAIVED", nil)
	case errors.Is(err, runtime.ErrBlockerNotResolvableViaCommand):
		// SCOPE_EXPANSION_REQUIRED — this command has no authority over
		// this blocker type at all, in either mode (only the real
		// approval/reconcile flow may resolve one).
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"this blocker type can only be resolved through its own owning flow, not this command", nil)
	case errors.Is(err, runtime.ErrBlockerNotWaivable):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"this blocker type can never be waived", nil)
	case errors.Is(err, runtime.ErrWorkItemHasNonTerminalRun):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the work item still has a non-terminal workflow run", nil)
	case errors.Is(err, runtime.ErrWorkspaceQuarantined):
		// Routed through the shared table (like
		// internal/delivery/httpapi/workitem's own writePreconditionFailed)
		// rather than a hardcoded status/code pair, so this stays
		// byte-for-byte consistent with errorcode.CodeWorkspaceQuarantined's
		// own single mapping (423 Locked, ErrorCodeConflict) if that table
		// is ever revisited.
		status, code := httpapi.StatusForAppErrorCode(errorcode.CodeWorkspaceQuarantined)
		httpapi.WriteError(w, status, code, "the work item's family has a quarantined repository workspace", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}
