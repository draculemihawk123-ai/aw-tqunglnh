// Package apperror is Alpha's application-layer error envelope
// (docs/design/03-v1-alpha-foundation.md V1-02): a typed Code plus a
// Retryable flag are what application logic branches on. This exists
// specifically so no call site ever parses a SQL/provider error message to
// decide what to do next (00-roadmap.md V1-02's "bỏ parse message
// SQL/provider khỏi application decisions") — an adapter maps its own
// concrete errors (a SQLite busy/locked code, a provider exit status, ...)
// into one of these Codes once, at the adapter boundary, and everything
// above that boundary only ever sees the typed Code.
package apperror

import (
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// Code is a stable, typed category for an application error — a type
// alias (not a wrapper) for internal/domain/errorcode.Code (V4-06,
// correction found during scoping review): V1-02 originally defined this
// type locally with only 5 of go-core-spec §18's own 22 values, which left
// no typed way for an ExecutionAttempt's own failure to carry a code from
// the other 17 (EXECUTION_FAILED, TIMEOUT, PROVIDER_UNAVAILABLE, ...).
// Unifying onto the domain package's own canonical enum here — rather than
// maintaining two independent code sets with the same names and meaning —
// is what lets every one of this package's ~80 existing call sites keep
// referencing apperror.Code/apperror.CodeXxx unchanged (a type alias makes
// apperror.Code and errorcode.Code the exact same type, not merely
// convertible), while every new caller that needs one of the other 17
// values reaches the domain package directly.
type Code = errorcode.Code

const (
	// CodeInvalidArgument: the caller supplied a request the domain can
	// never satisfy, no matter how many times it retries.
	CodeInvalidArgument = errorcode.CodeInvalidArgument
	// CodeNotFound: the referenced aggregate/entity does not exist.
	CodeNotFound = errorcode.CodeNotFound
	// CodeConflict: an optimistic-concurrency/CAS check lost against a
	// value that has genuinely already changed — retrying with the same
	// expected version will never succeed; the caller must reload first.
	CodeConflict = errorcode.CodeConflict
	// CodeUnavailable: a transient condition (e.g. lock contention) that a
	// bounded retry of the exact same operation may resolve on its own.
	CodeUnavailable = errorcode.CodeUnavailable
	// CodeInternal: an unexpected failure with no more specific category;
	// the private cause is where the actual diagnostic detail lives.
	CodeInternal = errorcode.CodeInternal
)

// Error is the envelope itself. Message and Details are the public/safe
// surface — safe to log, return over the API, or show an operator; cause
// is deliberately unexported so nothing outside this package can read it
// by accident (a naive %+v verb, a JSON marshal of the struct). Unwrap and
// Cause are the explicit, named escape hatches for the one place that
// legitimately needs the raw cause — an internal diagnostic sink, once
// V1-02A's shared redactor exists to pass it through safely.
type Error struct {
	Code      Code
	Message   string
	Retryable bool
	Details   map[string]string
	cause     error
}

// New creates an Error with no wrapped cause.
func New(code Code, message string, retryable bool) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable}
}

// Wrap creates an Error that also carries a private cause.
func Wrap(code Code, message string, retryable bool, cause error) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable, cause: cause}
}

// WithDetails returns a copy of e with Details set, leaving e itself
// unmodified. Detail values must already be safe to expose publicly — this
// method does not redact anything.
func (e *Error) WithDetails(details map[string]string) *Error {
	clone := *e
	clone.Details = details
	return &clone
}

// Error implements the error interface using only the public Code and
// Message — never the private cause — so a bare fmt.Println(err) or %v
// verb cannot leak it.
func (e *Error) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the private cause to errors.Is/errors.As chains; those
// APIs never print a wrapped error's text on their own, so this does not
// reintroduce the leak Error() avoids.
func (e *Error) Unwrap() error { return e.cause }

// Cause returns the private wrapped error, or nil if none was set. Named
// distinctly from Unwrap so every call site is searchable, and to signal
// that the caller now owns keeping this value out of any sink that is not
// itself redaction-safe.
func (e *Error) Cause() error { return e.cause }

// Is reports whether target is an *Error with the same Code — Code, not
// Message/Details/cause, is what application logic is meant to branch on.
func (e *Error) Is(target error) bool {
	var other *Error
	if !errors.As(target, &other) {
		return false
	}
	return e.Code == other.Code
}

// IsRetryable reports whether err (or something it wraps) is an *Error
// marked Retryable. A nil or non-*Error err is never retryable.
func IsRetryable(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Retryable
}

// CodeOf returns err's Code, or "" if err is not an *Error.
func CodeOf(err error) Code {
	var e *Error
	if !errors.As(err, &e) {
		return ""
	}
	return e.Code
}
