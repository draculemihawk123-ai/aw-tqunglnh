package process

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// RuntimeExecutionConfigProvider is the production
// ports.RuntimeExecutionConfigProvider (ADR-027, port defined at V4-04) —
// V5-05's own scope. It resolves the composition-root's already-loaded,
// validated config.Config into a RuntimeExecutionConfigSnapshotV1, doing no
// I/O of its own — config.Config is already fully resolved (file < env <
// flags, validated) by the time this runs. Resolve returns the raw
// snapshot; the caller (internal/app/runtime.ScheduleExecutableNodeRun)
// canonicalizes and hashes it via runtime.NewRuntimeExecutionConfigSnapshotV1
// itself, exactly as it already does for the fake provider.
//
// NetworkAccess is always reported as ALLOWED — this codebase has no real
// network-isolation mechanism for a spawned child process today (confirmed
// with the user before writing this file), and declaring NONE would be a
// false safety claim this provider must never make: it describes what this
// environment actually enforces, not what an operator might wish it
// enforced. A future typed external-enforcement attestation (a real
// firewall/sandbox probe with its own provenance and lifecycle) is the
// only legitimate way this could ever become configurable — not a plain
// operator boolean.
type RuntimeExecutionConfigProvider struct {
	cfg config.Config
}

// NewRuntimeExecutionConfigProvider wraps an already-loaded, validated
// config.Config.
func NewRuntimeExecutionConfigProvider(cfg config.Config) RuntimeExecutionConfigProvider {
	return RuntimeExecutionConfigProvider{cfg: cfg}
}

var _ ports.RuntimeExecutionConfigProvider = RuntimeExecutionConfigProvider{}

func (p RuntimeExecutionConfigProvider) Resolve(context.Context) (runtime.RuntimeExecutionConfigSnapshotV1, error) {
	return runtime.RuntimeExecutionConfigSnapshotV1{
		SchemaVersion:           1,
		ProcessOutputLimitBytes: p.cfg.ProcessOutputLimit,
		EnvAllowlist:            append([]string(nil), p.cfg.EnvAllowlist...),
		NetworkAccess:           runtime.NetworkAccessAllowed,
	}, nil
}
