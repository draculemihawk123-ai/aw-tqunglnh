// Package definitions is V6-15E's own CLI leaf
// (docs/design/08-v6-api-projections.md V6-15E: "author/validate/publish/
// inspect immutable definitions từ terminal") — the exact same
// internal/app/definitions application authority
// internal/delivery/httpapi/definitions already wraps for HTTP
// (CreateDefinition/ValidateDraft/PublishDefinitionVersion/GetDefinition/
// ListVersions/LoadAnyVersion and, new in this task, ListDefinitions —
// see that package's own queries.go doc comment), reshaped into
// `aw definition ...`/`aw version ...` subcommands on top of
// internal/delivery/cli's own shared framework (V6-15B), mirroring
// internal/delivery/cli/catalog's own established shape (V6-15D, this
// task's own closest, already-merged sibling). Named "definitions"
// (plural), not "definition", for the identical reason
// internal/delivery/httpapi/definitions already is: this package imports
// internal/domain/definition (singular) unaliased throughout, and a
// package cannot share its own name with an import it never aliases.
//
// This package owns its own Descriptor registrations (registered into
// cli.Default from its own init(), below) and its own tests — per the
// design doc's own §1 rule 8 ("Parallel work không sửa registry chung. Mỗi
// endpoint/CLI task sở hữu subpackage, descriptor/schema fragment và test
// riêng ... Chỉ V6-15O compose CLI/parity registry"). It deliberately
// never touches cmd/aw/main.go or cmd/aw/cli.go: routing real os.Args to
// this leaf's own Run* functions is V6-15O's own future composition-root
// job, not this task's — this package's own tests instead call those Run*
// functions directly, exactly like clicatalog's own tests already do.
//
// Every mutating Run* function (RunDefinitionCreate, RunDefinitionPublish)
// follows the identical flow internal/delivery/cli's own doc.go describes
// and internal/delivery/httpapi/definitions's own handlers already
// established for HTTP: resolve the acting principal
// (config.LoadLocalPrincipalFile + config.ValidateLocalPrincipal, the same
// mechanism catalog's own loadPrincipal uses) → derive THIS invocation's
// own scope from its flags (--project-id present means project-scoped,
// absent means global — the CLI's own equivalent of HTTP's path-derived
// scope: global route prefix vs a real {projectId}, never a body field)
// → for publish (and validate, which authoritatively reloads but never
// dispatches — see validate.go's own doc comment), reload the authoritative
// Definition first and confirm it actually belongs to the derived scope
// (loadDefinitionInScope/loadVersionInScope, helpers.go — the same
// leakage-normalized "does not exist" vs "exists in a different scope"
// indistinguishability internal/delivery/httpapi/definitions/
// authoritative.go already establishes) → build the command envelope via
// cli.BuildEnvelope → cli.Dispatch (which reuses
// internal/delivery/httpapi's own SemanticHash/LookupReceipt/
// ReconcileReceipt, so a receipt written by an HTTP call and one written
// by a CLI call for an equivalent request are checked against the exact
// same replay authority) → cli.EncodeCommandResult. Every read-only Run*
// function calls the matching internal/app/definitions query directly and
// writes its own typed view (views.go) via cli.EncodeQueryResult, except
// `aw version diff`, whose result (internal/app/definitions.VersionDiff)
// already carries its own stable, camelCase-tagged JSON shape (promoted by
// this same task from internal/delivery/httpapi/definitions/diff.go — see
// that package's own diff.go doc comment) and needs no separate view
// wrapper.
//
// Per-Kind document dispatch (dispatch.go's compileClosure/
// workflowRequestFrom/buildCandidate) is this package's own THIRD
// independent copy of the identical shape cmd/aw/definition.go's own
// compileClosureForKind (V2-11, pre-framework, NOT this task's precedent —
// see this task's own brief for why) and
// internal/delivery/httpapi/definitions/dispatch.go's own compileClosure
// (V6-05, the actual precedent this package follows) already carry — kept
// as an independent copy rather than shared for the identical reason
// internal/app/definitions/commands.go's own
// workflowVersionToVersionFields doc comment already gives for its own
// triplicated conversion: each of the nine kinds' own concrete ID/Version
// types are exactly what keep a document authored for one kind from ever
// being silently accepted as another's, and this package must never
// import internal/delivery/httpapi/definitions (a sibling delivery
// package, not a shared dependency) to reuse its unexported dispatch.
//
// aw definition list (RunDefinitionList) is this package's own genuine
// addition below the CLI layer: no HTTP route or application query
// answered "which Definitions of this Kind exist in this Scope" before
// this task (confirmed by reading internal/delivery/httpapi/definitions/
// routes.go's own 14-route inventory and internal/app/ports.
// DefinitionsRepository's full method set) — every existing caller already
// needs to know a specific ID first. queries.go's own ListDefinitions doc
// comment has the full reasoning; its Descriptor here is registered with
// cli.CLILocalOperation (no HTTP operationId exists to mirror), the same
// sentinel `aw evidence verify` already uses for a leaf with no HTTP
// counterpart.
package definitions
