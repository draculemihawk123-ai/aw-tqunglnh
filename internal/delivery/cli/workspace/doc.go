// Package workspace is V6-15L's own CLI leaf
// (docs/design/08-v6-api-projections.md:763-771): "inspect/recover
// workspace and bounded source/diff/log from the terminal" — `aw
// workspace-set show|release` and `aw repository-workspace
// source|diff|log|reconcile`. It is a thin dispatch layer built entirely
// on top of internal/delivery/cli (V6-15B's own shared framework) and two
// already-real, already-tested pairs of application packages:
//
//   - internal/app/workspacestate (V6-10B: GetWorkspaceSetState) and
//     internal/app/workspacerelease/internal/app/workspacereconcile
//     (V3-11/V3-10: RequestWorkspaceSetRelease/RequestWorkspaceReconciliation)
//     — mirrored here the same way internal/delivery/httpapi's own
//     workspacestate.go/workspacerelease.go/workspacereconcile.go already
//     wrap them for HTTP (workspaceroutes.go there lists the identical
//     4-route inventory this package's own show/release/reconcile
//     subcommands mirror three-for-three — this package deliberately does
//     NOT add a standalone "repository-workspace show" leaf for
//     GetRepositoryWorkspaceState, since this task's own "Command surface
//     to build" names only workspace-set show/release and
//     repository-workspace source/diff/log/reconcile).
//   - internal/app/workspaceinspection (V6-10C: GetSource/GetDiff/
//     GetRepositoryLog) — mirrored here the same way
//     internal/delivery/httpapi/workspaceinspection already wraps it for
//     HTTP (that package's own routes.go).
//
// Each subcommand registers its own cli.Descriptor via this package's own
// init() (registrations live next to each subcommand's own file: show.go,
// release.go, source.go, diff.go, log.go, reconcile.go) into the shared
// internal/delivery/cli registry — this package never touches
// cmd/aw/main.go or cmd/aw/cli.go itself (the CRITICAL scope rule this
// task's own brief names: routing real os.Args to this leaf is explicitly
// deferred to a future V6-15O, "chỉ V6-15O compose CLI/parity registry").
//
// # Two deliberately different dispatch shapes
//
//   - `workspace-set release` and `repository-workspace reconcile` follow
//     the full cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow
//     (Idempotency-Key, semantic hash, receipt replay), mirroring
//     internal/delivery/cli/run/start.go's own template exactly: both
//     underlying application commands (workspacerelease.RequestWorkspaceSetRelease,
//     workspacereconcile.RequestWorkspaceReconciliation) require a real
//     ports.Command with a non-zero ExpectedVersion, so both subcommands
//     bind cli.BindExpectedVersionFlag/cli.BindIdempotencyKeyFlag.
//   - `workspace-set show` and `repository-workspace source|diff|log` are
//     pure reads: cli.EncodeQueryResult only, no envelope, no principal —
//     mirroring internal/delivery/cli/workitem/show.go and
//     internal/delivery/cli/evidence's own query leaves.
//
// # No Git spawned, no arbitrary path, no interactive terminal
//
// This package never imports "os", "os/exec", or any internal/adapters/...
// package — internal/archtest/cli_workspace_boundary_test.go proves this
// for real, by walking this package's own AST, mirroring
// internal/archtest/workspace_delivery_boundary_test.go's and
// internal/archtest/workspace_inspection_http_test.go's identical
// "no direct Git/filesystem reach" architecture proofs for their own HTTP
// counterparts. `repository-workspace source`'s own `--output <path|->`
// streams real bytes via cli.WriteBinaryOutput (internal/delivery/cli/
// output.go) — the one file in this whole framework that legitimately
// opens a real file — which this package calls but never itself imports
// "os" for.
//
// "no arbitrary path" is enforced the identical way
// internal/delivery/httpapi/workspaceinspection already enforces it: every
// path/revision/cursor this package accepts is handed, unmodified, straight
// through to internal/app/workspaceinspection.Queries, which independently
// re-validates everything one layer below via
// ports.WorkspaceInspectionReader (a real internal/adapters/gitworktree.Provider
// at the composition root) — this package itself never inspects, resolves,
// or normalizes a path or revision string. mapInspectionQueryError
// (helpers.go) deliberately collapses every error other than
// ErrScopeMismatch/ErrWorkspaceNotReady/ports.ErrPersistenceNotFound into
// one opaque, generic message — the identical "structurally cannot name
// gitworktree's own sentinels without importing the forbidden package"
// boundary internal/delivery/httpapi/workspaceinspection/errors.go's own
// writeQueryError already establishes, restated here for the CLI so a path
// traversal attempt, an unauthorized/stale revision, a binary/directory/
// symlink/submodule rejection, or a non-ancestor log cursor are all
// surfaced identically opaquely — never with a more specific message that
// would leak which of those adapter-internal conditions actually fired.
package workspace

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Dependencies is everything this package's own subcommands need — the
// union of every dependency internal/delivery/httpapi/workspaceroutes.go
// and internal/delivery/httpapi/workspaceinspection each separately need
// from their own composition-root wiring (cmd/aw/serve.go), gathered here
// since one `aw workspace-set|repository-workspace ...` command tree fans
// out across both.
type Dependencies struct {
	// UOW is the one real ports.UnitOfWork every subcommand dispatches
	// through.
	UOW ports.UnitOfWork
	// IDs mints every fresh ID `workspace-set release`/`repository-workspace
	// reconcile` need: BuildEnvelope's own generated --idempotency-key and
	// the job id workspacerelease.RequestWorkspaceSetRelease/
	// workspacereconcile.RequestWorkspaceReconciliation each mint for their
	// own enqueued job.
	IDs idsource.Source
	// Authority is the real ports.ReleaseEligibilityAuthority
	// `workspace-set release` depends on — a composition root constructs
	// this ONCE, via work.NewEligibilityAuthority(uow) (the same
	// construction internal/delivery/httpapi/workspaceroutes.go's own
	// RegisterWorkspaceRoutes performs), and threads it through here; this
	// package never constructs its own.
	Authority ports.ReleaseEligibilityAuthority
	// Reader is the real ports.WorkspaceInspectionReader (a real
	// internal/adapters/gitworktree.Provider in production)
	// `repository-workspace source|diff|log` build their own
	// internal/app/workspaceinspection.Queries against, once per
	// invocation — mirrors internal/delivery/httpapi/workspaceinspection's
	// own Dependencies.Queries wiring, except this package holds the raw
	// pair (UOW, Reader) rather than an already-built *Queries, consistent
	// with every other Dependencies struct in this CLI framework holding
	// raw ports rather than a pre-wired application object (see
	// internal/delivery/cli/evidence.Dependencies for the identical
	// "raw UnitOfWork + raw ArtifactStore, not a wrapped object" shape).
	Reader ports.WorkspaceInspectionReader
	// Now overrides BuildEnvelope's own default time source
	// (time.Now().UTC()) — nil in production, set only for deterministic
	// tests.
	Now func() time.Time
}
