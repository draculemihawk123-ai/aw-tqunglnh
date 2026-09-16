package cli

import "errors"

// ExitCode is this framework's own typed replacement for a bare
// os.Exit(N) magic number — mirroring cmd/aw/cli.go's own unexported
// exitCode (V1-01's "no bare os.Exit magic number"), promoted here to an
// exported type so a future composition-root dispatcher (cmd/aw, wired in
// V6-15C+ per this task's own "Không làm: no domain leaf... import" —
// wiring cmd/aw to call into this framework is explicitly out of THIS
// task's scope) and every leaf package built on this framework share one
// convention instead of each leaf inventing its own. Values follow the
// same Go `flag`-package convention cmd/aw/cli.go already followed (2 for
// a usage error), so external tooling that already expects that
// convention keeps working for `aw` too.
type ExitCode int

const (
	ExitSuccess ExitCode = 0
	ExitFailure ExitCode = 1
	ExitUsage   ExitCode = 2
)

// UsageError marks an error caused by how a command was invoked (bad
// flags, a missing required argument, oversized bounded input) rather
// than a failure while doing real work — mirroring cmd/aw/cli.go's own
// unexported usageError exactly, promoted here for the same reason
// ExitCode is: every leaf built on this framework needs the identical
// distinction, not a fresh reinvention per leaf.
type UsageError struct{ Err error }

func (u UsageError) Error() string { return u.Err.Error() }
func (u UsageError) Unwrap() error { return u.Err }

// IsUsageError reports whether err (or anything it wraps) is a
// UsageError.
func IsUsageError(err error) bool {
	var u UsageError
	return errors.As(err, &u)
}

// ExitCodeFor classifies err into the ExitCode a composition-root
// dispatcher should exit the process with: nil -> ExitSuccess, a
// UsageError (however deeply wrapped) -> ExitUsage, anything else ->
// ExitFailure. A leaf's own domain-specific error-to-exit-code policy (if
// one ever needs something finer than this three-way split) is layered on
// top by that leaf, not inside this shared framework rule.
func ExitCodeFor(err error) ExitCode {
	if err == nil {
		return ExitSuccess
	}
	if IsUsageError(err) {
		return ExitUsage
	}
	return ExitFailure
}
