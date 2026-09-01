// Command spike-worker plays a crashed-worker child process for one of
// SPK-04's six standard fault points (docs/spikes/01-go-core-spike-plan.md
// §9), selected by the AGENTKIT_SPIKE_CRASH_WORKER environment variable
// (see internal/adapters/sqlite/crashworker.go's CrashMode* constants). It
// exists because internal/spikeacceptance's SPK-04 scenario runs outside
// `go test` (agentkit-spike acceptance --full) and so cannot use the
// re-invoke-the-test-binary trick the original
// crash_resume_*_integration_test.go files use to spawn their own crashed
// worker and its fake-provider grandchild.
//
// This binary shares every byte of that worker logic with the integration
// tests (internal/adapters/sqlite/crashworker.go, non-test, exported code);
// it only supplies the standalone-binary-specific self-spawn strategy, since
// a re-invoked go-test binary needs a -test.run flag this binary does not.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
)

func main() {
	if os.Getenv(sqlite.CrashCheckpointProviderModeEnvironment) == "1" {
		sqlite.RunCrashCheckpointFakeProvider()
		return
	}
	mode := os.Getenv(sqlite.CrashWorkerModeEnvironment)
	if err := sqlite.RunCrashWorker(mode, selfSpawn); err != nil {
		fmt.Fprintln(os.Stderr, "spike-worker:", err)
		os.Exit(1)
	}
}

// selfSpawn re-invokes this same spike-worker binary in one of the two
// internal roles some fault points need a child of their own for: a fake
// provider (CheckpointThenHang) or a clean-exit no-op standing in for "a
// real external process just completed" (the process-exit fault points).
// Unlike the integration tests' equivalent (testBinarySpawner in
// internal/adapters/sqlite/crash_resume_integration_test.go), this needs no
// -test.run flag: main() above already dispatches purely on env vars.
func selfSpawn(role sqlite.SpawnRole) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve current spike-worker executable: %w", err)
	}
	// Strip this process's own dispatch env vars from the copy the child
	// inherits before setting the child's own — os/exec does not guarantee
	// "last wins" for a duplicate env var name, so the parent's mode/provider
	// flag must not still be present alongside the child's.
	env := withoutEnv(os.Environ(), sqlite.CrashWorkerModeEnvironment, sqlite.CrashCheckpointProviderModeEnvironment)
	switch role {
	case sqlite.SpawnRoleFakeProvider:
		env = append(env, sqlite.CrashCheckpointProviderModeEnvironment+"=1")
	case sqlite.SpawnRoleNoopExit:
		env = append(env, sqlite.CrashWorkerModeEnvironment+"="+sqlite.CrashModeNoopExit)
	default:
		return nil, fmt.Errorf("unknown spawn role %q", role)
	}
	command := exec.Command(executable)
	command.Env = env
	return command, nil
}

func withoutEnv(env []string, keys ...string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		keep := true
		for _, key := range keys {
			if name == key {
				keep = false
				break
			}
		}
		if keep {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
