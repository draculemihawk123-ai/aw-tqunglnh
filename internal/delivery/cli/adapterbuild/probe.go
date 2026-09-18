package adapterbuild

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// RunProbe implements `aw adapter probe --provider-key <key>
// --executable-path <path> --protocol-version <v> --os <os> --toolchain
// <toolchain> --config-identity <id> --supports-start [--supports-resume]
// [--supports-cancel] [--canonical-event-kinds a,b,c]
// [--idempotency-key <key>] [--principal-config <path>] [--json]` — a
// mutation over appadapterbuild.ProbeAdapterBuild via the full
// cli.BuildEnvelope/cli.Dispatch flow, mirroring
// internal/delivery/cli/settings's own RunUpdate step for step: canonical
// payload -> cli.BuildEnvelope (the SAME httpapi.SemanticHash HTTP uses) ->
// cli.Dispatch (the SAME httpapi.LookupReceipt/ReconcileReceipt HTTP uses)
// -> replay/fresh-result branching -> cli.EncodeCommandResult/human output.
//
// Every field ProbeAdapterBuild needs is a caller-supplied flag on this
// command — mirroring POST /adapter-builds/probe's own probeAdapterBuildBody
// shape (internal/delivery/httpapi/adapterbuild/dto.go), NOT
// cmd/aw/adapter.go's own legacy runAdapterProbe, which instead spawns a
// real provider executor to MEASURE the capability manifest (see this
// package's own doc.go for the full reasoning). Probe never mutates the
// registry itself (appadapterbuild.ProbeAdapterBuild's own doc comment);
// this command only returns a candidate token for the operator to review
// before running `aw adapter register`.
func RunProbe(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("adapter probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	jsonOutput := cli.BindJSONFlag(fs)
	providerKey := fs.String("provider-key", "", "provider key this build identifies as (e.g. claude, codex)")
	executablePath := fs.String("executable-path", "", "canonical path to the provider CLI executable to probe")
	protocolVersion := fs.String("protocol-version", "", "protocol version this build speaks")
	osName := fs.String("os", "", "operating system this build was measured on")
	toolchain := fs.String("toolchain", "", "toolchain identity this build was measured with")
	configIdentity := fs.String("config-identity", "", "operator-assigned identity for this executable's configuration (permission mode, env profile, ...)")
	manifestFlags := bindCapabilityManifestFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw adapter probe --provider-key <key> --executable-path <path> --protocol-version <v> --os <os> --toolchain <toolchain> --config-identity <id> --supports-start [--supports-resume] [--supports-cancel] [--canonical-event-kinds a,b,c]")
	}
	switch {
	case strings.TrimSpace(*providerKey) == "":
		return usageErrorf("--provider-key is required")
	case strings.TrimSpace(*executablePath) == "":
		return usageErrorf("--executable-path is required")
	case strings.TrimSpace(*protocolVersion) == "":
		return usageErrorf("--protocol-version is required")
	case strings.TrimSpace(*osName) == "":
		return usageErrorf("--os is required")
	case strings.TrimSpace(*toolchain) == "":
		return usageErrorf("--toolchain is required")
	case strings.TrimSpace(*configIdentity) == "":
		return usageErrorf("--config-identity is required")
	}
	manifest := manifestFlags.manifest()
	if err := domainadapterbuild.ValidateCapabilityManifest(manifest); err != nil {
		return usageErrorf("capability manifest must have --supports-start and no empty/duplicate --canonical-event-kinds entries: %v", err)
	}

	// NormalizedPayload: a canonical, key-order-independent,
	// whitespace-independent encoding of a typed request struct — never the
	// raw flag values hashed directly (cli.EnvelopeRequest.NormalizedPayload's
	// own doc comment), mirroring probeAdapterBuildBody's own field set
	// exactly.
	normalized, err := json.Marshal(probeRequestWire{
		ProviderKey: *providerKey, ExecutablePath: *executablePath, ProtocolVersion: *protocolVersion,
		CapabilityManifest: capabilityManifestViewFrom(manifest), OS: *osName, Toolchain: *toolchain, ConfigIdentity: *configIdentity,
	})
	if err != nil {
		return fmt.Errorf("adapter probe: marshal canonical payload: %w", err)
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeProbe, Scope: ports.InstallationScope(),
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return appadapterbuild.ProbeAdapterBuild(ctx, deps.UoW, envelope.Command, appadapterbuild.ProbeRequest{
			ProviderKey: *providerKey, ExecutablePath: *executablePath, ProtocolVersion: *protocolVersion,
			CapabilityManifest: manifest, OS: *osName, Toolchain: *toolchain, ConfigIdentity: *configIdentity,
		})
	})
	if err != nil {
		return err
	}

	// A replayed result is the receipt's stored ResultJSON, decoded back
	// into json.RawMessage by cli.Dispatch — ProbeAdapterBuild's own
	// transaction writes exactly a domainadapterbuild.CandidateToken's own
	// JSON shape as that ResultJSON (every field on CandidateToken is
	// already exported), so this decodes directly into one, no intermediate
	// DTO needed (unlike RegisterAdapterBuild's own replay shape — see
	// views.go's own registerReplayPayload doc comment for why that one
	// needs one).
	var token domainadapterbuild.CandidateToken
	if dispatched.Replayed {
		raw, ok := dispatched.Result.(json.RawMessage)
		if !ok {
			return errors.New("adapter probe: replayed receipt result has an unexpected shape")
		}
		if err := json.Unmarshal(raw, &token); err != nil {
			return fmt.Errorf("adapter probe: decode replayed result: %w", err)
		}
	} else {
		typed, ok := dispatched.Result.(domainadapterbuild.CandidateToken)
		if !ok {
			return errors.New("adapter probe: unexpected result shape")
		}
		token = typed
	}

	if *jsonOutput {
		return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
			IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: token,
		})
	}
	writeHumanToken(stdout, token, dispatched.Replayed, envelope.Command.IdempotencyKey)
	return nil
}
