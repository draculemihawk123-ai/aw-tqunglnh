package config

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
)

func validConfig() Config {
	cfg := Defaults()
	cfg.WorkerID = "w1"
	return cfg
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Fatalf("Validate(validConfig()) = %v, want nil", err)
	}
}

func TestValidateRejectsEmptyDatabasePath(t *testing.T) {
	cfg := validConfig()
	cfg.DatabasePath = "  "
	assertInvalid(t, cfg, "database_path")
}

func TestValidateRejectsEmptyArtifactRoot(t *testing.T) {
	cfg := validConfig()
	cfg.ArtifactRoot = ""
	assertInvalid(t, cfg, "artifact_root")
}

func TestValidateRejectsEmptyWorkerID(t *testing.T) {
	cfg := validConfig()
	cfg.WorkerID = ""
	assertInvalid(t, cfg, "worker_id")
}

func TestValidateRejectsNonPositiveWorkerConcurrency(t *testing.T) {
	for _, n := range []int{0, -1} {
		cfg := validConfig()
		cfg.WorkerConcurrency = n
		assertInvalid(t, cfg, "worker_concurrency")
	}
}

func TestValidateRejectsNonPositiveLeaseTTL(t *testing.T) {
	cfg := validConfig()
	cfg.LeaseTTL = 0
	assertInvalid(t, cfg, "lease_ttl")
}

func TestValidateRejectsNonPositiveLeaseHeartbeat(t *testing.T) {
	cfg := validConfig()
	cfg.LeaseHeartbeat = -1 * time.Second
	assertInvalid(t, cfg, "lease_heartbeat")
}

// TestValidateRejectsHeartbeatNotShorterThanTTL is the "invalid
// combination" case: each field is individually positive, but together
// they are meaningless (a lease that renews no faster than it expires).
func TestValidateRejectsHeartbeatNotShorterThanTTL(t *testing.T) {
	cfg := validConfig()
	cfg.LeaseTTL = 10 * time.Second
	cfg.LeaseHeartbeat = 10 * time.Second // equal, not shorter
	assertInvalid(t, cfg, "lease_heartbeat/lease_ttl")

	cfg.LeaseHeartbeat = 20 * time.Second // longer than TTL
	assertInvalid(t, cfg, "lease_heartbeat/lease_ttl")
}

func TestValidateAcceptsHeartbeatShorterThanTTL(t *testing.T) {
	cfg := validConfig()
	cfg.LeaseTTL = 30 * time.Second
	cfg.LeaseHeartbeat = 10 * time.Second
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate with heartbeat < TTL should pass, got: %v", err)
	}
}

func TestValidateRejectsNonPositiveProcessOutputLimit(t *testing.T) {
	cfg := validConfig()
	cfg.ProcessOutputLimit = 0
	assertInvalid(t, cfg, "process_output_limit")
}

func TestValidateRejectsEmptyProviderExecutablePath(t *testing.T) {
	cfg := validConfig()
	cfg.ProviderExecutables = map[string]string{"claude": "", "codex": "/usr/bin/codex"}
	assertInvalid(t, cfg, "provider_executables[claude]")
}

func TestValidateAcceptsNonEmptyProviderExecutables(t *testing.T) {
	cfg := validConfig()
	cfg.ProviderExecutables = map[string]string{"claude": "/usr/bin/claude"}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate with a real provider executable should pass, got: %v", err)
	}
}

func TestValidateRejectsBlankEnvAllowlistEntry(t *testing.T) {
	cfg := validConfig()
	cfg.EnvAllowlist = []string{"PATH", "  "}
	assertInvalid(t, cfg, "env_allowlist[1]")
}

func TestValidateAcceptsEmptyEnvAllowlist(t *testing.T) {
	cfg := validConfig()
	cfg.EnvAllowlist = nil
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate with a nil EnvAllowlist should pass (closed, empty by default), got: %v", err)
	}
}

func TestValidateAcceptsNonEmptyEnvAllowlist(t *testing.T) {
	cfg := validConfig()
	cfg.EnvAllowlist = []string{"PATH", "HOME"}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate with a real env allowlist should pass, got: %v", err)
	}
}

// TestValidateCollectsAllProblems proves Validate does not stop at the
// first failure — an operator fixing config from scratch needs the whole
// picture in one run, not one error per restart.
func TestValidateCollectsAllProblems(t *testing.T) {
	cfg := Config{} // every field invalid at once
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate(zero Config) should fail")
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("Validate error should be an *apperror.Error, got %T", err)
	}
	problems := appErr.Details["problems"]
	for _, want := range []string{"database_path", "artifact_root", "worker_id", "worker_concurrency", "lease_ttl", "lease_heartbeat", "process_output_limit"} {
		if !strings.Contains(problems, want) {
			t.Errorf("problems = %q, want it to mention %q", problems, want)
		}
	}
}

func TestValidateErrorHasCorrelationID(t *testing.T) {
	err := Validate(Config{})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("Validate error should be an *apperror.Error, got %T", err)
	}
	if appErr.Details["correlationId"] == "" {
		t.Fatal("Validate error should carry a non-empty correlationId")
	}
}

func TestValidateErrorIsInvalidArgumentAndNotRetryable(t *testing.T) {
	err := Validate(Config{})
	if apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("CodeOf(err) = %q, want %q", apperror.CodeOf(err), apperror.CodeInvalidArgument)
	}
	if apperror.IsRetryable(err) {
		t.Fatal("a config validation error is never retryable — the same config will fail again")
	}
}

func assertInvalid(t *testing.T, cfg Config, wantSubstring string) {
	t.Helper()
	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate should reject this config (expected a problem mentioning %q)", wantSubstring)
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("Validate error should be an *apperror.Error, got %T", err)
	}
	if !strings.Contains(appErr.Details["problems"], wantSubstring) {
		t.Fatalf("problems = %q, want it to mention %q", appErr.Details["problems"], wantSubstring)
	}
}
