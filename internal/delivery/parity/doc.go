// Package parity is V6-15O's own four-way machine checker
// (docs/design/08-v6-api-projections.md V6-15O: "prove one-to-one
// authority/scope/confirmation mapping before terminal acceptance"):
//
//	UX inventory  <->  HTTP/OpenAPI descriptors  <->  public operation registry  <->  CLI descriptors
//
// checked in BOTH directions, exactly the ADR-028 inventory ("UI
// action/query <-> HTTP operationId <-> aw command <-> public application
// command/query ... thiếu hoặc trùng mapping đều fail gate").
//
// # What each party is
//
//   - UX inventory: the rows apicontract.ParseUXDoc extracts from
//     docs/design/11-v6-00-ux-artifact.md (proposed operationId, kind, the
//     RESERVED `aw` invocation shape). Proposed names are non-binding
//     (V6-00 §1); a proposal resolves to real HTTP operationIds through
//     apicontract.ResolveProposal plus this package's own reviewed
//     uxProposalRenames.
//   - HTTP/OpenAPI: the apicontract.Contract V6-12 builds from the real,
//     fully composed route registry (method, path, scopeKind per operation).
//   - CLI: cli.Descriptor records (path, scope, AppOperation, HTTP
//     operationId or the typed CLI_LOCAL sentinel, HighImpact) that every
//     leaf package registers in its own init() and internal/delivery/
//     clicompose composes.
//   - Public operation registry: registry.go — see its own doc comment for
//     what it is and why it exists as a separate, independently declared
//     party.
//
// # What it rejects (Class)
//
// missing (MISSING_CLI/MISSING_HTTP/MISSING_APP), duplicate (DUPLICATE),
// scope mismatch (SCOPE_MISMATCH), query/command kind mismatch
// (KIND_MISMATCH), a registry that disagrees with a descriptor
// (APP_MISMATCH), an internal (worker-only) operation exposed
// (INTERNAL_EXPOSED), remote Git exposure (REMOTE_GIT_EXPOSED), a CLI_LOCAL
// leaf outside the closed set serve/worker/help/version/evidence verify
// (CLI_LOCAL_NOT_ALLOWED), a confirmation mismatch (CONFIRMATION_MISMATCH),
// a UX-reserved `aw` shape the CLI does not honor (UX_LEAF_MISMATCH) and a
// descriptor with no route in the composed router (ROUTE_MISSING).
//
// Check never consults a ledger: it reports every finding. The ledger
// (Ledger) is a separate, pinned, owned list of findings a reviewer has
// accepted as known debt; Evaluate splits a Check result into acknowledged
// and NEW findings so a brand-new violation always fails the gate while a
// resolved one forces its ledger entry to be deleted — the same "pinned set,
// both directions" discipline V6-12's knownUnimplementedGaps uses.
package parity
