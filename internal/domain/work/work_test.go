package work

import (
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func TestRepositoryScopeNormalizationAndFamilyInvariants(t *testing.T) {
	t.Parallel()

	family, repositories := familyFixture(t)
	approved, err := NewRepositoryScope(
		family.ID,
		1,
		repositories[0].ID,
		RepositoryWrite,
		[]string{"services\\user/./api", "services/user/api", "services/user"},
		"implement user API",
		"operator",
		time.Now(),
	)
	if err != nil {
		t.Fatalf("create approved scope: %v", err)
	}
	paths := approved.PathScopes()
	if len(paths) != 2 || paths[0] != "services/user" || paths[1] != "services/user/api" {
		t.Fatalf("unexpected normalized path scopes: %#v", paths)
	}
	paths[0] = "mutated"
	if approved.PathScopes()[0] != "services/user" {
		t.Fatal("RepositoryScope leaked a mutable path slice")
	}
	if err := ValidateFamilyScopes(family, repositories, []RepositoryScope{approved}); err != nil {
		t.Fatalf("valid family scope rejected: %v", err)
	}

	effectiveRead := mustScope(t, family.ID, repositories[0].ID, RepositoryRead, []string{"services/user/api/handler"})
	effectiveWrite := mustScope(t, family.ID, repositories[0].ID, RepositoryWrite, []string{"services/user/model"})
	if err := ValidateEffectiveScopes(family, 1, []RepositoryScope{approved}, []RepositoryScope{effectiveRead, effectiveWrite}); err != nil {
		t.Fatalf("valid effective scopes rejected: %v", err)
	}
}

func TestRepositoryScopeRejectsEscalationAndCrossProjectReferences(t *testing.T) {
	t.Parallel()

	family, repositories := familyFixture(t)
	readOnly := mustScope(t, family.ID, repositories[0].ID, RepositoryRead, []string{"services/user"})
	writeCandidate := mustScope(t, family.ID, repositories[0].ID, RepositoryWrite, []string{"services/user/api"})
	if err := ValidateEffectiveScopes(family, 1, []RepositoryScope{readOnly}, []RepositoryScope{writeCandidate}); err == nil {
		t.Fatal("WRITE effective scope was accepted against READ-only family scope")
	}

	outsidePath := mustScope(t, family.ID, repositories[0].ID, RepositoryRead, []string{"services/feed"})
	if err := ValidateEffectiveScopes(family, 1, []RepositoryScope{readOnly}, []RepositoryScope{outsidePath}); err == nil {
		t.Fatal("effective path outside approved family scope was accepted")
	}

	crossProject, err := project.NewRepository("repo-other", "project-other", "other", "https://example.invalid/other.git", "main")
	if err != nil {
		t.Fatalf("create cross-project repository: %v", err)
	}
	crossScope := mustScope(t, family.ID, crossProject.ID, RepositoryRead, nil)
	if err := ValidateFamilyScopes(family, append(repositories, crossProject), []RepositoryScope{crossScope}); err == nil {
		t.Fatal("cross-project repository scope was accepted")
	}

	_, err = NewRepositoryScope(
		family.ID,
		1,
		repositories[0].ID,
		RepositoryRead,
		[]string{"services/../secrets"},
		"invalid path",
		"operator",
		time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("parent traversal should be rejected, got %v", err)
	}
}

func TestChildWorkItemInheritsTaskFamilyAndProject(t *testing.T) {
	t.Parallel()

	root, err := NewRootWorkItem("root", "project", "family", "Root")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	child, err := NewChildWorkItem("child", root, "Child")
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.ProjectID != root.ProjectID || child.FamilyID != root.FamilyID || child.ParentID == nil || *child.ParentID != root.ID {
		t.Fatalf("child did not inherit root ownership: %#v", child)
	}
}

func familyFixture(t *testing.T) (TaskFamily, []project.Repository) {
	t.Helper()
	root, err := NewRootWorkItem("root", "project", "family", "Root")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	family, err := NewTaskFamily("family", root)
	if err != nil {
		t.Fatalf("create family: %v", err)
	}
	repository, err := project.NewRepository("repo-user", "project", "user", "https://example.invalid/user.git", "main")
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	return family, []project.Repository{repository}
}

func mustScope(
	t *testing.T,
	familyID TaskFamilyID,
	repositoryID project.RepositoryID,
	access RepositoryAccess,
	paths []string,
) RepositoryScope {
	t.Helper()
	scope, err := NewRepositoryScope(familyID, 1, repositoryID, access, paths, "test", "tester", time.Now())
	if err != nil {
		t.Fatalf("create repository scope: %v", err)
	}
	return scope
}
