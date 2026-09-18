package workitem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func decodeMarkReadyResult(t *testing.T, stdout *bytes.Buffer) workapp.MarkWorkItemReadyResult {
	t.Helper()
	var result workapp.MarkWorkItemReadyResult
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &result); err != nil {
		t.Fatalf("decode MarkWorkItemReadyResult: %v", err)
	}
	return result
}

// readyToMarkWorkItemFixture persists a WorkItem whose contract genuinely
// satisfies workdomain.ValidateReadinessGate, at BACKLOG/Version 1, sharing
// a real family/project pair — the precondition every mark-ready test in
// this file needs (a fresh CreateRootWorkItem's own contract is always
// empty, per internal/app/work/queries.go's own doc comment, so it can
// never itself reach READY without this same "build the value directly"
// escape hatch fixture_test.go's own fullyContractedWorkItem already uses
// for readiness_test.go). Deliberately generic over deps.UoW (never
// type-asserted to *fake.UnitOfWork): every helper this calls
// (rootWorkItemFixture/persistWorkItem) already accepts the plain
// ports.UnitOfWork interface, so this same fixture works unmodified against
// either the fake (most tests in this file) or a real sqlite-backed store
// (TestRunWorkItemMarkReady_ConcurrentDoubleMarkReady_ExactlyOneWinner
// below — see that test's own doc comment for why it needs real sqlite).
func readyToMarkWorkItemFixture(t *testing.T, deps cliworkitem.Dependencies) (workItemID string) {
	t.Helper()
	root := rootWorkItemFixture(t, deps.UoW, deps.IDs, "project-1", "repo-1")
	item := fullyContractedWorkItem("wi-markready", project.ProjectID("project-1"), workdomain.TaskFamilyID(root.FamilyID), workdomain.WorkItemBacklog)
	persistWorkItem(t, deps.UoW, item)
	return string(item.ID)
}

func TestRunWorkItemMarkReady_TransitionsBacklogToReady(t *testing.T) {
	deps := newTestDeps(t)
	workItemID := readyToMarkWorkItemFixture(t, deps)

	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", "1", "--idempotency-key", "key-mark-1", workItemID}
	if err := cliworkitem.RunWorkItemMarkReady(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunWorkItemMarkReady() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeMarkReadyResult(t, &stdout)
	if result.Status != string(workdomain.WorkItemReady) || result.Version != 2 {
		t.Fatalf("result = %+v, want Status=READY Version=2", result)
	}

	item := workItemState(t, deps.UoW, workItemID)
	if item.Status != workdomain.WorkItemReady {
		t.Fatalf("work item status = %s, want READY", item.Status)
	}
}

// TestRunWorkItemMarkReady_NotReady_ReturnsReadinessError proves an
// incomplete-contract WorkItem is rejected with the real
// *workdomain.ReadinessError, never silently marked ready — the identical
// validator ExplainWorkItemReadiness itself runs, reused rather than
// reinvented.
func TestRunWorkItemMarkReady_NotReady_ReturnsReadinessError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")
	incomplete := fullyContractedWorkItem("wi-incomplete-mark", project.ProjectID("project-1"), workdomain.TaskFamilyID(root.FamilyID), workdomain.WorkItemBacklog)
	incomplete.Behavior = ""
	persistWorkItem(t, u, incomplete)

	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", "1", "wi-incomplete-mark"}
	err := cliworkitem.RunWorkItemMarkReady(context.Background(), deps, args, &stdout, &stderr)
	var readinessErr *workdomain.ReadinessError
	if !errors.As(err, &readinessErr) {
		t.Fatalf("error = %v, want a *workdomain.ReadinessError", err)
	}

	item := workItemState(t, deps.UoW, "wi-incomplete-mark")
	if item.Status != workdomain.WorkItemBacklog || item.Version != 1 {
		t.Fatalf("work item after rejected mark-ready = %+v, want unchanged BACKLOG@1", item)
	}
}

// TestRunWorkItemMarkReady_StaleExpectedVersion_Rejected is this task's own
// "stale" Verify bullet: a genuinely fresh (non-replayed) mark-ready call
// naming a --expected-version that no longer matches the WorkItem's own
// real current version is rejected, never silently applied against
// whatever the current version happens to be.
func TestRunWorkItemMarkReady_StaleExpectedVersion_Rejected(t *testing.T) {
	deps := newTestDeps(t)
	workItemID := readyToMarkWorkItemFixture(t, deps)

	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", "99", workItemID}
	err := cliworkitem.RunWorkItemMarkReady(context.Background(), deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunWorkItemMarkReady() with a stale --expected-version returned nil error")
	}
	if isUsageError(err) {
		t.Fatalf("error = %v, want a state-conflict error (not a usage error)", err)
	}

	item := workItemState(t, deps.UoW, workItemID)
	if item.Status != workdomain.WorkItemBacklog || item.Version != 1 {
		t.Fatalf("work item after rejected mark-ready = %+v, want unchanged BACKLOG@1", item)
	}
}

func TestRunWorkItemMarkReady_MissingExpectedVersion_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	workItemID := readyToMarkWorkItemFixture(t, deps)

	var stdout, stderr bytes.Buffer
	err := cliworkitem.RunWorkItemMarkReady(context.Background(), deps, []string{workItemID}, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunWorkItemMarkReady() with no --expected-version returned %v, want a cli.UsageError", err)
	}
}

// TestRunWorkItemMarkReady_ReplaySameKey_WinsOverStateDrift mirrors
// internal/delivery/cli/catalog/repository_test.go's own
// TestRunRepositoryRetryProbe_ReplaySameKey_WinsOverStateDrift: the FIRST
// mark-ready call (BACKLOG->READY) succeeds; a SECOND call with the
// identical --idempotency-key — now that the WorkItem has already moved
// past BACKLOG — must still replay the original accepted result, never
// re-run the CAS or reject with a stale-version conflict.
func TestRunWorkItemMarkReady_ReplaySameKey_WinsOverStateDrift(t *testing.T) {
	deps := newTestDeps(t)
	workItemID := readyToMarkWorkItemFixture(t, deps)
	args := []string{"--expected-version", "1", "--idempotency-key", "key-mark-replay", workItemID}

	var first bytes.Buffer
	if err := cliworkitem.RunWorkItemMarkReady(context.Background(), deps, args, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunWorkItemMarkReady() error = %v", err)
	}

	var second bytes.Buffer
	if err := cliworkitem.RunWorkItemMarkReady(context.Background(), deps, args, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second (replay) RunWorkItemMarkReady() error = %v, want nil (must replay, not re-check version)", err)
	}
	if !replayedField(t, second.String()) {
		t.Fatal("second call reported Replayed = false, want true")
	}
	firstResult := decodeMarkReadyResult(t, &first)
	secondResult := decodeMarkReadyResult(t, &second)
	if secondResult != firstResult {
		t.Fatalf("replay result = %+v, want the exact original %+v", secondResult, firstResult)
	}

	item := workItemState(t, deps.UoW, workItemID)
	if item.Version != 2 {
		t.Fatalf("work item Version = %d, want 2 (the replay must never re-run the CAS a second time)", item.Version)
	}
}

// TestRunWorkItemMarkReady_ConcurrentDoubleMarkReady_ExactlyOneWinner is
// this task's own "concurrency" Verify bullet: several concurrent
// mark-ready calls against the SAME WorkItem, each with its own distinct
// idempotency key (so none of them can trivially replay another), must
// converge on exactly one real BACKLOG->READY transition — the underlying
// optimistic-concurrency CAS inside workapp.MarkWorkItemReady's own
// TransitionWorkItemStatus call is what actually enforces this. This test
// deliberately uses a REAL sqlite-backed store (newSQLiteTestDeps below),
// not the in-memory fake: fake.UnitOfWork's own WithSerializedWrite
// (internal/app/ports/fake/unitofwork.go) only detects and rejects a
// genuinely-concurrent second caller with a synthetic ErrNestedTransaction
// (a boolean in-progress flag, not a real queue/lock) rather than
// serializing it — confirmed by observation: a version of this test built
// on fake.UnitOfWork flaked intermittently with exactly that error under
// real goroutine concurrency, the same reason
// internal/delivery/httpapi/recovery/resolveblocker_test.go's own
// TestResolveWorkItemBlocker_HTTP_ConcurrentResolveRace_ExactlyOneFreshDecision
// already uses a real sqlite-backed server rather than the fake for its own
// concurrency proof.
func TestRunWorkItemMarkReady_ConcurrentDoubleMarkReady_ExactlyOneWinner(t *testing.T) {
	deps := newSQLiteTestDeps(t, "mark-ready-race.db")
	workItemID := readyToMarkWorkItemFixture(t, deps)

	const attempts = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		i := i
		go func() {
			defer wg.Done()
			var stdout, stderr bytes.Buffer
			args := []string{"--expected-version", "1", "--idempotency-key", fmt.Sprintf("key-mark-race-%d", i), workItemID}
			err := cliworkitem.RunWorkItemMarkReady(context.Background(), deps, args, &stdout, &stderr)
			mu.Lock()
			defer mu.Unlock()
			errs = append(errs, err)
		}()
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded = %d across %d concurrent racers, want exactly 1", succeeded, attempts)
	}

	item := workItemState(t, deps.UoW, workItemID)
	if item.Status != workdomain.WorkItemReady || item.Version != 2 {
		t.Fatalf("work item after race = %+v, want exactly one real READY@2 transition", item)
	}
}
