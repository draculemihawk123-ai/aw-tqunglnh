package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
)

// V9-05 (gap G5): what `aw worker --env-allowlist` now governs besides COMMAND
// and GATE processes — the provider executable's startup probe, and the
// ceiling of the environment an agent process inherits.

// TestWorkerStartupProbeInheritsTheEnvAllowlist: the fake provider CLI cannot
// run without PATH, like a real CLI that needs it to find its runtime. With
// the variable that says so in the allowlist but PATH left out, `aw worker`
// cannot even build its provider registry (the typed probe failure surfaces
// as the startup error); allowing PATH is all it takes — no wrapper script
// that hard-codes it.
func TestWorkerStartupProbeInheritsTheEnvAllowlist(t *testing.T) {
	requireGit(t)
	fake := fakeClaudeBinary(t)
	t.Setenv("AGENTKIT_HELPER_REQUIRE_ENV", "PATH")

	dirs := newWorkerDirs(t)
	options := dirs.options("aw-worker-env-insufficient")
	options.claudeExecutable = fake
	options.envAllowlist = []string{"AGENTKIT_HELPER_REQUIRE_ENV"}
	if assembled, err := assembleWorker(context.Background(), options); err == nil {
		assembled.close()
		t.Fatal("assembleWorker succeeded although the provider executable cannot run without PATH and PATH is not allowlisted")
	} else if !strings.Contains(err.Error(), "agent executor registry") || !strings.Contains(err.Error(), "exited 17") {
		t.Fatalf("assembleWorker error = %v, want the registry build to fail on the probe's exit code 17", err)
	}

	dirs = newWorkerDirs(t)
	options = dirs.options("aw-worker-env-sufficient")
	options.claudeExecutable = fake
	options.envAllowlist = []string{"AGENTKIT_HELPER_REQUIRE_ENV", "PATH"}
	assembled, err := assembleWorker(context.Background(), options)
	if err != nil {
		t.Fatalf("assembleWorker with PATH allowlisted: %v", err)
	}
	assembled.close()
}

// TestWorkerAgentExecutorIsBoundedByTheWorkersEnvAllowlist: the executor the
// worker registers cuts whatever a NodeRun pinned by what THIS worker allows.
func TestWorkerAgentExecutorIsBoundedByTheWorkersEnvAllowlist(t *testing.T) {
	workerConfig := config.Defaults()
	workerConfig.EnvAllowlist = []string{"PATH", "TMP"}
	executor := newWorkerAgentNodeExecutor(workerDeps{execConfig: process.NewRuntimeExecutionConfigProvider(workerConfig)})

	got, err := executor.EffectiveInheritedEnvironment(context.Background(), []string{"HOME", "PATH", "AWS_SECRET_ACCESS_KEY"})
	if err != nil {
		t.Fatalf("EffectiveInheritedEnvironment: %v", err)
	}
	if want := []string{"PATH"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EffectiveInheritedEnvironment = %v, want %v: only the names that are both pinned and allowed by this worker", got, want)
	}

	// A worker started without --env-allowlist passes nothing, whatever was pinned.
	bare := newWorkerAgentNodeExecutor(workerDeps{execConfig: process.NewRuntimeExecutionConfigProvider(config.Defaults())})
	got, err = bare.EffectiveInheritedEnvironment(context.Background(), []string{"HOME", "PATH"})
	if err != nil || len(got) != 0 {
		t.Fatalf("EffectiveInheritedEnvironment on a worker with no allowlist = %v, %v, want nothing", got, err)
	}
}
