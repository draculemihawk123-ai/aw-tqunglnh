package ports

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// RuntimeExecutionConfigProvider is ADR-027's own required dependency for
// V4-04's NodeRun/Attempt scheduling transaction: it resolves the
// composition-root Configuration (go-core-spec §19) an execution actually
// runs under into a runtime.RuntimeExecutionConfigSnapshotV1 — structured,
// verifiable data, never a pre-computed hash (ADR-027's own "không nhận
// hash trần từ caller": only
// runtime.NewRuntimeExecutionConfigSnapshotV1 may ever produce a
// RuntimeExecutionConfigHash, and it does so from this method's own
// return value).
//
// Resolve is called OUTSIDE any database transaction (it may do real I/O —
// reading process-level config, the same "real I/O entirely outside any
// open transaction" discipline workspaceprovision.Handler's own
// ports.WorkspaceProvider already follows) — its caller re-validates the
// scheduling transaction's own preconditions (Run/NodeRun/definition pins)
// after Resolve returns, since real time passes between the two.
//
// Implementations: internal/app/ports/fake.RuntimeExecutionConfigProvider
// (a fixed, deterministic snapshot for tests — V4-04's own scope, reused
// as-is by V4-05's fake executor); a production implementation wired from
// internal/app/config.Config is V5-05's own scope (ADR-027), not built
// yet.
type RuntimeExecutionConfigProvider interface {
	Resolve(ctx context.Context) (runtime.RuntimeExecutionConfigSnapshotV1, error)
}
