package workitem

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// CancelResult is `work-item cancel`'s own JSON result — a camelCase-JSON
// projection of runtime.CancelWorkItemResult (which carries no json tags of
// its own), mirroring internal/delivery/httpapi/recovery's own
// CancelWorkItemResponse mapping discipline (cancelworkitem.go there)
// exactly.
type CancelResult struct {
	WorkItemID string `json:"workItemId"`
	// AlreadyRequested: a second cancel call for the same WorkItem —
	// idempotent by WorkItemID via its own durable
	// WorkItemCancellationIntent, never a second intent, never a second
	// round of Run-quiescing.
	AlreadyRequested bool `json:"alreadyRequested"`
	// Status is the WorkItem's own real current Status this call actually
	// observed — CANCELLED when every one of its Runs was already terminal
	// (or it had none), still whatever it was before otherwise (a WorkItem
	// with at least one still-quiescing Run is NOT forced into any
	// intermediate status by this call — see
	// internal/app/runtime/cancel_work_item.go's own package doc comment).
	Status string `json:"status"`
}

// RunWorkItemCancel implements `aw work-item cancel <workItemId> --reason <text>`.
//
// Deliberately NOT cli.BuildEnvelope/cli.Dispatch — no --idempotency-key,
// no --expected-version flag exists on this subcommand at all, and no
// --project-id flag either. This mirrors
// internal/delivery/cli/run/cancel.go's own Cancel exactly, for the
// identical reason spelled out there at length (re-read in full before
// writing this file, per this task's own explicit instruction) and
// internal/delivery/httpapi/recovery/cancelworkitem.go's own
// CancelWorkItemHTTPHandler: runtime.CancelWorkItem (cancel_work_item.go)
// takes a plain CancelWorkItemRequest{WorkItemID, Actor, Reason,
// CorrelationID} — no ports.Command, no IdempotencyKey field, no
// ExpectedVersion field, no ProjectID field anywhere on it. It is
// idempotent BY WORKITEMID, structurally: a WorkItem has exactly one
// durable WorkItemCancellationIntent ever, so a second
// `aw work-item cancel <id>` call for the same WorkItem (a deliberate retry
// after a network blip, or a genuine concurrent duplicate) finds it already
// recorded and safely returns AlreadyRequested=true — never a second
// intent. Binding --idempotency-key/--expected-version/--project-id here
// would validate flags this command could never actually consume
// (runtime.CancelWorkItem writes no command receipt at all and has no
// project-scoped precondition to check against a caller-supplied value),
// misrepresenting the real contract rather than honoring it.
//
// For the identical reason, this writes its result via
// cli.EncodeQueryResult rather than cli.EncodeCommandResult/
// cli.ResultEnvelope: this command has structurally no idempotency key to
// report.
func RunWorkItemCancel(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	reason := fs.String("reason", "", "reason for cancelling this work item (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	workItemID := fs.Arg(0)
	if strings.TrimSpace(workItemID) == "" {
		return errors.New("cli: work-item cancel: <workItemId> argument is required")
	}
	if strings.TrimSpace(*reason) == "" {
		return errors.New("cli: work-item cancel: --reason is required")
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	// CorrelationID is a fresh tracing id per invocation — never
	// idempotency-relevant for this command (see this function's own doc
	// comment), mirroring run.Cancel's own identical choice.
	result, err := runtime.CancelWorkItem(ctx, deps.UoW, deps.IDs, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: principal.Actor, Reason: *reason, CorrelationID: deps.IDs.NewID(),
	})
	if err != nil {
		return err
	}

	return cli.EncodeQueryResult(stdout, CancelResult{
		WorkItemID: result.WorkItemID, AlreadyRequested: result.AlreadyRequested, Status: result.Status,
	})
}
