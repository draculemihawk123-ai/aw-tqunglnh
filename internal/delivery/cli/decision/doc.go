// Package decision is V6-15I's own CLI leaf package for the runtime human-
// decision surface (docs/design/08-v6-api-projections.md:733-741): `aw
// approval resolve` and `aw wait signal` — a thin CLI mirror of
// internal/delivery/httpapi/decision (V6-06A) over the exact same two
// internal/app/runtime application commands (ResolveApproval/SignalWait) —
// never a second implementation of either command's own business rules.
//
// Naming note (this task's own explicit collision warning): this package's
// own WAIT-signal command is named SignalWait, matching
// runtime.SignalWait's own name — deliberately NOT a function literally
// called Wait, which would be confusable with cli.Wait
// (internal/delivery/cli/wait.go), an entirely unrelated, already-existing
// generic polling primitive (cli.Wait/cli.WaitOptions/cli.Sleeper,
// V6-15B's own `--wait` flag support). decision.SignalWait never calls, and
// has nothing to do with, cli.Wait.
//
// Both commands in this package follow the identical reload-first
// discipline internal/delivery/httpapi/decision's own package doc comment
// establishes: the target (ApprovalRequest for `approval resolve`,
// WaitRegistration for `wait signal`) is reloaded via a direct
// uow.WithReadOnly + tx.Approvals()/tx.Wait() read — there is no scoped
// internal/app/runtime query function for either (confirmed by reading
// that package's own queries.go before writing this package) — BEFORE
// Idempotency-Key/flags are even read, both to derive this invocation's
// own ports.CommandScope (from the reloaded row's own ProjectID) and to
// catch a stale/cross-run target early, exactly like
// internal/delivery/httpapi/decision's own loadApprovalRequestForUpdate/
// loadWaitRegistrationForRun.
//
// The two commands deliberately DIVERGE on --expected-version, mirroring
// internal/delivery/httpapi/decision's own approval.go/wait.go split
// exactly: `approval resolve` requires it (a human operator has
// necessarily just reloaded the ApprovalRequest through some UI/CLI `show`
// first); `wait signal` does not (a WAIT signal's caller is typically an
// external system — e.g. a CI webhook — identified by --signal-key, not a
// human who just reloaded a page, and SignalWait's own idempotent-
// duplicate-delivery contract must keep working even when a retried
// delivery has no way of knowing the registration's current version).
//
// Every mutation in this package resolves its own acting principal
// exclusively via --principal-config (cli.BindPrincipalFlag) — there is no
// --actor/--role flag anywhere in this package (ADR-028; see
// flags_test.go's own TestDecisionCommandsNeverDefineActorOrRoleFlag).
package decision
