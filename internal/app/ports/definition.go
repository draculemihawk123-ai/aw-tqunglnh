package ports

import (
	"context"
	"errors"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// ErrCrossProjectDependency is returned when a DependencyPin resolves to
// a definition in a different project than the one publishing against
// it. The referenced definition's actual project is always resolved by
// the repository itself at publish time — never trusted from a value the
// publisher supplied, which would make this check spoofable.
var ErrCrossProjectDependency = errors.New("ports: dependency pin references a definition in a different project")

// ErrDefinitionVersionNotFound is returned by
// DefinitionsRepository.LoadVersion when no Version exists for the given
// ID — the resolution-time counterpart of a dependency pin naming a
// Version that was never published (or never will be, e.g. a typo).
var ErrDefinitionVersionNotFound = errors.New("ports: definition version not found")

// PublishVersionRequest is what a caller supplies to
// DefinitionPublisher.PublishDefinitionVersion — kind-agnostic; the
// Definition it publishes against must already exist (create is a
// separate concern, V2-10's job). VersionNumber is deliberately absent:
// the next version number is always the repository's own responsibility
// to allocate, the same way V1-07's journal_position is, so two
// concurrent publishers can never both believe they own the same number.
type PublishVersionRequest struct {
	DefinitionID     string
	Kind             definition.Kind
	VersionID        string
	SchemaVersion    int
	CanonicalSource  string
	SourceHash       string
	CompiledSnapshot string
	CompiledHash     string
	Dependencies     definition.DependencyManifest
	PublishedBy      string
	PublishedAt      time.Time
}

// DefinitionPublisher is the one application-level entry point every
// DefinitionKind's publish goes through (docs/design/04-v2-definition-plane.md
// V2-02): application code never has to know whether a given Kind is
// backed by Workflow's own dedicated tables (V0) or the shared
// definitions/definition_versions tables the other 8 kinds use (V2-02) —
// both are hidden behind this single method. Publishing the exact same
// compiled content twice is idempotent (returns the already-published
// Version, never a duplicate row); publishing genuinely different
// content is never silently allowed to overwrite an existing version
// number.
type DefinitionPublisher interface {
	PublishDefinitionVersion(ctx context.Context, req PublishVersionRequest) (definition.VersionFields, error)
}
