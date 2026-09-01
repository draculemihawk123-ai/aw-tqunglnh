package ports

import "context"

// UnitOfWork is the one write/read-transaction boundary application
// command handlers use (docs/design/03-v1-alpha-foundation.md V1-05): a
// handler commits state atomically by calling WithSerializedWrite, never
// by holding an adapter-specific transaction type directly.
// WithSerializedWrite corresponds to V1-04A's RunSerializedWrite semantic
// contract (claim/version/JournalPosition allocation); WithReadOnly to
// RunReadOnly — a caller names which contract it needs, never the SQL
// (BEGIN IMMEDIATE) that satisfies it. fn must never open a nested
// transaction or make an external call (network, process spawn,
// filesystem outside the artifact port): everything it does must be
// through the Tx it receives, so the whole call commits or rolls back as
// one atomic unit.
type UnitOfWork interface {
	WithSerializedWrite(ctx context.Context, fn func(Tx) error) error
	WithReadOnly(ctx context.Context, fn func(Tx) error) error
}

// Tx is the concern-scoped repository surface available inside one
// UnitOfWork call — replacing WorkflowPersistence's single flat interface
// (internal/app/ports/persistence.go) with one accessor per concern, so a
// handler that only needs Definitions never has to see Runtime's surface
// and vice versa. Catalog/Work/Definitions/Runtime/Receipts are
// deliberately left as thin, near-empty interfaces here: their concrete
// methods are added by the task that actually owns building that concern
// against a real caller (Catalog: V3-01; Work: V3-04/V3-06; Definitions:
// V2; Runtime: V4; Receipts: V1-06) — adding methods speculatively ahead
// of a real handler that calls them is exactly what 00-roadmap.md §3
// warns against ("Không chia chỉ để tạo file/field nếu phần đó chưa có
// contract test hoặc behavior quan sát được"). Jobs and Events keep the
// same treatment for the same reason: EnqueueJob/ClaimJob/domain-event
// append already exist as real, tested logic in the pre-existing
// sqlite.Store (V0 spike) with their own internal transaction handling;
// re-deriving Tx-composable versions belongs to whichever later task
// first needs true cross-repository atomicity through this port, once a
// real caller makes the exact required method shape clear.
type Tx interface {
	Catalog() CatalogRepository
	Work() WorkRepository
	Definitions() DefinitionsRepository
	Runtime() RuntimeRepository
	Jobs() JobsRepository
	Events() EventsRepository
	Receipts() ReceiptsRepository
}

// CatalogRepository will expose Project/Repository/Component persistence
// once V3-01 builds it.
type CatalogRepository interface{}

// WorkRepository will expose WorkItem/TaskFamily/WorkspaceSet persistence
// once V3-04/V3-06 build it.
type WorkRepository interface{}

// DefinitionsRepository will expose WorkflowDefinition/WorkflowVersion
// (and the other DefinitionKinds) persistence once V2 builds it —
// superseding WorkflowPersistence's PublishWorkflowVersion/
// LoadWorkflowVersion methods with a Tx-composable equivalent.
type DefinitionsRepository interface{}

// RuntimeRepository will expose WorkflowRun/NodeRun/ExecutionAttempt/
// ContextSnapshot/Checkpoint persistence once V4 builds it — superseding
// the rest of WorkflowPersistence's methods.
type RuntimeRepository interface{}

// JobsRepository will expose durable_jobs/write_leases persistence once a
// later task needs it composed inside a UnitOfWork transaction alongside
// other concerns (the pre-existing sqlite.Store.EnqueueJob/ClaimJob/
// AcquireWriteLeases already cover this outside a shared Tx).
type JobsRepository interface{}

// EventsRepository will expose domain_events append/query once a later
// task needs it composed inside a UnitOfWork transaction (every existing
// domain_events writer today already appends it correctly inside its own
// aggregate-specific transaction — see internal/adapters/sqlite/
// workflow_store.go, node_dispatch.go, workspace_lifecycle.go,
// attempt_store.go).
type EventsRepository interface{}

// ReceiptsRepository will expose command_receipts persistence once V1-06
// builds the command envelope this repository backs.
type ReceiptsRepository interface{}

// QueryStore is the read path V1-05 keeps separate from UnitOfWork's
// write-transaction-scoped Tx ("query store tách read"): a query does not
// need — and must never require — a write-intent transaction just to
// read projection-shaped state. It is intentionally its own top-level
// port, not a Tx accessor, so a query handler's dependency signature
// documents that it can never mutate anything.
type QueryStore interface {
	// Ping proves a QueryStore implementation is reachable/healthy. Real
	// query methods are added by whichever task first builds a read
	// handler against this port (V6's projection query endpoints are the
	// first likely caller).
	Ping(ctx context.Context) error
}
