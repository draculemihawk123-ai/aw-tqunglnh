package errorcode_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

func TestCode_Valid(t *testing.T) {
	valid := []errorcode.Code{
		errorcode.CodeInvalidArgument, errorcode.CodeNotFound, errorcode.CodeAlreadyExists,
		errorcode.CodeConflict, errorcode.CodeIdempotencyConflict, errorcode.CodePreconditionFailed,
		errorcode.CodeValidationFailed, errorcode.CodePolicyDenied, errorcode.CodeScopeViolation,
		errorcode.CodeLeaseLost, errorcode.CodeFenceRejected, errorcode.CodeWorkspaceQuarantined,
		errorcode.CodeIsolationEnforcementUnavailable, errorcode.CodeAdapterBuildDrift,
		errorcode.CodeUnavailable, errorcode.CodeProviderUnavailable, errorcode.CodeExecutionFailed,
		errorcode.CodeTimeout, errorcode.CodeCancelled, errorcode.CodeIndeterminate,
		errorcode.CodeRetryExhausted, errorcode.CodeInternal,
	}
	if len(valid) != 22 {
		t.Fatalf("test lists %d codes, want exactly 22 (go-core-spec §18's own count)", len(valid))
	}
	for _, code := range valid {
		if !code.Valid() {
			t.Fatalf("Code(%q).Valid() = false, want true", code)
		}
	}
	if errorcode.Code("NOT_A_REAL_CODE").Valid() {
		t.Fatal(`Code("NOT_A_REAL_CODE").Valid() = true, want false`)
	}
}

func TestCode_NeverRetryable(t *testing.T) {
	neverRetryable := []errorcode.Code{errorcode.CodeIsolationEnforcementUnavailable, errorcode.CodeIndeterminate}
	for _, code := range neverRetryable {
		if !code.NeverRetryable() {
			t.Fatalf("Code(%q).NeverRetryable() = false, want true", code)
		}
	}
	if errorcode.CodeExecutionFailed.NeverRetryable() {
		t.Fatal("CodeExecutionFailed.NeverRetryable() = true, want false")
	}
}
