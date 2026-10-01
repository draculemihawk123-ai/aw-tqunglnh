package kanban

import (
	"context"
	"errors"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// DetailRequest names one projected-detail read.
type DetailRequest struct {
	WorkItemID string
	// ProjectID, when non-empty, must equal the WorkItem's own project or the
	// read fails with ports.ErrScopeMismatch (the CLI's --project-id). Empty
	// derives the project solely from the WorkItem's stored row — the HTTP
	// route has no {projectId} path segment on purpose, so a caller-supplied
	// project is never trusted over the real row.
	ProjectID string
}

// Detail is a WorkItem's projected Card next to its FRESH authoritative
// Readiness. Card.Status/badges are projected and possibly stale (Freshness
// says how stale); Readiness is never derived from them.
type Detail struct {
	Card      Card                      `json:"card"`
	Readiness workapp.WorkItemReadiness `json:"readiness"`
	Freshness Freshness                 `json:"freshness"`
}

// GetWorkItemProjectedDetail returns req.WorkItemID's projected card and fresh
// readiness.
//
// The WorkItem is reloaded first, directly via tx.Work().GetWorkItem rather
// than work.GetWorkItem (which needs an already-known project scope the
// request does not carry), to learn its ProjectID. The card is then built from
// whatever the projection currently has — falling back to the authoritative
// row itself, with every field it cannot know left at its honest zero value,
// when no projection row exists yet (the WorkItem was just created and the
// live consumer has not applied its created event). Readiness is computed
// LAST, in its OWN transaction opened only after the card-building one has
// closed — the ordering that makes the "action race" guarantee hold by
// construction: however stale the card is, readiness reflects the real
// WorkItem at the moment this function returns.
func GetWorkItemProjectedDetail(ctx context.Context, uow ports.UnitOfWork, req DetailRequest) (Detail, error) {
	workItemID := strings.TrimSpace(req.WorkItemID)
	if workItemID == "" {
		return Detail{}, errors.New("kanban: WorkItemID is required")
	}

	var item workdomain.WorkItem
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(ctx, workItemID)
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	projectID := string(item.ProjectID)
	if req.ProjectID != "" && req.ProjectID != projectID {
		return Detail{}, ports.ErrScopeMismatch
	}

	var detail Detail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		generation, freshness, err := resolveProjectionState(ctx, tx, projectID)
		if err != nil {
			return err
		}
		detail.Freshness = freshness

		row, err := tx.Projections().GetProjectionRow(ctx, projectID, projection.ProjectionName, generation, workItemID)
		if err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				detail.Card = cardFromAuthoritative(item)
				return nil
			}
			return err
		}
		decoded, err := decodeCardRow(row)
		if err != nil {
			return err
		}
		workspaceSetID, err := resolveWorkspaceSetIDForDetail(ctx, tx, projectID, generation, decoded)
		if err != nil {
			return err
		}
		badges, err := newBadgeLookup(tx).Badges(ctx, workspaceSetID)
		if err != nil {
			return err
		}
		detail.Card = newCard(workItemID, projectID, decoded, workspaceSetID, badges)
		return nil
	})
	if err != nil {
		return Detail{}, err
	}

	// Fresh, authoritative, in a NEW transaction opened only now.
	detail.Readiness, err = workapp.ExplainWorkItemReadiness(ctx, uow, ports.ProjectScope(projectID), workItemID)
	if err != nil {
		return Detail{}, err
	}
	return detail, nil
}

// resolveWorkspaceSetIDForDetail returns card's effective WorkspaceSetID for
// the single-resource view — a targeted GetTaskFamily+GetProjectionRow lookup
// rather than ListWorkItemKanban's full row scan (that scan is free there
// since the list fetches every row anyway; one detail has no reason to pull a
// whole project's rows just to find one family's root). Empty when the family
// or its root row cannot be resolved yet, never a guess.
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

// cardFromAuthoritative builds a fallback Card straight from the REAL
// WorkItem row, used only when no projection row exists yet. Every field it
// cannot know (ActiveRun*/BlockerCount/TopBlockerType/
// PendingScopeExpansionCount/WorkspaceSetID/RepositoryBadges) stays at its
// honest zero value rather than a fabricated guess. Still display-only, like a
// real projected card, never fed into readiness authority.
func cardFromAuthoritative(item workdomain.WorkItem) Card {
	card := Card{
		WorkItemID: string(item.ID), ProjectID: string(item.ProjectID), FamilyID: string(item.FamilyID),
		Title: item.Title, IsRoot: item.ParentID == nil, Status: string(item.Status),
	}
	if item.ParentID != nil {
		card.ParentWorkItemID = string(*item.ParentID)
	}
	return card
}
