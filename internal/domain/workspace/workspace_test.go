package workspace

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func TestRevisionSetNormalizesOrderAndIsImmutable(t *testing.T) {
	t.Parallel()

	first, err := NewRevisionSet([]Revision{
		{RepositoryID: "repo-web", VCSObjectID: " web-commit ", WorkspaceGeneration: 2},
		{RepositoryID: "repo-user", VCSObjectID: "user-commit", WorkspaceGeneration: 1},
	})
	if err != nil {
		t.Fatalf("create first revision set: %v", err)
	}
	second, err := NewRevisionSet([]Revision{
		{RepositoryID: "repo-user", VCSObjectID: "user-commit", WorkspaceGeneration: 1},
		{RepositoryID: "repo-web", VCSObjectID: "web-commit", WorkspaceGeneration: 2},
	})
	if err != nil {
		t.Fatalf("create second revision set: %v", err)
	}
	if first.ContentHash() != second.ContentHash() {
		t.Fatalf("entry order changed revision set hash: %q != %q", first.ContentHash(), second.ContentHash())
	}
	entries := first.Entries()
	if entries[0].RepositoryID != "repo-user" || entries[1].RepositoryID != "repo-web" {
		t.Fatalf("revision entries are not normalized: %#v", entries)
	}
	entries[0].VCSObjectID = "mutated"
	revision, ok := first.RevisionFor("repo-user")
	if !ok || revision.VCSObjectID != "user-commit" {
		t.Fatal("RevisionSet leaked its mutable entry slice")
	}
}

func TestRevisionSetRejectsDuplicateRepository(t *testing.T) {
	t.Parallel()

	_, err := NewRevisionSet([]Revision{
		{RepositoryID: "repo-user", VCSObjectID: "commit-1", WorkspaceGeneration: 1},
		{RepositoryID: "repo-user", VCSObjectID: "commit-2", WorkspaceGeneration: 2},
	})
	if err == nil {
		t.Fatal("duplicate repository revision was accepted")
	}
}

func TestRepositoryWorkspacesMustStayInsideTaskFamilyScope(t *testing.T) {
	t.Parallel()

	root, err := work.NewRootWorkItem("root", "project", "family", "Root")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	family, err := work.NewTaskFamily("family", root)
	if err != nil {
		t.Fatalf("create family: %v", err)
	}
	set, err := NewWorkspaceSet("workspace-set", family)
	if err != nil {
		t.Fatalf("create workspace set: %v", err)
	}
	repository := mustRepository(t, "repo-user", "project")
	otherRepository := mustRepository(t, "repo-other", "project")
	scope, err := work.NewRepositoryScope(
		family.ID,
		1,
		repository.ID,
		work.RepositoryWrite,
		nil,
		"test",
		"tester",
		time.Now(),
	)
	if err != nil {
		t.Fatalf("create scope: %v", err)
	}
	allowed := mustRepositoryWorkspace(t, "workspace-user", set, repository)
	outside := mustRepositoryWorkspace(t, "workspace-other", set, otherRepository)

	if err := ValidateRepositoryWorkspaces(set, family, []work.RepositoryScope{scope}, []RepositoryWorkspace{allowed}); err != nil {
		t.Fatalf("valid repository workspace rejected: %v", err)
	}
	if err := ValidateRepositoryWorkspaces(set, family, []work.RepositoryScope{scope}, []RepositoryWorkspace{outside}); err == nil {
		t.Fatal("repository workspace outside family scope was accepted")
	}
	if err := ValidateRepositoryWorkspaces(set, family, []work.RepositoryScope{scope}, []RepositoryWorkspace{allowed, allowed}); err == nil {
		t.Fatal("duplicate repository workspace generation was accepted")
	}
}

func mustRepository(t *testing.T, id project.RepositoryID, projectID project.ProjectID) project.Repository {
	t.Helper()
	repository, err := project.NewRepository(id, projectID, string(id), "https://example.invalid/repo.git", "main")
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	return repository
}

func mustRepositoryWorkspace(
	t *testing.T,
	id RepositoryWorkspaceID,
	set WorkspaceSet,
	repository project.Repository,
) RepositoryWorkspace {
	t.Helper()
	repositoryWorkspace, err := NewRepositoryWorkspace(id, set, repository, 1, "opaque://workspace", "branch", "base")
	if err != nil {
		t.Fatalf("create repository workspace: %v", err)
	}
	return repositoryWorkspace
}
