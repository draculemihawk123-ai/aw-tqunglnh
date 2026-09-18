package workitemblocker

import (
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is everything this package's own one subcommand needs — the
// CLI-side counterpart of the slice of
// internal/delivery/httpapi/recovery.Dependencies that
// ResolveWorkItemBlockerHTTPHandler alone needs.
// runtime.ResolveWorkItemBlocker itself mints no new top-level ID (see
// internal/app/runtime/resolve_work_item_blocker.go's own doc comment on
// why — BlockerID is caller-supplied and a WAIVED resolution's own
// DecisionArtifact ID is deterministic from it), so IDs here is used only
// to mint a fresh per-invocation CorrelationID — mirroring
// internal/delivery/cli/run/cancel.go's own identical use of deps.IDs for
// the same tracing-only purpose on a command with no ports.Command
// envelope of its own.
type Dependencies struct {
	// UoW is the one real ports.UnitOfWork this package's own subcommand
	// dispatches through.
	UoW ports.UnitOfWork
	// IDs mints a fresh CorrelationID for this command's own tracing field
	// — never an idempotency key (this command has none).
	IDs idsource.Source
}

// This block registers this package's own one Descriptor into cli.Default
// from this package's own init() — the "own package, own init()"
// discipline descriptor.go's own doc comment describes.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"blocker", "resolve"}, Scope: cli.ScopeProject,
		AppOperation: "ResolveWorkItemBlocker", HTTPOperationID: "resolveWorkItemBlocker",
	})
}
