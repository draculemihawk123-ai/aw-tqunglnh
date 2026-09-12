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

// TestCreateProject_ConcurrentDistinctRequests_NoDuplicateOrLostProject
// races N goroutines each creating a DISTINCT Project through the real
// sqlite UnitOfWork (not the in-memory fake — see
// TestRegisterRepository_ConcurrentRegistration_NoDuplicateOrLostJob's own
// doc comment below for why a genuine writer-contention proof needs real
// sqlite). Every writer must see its own freshly minted ProjectID
// committed, none lost, none duplicated, even though SQLite serializes the
// underlying writes one at a time.
func TestCreateProject_ConcurrentDistinctRequests_NoDuplicateOrLostProject(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-catalog-create-project-concurrent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]catalog.CreateProjectResult, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ids := idsource.Random{} // each writer mints its own id independently, like a real concurrent caller would
			cmd := ports.Command{
				ID: fmt.Sprintf("cmd-create-project-%d", i), IdempotencyKey: fmt.Sprintf("idem-create-project-%d", i),
				Actor: "actor-1", CorrelationID: "corr-1", Scope: ports.InstallationScope(),
				RequestedAt: time.Now().UTC(), Type: "CreateProject",
				RequestHash: fmt.Sprintf("hash-create-project-%d", i),
			}
			results[i], errs[i] = catalog.CreateProject(ctx, uow, ids, cmd, catalog.CreateProjectRequest{Name: fmt.Sprintf("project-%d", i)})
		}()
	}
	close(start)
	wg.Wait()

	seenProjectIDs := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if results[i].ProjectID == "" || seenProjectIDs[results[i].ProjectID] {
			t.Fatalf("writer %d got empty or duplicate project id %q", i, results[i].ProjectID)
		}
		seenProjectIDs[results[i].ProjectID] = true
	}
	if len(seenProjectIDs) != writers {
		t.Fatalf("got %d distinct project ids, want %d", len(seenProjectIDs), writers)
	}

	projects, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != writers {
		t.Fatalf("project rows = %d, want %d (one per writer, none lost/duplicated)", len(projects), writers)
	}
}

// TestCreateProject_ConcurrentSameIdempotencyKey_ExactlyOneProjectPersists
// is the same-key counterpart: N goroutines race CreateProject with the
// EXACT same IdempotencyKey/RequestHash/Name — the scenario a real client
// retry storm produces (e.g. an HTTP client that fires the same request
// twice under load, or two workers racing the same at-least-once
// delivery). SQLite's own BEGIN IMMEDIATE write-lock genuinely serializes
// these transactions one at a time (internal/adapters/sqlite/txrunner.go's
// own runTx), so at most one writer ever reaches
// tx.Catalog().CreateProject/ids.NewID(); every later writer's own
// tx.Receipts().Load call (the first thing CreateProject's own
// WithSerializedWrite body does) finds the first writer's committed
// receipt and replays its exact stored ProjectID instead. This is the
// receipt ledger's own ON CONFLICT DO NOTHING race-safety
// (command_receipts.go's Record) exercised as a real concurrent scenario,
// not merely asserted by reading its doc comment.
func TestCreateProject_ConcurrentSameIdempotencyKey_ExactlyOneProjectPersists(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-catalog-create-project-race.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]catalog.CreateProjectResult, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Every writer uses the SAME idempotency key/hash/name and its
			// own distinct idsource.Random — if two writers ever actually
			// reached the mutation, they would mint two different random
			// project ids, which the final "every result agrees" assertion
			// below would catch immediately.
			ids := idsource.Random{}
			cmd := ports.Command{
				ID: "cmd-create-project-race", IdempotencyKey: "idem-create-project-race",
				Actor: "actor-1", CorrelationID: "corr-1", Scope: ports.InstallationScope(),
				RequestedAt: time.Now().UTC(), Type: "CreateProject", RequestHash: "hash-create-project-race",
			}
			results[i], errs[i] = catalog.CreateProject(ctx, uow, ids, cmd, catalog.CreateProjectRequest{Name: "race target"})
		}()
	}
	close(start)
	wg.Wait()

	firstID := ""
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if firstID == "" {
			firstID = results[i].ProjectID
		} else if results[i].ProjectID != firstID {
			t.Fatalf("writer %d resolved to project id %q, want every writer to agree on the same id %q", i, results[i].ProjectID, firstID)
		}
	}

	projects, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project rows = %d, want exactly 1 (same idempotency key racing must never duplicate)", len(projects))
	}
}

// TestCreateProject_ReplayAfterCommit_SimulatesCallerNeverSawFirstAck is
// V6-03's own "crash-after-commit-before-ack" proof: it calls CreateProject
// once against real sqlite, independently confirms via direct SQL that the
// Project row, its ProjectCreated event and its command receipt all
// genuinely committed (not merely that the in-process call returned nil),
// then issues a SECOND, entirely independent CreateProject call built from
// a fresh ports.Command value with the same Actor/Scope/IdempotencyKey/
// Type/RequestHash and a fresh idsource.Sequential (which would mint
// "project-2" if actually invoked) — exactly what a real caller does after
// a network timeout/crash lost the first call's response before ever
// seeing it and it retries. The second call must replay the first
// commit's exact ProjectID without touching storage again.
func TestCreateProject_ReplayAfterCommit_SimulatesCallerNeverSawFirstAck(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-catalog-create-project-crash-replay.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	firstCmd := ports.Command{
		ID: "cmd-crash-replay", IdempotencyKey: "idem-crash-replay", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.InstallationScope(),
		RequestedAt: time.Now().UTC(), Type: "CreateProject", RequestHash: "hash-crash-replay",
	}
	req := catalog.CreateProjectRequest{Name: "crash-replay target"}
	first, err := catalog.CreateProject(ctx, uow, idsource.NewSequential("crash-project"), firstCmd, req)
	if err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}
	if first.ProjectID != "crash-project-1" {
		t.Fatalf("first.ProjectID = %q, want crash-project-1", first.ProjectID)
	}

	// Independently prove the first call's commit really happened —
	// mirroring internal/adapters/sqlite/command_handler_example_test.go's
	// own TestHandleCreateProject_CommitsStateEventAndReceiptAtomically —
	// before simulating the caller never having seen that result.
	eventCount, err := store.CountDomainEvents(ctx, "Project", first.ProjectID)
	if err != nil {
		t.Fatalf("count domain_events: %v", err)
	}
	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, firstCmd.IdempotencyKey)
	if err != nil {
		t.Fatalf("count command_receipts: %v", err)
	}
	projectsAfterFirst, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects after first call: %v", err)
	}
	if len(projectsAfterFirst) != 1 || eventCount != 1 || receiptCount != 1 {
		t.Fatalf("project/event/receipt counts after first call = %d/%d/%d, want 1/1/1 (the first call must have really committed)", len(projectsAfterFirst), eventCount, receiptCount)
	}

	// Simulate the caller crashing (or its connection dropping) after the
	// commit above but before it ever saw `first` — a brand new
	// ports.Command value with the identical envelope fields a retried
	// client would send, and a fresh idsource.Sequential a buggy replay
	// path would call into.
	secondCmd := ports.Command{
		ID: "cmd-crash-replay", IdempotencyKey: "idem-crash-replay", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.InstallationScope(),
		RequestedAt: time.Now().UTC(), Type: "CreateProject", RequestHash: "hash-crash-replay",
	}
	second, err := catalog.CreateProject(ctx, uow, idsource.NewSequential("crash-project"), secondCmd, req)
	if err != nil {
		t.Fatalf("second (post-crash retry) CreateProject: %v", err)
	}
	if second != first {
		t.Fatalf("post-crash retry result = %+v, want identical to the original commit's result %+v", second, first)
	}
	if second.ProjectID != "crash-project-1" {
		t.Fatalf("post-crash retry ProjectID = %q, want the exact original crash-project-1, not a freshly minted id", second.ProjectID)
	}

	eventCount, err = store.CountDomainEvents(ctx, "Project", first.ProjectID)
	if err != nil {
		t.Fatalf("count domain_events after retry: %v", err)
	}
	projectsAfterRetry, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects after retry: %v", err)
	}
	if len(projectsAfterRetry) != 1 || eventCount != 1 {
		t.Fatalf("project/event counts after post-crash retry = %d/%d, want still 1/1 (the retry must never redo the mutation)", len(projectsAfterRetry), eventCount)
	}
}

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
