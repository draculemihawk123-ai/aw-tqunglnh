package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
)

func TestRunSerializedWrite_CommitsOnSuccess(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, migratedDatabasePath(t, "agentkit-txok.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if _, err := store.db.ExecContext(ctx, `CREATE TABLE probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	err = store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO probe DEFAULT VALUES`)
		return err
	})
	if err != nil {
		t.Fatalf("RunSerializedWrite: %v", err)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe`).Scan(&count); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestRunSerializedWrite_RollsBackOnFnError(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, migratedDatabasePath(t, "agentkit-txfail.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if _, err := store.db.ExecContext(ctx, `CREATE TABLE probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	sentinel := errors.New("fn refused to commit")
	err = store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO probe DEFAULT VALUES`); err != nil {
			return err
		}
		return sentinel
	})
	if err == nil {
		t.Fatal("RunSerializedWrite should propagate fn's error")
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probe`).Scan(&count); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0 (fn's error must roll back its own insert)", count)
	}
}

// TestRunSerializedWrite_ConcurrentWritersSerializeAndBothSucceed opens two
// genuinely separate connections to the same database file and has both
// write concurrently — SQLite's single-writer model means one must wait
// for the other, exercising real cross-connection contention rather than
// goroutines sharing one pool.
func TestRunSerializedWrite_ConcurrentWritersSerializeAndBothSucceed(t *testing.T) {
	ctx := context.Background()
	databasePath := migratedDatabasePath(t, "agentkit-contention.db")
	storeA, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open (A): %v", err)
	}
	defer storeA.Close()
	storeB, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open (B): %v", err)
	}
	defer storeB.Close()

	if _, err := storeA.db.ExecContext(ctx, `CREATE TABLE contention_probe (id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	write := func(store *Store, idx int, value string) {
		defer wg.Done()
		<-start
		errs[idx] = store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
			time.Sleep(20 * time.Millisecond) // widen the contention window
			_, err := tx.ExecContext(ctx, `INSERT INTO contention_probe(value) VALUES (?)`, value)
			return err
		})
	}
	wg.Add(2)
	go write(storeA, 0, "from-a")
	go write(storeB, 1, "from-b")
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d failed: %v", i, err)
		}
	}
	var count int
	if err := storeA.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contention_probe`).Scan(&count); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2 (both concurrent writers must eventually commit despite contention)", count)
	}
}

func TestMapSQLiteError_Nil(t *testing.T) {
	if err := MapSQLiteError(nil); err != nil {
		t.Fatalf("MapSQLiteError(nil) = %v, want nil", err)
	}
}

func TestMapSQLiteError_BusyMapsToUnavailableRetryable(t *testing.T) {
	raw := errors.New("SQLITE_BUSY: database is locked")
	mapped := MapSQLiteError(raw)
	if apperror.CodeOf(mapped) != apperror.CodeUnavailable {
		t.Fatalf("CodeOf = %q, want %q", apperror.CodeOf(mapped), apperror.CodeUnavailable)
	}
	if !apperror.IsRetryable(mapped) {
		t.Fatal("a busy/lock-contention error must be retryable")
	}
}

func TestMapSQLiteError_OtherErrorMapsToInternalNotRetryable(t *testing.T) {
	raw := errors.New("some other sqlite failure")
	mapped := MapSQLiteError(raw)
	if apperror.CodeOf(mapped) != apperror.CodeInternal {
		t.Fatalf("CodeOf = %q, want %q", apperror.CodeOf(mapped), apperror.CodeInternal)
	}
	if apperror.IsRetryable(mapped) {
		t.Fatal("a generic internal error must not be retryable")
	}
}

// TestMapSQLiteError_NeverLeaksRawMessage is V1-04A's own "assert không
// error nào mang chuỗi SQLite rò ra ngoài adapter": apperror.Error's
// Error() method never includes its private cause, so the mapped error's
// public text must not contain the raw driver message.
func TestMapSQLiteError_NeverLeaksRawMessage(t *testing.T) {
	raw := errors.New("SQLITE_BUSY: database is locked, path=/secret/internal/path.db")
	mapped := MapSQLiteError(raw)
	if strings.Contains(mapped.Error(), "/secret/internal/path.db") {
		t.Fatalf("mapped error leaked the raw SQLite message: %v", mapped)
	}
	if strings.Contains(mapped.Error(), "SQLITE_BUSY") {
		t.Fatalf("mapped error leaked the raw SQLite code: %v", mapped)
	}
}

func TestMapSQLiteError_PassesThroughExistingAppError(t *testing.T) {
	original := apperror.New(apperror.CodeConflict, "already mapped", false)
	mapped := MapSQLiteError(original)
	if mapped != original {
		t.Fatalf("MapSQLiteError should pass an already-*apperror.Error through unchanged, got a different value: %v", mapped)
	}
}

// TestMapSQLiteError_ConflictNeverProducedAutomatically documents that
// this generic mapper can only ever produce Unavailable or Internal —
// CONFLICT requires a caller to confirm a real CAS/version loss by
// reloading state, which a bare error string can never prove on its own.
func TestMapSQLiteError_ConflictNeverProducedAutomatically(t *testing.T) {
	cases := []error{
		errors.New("SQLITE_BUSY: database is locked"),
		errors.New("SQLITE_LOCKED"),
		errors.New("constraint failed"),
		errors.New("no such table: widgets"),
	}
	for _, raw := range cases {
		if got := apperror.CodeOf(MapSQLiteError(raw)); got == apperror.CodeConflict {
			t.Fatalf("MapSQLiteError(%v) produced CodeConflict, which this generic mapper must never do on its own", raw)
		}
	}
}
