// Package workitem is V6-15G's own CLI leaf
// (docs/design/08-v6-api-projections.md:713-721): "manage WorkItem
// readiness/lifecycle and blocker resolution via named commands" — the
// WorkItem half of that task (`aw work-item list|show|create|create-child|
// readiness|mark-ready|cancel`); the blocker half
// (`aw blocker resolve`) is its own sibling package,
// internal/delivery/cli/workitemblocker, kept separate because it wraps a
// different HTTP surface entirely (see that package's own doc comment).
//
// This package is a thin dispatch layer over internal/delivery/cli's own
// shared framework (V6-15B) and two already-real, already-tested
// application packages:
//
//   - internal/app/work (commands.go's CreateRootWorkItem/CreateChildWorkItem,
//     queries.go's GetWorkItem/ListWorkItems/ExplainWorkItemReadiness,
//     mark_ready.go's MarkWorkItemReady) — mirrored here the same way
//     internal/delivery/httpapi/workitem already wraps them for HTTP
//     (routes.go there lists the full inventory this package's own
//     list/show/create/create-child/readiness/mark-ready subcommands mirror
//     one for one).
//   - internal/app/runtime.CancelWorkItem — mirrored here the same way
//     internal/delivery/httpapi/recovery already wraps it for HTTP
//     (cancelworkitem.go there), NOT internal/delivery/httpapi/workitem: the
//     WorkItem-level cancel command lives in a different application package
//     (runtime, not work) and a different HTTP package (recovery, not
//     workitem) than every other subcommand in this file, but it stays in
//     THIS CLI package because the spec's own command surface groups it
//     under "work-item ..." regardless of which HTTP package wraps it.
//
// Each subcommand registers its own cli.Descriptor via this package's own
// init() (workitem.go) into the shared internal/delivery/cli registry —
// this package never touches cmd/aw/main.go or cmd/aw/cli.go itself (the
// CRITICAL scope rule this task's own brief names: routing real os.Args to
// this leaf is explicitly deferred to a future V6-15O, "chỉ V6-15O compose
// CLI/parity registry").
//
// Two deliberately different dispatch shapes coexist in this one package,
// mirroring the two different HTTP packages above exactly:
//
//   - create/create-child/mark-ready follow the full
//     cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow (Idempotency-Key,
//     semantic hash, receipt replay) — mark-ready additionally requires
//     --expected-version (the CLI equivalent of HTTP's strong If-Match,
//     cli.BindExpectedVersionFlag's own doc comment), since MarkWorkItemReady
//     is an UPDATE-shaped mutation on an already-existing WorkItem, not a
//     create.
//   - cancel is deliberately NOT cli.BuildEnvelope/cli.Dispatch — no
//     --idempotency-key, no --expected-version flag exists on this
//     subcommand at all, mirroring internal/delivery/cli/run/cancel.go's own
//     identical choice for the identical reason (re-read that file's own doc
//     comment in full before touching cancel.go here): runtime.CancelWorkItem
//     (cancel_work_item.go) takes a plain
//     CancelWorkItemRequest{WorkItemID, Actor, Reason, CorrelationID} — no
//     ports.Command, no IdempotencyKey field, no ExpectedVersion field
//     anywhere on it. It is idempotent BY WORKITEMID, structurally: a
//     WorkItem has exactly one durable WorkItemCancellationIntent ever, so a
//     second `aw work-item cancel <id>` call for the same WorkItem safely
//     returns AlreadyRequested=true — never a second intent. Binding
//     --idempotency-key/--expected-version here would validate flags this
//     command could never actually consume, misrepresenting the real
//     contract rather than honoring it.
//
// No subcommand in this package binds --actor or --role: ADR-028 permits
// exactly one mechanism to select the acting principal
// (cli.BindPrincipalFlag's own --principal-config) — see workitem_test.go's
// own TestWorkItemCommandsNeverDefineActorOrRoleFlag.
//
// list/show/readiness are pure reads over internal/app/work's own
// AUTHORITATIVE (non-projected) queries — never anything from V6-10's
// separate projected Kanban/detail. readiness in particular is this task's
// own "recompute fresh, never trust a projection" primitive: it dispatches
// straight to workapp.ExplainWorkItemReadiness, which itself reloads the
// real, current WorkItem and re-runs the real workdomain.ValidateReadinessGate
// validator every single call — there is no projection table, cache or
// stored-status shortcut anywhere in this package's own readiness.go for a
// future edit to accidentally introduce.
//
// This package's own "Không làm" line (this task's own brief) is satisfied
// by construction: there is no generic "set-status"/"set-state" subcommand
// anywhere in this file — mark-ready is the one single narrow BACKLOG->READY
// transition (ADR-028 §30: "Command không nhận target status" — mirrored
// here by MarkWorkItemReadyRequest carrying only a WorkItemID, nothing this
// leaf's own flags could even attempt to name a different transition with),
// and cancel/readiness never accept or derive authorization from anything
// but a freshly reloaded real row.
package workitem
