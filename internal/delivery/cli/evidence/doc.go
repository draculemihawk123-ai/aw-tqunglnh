// Package evidence is V6-15K's own CLI leaf
// (docs/design/08-v6-api-projections.md:753-761): "inspect/verify evidence,
// ContextSnapshot, and artifact content from the terminal" — `aw evidence
// list|verify`, `aw context-snapshot show`, `aw artifact get`. It is a thin
// dispatch layer built entirely on top of internal/delivery/cli (V6-15B's
// own shared framework) and internal/app/runtime's own already-real,
// already-tested queries.go (V6-07B: ListEvidenceForWorkItem/GetEvidence/
// ListArtifactsForEvidence/ResolveEvidenceArtifactContent/
// GetContextSnapshot) — never a second implementation of any of them.
//
// This package owns three separate command trees (`evidence`,
// `context-snapshot`, `artifact`), each registering its own cli.Descriptor
// via this package's own init() (registrations live next to each
// subcommand's own file: list.go, verify.go, contextsnapshot.go,
// artifact.go) into the shared internal/delivery/cli registry — mirroring
// internal/delivery/cli/catalog's own identical "one package, several
// command trees" shape (that package owns `project`/`repository`/
// `component`/`pack-assignment`). This package never touches
// cmd/aw/main.go or cmd/aw/cli.go itself (the CRITICAL scope rule this
// task's own brief names: routing real os.Args to this leaf is explicitly
// deferred to a future V6-15O, "chỉ V6-15O compose CLI/parity registry").
//
// Every DTO this package's own JSON output ever carries is either
// internal/app/runtime's own already-bounded query-result type, reused
// VERBATIM (EvidenceDetail/ArtifactSummary/ContextSnapshotDetail — every
// one of them already has NO Locator/filesystem-path field by
// construction, exactly like internal/delivery/httpapi/evidence's own
// listEvidenceResponse/listArtifactsResponse already reuse them for HTTP),
// or a small new type this package itself defines for a genuinely new
// shape those queries don't already produce (VerifyResult/
// ArtifactVerification below, verify.go — the one aggregate "verify a whole
// Evidence row" orchestration no existing function performs). Either way,
// dto_scan_test.go proves by reflection, not just code review, that no
// field name across either set ever looks like a locator/filesystem path/
// secret/process-identity leak.
//
// `evidence verify` is this package's own one CLI_LOCAL leaf (see verify.go
// for why: it needs a real ports.ArtifactStore.Verify call, which — like
// `artifact get`'s own content streaming — is real I/O internal/app/
// runtime/queries.go deliberately never performs itself; there is
// correspondingly no HTTP route for it either, internal/delivery/httpapi/
// evidence/routes.go's own five-route inventory has no "verify" entry at
// all). `evidence list`, `context-snapshot show`, and `artifact get` are
// ordinary leaves with real HTTP twins (listEvidence/getContextSnapshot/
// getArtifactContent) and register those exact operationIds.
package evidence

import (
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Dependencies is everything this package's own subcommands need — the
// exact same pair internal/delivery/httpapi/evidence.Dependencies already
// carries (that package's own dependencies.go:13-22, which this task's own
// brief names directly as "the exact pair your own CLI Dependencies struct
// should carry"): a real ports.UnitOfWork every query dispatches through,
// and a real ports.ArtifactStore for the two leaves that touch real
// content bytes (`artifact get`'s own stream, `evidence verify`'s own
// per-artifact re-hash). No idsource.Source/clock.Clock/principal is
// needed: every command in this package is read-only and mints no new ID,
// timestamp, or mutation of its own — even `evidence verify`, despite being
// CLI_LOCAL, never writes anything; it only re-reads and re-hashes what is
// already durably stored.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every subcommand in this
	// package dispatches through.
	UnitOfWork ports.UnitOfWork
	// ArtifactStore is the real, composition-root-owned content-addressed
	// store `artifact get` and `evidence verify` open/verify against — the
	// SAME store cmd/aw/serve.go already constructs for
	// internal/delivery/httpapi/evidence, never a second one rooted
	// elsewhere.
	ArtifactStore ports.ArtifactStore
}
