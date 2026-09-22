package main

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// openDefinitionDB opens the SQLite database at dbPath and returns the store
// (so the caller can Close it) together with its ports.UnitOfWork. It is the
// one place the composition root turns a --db path into a UnitOfWork: `aw
// serve`, `aw worker` and the one-shot resource router (oneshot.go) all call
// it. (The name predates V6 — it was first written for the V2-11 definition
// CLI, which V6-15O retired in favor of the internal/delivery/cli/definitions
// leaf — and is kept because `aw worker` already references it.)
func openDefinitionDB(ctx context.Context, dbPath string) (*sqlite.Store, ports.UnitOfWork, error) {
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	return store, sqlite.NewUnitOfWork(store), nil
}
