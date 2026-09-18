// Package releaseset is V6-15M's own CLI leaf
// (docs/design/08-v6-api-projections.md:773-781): "operate local
// multi-repository release and local commit safely from the terminal" —
// `aw release-set list|create|show|seal|abandon|local-commit`. It is a thin
// dispatch layer built entirely on top of internal/delivery/cli (V6-15B's
// own shared framework) and internal/app/work's/internal/app/
// releasesetcommit's own already-real, already-tested commands/queries
// (CreateReleaseSet/SealReleaseSet/AbandonReleaseSet/GetReleaseSet/
// ListReleaseSetsForFamily/GetReleaseSetLocalCommitStatus,
// RequestReleaseSetLocalCommit) — never a second implementation of any of
// them. internal/delivery/httpapi/releaseset (V6-10F) is this package's own
// direct precedent: every subcommand below mirrors that package's own
// handler one for one (see each subcommand file's own doc comment for the
// exact mapping).
//
// This package never touches cmd/aw/main.go or cmd/aw/cli.go itself — the
// CRITICAL scope rule this task's own brief names: routing real os.Args to
// this leaf is explicitly deferred to a future V6-15O, "chỉ V6-15O compose
// CLI/parity registry". Each subcommand registers its own cli.Descriptor via
// this package's own init() (below) into the shared internal/delivery/cli
// registry.
//
// # Không làm (locked scope, V6-15M's own line): LOCAL only
//
// No push/fetch/PR/merge/rebase/force-push, ever, from anywhere in this
// package. There is no push/fetch/remote-mutating port anywhere in this
// codebase to spy on at runtime (ports.LocalCommitCreator's own doc
// comment: "CreateLocalCommit is the only Git-mutating method this port —
// or any port in this codebase — declares"), so the proof this package
// never reaches one is architectural absence instead: see
// internal/archtest's own TestDeliveryCLIReleaseSetNeverReachesGitOrWorker
// (releaseset_cli_boundary_test.go), which walks this package's own AST and
// forbids importing "os", "os/exec" or any internal/adapters/... package,
// and forbids any call expression named ExecuteReleaseSetLocalCommit or
// CreateLocalCommit — mirroring
// internal/archtest/releaseset_delivery_boundary_test.go's own
// TestDeliveryReleaseSetRoutesNeverReachGitOrWorker exactly, scoped to this
// package instead of the HTTP one. internal/archtest's own pre-existing
// TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters and
// TestDeliveryCLINeverDirectlyImportsAnInternalWorkerPackage
// (cli_boundary_test.go) already cover this package too, recursively, since
// they walk all of internal/delivery/cli/....
//
// # `local-commit` is asynchronous — `--wait` is not decoration
//
// RequestReleaseSetLocalCommit (releasesetcommit package) does ONLY
// database work inside one transaction — it never touches real Git. The
// real commit happens later, out-of-band, inside
// releasesetcommit.ExecuteReleaseSetLocalCommit, driven by a separate `aw
// worker` process claiming the durable job this command enqueues — never
// synchronously inside this leaf's own dispatch call. Without --wait, `aw
// release-set local-commit` returns almost immediately with State:
// "REQUESTED" and a JobID; --wait (localcommit.go) is the only way an
// operator sees the real COMMITTED/FAILED outcome from one invocation,
// built by copying internal/delivery/cli/run's own `run start --wait`
// wiring exactly (cli.BindWaitFlags/cli.Wait/cli.WaitOptions, a Dependencies-
// level Sleep seam for deterministic tests) — see localcommit.go's own doc
// comment for the full contract.
//
// # `create`'s own repeatable-entry design choice
//
// `release-set create`'s own Repositories list (one CreateReleaseSetRequest
// entry per repository, each pinning an EXACT base/result VCS object ID
// pair — never a floating branch/ref) is read as a JSON body via --file/
// stdin (cli.ReadBoundedInput), never a repeatable `--entry k=v,k=v` flag:
// this mirrors internal/delivery/cli/workitem's own `work-item create`
// (create.go)'s identical choice for its own repeatable InitialScope
// list — a structured, multi-field (4 fields per entry here) list validates
// far more cleanly as JSON (reusing encoding/json's own array/object
// grammar, no custom mini-syntax/escaping to invent or document) than as a
// bespoke inline flag grammar, and it is this codebase's own only existing
// precedent for "a CLI leaf accepts a caller-repeated structured entry
// list" — reusing it here keeps every multi-entry leaf in this framework
// consistent with the identical input mechanism, rather than each leaf
// inventing its own.
package releaseset

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is everything this package's own subcommands need.
type Dependencies struct {
	// UoW is the one real ports.UnitOfWork every subcommand dispatches
	// through.
	UoW ports.UnitOfWork
	// IDs mints every fresh ID a subcommand needs: BuildEnvelope's own
	// generated --idempotency-key (create/local-commit) and
	// RequestReleaseSetLocalCommit's own ReleaseSetLocalCommitID/JobID
	// (local-commit).
	IDs idsource.Source
	// Now overrides BuildEnvelope's own default time source
	// (time.Now().UTC()) — nil in production, set only for deterministic
	// tests.
	Now func() time.Time
	// Sleep overrides cli.Wait's own default Sleeper (cli.DefaultSleeper)
	// for `release-set local-commit --wait` — nil in production, set only
	// for deterministic/interruptible tests, mirroring
	// internal/delivery/cli/run.Dependencies.Sleep's own identical field
	// exactly.
	Sleep cli.Sleeper
}

// This block registers this package's own six Descriptors into cli.Default
// from this package's own init() — the "own package, own init()"
// discipline descriptor.go's own doc comment describes, so no shared
// registry file ever needs editing for this leaf to add itself. Path/Scope
// mirror the "Command surface to build" this task's own brief lists one for
// one; HTTPOperationID mirrors
// internal/delivery/httpapi/releaseset/routes.go's own RegisterRoutes
// OperationID values exactly — the same operation this leaf's own dispatch
// calls into by way of the shared application function AppOperation names.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"release-set", "list"}, Scope: cli.ScopeProject,
		AppOperation: appOpListReleaseSetsForFamily, HTTPOperationID: "listReleaseSetsForFamily",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"release-set", "create"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeCreateReleaseSet, HTTPOperationID: "createReleaseSet",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"release-set", "show"}, Scope: cli.ScopeProject,
		AppOperation: appOpGetReleaseSet, HTTPOperationID: "getReleaseSet",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"release-set", "seal"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeSealReleaseSet, HTTPOperationID: "sealReleaseSet",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"release-set", "abandon"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeAbandonReleaseSet, HTTPOperationID: "abandonReleaseSet",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"release-set", "local-commit"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeRequestReleaseSetLocalCommit, HTTPOperationID: "requestReleaseSetLocalCommit",
	})
}
