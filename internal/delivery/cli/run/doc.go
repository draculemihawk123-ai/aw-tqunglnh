// Package run is V6-15H's own CLI leaf
// (docs/design/08-v6-api-projections.md:723-731): "start, inspect, cancel,
// and recover a Run from the terminal" — `aw run start|show|cancel|graph|
// timeline|diagnostics`. It is a thin dispatch layer built entirely on top
// of internal/delivery/cli (V6-15B's own shared framework — see that
// package's own sample_test.go for the reference end-to-end pattern this
// file follows) and internal/app/runtime's own already-real, already-tested
// commands/queries (StartWorkflowRun, CancelRun, GetRunDetail, GetRunGraph,
// GetRunTimeline, GetRunDiagnostics) — never a second implementation of any
// of them.
//
// Each subcommand registers its own cli.Descriptor via this package's own
// init() (descriptor registrations live next to each subcommand's own
// file: start.go, cancel.go, show.go, graph.go, timeline.go,
// diagnostics.go) into the shared internal/delivery/cli registry — this
// package never touches cmd/aw/main.go or cmd/aw/cli.go itself (the
// CRITICAL scope rule this task's own brief names: routing real os.Args to
// this leaf is explicitly deferred to a future V6-15O, "chỉ V6-15O compose
// CLI/parity registry").
//
// Package `noderun` (internal/delivery/cli/noderun) is this task's own
// sibling package for `aw node-run retry-blocked` — kept separate from
// `run` because it wraps a different HTTP surface entirely
// (internal/delivery/httpapi/recovery, not internal/delivery/httpapi/run),
// mirroring the same three-way HTTP package split this task's own brief
// calls out (run / rundetail / diagnostics / recovery — four HTTP packages
// this one CLI command tree fans out across).
//
// One important, deliberate asymmetry between subcommands: `run start`
// follows the full cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow
// (Idempotency-Key, semantic hash, receipt replay — see start.go), while
// `run cancel` calls runtime.CancelRun directly with a plain
// CancelRunRequest and binds NO --idempotency-key/--expected-version flags
// at all — see cancel.go's own doc comment for the full reasoning (mirrors
// internal/delivery/httpapi/run/cancel.go's own identical choice, which
// explains it at length: CancelRun is idempotent BY RunID, not by envelope).
package run

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is everything this package's own subcommands need — the
// union of every dependency internal/delivery/httpapi/run,
// internal/delivery/httpapi/rundetail and internal/delivery/httpapi/
// diagnostics each separately need from their own composition-root wiring
// (cmd/aw/serve.go), gathered here into one struct since one `aw run ...`
// command tree fans out across all three of those HTTP packages' worth of
// application-layer functions (this task's own brief, "Command surface to
// build").
type Dependencies struct {
	// UOW is the one real ports.UnitOfWork every subcommand dispatches
	// through.
	UOW ports.UnitOfWork
	// IDs mints every fresh ID a subcommand needs: BuildEnvelope's own
	// generated --idempotency-key (Start) and a fresh CorrelationID for a
	// command that carries no ports.Command envelope at all (Cancel).
	IDs idsource.Source
	// Isolation and Agents are GetRunDiagnostics' own two admission
	// cross-reference dependencies (diagnostics.go) — the SAME two
	// dependencies internal/delivery/httpapi/diagnostics.Dependencies
	// already carries (that package's own doc comment: "the SAME three
	// dependencies internal/delivery/httpapi/recovery already needs").
	Isolation ports.IsolationEnforcementChecker
	Agents    *agentregistry.Registry
	// Matcher is the SAME process-lifetime redact.Matcher construction
	// internal/delivery/httpapi/rundetail.Dependencies.Matcher already
	// uses (see that package's own doc comment) — GetRunGraph/
	// GetRunTimeline redact NodeRun.BlockReason through it before this
	// package ever sees the value. A CLI invocation has no long-lived
	// server process to mint a per-process bootstrap secret against
	// (cmd/aw/serve.go's own sessionToken has no CLI equivalent), so a
	// future composition root (V6-15O) is expected to pass
	// redact.NewMatcher() — the SAME constructor, with whatever known
	// secrets it has (possibly none) — never a different, ad hoc
	// redaction mechanism invented just for this package.
	Matcher redact.Matcher
	// Now overrides BuildEnvelope's own default time source
	// (time.Now().UTC()) — nil in production, set only for deterministic
	// tests.
	Now func() time.Time
	// Sleep overrides cli.Wait's own default Sleeper (cli.DefaultSleeper)
	// for `run start --wait` — nil in production, set only for
	// deterministic/interruptible tests (see wait_test.go).
	Sleep cli.Sleeper
}
