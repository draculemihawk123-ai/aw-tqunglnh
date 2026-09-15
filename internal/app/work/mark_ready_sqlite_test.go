package work_test

// This file is V6-04A's own real-sqlite Verify line, made concrete against a
// real database, mirroring scope_expansion_sqlite_test.go's own rigor (real
// row-count/state assertions, never merely "the function returned an
// error"): replay, concurrency (both same-key and different-key), readiness
// rejection with the exact problem list, and the already-READY "READY
// conflict" case.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func markReadyCmd(idempotencyKey, requestHash, projectID string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "MarkWorkItemReady", RequestHash: requestHash,
	}
}

// seedReadyEligibleRootSQLite persists (via the REAL tx.Work().CreateTaskFamily/
// CreateWorkItem repository methods — never raw SQL) a root WorkItem whose
// contract is complete enough to pass workdomain.ValidateReadinessGate. No
// public application command in this codebase populates a WorkItem's
// contract fields yet (CreateRootWorkItem's own doc comment, commands.go),
// so this fixture builds the domain value directly and persists it through
// the real repository methods — exactly the escape hatch work.go's own
// "NewRootWorkItem...a caller needing a fully-contracted WorkItem today sets
// the exported fields directly...and then calls ValidateReadinessGate
// itself" doc comment describes, now backed by real sqlite persistence
// (V6-04A's own createWorkItemTx/getWorkItemTx contract round-trip fix,
// internal/adapters/sqlite/work.go).
func seedReadyEligibleRootSQLite(t *testing.T, uow ports.UnitOfWork, projectID, workItemID, familyID string) workdomain.WorkItem {
	t.Helper()
	ctx := context.Background()
	item := workdomain.WorkItem{
		ID: workdomain.WorkItemID(workItemID), ProjectID: project.ProjectID(projectID), Kind: workdomain.WorkItemRoot,
		FamilyID: workdomain.TaskFamilyID(familyID), SchemaVersion: 1, Title: "Ship the thing",
		Behavior: "Users can do the thing end to end",
		AcceptanceCriteria: []workdomain.AcceptanceCriterion{
			{Description: "the thing works", VerificationRef: "go test ./..."},
		},
		VerificationSpec: "run the full suite locally and in CI", RiskLevel: workdomain.RiskLevel("MEDIUM"),
		Status: workdomain.WorkItemBacklog, Version: 1,
	}
	family, err := workdomain.NewTaskFamily(workdomain.TaskFamilyID(familyID), item)
	if err != nil {
		t.Fatalf("NewTaskFamily: %v", err)
	}
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}
		_, err := tx.Work().CreateWorkItem(ctx, item)
		return err
	})
	if err != nil {
		t.Fatalf("seed ready-eligible root %s: %v", workItemID, err)
	}
	return item
}

func loadWorkItemSQLite(t *testing.T, uow ports.UnitOfWork, id string) workdomain.WorkItem {
	t.Helper()
	var item workdomain.WorkItem
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(context.Background(), id)
		return err
	})
	if err != nil {
		t.Fatalf("GetWorkItem(%s): %v", id, err)
	}
	return item
}

// TestMarkWorkItemReady_ContractRoundTrips proves V6-04A's own
// createWorkItemTx/getWorkItemTx contract-persistence fix actually works
// end to end against real sqlite: a WorkItem persisted with a full contract
// reads back with that exact same contract, not an empty one.
func TestMarkWorkItemReady_ContractRoundTripsSQLite(t *testing.T) {
	store := openSQLiteStore(t, "agentkit-mark-ready-contract-roundtrip.db")
	uow := sqlite.NewUnitOfWork(store)
	seedProjectSQLite(t, uow, "project-1")

	seeded := seedReadyEligibleRootSQLite(t, uow, "project-1", "work-item-1", "family-1")
	reloaded := loadWorkItemSQLite(t, uow, "work-item-1")

	if reloaded.SchemaVersion != seeded.SchemaVersion {
		t.Fatalf("reloaded SchemaVersion = %d, want %d", reloaded.SchemaVersion, seeded.SchemaVersion)
	}
	if reloaded.Behavior != seeded.Behavior {
		t.Fatalf("reloaded Behavior = %q, want %q", reloaded.Behavior, seeded.Behavior)
	}
	if reloaded.VerificationSpec != seeded.VerificationSpec {
		t.Fatalf("reloaded VerificationSpec = %q, want %q", reloaded.VerificationSpec, seeded.VerificationSpec)
	}
	if reloaded.RiskLevel != seeded.RiskLevel {
		t.Fatalf("reloaded RiskLevel = %q, want %q", reloaded.RiskLevel, seeded.RiskLevel)
	}
	if len(reloaded.AcceptanceCriteria) != 1 || reloaded.AcceptanceCriteria[0] != seeded.AcceptanceCriteria[0] {
		t.Fatalf("reloaded AcceptanceCriteria = %+v, want %+v", reloaded.AcceptanceCriteria, seeded.AcceptanceCriteria)
	}
	if err := workdomain.ValidateReadinessGate(reloaded); err != nil {
		t.Fatalf("reloaded seed item fails readiness gate: %v", err)
	}
}

// TestMarkWorkItemReady_HappyPath_TransitionsBacklogToReadySQLite is the
// core positive path: a genuinely ready-eligible WorkItem transitions
// BACKLOG->READY, Version bumps by exactly one, one WORK_ITEM_MARKED_READY
// event and one command receipt are recorded.
func TestMarkWorkItemReady_HappyPath_TransitionsBacklogToReadySQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-mark-ready-happy-path.db")
	uow := sqlite.NewUnitOfWork(store)
	seedProjectSQLite(t, uow, "project-1")
	seedReadyEligibleRootSQLite(t, uow, "project-1", "work-item-1", "family-1")

	cmd := markReadyCmd("idem-mark-1", "hash-mark-1", "project-1")
	result, err := work.MarkWorkItemReady(ctx, uow, cmd, work.MarkWorkItemReadyRequest{WorkItemID: "work-item-1"})
	if err != nil {
		t.Fatalf("MarkWorkItemReady: %v", err)
	}
	if result.Status != string(workdomain.WorkItemReady) {
		t.Fatalf("result.Status = %q, want READY", result.Status)
	}
	if result.Version != 2 {
		t.Fatalf("result.Version = %d, want 2", result.Version)
	}
	if result.ProjectID != "project-1" || result.FamilyID != "family-1" || result.WorkItemID != "work-item-1" {
		t.Fatalf("result = %+v, unexpected identity fields", result)
	}

	reloaded := loadWorkItemSQLite(t, uow, "work-item-1")
	if reloaded.Status != workdomain.WorkItemReady || reloaded.Version != 2 {
		t.Fatalf("reloaded WorkItem = %+v, want Status=READY Version=2", reloaded)
	}

	eventCount, err := store.CountDomainEventsByType(ctx, work.WorkItemMarkedReadyEventType)
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("WORK_ITEM_MARKED_READY event count = %d, want 1", eventCount)
	}
	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-mark-1")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("command receipt count = %d, want 1", receiptCount)
	}
}

// TestMarkWorkItemReady_Replay_SameKeySameResultSQLite proves a retry with
// the identical Idempotency-Key/RequestHash replays the first call's own
// stored result rather than attempting (and failing, since the WorkItem is
// no longer BACKLOG) a second real transition.
func TestMarkWorkItemReady_Replay_SameKeySameResultSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-mark-ready-replay.db")
	uow := sqlite.NewUnitOfWork(store)
	seedProjectSQLite(t, uow, "project-1")
	seedReadyEligibleRootSQLite(t, uow, "project-1", "work-item-1", "family-1")

	cmd := markReadyCmd("idem-mark-1", "hash-mark-1", "project-1")
	first, err := work.MarkWorkItemReady(ctx, uow, cmd, work.MarkWorkItemReadyRequest{WorkItemID: "work-item-1"})
	if err != nil {
		t.Fatalf("first MarkWorkItemReady: %v", err)
	}

	second, err := work.MarkWorkItemReady(ctx, uow, cmd, work.MarkWorkItemReadyRequest{WorkItemID: "work-item-1"})
	if err != nil {
		t.Fatalf("replayed MarkWorkItemReady (same key/hash) must not error: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first = %+v", second, first)
	}

	// Replay must not have attempted a second real transition: Version is
	// still exactly 2 (one bump only), exactly one event, exactly one
	// receipt row for this key.
	reloaded := loadWorkItemSQLite(t, uow, "work-item-1")
	if reloaded.Version != 2 {
		t.Fatalf("WorkItem.Version after replay = %d, want unchanged 2", reloaded.Version)
	}
	eventCount, err := store.CountDomainEventsByType(ctx, work.WorkItemMarkedReadyEventType)
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("WORK_ITEM_MARKED_READY event count after replay = %d, want unchanged 1", eventCount)
	}
	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-mark-1")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("command receipt count after replay = %d, want unchanged 1", receiptCount)
	}
}

// TestMarkWorkItemReady_SameKeyDifferentHash_ConflictsSQLite proves a
// reused Idempotency-Key with a semantically different request (a different
// RequestHash) is rejected as ports.ErrReceiptConflict — never silently
// replayed, never silently treated as a second attempt.
func TestMarkWorkItemReady_SameKeyDifferentHash_ConflictsSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-mark-ready-hash-conflict.db")
	uow := sqlite.NewUnitOfWork(store)
	seedProjectSQLite(t, uow, "project-1")
	seedReadyEligibleRootSQLite(t, uow, "project-1", "work-item-1", "family-1")

	first := markReadyCmd("idem-mark-1", "hash-mark-1", "project-1")
	if _, err := work.MarkWorkItemReady(ctx, uow, first, work.MarkWorkItemReadyRequest{WorkItemID: "work-item-1"}); err != nil {
		t.Fatalf("first MarkWorkItemReady: %v", err)
	}

	second := markReadyCmd("idem-mark-1", "hash-mark-DIFFERENT", "project-1")
	_, err := work.MarkWorkItemReady(ctx, uow, second, work.MarkWorkItemReadyRequest{WorkItemID: "work-item-1"})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("MarkWorkItemReady (same key, different hash) err = %v, want ports.ErrReceiptConflict", err)
	}
}

// TestMarkWorkItemReady_AlreadyReady_NewKeyIsRealConflictSQLite is this
// task's own explicit "READY conflict" spec line made concrete: once a
// WorkItem is genuinely READY, a FRESH Idempotency-Key (never seen before —
// not a replay of the call that made it READY) must fail with
// ErrWorkItemNotEligibleForReady, a real conflict, never a silent no-op and
// never a second WORK_ITEM_MARKED_READY event.
func TestMarkWorkItemReady_AlreadyReady_NewKeyIsRealConflictSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-mark-ready-already-ready.db")
	uow := sqlite.NewUnitOfWork(store)
	seedProjectSQLite(t, uow, "project-1")
	seedReadyEligibleRootSQLite(t, uow, "project-1", "work-item-1", "family-1")

	firstCmd := markReadyCmd("idem-mark-1", "hash-mark-1", "project-1")
	if _, err := work.MarkWorkItemReady(ctx, uow, firstCmd, work.MarkWorkItemReadyRequest{WorkItemID: "work-item-1"}); err != nil {
		t.Fatalf("first MarkWorkItemReady: %v", err)
	}

	secondCmd := markReadyCmd("idem-mark-2", "hash-mark-2", "project-1")
	_, err := work.MarkWorkItemReady(ctx, uow, secondCmd, work.MarkWorkItemReadyRequest{WorkItemID: "work-item-1"})
	if !errors.Is(err, work.ErrWorkItemNotEligibleForReady) {
		t.Fatalf("second (fresh key) MarkWorkItemReady err = %v, want work.ErrWorkItemNotEligibleForReady", err)
	}

	reloaded := loadWorkItemSQLite(t, uow, "work-item-1")
	if reloaded.Version != 2 {
		t.Fatalf("WorkItem.Version after rejected second attempt = %d, want unchanged 2", reloaded.Version)
	}
	eventCount, err := store.CountDomainEventsByType(ctx, work.WorkItemMarkedReadyEventType)
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("WORK_ITEM_MARKED_READY event count = %d, want unchanged 1 (no second event for a rejected attempt)", eventCount)
	}
	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-mark-2")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("command receipt count for the rejected second key = %d, want 0 (a failed command must remain retryable)", receiptCount)
	}
}

// TestMarkWorkItemReady_ReadinessGateFailure_RejectedWithSameProblemsAsExplainSQLite
// is this task's own explicit "readiness" Verify-line requirement: a
// WorkItem that does NOT pass workdomain.ValidateReadinessGate (here, one
// created via the real CreateRootWorkItem command, which — per commands.go's
// own doc comment — always starts with an empty contract) is rejected with
// the SAME problem list ExplainWorkItemReadiness would report for the
// identical WorkItem — reusing that exact logic/vocabulary, never a second,
// reinvented check. No transition, no event, no receipt.
func TestMarkWorkItemReady_ReadinessGateFailure_RejectedWithSameProblemsAsExplainSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-mark-ready-not-ready.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	root := seedRootFixtureSQLite(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	explanation, err := work.ExplainWorkItemReadiness(ctx, uow, ports.ProjectScope("project-1"), root.WorkItemID)
	if err != nil {
		t.Fatalf("ExplainWorkItemReadiness: %v", err)
	}
	if explanation.Ready {
		t.Fatalf("fixture root unexpectedly passes readiness — test fixture invariant broken")
	}
	if len(explanation.Problems) == 0 {
		t.Fatal("ExplainWorkItemReadiness reported zero problems for a not-ready item")
	}

	cmd := markReadyCmd("idem-mark-1", "hash-mark-1", "project-1")
	_, err = work.MarkWorkItemReady(ctx, uow, cmd, work.MarkWorkItemReadyRequest{WorkItemID: root.WorkItemID})
	var readinessErr *workdomain.ReadinessError
	if !errors.As(err, &readinessErr) {
		t.Fatalf("MarkWorkItemReady err = %v (%T), want *workdomain.ReadinessError", err, err)
	}
	if len(readinessErr.Problems) != len(explanation.Problems) {
		t.Fatalf("MarkWorkItemReady problems = %v, want the exact same set ExplainWorkItemReadiness reported = %v",
			readinessErr.Problems, explanation.Problems)
	}
	for i, want := range explanation.Problems {
		if readinessErr.Problems[i] != want {
			t.Fatalf("MarkWorkItemReady problems = %v, want %v (same order/vocabulary as ExplainWorkItemReadiness)",
				readinessErr.Problems, explanation.Problems)
		}
	}

	reloaded := loadWorkItemSQLite(t, uow, root.WorkItemID)
	if reloaded.Status != workdomain.WorkItemBacklog || reloaded.Version != 1 {
		t.Fatalf("reloaded WorkItem = %+v, want unchanged Status=BACKLOG Version=1", reloaded)
	}
	eventCount, err := store.CountDomainEventsByType(ctx, work.WorkItemMarkedReadyEventType)
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("WORK_ITEM_MARKED_READY event count = %d, want 0 (readiness gate must reject before any transition)", eventCount)
	}
	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-mark-1")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("command receipt count = %d, want 0 (a failed command must remain retryable)", receiptCount)
	}
}

// TestMarkWorkItemReady_ConcurrentSameIdempotencyKey_OneTransitionsOtherReplaysSQLite
// is this task's own "concurrency...replay depending on timing" Verify-line
// requirement for the SAME key case: two goroutines racing the IDENTICAL
// (Actor, Scope, IdempotencyKey, RequestHash) against a real sqlite database
// — exactly one performs the real transition, the other's own inner receipt
// recheck (inside its own serialized transaction) finds the first's already-
// committed receipt and replays it. Both calls must return the identical
// result; never two events, never two receipts.
func TestMarkWorkItemReady_ConcurrentSameIdempotencyKey_OneTransitionsOtherReplaysSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-mark-ready-concurrent-same-key.db")
	uow := sqlite.NewUnitOfWork(store)
	seedProjectSQLite(t, uow, "project-1")
	seedReadyEligibleRootSQLite(t, uow, "project-1", "work-item-1", "family-1")

	cmd := markReadyCmd("idem-mark-1", "hash-mark-1", "project-1")
	const writers = 2
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]work.MarkWorkItemReadyResult, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = work.MarkWorkItemReady(ctx, uow, cmd, work.MarkWorkItemReadyRequest{WorkItemID: "work-item-1"})
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d MarkWorkItemReady: %v", i, err)
		}
	}
	if results[0] != results[1] {
		t.Fatalf("concurrent same-key results differ: %+v vs %+v, want identical", results[0], results[1])
	}
	if results[0].Status != string(workdomain.WorkItemReady) || results[0].Version != 2 {
		t.Fatalf("result = %+v, want Status=READY Version=2", results[0])
	}

	eventCount, err := store.CountDomainEventsByType(ctx, work.WorkItemMarkedReadyEventType)
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("WORK_ITEM_MARKED_READY event count = %d, want exactly 1 (never two events for one same-key race)", eventCount)
	}
	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-mark-1")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("command receipt count = %d, want exactly 1", receiptCount)
	}
}

// TestMarkWorkItemReady_ConcurrentDifferentIdempotencyKeys_ExactlyOneTransitionsSQLite
// is this task's own "concurrency" Verify-line requirement for the
// DIFFERENT-key case: two goroutines, two DISTINCT Idempotency-Keys, racing
// the SAME WorkItem. Because internal/adapters/sqlite's own Store opens
// every connection with _txlock=immediate (the identical reasoning
// TestApproveScopeExpansion_ConcurrentApprovals_BothSucceedWithDistinctScopeVersionsSQLite's
// own doc comment gives), the two WithSerializedWrite transactions fully
// serialize: whichever commits first leaves the WorkItem genuinely READY,
// and the second transaction's own fresh GetWorkItem read (inside its own,
// later transaction) already observes that committed READY status — so it
// deterministically loses with ErrWorkItemNotEligibleForReady, never a
// double transition and never a silent no-op.
func TestMarkWorkItemReady_ConcurrentDifferentIdempotencyKeys_ExactlyOneTransitionsSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-mark-ready-concurrent-diff-keys.db")
	uow := sqlite.NewUnitOfWork(store)
	seedProjectSQLite(t, uow, "project-1")
	seedReadyEligibleRootSQLite(t, uow, "project-1", "work-item-1", "family-1")

	const writers = 2
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]work.MarkWorkItemReadyResult, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cmd := markReadyCmd(fmt.Sprintf("idem-mark-%d", i), fmt.Sprintf("hash-mark-%d", i), "project-1")
			results[i], errs[i] = work.MarkWorkItemReady(ctx, uow, cmd, work.MarkWorkItemReadyRequest{WorkItemID: "work-item-1"})
		}()
	}
	close(start)
	wg.Wait()

	wins, losses := 0, 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
			if results[i].Status != string(workdomain.WorkItemReady) || results[i].Version != 2 {
				t.Fatalf("writer %d winning result = %+v, want Status=READY Version=2", i, results[i])
			}
		case errors.Is(err, work.ErrWorkItemNotEligibleForReady):
			losses++
		default:
			t.Fatalf("writer %d MarkWorkItemReady unexpected err = %v", i, err)
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins=%d losses=%d, want exactly 1 winner and 1 loser", wins, losses)
	}

	reloaded := loadWorkItemSQLite(t, uow, "work-item-1")
	if reloaded.Status != workdomain.WorkItemReady || reloaded.Version != 2 {
		t.Fatalf("final WorkItem = %+v, want Status=READY Version=2 (exactly one transition)", reloaded)
	}
	eventCount, err := store.CountDomainEventsByType(ctx, work.WorkItemMarkedReadyEventType)
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("WORK_ITEM_MARKED_READY event count = %d, want exactly 1", eventCount)
	}
}
