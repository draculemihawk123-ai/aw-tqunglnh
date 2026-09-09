package ports

import "context"

// SecretResolver is V5-09's own required COMMAND-execution dependency: a
// CommandDocument's own SecretRefs names only which secrets its execution
// may see (internal/domain/command.CommandDocument's own doc comment:
// "secret chỉ resolve ở worker ngay trước spawn và không persist") — this
// port is the one place that name-to-value resolution actually happens,
// right before ports.ProcessSupervisor.Run, and its result is injected
// directly into ProcessSpec.Environment, never Argv (a secret VALUE must
// never appear in a spawned process's own argv — visible to any other
// process/user on the same host via a process list; ProcessSpec's own
// dual env model, Environment vs InheritedEnvironment, exists for exactly
// this: an explicit, freshly-resolved value vs a name merely passed
// through from the worker's own ambient environment).
//
// Resolve fails closed (a non-nil error) rather than silently substituting
// an empty string when name cannot be resolved — the same "fail closed
// rather than run under a false assumption" posture
// IsolationEnforcementChecker.VerifyEnforceable already establishes for
// its own admission dependency.
//
// Implementations: internal/app/ports/fake.SecretResolver (tests);
// internal/adapters/secretenv.Resolver (production, V5-09's own Alpha
// scope) — resolves a secret name to the worker HOST's own OS environment
// variable of the identical name, the same "trust the local machine" Alpha
// posture ADR-016 already establishes for the local HTTP trust boundary. A
// real secret-store-backed implementation (Vault or equivalent) is a
// clean drop-in replacement later — this port's own contract never
// changes, only which implementation is wired in.
type SecretResolver interface {
	Resolve(ctx context.Context, name string) (value string, err error)
}
