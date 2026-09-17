package projectionrebuildworker_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuildworker"
)

// This file's own fixture is shared by every *_sqlite_test.go file in this
// package — every one of them exercises the real, non-fake stack (a real
// sqlite.Store) per this task's own brief: "Real sqlite tests, not just
// fakes, for the crash-recovery scenarios — this task is almost entirely
// crash-recovery logic."

const testProjectionName = projection.ProjectionName

type workerFixture struct {
	ctx     context.Context
	store   *sqlite.Store
	uow     ports.UnitOfWork
	ids     idsource.Source
	catalog *projection.Catalog
}

func newWorkerFixture(t *testing.T, dbName string) *workerFixture {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), dbName))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	return &workerFixture{
		ctx: ctx, store: store, uow: sqlite.NewUnitOfWork(store),
		ids: idsource.Random{}, catalog: projection.NewCatalog(),
	}
}

func (fx *workerFixture) deps() projectionrebuildworker.Deps {
	return projectionrebuildworker.Deps{
		UnitOfWork: fx.uow, IDs: fx.ids, Catalog: fx.catalog,
		BatchSize: 100, ShadowLeaseTTL: 30 * time.Second, MaxCutoverCatchUpRounds: 3,
	}
}

func (fx *workerFixture) appendEvent(t *testing.T, eventType string, schemaVersion int, payloadJSON string) {
	t.Helper()
	if err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		return tx.Events().Append(fx.ctx, ports.DomainEvent{
			ID: fx.ids.NewID(), ProjectID: "project-1", AggregateType: "WorkItem",
			AggregateID: fx.ids.NewID(), Sequence: 1, EventType: eventType, SchemaVersion: schemaVersion,
			PayloadJSON: payloadJSON, CorrelationID: fx.ids.NewID(), CreatedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("append %s: %v", eventType, err)
	}
}

func (fx *workerFixture) applyLive(t *testing.T, owner string) projection.ApplyBatchOutcome {
	t.Helper()
	outcome, err := projection.ApplyBatch(fx.ctx, fx.uow, fx.catalog, projection.ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: testProjectionName, Owner: owner,
		TTL: 30 * time.Second, BatchSize: 100, Now: time.Now().UTC(), IDs: fx.ids,
	})
	if err != nil {
		t.Fatalf("ApplyBatch(%s): %v", owner, err)
	}
	return outcome
}

func (fx *workerFixture) requestRebuild(t *testing.T, idempotencyKey string) projectionrebuild.RequestProjectionRebuildResult {
	t.Helper()
	result, err := projectionrebuild.RequestProjectionRebuild(fx.ctx, fx.uow, fx.ids,
		ports.Command{
			ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
			Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
			Type: "RequestProjectionRebuild", RequestHash: "hash-" + idempotencyKey,
		},
		projectionrebuild.RequestProjectionRebuildRequest{ProjectID: "project-1", ProjectionName: testProjectionName},
	)
	if err != nil {
		t.Fatalf("RequestProjectionRebuild: %v", err)
	}
	return result
}

func (fx *workerFixture) claimJob(t *testing.T, owner string, ttl time.Duration) (ports.DurableJob, ports.JobLease) {
	t.Helper()
	job, lease, err := fx.store.ClaimJob(fx.ctx, owner, ttl)
	if err != nil {
		t.Fatalf("ClaimJob(%s): %v", owner, err)
	}
	return job, lease
}

func (fx *workerFixture) getOperation(t *testing.T, id string) ports.ProjectionRebuildOperation {
	t.Helper()
	var op ports.ProjectionRebuildOperation
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		var err error
		op, err = tx.ProjectionRebuilds().GetOperation(fx.ctx, id)
		return err
	}); err != nil {
		t.Fatalf("GetOperation(%s): %v", id, err)
	}
	return op
}

func (fx *workerFixture) getActiveGeneration(t *testing.T) (uint64, bool) {
	t.Helper()
	var gen uint64
	var ok bool
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		var err error
		gen, ok, err = tx.Projections().GetActiveGeneration(fx.ctx, "project-1", testProjectionName)
		return err
	}); err != nil {
		t.Fatalf("GetActiveGeneration: %v", err)
	}
	return gen, ok
}

func (fx *workerFixture) listRows(t *testing.T, generation uint64) []ports.ProjectionRow {
	t.Helper()
	var rows []ports.ProjectionRow
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		var err error
		rows, err = tx.Projections().ListProjectionRows(fx.ctx, "project-1", testProjectionName, generation)
		return err
	}); err != nil {
		t.Fatalf("ListProjectionRows(%d): %v", generation, err)
	}
	return rows
}

func (fx *workerFixture) getRow(t *testing.T, generation uint64, entityKey string) ports.ProjectionRow {
	t.Helper()
	var row ports.ProjectionRow
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		var err error
		row, err = tx.Projections().GetProjectionRow(fx.ctx, "project-1", testProjectionName, generation, entityKey)
		return err
	}); err != nil {
		t.Fatalf("GetProjectionRow(%d, %s): %v", generation, entityKey, err)
	}
	return row
}

func (fx *workerFixture) getCheckpoint(t *testing.T, generation uint64) ports.ProjectionCheckpoint {
	t.Helper()
	var checkpoint ports.ProjectionCheckpoint
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		var err error
		checkpoint, err = tx.Projections().GetProjectionCheckpoint(fx.ctx, "project-1", testProjectionName, generation)
		return err
	}); err != nil {
		t.Fatalf("GetProjectionCheckpoint(%d): %v", generation, err)
	}
	return checkpoint
}

// rootWorkItemCreatedPayload is this file's own fixed, valid
// RootWorkItemCreated v1 payload for work-item-1 — every test in this
// package that needs at least one real, applyable event starts from this.
const rootWorkItemCreatedPayload = `{"workItemId":"work-item-1","projectId":"project-1","familyId":"family-1","workspaceSetId":"ws-1","title":"Root"}`

const markedReadyPayload = `{"workItemId":"work-item-1","projectId":"project-1","familyId":"family-1","markedBy":"operator"}`
