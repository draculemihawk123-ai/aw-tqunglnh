package runtime_test

// V6-06C's own GetRunDiagnostics coverage (diagnostics.go). Every fixture
// below reuses this package's own already-established helpers
// (admissionFixture/claimableExecuteNodeJob from admission_test.go/
// execute_test.go, runCancelledBlockerFixture/quarantineExtraRepositoryWorkspace/
// seedBlocker from resolve_work_item_blocker_test.go, cancelWorkItemFixture/
// startRun/workItemState from cancel_work_item_test.go) rather than
// duplicating them — this file lives in the SAME package (runtime_test) as
// every one of those, so no cross-package duplication is needed (unlike
// internal/delivery/httpapi/recovery's own admission_fixture_test.go, which
// had to duplicate because it is a genuinely different package). Every
// fixture drives real application commands/handlers against a real
// *fake.UnitOfWork (this codebase's own accepted in-memory ports.UnitOfWork
// double — never a direct row fabrication for the OUTCOME under test; the
// one exception, orphanedRunningAttemptFixture's own direct
// TransitionExecutionAttempt poke to RUNNING, mirrors this package's own
// already-established "poke the one primitive a real crash would otherwise
// reach" discipline (readyFixture's own BACKLOG->READY poke,
// startSecondRunForWorkItem's own ACTIVE->READY poke): no command in this
// codebase ever reaches "RUNNING Attempt with an abandoned lease" except a
// genuine process crash under a live worker pool
// (internal/integration/v5accept/crash_recovery_test.go's own ~15s
// real-wall-clock proof) — far too expensive for a unit test whose own
// subject is the diagnostic READ, not the crash detection itself, which
// V4-13's own ListOrphanedRunningExecutionAttempts already has dedicated
// coverage for).
//
// HTTP-level coverage (scope/role matrix at the wire boundary, a genuinely
// sqlite-backed end-to-end proof, ValidActions computation) lives in
// internal/delivery/httpapi/diagnostics' own test files — this file
// additionally covers the query's own business logic and the redaction
// proof directly against the typed Go result, awkward to assert precisely
// through an HTTP JSON round-trip.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// TestGetRunDiagnostics_AdmissionBlocked_ReportsBlockerProviderIsolation is
// this task's own "genuinely blocked NodeRun" fixture: admissionFixture
// (admission_test.go) driven through a real ExecuteNodeHandler.Handle call
// with a failing isolation checker, the identical real pipeline
// (StartWorkflowRun -> AdvanceRun -> ScheduleExecutableNodeRun -> claim a
// real EXECUTE_NODE job -> ExecuteNodeHandler.Handle) this package's own
// admission tests already use — never a hand-seeded blocker row.
func TestGetRunDiagnostics_AdmissionBlocked_ReportsBlockerProviderIsolation(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	isolation := fake.IsolationEnforcementChecker{Err: errors.New("test: isolation enforcement not available")}
	handler := runtime.NewExecuteNodeHandler(uow, ids, &fake.NodeExecutor{}, clock.System{}, isolation, registry, nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle (fixture setup, blocking admission): %v", err)
	}

	diag, err := runtime.GetRunDiagnostics(context.Background(), uow, isolation, registry, ports.ProjectScope("project-1"), runID)
	if err != nil {
		t.Fatalf("GetRunDiagnostics: %v", err)
	}

	if diag.RunID != runID || diag.ProjectID != "project-1" {
		t.Fatalf("diag = %+v, want RunID=%s ProjectID=project-1", diag, runID)
	}
	if len(diag.Blockers) != 1 {
		t.Fatalf("len(diag.Blockers) = %d, want 1: %+v", len(diag.Blockers), diag.Blockers)
	}
	b := diag.Blockers[0]
	if b.Type != string(workdomain.BlockerIsolationEnforcementUnavailable) || b.State != string(workdomain.BlockerOpen) {
		t.Fatalf("blocker = %+v, want type=%s state=OPEN", b, workdomain.BlockerIsolationEnforcementUnavailable)
	}
	if !b.AdmissionReason {
		t.Fatalf("blocker.AdmissionReason = false, want true for an isolation-unavailable blocker")
	}
	if b.SourceNodeRunID != nodeRunID || b.SourceNodeRunVersion == 0 {
		t.Fatalf("blocker = %+v, want SourceNodeRunID=%s and a nonzero SourceNodeRunVersion", b, nodeRunID)
	}

	if len(diag.Isolation) != 1 || diag.Isolation[0].Enforceable {
		t.Fatalf("diag.Isolation = %+v, want exactly one entry, Enforceable=false", diag.Isolation)
	}
	if len(diag.Providers) != 1 {
		t.Fatalf("diag.Providers = %+v, want exactly one entry", diag.Providers)
	}
	if !diag.Providers[0].ProviderConfigured {
		t.Fatalf("diag.Providers[0].ProviderConfigured = false, want true (registry has a matching executor)")
	}
	if diag.Providers[0].ProviderKey != string(ports.ProviderClaude) {
		t.Fatalf("diag.Providers[0].ProviderKey = %s, want %s", diag.Providers[0].ProviderKey, ports.ProviderClaude)
	}
}

// TestGetRunDiagnostics_ProviderNotConfigured_ReportsFalse proves
// ProviderConfigured reflects the REAL live agentregistry.Registry this
// query is called with, not the one the blocker's own fixture happened to
// use to get admitted in the first place — a caller diagnosing against a
// DIFFERENT (e.g. freshly-restarted, still zero-executor) `aw serve`
// process must see ProviderConfigured=false honestly.
func TestGetRunDiagnostics_ProviderNotConfigured_ReportsFalse(t *testing.T) {
	uow, ids, runID, _, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	isolation := fake.IsolationEnforcementChecker{Err: errors.New("test: isolation enforcement not available")}
	handler := runtime.NewExecuteNodeHandler(uow, ids, &fake.NodeExecutor{}, clock.System{}, isolation, registry, nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle (fixture setup, blocking admission): %v", err)
	}

	empty := agentregistry.Empty()
	diag, err := runtime.GetRunDiagnostics(context.Background(), uow, isolation, empty, ports.ProjectScope("project-1"), runID)
	if err != nil {
		t.Fatalf("GetRunDiagnostics: %v", err)
	}
	if len(diag.Providers) != 1 || diag.Providers[0].ProviderConfigured {
		t.Fatalf("diag.Providers = %+v, want exactly one entry with ProviderConfigured=false", diag.Providers)
	}
}

// TestGetRunDiagnostics_RunCancelledBlockerAndQuarantinedWorkspace covers
// two of this task's own three named "blocked/lost/quarantined" fixtures at
// once: runCancelledBlockerFixture (resolve_work_item_blocker_test.go) is
// the cheapest REAL non-admission blocker this package already establishes
// (RUN_CANCELLED, via a genuine CancelRun + CancelRunCoordinatorHandler
// drive, never hand-seeded), and quarantineExtraRepositoryWorkspace adds a
// real QUARANTINED RepositoryWorkspace row to that same WorkItem's own
// family — the identical construction resolve_work_item_blocker_test.go's
// own ErrWorkspaceQuarantined test already relies on.
func TestGetRunDiagnostics_RunCancelledBlockerAndQuarantinedWorkspace(t *testing.T) {
	uow, ids, workItemID, _, blocker := runCancelledBlockerFixture(t)
	item := workItemState(t, uow, workItemID)
	set, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(context.Background(), string(item.FamilyID))
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	quarantineExtraRepositoryWorkspace(t, uow, ids, string(set.ID))

	runID := blocker.SourceRunID
	if runID == "" {
		t.Fatalf("blocker.SourceRunID is empty: %+v", blocker)
	}

	diag, err := runtime.GetRunDiagnostics(
		context.Background(), uow, fake.IsolationEnforcementChecker{}, agentregistry.Empty(), ports.ProjectScope("project-1"), runID,
	)
	if err != nil {
		t.Fatalf("GetRunDiagnostics: %v", err)
	}

	if len(diag.Blockers) != 1 {
		t.Fatalf("len(diag.Blockers) = %d, want 1: %+v", len(diag.Blockers), diag.Blockers)
	}
	if diag.Blockers[0].Type != string(workdomain.BlockerRunCancelled) || diag.Blockers[0].AdmissionReason {
		t.Fatalf("blocker = %+v, want type=RUN_CANCELLED AdmissionReason=false", diag.Blockers[0])
	}
	// A non-admission blocker's own NodeRun cross-reference never runs
	// (RUN_CANCELLED carries no SourceNodeRunID at all — see
	// openRunCancelledBlockerTx's own call site, completion.go) — Providers/
	// Isolation both stay empty, never populated from an unrelated source.
	if len(diag.Providers) != 0 || len(diag.Isolation) != 0 {
		t.Fatalf("diag.Providers/Isolation = %+v / %+v, want both empty for a non-admission blocker", diag.Providers, diag.Isolation)
	}

	var quarantined *runtime.RepositoryWorkspaceDiagnostic
	for i := range diag.RepositoryWorkspaces {
		if diag.RepositoryWorkspaces[i].State == "QUARANTINED" {
			quarantined = &diag.RepositoryWorkspaces[i]
		}
	}
	if quarantined == nil {
		t.Fatalf("diag.RepositoryWorkspaces = %+v, want one QUARANTINED entry", diag.RepositoryWorkspaces)
	}
	if quarantined.RepositoryID != "repo-2" {
		t.Fatalf("quarantined.RepositoryID = %s, want repo-2", quarantined.RepositoryID)
	}
}

// orphanedRunningAttemptFixture forces attemptID from QUEUED to RUNNING via
// a direct TransitionExecutionAttempt poke, WITHOUT ever claiming its own
// EXECUTE_NODE job — see this file's own package doc comment for why this
// is the accepted, established way to reach this precondition in a fast
// unit test (the real path is a genuine process crash under a live worker
// pool, already covered elsewhere at integration-test cost). The resulting
// state (RUNNING attempt, zero active lease for its own EXECUTE_NODE job)
// is EXACTLY or ListOrphanedRunningExecutionAttempts' own documented real
// definition (ports/unitofwork.go) — this fixture never touches that
// query's own implementation, only the state it reads.
func orphanedRunningAttemptFixture(t *testing.T, uow *fake.UnitOfWork, attemptID string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionExecutionAttempt(context.Background(), ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: attempt.State, ExpectedVersion: attempt.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		return err
	})
	if err != nil {
		t.Fatalf("force attempt %s to RUNNING: %v", attemptID, err)
	}
}

// TestGetRunDiagnostics_OrphanedAttempt_ReportsQueueJobLeaseEvidence covers
// this task's own third named fixture: "lost". HasWriteLease/
// RepositoryWorkspaceID stay false/"" here — this package's own
// fake.RuntimeRepository.GetWriteLeaseRepositoryWorkspaceForAttempt is a
// documented stub (fake/runtime.go's own doc comment: "no fake worker pool
// exists... a test that needs the mutating/INDETERMINATE recovery path
// exercised does so against real sqlite instead") — the real-lease variant
// of this same scenario lives in
// internal/delivery/httpapi/diagnostics' own sqlite-backed tests.
func TestGetRunDiagnostics_OrphanedAttempt_ReportsQueueJobLeaseEvidence(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	_ = ids
	orphanedRunningAttemptFixture(t, uow, attemptID)

	diag, err := runtime.GetRunDiagnostics(
		context.Background(), uow, fake.IsolationEnforcementChecker{}, registry, ports.ProjectScope("project-1"), runID,
	)
	if err != nil {
		t.Fatalf("GetRunDiagnostics: %v", err)
	}
	if len(diag.OrphanedAttempts) != 1 {
		t.Fatalf("len(diag.OrphanedAttempts) = %d, want 1: %+v", len(diag.OrphanedAttempts), diag.OrphanedAttempts)
	}
	oa := diag.OrphanedAttempts[0]
	if oa.AttemptID != attemptID || oa.NodeRunID != nodeRunID {
		t.Fatalf("orphaned attempt = %+v, want AttemptID=%s NodeRunID=%s", oa, attemptID, nodeRunID)
	}
	if oa.HasWriteLease || oa.RepositoryWorkspaceID != "" {
		t.Fatalf("orphaned attempt = %+v, want HasWriteLease=false RepositoryWorkspaceID=\"\" (fake stub)", oa)
	}
	if diag.OrphanedAttemptsTruncated {
		t.Fatalf("diag.OrphanedAttemptsTruncated = true, want false for a single entry well under the cap")
	}
}

// TestGetRunDiagnostics_UnknownRun_ReturnsPersistenceNotFound proves an
// unknown RunID fails closed rather than panicking or returning a zero
// value silently.
func TestGetRunDiagnostics_UnknownRun_ReturnsPersistenceNotFound(t *testing.T) {
	uow, _, workItemID, _ := cancelWorkItemFixture(t)
	_ = workItemID
	_, err := runtime.GetRunDiagnostics(
		context.Background(), uow, fake.IsolationEnforcementChecker{}, agentregistry.Empty(), ports.ProjectScope("project-1"), "does-not-exist",
	)
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("error = %v, want ErrPersistenceNotFound", err)
	}
}

// TestGetRunDiagnostics_WrongProjectScope_ReturnsScopeMismatch is this
// task's own "role/project matrix" Verify bullet, half one: a Run that
// genuinely exists but under a DIFFERENT project than the caller's own
// scope claims must be leakage-normalized identically to a genuine
// not-found (V6-02A's own policy) at the HTTP layer — this test proves the
// underlying query itself produces the distinguishing sentinel
// (ports.ErrScopeMismatch) the delivery layer's own writeQueryError then
// folds into that identical response.
func TestGetRunDiagnostics_WrongProjectScope_ReturnsScopeMismatch(t *testing.T) {
	uow, ids, runID, _, _, registry := admissionFixture(t, admissionFixtureOptions{})
	mustCreateProject(t, uow, "project-2")

	_, err := runtime.GetRunDiagnostics(
		context.Background(), uow, fake.IsolationEnforcementChecker{}, registry, ports.ProjectScope("project-2"), runID,
	)
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("error = %v, want ErrScopeMismatch", err)
	}
	_ = ids
}

// TestGetRunDiagnostics_InstallationScope_RejectsAsScopeMismatch is this
// task's own "role/project matrix" Verify bullet, half two: this query is
// project-scoped (mirrors internal/app/runtime/queries.go's own
// requireProjectScope contract for every V6-07B query) — an
// installation-scoped caller, structurally incapable of naming a project at
// all, is rejected the identical way a genuinely wrong project is, never
// silently treated as "every project".
func TestGetRunDiagnostics_InstallationScope_RejectsAsScopeMismatch(t *testing.T) {
	uow, _, runID, _, _, registry := admissionFixture(t, admissionFixtureOptions{})
	_, err := runtime.GetRunDiagnostics(
		context.Background(), uow, fake.IsolationEnforcementChecker{}, registry, ports.InstallationScope(), runID,
	)
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("error = %v, want ErrScopeMismatch", err)
	}
}

// TestGetRunDiagnostics_NeverExposesProcessOrSecretShapedFields is this
// task's own explicit, named prohibition made mechanical: a reflect-based
// scan of every exported field name on every type GetRunDiagnostics'
// own result graph (RunDiagnostics and everything it embeds) rejects any
// field whose name even loosely suggests a PID, argv, cwd/working
// directory, or a secret/credential/token — the same defense-in-depth
// "scan the field names, don't just trust a manual read-through" technique
// this codebase already applies elsewhere (redact.Matcher's own known-
// secret registration in cmd/aw/serve.go). New fields added to any of
// these types in the future are automatically covered without this test
// needing an update.
func TestGetRunDiagnostics_NeverExposesProcessOrSecretShapedFields(t *testing.T) {
	forbidden := []string{"pid", "argv", "cwd", "workingdir", "secret", "credential", "password", "apikey"}
	types := []any{
		runtime.RunDiagnostics{}, runtime.BlockerDiagnostic{}, runtime.OrphanedAttemptDiagnostic{},
		runtime.ProviderDiagnostic{}, runtime.IsolationDiagnostic{}, runtime.RepositoryWorkspaceDiagnostic{},
	}
	for _, v := range types {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			name := strings.ToLower(rt.Field(i).Name)
			for _, bad := range forbidden {
				if strings.Contains(name, bad) {
					t.Fatalf("%s.%s looks process/secret-shaped (contains %q) — this task's own explicit prohibition", rt.Name(), rt.Field(i).Name, bad)
				}
			}
		}
	}
}
