package safesettings

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validSettings() SafeSettings {
	return SafeSettings{
		ManagedWorkspaceRoot:   "data/workspaces",
		ManagedArtifactRoot:    "data/artifacts",
		EvidenceRetention:      7 * 24 * time.Hour,
		ProcessOutputLimit:     1 << 20,
		ProviderExecutablePath: "C:/tools/claude/claude.exe",
		ProviderDefaultModel:   "claude-sonnet-4-5",
		ProviderCredentialRef:  "keychain:claude-api-key",
	}
}

func TestValidate_AcceptsWellFormedSettings(t *testing.T) {
	if err := Validate(validSettings()); err != nil {
		t.Fatalf("Validate(valid) = %v, want nil", err)
	}
}

func TestIsZero(t *testing.T) {
	if !(SafeSettings{}).IsZero() {
		t.Fatal("SafeSettings{}.IsZero() = false, want true")
	}
	if validSettings().IsZero() {
		t.Fatal("validSettings().IsZero() = true, want false")
	}
}

// TestValidate_RootTraversalRejected is this task's own Verify line
// "traversal" scenario.
func TestValidate_RootTraversalRejected(t *testing.T) {
	s := validSettings()
	s.ManagedWorkspaceRoot = "data/../../etc"
	err := Validate(s)
	if err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("Validate(traversal) = %v, want parent traversal error", err)
	}
}

func TestValidate_ProviderExecutablePathTraversalRejected(t *testing.T) {
	s := validSettings()
	s.ProviderExecutablePath = "tools/../../claude.exe"
	err := Validate(s)
	if err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("Validate(provider path traversal) = %v, want parent traversal error", err)
	}
}

// TestValidate_RootOverlapRejected is this task's own Verify line "root
// overlap" scenario: the artifact root is a subdirectory of the workspace
// root.
func TestValidate_RootOverlapRejected(t *testing.T) {
	s := validSettings()
	s.ManagedWorkspaceRoot = "data/managed"
	s.ManagedArtifactRoot = "data/managed/artifacts"
	err := Validate(s)
	if err == nil || !strings.Contains(err.Error(), "must not contain each other") {
		t.Fatalf("Validate(overlap) = %v, want overlap error", err)
	}
}

func TestValidate_RootOverlapRejected_ReverseDirection(t *testing.T) {
	s := validSettings()
	s.ManagedWorkspaceRoot = "data/managed/workspaces"
	s.ManagedArtifactRoot = "data/managed"
	err := Validate(s)
	if err == nil || !strings.Contains(err.Error(), "must not contain each other") {
		t.Fatalf("Validate(reverse overlap) = %v, want overlap error", err)
	}
}

func TestValidate_IdenticalRootsRejected(t *testing.T) {
	s := validSettings()
	s.ManagedArtifactRoot = s.ManagedWorkspaceRoot
	err := Validate(s)
	if err == nil || !strings.Contains(err.Error(), "must not contain each other") {
		t.Fatalf("Validate(identical roots) = %v, want overlap error", err)
	}
}

func TestValidate_NonOverlappingSiblingRootsAccepted(t *testing.T) {
	s := validSettings()
	s.ManagedWorkspaceRoot = "data/workspaces"
	s.ManagedArtifactRoot = "data/artifacts"
	if err := Validate(s); err != nil {
		t.Fatalf("Validate(sibling roots) = %v, want nil", err)
	}
	// "data/workspaces-extra" must never be treated as overlapping
	// "data/workspaces" merely because of a shared string prefix without a
	// path-separator boundary.
	s.ManagedArtifactRoot = "data/workspaces-extra"
	if err := Validate(s); err != nil {
		t.Fatalf("Validate(prefix-but-not-overlapping roots) = %v, want nil", err)
	}
}

// TestValidate_InvalidRetentionRejected is this task's own Verify line
// "invalid retention" scenario.
func TestValidate_InvalidRetentionRejected(t *testing.T) {
	for _, retention := range []time.Duration{0, -time.Hour} {
		s := validSettings()
		s.EvidenceRetention = retention
		if err := Validate(s); err == nil {
			t.Fatalf("Validate(retention=%s) = nil, want error", retention)
		}
	}
}

func TestValidate_InvalidProcessOutputLimitRejected(t *testing.T) {
	for _, limit := range []int{0, -1} {
		s := validSettings()
		s.ProcessOutputLimit = limit
		if err := Validate(s); err == nil {
			t.Fatalf("Validate(processOutputLimit=%d) = nil, want error", limit)
		}
	}
}

// TestValidate_InvalidModelRejected is this task's own Verify line "invalid
// ... model" scenario.
func TestValidate_InvalidModelRejected(t *testing.T) {
	for name, model := range map[string]string{
		"empty":      "",
		"whitespace": "  claude-sonnet  ",
		"control":    "claude\x00sonnet",
		"too-long":   strings.Repeat("m", 129),
	} {
		t.Run(name, func(t *testing.T) {
			s := validSettings()
			s.ProviderDefaultModel = model
			if err := Validate(s); err == nil {
				t.Fatalf("Validate(model=%q) = nil, want error", model)
			}
		})
	}
}

// TestValidate_InvalidProviderCredentialRefRejected is this task's own
// Verify line "invalid ... provider ref" scenario — including a
// space-containing value standing in for "looks like a raw secret rather
// than a reference id".
func TestValidate_InvalidProviderCredentialRefRejected(t *testing.T) {
	for name, ref := range map[string]string{
		"empty":           "",
		"whitespace":      "  ref  ",
		"contains-space":  "not a reference, looks like a pasted secret",
		"too-long":        strings.Repeat("a", 257),
		"disallowed-char": "ref+with+plus",
	} {
		t.Run(name, func(t *testing.T) {
			s := validSettings()
			s.ProviderCredentialRef = ref
			if err := Validate(s); err == nil {
				t.Fatalf("Validate(providerCredentialRef=%q) = nil, want error", ref)
			}
		})
	}
}

func TestValidate_EmptyRootsRejected(t *testing.T) {
	s := validSettings()
	s.ManagedWorkspaceRoot = ""
	if err := Validate(s); err == nil {
		t.Fatal("Validate(empty workspace root) = nil, want error")
	}
	s = validSettings()
	s.ManagedArtifactRoot = "   "
	if err := Validate(s); err == nil {
		t.Fatal("Validate(blank artifact root) = nil, want error")
	}
}

// TestJSONRoundTrip proves MarshalJSON/UnmarshalJSON round-trip exactly,
// including the human-readable Go-syntax duration string encoding.
func TestJSONRoundTrip(t *testing.T) {
	original := validSettings()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"evidenceRetention":"168h0m0s"`) {
		t.Fatalf("Marshal output = %s, want a Go-syntax duration string for evidenceRetention", data)
	}
	var decoded SafeSettings
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != original {
		t.Fatalf("round-tripped = %+v, want %+v", decoded, original)
	}
}

// TestJSONRoundTrip_ZeroValue proves the seeded "never configured" zero
// document round-trips too (evidenceRetention "0s" must not fail to parse
// as empty would).
func TestJSONRoundTrip_ZeroValue(t *testing.T) {
	var zero SafeSettings
	data, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal(zero): %v", err)
	}
	var decoded SafeSettings
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal(zero): %v", err)
	}
	if !decoded.IsZero() {
		t.Fatalf("round-tripped zero value = %+v, want IsZero() true", decoded)
	}
}

// TestJSONUnmarshal_EmptyObjectDecodesToZeroValue proves a bare "{}" (the
// literal seed value migration 0036_safe_settings.sql writes) decodes
// cleanly rather than failing on an empty evidenceRetention string.
func TestJSONUnmarshal_EmptyObjectDecodesToZeroValue(t *testing.T) {
	var decoded SafeSettings
	if err := json.Unmarshal([]byte(`{}`), &decoded); err != nil {
		t.Fatalf("Unmarshal(`{}`): %v", err)
	}
	if !decoded.IsZero() {
		t.Fatalf("Unmarshal(`{}`) = %+v, want IsZero() true", decoded)
	}
}

// TestJSONUnmarshal_UnknownFieldRejected is this task's own Verify line
// "unknown/startup-security field" scenario: an update payload naming a
// field outside the closed 7-field allowlist — including one of the
// explicitly-forbidden startup-security fields (databasePath, workerId,
// localPrincipal, a session key) — must be rejected at decode time, never
// silently ignored.
func TestJSONUnmarshal_UnknownFieldRejected(t *testing.T) {
	for name, payload := range map[string]string{
		"plain-unknown-field": `{"managedWorkspaceRoot":"w","notAllowlisted":"x"}`,
		"databasePath":        `{"managedWorkspaceRoot":"w","databasePath":"/tmp/evil.db"}`,
		"workerId":            `{"managedWorkspaceRoot":"w","workerId":"worker-1"}`,
		"localPrincipal":      `{"managedWorkspaceRoot":"w","localPrincipal":{"actor":"root","roles":["operator"]}}`,
		"sessionKey":          `{"managedWorkspaceRoot":"w","sessionKey":"deadbeef"}`,
		"signingKey":          `{"managedWorkspaceRoot":"w","signingKey":"deadbeef"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var decoded SafeSettings
			err := json.Unmarshal([]byte(payload), &decoded)
			if err == nil {
				t.Fatalf("Unmarshal(%s) = nil error, want rejection of the unrecognized field", payload)
			}
		})
	}
}
