// Package workitemblocker is V6-15G's own second CLI leaf
// (docs/design/08-v6-api-projections.md:713-721): "manage WorkItem
// readiness/lifecycle and blocker resolution via named commands" — the
// blocker half of that task, `aw blocker resolve <blockerId>`. Named
// distinctly from its sibling internal/delivery/cli/workitem (rather than
// living inside it, or being named plain "blocker") because it wraps a
// different HTTP surface entirely: internal/delivery/httpapi/recovery
// (resolveblocker.go), not internal/delivery/httpapi/workitem — the same
// "own package, own descriptor/schema fragment and test" split
// internal/delivery/cli/noderun already establishes as this codebase's own
// precedent for a leaf whose command tree is grouped with a sibling by
// resource-name convention but whose underlying HTTP package differs.
//
// This package is a thin dispatch layer over internal/delivery/cli's own
// shared framework (V6-15B) and internal/app/runtime.ResolveWorkItemBlocker
// (resolve_work_item_blocker.go) — the one public command with authority to
// transition a WorkItemBlocker OPEN -> RESOLVED|WAIVED. Read that function's
// own package doc comment in full (dense and load-bearing, per this task's
// own explicit instruction) before touching resolve.go: it documents the
// complete resolution-mode x blocker-type authority matrix this leaf's own
// flags surface, never re-decide.
//
// Deliberately NOT cli.BuildEnvelope/cli.Dispatch — no --idempotency-key,
// no --expected-version/--project-id flag exists on this subcommand at all,
// mirroring internal/delivery/cli/run/cancel.go's own identical choice for
// the identical class of reason (see resolve.go's own doc comment):
// runtime.ResolveWorkItemBlocker takes a plain
// ResolveWorkItemBlockerRequest{BlockerID, Mode, Actor, Reason,
// PolicyGrantRef, CorrelationID} — no ports.Command, no IdempotencyKey
// field anywhere on it. It is idempotent BY BLOCKERID: a blocker already
// RESOLVED/WAIVED (not OPEN) replays as AlreadyResolved, never a second
// decision, never an error.
//
// This package's own init() (workitemblocker.go) registers its one
// cli.Descriptor into the shared internal/delivery/cli registry — this
// package never touches cmd/aw/main.go or cmd/aw/cli.go itself (the
// CRITICAL scope rule this task's own brief names: routing real os.Args to
// this leaf is explicitly deferred to a future V6-15O).
//
// No subcommand in this package binds --actor or --role: ADR-028 permits
// exactly one mechanism to select the acting principal
// (cli.BindPrincipalFlag's own --principal-config) — see
// workitemblocker_test.go's own
// TestBlockerCommandsNeverDefineActorOrRoleFlag.
//
// --mode has no default (ADR-020's own "Payload MUST chọn resolution mode
// tường minh, không có mặc định"): this leaf's own --mode flag is required
// and rejects any value other than "RESOLVED"/"WAIVED" client-side, in
// addition to the application layer's own ErrResolutionModeRequired
// enforcing the identical rule — belt and suspenders, never a silent
// default either layer could introduce.
package workitemblocker
