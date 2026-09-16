// Package settings is V6-15C's own CLI leaf over V6-10H's own safe
// settings HTTP slice (docs/design/08-v6-api-projections.md V6-15C:
// "operate ... safe settings from the terminal with the same installation
// authority as HTTP"): `aw settings show` and `aw settings update` — the
// terminal-side mirror of GET/PUT /settings/safe
// (internal/delivery/httpapi/safesettings), built on the exact same
// internal/app/safesettings application layer (GetSafeSettings/
// UpdateSafeSettings) and the exact same redact.Matcher masking
// discipline that package already uses — never a second, independently
// invented settings concept or a second masking scheme.
//
// "Không làm: no principal/security config mutation" (this task's own
// scope line): this package only ever reads/writes
// internal/domain/safesettings.SafeSettings' own closed 7-field allowlist
// (ManagedWorkspaceRoot, ManagedArtifactRoot, EvidenceRetention,
// ProcessOutputLimit, ProviderExecutablePath, ProviderDefaultModel,
// ProviderCredentialRef — none of them principal/security fields) —
// enforced structurally, not by convention: SafeSettings' own
// UnmarshalJSON (DisallowUnknownFields) rejects any request body naming a
// field outside that allowlist, the identical strict decode HTTP's own PUT
// /settings/safe already relies on (see commands.go's own doc comment).
// --principal-config (cli.BindPrincipalFlag) selects WHO is making the
// call (ADR-028's own authentication context), never WHAT is being
// changed — it is no more a "security config mutation" than the identical
// flag `aw serve` already uses to select its own acting principal.
//
// This package never wires itself into cmd/aw (V6-15O's own job — "chỉ
// V6-15O compose CLI/parity registry", docs/design/08-v6-api-projections.md
// §1 rule 8): it only registers its own cli.Descriptor(s) into cli.Default
// from its own init(), and exposes RunShow/RunUpdate for a future
// composition root to call once that wiring exists.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"settings", "show"}, Scope: cli.ScopeInstallation,
		AppOperation: "GetSafeSettings", HTTPOperationID: "getSafeSettings",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"settings", "update"}, Scope: cli.ScopeInstallation,
		AppOperation: "UpdateSafeSettings", HTTPOperationID: "updateSafeSettings",
	})
}

// ErrExpectedVersionRequired is returned by RunUpdate when
// --expected-version is omitted (or left at its zero value) — the CLI
// equivalent of HTTP's RequireIfMatch (real SafeSettingsRecord versions
// start at 1, so 0 can never be a legitimate current version).
var ErrExpectedVersionRequired = errors.New("settings update: --expected-version is required")

// ErrStaleExpectedVersion is returned by RunUpdate's own execute closure
// when --expected-version no longer matches the current persisted
// version — the CLI equivalent of HTTP's 412 "If-Match does not match the
// current version" response (internal/delivery/httpapi/safesettings/
// commands.go's own handleUpdateSafeSettings pre-dispatch check, mirrored
// here inside the Execute closure so it only ever runs on a genuinely
// fresh request, never a receipt replay — see RunUpdate's own doc
// comment).
var ErrStaleExpectedVersion = errors.New("settings update: --expected-version does not match the current version; reload and retry")

// Dependencies is everything both `aw settings` commands need from a
// composition root.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork both commands dispatch
	// through.
	UnitOfWork ports.UnitOfWork
	// IDs mints the event ID UpdateSafeSettings' own transaction needs for
	// its SAFE_SETTINGS_UPDATED event append.
	IDs idsource.Source
	// Clock supplies cmd.RequestedAt for RunUpdate's own command envelope.
	// Defaults to clock.System{} when nil.
	Clock clock.Clock
	// Matcher masks ProviderCredentialRef in every response this package
	// ever writes, GET, PUT and a replayed PUT alike — mirrors
	// internal/delivery/httpapi/safesettings.Dependencies' own Matcher doc
	// comment: reused directly (the SAME redact.Matcher.Tagged(redact.Secret,
	// ...) call, never a second, hand-rolled masking scheme), an HTTP
	// caller never gets to supply its own.
	Matcher redact.Matcher
}

// desiredWire mirrors internal/delivery/httpapi/safesettings/dto.go's own
// unexported desiredWire exactly: SafeSettings' own 7-field allowlist and
// JSON tags, EXCEPT ProviderCredentialRef, which is always masked.
type desiredWire struct {
	ManagedWorkspaceRoot   string `json:"managedWorkspaceRoot"`
	ManagedArtifactRoot    string `json:"managedArtifactRoot"`
	EvidenceRetention      string `json:"evidenceRetention"`
	ProcessOutputLimit     int    `json:"processOutputLimit"`
	ProviderExecutablePath string `json:"providerExecutablePath"`
	ProviderDefaultModel   string `json:"providerDefaultModel"`
	ProviderCredentialRef  string `json:"providerCredentialRef"`
}

func maskedDesiredWire(desired safesettings.SafeSettings, matcher redact.Matcher) desiredWire {
	return desiredWire{
		ManagedWorkspaceRoot:   desired.ManagedWorkspaceRoot,
		ManagedArtifactRoot:    desired.ManagedArtifactRoot,
		EvidenceRetention:      desired.EvidenceRetention.String(),
		ProcessOutputLimit:     desired.ProcessOutputLimit,
		ProviderExecutablePath: desired.ProviderExecutablePath,
		ProviderDefaultModel:   desired.ProviderDefaultModel,
		ProviderCredentialRef:  matcher.Tagged(redact.Secret, desired.ProviderCredentialRef),
	}
}

// stringFieldWire/durationFieldWire/intFieldWire mirror
// internal/delivery/httpapi/safesettings/dto.go's own three identically
// named unexported types.
type stringFieldWire struct {
	Effective             string                      `json:"effective"`
	Source                safesettingsapp.FieldSource `json:"source"`
	MaskedByStartupSource safesettingsapp.FieldSource `json:"maskedByStartupSource"`
}

type durationFieldWire struct {
	Effective             string                      `json:"effective"`
	Source                safesettingsapp.FieldSource `json:"source"`
	MaskedByStartupSource safesettingsapp.FieldSource `json:"maskedByStartupSource"`
}

type intFieldWire struct {
	Effective             int                         `json:"effective"`
	Source                safesettingsapp.FieldSource `json:"source"`
	MaskedByStartupSource safesettingsapp.FieldSource `json:"maskedByStartupSource"`
}

// effectiveWire mirrors internal/delivery/httpapi/safesettings/dto.go's own
// unexported effectiveWire exactly.
type effectiveWire struct {
	ManagedWorkspaceRoot   stringFieldWire   `json:"managedWorkspaceRoot"`
	ManagedArtifactRoot    stringFieldWire   `json:"managedArtifactRoot"`
	EvidenceRetention      durationFieldWire `json:"evidenceRetention"`
	ProcessOutputLimit     intFieldWire      `json:"processOutputLimit"`
	ProviderExecutablePath stringFieldWire   `json:"providerExecutablePath"`
	ProviderDefaultModel   stringFieldWire   `json:"providerDefaultModel"`
	ProviderCredentialRef  stringFieldWire   `json:"providerCredentialRef"`
}

func stringFieldWireFrom(f safesettingsapp.StringFieldEffective) stringFieldWire {
	return stringFieldWire{Effective: f.Effective, Source: f.Source, MaskedByStartupSource: f.MaskedByStartupSource}
}

func maskedStringFieldWireFrom(f safesettingsapp.StringFieldEffective, matcher redact.Matcher) stringFieldWire {
	return stringFieldWire{
		Effective: matcher.Tagged(redact.Secret, f.Effective), Source: f.Source, MaskedByStartupSource: f.MaskedByStartupSource,
	}
}

func durationFieldWireFrom(f safesettingsapp.DurationFieldEffective) durationFieldWire {
	return durationFieldWire{Effective: f.Effective.String(), Source: f.Source, MaskedByStartupSource: f.MaskedByStartupSource}
}

func intFieldWireFrom(f safesettingsapp.IntFieldEffective) intFieldWire {
	return intFieldWire{Effective: f.Effective, Source: f.Source, MaskedByStartupSource: f.MaskedByStartupSource}
}

func effectiveWireFrom(effective safesettingsapp.Effective, matcher redact.Matcher) effectiveWire {
	return effectiveWire{
		ManagedWorkspaceRoot:   stringFieldWireFrom(effective.ManagedWorkspaceRoot),
		ManagedArtifactRoot:    stringFieldWireFrom(effective.ManagedArtifactRoot),
		EvidenceRetention:      durationFieldWireFrom(effective.EvidenceRetention),
		ProcessOutputLimit:     intFieldWireFrom(effective.ProcessOutputLimit),
		ProviderExecutablePath: stringFieldWireFrom(effective.ProviderExecutablePath),
		ProviderDefaultModel:   stringFieldWireFrom(effective.ProviderDefaultModel),
		ProviderCredentialRef:  maskedStringFieldWireFrom(effective.ProviderCredentialRef, matcher),
	}
}

// responseDTO is the exact wire shape both `aw settings show` and
// `aw settings update` (fresh success or replay alike) produce — mirrors
// internal/delivery/httpapi/safesettings/dto.go's own responseDTO
// field-for-field: V6-15C's own "desired/effective/masking/restart output"
// line, satisfied identically regardless of which code path produced it.
type responseDTO struct {
	Desired         desiredWire   `json:"desired"`
	Version         uint64        `json:"version"`
	UpdatedAt       time.Time     `json:"updatedAt"`
	UpdatedBy       string        `json:"updatedBy"`
	RestartRequired bool          `json:"restartRequired"`
	Effective       effectiveWire `json:"effective"`
}

func buildResponse(result safesettingsapp.SafeSettingsResult, effective safesettingsapp.Effective, matcher redact.Matcher) responseDTO {
	return responseDTO{
		Desired:         maskedDesiredWire(result.Desired, matcher),
		Version:         result.Version,
		UpdatedAt:       result.UpdatedAt,
		UpdatedBy:       result.UpdatedBy,
		RestartRequired: result.RestartRequired,
		Effective:       effectiveWireFrom(effective, matcher),
	}
}

// resolveEffective fetches the current persisted safe-settings document and
// resolves this invocation's own Effective snapshot — the same
// safesettingsapp.ResolveEffective call cmd/aw/serve.go's own composition
// root makes once at boot (with the identical empty file/env/flag
// StartupOverrides: no prior task, HTTP or CLI, ever added real file/env/
// flag parsing for these 7 fields — see internal/delivery/httpapi/
// safesettings.Dependencies' own Effective doc comment for the full
// reasoning), just resolved fresh on every CLI invocation rather than once
// at a long-running server's own boot: a one-shot `aw settings show`/
// `update` process has no "boot, then serve requests for a while" window
// to freeze a snapshot across, so resolving it fresh, every time, is the
// literal equivalent for a process whose entire lifetime IS one request.
func resolveEffective(ctx context.Context, uow ports.UnitOfWork) (safesettingsapp.SafeSettingsResult, safesettingsapp.Effective, error) {
	current, err := safesettingsapp.GetSafeSettings(ctx, uow)
	if err != nil {
		return safesettingsapp.SafeSettingsResult{}, safesettingsapp.Effective{}, err
	}
	effective := safesettingsapp.ResolveEffective(
		safesettingsapp.Defaults(), safesettingsapp.StartupOverrides{}, current.Desired,
		safesettingsapp.StartupOverrides{}, safesettingsapp.StartupOverrides{},
	)
	return current, effective, nil
}

// writeHumanResponse renders response as human-readable text — the
// "human" half of V6-15B's own JSON/human dual-output contract
// (flags.go's own BindJSONFlag doc comment: no shared human-formatting
// helper exists yet in the V6-15B foundation, so each leaf branches on
// --json itself). idempotencyKey is empty for `aw settings show` (a plain
// query has no idempotency key or replay flag at all — output.go's own
// EncodeQueryResult doc comment draws the identical distinction for JSON
// output).
func writeHumanResponse(stdout io.Writer, response responseDTO, replayed bool, idempotencyKey string) {
	if idempotencyKey != "" {
		fmt.Fprintf(stdout, "idempotencyKey: %s\n", idempotencyKey)
		fmt.Fprintf(stdout, "replayed: %t\n", replayed)
	}
	fmt.Fprintf(stdout, "version: %d\n", response.Version)
	fmt.Fprintf(stdout, "updatedAt: %s\n", response.UpdatedAt.Format(time.RFC3339))
	fmt.Fprintf(stdout, "updatedBy: %s\n", response.UpdatedBy)
	fmt.Fprintf(stdout, "restartRequired: %t\n", response.RestartRequired)
	fmt.Fprintln(stdout, "desired:")
	fmt.Fprintf(stdout, "  managedWorkspaceRoot: %s\n", response.Desired.ManagedWorkspaceRoot)
	fmt.Fprintf(stdout, "  managedArtifactRoot: %s\n", response.Desired.ManagedArtifactRoot)
	fmt.Fprintf(stdout, "  evidenceRetention: %s\n", response.Desired.EvidenceRetention)
	fmt.Fprintf(stdout, "  processOutputLimit: %d\n", response.Desired.ProcessOutputLimit)
	fmt.Fprintf(stdout, "  providerExecutablePath: %s\n", response.Desired.ProviderExecutablePath)
	fmt.Fprintf(stdout, "  providerDefaultModel: %s\n", response.Desired.ProviderDefaultModel)
	fmt.Fprintf(stdout, "  providerCredentialRef: %s\n", response.Desired.ProviderCredentialRef)
	fmt.Fprintln(stdout, "effective:")
	writeHumanField(stdout, "managedWorkspaceRoot", response.Effective.ManagedWorkspaceRoot)
	writeHumanField(stdout, "managedArtifactRoot", response.Effective.ManagedArtifactRoot)
	fmt.Fprintf(stdout, "  evidenceRetention: %s (source=%s, maskedByStartupSource=%s)\n",
		response.Effective.EvidenceRetention.Effective, response.Effective.EvidenceRetention.Source, response.Effective.EvidenceRetention.MaskedByStartupSource)
	fmt.Fprintf(stdout, "  processOutputLimit: %d (source=%s, maskedByStartupSource=%s)\n",
		response.Effective.ProcessOutputLimit.Effective, response.Effective.ProcessOutputLimit.Source, response.Effective.ProcessOutputLimit.MaskedByStartupSource)
	writeHumanField(stdout, "providerExecutablePath", response.Effective.ProviderExecutablePath)
	writeHumanField(stdout, "providerDefaultModel", response.Effective.ProviderDefaultModel)
	writeHumanField(stdout, "providerCredentialRef", response.Effective.ProviderCredentialRef)
}

func writeHumanField(stdout io.Writer, name string, f stringFieldWire) {
	fmt.Fprintf(stdout, "  %s: %s (source=%s, maskedByStartupSource=%s)\n", name, f.Effective, f.Source, f.MaskedByStartupSource)
}

// RunShow implements `aw settings show`: a plain read, no idempotency key
// or CommandEnvelope at all (mirrors GET /settings/safe's own
// handleGetSafeSettings) — resolves the current desired/effective picture
// via resolveEffective and writes it as JSON (--json) or a human summary
// otherwise.
func RunShow(ctx context.Context, deps Dependencies, arguments []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("settings show", flag.ContinueOnError)
	jsonOutput := cli.BindJSONFlag(fs)
	if err := fs.Parse(arguments); err != nil {
		return cli.UsageError{Err: err}
	}
	current, effective, err := resolveEffective(ctx, deps.UnitOfWork)
	if err != nil {
		return err
	}
	response := buildResponse(current, effective, deps.Matcher)
	if *jsonOutput {
		return cli.EncodeQueryResult(stdout, response)
	}
	writeHumanResponse(stdout, response, false, "")
	return nil
}

// RunUpdate implements `aw settings update`: the full CommandEnvelope
// mutation flow, mirroring internal/delivery/httpapi/safesettings/
// commands.go's own handleUpdateSafeSettings step for step — strict decode
// (safesettings.SafeSettings' own UnmarshalJSON) → pre-dispatch
// safesettings.Validate → cli.BuildEnvelope (which calls the SAME
// httpapi.SemanticHash HTTP uses) → cli.Dispatch (which calls the SAME
// httpapi.LookupReceipt/ReconcileReceipt HTTP uses) → inside the Execute
// closure (i.e. only on a genuinely fresh request, never a replay,
// mirroring WriteReceiptReplay's own "never re-validates If-Match/current
// version on a true replay" contract), reload the current version and
// require it to still match --expected-version (ErrStaleExpectedVersion
// otherwise) before calling safesettingsapp.UpdateSafeSettings — then
// re-render the result (fresh OR replayed) through the SAME buildResponse
// RunShow uses, so a replayed update and a fresh one are always
// byte-for-byte identically shaped, credential ref always masked, exactly
// like that HTTP handler's own doc comment documents for its own replay
// path.
//
// Requires --expected-version (ErrExpectedVersionRequired otherwise, the
// CLI equivalent of HTTP's required strong If-Match) and reads the desired
// document from --file or stdin, bounded at cli.DefaultMaxInputBytes
// (cli.ReadBoundedInput). --idempotency-key is optional: cli.BuildEnvelope
// generates one when omitted, always returned in the JSON result.
func RunUpdate(ctx context.Context, deps Dependencies, arguments []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("settings update", flag.ContinueOnError)
	jsonOutput := cli.BindJSONFlag(fs)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := fs.Parse(arguments); err != nil {
		return cli.UsageError{Err: err}
	}
	if *expectedVersion == 0 {
		return cli.UsageError{Err: ErrExpectedVersionRequired}
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}

	// safesettings.SafeSettings' own UnmarshalJSON is already the exact
	// strict decoder (DisallowUnknownFields) HTTP's own PUT /settings/safe
	// relies on (see internal/delivery/httpapi/safesettings/commands.go's
	// own doc comment point 1) — reused here directly, never a second,
	// hand-rolled decoder. encoding/json.Unmarshal itself already rejects
	// trailing data after the one JSON value (the same "exactly one JSON
	// value" contract httpapi.DecodeJSON enforces via its own second
	// decode-and-expect-EOF check), so no separate check is needed here.
	var desired safesettings.SafeSettings
	if err := json.Unmarshal(raw, &desired); err != nil {
		return cli.UsageError{Err: fmt.Errorf("settings update: decode desired document: %w", err)}
	}
	// Pre-dispatch semantic validation — the SAME safesettings.Validate
	// UpdateSafeSettings' own transaction re-checks, called here first so
	// an operator gets a clean usage error before this command ever
	// computes a semantic hash or touches storage (mirrors
	// handleUpdateSafeSettings' own identical pre-dispatch call).
	if err := safesettings.Validate(desired); err != nil {
		return cli.UsageError{Err: err}
	}
	// A canonical, key-order-independent, whitespace-independent byte
	// encoding — encoding/json.Marshal always emits a struct's own fields
	// in declared order, the same technique httpapi.CanonicalizeJSON's own
	// doc comment documents for the identical purpose.
	canonical, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("settings update: marshal canonical payload: %w", err)
	}

	principal, err := config.LoadLocalPrincipalFile(*principalConfigPath)
	if err != nil {
		return err
	}
	if err := config.ValidateLocalPrincipal(principal); err != nil {
		return err
	}

	clk := deps.Clock
	if clk == nil {
		clk = clock.System{}
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: "UpdateSafeSettings", Scope: ports.InstallationScope(),
		NormalizedPayload: canonical, ExpectedVersion: *expectedVersion,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: clk.Now,
	})

	execute := func(ctx context.Context) (any, error) {
		// Reloaded AFTER cli.Dispatch's own replay decision — a true
		// replay never re-validates the expected version, only a
		// genuinely fresh request reaches this closure at all (mirrors
		// handleUpdateSafeSettings' own "nếu absent mới kiểm current
		// version" comment).
		current, err := safesettingsapp.GetSafeSettings(ctx, deps.UnitOfWork)
		if err != nil {
			return nil, err
		}
		if current.Version != *expectedVersion {
			return nil, ErrStaleExpectedVersion
		}
		return safesettingsapp.UpdateSafeSettings(ctx, deps.UnitOfWork, deps.IDs, envelope.Command,
			safesettingsapp.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(canonical)})
	}

	dispatched, err := cli.Dispatch(ctx, deps.UnitOfWork, envelope.Command, execute)
	if err != nil {
		return err
	}

	var result safesettingsapp.SafeSettingsResult
	if dispatched.Replayed {
		// A replay's own Result is the receipt's stored ResultJSON,
		// decoded back into json.RawMessage by cli.Dispatch (see that
		// function's own DispatchResult doc comment) — containing
		// ProviderCredentialRef in CLEARTEXT (the receipt was recorded by
		// UpdateSafeSettings' own transaction, which has no reason to know
		// about this package's own masking convention). This decodes it
		// back into a typed SafeSettingsResult so buildResponse below can
		// mask it identically to the fresh path — never returned to the
		// caller as-is.
		replayedRaw, ok := dispatched.Result.(json.RawMessage)
		if !ok {
			return errors.New("settings update: replayed receipt result has an unexpected shape")
		}
		if err := json.Unmarshal(replayedRaw, &result); err != nil {
			return fmt.Errorf("settings update: decode replayed result: %w", err)
		}
	} else {
		typed, ok := dispatched.Result.(safesettingsapp.SafeSettingsResult)
		if !ok {
			return errors.New("settings update: unexpected result shape")
		}
		result = typed
	}

	_, effective, err := resolveEffective(ctx, deps.UnitOfWork)
	if err != nil {
		return err
	}
	response := buildResponse(result, effective, deps.Matcher)

	if *jsonOutput {
		return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
			IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: response,
		})
	}
	writeHumanResponse(stdout, response, dispatched.Replayed, envelope.Command.IdempotencyKey)
	return nil
}
