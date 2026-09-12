// Package integration is V1-12's foundation integration gate
// (docs/design/03-v1-alpha-foundation.md, ROADMAP-§5B): it closes V1 by
// exercising the clean-start/restart contract end to end, through the
// same public surfaces a real `serve`/`worker` process would use — temp
// config, migration, artifact storage, event/outbox commit, and a
// kill/restart cycle that drains an orphaned job via the exact recovery
// path V1-10 built. It deliberately lives outside internal/domain/... and
// internal/app/...: internal/archtest.TestDomainAppNeverImportAdapters
// forbids domain/app from importing internal/adapters/... at all, but an
// end-to-end integration test's entire point is wiring adapters and
// app-layer code together the way a real binary does — exactly what a
// future cmd/aw `serve`/`worker` implementation will also do.
package integration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/doctor"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// TestFoundationCleanStartRestartContract is V1-12's own Verify scenario:
// temp config -> migrate -> store an artifact -> commit a domain event
// (with its outbox row, GC-INV-16) -> confirm Doctor reports HEALTHY ->
// enqueue and start claiming a durable job -> kill the process (close the
// store while the job is still claimed and in flight, never letting it
// complete) -> restart against the same database and artifact root ->
// prove the orphaned job drains through a fresh Pool's own startup
// recovery scan, the artifact is still readable, and the domain event/
// outbox row survived — all without a second, separate migration ever
// being required and without ever needing to touch internal state
// directly.
func TestFoundationCleanStartRestartContract(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	cfg := config.Defaults()
	cfg.DatabasePath = filepath.Join(root, "agentkit.db")
	cfg.ArtifactRoot = filepath.Join(root, "artifacts")
	cfg.WorkerID = "integration-worker"
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("temp config failed validation: %v", err)
	}

	// --- clean start ---
	store, err := sqlite.Open(ctx, cfg.DatabasePath)
	if err != nil {
		t.Fatalf("Open (clean start, applies migrations): %v", err)
	}
	// durable_jobs.project_id is NOT NULL REFERENCES projects(id); this
	// integration probe needs one real project row to enqueue against.
	// sqlite.SeedFixtureOwners exists exactly for a cross-package
	// acceptance test like this one — see its own doc comment.
	if err := sqlite.SeedFixtureOwners(ctx, store, "proj-foundation", "fam-foundation", "wi-foundation"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}

	artifacts, err := artifactstore.New(cfg.ArtifactRoot)
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	artifactContent := []byte("V1-12 foundation integration test artifact")
	artifactRef, err := artifacts.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain", Sensitivity: redact.Public}, bytes.NewReader(artifactContent))
	if err != nil {
		t.Fatalf("Put artifact: %v", err)
	}

	uow := sqlite.NewUnitOfWork(store)
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Events().Append(ctx, ports.DomainEvent{
			ID: "evt-foundation-1", AggregateType: "IntegrationProbe", AggregateID: "probe-1", Sequence: 1,
			EventType: "FoundationProbeRecorded", SchemaVersion: 1, PayloadJSON: `{"ok":true}`,
			CorrelationID: "corr-foundation-1", CreatedAt: time.Now().UTC(),
		})
	})
	if err != nil {
		t.Fatalf("commit domain event + outbox: %v", err)
	}

	healthReport := doctor.Run(ctx, doctor.Options{
		Config: cfg, Store: sqlite.NewQueryStore(store),
		WorkerConfig: workerpool.Config{Owner: cfg.WorkerID, LeaseTTL: cfg.LeaseTTL, HeartbeatEvery: cfg.LeaseHeartbeat},
		CheckWorker:  true,
	})
	if healthReport.Status != doctor.StatusHealthy {
		t.Fatalf("doctor report after clean start = %+v, want HEALTHY", healthReport)
	}

	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "job-foundation-1", ProjectID: "proj-foundation", Kind: "foundation-probe",
		AggregateType: "IntegrationProbe", AggregateID: "probe-1",
		MaxClaims: 5, IdempotencyKey: "job-foundation-1-key",
	}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	firstPoolLeaseTTL := 200 * time.Millisecond
	claimed := make(chan struct{})
	stuck := make(chan struct{}) // never closed: this handler simulates work interrupted by a crash
	registry1 := workerpool.NewRegistry()
	registry1.Register("foundation-probe", workerpool.HandlerFunc(func(ctx context.Context, _ ports.DurableJob) error {
		close(claimed)
		select {
		case <-stuck:
		case <-ctx.Done():
		}
		return ctx.Err()
	}))
	pool1, err := workerpool.New(store, registry1, workerpool.Config{
		Owner: cfg.WorkerID, LeaseTTL: firstPoolLeaseTTL, HeartbeatEvery: firstPoolLeaseTTL / 4,
		PollInterval: 20 * time.Millisecond, ShutdownGrace: 0, RecoveryInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("New (pool1): %v", err)
	}
	pool1Ctx, cancelPool1 := context.WithCancel(ctx)
	pool1Done := make(chan error, 1)
	go func() { pool1Done <- pool1.Run(pool1Ctx) }()

	select {
	case <-claimed:
	case <-time.After(3 * time.Second):
		t.Fatal("job was never claimed by pool1")
	}

	// --- kill: the process dies mid-job, with no graceful shutdown at
	// all — cancelPool1 stops the claim loop immediately (ShutdownGrace:
	// 0 means Run returns as soon as the workersFinished/timeout race
	// resolves, without waiting for the stuck handler), and closing the
	// store simulates the OS reclaiming this "process"'s resources. The
	// job is left LEASED, its lease still ticking down toward
	// firstPoolLeaseTTL, exactly like a real crash.
	cancelPool1()
	<-pool1Done
	if err := store.Close(); err != nil {
		t.Fatalf("Close (simulated kill): %v", err)
	}
	time.Sleep(2 * firstPoolLeaseTTL) // let the orphaned lease actually expire

	// --- restart ---
	store2, err := sqlite.Open(ctx, cfg.DatabasePath)
	if err != nil {
		t.Fatalf("Open (restart): %v", err)
	}
	defer store2.Close()

	restartHealth := doctor.Run(ctx, doctor.Options{
		Config: cfg, Store: sqlite.NewQueryStore(store2),
		WorkerConfig: workerpool.Config{Owner: cfg.WorkerID, LeaseTTL: cfg.LeaseTTL, HeartbeatEvery: cfg.LeaseHeartbeat},
		CheckWorker:  true,
	})
	if restartHealth.Status != doctor.StatusHealthy {
		t.Fatalf("doctor report after restart = %+v, want HEALTHY", restartHealth)
	}

	drained := make(chan struct{})
	registry2 := workerpool.NewRegistry()
	registry2.Register("foundation-probe", workerpool.HandlerFunc(func(context.Context, ports.DurableJob) error {
		close(drained)
		return nil
	}))
	pool2, err := workerpool.New(store2, registry2, workerpool.Config{
		Owner: cfg.WorkerID + "-restarted", LeaseTTL: cfg.LeaseTTL, HeartbeatEvery: cfg.LeaseHeartbeat,
		PollInterval: 20 * time.Millisecond, ShutdownGrace: time.Second, RecoveryInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("New (pool2): %v", err)
	}
	pool2Ctx, cancelPool2 := context.WithCancel(ctx)
	pool2Done := make(chan error, 1)
	go func() { pool2Done <- pool2.Run(pool2Ctx) }()

	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("orphaned job was never drained after restart — the startup recovery scan should have reclaimed it")
	}
	cancelPool2()
	if err := <-pool2Done; err != nil {
		t.Fatalf("pool2.Run: %v", err)
	}

	if _, _, err := store2.ClaimJob(ctx, "prober", time.Second); !errors.Is(err, ports.ErrNoJobAvailable) {
		t.Fatalf("ClaimJob after drain err = %v, want ports.ErrNoJobAvailable (the job must be SUCCEEDED, not claimable again)", err)
	}

	// --- everything committed before the kill survived it ---
	artifactsAfterRestart, err := artifactstore.New(cfg.ArtifactRoot)
	if err != nil {
		t.Fatalf("artifactstore.New (restart): %v", err)
	}
	reader, err := artifactsAfterRestart.Open(ctx, artifactRef)
	if err != nil {
		t.Fatalf("Open artifact after restart: %v", err)
	}
	defer reader.Close()
	roundTripped, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read artifact content after restart: %v", err)
	}
	if !bytes.Equal(roundTripped, artifactContent) {
		t.Fatalf("artifact content after restart = %q, want %q", roundTripped, artifactContent)
	}

	// The domain event (and its outbox row) committed before the kill
	// must still be there: re-appending the exact same
	// (aggregate_type, aggregate_id, sequence) after restart must be
	// rejected as a duplicate — which is only possible if the original
	// row survived the kill/restart cycle intact.
	err = sqlite.NewUnitOfWork(store2).WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Events().Append(ctx, ports.DomainEvent{
			ID: "evt-foundation-1-duplicate-probe", AggregateType: "IntegrationProbe", AggregateID: "probe-1", Sequence: 1,
			EventType: "FoundationProbeRecorded", SchemaVersion: 1, PayloadJSON: `{"ok":true}`,
			CorrelationID: "corr-foundation-1", CreatedAt: time.Now().UTC(),
		})
	})
	if err == nil {
		t.Fatal("re-appending (IntegrationProbe, probe-1, sequence=1) after restart succeeded — want a duplicate-sequence rejection, proving the original event row survived")
	}
}
