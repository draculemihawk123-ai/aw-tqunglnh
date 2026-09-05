package fake

import (
	"context"
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// RuntimeExecutionConfigProvider is a fixed, deterministic
// ports.RuntimeExecutionConfigProvider for tests (V4-04, ADR-027) — no
// real I/O, just whatever Snapshot a test sets, or Err to exercise the
// "provider fails closed" path. V4-05's fake executor reuses this
// unchanged.
type RuntimeExecutionConfigProvider struct {
	Snapshot runtime.RuntimeExecutionConfigSnapshotV1
	Err      error
}

var _ ports.RuntimeExecutionConfigProvider = (*RuntimeExecutionConfigProvider)(nil)

// NewRuntimeExecutionConfigProvider returns a provider that resolves a
// valid, minimal snapshot — sufficient for any test that does not itself
// care about the exact effective config, only that scheduling succeeds.
func NewRuntimeExecutionConfigProvider() *RuntimeExecutionConfigProvider {
	return &RuntimeExecutionConfigProvider{
		Snapshot: runtime.RuntimeExecutionConfigSnapshotV1{
			SchemaVersion: 1, ProcessOutputLimitBytes: 1 << 20,
			EnvAllowlist: []string{"PATH"}, NetworkAccess: runtime.NetworkAccessNone,
		},
	}
}

func (p *RuntimeExecutionConfigProvider) Resolve(context.Context) (runtime.RuntimeExecutionConfigSnapshotV1, error) {
	if p.Err != nil {
		return runtime.RuntimeExecutionConfigSnapshotV1{}, p.Err
	}
	return p.Snapshot, nil
}

// ErrRuntimeExecutionConfigUnavailable is a ready-made Err value for a
// test proving the "provider missing/failing must fail Attempt creation
// closed" path (ADR-027) without needing to fabricate a bespoke sentinel
// per call site.
var ErrRuntimeExecutionConfigUnavailable = errors.New("fake: runtime execution config provider unavailable")
