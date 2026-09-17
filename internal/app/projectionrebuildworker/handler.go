package projectionrebuildworker

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// Handler implements workerpool.Handler for
// projectionrebuild.ProjectionRebuildJobKind ("PROJECTION_REBUILD"),
// mirroring internal/app/releasesetcommit.Handler's identical shape:
// depends only on Deps's own port interfaces, never a concrete adapter
// package directly.
//
// This is a real, complete, tested workerpool.Handler — a caller can
// register it against a real workerpool.Registry and run it today. What
// this task deliberately does NOT do (matching the SAME scope precedent
// V6-07A/V6-08A already established, per this task's own brief) is wire a
// real `aw worker`/`aw serve` composition-root call that runs it
// continuously in production: no established "which task owns real
// worker-process wiring" pattern exists yet anywhere in this codebase
// (grepped: every workerpool.Registry.Register call today is in test code
// only), so this task does not invent one unilaterally.
type Handler struct {
	deps Deps
}

// NewHandler returns a ready-to-register Handler.
func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps}
}

var _ workerpool.Handler = (*Handler)(nil)

// Handle implements workerpool.Handler: delegate to
// ExecuteProjectionRebuild. Any non-nil error here leaves the job
// un-completed for retry (or DEAD once MaxClaims is exhausted) — exactly
// like releasesetcommit.Handler.Handle's identical discipline; a terminal,
// typed outcome (SUCCEEDED/FAILED) is instead recorded by
// ExecuteProjectionRebuild itself via its own fenced transactions (which
// also complete the job), so Handle returns nil for those cases — never an
// error a caller would otherwise read as "still needs retrying".
func (h *Handler) Handle(ctx context.Context, job ports.DurableJob) error {
	if err := ExecuteProjectionRebuild(ctx, h.deps, job); err != nil {
		return fmt.Errorf("projectionrebuildworker: execute rebuild for job %s: %w", job.ID, err)
	}
	return nil
}
