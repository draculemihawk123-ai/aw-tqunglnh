package config

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

func TestDump_RedactsMatchingSecret(t *testing.T) {
	cfg := validConfig()
	cfg.DatabasePath = "s3cr3t-db-path"
	matcher := redact.NewMatcher("s3cr3t-db-path")

	got, err := Dump(cfg, matcher)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if got["database_path"] != "[REDACTED]" {
		t.Fatalf("database_path = %v, want [REDACTED]", got["database_path"])
	}
}

func TestDump_LeavesNonSecretFieldsUnchanged(t *testing.T) {
	cfg := validConfig()
	matcher := redact.NewMatcher() // no known secrets

	got, err := Dump(cfg, matcher)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if got["worker_id"] != cfg.WorkerID {
		t.Errorf("worker_id = %v, want %v", got["worker_id"], cfg.WorkerID)
	}
	if got["database_path"] != cfg.DatabasePath {
		t.Errorf("database_path = %v, want %v", got["database_path"], cfg.DatabasePath)
	}
}

func TestDump_RedactsProviderExecutableSecret(t *testing.T) {
	cfg := validConfig()
	cfg.ProviderExecutables = map[string]string{"claude": "/secret/path/claude"}
	matcher := redact.NewMatcher("/secret/path/claude")

	got, err := Dump(cfg, matcher)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	providers, ok := got["provider_executables"].(map[string]any)
	if !ok {
		t.Fatalf("provider_executables = %T, want map[string]any", got["provider_executables"])
	}
	if providers["claude"] != "[REDACTED]" {
		t.Fatalf("provider_executables[claude] = %v, want [REDACTED]", providers["claude"])
	}
}

func TestDump_NeverLeaksSecretAnywhereInOutput(t *testing.T) {
	cfg := validConfig()
	cfg.WorkerID = "top-secret-worker-id"
	matcher := redact.NewMatcher("top-secret-worker-id")

	got, err := Dump(cfg, matcher)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	for key, value := range got {
		if s, ok := value.(string); ok && strings.Contains(s, "top-secret-worker-id") {
			t.Fatalf("field %q leaked the secret: %v", key, value)
		}
	}
}

func TestDump_DurationsAreHumanReadableStrings(t *testing.T) {
	cfg := validConfig()
	matcher := redact.NewMatcher()

	got, err := Dump(cfg, matcher)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	ttl, ok := got["lease_ttl"].(string)
	if !ok || ttl != cfg.LeaseTTL.String() {
		t.Fatalf("lease_ttl = %v (%T), want the Go-syntax duration string %q", got["lease_ttl"], got["lease_ttl"], cfg.LeaseTTL.String())
	}
}
