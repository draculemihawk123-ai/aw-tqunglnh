// Package errorcode is the canonical, closed AppError code enum
// docs/architecture/04-go-core-spec.md §18 defines — the SINGLE source of
// truth every layer that needs to classify a failure (internal/app/apperror's
// own application-error envelope, internal/domain/policy's own
// AttemptRules.RetryableErrorCodes allow-list, internal/domain/runtime's own
// ExecutionAttempt.FailureCode) now shares, rather than each maintaining its
// own private, potentially-drifting copy of the same 22-value set (V2-07's
// own policy.go had already hardcoded an equivalent but unexported map;
// V1-02's own apperror.Code had independently implemented only 5 of the 22
// values). It lives in the domain layer specifically so app-layer packages
// (apperror included) can depend on it without inverting the "domain never
// depends on app" rule this codebase's own internal/archtest enforces.
package errorcode

// Code is a stable, typed category for a failure — go-core-spec §18's own
// "Mã lỗi chuẩn" enum, verbatim.
type Code string

const (
	CodeInvalidArgument                 Code = "INVALID_ARGUMENT"
	CodeNotFound                        Code = "NOT_FOUND"
	CodeAlreadyExists                   Code = "ALREADY_EXISTS"
	CodeConflict                        Code = "CONFLICT"
	CodeIdempotencyConflict             Code = "IDEMPOTENCY_CONFLICT"
	CodePreconditionFailed              Code = "PRECONDITION_FAILED"
	CodeValidationFailed                Code = "VALIDATION_FAILED"
	CodePolicyDenied                    Code = "POLICY_DENIED"
	CodeScopeViolation                  Code = "SCOPE_VIOLATION"
	CodeLeaseLost                       Code = "LEASE_LOST"
	CodeFenceRejected                   Code = "FENCE_REJECTED"
	CodeWorkspaceQuarantined            Code = "WORKSPACE_QUARANTINED"
	CodeIsolationEnforcementUnavailable Code = "ISOLATION_ENFORCEMENT_UNAVAILABLE"
	CodeAdapterBuildDrift               Code = "ADAPTER_BUILD_DRIFT"
	CodeUnavailable                     Code = "UNAVAILABLE"
	CodeProviderUnavailable             Code = "PROVIDER_UNAVAILABLE"
	CodeExecutionFailed                 Code = "EXECUTION_FAILED"
	CodeTimeout                         Code = "TIMEOUT"
	CodeCancelled                       Code = "CANCELLED"
	CodeIndeterminate                   Code = "INDETERMINATE"
	CodeRetryExhausted                  Code = "RETRY_EXHAUSTED"
	CodeInternal                        Code = "INTERNAL"
)

var validCodes = map[Code]bool{
	CodeInvalidArgument: true, CodeNotFound: true, CodeAlreadyExists: true,
	CodeConflict: true, CodeIdempotencyConflict: true, CodePreconditionFailed: true,
	CodeValidationFailed: true, CodePolicyDenied: true, CodeScopeViolation: true,
	CodeLeaseLost: true, CodeFenceRejected: true, CodeWorkspaceQuarantined: true,
	CodeIsolationEnforcementUnavailable: true, CodeAdapterBuildDrift: true,
	CodeUnavailable: true, CodeProviderUnavailable: true, CodeExecutionFailed: true,
	CodeTimeout: true, CodeCancelled: true, CodeIndeterminate: true,
	CodeRetryExhausted: true, CodeInternal: true,
}

// Valid reports whether c is one of the 22 codes go-core-spec §18 defines.
func (c Code) Valid() bool { return validCodes[c] }

// neverRetryable is go-core-spec §14/§18's own explicit carve-out: these
// codes must never be treated as retryable technical failure, regardless of
// what any AttemptRules.RetryableErrorCodes list says —
// ISOLATION_ENFORCEMENT_UNAVAILABLE is fail-closed admission behavior, and
// INDETERMINATE always needs recovery/escalation, never a plain retry.
var neverRetryable = map[Code]bool{
	CodeIsolationEnforcementUnavailable: true,
	CodeIndeterminate:                   true,
}

// NeverRetryable reports whether c is one of go-core-spec §14/§18's own
// explicit "never retryable" codes.
func (c Code) NeverRetryable() bool { return neverRetryable[c] }
