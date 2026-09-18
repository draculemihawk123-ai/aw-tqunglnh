// Package projection is V6-15N's own CLI leaf, Part 1
// (docs/design/08-v6-api-projections.md:783-791: "observe/rebuild a
// projection ... from the terminal"): `aw projection status|rebuild|
// rebuild-status` — the terminal-side mirror of
// GET/POST /projects/{id}/projection[...] (internal/delivery/httpapi/
// projectionrebuild, V6-09B, merged), built entirely on the already-hardened
// internal/app/projectionrebuild application layer (GetProjectionStatus,
// RequestProjectionRebuild, GetProjectionRebuildStatus, V6-09) — never a
// second implementation of any of them.
//
// "Không làm: no cursor/row mutation, no latest-operation inference" (this
// task's own scope line, restated from internal/app/projectionrebuild's own
// doc comments): this package never touches projection_rows/
// projection_checkpoints directly (it only ever calls
// appprojectionrebuild.GetProjectionStatus, itself a read-only wrapper over
// two CAS-protected reads), and `projection rebuild-status` takes a
// mandatory, exact <operationId> positional argument — there is no "show me
// the latest rebuild for this project/projection" flag or fallback anywhere
// in this package, mirroring GetProjectionRebuildStatus's own doc comment
// ("it never infers 'the latest' operation") exactly.
//
// Every command here is project-scoped (`--project-id`), matching
// internal/delivery/httpapi/projectionrebuild's own three routes, all
// nested under /projects/{id}/... — except `rebuild-status`, whose own
// underlying query (GetProjectionRebuildStatus) takes no scope parameter at
// all: it is a plain, exact, by-operationId lookup, and the loaded
// operation's own ProjectID field IS its authoritative scope (mirrors
// internal/delivery/httpapi/projectionrebuild/operation_status.go's own doc
// comment: "the by-ID-loaded record's own ProjectID field IS this route's
// authoritative reload/authorize step, not a second reload of the
// Project"). This package's own `rebuild-status` command therefore binds no
// --project-id flag either — an operator supplies only the OperationID an
// earlier `rebuild` call (or a rebuild worker's own log) already handed
// them. Descriptor.Scope is still registered as cli.ScopeProject (matching
// getProjectionRebuildOperationStatus's own real HTTPOperationID scope, for
// ADR-028's eventual four-column parity inventory), even though the actual
// invocation binds no --project-id flag — see descriptor.go.
//
// This package never wires itself into cmd/aw (V6-15O's own job — "chỉ
// V6-15O compose CLI/parity registry", docs/design/08-v6-api-projections.md
// §1 rule 8): it only registers its own cli.Descriptor(s) into cli.Default
// from its own init(), and exposes RunStatus/RunRebuild/RunRebuildStatus for
// a future composition root to call once that wiring exists.
package projection
