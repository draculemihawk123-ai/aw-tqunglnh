package kanban

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// loadWorkItemForDetail reloads workItemID's own real, current row directly
// via tx.Work().GetWorkItem — never through workapp.GetWorkItem
// (internal/app/work/queries.go), which requires an already-known project
// scope this route's own path does not carry (routes.go's own doc comment:
// getWorkItemProjectedDetail has no {projectId} segment, mirroring
// internal/delivery/httpapi/workitem's own markWorkItemReady exactly).
func loadWorkItemForDetail(ctx context.Context, uow ports.UnitOfWork, workItemID string) (workdomain.WorkItem, error) {
	var item workdomain.WorkItem
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(ctx, workItemID)
		return err
	})
	return item, err
}

// resolveWorkspaceSetIDForDetail returns card's own effective
// WorkspaceSetID for the single-resource detail view — a targeted
// GetTaskFamily+GetProjectionRow lookup rather than list.go's own full
// ListProjectionRows scan (that scan is already free there, since the list
// handler fetches every row for the page anyway; a single detail lookup has
// no reason to pull an entire project's own row set just to find one
// family's root). Empty, nil when the family or its own root row cannot be
// resolved yet (an honest "not known yet", never a guess — mirrors list.go's
// own resolveWorkspaceSetID identical empty-map-miss behavior).
func resolveWorkspaceSetIDForDetail(ctx context.Context, tx ports.Tx, projectID string, generation uint64, card projection.WorkItemCardRow) (string, error) {
	if card.IsRoot {
		return card.WorkspaceSetID, nil
	}
	family, err := tx.Work().GetTaskFamily(ctx, card.FamilyID)
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return "", nil
		}
		return "", err
	}
	rootRow, err := tx.Projections().GetProjectionRow(ctx, projectID, projection.ProjectionName, generation, string(family.RootWorkItemID))
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return "", nil
		}
		return "", err
	}
	rootCard, err := decodeCardRow(rootRow)
	if err != nil {
		return "", err
	}
	return rootCard.WorkspaceSetID, nil
}

// handleGetWorkItemDetail implements GET /work-items/{workItemId}/detail
// (operationId getWorkItemProjectedDetail): the projected Card (built
// FIRST, from whatever the projection currently has — possibly stale) plus
// a FRESH authoritative Readiness (computed LAST, via
// internal/app/work.ExplainWorkItemReadiness, in its OWN separate
// transaction opened only after the card-building transaction below has
// already committed/closed) — see routes.go's own doc comment for why this
// exact ordering is what makes the "action race" Verify bullet hold: no
// matter how stale the projected Card's own Status/badges are, Readiness
// and ValidActions are always derived from whatever the REAL WorkItem state
// is at the moment this handler actually responds, never from anything the
// projection claimed a moment (or a generation) earlier.
func handleGetWorkItemDetail(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		workItemID := r.PathValue("workItemId")
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}

		item, err := loadWorkItemForDetail(ctx, deps.UnitOfWork, workItemID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		projectID := string(item.ProjectID)

		var card KanbanCardDTO
		var freshness httpapi.Freshness
		err = deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
			generation, fr, stateErr := resolveProjectionState(ctx, tx, projectID)
			if stateErr != nil {
				return stateErr
			}
			freshness = fr

			row, rowErr := tx.Projections().GetProjectionRow(ctx, projectID, projection.ProjectionName, generation, workItemID)
			if rowErr != nil {
				if errors.Is(rowErr, ports.ErrPersistenceNotFound) {
					card = cardFromAuthoritative(item)
					return nil
				}
				return rowErr
			}
			decoded, decodeErr := decodeCardRow(row)
			if decodeErr != nil {
				return decodeErr
			}
			workspaceSetID, wsErr := resolveWorkspaceSetIDForDetail(ctx, tx, projectID, generation, decoded)
			if wsErr != nil {
				return wsErr
			}
			badges, badgeErr := newBadgeLookup(tx).Badges(ctx, workspaceSetID)
			if badgeErr != nil {
				return badgeErr
			}
			card = cardToDTO(workItemID, projectID, decoded, workspaceSetID, badges)
			return nil
		})
		if err != nil {
			writeQueryError(w, err)
			return
		}

		// Fresh, authoritative, in a NEW transaction opened only now — the
		// exact ordering this handler's own doc comment above promises.
		readiness, err := workapp.ExplainWorkItemReadiness(ctx, deps.UnitOfWork, ports.ProjectScope(projectID), workItemID)
		if err != nil {
			writeQueryError(w, err)
			return
		}

		response := workItemDetailResponse{
			Card: card, Readiness: readiness, Freshness: freshness, ValidActions: validActionsForReadiness(readiness),
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, response, httpapi.ETagFromVersion(readiness.Version))
	}
}
