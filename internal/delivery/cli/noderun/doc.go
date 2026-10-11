// Package noderun is V6-15H's own second CLI leaf package — `aw node-run
// retry-blocked <nodeRunId>` (docs/design/08-v6-api-projections.md:723-731).
// Kept separate from internal/delivery/cli/run (this task's own sibling
// package) because it wraps a different HTTP surface entirely:
// internal/delivery/httpapi/recovery, not internal/delivery/httpapi/run —
// the SAME package split this task's own brief calls out explicitly
// ("Your CLI's run command tree fans out across all four of these HTTP
// packages' worth of app-layer functions ... make sure your own CLI
// package structure cleanly separates concerns the same way").
package noderun

import (
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is everything this package's one subcommand needs — the
// SAME shape internal/delivery/httpapi/recovery.Dependencies already
// carries (recovery.go there), minus that package's other two commands'
// own concerns (CancelWorkItem/ResolveWorkItemBlocker are out of this
// task's own scope).
type Dependencies struct {
	UOW       ports.UnitOfWork
	IDs       idsource.Source
	Isolation ports.IsolationEnforcementChecker
	Agents    *agentregistry.Registry
	// ArtifactStore, Matcher and Sleep serve `node-run show` and `node-run events` (V11-11): the store lets the detail summarize artifacts,
	// the matcher redacts, Sleep overrides the real clock between polls of --follow (nil = real time).
	ArtifactStore ports.ArtifactStore
	Matcher       redact.Matcher
	Sleep         cli.Sleeper
}
