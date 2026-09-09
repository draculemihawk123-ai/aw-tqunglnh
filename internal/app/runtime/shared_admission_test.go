package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// Audit finding (2026-09-08): GC-INV-23 makes a pinned AdapterBuildVersion
// mandatory for an AGENT node — runAdmissionProbePhase (admission.go) no
// longer treats a nil AdapterBuild as "nothing to verify, pass" for an
// AGENT executor. Every test fixture across this whole package that
// dispatches an AGENT Attempt through real admission (which is most of
// them — execute/finalize/fork/join/scope-expansion/cancel/completion) used
// to rely on the old "accidentally satisfied" behavior and never pinned
// one. Rather than threading a new registry return value through every one
// of those fixture functions and the ~30 call sites that destructure them,
// this package shares ONE real, valid, never-drifting AdapterBuild (and a
// matching agentregistry.Registry) across the whole test binary run —
// sync.Once-guarded so it is only actually built once. A caller that needs
// to construct its own workflow document with a real pinned build (fork/
// join/scope-expansion tests, which build their own literal
// AgentNodeConfig rather than going through agentExecutableDocument) calls
// sharedTestAdapterBuild(t) for the ID to embed and registers it itself
// before scheduling, exactly like admissionFixture already does; a caller
// that only needs a registry to hand to NewExecuteNodeHandler (replacing
// the old agentregistry.Empty() placeholder) calls sharedTestAgentRegistry(t).
var (
	sharedTestAdapterBuildOnce     sync.Once
	sharedTestAdapterBuildValue    domainadapterbuild.Build
	sharedTestAdapterRegistryValue *agentregistry.Registry
)

// sharedTestAdapterBuild returns the package-wide shared AdapterBuild,
// building it (a real temp executable, hashed, wrapped in a
// domainadapterbuild.Build) exactly once no matter how many tests call
// this.
func sharedTestAdapterBuild(t *testing.T) domainadapterbuild.Build {
	t.Helper()
	sharedTestAdapterBuildOnce.Do(func() { initSharedTestAdapterBuild(t) })
	return sharedTestAdapterBuildValue
}

// sharedTestAgentRegistry returns the package-wide shared registry whose
// own fake.AgentExecutor reports capabilities matching sharedTestAdapterBuild
// exactly — the direct replacement for the old agentregistry.Empty()
// placeholder at every NewExecuteNodeHandler call site that never actually
// cared about a real registry before this audit fix.
func sharedTestAgentRegistry(t *testing.T) *agentregistry.Registry {
	t.Helper()
	sharedTestAdapterBuildOnce.Do(func() { initSharedTestAdapterBuild(t) })
	return sharedTestAdapterRegistryValue
}

func initSharedTestAdapterBuild(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "shared-admission-build")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	executablePath := filepath.Join(dir, "shared-fixture-binary")
	if err := os.WriteFile(executablePath, []byte("shared-fixture-binary-v1"), 0o755); err != nil {
		t.Fatalf("write shared fixture executable: %v", err)
	}
	capabilities := ports.AgentCapabilities{
		Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
	}
	sharedTestAdapterBuildValue = admissionPinnedBuild(t, executablePath, capabilities)
	registry, err := agentregistry.New(context.Background(), &fake.AgentExecutor{CapabilitiesResult: capabilities})
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}
	sharedTestAdapterRegistryValue = registry
}

// registerSharedTestAdapterBuild inserts sharedTestAdapterBuild(t) into
// uow's own AdapterBuilds repository — schedule.go's own
// resolveExecutionProfile fails the whole scheduling transaction closed if
// a declared AdapterBuildID cannot be resolved, so this must run before
// ScheduleExecutableNodeRun whenever a test's own document pins the shared
// build's ID. Safe to call more than once for the same uow (InsertIfAbsent
// is idempotent by ID).
func registerSharedTestAdapterBuild(t *testing.T, uow ports.UnitOfWork) string {
	t.Helper()
	build := sharedTestAdapterBuild(t)
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, _, err := tx.AdapterBuilds().InsertIfAbsent(context.Background(), build)
		return err
	}); err != nil {
		t.Fatalf("register shared adapter build: %v", err)
	}
	return build.ID()
}
