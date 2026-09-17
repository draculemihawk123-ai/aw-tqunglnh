package projectionrebuild

import (
	"context"
	"errors"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ProjectionStatusRequest is what a caller supplies to GetProjectionStatus.
type ProjectionStatusRequest struct {
	ProjectID      string
	ProjectionName string
}

// ProjectionStatusResult is GetProjectionStatus's own result — a plain,
// wire-friendly view of a projection's own CURRENT freshness (never a
// rebuild operation's own status — GetProjectionRebuildStatus above is
// that, separate, query). Status reuses ports.ProjectionStatus's own
// closed LIVE/DEGRADED/STALE vocabulary verbatim (encoded here as a plain
// string, the same "query returns its own DTO, never the port type
// itself" convention ProjectionRebuildStatus.Phase above already follows
// for ports.ProjectionRebuildPhase) — V6-09B's own explicit instruction is
// to reuse this existing vocabulary rather than invent a new one.
type ProjectionStatusResult struct {
	ProjectID      string `json:"projectId"`
	ProjectionName string `json:"projectionName"`
	Generation     uint64 `json:"generation"`
	Cursor         uint64 `json:"cursor"`
	Status         string `json:"status"`
}

// GetProjectionStatus answers V6-09B's own genuine gap: no query anywhere
// in this codebase before this task returned a projection's own current
// generation/cursor/freshness as a single, callable result —
// ports.ProjectionRepository (internal/app/ports/projection.go) only
// exposes the raw CRUD/CAS primitives GetActiveGeneration/
// GetProjectionCheckpoint this function wraps, one ports.Tx read
// transaction, nothing else.
//
// Deliberately placed in THIS package rather than internal/app/projection
// (V6-08/V6-08A's own live-consumer/classification concern) or a brand-new
// sibling package: V6-09B's own HTTP surface (internal/delivery/httpapi/
// projectionrebuild) needs exactly three queries/commands —
// RequestProjectionRebuild, GetProjectionRebuildStatus and this one — and
// keeping every one of them in one app-layer package lets that HTTP
// package's own architecture dispatch-spy test assert a single, simple
// fact ("imports internal/app/projectionrebuild and nothing else from
// internal/app"), matching this task's own "Không làm: handler does not
// call the worker/row store directly" line by construction rather than by
// convention alone.
//
// Freshness fallback logic mirrors internal/delivery/httpapi/kanban/
// projection_state.go's own resolveProjectionState exactly (read there
// first, before writing this): GetActiveGeneration ok=false (no generation
// ever created for this ProjectID/ProjectionName — V6-08A has never run
// against it) and GetProjectionCheckpoint returning
// ports.ErrPersistenceNotFound (a generation exists but the live consumer
// has never written its own first checkpoint for it) are BOTH real,
// expected early-lifecycle states, never errors — both report Cursor 0,
// Status STALE, never a fabricated LIVE. Kanban's own version of this
// logic reaches tx.Projections() directly from its own HTTP-layer file,
// which V6-09B's own stricter, explicit "Không làm" line forbids for THIS
// task's own routes — promoting the identical logic to a real, tested
// app-layer query is what closes that gap rather than reusing kanban's own
// HTTP-layer helper as-is.
func GetProjectionStatus(ctx context.Context, uow ports.UnitOfWork, req ProjectionStatusRequest) (ProjectionStatusResult, error) {
	projectID := strings.TrimSpace(req.ProjectID)
	projectionName := strings.TrimSpace(req.ProjectionName)
	if projectID == "" {
		return ProjectionStatusResult{}, errors.New("projectionrebuild: ProjectID is required")
	}
	if projectionName == "" {
		return ProjectionStatusResult{}, errors.New("projectionrebuild: ProjectionName is required")
	}

	var result ProjectionStatusResult
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		generation, ok, err := tx.Projections().GetActiveGeneration(ctx, projectID, projectionName)
		if err != nil {
			return err
		}
		if !ok {
			result = ProjectionStatusResult{
				ProjectID: projectID, ProjectionName: projectionName,
				Generation: 0, Cursor: 0, Status: string(ports.ProjectionStale),
			}
			return nil
		}
		checkpoint, err := tx.Projections().GetProjectionCheckpoint(ctx, projectID, projectionName, generation)
		if err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				result = ProjectionStatusResult{
					ProjectID: projectID, ProjectionName: projectionName,
					Generation: generation, Cursor: 0, Status: string(ports.ProjectionStale),
				}
				return nil
			}
			return err
		}
		result = ProjectionStatusResult{
			ProjectID: projectID, ProjectionName: projectionName,
			Generation: generation, Cursor: checkpoint.Cursor, Status: string(checkpoint.Status),
		}
		return nil
	})
	return result, err
}
