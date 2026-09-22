package workitem

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// writeQueryError maps a query-side error from internal/app/work/queries.go
// to the shared httpapi envelope. Both "genuinely does not exist"
// (ports.ErrPersistenceNotFound) and "exists but belongs to a project the
// caller's own path scope does not name" (ports.ErrScopeMismatch,
// queries.go's own scopeMismatch helper) fold into the identical
// WriteResourceHidden response — V6-02A's own leakage-normalization policy:
// a guessed cross-project ID and a genuinely nonexistent one must be
// indistinguishable to the caller.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrScopeMismatch) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeCommandError maps every named sentinel the six mutating commands this
// package wraps (internal/app/work/commands.go's CreateRootWorkItem/
// CreateChildWorkItem, scope_expansion.go's RequestScopeExpansion/
// ApproveScopeExpansion/RejectScopeExpansion/WithdrawScopeExpansion) can
// actually return — enumerated by reading every one of those functions' own
// source, not guessed:
//
//   - ports.ErrPersistenceNotFound / ports.ErrScopeMismatch /
//     ports.ErrCrossProjectReference: the named target (a repository inside
//     the request body, in every case this package's own handlers reach
//     these commands with — the route's OWN primary target is always
//     reloaded and scope-checked by the handler itself before ever
//     dispatching, see workitem_commands.go/scope_expansion_commands.go)
//     either does not exist or belongs to a project the caller's path scope
//     does not name — the identical leakage-normalized WriteResourceHidden
//     response the query side already uses.
//   - ports.ErrReceiptConflict: a genuine concurrent race past this
//     package's own pre-dispatch LookupReceipt fast path (two callers raced
//     the identical Idempotency-Key with two different request bodies) —
//     the command's own internal receipt recheck, inside its
//     WithSerializedWrite transaction, is what actually caught it.
//   - ports.ErrOptimisticConflict: ApproveScopeExpansion's own fenced
//     TransitionTaskFamilyScopeVersion CAS lost against state that already
//     moved.
//   - workapp.ErrRepositoryNotActive: a named repository exists in this
//     project but is not currently ACTIVE — a real, visible business-state
//     conflict (the caller already has this project's own access, so this
//     is not a leakage concern the way a cross-project reference is).
//   - workapp.ErrScopeExpansionNotPending: the targeted ScopeExpansionRequest
//     already carries a terminal decision — a real conflict.
//   - workapp.ErrEffectiveScopeExceedsFamilyScope /
//     workapp.ErrCrossFamilyReference: the request, exactly as given, cannot
//     succeed against the family's own current approved scope/membership —
//     a request-shape problem, not a race.
//   - workapp.ErrWorkItemNotEligibleForReady (MarkWorkItemReady, V6-04A):
//     the targeted WorkItem's own current Status is not BACKLOG — including
//     the already-READY "READY conflict" this task's own spec names for a
//     fresh Idempotency-Key arriving after an earlier call already
//     succeeded. A real conflict, mapped the same way
//     workapp.ErrScopeExpansionNotPending already is.
//   - *workdomain.ReadinessError (MarkWorkItemReady, V6-04A): the WorkItem
//     IS BACKLOG but fails workdomain.ValidateReadinessGate — checked and
//     mapped FIRST, before the switch below, so its own Problems list
//     (the SAME vocabulary GetWorkItemReadiness/ExplainWorkItemReadiness
//     already report for the identical WorkItem) rides along as
//     httpapi.ErrorDetail entries rather than being flattened into a single
//     opaque message.
//   - *workapp.InvalidWorkItemContractError (CreateRootWorkItem/
//     CreateChildWorkItem, V6-04B): the request's optional "contract" object
//     has a shape that can never be valid (a negative schema version, a
//     criterion with no description, a blank exclusion or workflow version ID)
//     — mapped to a 400 with one "contract.<field>" ErrorDetail per problem,
//     also checked before the switch. Handlers run the same Validate()
//     themselves before dispatching, so this mapping is the command's own
//     defense in depth reaching the wire consistently, never a second
//     vocabulary.
//   - workapp.ErrUnknownWorkflowVersion (same two commands): "contract.
//     workflowVersionId" names no WorkflowVersion — a request-shape problem
//     the caller can fix, so a 400 with a field detail rather than the 404
//     an unknown repository gets above. The lookup is existence-only, by ID,
//     with no per-project ownership check — the same boundary
//     runtime.StartWorkflowRun's own lookup of that pin already has; this
//     mapping does not move it.
//
// Every unmatched error falls through to a plain 500 INTERNAL. In practice
// this default is defense-in-depth only: the bare `errors.New(...)`
// field-presence guards at the top of each wrapped command are always
// re-validated by this package's own handler before it ever dispatches (see
// each handler's own explicit field checks right after decoding the body),
// so a well-formed request that reaches a real command should never
// actually trigger one of those guards.
func writeCommandError(w http.ResponseWriter, err error) {
	var readinessErr *workdomain.ReadinessError
	if errors.As(err, &readinessErr) {
		details := make([]httpapi.ErrorDetail, 0, len(readinessErr.Problems))
		for _, problem := range readinessErr.Problems {
			details = append(details, httpapi.ErrorDetail{Field: "readiness", Message: problem})
		}
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, readinessErr.Error(), details)
		return
	}
	var contractErr *workapp.InvalidWorkItemContractError
	if errors.As(err, &contractErr) {
		details := make([]httpapi.ErrorDetail, 0, len(contractErr.Problems))
		for _, problem := range contractErr.Problems {
			details = append(details, httpapi.ErrorDetail{Field: "contract." + problem.Field, Message: problem.Message})
		}
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed", details)
		return
	}
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound),
		errors.Is(err, ports.ErrScopeMismatch),
		errors.Is(err, ports.ErrCrossProjectReference):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrReceiptConflict),
		errors.Is(err, ports.ErrOptimisticConflict),
		errors.Is(err, workapp.ErrRepositoryNotActive),
		errors.Is(err, workapp.ErrScopeExpansionNotPending),
		errors.Is(err, workapp.ErrWorkItemNotEligibleForReady):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, workapp.ErrEffectiveScopeExceedsFamilyScope),
		errors.Is(err, workapp.ErrCrossFamilyReference):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, workapp.ErrUnknownWorkflowVersion):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
			[]httpapi.ErrorDetail{{Field: "contract.workflowVersionId", Message: "does not name a published WorkflowVersion"}})
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	}
}

// writeValidationError writes a single field-level 400 — this package's own
// pre-dispatch body validation (done before ever building a command
// envelope, so a malformed request never even computes a semantic hash).
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}

// writeReceiptHashConflict writes the response for httpapi.
// ErrReceiptHashConflict (receiptreplay.go's ReconcileReceipt): the same
// Idempotency-Key was reused with a semantically different request — V6-02's
// own "different body conflict trước I/O", caught here before any real
// command ever dispatches.
func writeReceiptHashConflict(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key reused with a different request", nil)
}

// writePreconditionFailed writes the response for a stale If-Match: the
// caller's own claimed ExpectedVersion no longer matches this package's own
// freshly reloaded authoritative target. Routed through
// httpapi.StatusForAppErrorCode(errorcode.CodePreconditionFailed) rather
// than a hardcoded status/code pair, so this stays byte-for-byte consistent
// with that shared table's own choice (412 Precondition Failed,
// ErrorCodeConflict) if it is ever revisited.
func writePreconditionFailed(w http.ResponseWriter, message string) {
	status, code := httpapi.StatusForAppErrorCode(errorcode.CodePreconditionFailed)
	httpapi.WriteError(w, status, code, message, nil)
}
