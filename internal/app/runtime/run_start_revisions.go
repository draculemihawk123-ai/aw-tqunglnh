package runtime

import (
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// startRevisionSet returns the revisions a new Run starts from (V9-16).
//
// A WorkspaceSet's BaseRevisionSet is computed once, when every repository of
// the family first reaches READY, and never changes: it says where the family
// began. A family then moves — aw makes a local commit after each task, and a
// quarantined worktree is replaced by a newer generation — so pinning that
// base into the next Run's manifest hands the Run a revision that is no longer
// the worktree's HEAD. Every MACHINE_GATE of such a Run then fails its
// freshness check (the gate compares the worktree's live HEAD with the pinned
// revision), and the base the Run reports is not the one its changes sit on.
//
// For each repository of the base set this takes the CurrentRevision and the
// generation of the newest READY RepositoryWorkspace of that repository in the
// set (a local commit advances CurrentRevision, see
// AdvanceRepositoryWorkspaceRevision). A repository with no READY workspace —
// quarantined, still provisioning — or one that has not recorded a revision
// keeps its base entry, which is exactly what every Run used before.
func startRevisionSet(base workspace.RevisionSet, workspaces []workspace.RepositoryWorkspace) (workspace.RevisionSet, error) {
	newest := make(map[string]workspace.RepositoryWorkspace, len(workspaces))
	for _, candidate := range workspaces {
		if candidate.State != workspace.RepositoryWorkspaceReady || candidate.CurrentRevision == "" {
			continue
		}
		key := string(candidate.RepositoryID)
		if held, found := newest[key]; !found || candidate.Generation > held.Generation {
			newest[key] = candidate
		}
	}

	entries := base.Entries()
	for index, entry := range entries {
		if current, found := newest[string(entry.RepositoryID)]; found {
			entries[index] = workspace.Revision{
				RepositoryID: entry.RepositoryID, VCSObjectID: current.CurrentRevision, WorkspaceGeneration: current.Generation,
			}
		}
	}
	return workspace.NewRevisionSet(entries)
}
