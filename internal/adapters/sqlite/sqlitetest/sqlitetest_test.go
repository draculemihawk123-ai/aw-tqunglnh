package sqlitetest_test

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite/sqlitetest"
)

// A copy opens with the real Open (which re-verifies every applied migration's checksum) and two copies are separate files.
func TestMigratedPath_OpensAndIsPrivate(t *testing.T) {
	a := sqlitetest.MigratedPath(t, "a.db")
	b := sqlitetest.MigratedPath(t, "b.db")
	if a == b {
		t.Fatalf("two calls returned the same path %s", a)
	}
	for _, p := range []string{a, b} {
		store, err := sqlite.Open(context.Background(), p)
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close %s: %v", p, err)
		}
	}
}
