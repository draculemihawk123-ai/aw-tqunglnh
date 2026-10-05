package runtime

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func mustRevisionSet(t *testing.T, entries ...workspace.Revision) workspace.RevisionSet {
	t.Helper()
	set, err := workspace.NewRevisionSet(entries)
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}
	return set
}

func readyWorkspace(repositoryID string, generation uint64, state workspace.RepositoryWorkspaceState, currentRevision string) workspace.RepositoryWorkspace {
	return workspace.RepositoryWorkspace{
		RepositoryID: project.RepositoryID(repositoryID), Generation: generation, State: state, CurrentRevision: currentRevision,
	}
}

func TestStartRevisionSet(t *testing.T) {
	base := mustRevisionSet(t,
		workspace.Revision{RepositoryID: "repo-a", VCSObjectID: "aaaa-base", WorkspaceGeneration: 1},
		workspace.Revision{RepositoryID: "repo-b", VCSObjectID: "bbbb-base", WorkspaceGeneration: 1},
	)

	tests := []struct {
		name       string
		workspaces []workspace.RepositoryWorkspace
		want       map[string]workspace.Revision
	}{
		{
			name:       "no workspaces keeps the base set",
			workspaces: nil,
			want: map[string]workspace.Revision{
				"repo-a": {RepositoryID: "repo-a", VCSObjectID: "aaaa-base", WorkspaceGeneration: 1},
				"repo-b": {RepositoryID: "repo-b", VCSObjectID: "bbbb-base", WorkspaceGeneration: 1},
			},
		},
		{
			name: "a READY workspace that committed moves its repository to the new HEAD",
			workspaces: []workspace.RepositoryWorkspace{
				readyWorkspace("repo-a", 1, workspace.RepositoryWorkspaceReady, "aaaa-commit-1"),
				readyWorkspace("repo-b", 1, workspace.RepositoryWorkspaceReady, "bbbb-base"),
			},
			want: map[string]workspace.Revision{
				"repo-a": {RepositoryID: "repo-a", VCSObjectID: "aaaa-commit-1", WorkspaceGeneration: 1},
				"repo-b": {RepositoryID: "repo-b", VCSObjectID: "bbbb-base", WorkspaceGeneration: 1},
			},
		},
		{
			name: "a workspace that never recorded a revision keeps the base entry",
			workspaces: []workspace.RepositoryWorkspace{
				readyWorkspace("repo-a", 1, workspace.RepositoryWorkspaceReady, ""),
			},
			want: map[string]workspace.Revision{
				"repo-a": {RepositoryID: "repo-a", VCSObjectID: "aaaa-base", WorkspaceGeneration: 1},
				"repo-b": {RepositoryID: "repo-b", VCSObjectID: "bbbb-base", WorkspaceGeneration: 1},
			},
		},
		{
			name: "the newest READY generation wins over an older one",
			workspaces: []workspace.RepositoryWorkspace{
				readyWorkspace("repo-a", 1, workspace.RepositoryWorkspaceReady, "aaaa-gen1"),
				readyWorkspace("repo-a", 2, workspace.RepositoryWorkspaceReady, "aaaa-gen2"),
			},
			want: map[string]workspace.Revision{
				"repo-a": {RepositoryID: "repo-a", VCSObjectID: "aaaa-gen2", WorkspaceGeneration: 2},
				"repo-b": {RepositoryID: "repo-b", VCSObjectID: "bbbb-base", WorkspaceGeneration: 1},
			},
		},
		{
			name: "a quarantined workspace is ignored, an older READY generation is used",
			workspaces: []workspace.RepositoryWorkspace{
				readyWorkspace("repo-a", 1, workspace.RepositoryWorkspaceReady, "aaaa-gen1"),
				readyWorkspace("repo-a", 2, workspace.RepositoryWorkspaceQuarantined, "aaaa-gen2"),
			},
			want: map[string]workspace.Revision{
				"repo-a": {RepositoryID: "repo-a", VCSObjectID: "aaaa-gen1", WorkspaceGeneration: 1},
				"repo-b": {RepositoryID: "repo-b", VCSObjectID: "bbbb-base", WorkspaceGeneration: 1},
			},
		},
		{
			name: "only quarantined workspaces keep the base entry",
			workspaces: []workspace.RepositoryWorkspace{
				readyWorkspace("repo-a", 1, workspace.RepositoryWorkspaceQuarantined, "aaaa-gen1"),
			},
			want: map[string]workspace.Revision{
				"repo-a": {RepositoryID: "repo-a", VCSObjectID: "aaaa-base", WorkspaceGeneration: 1},
				"repo-b": {RepositoryID: "repo-b", VCSObjectID: "bbbb-base", WorkspaceGeneration: 1},
			},
		},
		{
			name: "a workspace of a repository outside the base set adds nothing",
			workspaces: []workspace.RepositoryWorkspace{
				readyWorkspace("repo-z", 1, workspace.RepositoryWorkspaceReady, "zzzz"),
			},
			want: map[string]workspace.Revision{
				"repo-a": {RepositoryID: "repo-a", VCSObjectID: "aaaa-base", WorkspaceGeneration: 1},
				"repo-b": {RepositoryID: "repo-b", VCSObjectID: "bbbb-base", WorkspaceGeneration: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := startRevisionSet(base, tc.workspaces)
			if err != nil {
				t.Fatalf("startRevisionSet: %v", err)
			}
			entries := got.Entries()
			if len(entries) != len(tc.want) {
				t.Fatalf("entries = %+v, want %d repositories", entries, len(tc.want))
			}
			for _, entry := range entries {
				want, found := tc.want[string(entry.RepositoryID)]
				if !found || entry != want {
					t.Fatalf("entry %+v, want %+v", entry, want)
				}
			}
		})
	}
}

func TestStartRevisionSet_DoesNotMutateTheBaseSet(t *testing.T) {
	base := mustRevisionSet(t, workspace.Revision{RepositoryID: "repo-a", VCSObjectID: "aaaa-base", WorkspaceGeneration: 1})
	hashBefore := base.ContentHash()

	moved, err := startRevisionSet(base, []workspace.RepositoryWorkspace{
		readyWorkspace("repo-a", 1, workspace.RepositoryWorkspaceReady, "aaaa-commit-1"),
	})
	if err != nil {
		t.Fatalf("startRevisionSet: %v", err)
	}
	if moved.ContentHash() == hashBefore {
		t.Fatal("moved set has the same content hash as the base set")
	}
	if base.ContentHash() != hashBefore {
		t.Fatal("startRevisionSet mutated the base set")
	}
	if revision, _ := base.RevisionFor("repo-a"); revision.VCSObjectID != "aaaa-base" {
		t.Fatalf("base revision = %q, want aaaa-base", revision.VCSObjectID)
	}
}
