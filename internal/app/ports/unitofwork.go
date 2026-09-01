package ports

import (
	"context"
	"time"
)

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
// and vice versa. Catalog/Work/Definitions/Runtime/Jobs are deliberately
// left as thin, near-empty interfaces here: their concrete methods are
// added by the task that actually owns building that concern against a
// real caller (Catalog: V3-01; Work: V3-04/V3-06; Definitions: V2;
// Runtime: V4; Jobs: whichever later task first needs
// EnqueueJob/ClaimJob/AcquireWriteLeases composed inside a shared Tx — the
// pre-existing sqlite.Store already covers them outside one) — adding
// methods speculatively ahead of a real handler that calls them is
// exactly what 00-roadmap.md §3 warns against ("Không chia chỉ để tạo
// file/field nếu phần đó chưa có contract test hoặc behavior quan sát
// được"). Events and Receipts are populated now (V1-06): a minimal
// domain-event append plus the command-receipt idempotency ledger, just
// enough for V1-06's own "một handler mẫu commit state+event+receipt
// atomically" — full outbox/sequence-allocation semantics remain V1-07's
// job.
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

// EventsRepository appends a domain event inside the current transaction.
// This is deliberately minimal (V1-06's own illustrative "state+event"
// proof, not V1-07's full outbox/sequence-allocation contract): the
// caller supplies Sequence explicitly rather than this repository
// allocating it, since real per-aggregate sequence allocation under
// concurrent writers is V1-07's job (GC-INV-15).
type EventsRepository interface {
	Append(ctx context.Context, event DomainEvent) error
}

// DomainEvent is the minimal event shape EventsRepository.Append persists
// to domain_events. ProjectID is optional (some aggregates are
// installation-scoped); every other field is required.
type DomainEvent struct {
	ID            string
	ProjectID     string // empty means installation-scoped, stored as NULL
	AggregateType string
	AggregateID   string
	Sequence      int64
	EventType     string
	SchemaVersion int
	PayloadJSON   string
	CorrelationID string
	CreatedAt     time.Time
}

// ReceiptsRepository is the command-receipt idempotency ledger a handler
// consults before doing real work, and writes to atomically with
// whatever state/event change the command causes (V1-06,
// docs/design/00-roadmap.md's GC-INV-35).
type ReceiptsRepository interface {
	// Load returns the stored receipt for (actor, scope, idempotencyKey,
	// commandType) if one exists.
	Load(ctx context.Context, actor string, scope CommandScope, idempotencyKey, commandType string) (Receipt, bool, error)
	// Record stores a new receipt, or returns ErrReceiptConflict if one
	// already exists for the same key with a different RequestHash. A
	// handler checks Load first for the identical-payload replay case;
	// Record is the storage layer's own second line of defense against a
	// different-payload conflict racing in concurrently.
	Record(ctx context.Context, receipt Receipt) error
}

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
