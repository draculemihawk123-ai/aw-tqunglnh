package ports

import (
	"context"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
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
	// AdapterBuilds is populated now (V2-07A) with real methods — unlike
	// its near-empty siblings above, it is not a placeholder: this task
	// owns building the immutable AdapterBuildVersion registry (ADR-022)
	// end to end, so its accessor gets a real interface from the start.
	AdapterBuilds() AdapterBuildRepository
}

// CatalogRepository will expose Project/Repository/Component persistence
// once V3-01 builds it.
type CatalogRepository interface{}

// WorkRepository will expose WorkItem/TaskFamily/WorkspaceSet persistence
// once V3-04/V3-06 build it.
type WorkRepository interface{}

// DefinitionsRepository is populated now (V2-09/V2-10): V2-09 gave it
// LoadVersion, the one method its "Graph/dependency compiler" task
// needed; V2-10 ("validate/publish application commands") adds the rest —
// creating a Definition, publishing a new Version (both the eight shared
// kinds and Workflow), and listing every Version a Definition has
// published — so a command handler can do all of it composed inside one
// Tx, alongside whatever Events()/Receipts() write the same handler
// performs (GC-INV-15: a state transition and its domain event must
// commit in the same transaction).
type DefinitionsRepository interface {
	// LoadVersion returns the Version with the given ID, or
	// ErrDefinitionVersionNotFound. The caller is responsible for
	// checking the returned VersionFields' Kind()/DefinitionID() match
	// what the pin claimed — this method resolves by VersionID alone,
	// the same way internal/adapters/sqlite's own existing
	// LoadSharedDefinitionVersion already does, since a mismatched
	// Kind/DefinitionID is a pin-validity question the caller (the
	// resolver, not the repository) owns. It only ever resolves a
	// shared-kind Version — Workflow keeps its own dedicated tables and
	// is never the target of a node-level dependency pin (V2-08's own
	// schema never lets a workflow node pin another Workflow).
	LoadVersion(ctx context.Context, versionID string) (definition.VersionFields, error)

	// CreateDefinition creates a new Definition — DRAFT, generation 1,
	// the same rule definition.Create expresses for every kind, Workflow
	// included, even though Workflow's own row lives in a different
	// table (workflow_definitions, not definitions) than the other eight
	// kinds do.
	CreateDefinition(ctx context.Context, id string, kind definition.Kind, scope definition.Scope, name string, now time.Time) error

	// PublishVersion publishes a new Version for one of the eight shared
	// DefinitionKinds — never KindWorkflow, whose already-compiled
	// candidate (produced by workflowcompiler.CompileAndResolve, which
	// needs its own read-only registry snapshot and so cannot run inside
	// the same write transaction this method is composed inside) is
	// published through PublishWorkflowVersion below instead. Publishing
	// is idempotent by (DefinitionID, CompiledSnapshotHash), never by
	// SourceHash (AK-ARCH-005B): identical compiled content returns the
	// already-published Version rather than inserting a duplicate row.
	PublishVersion(ctx context.Context, req PublishVersionRequest) (definition.VersionFields, error)

	// PublishWorkflowVersion publishes a new WorkflowVersion from an
	// already-compiled candidate — the Tx-composable counterpart of
	// WorkflowPersistence.PublishWorkflowVersion, for a command handler
	// that needs the version insert and whatever domain event/receipt it
	// writes alongside it to commit as one atomic transaction.
	PublishWorkflowVersion(ctx context.Context, def workflow.WorkflowDefinition, candidate workflow.WorkflowVersion) (workflow.WorkflowVersion, error)

	// ListVersions returns every Version definitionID has published,
	// oldest first.
	ListVersions(ctx context.Context, kind definition.Kind, definitionID string) ([]definition.VersionFields, error)
}

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
// The caller still supplies Sequence explicitly rather than this
// repository allocating it, since real per-aggregate sequence allocation
// under concurrent writers belongs to whichever later task first needs
// it composed with a real aggregate write. Append itself allocates
// JournalPosition (database-monotonic, V1-07, ADR-015) and writes exactly
// one matching outbox row in the same transaction (GC-INV-16) — a
// committed event can never be missing its durable-delivery record, even
// if the process crashes immediately after commit.
type EventsRepository interface {
	Append(ctx context.Context, event DomainEvent) error
}

// DomainEvent is the event shape EventsRepository.Append persists to
// domain_events (and, via its outbox row, makes durably deliverable).
// ProjectID, CausationID and Topic are optional; every other field is
// required. CausationID is the ID of the event or command that directly
// caused this one (distinct from CorrelationID, which ties a whole chain
// of related activity together, not just the immediate cause). Topic
// defaults to EventType when left empty — it exists so a caller can group
// events onto a coarser outbox routing channel than one topic per event
// type, once a real consumer needs that.
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
	CausationID   string
	Topic         string
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
