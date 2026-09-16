package run

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"run", "start"}, Scope: cli.ScopeProject,
		AppOperation: "StartWorkflowRun", HTTPOperationID: "startWorkflowRun",
	})
}

// startRunHashPayload mirrors internal/delivery/httpapi/run's own
// unexported startRunHashPayload (start.go there) byte-for-byte — same
// field names, same json tags. Folding WorkItemID into the semantic hash is
// load-bearing there, not decorative (see that type's own doc comment), and
// this package must reproduce the EXACT same canonical bytes for an
// equivalent request: cli.BuildEnvelope's own doc comment is explicit that
// "a receipt written by an HTTP call and a receipt written by a CLI call
// for equivalent requests always hash identically" — that guarantee only
// holds if both delivery mechanisms canonicalize the identical shape.
type startRunHashPayload struct {
	WorkItemID        string `json:"workItemId"`
	WorkflowVersionID string `json:"workflowVersionId"`
}

// StartResult is `run start`'s own JSON result payload (nested inside
// cli.ResultEnvelope.Result): runtime.StartWorkflowRunResult verbatim, plus
// the final observed runtime.RunDetail when --wait was set and a terminal
// state was reached before any timeout/interrupt.
type StartResult struct {
	runtime.StartWorkflowRunResult
	// Wait is populated only when --wait was set and cli.Wait returned a
	// terminal observation — nil on a --wait timeout/interrupt (see
	// Start's own doc comment on why those cases still leave Wait nil
	// rather than a synthesized value).
	Wait *runtime.RunDetail `json:"wait,omitempty"`
}

// Start implements `aw run start <workItemId>`: the full
// cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow (Idempotency-Key
// required-or-generated, semantic hash, receipt replay) over
// runtime.StartWorkflowRun — mirroring internal/delivery/httpapi/run's own
// StartWorkflowRunHandler exactly (start.go there), reusing the identical
// hash-payload shape (startRunHashPayload) and the identical
// "reload the WorkItem for its own authoritative ProjectID" discipline
// (loadWorkItemProjectID, shared.go).
//
// --wait polls runtime.GetRunDetail — a pure read query — until the Run
// reaches a terminal state (SUCCEEDED/FAILED/CANCELLED) or --wait-timeout
// elapses/the context is cancelled (an operator's own Ctrl-C). This is
// exactly cli.Wait's own "observe-only, never executes/cancels" contract:
// the ObserveFunc below calls nothing but runtime.GetRunDetail, so a
// timed-out or interrupted --wait can never itself call runtime.CancelRun
// or any other mutating function — see wait_test.go for the explicit proof.
// A --wait outcome (success, timeout, or interrupt) never changes Start's
// own already-committed result: the JSON result envelope is always written
// to stdout once the start itself has succeeded (fresh or replayed),
// regardless of what --wait subsequently observes.
func Start(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	workflowVersionID := fs.String("workflow-version-id", "", "workflow version id to start (required)")
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	wait, waitTimeout := cli.BindWaitFlags(fs)
	_ = cli.BindJSONFlag(fs) // reserved for a future output-mode switch; this leaf always emits JSON today.
	if err := fs.Parse(args); err != nil {
		return err
	}

	workItemID := fs.Arg(0)
	if strings.TrimSpace(workItemID) == "" {
		return errors.New("cli: run start: <workItemId> argument is required")
	}
	if strings.TrimSpace(*workflowVersionID) == "" {
		return errors.New("cli: run start: --workflow-version-id is required")
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	projectID, err := loadWorkItemProjectID(ctx, deps.UOW, workItemID)
	if err != nil {
		return err
	}
	scope := ports.ProjectScope(projectID)

	normalizedPayload, err := json.Marshal(startRunHashPayload{WorkItemID: workItemID, WorkflowVersionID: *workflowVersionID})
	if err != nil {
		return fmt.Errorf("cli: run start: marshal hash payload: %w", err)
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: "StartWorkflowRun", Scope: scope,
		NormalizedPayload: normalizedPayload, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return runtime.StartWorkflowRun(ctx, deps.UOW, deps.IDs, envelope.Command, runtime.StartWorkflowRunRequest{
			ProjectID: projectID, WorkItemID: workItemID, WorkflowVersionID: *workflowVersionID,
		})
	})
	if err != nil {
		return err
	}

	startResult, err := decodeStartResult(dispatched.Result)
	if err != nil {
		return err
	}

	result := StartResult{StartWorkflowRunResult: startResult}
	var waitErr error
	if *wait {
		observed, err := cli.Wait(ctx, func(ctx context.Context) (any, bool, error) {
			detail, err := runtime.GetRunDetail(ctx, deps.UOW, startResult.RunID)
			if err != nil {
				return nil, false, err
			}
			return detail, isTerminalRunState(detail.State), nil
		}, cli.WaitOptions{Timeout: *waitTimeout, Sleep: deps.Sleep})
		switch {
		case err != nil:
			waitErr = err
			cli.Diagnosticf(stderr, "run %s: --wait did not observe a terminal state: %v", startResult.RunID, err)
		default:
			if detail, ok := observed.(runtime.RunDetail); ok {
				result.Wait = &detail
			}
		}
	}

	if encErr := cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: result,
	}); encErr != nil {
		return encErr
	}
	return waitErr
}

// decodeStartResult normalizes cli.Dispatch's own two possible shapes for
// DispatchResult.Result: a fresh execution returns the typed
// runtime.StartWorkflowRunResult verbatim (Execute's own return value,
// unwrapped by Dispatch), while a replay returns a json.RawMessage decoded
// from the stored receipt (Dispatch's own doc comment) — both must resolve
// to the identical typed result here.
func decodeStartResult(raw any) (runtime.StartWorkflowRunResult, error) {
	switch v := raw.(type) {
	case runtime.StartWorkflowRunResult:
		return v, nil
	case json.RawMessage:
		var result runtime.StartWorkflowRunResult
		if err := json.Unmarshal(v, &result); err != nil {
			return runtime.StartWorkflowRunResult{}, fmt.Errorf("cli: run start: decode replayed result: %w", err)
		}
		return result, nil
	default:
		return runtime.StartWorkflowRunResult{}, fmt.Errorf("cli: run start: unexpected dispatch result type %T", raw)
	}
}

// isTerminalRunState reports whether state is one of the three states past
// which a WorkflowRun will never again change on its own — the terminal
// set `run start --wait` polls for. This is deliberately a wider set than
// ErrRunAlreadyTerminal's own SUCCEEDED/FAILED pair (cancel_run.go: "chỉ
// PASS và FAIL làm Run terminal" is ADR-020's own no-op-cancel rule, a
// narrower concern) — CANCELLED is also a state nothing further will ever
// happen to, so a --wait that stopped polling only at SUCCEEDED/FAILED
// would hang forever on a cancelled run.
func isTerminalRunState(state string) bool {
	switch runtimedomain.WorkflowRunState(state) {
	case runtimedomain.WorkflowRunSucceeded, runtimedomain.WorkflowRunFailed, runtimedomain.WorkflowRunCancelled:
		return true
	default:
		return false
	}
}
