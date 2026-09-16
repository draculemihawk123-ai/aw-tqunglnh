package sqlite

import (
	"context"
	"database/sql"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// UnitOfWorkAdapter implements ports.UnitOfWork on top of Store's
// RunSerializedWrite/RunReadOnly (V1-04A): it is a thin wrapper, not a
// second monolithic Store — its only job is turning a *sql.Tx into the
// concern-scoped ports.Tx a handler is allowed to see.
type UnitOfWorkAdapter struct {
	store *Store
}

// NewUnitOfWork wraps store as a ports.UnitOfWork.
func NewUnitOfWork(store *Store) *UnitOfWorkAdapter {
	return &UnitOfWorkAdapter{store: store}
}

var _ ports.UnitOfWork = (*UnitOfWorkAdapter)(nil)

func (u *UnitOfWorkAdapter) WithSerializedWrite(ctx context.Context, fn func(ports.Tx) error) error {
	return u.store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return fn(newTxAdapter(tx))
	})
}

func (u *UnitOfWorkAdapter) WithReadOnly(ctx context.Context, fn func(ports.Tx) error) error {
	return u.store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		return fn(newTxAdapter(tx))
	})
}

// txAdapter is the concrete ports.Tx a handler receives inside one
// UnitOfWork call. Every accessor currently returns a zero-method
// placeholder repository (see ports.Tx's doc comment for why) — this
// struct exists so those placeholders have exactly one, real, shared
// *sql.Tx source once their owning task adds concrete methods, instead of
// each concern inventing its own transaction access.
type txAdapter struct {
	tx *sql.Tx
}

func newTxAdapter(tx *sql.Tx) *txAdapter {
	return &txAdapter{tx: tx}
}

var _ ports.Tx = (*txAdapter)(nil)

func (t *txAdapter) Catalog() ports.CatalogRepository         { return catalogRepository{tx: t.tx} }
func (t *txAdapter) Work() ports.WorkRepository               { return workRepository{tx: t.tx} }
func (t *txAdapter) Definitions() ports.DefinitionsRepository { return definitionsRepository{tx: t.tx} }
func (t *txAdapter) Runtime() ports.RuntimeRepository         { return runtimeRepository{tx: t.tx} }
func (t *txAdapter) Jobs() ports.JobsRepository               { return jobsRepository{tx: t.tx} }
func (t *txAdapter) Events() ports.EventsRepository           { return eventsRepository{tx: t.tx} }
func (t *txAdapter) Receipts() ports.ReceiptsRepository       { return receiptsRepository{tx: t.tx} }
func (t *txAdapter) AdapterBuilds() ports.AdapterBuildRepository {
	return adapterBuildRepository{tx: t.tx}
}
func (t *txAdapter) Readiness() ports.ReadinessRepository { return readinessRepository{tx: t.tx} }
func (t *txAdapter) Wait() ports.WaitRepository           { return waitRepository{tx: t.tx} }
func (t *txAdapter) Approvals() ports.ApprovalRepository  { return approvalRepository{tx: t.tx} }
func (t *txAdapter) Artifacts() ports.ArtifactRepository  { return artifactRepository{tx: t.tx} }
func (t *txAdapter) Messages() ports.MessageRepository    { return messageRepository{tx: t.tx} }
func (t *txAdapter) ContextSnapshots() ports.ContextSnapshotRepository {
	return contextSnapshotRepository{tx: t.tx}
}
func (t *txAdapter) AgentEvents() ports.AgentEventsRepository { return agentEventsRepository{tx: t.tx} }
func (t *txAdapter) Checkpoints() ports.CheckpointsRepository {
	return checkpointsRepository{tx: t.tx}
}
func (t *txAdapter) SafeSettings() ports.SafeSettingsRepository {
	return safeSettingsRepository{tx: t.tx}
}
func (t *txAdapter) AttachmentClaims() ports.AttachmentClaimRepository {
	return attachmentClaimRepository{tx: t.tx}
}
func (t *txAdapter) Projections() ports.ProjectionRepository { return projectionRepository{tx: t.tx} }
func (t *txAdapter) ProjectionRebuilds() ports.ProjectionRebuildRepository {
	return projectionRebuildRepository{tx: t.tx}
}

// Each placeholder repository already carries the shared *sql.Tx so the
// task that populates it with real methods (see ports.Tx's doc comment)
// only adds methods here — it never needs to change how Tx is threaded
// through.
type catalogRepository struct{ tx *sql.Tx }
type workRepository struct{ tx *sql.Tx }
type definitionsRepository struct{ tx *sql.Tx }
type runtimeRepository struct{ tx *sql.Tx }
type jobsRepository struct{ tx *sql.Tx }
type eventsRepository struct{ tx *sql.Tx }
type receiptsRepository struct{ tx *sql.Tx }

var (
	_ ports.CatalogRepository     = catalogRepository{}
	_ ports.WorkRepository        = workRepository{}
	_ ports.DefinitionsRepository = definitionsRepository{}
	_ ports.RuntimeRepository     = runtimeRepository{}
	_ ports.JobsRepository        = jobsRepository{}
	_ ports.EventsRepository      = eventsRepository{}
	_ ports.ReceiptsRepository    = receiptsRepository{}
)

// QueryStoreAdapter implements ports.QueryStore directly against Store's
// connection pool (read-only by convention — it never opens a write
// transaction).
type QueryStoreAdapter struct {
	store *Store
}

// NewQueryStore wraps store as a ports.QueryStore.
func NewQueryStore(store *Store) *QueryStoreAdapter {
	return &QueryStoreAdapter{store: store}
}

var _ ports.QueryStore = (*QueryStoreAdapter)(nil)

func (q *QueryStoreAdapter) Ping(ctx context.Context) error {
	return q.store.db.PingContext(ctx)
}
