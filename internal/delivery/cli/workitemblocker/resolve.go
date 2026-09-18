package workitemblocker

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// loadPrincipal resolves --principal-config into a validated
// config.LocalPrincipal — the same two-step (LoadLocalPrincipalFile then
// ValidateLocalPrincipal) `aw serve` itself performs, duplicated locally
// rather than shared across sibling leaf-command packages (see
// internal/delivery/cli/run/shared.go's own doc comment on loadPrincipal
// for why).
func loadPrincipal(path string) (config.LocalPrincipal, error) {
	principal, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		return config.LocalPrincipal{}, err
	}
	if err := config.ValidateLocalPrincipal(principal); err != nil {
		return config.LocalPrincipal{}, err
	}
	return principal, nil
}

// ResolveResult is `blocker resolve`'s own JSON result — a camelCase-JSON
// projection of runtime.ResolveWorkItemBlockerResult (which carries no json
// tags of its own), mirroring
// internal/delivery/httpapi/recovery/resolveblocker.go's own
// ResolveWorkItemBlockerResponse mapping discipline exactly.
type ResolveResult struct {
	BlockerID string `json:"blockerId"`
	// AlreadyResolved: the blocker was no longer OPEN by the time this call
	// actually looked (already RESOLVED or WAIVED by an earlier call) — a
	// safe, idempotent no-op replay.
	AlreadyResolved bool `json:"alreadyResolved"`
	// State is the blocker's own real current State ("RESOLVED" or
	// "WAIVED") this call actually observed.
	State string `json:"state"`
	// WorkItemUnblocked reports whether closing this blocker left the
	// WorkItem with zero remaining OPEN blockers.
	WorkItemUnblocked bool `json:"workItemUnblocked"`
	// WorkItemStatus is the WorkItem's own real current Status.
	WorkItemStatus string `json:"workItemStatus"`
}

// Resolve implements
// `aw blocker resolve <blockerId> --mode <RESOLVED|WAIVED> --reason <text> [--policy-grant-ref <ref>]`.
//
// Deliberately NOT cli.BuildEnvelope/cli.Dispatch — no --idempotency-key,
// no --expected-version, no --project-id flag exists on this subcommand at
// all. This mirrors internal/delivery/cli/run/cancel.go's own Cancel
// exactly, for the identical class of reason (re-read that file's own doc
// comment before touching this one, per this package's own doc comment) and
// internal/delivery/httpapi/recovery/resolveblocker.go's own
// ResolveWorkItemBlockerHTTPHandler: runtime.ResolveWorkItemBlocker
// (resolve_work_item_blocker.go) takes a plain
// ResolveWorkItemBlockerRequest{BlockerID, Mode, Actor, Reason,
// PolicyGrantRef, CorrelationID} — no ports.Command, no IdempotencyKey
// field, no ExpectedVersion field, no ProjectID field anywhere on it. It is
// idempotent BY BLOCKERID: a blocker already RESOLVED/WAIVED is a no-op,
// idempotent success reported via AlreadyResolved, never an error. Binding
// --idempotency-key/--expected-version/--project-id here would validate
// flags this command could never actually consume, misrepresenting the
// real contract rather than honoring it.
//
// --mode has no default: it is required, and rejected client-side (before
// this leaf ever calls the application layer) unless it is exactly
// "RESOLVED" or "WAIVED" — resolve_work_item_blocker.go's own
// ErrResolutionModeRequired still enforces the identical rule at the
// application layer, so a future caller of runtime.ResolveWorkItemBlocker
// directly (bypassing this CLI validation) is never left unprotected.
// SCOPE_EXPANSION_REQUIRED blockers reject both modes unconditionally, and
// WAIVED is only ever valid for a Waivable blocker type
// (RUN_CANCELLED/COMPLETION_POLICY_FAILED) — this leaf never tries to work
// around either rule, it just surfaces the application layer's own typed
// errors (ErrBlockerNotResolvableViaCommand, ErrBlockerNotWaivable,
// ErrWaiveRequiresPolicyGrant) unwrapped.
//
// For the identical no-envelope reason, this writes its result via
// cli.EncodeQueryResult rather than cli.EncodeCommandResult/
// cli.ResultEnvelope: this command has structurally no idempotency key to
// report.
func Resolve(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("blocker resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	mode := fs.String("mode", "", "resolution mode: RESOLVED or WAIVED (required, no default)")
	reason := fs.String("reason", "", "reason for this resolution (required)")
	policyGrantRef := fs.String("policy-grant-ref", "", "policy grant reference authorizing a WAIVED resolution (required when --mode=WAIVED)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	blockerID := fs.Arg(0)
	if strings.TrimSpace(blockerID) == "" {
		return errors.New("cli: blocker resolve: <blockerId> argument is required")
	}
	if strings.TrimSpace(*reason) == "" {
		return errors.New("cli: blocker resolve: --reason is required")
	}
	resolvedMode := runtime.ResolutionMode(strings.TrimSpace(*mode))
	if resolvedMode != runtime.ResolutionModeResolved && resolvedMode != runtime.ResolutionModeWaived {
		return cli.UsageError{Err: errors.New("cli: blocker resolve: --mode is required and must be exactly RESOLVED or WAIVED (no default)")}
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	// CorrelationID is a fresh tracing id per invocation — never
	// idempotency-relevant for this command (see this function's own doc
	// comment), mirroring run.Cancel's own identical choice.
	result, err := runtime.ResolveWorkItemBlocker(ctx, deps.UoW, runtime.ResolveWorkItemBlockerRequest{
		BlockerID: blockerID, Mode: resolvedMode, Actor: principal.Actor, Reason: *reason,
		PolicyGrantRef: *policyGrantRef, CorrelationID: deps.IDs.NewID(),
	})
	if err != nil {
		return err
	}

	return cli.EncodeQueryResult(stdout, ResolveResult{
		BlockerID: result.BlockerID, AlreadyResolved: result.AlreadyResolved, State: result.State,
		WorkItemUnblocked: result.WorkItemUnblocked, WorkItemStatus: result.WorkItemStatus,
	})
}
