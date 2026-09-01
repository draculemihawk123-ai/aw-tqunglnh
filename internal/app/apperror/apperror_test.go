package apperror

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorMessageNeverIncludesCause(t *testing.T) {
	cause := errors.New("SQLITE_BUSY: database is locked, dsn=file:secret.db?password=hunter2")
	err := Wrap(CodeUnavailable, "workspace lease is contended", true, cause)
	got := err.Error()
	if strings.Contains(got, "SQLITE_BUSY") || strings.Contains(got, "hunter2") {
		t.Fatalf("Error() leaked the private cause: %q", got)
	}
}

func TestCauseIsRecoverableExplicitly(t *testing.T) {
	cause := errors.New("underlying detail")
	err := Wrap(CodeInternal, "public message", false, cause)
	if err.Cause() != cause {
		t.Fatalf("Cause() = %v, want %v", err.Cause(), cause)
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is(err, cause) should be true via Unwrap")
	}
}

func TestNewHasNoCause(t *testing.T) {
	err := New(CodeNotFound, "not found", false)
	if err.Cause() != nil {
		t.Fatalf("New() should have a nil Cause, got %v", err.Cause())
	}
}

func TestIsMatchesBySameCodeOnly(t *testing.T) {
	a := New(CodeConflict, "first message", false)
	b := New(CodeConflict, "different message entirely", false)
	if !errors.Is(a, b) {
		t.Fatal("two *Error values with the same Code should satisfy errors.Is regardless of Message")
	}
	c := New(CodeUnavailable, "first message", true)
	if errors.Is(a, c) {
		t.Fatal("two *Error values with different Code should not satisfy errors.Is")
	}
}

func TestIsRetryable(t *testing.T) {
	retryable := New(CodeUnavailable, "contended", true)
	if !IsRetryable(retryable) {
		t.Fatal("IsRetryable should be true for a Retryable error")
	}
	permanent := New(CodeConflict, "lost CAS", false)
	if IsRetryable(permanent) {
		t.Fatal("IsRetryable should be false for a non-retryable error")
	}
	if IsRetryable(errors.New("plain error")) {
		t.Fatal("IsRetryable should be false for a non-*Error")
	}
	if IsRetryable(nil) {
		t.Fatal("IsRetryable should be false for nil")
	}
}

func TestCodeOf(t *testing.T) {
	err := New(CodeNotFound, "missing", false)
	if got := CodeOf(err); got != CodeNotFound {
		t.Fatalf("CodeOf = %q, want %q", got, CodeNotFound)
	}
	if got := CodeOf(errors.New("plain")); got != "" {
		t.Fatalf("CodeOf(plain error) = %q, want empty", got)
	}
}

func TestWithDetailsDoesNotMutateOriginal(t *testing.T) {
	original := New(CodeInvalidArgument, "bad input", false)
	withDetails := original.WithDetails(map[string]string{"field": "name"})
	if original.Details != nil {
		t.Fatalf("original.Details should remain nil, got %v", original.Details)
	}
	if withDetails.Details["field"] != "name" {
		t.Fatalf("withDetails.Details[field] = %q, want name", withDetails.Details["field"])
	}
}

func TestErrorWrapsIntoStandardErrorChain(t *testing.T) {
	sentinelCause := errors.New("sentinel")
	wrapped := Wrap(CodeInternal, "wrapped", false, sentinelCause)
	var target *Error
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As should find the *Error itself")
	}
	if !errors.Is(wrapped, sentinelCause) {
		t.Fatal("errors.Is should reach the wrapped sentinel cause")
	}
}

func TestErrorStringIncludesCode(t *testing.T) {
	err := New(CodeConflict, "version mismatch", false)
	got := err.Error()
	if !strings.Contains(got, string(CodeConflict)) || !strings.Contains(got, "version mismatch") {
		t.Fatalf("Error() = %q, want it to contain both the code and the message", got)
	}
}
