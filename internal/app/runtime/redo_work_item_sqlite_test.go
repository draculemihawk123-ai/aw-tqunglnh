package runtime_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V11-10 (docs/design/14-v11-bugfix.md, ADR-041): RetryWorkItem and RecreateWorkItem on a real *sqlite.Store, through the real
// commands. A failed run is produced by the same failRun helper the V9-06 rerun tests use.

func (f *rerunSQLite) redoDeps() runtime.RedoDeps {
	f.t.Helper()
	store, err := artifactstore.New(f.t.TempDir())
	if err != nil {
		f.t.Fatalf("artifactstore.New: %v", err)
	}
	return runtime.RedoDeps{UnitOfWork: f.uow, ArtifactStore: store, IDs: f.ids, Clock: clock.System{}, Matcher: redact.NewMatcher()}
}

func redoCommand(key, commandType string) ports.Command {
	return testCommand(key, "hash-"+key, ports.ProjectScope("project-1"), commandType)
}

func (f *rerunSQLite) retry(key, note string) (runtime.RetryWorkItemResult, error) {
	f.t.Helper()
	return runtime.RetryWorkItem(f.ctx, f.redoDeps(), redoCommand(key, "RetryWorkItem"), runtime.RetryWorkItemRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, Note: note,
	})
}

func (f *rerunSQLite) recreate(key string, carry bool, note string) (runtime.RecreateWorkItemResult, error) {
	f.t.Helper()
	return runtime.RecreateWorkItem(f.ctx, f.redoDeps(), redoCommand(key, "RecreateWorkItem"), runtime.RecreateWorkItemRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, Reason: "the kit was fixed", Note: note, CarryMessages: carry,
	})
}

func (f *rerunSQLite) workItem(id string) workdomain.WorkItem {
	f.t.Helper()
	var item workdomain.WorkItem
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(f.ctx, id)
		return err
	}); err != nil {
		f.t.Fatalf("GetWorkItem(%s): %v", id, err)
	}
	return item
}

// messageTexts returns the text of every message of the WorkItem in sequence order, with its role.
func (f *rerunSQLite) messageTexts(workItemID string) []string {
	f.t.Helper()
	var texts []string
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		messages, err := tx.Messages().ListMessagesForWorkItem(f.ctx, workItemID)
		if err != nil {
			return err
		}
		for _, m := range messages {
			texts = append(texts, string(m.Role)+":"+string(m.ContentArtifactID))
		}
		return nil
	}); err != nil {
		f.t.Fatalf("ListMessagesForWorkItem(%s): %v", workItemID, err)
	}
	return texts
}

func (f *rerunSQLite) appendMessage(role message.Role, text, key string) appmessage.AppendMessageResult {
	f.t.Helper()
	deps := f.redoDeps()
	result, err := appmessage.AppendMessage(f.ctx, f.uow, deps.ArtifactStore, f.ids, clock.System{}, redoCommand(key, "AppendMessage"), appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, Role: role, Content: []byte(text), ContentType: "text/plain", Sensitivity: redact.Public,
	})
	if err != nil {
		f.t.Fatalf("AppendMessage: %v", err)
	}
	return result
}

func (f *rerunSQLite) openBlockerTypes(workItemID string) []string {
	f.t.Helper()
	var types []string
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		blockers, err := tx.Work().ListWorkItemBlockersForWorkItem(f.ctx, workItemID)
		for _, b := range blockers {
			if b.State == workdomain.BlockerOpen {
				types = append(types, string(b.Type))
			}
		}
		return err
	}); err != nil {
		f.t.Fatalf("ListWorkItemBlockersForWorkItem: %v", err)
	}
	return types
}

func (f *rerunSQLite) runsOf(workItemID string) []runtimedomain.WorkflowRun {
	f.t.Helper()
	var runs []runtimedomain.WorkflowRun
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		var err error
		runs, err = tx.Runtime().ListWorkflowRunsForWorkItem(f.ctx, workItemID)
		return err
	}); err != nil {
		f.t.Fatalf("ListWorkflowRunsForWorkItem(%s): %v", workItemID, err)
	}
	return runs
}

// --- RetryWorkItem ---

// A failed run, a RUN_FAILED blocker, and one action: the note becomes a USER message, the blocker is RESOLVED and a new run starts
// on the pinned version of the SAME WorkItem.
func TestRetryWorkItem_SQLite_OneActionNoteBlockerAndRun(t *testing.T) {
	f := newRerunSQLite(t)
	first := f.mustStart("idem-start-1")
	f.failRun(first)
	blocker := f.onlyOpenRunFailedBlocker()

	result, err := f.retry("idem-retry-1", "use the fixed script this time")
	if err != nil {
		t.Fatalf("RetryWorkItem: %v", err)
	}
	if result.WorkItemID != f.workItemID || result.PreviousRunID != first.RunID || result.RunID == "" || result.RunID == first.RunID {
		t.Fatalf("result = %+v, want a new run of the same work item after %s", result, first.RunID)
	}
	if result.WorkflowVersionID != string(f.version.ID()) || result.RunState != string(runtimedomain.WorkflowRunRunning) {
		t.Fatalf("result = %+v, want the pinned version %s and a running run", result, f.version.ID())
	}
	if !reflect.DeepEqual(result.ResolvedBlockerIDs, []string{string(blocker.ID)}) || result.NoteMessageID == "" {
		t.Fatalf("result = %+v, want blocker %s resolved and a note message", result, blocker.ID)
	}
	if got := f.item().Status; got != workdomain.WorkItemActive {
		t.Fatalf("work item status = %s, want ACTIVE (the new run is live)", got)
	}
	if got := len(f.runs()); got != 2 {
		t.Fatalf("runs = %d, want 2", got)
	}
	for _, b := range f.blockers() {
		if b.ID == blocker.ID && b.State != workdomain.BlockerResolved {
			t.Fatalf("blocker %s = %s, want RESOLVED", b.ID, b.State)
		}
	}
	if texts := f.messageTexts(f.workItemID); len(texts) != 1 || !strings.HasPrefix(texts[0], "USER:") {
		t.Fatalf("messages = %v, want exactly the one USER note", texts)
	}
	if got := f.eventCount(runtime.WorkItemRetriedEventType); got != 1 {
		t.Fatalf("%s events = %d, want 1", runtime.WorkItemRetriedEventType, got)
	}
}

// Without a note the command writes no message; the blocker's resolution still says it was a retry.
func TestRetryWorkItem_SQLite_NoNoteWritesNoMessage(t *testing.T) {
	f := newRerunSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	result, err := f.retry("idem-retry-1", "  ")
	if err != nil {
		t.Fatalf("RetryWorkItem: %v", err)
	}
	if result.NoteMessageID != "" || len(f.messageTexts(f.workItemID)) != 0 {
		t.Fatalf("result = %+v, messages = %v, want no note message", result, f.messageTexts(f.workItemID))
	}
}

// The same key replays the first result with no second run, message or event; the same key with another request is a conflict.
func TestRetryWorkItem_SQLite_ReplayAndConflict(t *testing.T) {
	f := newRerunSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	first, err := f.retry("idem-retry-1", "note")
	if err != nil {
		t.Fatalf("RetryWorkItem: %v", err)
	}
	again, err := f.retry("idem-retry-1", "note")
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("replay = %+v, %v; want the first result %+v", again, err, first)
	}
	if got := len(f.runs()); got != 2 {
		t.Fatalf("runs after replay = %d, want 2", got)
	}
	if got := len(f.messageTexts(f.workItemID)); got != 1 {
		t.Fatalf("messages after replay = %d, want 1", got)
	}
	if got := f.eventCount(runtime.WorkItemRetriedEventType); got != 1 {
		t.Fatalf("%s events after replay = %d, want 1", runtime.WorkItemRetriedEventType, got)
	}

	conflicting := redoCommand("idem-retry-1", "RetryWorkItem")
	conflicting.RequestHash = "another-hash"
	_, err = runtime.RetryWorkItem(f.ctx, f.redoDeps(), conflicting, runtime.RetryWorkItemRequest{ProjectID: "project-1", WorkItemID: f.workItemID, Note: "other"})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("same key, different request = %v, want ErrReceiptConflict", err)
	}
}

// A blocker the command may not resolve leaves EVERYTHING as it was: no blocker resolved, no message, no run.
func TestRetryWorkItem_SQLite_StillBlocked_ChangesNothing(t *testing.T) {
	f := newRerunSQLite(t)
	first := f.mustStart("idem-start-1")
	f.failRun(first)
	failed := f.onlyOpenRunFailedBlocker()
	item := f.item()
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		other, err := workdomain.NewWorkItemBlocker("blocker-scope", item.ProjectID, item.ID, workdomain.BlockerScopeExpansionRequired, first.RunID, "", "", "needs more scope", time.Now().UTC())
		if err != nil {
			return err
		}
		_, err = tx.Work().CreateWorkItemBlocker(f.ctx, other)
		return err
	}); err != nil {
		t.Fatalf("seed a scope expansion blocker: %v", err)
	}

	_, err := f.retry("idem-retry-1", "try again")
	if !errors.Is(err, runtime.ErrWorkItemStillBlocked) {
		t.Fatalf("retry with a scope expansion blocker open = %v, want ErrWorkItemStillBlocked", err)
	}
	if !strings.Contains(err.Error(), "SCOPE_EXPANSION_REQUIRED") {
		t.Fatalf("error %q should name the blocker that remains", err)
	}
	if got := f.onlyOpenRunFailedBlocker(); got.Version != failed.Version {
		t.Fatalf("RUN_FAILED blocker = %+v, want untouched (the resolve rolled back)", got)
	}
	if got, want := len(f.runs()), 1; got != want {
		t.Fatalf("runs = %d, want %d", got, want)
	}
	if got := len(f.messageTexts(f.workItemID)); got != 0 {
		t.Fatalf("messages = %d, want none (the note rolled back)", got)
	}
	if got := f.eventCount(runtime.WorkItemRetriedEventType); got != 0 {
		t.Fatalf("%s events = %d, want 0", runtime.WorkItemRetriedEventType, got)
	}
}

func TestRetryWorkItem_SQLite_Refusals(t *testing.T) {
	t.Run("a work item that never ran", func(t *testing.T) {
		f := newRerunSQLite(t)
		if _, err := f.retry("idem-retry-1", ""); !errors.Is(err, runtime.ErrWorkItemNeverRan) {
			t.Fatalf("retry before any run = %v, want ErrWorkItemNeverRan", err)
		}
	})
	t.Run("a run that is still going", func(t *testing.T) {
		f := newRerunSQLite(t)
		f.mustStart("idem-start-1")
		if _, err := f.retry("idem-retry-1", ""); !errors.Is(err, runtime.ErrWorkItemNotRetryable) {
			t.Fatalf("retry while ACTIVE = %v, want ErrWorkItemNotRetryable", err)
		}
	})
	t.Run("another project", func(t *testing.T) {
		f := newRerunSQLite(t)
		f.failRun(f.mustStart("idem-start-1"))
		_, err := runtime.RetryWorkItem(f.ctx, f.redoDeps(), redoCommand("idem-retry-1", "RetryWorkItem"), runtime.RetryWorkItemRequest{
			ProjectID: "project-2", WorkItemID: f.workItemID,
		})
		if !errors.Is(err, ports.ErrScopeMismatch) {
			t.Fatalf("retry through another project = %v, want ErrScopeMismatch", err)
		}
	})
	t.Run("a quarantined workspace", func(t *testing.T) {
		f := newRerunSQLite(t)
		f.failRun(f.mustStart("idem-start-1"))
		f.quarantineSecondRepository()
		if _, err := f.retry("idem-retry-1", "note"); !errors.Is(err, runtime.ErrWorkspaceQuarantined) {
			t.Fatalf("retry with a QUARANTINED workspace = %v, want ErrWorkspaceQuarantined", err)
		}
		if got := f.onlyOpenRunFailedBlocker(); got.State != workdomain.BlockerOpen {
			t.Fatalf("blocker = %+v, want still OPEN", got)
		}
	})
}

// quarantineSecondRepository adds a QUARANTINED repository workspace to the fixture's family (the way the V9-06 precondition test does).
func (f *rerunSQLite) quarantineSecondRepository() {
	f.t.Helper()
	item := f.item()
	var setID string
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		set, err := tx.Work().GetWorkspaceSetByFamilyID(f.ctx, string(item.FamilyID))
		setID = string(set.ID)
		return err
	}); err != nil {
		f.t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	registerSecondRepositorySQLite(f.t, f.uow, f.ids)
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		_, err := tx.Work().CreateRepositoryWorkspace(f.ctx, workspace.RepositoryWorkspace{
			ID: workspace.RepositoryWorkspaceID(f.ids.NewID()), WorkspaceSetID: workspace.WorkspaceSetID(setID),
			RepositoryID: project.RepositoryID("repo-2"), Generation: 1, Locator: "handle-repo-2",
			BaseRevision: "cafebabecafebabecafebabecafebabecafebabe", State: workspace.RepositoryWorkspaceQuarantined, Version: 1,
		})
		return err
	}); err != nil {
		f.t.Fatalf("seed quarantined repository workspace: %v", err)
	}
}

// --- RecreateWorkItem ---

// newRecreateSQLite is the rerun fixture with the work moved onto a CHILD of the root: a READY child whose contract pins version 1
// and whose effective scope is repo-1. Recreate only works on a child.
func newRecreateSQLite(t *testing.T) (*rerunSQLite, string) {
	t.Helper()
	f := newRerunSQLite(t)
	rootID := f.workItemID
	cmd := redoCommand("idem-child-1", "CreateChildWorkItem")
	child, err := work.CreateChildWorkItem(f.ctx, f.uow, f.ids, cmd, work.CreateChildWorkItemRequest{
		ParentWorkItemID: rootID, Title: "T-01: add the endpoint", ParentJoinPolicy: "ALL_CHILDREN_DONE",
		EffectiveScope: []work.ScopeGrantRequest{{RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"**"}, Reason: "task T-01"}},
		Contract: &work.WorkItemContractRequest{
			SchemaVersion: 1, Behavior: "the endpoint answers 200", VerificationSpec: "run the tests", RiskLevel: "LOW",
			AcceptanceCriteria: []work.AcceptanceCriterionRequest{{Description: "tests pass", VerificationRef: "cmd:test"}},
			Exclusions:         []string{"no schema changes"}, WorkflowVersionID: string(f.version.ID()),
		},
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem: %v", err)
	}
	if _, err := work.MarkWorkItemReady(f.ctx, f.uow, redoCommand("idem-child-ready", "MarkWorkItemReady"), work.MarkWorkItemReadyRequest{WorkItemID: child.WorkItemID}); err != nil {
		t.Fatalf("MarkWorkItemReady(child): %v", err)
	}
	f.workItemID = child.WorkItemID
	return f, rootID
}

// publishNewerWorkflowVersion publishes version 2 of the fixture's workflow definition (different dependency hash, so a different
// compiled content) and returns its id.
func (f *rerunSQLite) publishNewerWorkflowVersion() string {
	f.t.Helper()
	pid := project.ProjectID("project-1")
	def := workflow.WorkflowDefinition{ID: "wf-def-1", ProjectID: &pid, Name: "workflow wf-def-1", Status: workflow.DefinitionActive, Version: 1}
	candidate, err := workflow.Compile(def, workflow.PublishRequest{
		VersionID: "wf-v-2", VersionNumber: 2, Document: f.version.Document(),
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{{Kind: "skill", Key: "implement", Version: "2", Hash: "sha256:dependency-2"}}},
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		f.t.Fatalf("compile workflow v2: %v", err)
	}
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		_, err := tx.Definitions().PublishWorkflowVersion(f.ctx, def, candidate)
		return err
	}); err != nil {
		f.t.Fatalf("publish workflow v2: %v", err)
	}
	return "wf-v-2"
}

// The whole redo in one action: the old child is CANCELLED, a sibling with the same content exists under the same parent, pinned to the
// newest version, READY then started; operator messages are carried (and only those); the link is readable both ways.
func TestRecreateWorkItem_SQLite_CancelsAndCreatesASiblingOnTheNewestVersion(t *testing.T) {
	f, rootID := newRecreateSQLite(t)
	oldID := f.workItemID
	f.failRun(f.mustStart("idem-start-1"))
	f.appendMessage(message.RoleUser, "remember the schema is frozen", "idem-msg-user")
	f.appendMessage(message.RoleTool, "log of the failed run", "idem-msg-tool")
	newer := f.publishNewerWorkflowVersion()

	result, err := f.recreate("idem-recreate-1", true, "start from a clean state")
	if err != nil {
		t.Fatalf("RecreateWorkItem: %v", err)
	}
	if result.OldWorkItemID != oldID || result.NewWorkItemID == "" || result.NewWorkItemID == oldID || result.ParentWorkItemID != rootID {
		t.Fatalf("result = %+v, want a new sibling under %s", result, rootID)
	}
	if result.PreviousWorkflowVersionID != string(f.version.ID()) || result.WorkflowVersionID != newer {
		t.Fatalf("versions = %s -> %s, want %s -> %s", result.PreviousWorkflowVersionID, result.WorkflowVersionID, f.version.ID(), newer)
	}
	if result.Title != "T-01: add the endpoint (lần 2)" || result.RedoNumber != 2 || result.OldWorkItemStatus != string(workdomain.WorkItemCancelled) {
		t.Fatalf("result = %+v, want title suffix, redo number 2 and the old item CANCELLED", result)
	}
	if result.CarriedMessageCount != 1 || result.NoteMessageID == "" {
		t.Fatalf("result = %+v, want one carried operator message and the new note", result)
	}

	old := f.workItem(oldID)
	if old.Status != workdomain.WorkItemCancelled {
		t.Fatalf("old status = %s, want CANCELLED", old.Status)
	}
	created := f.workItem(result.NewWorkItemID)
	if created.Status != workdomain.WorkItemActive || created.ParentID == nil || string(*created.ParentID) != rootID || created.FamilyID != old.FamilyID {
		t.Fatalf("new item = %+v, want an ACTIVE sibling in the same family under %s", created, rootID)
	}
	if created.WorkflowVersionID == nil || string(*created.WorkflowVersionID) != newer {
		t.Fatalf("new item pins %v, want %s", created.WorkflowVersionID, newer)
	}
	if created.Behavior != old.Behavior || created.VerificationSpec != old.VerificationSpec || created.RiskLevel != old.RiskLevel ||
		created.SchemaVersion != old.SchemaVersion || !reflect.DeepEqual(created.AcceptanceCriteria, old.AcceptanceCriteria) ||
		!reflect.DeepEqual(created.Exclusions, old.Exclusions) || created.ParentJoinPolicy != old.ParentJoinPolicy {
		t.Fatalf("contract differs: old %+v new %+v", old, created)
	}
	var oldScopes, newScopes []workdomain.RepositoryScope
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		var err error
		if oldScopes, err = tx.Work().ListWorkItemEffectiveScopes(f.ctx, oldID); err != nil {
			return err
		}
		newScopes, err = tx.Work().ListWorkItemEffectiveScopes(f.ctx, result.NewWorkItemID)
		return err
	}); err != nil {
		t.Fatalf("ListWorkItemEffectiveScopes: %v", err)
	}
	if len(newScopes) != 1 || len(oldScopes) != 1 || newScopes[0].RepositoryID() != oldScopes[0].RepositoryID() ||
		newScopes[0].Access() != oldScopes[0].Access() || !reflect.DeepEqual(newScopes[0].PathScopes(), oldScopes[0].PathScopes()) {
		t.Fatalf("effective scope: old %+v new %+v, want the same", oldScopes, newScopes)
	}

	runs := f.runsOf(result.NewWorkItemID)
	if len(runs) != 1 || string(runs[0].ID) != result.RunID || string(runs[0].WorkflowVersionID) != newer || runs[0].State != runtimedomain.WorkflowRunRunning {
		t.Fatalf("runs of the new item = %+v, want one running run on %s", runs, newer)
	}
	if texts := f.messageTexts(result.NewWorkItemID); len(texts) != 2 || !strings.HasPrefix(texts[0], "USER:") || !strings.HasPrefix(texts[1], "USER:") {
		t.Fatalf("messages of the new item = %v, want the carried USER message and the new note", texts)
	}
	if texts := f.messageTexts(oldID); len(texts) != 2 {
		t.Fatalf("messages of the old item = %v, want its own two untouched", texts)
	}

	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		byOld, err := tx.Work().GetWorkItemSupersessionByOld(f.ctx, oldID)
		if err != nil {
			return err
		}
		byNew, err := tx.Work().GetWorkItemSupersessionByNew(f.ctx, result.NewWorkItemID)
		if err != nil {
			return err
		}
		if byOld != byNew || string(byOld.NewWorkItemID) != result.NewWorkItemID || byOld.Reason != "the kit was fixed" || byOld.Actor != "actor-1" {
			t.Fatalf("supersession = %+v / %+v", byOld, byNew)
		}
		return nil
	}); err != nil {
		t.Fatalf("read the supersession: %v", err)
	}
	if got := f.eventCount(runtime.WorkItemRecreatedEventType); got != 1 {
		t.Fatalf("%s events = %d, want 1", runtime.WorkItemRecreatedEventType, got)
	}
}

// With no newer version the redo is a clean redo on the same version, and not carrying messages copies none.
func TestRecreateWorkItem_SQLite_SameVersionAndNoCarry(t *testing.T) {
	f, _ := newRecreateSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	f.appendMessage(message.RoleUser, "an operator note", "idem-msg-user")

	result, err := f.recreate("idem-recreate-1", false, "")
	if err != nil {
		t.Fatalf("RecreateWorkItem: %v", err)
	}
	if result.WorkflowVersionID != string(f.version.ID()) || result.PreviousWorkflowVersionID != result.WorkflowVersionID {
		t.Fatalf("versions = %s -> %s, want the same version", result.PreviousWorkflowVersionID, result.WorkflowVersionID)
	}
	if result.CarriedMessageCount != 0 || len(f.messageTexts(result.NewWorkItemID)) != 0 {
		t.Fatalf("result = %+v, messages = %v, want none carried", result, f.messageTexts(result.NewWorkItemID))
	}
}

// Redoing a redo counts up and does not stack the title suffix.
func TestRecreateWorkItem_SQLite_RedoOfARedo(t *testing.T) {
	f, _ := newRecreateSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	second, err := f.recreate("idem-recreate-1", true, "")
	if err != nil {
		t.Fatalf("first redo: %v", err)
	}
	f.workItemID = second.NewWorkItemID
	f.failRun(runtime.StartWorkflowRunResult{RunID: second.RunID, NodeRunID: second.NodeRunID, WorkItemID: second.NewWorkItemID})
	third, err := f.recreate("idem-recreate-2", true, "")
	if err != nil {
		t.Fatalf("second redo: %v", err)
	}
	if third.RedoNumber != 3 || third.Title != "T-01: add the endpoint (lần 3)" {
		t.Fatalf("third = %+v, want redo number 3 and a single suffix", third)
	}
}

// The same key returns the same new work item and creates nothing more.
func TestRecreateWorkItem_SQLite_ReplayCreatesNothingTwice(t *testing.T) {
	f, rootID := newRecreateSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	first, err := f.recreate("idem-recreate-1", true, "")
	if err != nil {
		t.Fatalf("RecreateWorkItem: %v", err)
	}
	again, err := f.recreate("idem-recreate-1", true, "")
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("replay = %+v, %v; want %+v", again, err, first)
	}
	var children []workdomain.WorkItem
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		var err error
		children, err = tx.Work().ListChildWorkItems(f.ctx, rootID)
		return err
	}); err != nil {
		t.Fatalf("ListChildWorkItems: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2 (the original and one replacement)", len(children))
	}
	if got := f.eventCount(runtime.WorkItemRecreatedEventType); got != 1 {
		t.Fatalf("%s events after replay = %d, want 1", runtime.WorkItemRecreatedEventType, got)
	}
}

func TestRecreateWorkItem_SQLite_Refusals(t *testing.T) {
	t.Run("a root work item", func(t *testing.T) {
		f := newRerunSQLite(t)
		f.failRun(f.mustStart("idem-start-1"))
		if _, err := f.recreate("idem-recreate-1", true, ""); !errors.Is(err, runtime.ErrRecreateRootUnsupported) {
			t.Fatalf("recreate a root = %v, want ErrRecreateRootUnsupported", err)
		}
	})
	t.Run("a run that is still going changes nothing", func(t *testing.T) {
		f, rootID := newRecreateSQLite(t)
		f.mustStart("idem-start-1")
		if _, err := f.recreate("idem-recreate-1", true, ""); !errors.Is(err, runtime.ErrWorkItemHasNonTerminalRun) {
			t.Fatalf("recreate with a live run = %v, want ErrWorkItemHasNonTerminalRun", err)
		}
		if got := f.item().Status; got != workdomain.WorkItemActive {
			t.Fatalf("old status = %s, want ACTIVE untouched", got)
		}
		var children []workdomain.WorkItem
		_ = f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
			var err error
			children, err = tx.Work().ListChildWorkItems(f.ctx, rootID)
			return err
		})
		if len(children) != 1 {
			t.Fatalf("children = %d, want only the original", len(children))
		}
	})
	t.Run("a quarantined workspace", func(t *testing.T) {
		f, _ := newRecreateSQLite(t)
		f.failRun(f.mustStart("idem-start-1"))
		f.quarantineSecondRepository()
		if _, err := f.recreate("idem-recreate-1", true, ""); !errors.Is(err, runtime.ErrWorkspaceQuarantined) {
			t.Fatalf("recreate with a QUARANTINED workspace = %v, want ErrWorkspaceQuarantined", err)
		}
		if got := f.item().Status; got != workdomain.WorkItemBlocked {
			t.Fatalf("old status = %s, want BLOCKED untouched", got)
		}
	})
	t.Run("already replaced", func(t *testing.T) {
		f, _ := newRecreateSQLite(t)
		f.failRun(f.mustStart("idem-start-1"))
		first, err := f.recreate("idem-recreate-1", true, "")
		if err != nil {
			t.Fatalf("RecreateWorkItem: %v", err)
		}
		_, err = f.recreate("idem-recreate-2", true, "")
		if !errors.Is(err, runtime.ErrWorkItemAlreadySuperseded) || !strings.Contains(err.Error(), first.NewWorkItemID) {
			t.Fatalf("recreate an already replaced item = %v, want ErrWorkItemAlreadySuperseded naming %s", err, first.NewWorkItemID)
		}
	})
	t.Run("a reason is required", func(t *testing.T) {
		f, _ := newRecreateSQLite(t)
		f.failRun(f.mustStart("idem-start-1"))
		_, err := runtime.RecreateWorkItem(f.ctx, f.redoDeps(), redoCommand("idem-recreate-1", "RecreateWorkItem"), runtime.RecreateWorkItemRequest{
			ProjectID: "project-1", WorkItemID: f.workItemID,
		})
		if err == nil {
			t.Fatal("recreate without a reason succeeded")
		}
	})
}

// Starting a run with a version other than the pinned one is still refused (V11-10 must not weaken ADR-033's pin).
func TestStartWorkflowRun_SQLite_StillRefusesAnotherVersionThanThePinned(t *testing.T) {
	f := newRerunSQLite(t)
	newer := f.publishNewerWorkflowVersion()
	if _, err := f.startWith("idem-start-other", newer); !errors.Is(err, runtime.ErrWorkflowVersionMismatch) {
		t.Fatalf("start with another version = %v, want ErrWorkflowVersionMismatch", err)
	}
}

// --- AssessRedo ---

func (f *rerunSQLite) assess() runtime.RedoAssessment {
	f.t.Helper()
	got, err := runtime.AssessRedo(f.ctx, f.uow, "project-1", f.workItemID)
	if err != nil {
		f.t.Fatalf("AssessRedo: %v", err)
	}
	return got
}

// The assessment says, without changing anything, what each command would do and why one would refuse.
func TestAssessRedo_SQLite_ReportsEligibilityVersionsAndLineage(t *testing.T) {
	f, rootID := newRecreateSQLite(t)

	// before any run: nothing to retry, but a redo is possible (a READY child pins a version)
	got := f.assess()
	if got.Retry == nil || got.Retry.Code != runtime.RedoRefusalNeverRan {
		t.Fatalf("retry before any run = %+v, want NEVER_RAN", got.Retry)
	}

	first := f.mustStart("idem-start-1")
	if got := f.assess(); got.Retry == nil || got.Retry.Code != runtime.RedoRefusalNotRetryable || got.Recreate == nil || got.Recreate.Code != runtime.RedoRefusalRunInProgress {
		t.Fatalf("while running: retry %+v recreate %+v, want NOT_RETRYABLE / RUN_IN_PROGRESS", got.Retry, got.Recreate)
	}

	f.failRun(first)
	f.appendMessage(message.RoleUser, "an operator note", "idem-msg-user")
	newer := f.publishNewerWorkflowVersion()
	got = f.assess()
	if got.Retry != nil || got.Recreate != nil {
		t.Fatalf("after a failed run: retry %+v recreate %+v, want both allowed", got.Retry, got.Recreate)
	}
	if got.PinnedWorkflowVersionID != string(f.version.ID()) || got.PinnedVersionNumber != 1 || got.LatestWorkflowVersionID != newer || got.LatestVersionNumber != 2 {
		t.Fatalf("versions = %+v", got)
	}
	if len(got.BlockersToResolve) != 1 || got.BlockersToResolve[0].Type != string(workdomain.BlockerRunFailed) || got.OperatorMessageCount != 1 || got.RedoNumber != 2 {
		t.Fatalf("assessment = %+v, want the RUN_FAILED blocker, one operator message and redo number 2", got)
	}
	if len(got.Scopes) != 1 || got.Scopes[0].RepositoryID() != "repo-1" || got.Scopes[0].Access() != workdomain.RepositoryWrite {
		t.Fatalf("scopes = %+v", got.Scopes)
	}
	if !got.IsChild || got.LineageRunIDs[0] != first.RunID {
		t.Fatalf("assessment = %+v", got)
	}
	_ = rootID

	// after a redo the old item is superseded and the new one knows what it replaced, with both histories in its lineage
	redone, err := f.recreate("idem-recreate-1", true, "")
	if err != nil {
		t.Fatalf("RecreateWorkItem: %v", err)
	}
	old, err := runtime.AssessRedo(f.ctx, f.uow, "project-1", f.workItemID)
	if err != nil {
		t.Fatalf("AssessRedo(old): %v", err)
	}
	if old.SupersededByWorkItemID != redone.NewWorkItemID || old.Recreate == nil || old.Recreate.Code != runtime.RedoRefusalNotRecreatable && old.Recreate.Code != runtime.RedoRefusalAlreadySuperseded {
		t.Fatalf("old item = %+v, want it superseded by %s and not recreatable", old, redone.NewWorkItemID)
	}
	f.workItemID = redone.NewWorkItemID
	fresh := f.assess()
	if fresh.SupersedesWorkItemID == "" || fresh.RedoNumber != 3 || len(fresh.LineageRunIDs) != 2 || fresh.LineageRunIDs[1] != redone.RunID {
		t.Fatalf("new item = %+v, want it replacing the old one, redo number 3 and both runs in its lineage", fresh)
	}
}

func TestAssessRedo_SQLite_RootAndForeignProject(t *testing.T) {
	f := newRerunSQLite(t)
	f.failRun(f.mustStart("idem-start-1"))
	got := f.assess()
	if got.IsChild || got.Recreate == nil || got.Recreate.Code != runtime.RedoRefusalRootUnsupported || got.Retry != nil {
		t.Fatalf("root: %+v, want retry allowed and recreate ROOT_UNSUPPORTED", got)
	}
	if _, err := runtime.AssessRedo(f.ctx, f.uow, "project-2", f.workItemID); !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("assess through another project = %v, want ErrScopeMismatch", err)
	}
	if runtime.RedoRefusalCode(errors.New("boom")) != "" {
		t.Fatal("an unrelated error must have no refusal code")
	}
}
