package config

import (
	"testing"
	"time"
)

func TestDefaultsAreValid(t *testing.T) {
	cfg := Defaults()
	cfg.WorkerID = "seed-worker" // the one field Defaults deliberately leaves empty
	if err := Validate(cfg); err != nil {
		t.Fatalf("Defaults() with WorkerID set should be valid, got: %v", err)
	}
}

func TestDefaultsLeavesWorkerIDEmpty(t *testing.T) {
	if got := Defaults().WorkerID; got != "" {
		t.Fatalf("Defaults().WorkerID = %q, want empty (no safe default identity)", got)
	}
}

func strPtr(s string) *string     { return &s }
func intPtr(i int) *int           { return &i }
func durPtr(d time.Duration) *time.Duration { return &d }

func TestApplyOnlyOverwritesSetFields(t *testing.T) {
	base := Defaults()
	base.WorkerID = "base-worker"
	overrides := Overrides{DatabasePath: strPtr("custom.db")}
	got := overrides.Apply(base)
	if got.DatabasePath != "custom.db" {
		t.Errorf("DatabasePath = %q, want custom.db", got.DatabasePath)
	}
	if got.WorkerID != "base-worker" {
		t.Errorf("WorkerID = %q, want unchanged base-worker (Overrides did not set it)", got.WorkerID)
	}
	if got.WorkerConcurrency != base.WorkerConcurrency {
		t.Errorf("WorkerConcurrency = %d, want unchanged %d", got.WorkerConcurrency, base.WorkerConcurrency)
	}
}

func TestApplyDoesNotMutateBase(t *testing.T) {
	base := Defaults()
	original := base.DatabasePath
	_ = Overrides{DatabasePath: strPtr("changed.db")}.Apply(base)
	if base.DatabasePath != original {
		t.Fatalf("Apply mutated base.DatabasePath: got %q, want unchanged %q", base.DatabasePath, original)
	}
}

func TestApplyMergesProviderExecutablesKeyByKey(t *testing.T) {
	base := Defaults()
	base.ProviderExecutables = map[string]string{"claude": "/usr/bin/claude"}
	overrides := Overrides{ProviderExecutables: map[string]string{"codex": "/usr/bin/codex"}}
	got := overrides.Apply(base)
	if got.ProviderExecutables["claude"] != "/usr/bin/claude" {
		t.Errorf("claude entry should survive the merge, got %v", got.ProviderExecutables)
	}
	if got.ProviderExecutables["codex"] != "/usr/bin/codex" {
		t.Errorf("codex entry should be added by the merge, got %v", got.ProviderExecutables)
	}
}

func TestApplyOverridesEveryScalarField(t *testing.T) {
	base := Defaults()
	overrides := Overrides{
		DatabasePath:       strPtr("custom.db"),
		ArtifactRoot:       strPtr("custom-artifacts"),
		WorkerID:           strPtr("worker-1"),
		WorkerConcurrency:  intPtr(9),
		LeaseTTL:           durPtr(90 * time.Second),
		LeaseHeartbeat:     durPtr(30 * time.Second),
		ProcessOutputLimit: intPtr(2048),
	}
	got := overrides.Apply(base)
	if got.DatabasePath != "custom.db" {
		t.Errorf("DatabasePath = %q, want custom.db", got.DatabasePath)
	}
	if got.ArtifactRoot != "custom-artifacts" {
		t.Errorf("ArtifactRoot = %q, want custom-artifacts", got.ArtifactRoot)
	}
	if got.WorkerID != "worker-1" {
		t.Errorf("WorkerID = %q, want worker-1", got.WorkerID)
	}
	if got.WorkerConcurrency != 9 {
		t.Errorf("WorkerConcurrency = %d, want 9", got.WorkerConcurrency)
	}
	if got.LeaseTTL != 90*time.Second {
		t.Errorf("LeaseTTL = %v, want 90s", got.LeaseTTL)
	}
	if got.LeaseHeartbeat != 30*time.Second {
		t.Errorf("LeaseHeartbeat = %v, want 30s", got.LeaseHeartbeat)
	}
	if got.ProcessOutputLimit != 2048 {
		t.Errorf("ProcessOutputLimit = %d, want 2048", got.ProcessOutputLimit)
	}
}

func TestApplyProviderExecutablesOverridesSameKey(t *testing.T) {
	base := Defaults()
	base.ProviderExecutables = map[string]string{"claude": "/old/claude"}
	overrides := Overrides{ProviderExecutables: map[string]string{"claude": "/new/claude"}}
	got := overrides.Apply(base)
	if got.ProviderExecutables["claude"] != "/new/claude" {
		t.Errorf("claude entry should be overridden, got %v", got.ProviderExecutables)
	}
}

// TestLoadPrecedence proves the full defaults < file < env < flags chain:
// each layer sets DatabasePath, and only the last one applied (flags)
// should win.
func TestLoadPrecedence(t *testing.T) {
	file := Overrides{DatabasePath: strPtr("from-file.db"), WorkerID: strPtr("w"), ArtifactRoot: strPtr("a")}
	env := Overrides{DatabasePath: strPtr("from-env.db")}
	flags := Overrides{DatabasePath: strPtr("from-flags.db")}

	cfg, err := Load(file, env, flags)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabasePath != "from-flags.db" {
		t.Fatalf("DatabasePath = %q, want from-flags.db (flags must win over env and file)", cfg.DatabasePath)
	}
}

func TestLoadPrecedence_EnvBeatsFileWhenFlagsSilent(t *testing.T) {
	file := Overrides{DatabasePath: strPtr("from-file.db"), WorkerID: strPtr("w"), ArtifactRoot: strPtr("a")}
	env := Overrides{DatabasePath: strPtr("from-env.db")}
	flags := Overrides{} // flags layer sets nothing

	cfg, err := Load(file, env, flags)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabasePath != "from-env.db" {
		t.Fatalf("DatabasePath = %q, want from-env.db (env must win over file when flags are silent)", cfg.DatabasePath)
	}
}

func TestLoadPrecedence_FileBeatsDefaultsWhenEnvAndFlagsSilent(t *testing.T) {
	file := Overrides{DatabasePath: strPtr("from-file.db"), WorkerID: strPtr("w"), ArtifactRoot: strPtr("a")}

	cfg, err := Load(file, Overrides{}, Overrides{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabasePath != "from-file.db" {
		t.Fatalf("DatabasePath = %q, want from-file.db", cfg.DatabasePath)
	}
}

func TestLoadReturnsValidationError(t *testing.T) {
	// No WorkerID set anywhere: Defaults() leaves it empty and no layer
	// fills it in, so Load must fail rather than silently start with an
	// empty worker identity.
	_, err := Load(Overrides{}, Overrides{}, Overrides{})
	if err == nil {
		t.Fatal("Load with no WorkerID anywhere should fail validation")
	}
}

func TestLoadNeverReturnsPartialConfigAlongsideError(t *testing.T) {
	cfg, err := Load(Overrides{}, Overrides{}, Overrides{})
	if err == nil {
		t.Fatal("expected a validation error")
	}
	// Config embeds a map field, so it is not comparable with == — check
	// the zero-value fields individually instead.
	if cfg.DatabasePath != "" || cfg.ArtifactRoot != "" || cfg.WorkerID != "" ||
		cfg.WorkerConcurrency != 0 || cfg.LeaseTTL != 0 || cfg.LeaseHeartbeat != 0 ||
		cfg.ProcessOutputLimit != 0 || len(cfg.ProviderExecutables) != 0 {
		t.Fatalf("Load returned a non-zero Config alongside an error: %+v", cfg)
	}
}
