package scopeguard

import (
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func TestValidateDiffsAcceptsOnlyCoveredWritePaths(t *testing.T) {
	scopes := []work.RepositoryScope{
		mustScope(t, "repo-user", work.RepositoryWrite, []string{"src", "docs"}),
		mustScope(t, "repo-web", work.RepositoryRead, nil),
	}
	diffs := []ports.WorkspaceDiff{{
		RepositoryID: "repo-user",
		Files: []ports.FileStatus{
			{Code: " M", Path: "src/main.go"},
			{Code: "A ", Path: "docs/adr.md"},
		},
	}}
	if err := ValidateDiffs(scopes, diffs); err != nil {
		t.Fatalf("ValidateDiffs() error = %v", err)
	}
}

func TestValidateDiffsRejectsReadOnlyAndOutOfPathChanges(t *testing.T) {
	scopes := []work.RepositoryScope{
		mustScope(t, "repo-user", work.RepositoryWrite, []string{"src"}),
		mustScope(t, "repo-web", work.RepositoryRead, nil),
	}
	diffs := []ports.WorkspaceDiff{
		{RepositoryID: "repo-user", Files: []ports.FileStatus{{Code: " M", Path: "infra/deploy.yaml"}}},
		{RepositoryID: "repo-web", Files: []ports.FileStatus{{Code: " M", Path: "app.ts"}}},
	}
	err := ValidateDiffs(scopes, diffs)
	if !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("ValidateDiffs() error = %v, want ErrScopeViolation", err)
	}
}

func TestValidateDiffsChecksBothSidesOfRename(t *testing.T) {
	scopes := []work.RepositoryScope{mustScope(t, "repo-user", work.RepositoryWrite, []string{"src"})}
	err := ValidateDiffs(scopes, []ports.WorkspaceDiff{{
		RepositoryID: "repo-user",
		Files:        []ports.FileStatus{{Code: "R ", Path: "src/new.go", OriginalPath: "private/old.go"}},
	}})
	if !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("rename ValidateDiffs() error = %v, want ErrScopeViolation", err)
	}
}

func mustScope(t *testing.T, repository string, access work.RepositoryAccess, paths []string) work.RepositoryScope {
	t.Helper()
	scope, err := work.NewRepositoryScope(
		"family-1", 1, project.RepositoryID(repository), access, paths,
		"spike test", "tester", time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
