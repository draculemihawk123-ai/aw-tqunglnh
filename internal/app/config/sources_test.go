package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFromFile_MissingFileIsNotAnError(t *testing.T) {
	overrides, err := FromFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("FromFile(missing) = %v, want nil error", err)
	}
	if overrides.DatabasePath != nil {
		t.Fatalf("FromFile(missing) should return an empty Overrides, got %+v", overrides)
	}
}

func TestFromFile_ParsesAllFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
		"database_path": "file.db",
		"artifact_root": "file-artifacts",
		"worker_id": "file-worker",
		"worker_concurrency": 7,
		"lease_ttl": "45s",
		"lease_heartbeat": "15s",
		"process_output_limit": 4096,
		"provider_executables": {"claude": "/usr/bin/claude"}
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	overrides, err := FromFile(path)
	if err != nil {
		t.Fatalf("FromFile: %v", err)
	}
	if overrides.DatabasePath == nil || *overrides.DatabasePath != "file.db" {
		t.Errorf("DatabasePath = %v, want file.db", overrides.DatabasePath)
	}
	if overrides.WorkerConcurrency == nil || *overrides.WorkerConcurrency != 7 {
		t.Errorf("WorkerConcurrency = %v, want 7", overrides.WorkerConcurrency)
	}
	if overrides.LeaseTTL == nil || *overrides.LeaseTTL != 45*time.Second {
		t.Errorf("LeaseTTL = %v, want 45s", overrides.LeaseTTL)
	}
	if overrides.LeaseHeartbeat == nil || *overrides.LeaseHeartbeat != 15*time.Second {
		t.Errorf("LeaseHeartbeat = %v, want 15s", overrides.LeaseHeartbeat)
	}
	if overrides.ProviderExecutables["claude"] != "/usr/bin/claude" {
		t.Errorf("ProviderExecutables[claude] = %v, want /usr/bin/claude", overrides.ProviderExecutables)
	}
}

func TestFromFile_InvalidJSONIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := FromFile(path); err == nil {
		t.Fatal("FromFile with invalid JSON should return an error")
	}
}

func TestFromFile_InvalidDurationIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"lease_ttl": "not-a-duration"}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := FromFile(path); err == nil {
		t.Fatal("FromFile with an invalid lease_ttl duration should return an error")
	}
}

func fakeLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func TestFromEnv_ParsesSetVariables(t *testing.T) {
	lookup := fakeLookup(map[string]string{
		envDatabasePath:      "env.db",
		envWorkerConcurrency: "3",
		envLeaseTTL:          "20s",
	})
	overrides, err := FromEnv(lookup)
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if overrides.DatabasePath == nil || *overrides.DatabasePath != "env.db" {
		t.Errorf("DatabasePath = %v, want env.db", overrides.DatabasePath)
	}
	if overrides.WorkerConcurrency == nil || *overrides.WorkerConcurrency != 3 {
		t.Errorf("WorkerConcurrency = %v, want 3", overrides.WorkerConcurrency)
	}
	if overrides.LeaseTTL == nil || *overrides.LeaseTTL != 20*time.Second {
		t.Errorf("LeaseTTL = %v, want 20s", overrides.LeaseTTL)
	}
	if overrides.ArtifactRoot != nil {
		t.Errorf("ArtifactRoot should be nil (not set in env), got %v", overrides.ArtifactRoot)
	}
}

func TestFromEnv_UnsetVariablesStayNil(t *testing.T) {
	overrides, err := FromEnv(fakeLookup(nil))
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if overrides.DatabasePath != nil || overrides.WorkerConcurrency != nil || overrides.LeaseTTL != nil {
		t.Fatalf("FromEnv with nothing set should return an all-nil Overrides, got %+v", overrides)
	}
}

func TestFromEnv_InvalidIntegerIsAnError(t *testing.T) {
	lookup := fakeLookup(map[string]string{envWorkerConcurrency: "not-a-number"})
	if _, err := FromEnv(lookup); err == nil {
		t.Fatal("FromEnv with a non-numeric AGENTKIT_WORKER_CONCURRENCY should return an error")
	}
}

func TestFromEnv_InvalidDurationIsAnError(t *testing.T) {
	lookup := fakeLookup(map[string]string{envLeaseHeartbeat: "not-a-duration"})
	if _, err := FromEnv(lookup); err == nil {
		t.Fatal("FromEnv with an invalid AGENTKIT_LEASE_HEARTBEAT should return an error")
	}
}

func TestFromFlags_ParsesPassedFlags(t *testing.T) {
	overrides, err := FromFlags([]string{"--database-path", "flags.db", "--worker-concurrency", "12"})
	if err != nil {
		t.Fatalf("FromFlags: %v", err)
	}
	if overrides.DatabasePath == nil || *overrides.DatabasePath != "flags.db" {
		t.Errorf("DatabasePath = %v, want flags.db", overrides.DatabasePath)
	}
	if overrides.WorkerConcurrency == nil || *overrides.WorkerConcurrency != 12 {
		t.Errorf("WorkerConcurrency = %v, want 12", overrides.WorkerConcurrency)
	}
	if overrides.ArtifactRoot != nil {
		t.Errorf("ArtifactRoot should be nil (flag not passed), got %v", overrides.ArtifactRoot)
	}
}

func TestFromFlags_NoFlagsPassedLeavesEverythingNil(t *testing.T) {
	overrides, err := FromFlags(nil)
	if err != nil {
		t.Fatalf("FromFlags(nil): %v", err)
	}
	if overrides.DatabasePath != nil || overrides.WorkerConcurrency != nil || overrides.LeaseTTL != nil {
		t.Fatalf("FromFlags with no arguments should return an all-nil Overrides, got %+v", overrides)
	}
}

func TestFromFlags_DurationFlag(t *testing.T) {
	overrides, err := FromFlags([]string{"--lease-ttl", "1m"})
	if err != nil {
		t.Fatalf("FromFlags: %v", err)
	}
	if overrides.LeaseTTL == nil || *overrides.LeaseTTL != time.Minute {
		t.Errorf("LeaseTTL = %v, want 1m", overrides.LeaseTTL)
	}
}

func TestFromFlags_UnknownFlagIsAnError(t *testing.T) {
	if _, err := FromFlags([]string{"--not-a-real-flag"}); err == nil {
		t.Fatal("FromFlags with an unknown flag should return an error")
	}
}
