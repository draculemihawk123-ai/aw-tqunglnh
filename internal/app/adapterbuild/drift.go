package adapterbuild

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// ErrAdapterBuildDrifted is VerifyNoDrift's own fail-closed answer (V5-08,
// ADR-022's own "execution admission vẫn hash lại executable trước
// dispatch, nên drift xảy ra sau registration bị từ chối bằng
// ADAPTER_BUILD_DRIFT"): the executable currently configured for pinned's
// own provider no longer produces the exact tuple pinned's own ID was
// computed from — on ANY axis (content hash, protocol version, capability
// manifest, OS, toolchain). A caller maps this to
// runtime.TerminationReasonAdapterBuildDrift; VerifyNoDrift itself never
// touches the Attempt/NodeRun state machine.
var ErrAdapterBuildDrifted = errors.New("adapterbuild: configured executable no longer matches the registered build it was pinned against")

// VerifyNoDrift re-measures, right now, exactly what ProbeAdapterBuild
// measured at registration time — the executable's own content hash and a
// live capability manifest — and reports whether the freshly-computed
// CandidateTuple still has the same identity as pinned. executor MUST be
// the real, live ports.AgentExecutor configured for pinned.Tuple()'s own
// ProviderKey (a caller resolves it, e.g. via agentregistry.Registry;
// this function never resolves one itself, so it never has occasion to
// branch on provider identity).
//
// ExecutablePath and ConfigIdentity are carried over unchanged from
// pinned's own tuple: the former is what gets re-hashed, and the latter is
// pure operator-declared metadata (the OS/Toolchain a real CLI reports are
// live facts about the CURRENT process instead — cmd/aw/adapter.go's
// own registration flow already sources them from runtime.GOOS/
// runtime.Version() the exact same way, never from an operator flag).
//
// A single string comparison (freshTuple.ID() == pinned.ID()) is
// sufficient to detect drift across every axis at once, since ID is
// itself a canonical hash of the whole tuple — this function never needs
// a field-by-field diff.
//
// This performs real I/O (a filesystem read, a live process spawn via
// executor.Capabilities) and MUST be called entirely outside any database
// transaction, exactly like every other "real I/O before a fenced
// transaction re-verifies" caller in this codebase (schedule.go's own
// RuntimeExecutionConfigProvider.Resolve is the closest sibling).
func VerifyNoDrift(ctx context.Context, executor ports.AgentExecutor, pinned adapterbuild.Build) error {
	pinnedTuple := pinned.Tuple()

	contentHash, err := HashExecutableFile(pinnedTuple.ExecutablePath)
	if err != nil {
		return fmt.Errorf("%w: measure executable %q: %v", ErrAdapterBuildDrifted, pinnedTuple.ExecutablePath, err)
	}

	capabilities, err := executor.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("%w: probe capabilities: %v", ErrAdapterBuildDrifted, err)
	}
	manifest := adapterbuild.CapabilityManifest{
		SupportsStart:       capabilities.SupportsStart,
		SupportsResume:      capabilities.SupportsResume,
		SupportsCancel:      capabilities.SupportsCancel,
		CanonicalEventKinds: canonicalEventKindStrings(capabilities.CanonicalEventKinds),
	}
	_, manifestHash, err := adapterbuild.HashCapabilityManifest(manifest)
	if err != nil {
		return fmt.Errorf("%w: hash observed capability manifest: %v", ErrAdapterBuildDrifted, err)
	}

	freshTuple := adapterbuild.CandidateTuple{
		ProviderKey:            pinnedTuple.ProviderKey,
		ExecutablePath:         pinnedTuple.ExecutablePath,
		ExecutableContentHash:  contentHash,
		ProtocolVersion:        capabilities.ProtocolVersion,
		CapabilityManifestHash: manifestHash,
		OS:                     runtime.GOOS,
		Toolchain:              runtime.Version(),
		ConfigIdentity:         pinnedTuple.ConfigIdentity,
	}
	freshID, err := freshTuple.ID()
	if err != nil {
		return fmt.Errorf("%w: compute observed build identity: %v", ErrAdapterBuildDrifted, err)
	}
	if freshID != pinned.ID() {
		return fmt.Errorf("%w: observed build %s, pinned build %s", ErrAdapterBuildDrifted, freshID, pinned.ID())
	}
	return nil
}

func canonicalEventKindStrings(kinds []ports.AgentEventKind) []string {
	if len(kinds) == 0 {
		return nil
	}
	result := make([]string, len(kinds))
	for i, kind := range kinds {
		result[i] = string(kind)
	}
	return result
}
