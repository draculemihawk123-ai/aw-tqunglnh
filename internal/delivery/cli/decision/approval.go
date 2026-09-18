package decision

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"approval", "resolve"}, Scope: cli.ScopeProject,
		AppOperation: "ResolveApproval", HTTPOperationID: "resolveApproval",
	})
}

const commandTypeResolveApproval = "ResolveApproval"

// resolveApprovalHashPayload mirrors
// internal/delivery/httpapi/decision/approval.go's own
// resolveApprovalHashPayload exactly — the ApprovalRequestID is folded in
// alongside Outcome/Reason for the identical reason that file documents:
// without it, two callers resolving two DIFFERENT ApprovalRequests in the
// same project, who happen to reuse the same literal --idempotency-key AND
// request the same --outcome/--reason, would compute an identical
// semantic hash.
type resolveApprovalHashPayload struct {
	ApprovalRequestID string `json:"approvalRequestId"`
	Outcome           string `json:"outcome"`
	Reason            string `json:"reason,omitempty"`
}

type resolveApprovalFlags struct {
	principal, idempotencyKey, outcome, reason *string
	expectedVersion                            *uint64
}

// bindResolveApprovalFlags is ResolveApproval's own flag-binding step,
// factored out so flags_test.go can scan exactly the flags this command
// really binds (this task's own "spoof" Verify bullet) without having to
// execute ResolveApproval itself. Note there is deliberately no
// --actor/--role flag and no closed --outcome enum — see this task's own
// "IMPORTANT CORRECTION" framing: Outcome is validated deep inside
// advanceRunTx against the node's own compiled ApprovalNodeConfig, never
// here.
func bindResolveApprovalFlags(fs *flag.FlagSet) resolveApprovalFlags {
	return resolveApprovalFlags{
		principal:       cli.BindPrincipalFlag(fs),
		expectedVersion: cli.BindExpectedVersionFlag(fs),
		idempotencyKey:  cli.BindIdempotencyKeyFlag(fs),
		outcome:         fs.String("outcome", "", "the exact declared outcome this operator's decision maps to (required; an open, per-node vocabulary — validated against the node's own allow-list inside advanceRunTx, never a closed enum here)"),
		reason:          fs.String("reason", "", "optional human-readable reason for this decision"),
	}
}

// ResolveApproval implements `aw approval resolve <runId>
// <approvalRequestId> --outcome <value> [--reason <text>]
// --expected-version <n> [--idempotency-key <key>]` — the sole CLI entry
// point a typed operator decision goes through, dispatching
// runtime.ResolveApproval directly (the SAME function
// internal/delivery/httpapi/decision's own ResolveApprovalHandler
// dispatches, HE-14-S03's own "chat không tự resolve" upheld identically
// from either delivery mechanism by construction).
//
// The named ApprovalRequest is reloaded via loadApprovalRequestForUpdate
// FIRST (deriving scope, and its current Version for the
// --expected-version precondition below) before Idempotency-Key is even
// read — mirrors ResolveApprovalHandler's own identical discipline.
//
// The Version precondition check happens INSIDE the cli.Dispatch execute
// closure — reached only on a genuinely fresh (non-replayed) dispatch —
// exactly mirroring ResolveApprovalHandler's own documented ordering ("A
// real replay wins over ETag/state drift... never re-checked against the
// If-Match precondition"): a receipt replay for a retried
// --idempotency-key must always return the original stored result, never
// be rejected merely because the ApprovalRequest's own version has since
// moved on. This outer check can only ever catch an ALREADY-stale caller —
// runtime.ResolveApproval's own internal fenced CAS (keyed on
// ExpectedState=PENDING) remains the one true race-decider; two genuinely
// concurrent, equally-fresh callers both racing past this check still
// resolve safely inside ResolveApproval itself (exactly one Won=true).
func ResolveApproval(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("approval resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := bindResolveApprovalFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw approval resolve <runId> <approvalRequestId> --outcome <value> [--reason <text>] --expected-version <n>")
	}
	runID, approvalRequestID := positional[0], positional[1]
	if strings.TrimSpace(*f.outcome) == "" {
		return usageErrorf("--outcome is required")
	}
	if *f.expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the ApprovalRequest version you observed, the CLI equivalent of HTTP's If-Match)")
	}

	request, err := loadApprovalRequestForUpdate(ctx, deps.UOW, runID, approvalRequestID)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*f.principal)
	if err != nil {
		return err
	}

	scope := ports.ProjectScope(string(request.ProjectID))
	hashPayload, err := json.Marshal(resolveApprovalHashPayload{ApprovalRequestID: approvalRequestID, Outcome: *f.outcome, Reason: *f.reason})
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeResolveApproval, Scope: scope,
		NormalizedPayload: hashPayload, ExpectedVersion: *f.expectedVersion,
		IdempotencyKey: *f.idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		if request.Version != *f.expectedVersion {
			return nil, fmt.Errorf("approval request %s is at version %d, not %d — reload and retry", approvalRequestID, request.Version, *f.expectedVersion)
		}
		return runtime.ResolveApproval(ctx, deps.UOW, deps.IDs, envelope.Command, runtime.ResolveApprovalRequest{
			RunID: runID, ApprovalRequestID: approvalRequestID, Outcome: *f.outcome, Reason: *f.reason,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
