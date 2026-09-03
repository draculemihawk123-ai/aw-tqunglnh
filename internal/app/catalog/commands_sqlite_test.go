package catalog_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// TestRegisterRepository_ConcurrentRegistration_NoDuplicateOrLostJob races
// N goroutines each registering a DISTINCT repository under the same
// Project through the real sqlite UnitOfWork (not the in-memory fake,
// whose WithSerializedWrite unlocks before running fn and so cannot
// exercise genuine writer contention — the same reasoning
// internal/app/definitions/commands_sqlite_test.go's own concurrent test
// gives). This is the meaningful concurrency scenario for
// RegisterRepository: every writer must see its own Repository row
// committed as REGISTERING with its own distinct probe job, none lost,
// none duplicated, even though SQLite serializes the underlying writes
// one at a time.
func TestRegisterRepository_ConcurrentRegistration_NoDuplicateOrLostJob(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-catalog-command-concurrent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	seedProjectSQLite(t, uow, "project-race")

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]catalog.RegisterRepositoryResult, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cmd := ports.Command{
				ID: fmt.Sprintf("cmd-register-%d", i), IdempotencyKey: fmt.Sprintf("idem-register-%d", i),
				Actor: "actor-1", CorrelationID: "corr-1", Scope: ports.ProjectScope("project-race"),
				RequestedAt: time.Now().UTC(), Type: "RegisterRepository",
				RequestHash: fmt.Sprintf("hash-register-%d", i),
			}
			results[i], errs[i] = catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
				RepositoryID: fmt.Sprintf("repo-race-%d", i), ProjectID: "project-race",
				Name: fmt.Sprintf("svc-%d", i), RemoteLocator: fmt.Sprintf("https://example.invalid/repo-%d.git", i),
				DefaultRef: "main",
			})
		}()
	}
	close(start)
	wg.Wait()

	seenRepositoryIDs := map[string]bool{}
	seenProbeJobIDs := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if results[i].Status != string(project.RepositoryRegistering) {
			t.Fatalf("writer %d Status = %q, want REGISTERING", i, results[i].Status)
		}
		if seenRepositoryIDs[results[i].RepositoryID] {
			t.Fatalf("writer %d got duplicate repository id %q", i, results[i].RepositoryID)
		}
		seenRepositoryIDs[results[i].RepositoryID] = true
		if results[i].ProbeJobID == "" || seenProbeJobIDs[results[i].ProbeJobID] {
			t.Fatalf("writer %d got empty or duplicate probe job id %q", i, results[i].ProbeJobID)
		}
		seenProbeJobIDs[results[i].ProbeJobID] = true
	}
	if len(seenRepositoryIDs) != writers {
		t.Fatalf("got %d distinct repository ids, want %d", len(seenRepositoryIDs), writers)
	}

	repos, err := catalog.ListProjectRepositories(ctx, uow, "project-race")
	if err != nil {
		t.Fatalf("ListProjectRepositories: %v", err)
	}
	if len(repos) != writers {
		t.Fatalf("repository rows = %d, want %d (one per writer, none lost/duplicated)", len(repos), writers)
	}

	// Each writer's own REPOSITORY_PROBE job is keyed by
	// cmd.IdempotencyKey+"-probe" (RegisterRepository's own convention) —
	// Store.CountDurableJobsByIdempotencyKey (already exported for
	// exactly this "prove a boundary dispatched exactly one job" purpose)
	// proves each one was durably persisted exactly once, none lost to
	// the concurrent writes.
	for i := 0; i < writers; i++ {
		count, err := store.CountDurableJobsByIdempotencyKey(ctx, fmt.Sprintf("idem-register-%d-probe", i))
		if err != nil {
			t.Fatalf("CountDurableJobsByIdempotencyKey(writer %d): %v", i, err)
		}
		if count != 1 {
			t.Fatalf("writer %d probe job count = %d, want exactly 1", i, count)
		}
	}
}

func seedProjectSQLite(t *testing.T, uow ports.UnitOfWork, id string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}
