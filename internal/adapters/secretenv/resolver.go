// Package secretenv is the production ports.SecretResolver (V5-09's own
// Alpha scope): a secret name resolves to the worker HOST's own OS
// environment variable of the identical name. This is the same "trust the
// local machine" posture ADR-016's own local HTTP trust boundary already
// establishes elsewhere in this codebase — Alpha has no real secret store
// (Vault or equivalent) anywhere, and CommandDocument.SecretRefs names only
// ever need resolving right before spawn, never persisted (see that
// field's own doc comment) — so the operator provisioning the worker
// process's own environment IS the secret store for Alpha. A real
// secret-store-backed implementation is a clean drop-in replacement later:
// ports.SecretResolver's own contract never changes.
package secretenv

import (
	"context"
	"fmt"
	"os"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ErrSecretNotSet is returned when name names no environment variable on
// this worker host — fails closed rather than resolving to an empty
// string, the same "a missing pin/grant never silently substitutes a
// default" posture this codebase's admission checks already establish.
var ErrSecretNotSet = fmt.Errorf("secretenv: secret is not set in this worker's own environment")

// Resolver is the production ports.SecretResolver. It holds no state and
// does no I/O beyond a single os.LookupEnv call.
type Resolver struct{}

// NewResolver returns the production, OS-environment-backed SecretResolver.
func NewResolver() Resolver { return Resolver{} }

var _ ports.SecretResolver = Resolver{}

func (Resolver) Resolve(_ context.Context, name string) (string, error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrSecretNotSet, name)
	}
	return value, nil
}
