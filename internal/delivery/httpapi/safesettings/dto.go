package safesettings

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

// desiredWire is the wire shape of SafeSettingsResult.Desired — the exact
// same 7-field allowlist and JSON tags internal/domain/safesettings's own
// jsonSafeSettings wire type already uses (EvidenceRetention as a
// Go-syntax duration string, never a raw nanosecond integer), EXCEPT
// ProviderCredentialRef, which this type always masks (see
// maskedDesiredWire below) — this task's own "never echoed back in
// cleartext" line applies to every response this package writes, not only
// a hand-picked subset.
type desiredWire struct {
	ManagedWorkspaceRoot   string `json:"managedWorkspaceRoot"`
	ManagedArtifactRoot    string `json:"managedArtifactRoot"`
	EvidenceRetention      string `json:"evidenceRetention"`
	ProcessOutputLimit     int    `json:"processOutputLimit"`
	ProviderExecutablePath string `json:"providerExecutablePath"`
	ProviderDefaultModel   string `json:"providerDefaultModel"`
	ProviderCredentialRef  string `json:"providerCredentialRef"`
}

// maskedDesiredWire converts desired to its wire shape with
// ProviderCredentialRef always routed through matcher.Tagged(redact.Secret,
// ...) — a structural tag ("this field's role is always a credential
// reference"), independent of whether it happens to also be one of
// matcher's own known-secret values.
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
// internal/app/safesettings/startup.go's own StringFieldEffective/
// DurationFieldEffective/IntFieldEffective, restated with camelCase JSON
// tags (those domain types carry no json tags of their own, since nothing
// before this task ever put them on the wire) and, for the duration
// variant, the same Go-syntax-duration-string wire convention
// desiredWire.EvidenceRetention already uses. Desired is deliberately
// dropped here: it would only ever restate SafeSettingsResult.Desired's own
// per-field value a second time (ResolveEffective's own doc comment: "desired
// document... either every field was validated and persisted together, or
// none was" — there is exactly one desired document, exposed once already
// at desiredWire's own top level), so keeping it out of effectiveWire's
// per-field pictures avoids a redundant, potentially-diverging duplicate of
// data desiredWire has already made cleartext-safe.
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

// effectiveWire is Effective's own wire shape — V6-10H's own "response
// includes desired/effective/restart/masking" line's "effective" half.
type effectiveWire struct {
	ManagedWorkspaceRoot   stringFieldWire   `json:"managedWorkspaceRoot"`
	ManagedArtifactRoot    stringFieldWire   `json:"managedArtifactRoot"`
	EvidenceRetention      durationFieldWire `json:"evidenceRetention"`
	ProcessOutputLimit     intFieldWire      `json:"processOutputLimit"`
	ProviderExecutablePath stringFieldWire   `json:"providerExecutablePath"`
	ProviderDefaultModel   stringFieldWire   `json:"providerDefaultModel"`
	// ProviderCredentialRef is masked exactly like desiredWire's own field
	// of the same name — the SAME structural Secret tag, applied to
	// whichever value actually won this field's own precedence resolution
	// (which may be the SQLite desired value, a file/env/flag override, or
	// the empty default — masking applies uniformly regardless of source).
	ProviderCredentialRef stringFieldWire `json:"providerCredentialRef"`
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

// responseDTO is the exact wire shape both GET /settings/safe and PUT
// /settings/safe return on success — including a true idempotent replay of
// PUT (see replayOrProceed in commands.go): V6-10H's own "response includes
// desired/effective/restart/masking together" line, satisfied identically
// regardless of which of the three code paths produced it.
type responseDTO struct {
	Desired         desiredWire   `json:"desired"`
	Version         uint64        `json:"version"`
	UpdatedAt       time.Time     `json:"updatedAt"`
	UpdatedBy       string        `json:"updatedBy"`
	RestartRequired bool          `json:"restartRequired"`
	Effective       effectiveWire `json:"effective"`
}

// buildResponse converts one safesettingsapp.SafeSettingsResult (fresh from
// GetSafeSettings/UpdateSafeSettings, OR decoded back out of a replayed
// command receipt's own stored ResultJSON — see commands.go) plus this
// process' own fixed, boot-time Effective snapshot into the one wire shape
// this package ever returns.
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
