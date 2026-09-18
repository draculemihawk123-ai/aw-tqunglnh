// Package scopeexpansion is V6-15I's own CLI leaf package for the
// TaskFamily scope-expansion decision surface
// (docs/design/08-v6-api-projections.md:733-741): `aw scope-expansion
// request|approve|reject|withdraw`, a thin CLI mirror of
// internal/delivery/httpapi/workitem's own scope_expansion_commands.go
// (V6-06A) over the exact same four internal/app/work application commands
// (RequestScopeExpansion/ApproveScopeExpansion/RejectScopeExpansion/
// WithdrawScopeExpansion) — never a second implementation of any of the
// business rules those commands already own.
//
// Every mutation in this package resolves its own acting principal
// exclusively via --principal-config (cli.BindPrincipalFlag) — there is no
// --actor/--role flag anywhere in this package (ADR-028; see
// flags_test.go's own TestScopeExpansionCommandsNeverDefineActorOrRoleFlag).
//
// Reload-first discipline: every command in this package reloads its own
// authoritative target (the TaskFamily for `request`, the
// ScopeExpansionRequest for approve/reject/withdraw) via the same
// internal/app/work read queries (GetTaskFamily/GetScopeExpansionRequest)
// BEFORE Idempotency-Key/body are even read — mirroring
// scope_expansion_commands.go's own identical discipline exactly, both for
// deriving this invocation's own ports.CommandScope (a family/request
// belongs to exactly one project, named by --project-id, never trusted
// bare) and, for approve/reject/withdraw, for the current Version an
// operator's own --expected-version must match (the CLI equivalent of
// HTTP's strong If-Match precondition — see approve.go/reject.go/
// withdraw.go's own doc comments for why that comparison happens INSIDE
// the cli.Dispatch execute closure rather than before it: a genuine
// receipt replay must always win over an apparently-stale version, exactly
// like ResolveApprovalHandler's own documented behavior).
//
// `scope-expansion request` is deliberately CREATE-shaped (no
// --expected-version at all, mirroring handleRequestScopeExpansion's own
// prepareCreateCommand) — a brand-new ScopeExpansionRequest has no prior
// version for a precondition to protect.
package scopeexpansion
