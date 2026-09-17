package kanban

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// badgeLookup resolves a WorkspaceSetID's own current multi-repo badge set
// (RepositoryBadgeDTO, one per distinct RepositoryID) via
// tx.Work().ListWorkspaceSetRepositoryWorkspaces, caching by WorkspaceSetID
// within one handler invocation — a Kanban page routinely holds several
// cards (a root plus its children) that all share the SAME WorkspaceSetID
// (row.go's own "child inherits its root's WorkspaceSet"), so this avoids
// repeating an identical lookup once per card.
type badgeLookup struct {
	tx    ports.Tx
	cache map[string][]RepositoryBadgeDTO
}

func newBadgeLookup(tx ports.Tx) *badgeLookup {
	return &badgeLookup{tx: tx, cache: map[string][]RepositoryBadgeDTO{}}
}

// Badges returns workspaceSetID's own current per-repository badge list —
// nil, nil for an empty workspaceSetID (a WorkItem whose family has not yet
// reached the point of having a real WorkspaceSet at all, e.g. a fresh
// BACKLOG root with no approved scope yet), never an error for "no
// RepositoryWorkspace rows exist yet" (ListWorkspaceSetRepositoryWorkspaces'
// own contract: an empty slice, not ErrPersistenceNotFound, exactly like
// every other List* method in ports.WorkRepository).
func (b *badgeLookup) Badges(ctx context.Context, workspaceSetID string) ([]RepositoryBadgeDTO, error) {
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
// (RepositoryID, Generation) ascending — ListWorkspaceSetRepositoryWorkspaces'
// own documented order) down to one badge per distinct RepositoryID: its own
// MOST RECENT (highest Generation) RepositoryWorkspace state. A repository
// can carry more than one RepositoryWorkspace row across its own
// provision/quarantine/reconcile history (a new Generation each time it is
// re-provisioned) — a Kanban badge only ever needs the CURRENT state, never
// the full history (the same "one representative value per card, full
// history stays Screen 7/8's own job" simplification row.go's own
// TopBlockerType doc comment already applies to blockers).
func dedupeLatestGenerationPerRepository(repoWorkspaces []workspace.RepositoryWorkspace) []RepositoryBadgeDTO {
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
		// repoWorkspaces is Generation-ascending within each RepositoryID
		// (the port's own documented ORDER BY), so the last write for a
		// given key here is always its own highest Generation.
		latest[repositoryID] = rw
	}
	badges := make([]RepositoryBadgeDTO, 0, len(order))
	for _, repositoryID := range order {
		rw := latest[repositoryID]
		badges = append(badges, RepositoryBadgeDTO{RepositoryID: repositoryID, State: string(rw.State)})
	}
	return badges
}
