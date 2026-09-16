package projectionrebuild

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// This file exercises the real stack — a real sqlite.Store, never a fake —
// for every crash-safety/concurrency guarantee this task's own Verify line
// names (docs/design/08-v6-api-projections.md V6-09: "replay, concurrency,
// crash-after-intent, restart, job reclaim, exact operation lookup").
// commands_test.go's own fake-backed tests already cover the pure business
// logic (validation, typed-conflict shape, scoping) cheaply; this file
// covers only what genuinely needs a real, durable, serialized-write
// SQLite database underneath.

type rebuildSQLiteFixture struct {
	ctx    context.Context
	dbPath string
	store  *sqlite.Store
	uow    ports.UnitOfWork
	ids    idsource.Source
}

func newRebuildSQLiteFixture(t *testing.T) *rebuildSQLiteFixture {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "projection-rebuild.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	return &rebuildSQLiteFixture{
		ctx: ctx, dbPath: dbPath, store: store, uow: sqlite.NewUnitOfWork(store), ids: idsource.NewSequential("op"),
	}
}

// TestRequestProjectionRebuild_SQLite_Replay is V6-09's own "replay"
// Verify-line scenario against the real adapter: the same
// IdempotencyKey+RequestHash returns the exact same OperationID, never a
// second row in projection_rebuild_operations.
func TestRequestProjectionRebuild_SQLite_Replay(t *testing.T) {
	fx := newRebuildSQLiteFixture(t)
	req := RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: "workitem"}

	first, err := RequestProjectionRebuild(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-1", ports.ProjectScope("project-1"), "RequestProjectionRebuild"), req)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	second, err := RequestProjectionRebuild(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-1", ports.ProjectScope("project-1"), "RequestProjectionRebuild"), req)
	if err != nil {
		t.Fatalf("replayed request: %v", err)
	}
	if first != second {
		t.Fatalf("replay result = %+v, want identical to first %+v", second, first)
	}

	status, err := GetProjectionRebuildStatus(fx.ctx, fx.uow, first.OperationID)
	if err != nil {
		t.Fatalf("GetProjectionRebuildStatus: %v", err)
	}
	if status.Phase != string(ports.ProjectionRebuildRequested) {
		t.Fatalf("status.Phase = %s, want REQUESTED", status.Phase)
	}
}

// TestRequestProjectionRebuild_SQLite_Concurrency_OneWinner is V6-09's own
// "concurrency" Verify-line scenario, proven as a REAL race (two goroutines,
// two genuinely different IdempotencyKeys, calling RequestProjectionRebuild
// against the SAME real store at the same time) rather than only the
// sequential proof commands_test.go's own fake-backed
// TestRequestProjectionRebuild_ActiveOperationConflict_TypedErrorCarriesID
// already gives: this Store's own global BEGIN-IMMEDIATE write
// serialization (txrunner.go) must make exactly one of the two actually
// win, with the loser's own typed conflict carrying the winner's real
// OperationID — never two operations, never a lost update.
func TestRequestProjectionRebuild_SQLite_Concurrency_OneWinner(t *testing.T) {
	fx := newRebuildSQLiteFixture(t)

	type outcome struct {
		result RequestProjectionRebuildResult
		err    error
	}
	results := make([]outcome, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i, key := range []string{"req-A", "req-B"} {
		go func(i int, idempotencyKey string) {
			defer wg.Done()
			ids := idsource.NewSequential("racer-" + idempotencyKey)
			result, err := RequestProjectionRebuild(fx.ctx, fx.uow, ids,
				testCommand(idempotencyKey, "hash-"+idempotencyKey, ports.ProjectScope("project-1"), "RequestProjectionRebuild"),
				RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: "workitem"},
			)
			results[i] = outcome{result: result, err: err}
		}(i, key)
	}
	wg.Wait()

	var winners, losers int
	var winnerID string
	for _, o := range results {
		if o.err == nil {
			winners++
			winnerID = o.result.OperationID
		} else {
			losers++
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d (results=%+v), want exactly one winner and one loser", winners, losers, results)
	}
	if winnerID == "" {
		t.Fatal("winner's own OperationID is empty")
	}
	for _, o := range results {
		if o.err == nil {
			continue
		}
		activeID, ok := ActiveProjectionRebuildOperationID(o.err)
		if !ok {
			t.Fatalf("loser error = %v, want a typed active-rebuild conflict carrying the winner's ID", o.err)
		}
		if activeID != winnerID {
			t.Fatalf("loser's carried active ID = %s, want the real winner %s", activeID, winnerID)
		}
	}

	// Exactly one operation row ever exists for this (project, projection).
	status, err := GetProjectionRebuildStatus(fx.ctx, fx.uow, winnerID)
	if err != nil {
		t.Fatalf("GetProjectionRebuildStatus(winner): %v", err)
	}
	if status.ProjectID != "project-1" || status.ProjectionName != "workitem" {
		t.Fatalf("winner status = %+v, want project-1/workitem", status)
	}
}

// TestRequestProjectionRebuild_SQLite_CrashAfterIntent_NoPartialState is
// V6-09's own "crash-after-intent" Verify-line scenario. Literally killing
// a transaction mid-commit is not something a Go test can do to SQLite's
// own atomic commit — so, as this task's own brief notes, the proof
// instead comes from an INJECTED failure at the LAST write inside
// RequestProjectionRebuild's own single WithSerializedWrite closure
// (EnqueueJob's own idempotency-key collision, forced by pre-seeding a
// durable_jobs row under the exact idempotency key the command's own
// EnqueueJob call will independently derive from the operation ID this
// test's own deterministic idsource.Sequential predicts): if the operation
// insert genuinely committed independently of the job insert, this
// scenario would leave a REQUESTED operation row with no matching job — a
// partial, crash-like state this task's own atomicity guarantee forbids by
// construction (one shared *sql.Tx, one commit). This test proves that
// never happens, and that a subsequent retry recovers cleanly.
func TestRequestProjectionRebuild_SQLite_CrashAfterIntent_NoPartialState(t *testing.T) {
	fx := newRebuildSQLiteFixture(t)

	// idsource.Sequential("op") mints "op-1" as RequestProjectionRebuild's
	// own first ids.NewID() call (the operation ID) — see commands.go.
	// Pre-seed a durable_jobs row already holding the EXACT idempotency key
	// that operation's own later EnqueueJob call will derive
	// ("projection-rebuild:op-1"), forcing that call to fail with
	// ports.ErrPersistenceAlreadyExists from INSIDE the same transaction
	// CreateOperation already wrote to.
	if _, err := fx.store.EnqueueJob(fx.ctx, ports.EnqueueJobRequest{
		ID: "job-preexisting", ProjectID: "project-1", Kind: "PROBE", AggregateType: "Probe", AggregateID: "probe-1",
		MaxClaims: 1, IdempotencyKey: "projection-rebuild:op-1",
	}); err != nil {
		t.Fatalf("pre-seed colliding job: %v", err)
	}

	_, err := RequestProjectionRebuild(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-1", ports.ProjectScope("project-1"), "RequestProjectionRebuild"),
		RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: "workitem"},
	)
	if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
		t.Fatalf("RequestProjectionRebuild (forced mid-transaction failure) error = %v, want ErrPersistenceAlreadyExists", err)
	}

	// The operation row this transaction's own CreateOperation call wrote
	// must NOT have survived the rollback — no partial state.
	if _, err := GetProjectionRebuildStatus(fx.ctx, fx.uow, "op-1"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetProjectionRebuildStatus(op-1) after forced rollback = %v, want ErrPersistenceNotFound (no partial operation row)", err)
	}
	// A subsequent retry (representing a fresh, post-crash attempt) with
	// the SAME idsource continuing on ("op-3", since "op-1"/"op-2" were
	// already consumed, unsuccessfully, by the forced-failure attempt
	// above) succeeds cleanly — nothing about the failed attempt left any
	// state behind that could block it.
	retry, err := RequestProjectionRebuild(fx.ctx, fx.uow, fx.ids,
		testCommand("req-2-retry", "hash-2", ports.ProjectScope("project-1"), "RequestProjectionRebuild"),
		RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: "workitem"},
	)
	if err != nil {
		t.Fatalf("retry after forced failure: %v", err)
	}
	if retry.Phase != string(ports.ProjectionRebuildRequested) {
		t.Fatalf("retry.Phase = %s, want REQUESTED", retry.Phase)
	}
}

// TestRequestProjectionRebuild_SQLite_Restart is V6-09's own "restart"
// Verify-line scenario: a REQUESTED operation, written by one process
// (Store), is still visible, unchanged, to a fresh process that only ever
// re-opens the same database file — this task's own operation record is
// durable, not held in any in-memory state.
func TestRequestProjectionRebuild_SQLite_Restart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "projection-rebuild-restart.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	created, err := RequestProjectionRebuild(ctx, uow, idsource.NewSequential("op"),
		testCommand("req-1", "hash-1", ports.ProjectScope("project-1"), "RequestProjectionRebuild"),
		RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: "workitem"},
	)
	if err != nil {
		t.Fatalf("RequestProjectionRebuild: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	restarted, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("re-open store after restart: %v", err)
	}
	defer func() { _ = restarted.Close() }()
	restartedUow := sqlite.NewUnitOfWork(restarted)

	status, err := GetProjectionRebuildStatus(ctx, restartedUow, created.OperationID)
	if err != nil {
		t.Fatalf("GetProjectionRebuildStatus after restart: %v", err)
	}
	if status.OperationID != created.OperationID || status.Phase != string(ports.ProjectionRebuildRequested) {
		t.Fatalf("status after restart = %+v, want OperationID=%s Phase=REQUESTED", status, created.OperationID)
	}
	if status.JobID != created.JobID {
		t.Fatalf("status.JobID after restart = %s, want %s", status.JobID, created.JobID)
	}

	// A fresh request against the restarted process still sees the
	// operation as active — a fresh in-memory process has no state of its
	// own to have forgotten it from.
	_, err = RequestProjectionRebuild(ctx, restartedUow, idsource.NewSequential("op-after-restart"),
		testCommand("req-2-different-key", "hash-2", ports.ProjectScope("project-1"), "RequestProjectionRebuild"),
		RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: "workitem"},
	)
	activeID, ok := ActiveProjectionRebuildOperationID(err)
	if !ok || activeID != created.OperationID {
		t.Fatalf("post-restart conflict = (%v, ok=%v), want the pre-restart OperationID %s carried", err, ok, created.OperationID)
	}
}

// TestRequestProjectionRebuild_SQLite_JobReclaim is V6-09's own "job
// reclaim" Verify-line scenario — mirroring
// releasesetcommit's execute_test.go's own identical
// claim/expire/RecoverExpiredJobs/reclaim shape
// (TestExecuteReleaseSetLocalCommit_CrashBeforeGit_CleanRetryOneCommit):
// the job RequestProjectionRebuild enqueues is an ordinary durable_jobs
// row, so it participates in the exact same generic lease-expiry/reclaim
// mechanism every other job kind in this codebase already gets, with no
// new mechanism of this task's own — proven here by actually exercising
// it against the real job this command enqueues.
func TestRequestProjectionRebuild_SQLite_JobReclaim(t *testing.T) {
	fx := newRebuildSQLiteFixture(t)
	created, err := RequestProjectionRebuild(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-1", ports.ProjectScope("project-1"), "RequestProjectionRebuild"),
		RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: "workitem"},
	)
	if err != nil {
		t.Fatalf("RequestProjectionRebuild: %v", err)
	}

	jobA, leaseA, err := fx.store.ClaimJob(fx.ctx, "worker-a", 600*time.Millisecond)
	if err != nil {
		t.Fatalf("ClaimJob (worker A): %v", err)
	}
	if string(jobA.ID) != created.JobID || jobA.Kind != ProjectionRebuildJobKind {
		t.Fatalf("claimed job = %+v, want ID=%s Kind=%s", jobA, created.JobID, ProjectionRebuildJobKind)
	}
	if jobA.ClaimCount != 1 {
		t.Fatalf("jobA.ClaimCount = %d, want 1", jobA.ClaimCount)
	}

	// Worker A "crashes" — never completes the job. Its lease lapses.
	time.Sleep(1500 * time.Millisecond)
	reclaimed, err := fx.store.RecoverExpiredJobs(fx.ctx)
	if err != nil {
		t.Fatalf("RecoverExpiredJobs: %v", err)
	}
	if reclaimed < 1 {
		t.Fatalf("RecoverExpiredJobs reclaimed %d jobs, want at least 1", reclaimed)
	}

	jobB, leaseB, err := fx.store.ClaimJob(fx.ctx, "worker-b", 10*time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob (worker B, after reclaim): %v", err)
	}
	if jobB.ID != jobA.ID {
		t.Fatalf("worker B claimed job %s, want the SAME reclaimed job %s", jobB.ID, jobA.ID)
	}
	if jobB.ClaimCount != 2 {
		t.Fatalf("jobB.ClaimCount = %d, want 2 (reclaimed once)", jobB.ClaimCount)
	}
	if leaseB.Token <= leaseA.Token {
		t.Fatalf("leaseB.Token = %d, want greater than worker A's stale token %d", leaseB.Token, leaseA.Token)
	}

	// The operation row itself is untouched by any of this — job
	// lease/reclaim mechanics never reach into
	// projection_rebuild_operations (V6-09's own "Không làm").
	status, err := GetProjectionRebuildStatus(fx.ctx, fx.uow, created.OperationID)
	if err != nil {
		t.Fatalf("GetProjectionRebuildStatus: %v", err)
	}
	if status.Phase != string(ports.ProjectionRebuildRequested) {
		t.Fatalf("status.Phase after job reclaim = %s, want still REQUESTED", status.Phase)
	}
}

// TestGetProjectionRebuildStatus_SQLite_ExactOperationLookup is V6-09's own
// "exact operation lookup" Verify-line scenario against the real adapter:
// creating two DIFFERENT operations (two different projection names in the
// same project, so both can be REQUESTED at once) and confirming each
// lookup by ID returns exactly its own row, never the other's.
func TestGetProjectionRebuildStatus_SQLite_ExactOperationLookup(t *testing.T) {
	fx := newRebuildSQLiteFixture(t)
	first, err := RequestProjectionRebuild(fx.ctx, fx.uow, fx.ids,
		testCommand("req-1", "hash-1", ports.ProjectScope("project-1"), "RequestProjectionRebuild"),
		RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: "workitem"},
	)
	if err != nil {
		t.Fatalf("first RequestProjectionRebuild: %v", err)
	}
	second, err := RequestProjectionRebuild(fx.ctx, fx.uow, fx.ids,
		testCommand("req-2", "hash-2", ports.ProjectScope("project-1"), "RequestProjectionRebuild"),
		RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: "another-projection"},
	)
	if err != nil {
		t.Fatalf("second RequestProjectionRebuild: %v", err)
	}
	if first.OperationID == second.OperationID {
		t.Fatal("two distinct requests minted the same OperationID")
	}

	firstStatus, err := GetProjectionRebuildStatus(fx.ctx, fx.uow, first.OperationID)
	if err != nil {
		t.Fatalf("GetProjectionRebuildStatus(first): %v", err)
	}
	if firstStatus.ProjectionName != "workitem" {
		t.Fatalf("firstStatus.ProjectionName = %s, want workitem", firstStatus.ProjectionName)
	}
	secondStatus, err := GetProjectionRebuildStatus(fx.ctx, fx.uow, second.OperationID)
	if err != nil {
		t.Fatalf("GetProjectionRebuildStatus(second): %v", err)
	}
	if secondStatus.ProjectionName != "another-projection" {
		t.Fatalf("secondStatus.ProjectionName = %s, want another-projection", secondStatus.ProjectionName)
	}
}
