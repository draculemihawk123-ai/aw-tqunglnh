package httpapi

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// ErrorCode is httpapi's own closed wire vocabulary for the top-level
// `error.code` field every handler's failure response carries — V6-02A's
// own "Phạm vi: error envelope" line. It is deliberately a much smaller,
// HTTP-shaped set than internal/domain/errorcode.Code's own 22-value
// domain vocabulary: a client branching on HTTP responses needs "is this
// retryable-as-is, a conflict, not found, forbidden, or a hard failure",
// not the full internal detail of which of the 22 domain conditions
// produced it — that finer detail is exactly what the HTTP status code
// (via StatusForAppErrorCode) and the human Message already carry.
type ErrorCode string

const (
	// ErrorCodeInvalidRequest: the request itself (body, query parameter,
	// path, header, cursor) is malformed or fails validation; retrying the
	// identical request can never succeed.
	ErrorCodeInvalidRequest ErrorCode = "INVALID_REQUEST"
	// ErrorCodeNotFound: the referenced resource does not exist — or, per
	// this task's own leakage-normalization policy (see WriteResourceHidden
	// below), exists but the caller's scope must not be told so.
	ErrorCodeNotFound ErrorCode = "NOT_FOUND"
	// ErrorCodeForbidden: the caller is authenticated but not permitted to
	// perform THIS operation on a resource whose existence is not itself
	// sensitive (contrast ErrorCodeNotFound's leakage-normalized case).
	ErrorCodeForbidden ErrorCode = "FORBIDDEN"
	// ErrorCodeConflict: an optimistic-concurrency/idempotency/fencing
	// check lost against state that has genuinely already changed; the
	// caller must reload before retrying.
	ErrorCodeConflict ErrorCode = "CONFLICT"
	// ErrorCodeResyncRequired: a paging/stream cursor decoded fine but no
	// longer applies to the caller's current project/query/generation
	// (cursor.go's ResyncError) — the caller must restart from a fresh,
	// cursor-less request, not merely retry.
	ErrorCodeResyncRequired ErrorCode = "RESYNC_REQUIRED"
	// ErrorCodeUnavailable: a transient condition; a bounded retry of the
	// exact same request may succeed on its own.
	ErrorCodeUnavailable ErrorCode = "UNAVAILABLE"
	// ErrorCodeInternal: an unexpected failure with no more specific
	// category.
	ErrorCodeInternal ErrorCode = "INTERNAL"
)

// ErrorDetail is one field-level explanation an ErrorBody may attach —
// e.g. which request field failed validation and why. Field is empty for
// a detail that is not about one specific field (e.g. cursor.go's own
// resync reason, attached as {Field: "cursor", Message: <reason>}).
type ErrorDetail struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ErrorBody is the typed error payload every failing handler response
// carries.
type ErrorBody struct {
	Code    ErrorCode     `json:"code"`
	Message string        `json:"message"`
	Details []ErrorDetail `json:"details,omitempty"`
}

// ErrorResponse is the canonical top-level JSON shape every httpapi
// handler returns on failure — V6-02A's own "Phạm vi: error envelope"
// line, frozen once here so no endpoint task invents its own shape
// (V6-02A's own "Hoàn thành khi": "endpoint task không phải tự quyết
// DTO/... error/... convention").
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// WriteError writes status and the canonical ErrorResponse envelope for
// code/message/details — the one function every other WriteXxx helper in
// this file (and every future endpoint handler) funnels through, so the
// wire shape can never accidentally drift between call sites.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string, details []ErrorDetail) {
	writeJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: message, Details: details}})
}

// ErrResourceHidden is the sentinel a query/lookup handler wraps (or
// compares against with errors.Is) when a resource either does not exist,
// or exists but is outside the caller's authorized scope. It exists so a
// "not found" code path and an "unauthorized for this scope" code path can
// share the exact same handling — see WriteResourceHidden.
var ErrResourceHidden = errors.New("httpapi: resource not found or not visible in caller's scope")

// hiddenResourceMessage is deliberately generic: it must read identically
// whether the resource never existed or the caller simply cannot see it.
const hiddenResourceMessage = "the requested resource was not found"

// WriteResourceHidden writes the leakage-normalization policy V6-02A's own
// "Thực hiện" line requires ("Normalize unauthorized/not-found theo
// leakage policy"): a resource lookup that fails because the resource does
// not exist, and one that fails because it exists in a scope the caller is
// not authorized to see, MUST produce the exact same status, code and
// message — otherwise an attacker can enumerate real IDs simply by
// noticing which failure a guessed ID produces. Every future route task
// that authorizes a scoped resource lookup (V6-03A onward) calls this one
// function from BOTH of its own "not found" and "not authorized" branches,
// rather than writing two responses that might quietly diverge over time.
func WriteResourceHidden(w http.ResponseWriter) {
	WriteError(w, http.StatusNotFound, ErrorCodeNotFound, hiddenResourceMessage, nil)
}

// WriteDecodeError maps DecodeJSON's (json.go) own typed
// ErrBodyTooLarge/ErrMalformedJSON into the canonical envelope — the
// "should feed naturally into DecodeJSON's existing typed errors" half of
// V6-02A's own error-envelope scope.
func WriteDecodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBodyTooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, ErrorCodeInvalidRequest, "request body exceeds the size limit", nil)
	case errors.Is(err, ErrMalformedJSON):
		WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "request body is not valid JSON", nil)
	default:
		WriteError(w, http.StatusInternalServerError, ErrorCodeInternal, "internal error", nil)
	}
}

// WriteResyncRequired writes the typed resync response for a cursor
// ResyncError (cursor.go) — V6-02A's own "mismatch/swap trả typed resync"
// line. 409 Conflict is the HTTP status (the caller's cursor conflicts
// with the server's current state); ErrorCodeResyncRequired is the
// machine-readable wire code a client checks to know the correct recovery
// is "restart the walk," not "retry the same request."
func WriteResyncRequired(w http.ResponseWriter, reason ResyncReason) {
	WriteError(w, http.StatusConflict, ErrorCodeResyncRequired,
		"cursor is no longer valid for the current project/query/generation; restart pagination without a cursor",
		[]ErrorDetail{{Field: "cursor", Message: string(reason)}})
}

// WriteCursorInvalid writes the response for cursor.go's own
// ErrCursorInvalid (a malformed or tampered opaque cursor token) — a
// caller mistake distinct from ResyncError's "valid but stale" case, so it
// gets ErrorCodeInvalidRequest (400) rather than ErrorCodeResyncRequired
// (409): there is no server-side state to resync against, the token
// itself is not one this server ever issued.
func WriteCursorInvalid(w http.ResponseWriter) {
	WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "cursor is malformed or invalid", nil)
}

// StatusForAppErrorCode maps internal/domain/errorcode.Code's full
// 22-value domain vocabulary onto an HTTP status and this package's own
// smaller wire ErrorCode — the one shared table so no individual endpoint
// task decides its own status for a given domain code (V6-02A's own
// "Hoàn thành khi" line). Codes that describe a runtime/attempt outcome an
// HTTP caller normally observes as DATA rather than as a request failure
// (CodeExecutionFailed, CodeIndeterminate, CodeRetryExhausted, and any
// future/unrecognized code) fall through to the same safe default as
// CodeInternal: 500 with ErrorCodeInternal.
//
// This table is the GENERIC mapping. It must never be used for a scoped
// resource lookup's own not-found/unauthorized branches — those call
// WriteResourceHidden directly instead, per the leakage-normalization
// policy documented there; CodePolicyDenied/CodeScopeViolation below map
// to a plain 403 Forbidden precisely because THIS table has no way to know
// whether a given call site is one of those leakage-sensitive lookups.
func StatusForAppErrorCode(code apperror.Code) (int, ErrorCode) {
	switch code {
	case errorcode.CodeInvalidArgument, errorcode.CodeValidationFailed:
		return http.StatusBadRequest, ErrorCodeInvalidRequest
	case errorcode.CodeNotFound:
		return http.StatusNotFound, ErrorCodeNotFound
	case errorcode.CodeAlreadyExists, errorcode.CodeConflict, errorcode.CodeIdempotencyConflict,
		errorcode.CodeAdapterBuildDrift, errorcode.CodeLeaseLost, errorcode.CodeFenceRejected,
		errorcode.CodeCancelled:
		return http.StatusConflict, ErrorCodeConflict
	case errorcode.CodePreconditionFailed:
		return http.StatusPreconditionFailed, ErrorCodeConflict
	case errorcode.CodeWorkspaceQuarantined:
		return http.StatusLocked, ErrorCodeConflict
	case errorcode.CodePolicyDenied, errorcode.CodeScopeViolation:
		return http.StatusForbidden, ErrorCodeForbidden
	case errorcode.CodeUnavailable, errorcode.CodeProviderUnavailable, errorcode.CodeIsolationEnforcementUnavailable:
		return http.StatusServiceUnavailable, ErrorCodeUnavailable
	case errorcode.CodeTimeout:
		return http.StatusGatewayTimeout, ErrorCodeUnavailable
	default: // CodeInternal, CodeExecutionFailed, CodeIndeterminate, CodeRetryExhausted, and anything unrecognized
		return http.StatusInternalServerError, ErrorCodeInternal
	}
}

// WriteAppError writes err as the canonical envelope: an *apperror.Error's
// Code is mapped via StatusForAppErrorCode and its Message (already the
// safe, public-facing half of apperror.Error — see
// internal/app/apperror's own doc comment) is used verbatim; any other
// error (including a nil-typed check failure) writes a bare 500 INTERNAL
// with a generic message, never the raw error's own text, which has made
// no promise of being safe to expose.
func WriteAppError(w http.ResponseWriter, err error) {
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		status, code := StatusForAppErrorCode(appErr.Code)
		WriteError(w, status, code, appErr.Message, nil)
		return
	}
	WriteError(w, http.StatusInternalServerError, ErrorCodeInternal, "internal error", nil)
}
