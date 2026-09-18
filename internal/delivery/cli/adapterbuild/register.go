package adapterbuild

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// RunRegister implements `aw adapter register --file <candidate-token.json>
// --supports-start [--supports-resume] [--supports-cancel]
// [--canonical-event-kinds a,b,c] [--idempotency-key <key>]
// [--principal-config <path>] [--json]` (or pipe the candidate token JSON
// printed by `aw adapter probe --json`'s own "result" field via stdin) — a
// mutation over appadapterbuild.RegisterAdapterBuild via the full
// cli.BuildEnvelope/cli.Dispatch flow, mirroring RunProbe's own shape.
//
// The candidate token is read from --file/stdin (cli.ReadBoundedInput) as
// the exact domainadapterbuild.CandidateToken JSON `aw adapter probe`
// handed the operator — mirroring POST /adapter-builds' own
// registerAdapterBuildBody.Token field, "a caller must echo back,
// byte-for-byte, the exact token probe handed it" (that file's own doc
// comment). The capability manifest is re-supplied via the SAME
// --supports-*/--canonical-event-kinds flags RunProbe uses (never read back
// off the token): RegisterAdapterBuild re-derives its hash and compares
// against what the token bound, the TOCTOU-closing re-measurement ADR-022
// requires — a caller confirming a DIFFERENT manifest than what was probed
// is exactly what appadapterbuild.ErrCapabilityManifestDrift exists to
// reject, not silently accept.
func RunRegister(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("adapter register", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	jsonOutput := cli.BindJSONFlag(fs)
	filePath := cli.BindFileFlag(fs)
	manifestFlags := bindCapabilityManifestFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw adapter register [--file <candidate-token.json>] --supports-start [--supports-resume] [--supports-cancel] [--canonical-event-kinds a,b,c] (or pipe the candidate token JSON via stdin)")
	}
	manifest := manifestFlags.manifest()
	if err := domainadapterbuild.ValidateCapabilityManifest(manifest); err != nil {
		return usageErrorf("capability manifest must have --supports-start and no empty/duplicate --canonical-event-kinds entries: %v", err)
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var token domainadapterbuild.CandidateToken
	if err := json.Unmarshal(raw, &token); err != nil {
		return usageErrorf("adapter register: decode candidate token: %v", err)
	}

	normalized, err := json.Marshal(registerRequestWire{Token: token, CapabilityManifest: capabilityManifestViewFrom(manifest)})
	if err != nil {
		return fmt.Errorf("adapter register: marshal canonical payload: %w", err)
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRegister, Scope: ports.InstallationScope(),
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return appadapterbuild.RegisterAdapterBuild(ctx, deps.UoW, envelope.Command, appadapterbuild.RegisterRequest{
			Token: token, CapabilityManifest: manifest,
		})
	})
	if err != nil {
		return err
	}

	// A replayed result is RegisterAdapterBuild's own registerReceiptPayload
	// shape (Tuple/CapabilityManifest/RegisteredBy/RegisteredAt/
	// AlreadyExisted), never appadapterbuild.RegisterResult directly — see
	// views.go's own registerReplayPayload doc comment for why (Build has no
	// exported field, so its receipt round-trips through NewBuild instead).
	var result appadapterbuild.RegisterResult
	if dispatched.Replayed {
		rawResult, ok := dispatched.Result.(json.RawMessage)
		if !ok {
			return errors.New("adapter register: replayed receipt result has an unexpected shape")
		}
		var payload registerReplayPayload
		if err := json.Unmarshal(rawResult, &payload); err != nil {
			return fmt.Errorf("adapter register: decode replayed result: %w", err)
		}
		result, err = payload.toResult()
		if err != nil {
			return fmt.Errorf("adapter register: reconstruct replayed build: %w", err)
		}
	} else {
		typed, ok := dispatched.Result.(appadapterbuild.RegisterResult)
		if !ok {
			return errors.New("adapter register: unexpected result shape")
		}
		result = typed
	}

	view := newRegisterResultView(result)
	if *jsonOutput {
		return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
			IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: view,
		})
	}
	writeHumanRegisterResult(stdout, view, dispatched.Replayed, envelope.Command.IdempotencyKey)
	return nil
}
