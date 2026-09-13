package releasesetcommit

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// Handler implements workerpool.Handler for ReleaseSetLocalCommitJobKind,
// mirroring workspacerelease.Handler's identical shape: depends only on
// ExecuteReleaseSetLocalCommitDeps's own port interfaces, never a concrete
// adapter package directly.
type Handler struct {
	deps ExecuteReleaseSetLocalCommitDeps
}

// NewHandler returns a ready-to-register Handler.
func NewHandler(deps ExecuteReleaseSetLocalCommitDeps) *Handler {
	return &Handler{deps: deps}
}

var _ workerpool.Handler = (*Handler)(nil)

// Handle implements workerpool.Handler: delegate to
// ExecuteReleaseSetLocalCommit. Any non-nil error here leaves the job
// un-completed for retry (or DEAD once MaxClaims is exhausted) — exactly
// like workspacerelease.Handler.Handle's identical discipline; a terminal,
// typed outcome (STALE_GENERATION/WORKSPACE_QUARANTINED/MARKER_DRIFT) is
// instead recorded by ExecuteReleaseSetLocalCommit itself via its own
// fenced failTerminal transaction (which also completes the job), so
// Handle returns nil for that case — never an error a caller would
// otherwise read as "still needs retrying".
func (h *Handler) Handle(ctx context.Context, job ports.DurableJob) error {
	if err := ExecuteReleaseSetLocalCommit(ctx, h.deps, job); err != nil {
		return fmt.Errorf("releasesetcommit: execute local commit for job %s: %w", job.ID, err)
	}
	return nil
}
