package kanban

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// badgeLookup resolves a WorkspaceSetID's current multi-repo badge set (one
// RepositoryBadge per distinct RepositoryID) via
// tx.Work().ListWorkspaceSetRepositoryWorkspaces, caching by WorkspaceSetID
// within one query — a board page routinely holds a root plus children that
// all share the SAME WorkspaceSetID, so this avoids repeating an identical
// lookup once per card.
type badgeLookup struct {
	tx    ports.Tx
	cache map[string][]RepositoryBadge
}

func newBadgeLookup(tx ports.Tx) *badgeLookup {
	return &badgeLookup{tx: tx, cache: map[string][]RepositoryBadge{}}
}

// Badges returns workspaceSetID's current per-repository badge list — nil, nil
// for an empty workspaceSetID (a family that has not yet reached the point of
// having a real WorkspaceSet, e.g. a fresh BACKLOG root with no approved scope
// yet), never an error for "no RepositoryWorkspace rows exist yet"
// (ListWorkspaceSetRepositoryWorkspaces returns an empty slice, not
// ErrPersistenceNotFound, like every List* method in ports.WorkRepository).
func (b *badgeLookup) Badges(ctx context.Context, workspaceSetID string) ([]RepositoryBadge, error) {
	if workspaceSetID == "" {
		return nil, nil
	}
	if cached, ok := b.cache[workspaceSetID]; ok {
		return cached, nil
	}
	repoWorkspaces, err := b.tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, workspaceSetID)
	if err != nil {
		return nil, err
	}
	badges := dedupeLatestGenerationPerRepository(repoWorkspaces)
	b.cache[workspaceSetID] = badges
	return badges, nil
}

// dedupeLatestGenerationPerRepository collapses repoWorkspaces (ordered by
// (RepositoryID, Generation) ascending — the port's own documented order) down
// to one badge per distinct RepositoryID: its MOST RECENT (highest Generation)
// RepositoryWorkspace state. A repository can carry more than one
// RepositoryWorkspace across its provision/quarantine/reconcile history (a new
// Generation each time it is re-provisioned); a board badge only ever needs
// the CURRENT state, never the full history.
func dedupeLatestGenerationPerRepository(repoWorkspaces []workspace.RepositoryWorkspace) []RepositoryBadge {
	if len(repoWorkspaces) == 0 {
		return nil
	}
	order := make([]string, 0, len(repoWorkspaces))
	latest := map[string]workspace.RepositoryWorkspace{}
	for _, rw := range repoWorkspaces {
		repositoryID := string(rw.RepositoryID)
		if _, seen := latest[repositoryID]; !seen {
			order = append(order, repositoryID)
		}
		// Generation-ascending within each RepositoryID, so the last write for
		// a key is always its highest Generation.
		latest[repositoryID] = rw
	}
	badges := make([]RepositoryBadge, 0, len(order))
	for _, repositoryID := range order {
		badges = append(badges, RepositoryBadge{RepositoryID: repositoryID, State: string(latest[repositoryID].State)})
	}
	return badges
}
