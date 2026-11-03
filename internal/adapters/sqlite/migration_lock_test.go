package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

// V12-08 (UX-08): `aw serve` and `aw worker` started together on a fresh database both ran the same migration and one failed
// with "duplicate column name". Several processes opening one fresh database at once must all succeed, each migration once.
func TestOpen_ConcurrentOpensOfAFreshDatabaseMigrateOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	const openers = 4
	var wg sync.WaitGroup
	errs := make([]error, openers)
	for i := 0; i < openers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store, err := Open(context.Background(), path)
			if err == nil {
				err = store.Close()
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("opener %d: %v", i, err)
		}
	}
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count, distinct int
	if err := store.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT version) FROM schema_migrations`).Scan(&count, &distinct); err != nil {
		t.Fatal(err)
	}
	if count != distinct {
		t.Fatalf("schema_migrations has %d rows for %d versions", count, distinct)
	}
}
