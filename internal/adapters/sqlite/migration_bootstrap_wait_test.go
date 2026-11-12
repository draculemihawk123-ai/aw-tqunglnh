package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// The CI race job failed TestOpen_ConcurrentOpensOfAFreshDatabaseMigrateOnce with "bootstrap migrations: database is locked":
// the first opener held the write lock for its whole migration, longer than busy_timeout (5 s) under -race, and the second
// opener's CREATE of schema_migrations gave up instead of waiting. Holding the write lock past busy_timeout must only delay Open.
func TestOpen_WaitsPastBusyTimeoutForAnotherWritersLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.db")
	holder, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	ctx := context.Background()
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE lock_holder(x INTEGER)`); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		store, err := Open(ctx, path)
		if err == nil {
			err = store.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Open returned while another connection held the write lock: %v", err)
	case <-time.After(6 * time.Second): // past busy_timeout(5000)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Open after the lock was released: %v", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("Open did not finish within a minute of the lock being released")
	}
}
