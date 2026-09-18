package decision

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"wait", "signal"}, Scope: cli.ScopeProject,
		AppOperation: "SignalWait", HTTPOperationID: "submitWaitSignal",
	})
}

const commandTypeSignalWait = "SignalWait"

// submitWaitSignalHashPayload mirrors
// internal/delivery/httpapi/decision/wait.go's own
// submitWaitSignalHashPayload exactly — the WaitRegistrationID is folded
// in alongside SignalKey/Payload for the identical reason
// resolveApprovalHashPayload folds in ApprovalRequestID (approval.go).
type submitWaitSignalHashPayload struct {
	WaitRegistrationID string          `json:"waitRegistrationId"`
	SignalKey          string          `json:"signalKey"`
	Payload            json.RawMessage `json:"payload,omitempty"`
}

type signalWaitFlags struct {
	principal, idempotencyKey, signalKey, payload *string
}

// bindSignalWaitFlags is SignalWait's own flag-binding step — see
// approval.go's own bindResolveApprovalFlags doc comment for why this is
// factored out (the "spoof" Verify bullet). Deliberately no
// --expected-version here at all (this package's own doc comment explains
// why `wait signal` carries no If-Match-equivalent precondition).
func bindSignalWaitFlags(fs *flag.FlagSet) signalWaitFlags {
	return signalWaitFlags{
		principal:      cli.BindPrincipalFlag(fs),
		idempotencyKey: cli.BindIdempotencyKeyFlag(fs),
		signalKey:      fs.String("signal-key", "", "caller-supplied, stable identity of the real-world external event being reported (required; deliberately distinct from --idempotency-key — see runtime.SignalWait's own doc comment)"),
		payload:        fs.String("payload", "", "optional caller-shaped JSON payload for this signal, passed through to the WAIT node verbatim"),
	}
}

// SignalWait implements `aw wait signal <runId> <waitRegistrationId>
// --signal-key <key> [--payload <json>] [--idempotency-key <key>]` — the
// sole CLI entry point an external signal delivery goes through,
// dispatching runtime.SignalWait directly (the SAME function
// internal/delivery/httpapi/decision's own SubmitWaitSignalHandler
// dispatches).
//
// Named SignalWait, not Wait — see this package's own doc comment for why:
// internal/delivery/cli's own cli.Wait (wait.go there) is a completely
// unrelated, already-existing generic polling primitive (V6-15B's own
// `--wait` flag support); this function neither calls nor is called by it.
//
// The named WaitRegistration is reloaded via loadWaitRegistrationForRun
// FIRST (deriving scope) before Idempotency-Key is even read — mirrors
// SubmitWaitSignalHandler's own identical discipline. Deliberately NO
// --expected-version/If-Match precondition of any kind (contrast
// approval.go's own ResolveApproval): a WAIT signal's caller is typically
// an external system (e.g. a CI webhook) reporting a real-world event it
// identifies by --signal-key, not a human who has just reloaded a UI page
// — runtime.SignalWait's own idempotent-duplicate-delivery contract (the
// SAME real external event reported through a genuinely different command
// invocation must still be recognized as one signal and safely no-op) must
// keep working even when a retried delivery has no way of knowing the
// registration's current version.
func SignalWait(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("wait signal", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := bindSignalWaitFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw wait signal <runId> <waitRegistrationId> --signal-key <key> [--payload <json>]")
	}
	runID, waitRegistrationID := positional[0], positional[1]
	if strings.TrimSpace(*f.signalKey) == "" {
		return usageErrorf("--signal-key is required")
	}
	var payload json.RawMessage
	if trimmed := strings.TrimSpace(*f.payload); trimmed != "" {
		if !json.Valid([]byte(trimmed)) {
			return usageErrorf("--payload is not valid JSON")
		}
		payload = json.RawMessage(trimmed)
	}

	registration, err := loadWaitRegistrationForRun(ctx, deps.UOW, runID, waitRegistrationID)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*f.principal)
	if err != nil {
		return err
	}

	scope := ports.ProjectScope(string(registration.ProjectID))
	hashPayload, err := json.Marshal(submitWaitSignalHashPayload{WaitRegistrationID: waitRegistrationID, SignalKey: *f.signalKey, Payload: payload})
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeSignalWait, Scope: scope,
		NormalizedPayload: hashPayload, IdempotencyKey: *f.idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return runtime.SignalWait(ctx, deps.UOW, deps.IDs, envelope.Command, runtime.SignalWaitRequest{
			RunID: runID, WaitRegistrationID: waitRegistrationID, SignalKey: *f.signalKey, Payload: payload,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
