package workspacereconcile_test

// V12-33 (ADR-065): the aw-private task documents against real sqlite and a real Git worktree. The fixture lives in this package
// (realFixture); the code under test is internal/app/awdocuments and internal/adapters/gitworktree.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/awdocuments"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAwDocuments_GitIgnoresThemAndExportCopiesThemIntoDocs(t *testing.T) {
	f := newRealFixture(t, "awdocs.db")
	ctx := context.Background()
	dir := f.workingDirectory(t)
	d := awdocuments.Deps{UnitOfWork: f.uow, Store: f.provider}

	writeFile(t, filepath.Join(dir, ".aw/features/quote/tasks/T-01/plan.md"), "## Files\n- a.go\n")
	writeFile(t, filepath.Join(dir, ".aw/features/quote/tasks/T-01/frame.md"), "# Frame\n")
	// Provisioning excluded .aw/: git sees nothing to commit, so neither a commit nor the scope check takes them.
	if status := runReconcileFixtureGit(t, dir, "status", "--porcelain", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Fatalf("git status = %q, want the .aw/ documents ignored", status)
	}

	list, err := awdocuments.ListDocuments(ctx, d, f.projectID, string(f.rw.ID))
	if err != nil || len(list.Documents) != 2 || list.Documents[1].Path != ".aw/features/quote/tasks/T-01/plan.md" ||
		list.Documents[1].Content != "## Files\n- a.go\n" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := awdocuments.ListDocuments(ctx, d, "another-project", string(f.rw.ID)); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("another project's worktree: %v", err)
	}

	// An existing different file refuses the whole export.
	writeFile(t, filepath.Join(dir, "docs/features/quote/tasks/T-01/frame.md"), "# Older frame\n")
	result, err := awdocuments.Export(ctx, d, runAppCommand(f, "export-1", "ExportAwDocuments"), f.projectID, string(f.rw.ID), false)
	if !errors.Is(err, awdocuments.ErrConflicts) || len(result.Conflicts) != 1 || len(result.Exported) != 0 {
		t.Fatalf("export with a conflict = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs/features/quote/tasks/T-01/plan.md")); !os.IsNotExist(err) {
		t.Fatal("a refused export must write nothing")
	}
	result, err = awdocuments.Export(ctx, d, runAppCommand(f, "export-2", "ExportAwDocuments"), f.projectID, string(f.rw.ID), true)
	if err != nil || len(result.Exported) != 2 {
		t.Fatalf("export with overwrite = %+v, %v", result, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "docs/features/quote/tasks/T-01/frame.md")); string(got) != "# Frame\n" {
		t.Fatalf("exported frame.md = %q", got)
	}
	status := runReconcileFixtureGit(t, dir, "status", "--porcelain", "--untracked-files=all")
	if !strings.Contains(status, "docs/features/quote/tasks/T-01/plan.md") || strings.Contains(status, ".aw/") {
		t.Fatalf("after export git status = %q, want the docs/ copies only", status)
	}
	if again, err := awdocuments.Export(ctx, d, runAppCommand(f, "export-2", "ExportAwDocuments"), f.projectID, string(f.rw.ID), true); err != nil || len(again.Exported) != 2 {
		t.Fatalf("a replayed export = %+v, %v", again, err)
	}

	// Export writes in the worktree: refused while an attempt holds its write lease.
	if err := sqlite.SeedFixtureWriteLease(ctx, f.store, f.projectID, f.familyID, f.workItemID, f.repositoryID, string(f.rw.ID), f.rw.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := awdocuments.Export(ctx, d, runAppCommand(f, "export-3", "ExportAwDocuments"), f.projectID, string(f.rw.ID), true); !errors.Is(err, awdocuments.ErrWriterActive) {
		t.Fatalf("export with a writer: %v", err)
	}
}
