package runtime_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/workitemredo"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V11-10: the service around the redo commands — reading the worktree for files outside the scope, parking them before the command, and
// the preview the dialogs show. Git is replaced by a stub that holds a list of changed files.

type stubWorktree struct {
	ports.WorkspaceInspectionReader
	files     []ports.WorkingTreeFileChange
	parkCalls [][]string
}

func (w *stubWorktree) ReadWorkingTreeDiff(_ context.Context, _ ports.ReadWorkingTreeDiffRequest) (ports.WorkingTreeDiff, error) {
	return ports.WorkingTreeDiff{Files: append([]ports.WorkingTreeFileChange(nil), w.files...), TotalFiles: len(w.files)}, nil
}

func (w *stubWorktree) ParkChangedPaths(_ context.Context, _ ports.WorkspaceHandle, ref string, paths []string) (ports.ParkedChanges, error) {
	w.parkCalls = append(w.parkCalls, append([]string(nil), paths...))
	gone := map[string]bool{}
	for _, p := range paths {
		gone[p] = true
	}
	var left []ports.WorkingTreeFileChange
	for _, f := range w.files {
		if !gone[f.Path] {
			left = append(left, f)
		}
	}
	w.files = left
	return ports.ParkedChanges{Ref: ref, Paths: paths}, nil
}

func changed(paths ...string) []ports.WorkingTreeFileChange {
	var files []ports.WorkingTreeFileChange
	for _, p := range paths {
		files = append(files, ports.WorkingTreeFileChange{Path: p, Status: ports.WorkingTreeFileAdded, Untracked: true})
	}
	return files
}

// service builds the service over the fixture with a stub worktree holding paths. The WorkItem may write under docs/ only.
func (f *rerunSQLite) service(withParker bool, paths ...string) (workitemredo.Service, *stubWorktree) {
	f.t.Helper()
	f.narrowScopeToDocs()
	return f.serviceOver(withParker, paths...)
}

// serviceOver is service without narrowing the scope: the fixture's own path scope matches no path, so every file is out of scope.
func (f *rerunSQLite) serviceOver(withParker bool, paths ...string) (workitemredo.Service, *stubWorktree) {
	f.t.Helper()
	stub := &stubWorktree{files: changed(paths...)}
	service := workitemredo.Service{RedoDeps: f.redoDeps(), Inspection: workspaceinspection.New(f.uow, stub)}
	if withParker {
		service.Parker = stub
	}
	return service, stub
}

// narrowScopeToDocs replaces the fixture's effective scope (whose path scope "**" matches nothing, the scope guard compares path
// prefixes) with WRITE on docs/ only, once per WorkItem. The row is removed with a second connection because no command shrinks a scope.
func (f *rerunSQLite) narrowScopeToDocs() {
	f.t.Helper()
	var narrowed bool
	_ = f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		scopes, err := tx.Work().ListWorkItemEffectiveScopes(f.ctx, f.workItemID)
		narrowed = err == nil && len(scopes) == 1 && reflect.DeepEqual(scopes[0].PathScopes(), []string{"docs"})
		return err
	})
	if narrowed {
		return
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(f.dbPath))
	if err != nil {
		f.t.Fatalf("open a second connection: %v", err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(f.ctx, `DELETE FROM work_item_effective_scopes WHERE work_item_id = ?`, f.workItemID); err != nil {
		f.t.Fatalf("remove the fixture scope: %v", err)
	}
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(f.ctx, f.workItemID)
		if err != nil {
			return err
		}
		scope, err := workdomain.NewRepositoryScope(item.FamilyID, 1, "repo-1", workdomain.RepositoryWrite, []string{"docs"}, "docs only", "actor-1", time.Now().UTC())
		if err != nil {
			return err
		}
		_, err = tx.Work().AddEffectiveScope(f.ctx, f.workItemID, scope)
		return err
	}); err != nil {
		f.t.Fatalf("add the docs scope: %v", err)
	}
}

func retryRequest(f *rerunSQLite, disposition string) workitemredo.RetryRequest {
	return workitemredo.RetryRequest{ProjectID: "project-1", WorkItemID: f.workItemID, Note: "try again", OutOfScope: disposition}
}

func TestService_Preview_ListsLeftoverFilesAndCosts(t *testing.T) {
	f := newRerunSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	service, _ := f.service(true, "docs/spec.md", "notes/stray.md")

	preview, err := service.Preview(f.ctx, "project-1", f.workItemID)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !preview.CanRetry || preview.CanRecreate || preview.RecreateRefusal == nil || preview.RecreateRefusal.Code != runtime.RedoRefusalRootUnsupported {
		t.Fatalf("preview = %+v, want retry allowed and recreate ROOT_UNSUPPORTED (the fixture item is a root)", preview)
	}
	if preview.PriorRunCount != 1 || preview.CostKnown || len(preview.BlockersToResolve) != 1 || preview.RedoNumber != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.OutOfScopeCount != 1 || len(preview.Files) != 2 || preview.FilesUnavailableReason != "" {
		t.Fatalf("files = %+v (%d out of scope, unavailable %q), want 2 files of which notes/stray.md is out of scope", preview.Files, preview.OutOfScopeCount, preview.FilesUnavailableReason)
	}
	for _, file := range preview.Files {
		if want := file.Path == "notes/stray.md"; file.OutOfScope != want {
			t.Errorf("%s outOfScope = %v, want %v", file.Path, file.OutOfScope, want)
		}
	}

	// without an inspection reader the preview says so instead of failing
	blind := workitemredo.Service{RedoDeps: f.redoDeps()}
	preview, err = blind.Preview(f.ctx, "project-1", f.workItemID)
	if err != nil || preview.FilesUnavailableReason == "" || len(preview.Files) != 0 {
		t.Fatalf("preview without an inspection reader = %+v, %v; want an unavailable reason and no files", preview, err)
	}
}

func TestService_Retry_NeedsADecisionForFilesOutsideTheScope(t *testing.T) {
	f := newRerunSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	service, stub := f.service(true, "docs/spec.md", "notes/stray.md")

	_, err := service.Retry(f.ctx, redoCommand("idem-retry-1", "RetryWorkItem"), retryRequest(f, ""))
	var needs *workitemredo.OutOfScopeDecisionError
	if !errors.As(err, &needs) || len(needs.Files) != 1 || needs.Files[0].Path != "notes/stray.md" {
		t.Fatalf("retry without a choice = %v, want OutOfScopeDecisionError naming notes/stray.md", err)
	}
	if len(stub.parkCalls) != 0 || len(f.runs()) != 1 || f.onlyOpenRunFailedBlocker().State != workdomain.BlockerOpen {
		t.Fatalf("a refused retry must change nothing: parks %v, runs %d", stub.parkCalls, len(f.runs()))
	}
	if _, err := service.Retry(f.ctx, redoCommand("idem-retry-2", "RetryWorkItem"), retryRequest(f, "DELETE")); !errors.Is(err, workitemredo.ErrInvalidDisposition) {
		t.Fatalf("retry with an unknown choice = %v, want ErrInvalidDisposition", err)
	}
}

func TestService_Retry_KeepLeavesFilesAndRecordsTheChoice(t *testing.T) {
	f := newRerunSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	service, stub := f.service(true, "notes/stray.md")

	result, err := service.Retry(f.ctx, redoCommand("idem-retry-1", "RetryWorkItem"), retryRequest(f, workitemredo.Keep))
	if err != nil {
		t.Fatalf("Retry(KEEP): %v", err)
	}
	if result.OutOfScope != workitemredo.Keep || len(result.Parked) != 0 || len(stub.parkCalls) != 0 || len(f.runs()) != 2 {
		t.Fatalf("result = %+v, parks %v, runs %d; want KEEP recorded, nothing parked and a new run", result, stub.parkCalls, len(f.runs()))
	}
}

func TestService_Retry_ParkMovesOnlyTheOutOfScopeFilesBeforeTheRun(t *testing.T) {
	f := newRerunSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	service, stub := f.service(true, "docs/spec.md", "notes/stray.md", "tmp/x.log")

	result, err := service.Retry(f.ctx, redoCommand("idem-retry-1", "RetryWorkItem"), retryRequest(f, workitemredo.Park))
	if err != nil {
		t.Fatalf("Retry(PARK): %v", err)
	}
	if want := [][]string{{"notes/stray.md", "tmp/x.log"}}; !reflect.DeepEqual(stub.parkCalls, want) {
		t.Fatalf("parked %v, want only the out-of-scope files %v", stub.parkCalls, want)
	}
	if result.OutOfScope != workitemredo.Park || len(result.Parked) != 1 || result.Parked[0].RepositoryID != "repo-1" ||
		!reflect.DeepEqual(result.Parked[0].Paths, []string{"notes/stray.md", "tmp/x.log"}) || result.Parked[0].Ref == "" {
		t.Fatalf("result.Parked = %+v", result.Parked)
	}
	if left := stub.files; len(left) != 1 || left[0].Path != "docs/spec.md" {
		t.Fatalf("the in-scope file must stay for the next run: %+v", left)
	}

	// the same request again is a replay: same result, nothing parked a second time
	again, err := service.Retry(f.ctx, redoCommand("idem-retry-1", "RetryWorkItem"), retryRequest(f, workitemredo.Park))
	if err != nil || !reflect.DeepEqual(again, result) || len(stub.parkCalls) != 1 {
		t.Fatalf("replay = %+v, %v, parks %v; want the first result and no second park", again, err, stub.parkCalls)
	}
}

func TestService_Retry_ParkWithoutAProviderOrForARefusedCommandChangesNothing(t *testing.T) {
	f := newRerunSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	service, stub := f.service(false, "notes/stray.md")
	if _, err := service.Retry(f.ctx, redoCommand("idem-retry-1", "RetryWorkItem"), retryRequest(f, workitemredo.Park)); !errors.Is(err, workitemredo.ErrParkUnavailable) {
		t.Fatalf("PARK without a provider = %v, want ErrParkUnavailable", err)
	}
	if len(f.runs()) != 1 {
		t.Fatalf("runs = %d, want 1", len(f.runs()))
	}

	// a retry the command would refuse parks nothing: the WorkItem is already running again
	service, stub = f.service(true, "notes/stray.md")
	if _, err := service.Retry(f.ctx, redoCommand("idem-retry-2", "RetryWorkItem"), retryRequest(f, workitemredo.Keep)); err != nil {
		t.Fatalf("Retry(KEEP): %v", err)
	}
	if _, err := service.Retry(f.ctx, redoCommand("idem-retry-3", "RetryWorkItem"), retryRequest(f, workitemredo.Park)); !errors.Is(err, runtime.ErrWorkItemNotRetryable) {
		t.Fatalf("retry while running = %v, want ErrWorkItemNotRetryable", err)
	}
	if len(stub.parkCalls) != 0 {
		t.Fatalf("a refused command must not park: %v", stub.parkCalls)
	}
}

func TestService_Recreate_ParksBeforeCreatingTheReplacement(t *testing.T) {
	f, _ := newRecreateSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	service, stub := f.serviceOver(true, "docs/spec.md", "notes/stray.md")

	_, err := service.Recreate(f.ctx, redoCommand("idem-recreate-1", "RecreateWorkItem"), workitemredo.RecreateRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, Reason: "kit fixed", CarryMessages: true,
	})
	var needs *workitemredo.OutOfScopeDecisionError
	if !errors.As(err, &needs) {
		t.Fatalf("recreate without a choice = %v, want OutOfScopeDecisionError", err)
	}
	result, err := service.Recreate(f.ctx, redoCommand("idem-recreate-2", "RecreateWorkItem"), workitemredo.RecreateRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, Reason: "kit fixed", CarryMessages: true, OutOfScope: workitemredo.Park,
	})
	if err != nil {
		t.Fatalf("Recreate(PARK): %v", err)
	}
	if want := [][]string{{"docs/spec.md", "notes/stray.md"}}; !reflect.DeepEqual(stub.parkCalls, want) {
		t.Fatalf("parked %v, want %v", stub.parkCalls, want)
	}
	if result.OutOfScope != workitemredo.Park || len(result.Parked) != 1 || result.NewWorkItemID == "" {
		t.Fatalf("result = %+v", result)
	}
}
