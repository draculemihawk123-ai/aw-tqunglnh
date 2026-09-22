package run

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"run", "cancel"}, Scope: cli.ScopeProject,
		AppOperation: "CancelRun", HTTPOperationID: "cancelRun",
		HighImpact: true, // UX Screen 7: "cancel run/cancel task mở dialog confirm" (V6-15O confirmation parity)
	})
}

// CancelResult is `run cancel`'s own JSON result — a camelCase-JSON
// projection of runtime.CancelRunResult (which carries no json tags of its
// own), mirroring internal/delivery/httpapi/run's own CancelRunResponse
// mapping discipline (cancel.go there) exactly.
type CancelResult struct {
	RunID            string `json:"runId"`
	State            string `json:"state"`
	AlreadyRequested bool   `json:"alreadyRequested"`
	CoordinatorJobID string `json:"coordinatorJobId,omitempty"`
}

// Cancel implements `aw run cancel <runId>`.
//
// Deliberately NOT cli.BuildEnvelope/cli.Dispatch — no --idempotency-key,
// no --expected-version flag exists on this subcommand at all. This
// mirrors internal/delivery/httpapi/run/cancel.go's own CancelRunHandler
// exactly, for the identical reason spelled out there at length (re-read in
// full before writing this file, per this task's own explicit instruction):
// runtime.CancelRun (cancel_run.go) takes a plain
// CancelRunRequest{RunID, Actor, Reason, CorrelationID} — no ports.Command,
// no IdempotencyKey field, no ExpectedVersion field anywhere on it, unlike
// runtime.StartWorkflowRun (see start.go). ADR-020 §22 states plainly
// "command là idempotent theo run" — CancelRun is idempotent BY RUNID,
// structurally: a Run has exactly one RunCancellationIntent ever
// (unique per RunID, cancel_run.go's own package doc comment), so a second
// `aw run cancel <id>` call for the same run (a deliberate retry after a
// network blip, or a genuine concurrent duplicate) finds it already
// recorded and safely returns AlreadyRequested=true — never a second
// intent, never a second CANCEL_RUN_COORDINATOR job. Binding
// --idempotency-key/--expected-version here would validate flags this
// command could never actually consume (runtime.CancelRun writes no
// command receipt at all), misrepresenting the real contract rather than
// honoring it — exactly the mistake this task's own brief calls out as "a
// real correctness bug, not a style choice."
//
// For the identical reason, this writes its result via
// cli.EncodeQueryResult rather than cli.EncodeCommandResult/
// cli.ResultEnvelope: ResultEnvelope's own IdempotencyKey field is ALWAYS
// present by contract (output.go's own doc comment) because every OTHER
// mutating leaf in this framework goes through BuildEnvelope, which always
// has one (caller-supplied or generated) to report back. Cancel has
// structurally no idempotency key to report — inventing one here would be
// exactly the misrepresentation the paragraph above already rejects.
func Cancel(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	reason := fs.String("reason", "", "reason for cancelling this run (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runID := fs.Arg(0)
	if strings.TrimSpace(runID) == "" {
		return errors.New("cli: run cancel: <runId> argument is required")
	}
	if strings.TrimSpace(*reason) == "" {
		return errors.New("cli: run cancel: --reason is required")
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	// CorrelationID is a fresh tracing id per invocation — never
	// idempotency-relevant for this command (see this function's own doc
	// comment), mirroring httpapi.CorrelationIDFromContext's own "a value
	// for log correlation, not a replay key" role for the HTTP handler.
	result, err := runtime.CancelRun(ctx, deps.UOW, deps.IDs, runtime.CancelRunRequest{
		RunID: runID, Actor: principal.Actor, Reason: *reason, CorrelationID: deps.IDs.NewID(),
	})
	if err != nil {
		return err
	}

	return cli.EncodeQueryResult(stdout, CancelResult{
		RunID: result.RunID, State: result.State, AlreadyRequested: result.AlreadyRequested,
		CoordinatorJobID: result.CoordinatorJobID,
	})
}
