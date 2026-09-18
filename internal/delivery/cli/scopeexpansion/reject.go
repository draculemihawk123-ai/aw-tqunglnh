package scopeexpansion

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"scope-expansion", "reject"}, Scope: cli.ScopeProject,
		AppOperation: "RejectScopeExpansion", HTTPOperationID: "rejectScopeExpansion",
	})
}

const commandTypeRejectScopeExpansion = "RejectScopeExpansion"

// rejectScopeExpansionBody mirrors
// internal/delivery/httpapi/workitem's own rejectScopeExpansionBody
// exactly — the one field this command's own hash canonicalizes.
type rejectScopeExpansionBody struct {
	DecisionNote string `json:"decisionNote"`
}

type rejectFlags struct {
	principal, project, idempotencyKey, note *string
	expectedVersion                          *uint64
}

// bindRejectFlags is Reject's own flag-binding step — see request.go's own
// bindRequestFlags doc comment for why this is factored out (the "spoof"
// Verify bullet).
func bindRejectFlags(fs *flag.FlagSet) rejectFlags {
	return rejectFlags{
		principal:       cli.BindPrincipalFlag(fs),
		project:         cli.BindProjectFlag(fs),
		expectedVersion: cli.BindExpectedVersionFlag(fs),
		idempotencyKey:  cli.BindIdempotencyKeyFlag(fs),
		note:            fs.String("note", "", "decision note explaining why this scope expansion request is rejected (required)"),
	}
}

// Reject implements `aw scope-expansion reject <requestId> --project-id
// <projectId> --expected-version <n> --note <text> [--idempotency-key
// <key>]` — an UPDATE-shaped mutation over workapp.RejectScopeExpansion.
// See approve.go's own doc comment for the shared reload-first/
// Version-precondition-inside-the-execute-closure discipline this command
// follows identically.
func Reject(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scope-expansion reject", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := bindRejectFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw scope-expansion reject <requestId> --project-id <projectId> --expected-version <n> --note <text>")
	}
	requestID := positional[0]
	if strings.TrimSpace(*f.project) == "" {
		return usageErrorf("--project-id is required")
	}
	if *f.expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the ScopeExpansionRequest version you observed, the CLI equivalent of HTTP's If-Match)")
	}
	if strings.TrimSpace(*f.note) == "" {
		return usageErrorf("--note is required")
	}

	scope := ports.ProjectScope(*f.project)
	detail, err := workapp.GetScopeExpansionRequest(ctx, deps.UOW, scope, requestID)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*f.principal)
	if err != nil {
		return err
	}

	normalized, err := json.Marshal(rejectScopeExpansionBody{DecisionNote: *f.note})
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRejectScopeExpansion, Scope: scope,
		NormalizedPayload: normalized, ExpectedVersion: *f.expectedVersion,
		IdempotencyKey: *f.idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		if detail.Version != *f.expectedVersion {
			return nil, fmt.Errorf("scope expansion request %s is at version %d, not %d — reload and retry", requestID, detail.Version, *f.expectedVersion)
		}
		return workapp.RejectScopeExpansion(ctx, deps.UOW, envelope.Command, workapp.RejectScopeExpansionRequest{
			RequestID: requestID, DecisionNote: *f.note,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
