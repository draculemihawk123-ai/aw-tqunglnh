package doctor_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/doctor"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

func TestCheckLiveness_AlwaysHealthy(t *testing.T) {
	result := doctor.CheckLiveness()
	if result.Status != doctor.StatusHealthy || result.Category != doctor.CategoryLiveness {
		t.Fatalf("CheckLiveness() = %+v, want Status=HEALTHY Category=LIVENESS", result)
	}
}

func validConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		DatabasePath:        filepath.Join(t.TempDir(), "agentkit.db"),
		ArtifactRoot:        t.TempDir(),
		WorkerID:            "worker-1",
		WorkerConcurrency:   4,
		LeaseTTL:            30 * time.Second,
		LeaseHeartbeat:      10 * time.Second,
		ProcessOutputLimit:  1 << 20,
		ProviderExecutables: map[string]string{},
	}
}

func TestCheckAppConfig_Valid(t *testing.T) {
	result := doctor.CheckAppConfig(validConfig(t))
	if result.Status != doctor.StatusHealthy {
		t.Fatalf("CheckAppConfig(valid) = %+v, want HEALTHY", result)
	}
}

func TestCheckAppConfig_Invalid_BlockedWithRemediation(t *testing.T) {
	cfg := validConfig(t)
	cfg.WorkerID = ""
	result := doctor.CheckAppConfig(cfg)
	if result.Status != doctor.StatusBlocked {
		t.Fatalf("CheckAppConfig(invalid) status = %v, want BLOCKED", result.Status)
	}
	if result.Remediation == "" {
		t.Fatal("CheckAppConfig(invalid) has no remediation")
	}
}

func TestCheckDatabase_Reachable_Healthy(t *testing.T) {
	result := doctor.CheckDatabase(context.Background(), &fake.QueryStore{})
	if result.Status != doctor.StatusHealthy {
		t.Fatalf("CheckDatabase(reachable) = %+v, want HEALTHY", result)
	}
}

func TestCheckDatabase_Unreachable_Blocked(t *testing.T) {
	result := doctor.CheckDatabase(context.Background(), &fake.QueryStore{Unreachable: true})
	if result.Status != doctor.StatusBlocked {
		t.Fatalf("CheckDatabase(unreachable) status = %v, want BLOCKED", result.Status)
	}
	if result.Remediation == "" {
		t.Fatal("CheckDatabase(blocked) has no remediation")
	}
}

func TestCheckRoot_MissingDirectory_Degraded(t *testing.T) {
	result := doctor.CheckRoot("artifact_root", filepath.Join(t.TempDir(), "does-not-exist-yet"))
	if result.Status != doctor.StatusDegraded {
		t.Fatalf("CheckRoot(missing) status = %v, want DEGRADED", result.Status)
	}
	if result.Remediation == "" {
		t.Fatal("CheckRoot(degraded) has no remediation")
	}
}

func TestCheckRoot_PathIsAFile_Blocked(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	result := doctor.CheckRoot("artifact_root", file)
	if result.Status != doctor.StatusBlocked {
		t.Fatalf("CheckRoot(file, not dir) status = %v, want BLOCKED", result.Status)
	}
}

func TestCheckRoot_WritableDirectory_Healthy(t *testing.T) {
	dir := t.TempDir()
	result := doctor.CheckRoot("artifact_root", dir)
	if result.Status != doctor.StatusHealthy {
		t.Fatalf("CheckRoot(writable) = %+v, want HEALTHY", result)
	}
	// The writability probe must clean up after itself.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("CheckRoot left %d file(s) behind in the probed directory, want 0: %+v", len(entries), entries)
	}
}

func TestCheckGit_Healthy(t *testing.T) {
	result := doctor.CheckGit(context.Background())
	if result.Status != doctor.StatusHealthy {
		t.Fatalf("CheckGit() = %+v, want HEALTHY (git is required to be on PATH for this repo's own CI/dev workflow)", result)
	}
}

func TestCheckWorkerConfig_Valid(t *testing.T) {
	cfg := workerpool.Config{Owner: "w", LeaseTTL: 30 * time.Second, HeartbeatEvery: 10 * time.Second}
	result := doctor.CheckWorkerConfig(cfg)
	if result.Status != doctor.StatusHealthy {
		t.Fatalf("CheckWorkerConfig(valid) = %+v, want HEALTHY", result)
	}
}

func TestCheckWorkerConfig_Invalid_Blocked(t *testing.T) {
	cfg := workerpool.Config{Owner: "", LeaseTTL: 30 * time.Second}
	result := doctor.CheckWorkerConfig(cfg)
	if result.Status != doctor.StatusBlocked {
		t.Fatalf("CheckWorkerConfig(invalid) status = %v, want BLOCKED", result.Status)
	}
	if result.Remediation == "" {
		t.Fatal("CheckWorkerConfig(blocked) has no remediation")
	}
}

func TestCheckProviderExecutable_NotConfigured_Degraded(t *testing.T) {
	result := doctor.CheckProviderExecutable("claude", "")
	if result.Status != doctor.StatusDegraded || result.Category != doctor.CategoryCapability {
		t.Fatalf("CheckProviderExecutable(unconfigured) = %+v, want DEGRADED/CAPABILITY", result)
	}
}

func TestCheckProviderExecutable_NotFound_Degraded(t *testing.T) {
	result := doctor.CheckProviderExecutable("claude", filepath.Join(t.TempDir(), "no-such-binary"))
	if result.Status != doctor.StatusDegraded {
		t.Fatalf("CheckProviderExecutable(not found) status = %v, want DEGRADED", result.Status)
	}
}

func TestCheckProviderExecutable_Found_HealthyAndObservedOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake-provider")
	if err := os.WriteFile(path, []byte("fake-provider-binary-v1\n"), 0o755); err != nil {
		t.Fatalf("write fixture executable: %v", err)
	}
	result := doctor.CheckProviderExecutable("claude", path)
	if result.Status != doctor.StatusHealthy {
		t.Fatalf("CheckProviderExecutable(found) = %+v, want HEALTHY", result)
	}
	for _, want := range []string{"observed", "sha256:", "not a registered AdapterBuildVersion"} {
		if !strings.Contains(result.Detail, want) {
			t.Fatalf("CheckProviderExecutable(found).Detail = %q, want it to contain %q (per ADR-022, must be clearly marked observed-only)", result.Detail, want)
		}
	}
}
