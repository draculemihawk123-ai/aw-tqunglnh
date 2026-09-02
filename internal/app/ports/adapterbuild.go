package ports

import (
	"context"
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// ErrNoSigningKey is returned by AdapterBuildRepository.LoadSigningKey
// when no signing key has ever been created — RegisterAdapterBuild sees
// this if it is somehow called before any ProbeAdapterBuild ever ran.
var ErrNoSigningKey = errors.New("ports: no adapter build signing key has been created yet")

// ErrAdapterBuildNotFound is returned by AdapterBuildRepository.Get for
// an unknown build ID.
var ErrAdapterBuildNotFound = errors.New("ports: adapter build version not found")

// AdapterBuildRepository is V2-07A's Tx accessor for the immutable
// AdapterBuildVersion registry (docs/design/04-v2-definition-plane.md
// V2-07A, ADR-022). It is deliberately its own accessor, not folded into
// DefinitionsRepository: AdapterBuildVersion is explicitly not a
// DefinitionKind (see internal/domain/definition.Kind's own doc comment)
// — sharing an accessor with Definitions would blur exactly the boundary
// ADR-022 draws between the authoring catalog and this operational
// registry.
type AdapterBuildRepository interface {
	// LoadOrCreateSigningKey returns the current per-installation
	// candidate-token signing key, generating and persisting a new one
	// on first call. This is the ONLY mutation ProbeAdapterBuild is ever
	// allowed to perform — it must never insert into the adapter build
	// registry itself ("không mutate registry").
	LoadOrCreateSigningKey(ctx context.Context) ([]byte, error)
	// LoadSigningKey returns the current signing key, or ErrNoSigningKey
	// if none has ever been created. RegisterAdapterBuild uses this (not
	// LoadOrCreateSigningKey): it must never itself be able to bootstrap
	// the key — only a Probe (or an explicit future rotate) does that.
	LoadSigningKey(ctx context.Context) ([]byte, error)
	// RotateSigningKey replaces the current signing key with a freshly
	// generated one. Every outstanding candidate token instantly fails
	// verification against the new key — ADR-022's own stated intent for
	// rotation, not a side effect to work around.
	RotateSigningKey(ctx context.Context) ([]byte, error)
	// InsertIfAbsent persists build under its own content-addressed ID,
	// or — if a row with that exact ID already exists — returns the
	// existing row unchanged (alreadyExisted=true) instead of erroring or
	// inserting a duplicate. No method on this interface ever updates or
	// deletes an existing row: a build's immutability is structural, not
	// merely policy.
	InsertIfAbsent(ctx context.Context, build adapterbuild.Build) (result adapterbuild.Build, alreadyExisted bool, err error)
	// Get returns the build with the given ID, or ErrAdapterBuildNotFound.
	Get(ctx context.Context, id string) (adapterbuild.Build, error)
	// List returns every registered build, ordered by RegisteredAt.
	List(ctx context.Context) ([]adapterbuild.Build, error)
}
