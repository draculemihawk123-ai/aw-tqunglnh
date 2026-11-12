package catalog_test

// V12-33 (ADR-065): the project's task documents location, against a real sqlite store.

import (
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
)

func TestTaskDocuments_DefaultsToTheRepositoryAndCanMoveToAw(t *testing.T) {
	f := newLifecycleFixture(t)
	view, err := catalog.GetTaskDocuments(f.ctx, f.uow, "p1")
	if err != nil || view.Location != catalog.TaskDocumentsRepo || view.DirectoryPattern != "docs/features/<feature>/tasks/<task>" || view.Version != 0 {
		t.Fatalf("default = %+v, %v", view, err)
	}
	if _, err := catalog.SetTaskDocuments(f.ctx, f.uow, f.cmd("SetProjectTaskDocuments", "td-bad", 0), "p1", "elsewhere"); !errors.Is(err, catalog.ErrInvalidTaskDocuments) {
		t.Fatalf("a bad location: %v", err)
	}
	set, err := catalog.SetTaskDocuments(f.ctx, f.uow, f.cmd("SetProjectTaskDocuments", "td-1", 0), "p1", "aw")
	if err != nil || set.Location != catalog.TaskDocumentsAw || set.DirectoryPattern != ".aw/features/<feature>/tasks/<task>" || set.Version != 1 {
		t.Fatalf("set = %+v, %v", set, err)
	}
	if again, err := catalog.SetTaskDocuments(f.ctx, f.uow, f.cmd("SetProjectTaskDocuments", "td-1", 0), "p1", "aw"); err != nil || again.Version != 1 {
		t.Fatalf("a replay = %+v, %v", again, err)
	}
	if view, _ := catalog.GetTaskDocuments(f.ctx, f.uow, "p1"); view.Location != catalog.TaskDocumentsAw {
		t.Fatalf("after set = %+v", view)
	}
	if catalog.TaskDocumentsDirectory(catalog.TaskDocumentsAw, "quote", "T-01") != ".aw/features/quote/tasks/T-01" {
		t.Fatal("TaskDocumentsDirectory")
	}
}
