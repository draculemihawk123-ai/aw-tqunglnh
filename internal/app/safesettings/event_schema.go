package safesettings

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

const (
	// SafeSettingsUpdatedEventType/SchemaVersion is V6-10G's own registered
	// domain event (ADR-016/017/025/028): appended, inside the same
	// transaction as the CAS itself (GC-INV-15), every time
	// UpdateSafeSettings successfully replaces the full desired document.
	SafeSettingsUpdatedEventType     = "SafeSettingsUpdated"
	SafeSettingsUpdatedSchemaVersion = 1
)

// safeSettingsUpdatedEventPayload is SafeSettingsUpdated v1's own shape
// (UpdateSafeSettings, commands.go) — every field of the closed 7-field
// allowlist, flattened to plain scalars only (EvidenceRetentionSeconds, not
// a nested duration type) so this struct stays comparable via `==`, the
// same convention every other event payload in this codebase already
// follows (e.g. internal/app/work's own rootWorkItemCreatedEventPayload).
// It never carries a raw secret value — ProviderCredentialRef is always a
// reference id (see internal/domain/safesettings.SafeSettings' own doc
// comment).
type safeSettingsUpdatedEventPayload struct {
	ManagedWorkspaceRoot     string `json:"managedWorkspaceRoot"`
	ManagedArtifactRoot      string `json:"managedArtifactRoot"`
	EvidenceRetentionSeconds int64  `json:"evidenceRetentionSeconds"`
	ProcessOutputLimit       int    `json:"processOutputLimit"`
	ProviderExecutablePath   string `json:"providerExecutablePath"`
	ProviderDefaultModel     string `json:"providerDefaultModel"`
	ProviderCredentialRef    string `json:"providerCredentialRef"`
	Version                  uint64 `json:"version"`
	UpdatedBy                string `json:"updatedBy"`
}

// DecodeSafeSettingsUpdatedV1 decodes SafeSettingsUpdated v1's own payload.
func DecodeSafeSettingsUpdatedV1(payloadJSON string) (any, error) {
	var payload safeSettingsUpdatedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/work/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(SafeSettingsUpdatedEventType, SafeSettingsUpdatedSchemaVersion, DecodeSafeSettingsUpdatedV1)
}
