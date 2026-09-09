package fake

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// SecretResolver is a fixed, deterministic ports.SecretResolver for tests:
// Values maps a secret name to the value Resolve returns for it; a name
// with no entry fails closed (ErrSecretNotSet), mirroring the production
// internal/adapters/secretenv.Resolver's own fail-closed behavior for an
// unset environment variable.
type SecretResolver struct {
	Values map[string]string
}

// ErrSecretNotSet mirrors internal/adapters/secretenv.Resolver's own
// sentinel — a separate value (not a re-export) since this package never
// imports an adapters package, the same "no domain/app package depends on
// adapters" boundary this codebase already enforces elsewhere.
var ErrSecretNotSet = fmt.Errorf("fake: secret is not set")

var _ ports.SecretResolver = SecretResolver{}

func (r SecretResolver) Resolve(_ context.Context, name string) (string, error) {
	value, ok := r.Values[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrSecretNotSet, name)
	}
	return value, nil
}
