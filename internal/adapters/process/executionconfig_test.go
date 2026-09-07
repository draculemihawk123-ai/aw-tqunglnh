package process

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestRuntimeExecutionConfigProvider_ResolvesFromConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	cfg.WorkerID = "w1"
	cfg.ProcessOutputLimit = 4096
	cfg.EnvAllowlist = []string{"PATH", "HOME"}

	snapshot, err := NewRuntimeExecutionConfigProvider(cfg).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if snapshot.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", snapshot.SchemaVersion)
	}
	if snapshot.ProcessOutputLimitBytes != 4096 {
		t.Errorf("ProcessOutputLimitBytes = %d, want 4096", snapshot.ProcessOutputLimitBytes)
	}
	if len(snapshot.EnvAllowlist) != 2 || snapshot.EnvAllowlist[0] != "PATH" || snapshot.EnvAllowlist[1] != "HOME" {
		t.Errorf("EnvAllowlist = %v, want [PATH HOME]", snapshot.EnvAllowlist)
	}
}

// TestRuntimeExecutionConfigProvider_AlwaysReportsNetworkAllowed proves the
// user's own confirmed decision: this codebase has no real network
// isolation for a spawned child, so declaring NONE would be a false safety
// claim — NetworkAccess is always ALLOWED regardless of config content.
func TestRuntimeExecutionConfigProvider_AlwaysReportsNetworkAllowed(t *testing.T) {
	t.Parallel()

	snapshot, err := NewRuntimeExecutionConfigProvider(config.Config{ProcessOutputLimit: 1}).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if snapshot.NetworkAccess != runtime.NetworkAccessAllowed {
		t.Fatalf("NetworkAccess = %q, want ALLOWED", snapshot.NetworkAccess)
	}
}

// TestRuntimeExecutionConfigProvider_ProducesValidSnapshot proves Resolve's
// own output is always acceptable to
// runtime.NewRuntimeExecutionConfigSnapshotV1 — exactly what
// ScheduleExecutableNodeRun does with whatever a provider returns.
func TestRuntimeExecutionConfigProvider_ProducesValidSnapshot(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	cfg.WorkerID = "w1"
	snapshot, err := NewRuntimeExecutionConfigProvider(cfg).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, _, err := runtime.NewRuntimeExecutionConfigSnapshotV1(snapshot); err != nil {
		t.Fatalf("NewRuntimeExecutionConfigSnapshotV1(Resolve() output) = %v, want nil", err)
	}
}

func TestRuntimeExecutionConfigProvider_DoesNotAliasConfigEnvAllowlist(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	cfg.EnvAllowlist = []string{"PATH"}
	provider := NewRuntimeExecutionConfigProvider(cfg)

	snapshot, err := provider.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	snapshot.EnvAllowlist[0] = "MUTATED"

	again, err := provider.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if again.EnvAllowlist[0] != "PATH" {
		t.Fatalf("mutating one Resolve() result's EnvAllowlist affected a later Resolve() call: got %v", again.EnvAllowlist)
	}
}
