package ports

import (
	"context"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
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
// real caller (Catalog: V3-01; Work: V3-04, see ports/work.go — V3-06 adds
// RepositoryWorkspace persistence for provisioning on top of it;
// Definitions: V2; Runtime: V4; Jobs: whichever later task first needs
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
	// Readiness is populated now (V3-07,
	// docs/design/05-v3-project-workspace.md): the same "gets a real
	// interface from the start" treatment AdapterBuilds above already
	// established for a concern this task owns end to end — readiness
	// profiles, pre-change baseline evidence and this task's own narrow
	// typed environment blocker (see ReadinessRepository's own doc
	// comment, internal/app/ports/readiness.go).
	Readiness() ReadinessRepository
}

// CatalogRepository is populated now (V3-01,
// docs/design/05-v3-project-workspace.md): Project/Repository/Component/
// ComponentPackAssignment persistence, composed inside the same
// UnitOfWork call whatever Events()/Receipts()/Jobs() write alongside it
// (GC-INV-15: a state transition and its domain event commit in the same
// transaction — RegisterRepository's own "atomically tạo record
// REGISTERING và probe job/outbox" needs exactly this). Every method here
// takes and returns identity as plain strings (mirroring
// DefinitionsRepository.CreateDefinition/LoadVersion's own id-as-string
// convention), converting to/from the typed project.ProjectID/
// RepositoryID/ComponentID/ComponentPackAssignmentID domain types
// internally. list/filter methods (ListProjectRepositories,
// ListComponentPackAssignments) scope strictly by the stored foreign-key
// column alone, never by name/path/slug — V3-01's own "Hoàn thành khi:
// list/filter không suy identity từ slug/cwd/remote".
type CatalogRepository interface {
	// CreateProject inserts a new Project row (ACTIVE, generation 1, per
	// project.NewProject). Not itself a cited public command in
	// docs/architecture/04-go-core-spec.md's §8 command table for V3-01's
	// own scope (ADR-025 makes CreateProject installation-scoped, a
	// concern this task's own Thực hiện line never names) — this method
	// exists purely so RegisterRepository/CreateComponent has a Project
	// to reference and so tests can set up fixtures without reaching
	// into sqlite directly; a later task adding the full idempotent
	// CreateProject command wraps this same method rather than
	// duplicating the insert.
	CreateProject(ctx context.Context, req CreateProjectRequest) (project.Project, error)
	// GetProject returns the Project with the given ID, or
	// ErrPersistenceNotFound.
	GetProject(ctx context.Context, id string) (project.Project, error)

	// RegisterRepository atomically creates a new Repository row —
	// always RepositoryRegistering (project.NewRepository's own rule) —
	// after verifying req.ProjectID names a Project that actually exists
	// (ErrPersistenceNotFound otherwise). It does not itself enqueue the
	// probe job or append a domain event: those are the calling command
	// handler's job (internal/app/catalog.RegisterRepository), composed
	// alongside this call inside the same ports.Tx, exactly the way
	// DefinitionsRepository.CreateDefinition never itself appends
	// DefinitionCreated either.
	RegisterRepository(ctx context.Context, req RegisterRepositoryRequest) (project.Repository, error)
	// GetRepository returns the Repository with the given ID, or
	// ErrPersistenceNotFound.
	GetRepository(ctx context.Context, id string) (project.Repository, error)
	// ListProjectRepositories returns every Repository whose stored
	// project_id column equals projectID — never a name/slug match.
	ListProjectRepositories(ctx context.Context, projectID string) ([]project.Repository, error)

	// TransitionRepositoryStatus is populated now (V3-02,
	// docs/design/05-v3-project-workspace.md): an optimistic
	// compare-and-swap on a Repository's own status, the persistence half
	// of every legal RepositoryStatus edge project.CanTransitionRepositoryStatus
	// declares (REGISTERING->PROBING, BLOCKED->PROBING, PROBING->ACTIVE,
	// PROBING->BLOCKED). It validates req.ExpectedStatus->req.NextStatus is
	// itself a legal transition via project.CanTransitionRepositoryStatus
	// before ever touching storage (mirroring
	// WorkflowPersistence.CompareAndSwapWorkflowRun's own
	// validateWorkflowRunTransition-first discipline), then performs the
	// CAS: req.ExpectedVersion must match the row's current version and
	// its current status must equal req.ExpectedStatus, or the whole call
	// fails with ErrOptimisticConflict (a stale caller never silently
	// overwrites a transition another worker already committed) —
	// ErrPersistenceNotFound if the Repository does not exist at all. On
	// success, version increments by exactly one and the returned
	// Repository reflects the new row.
	TransitionRepositoryStatus(ctx context.Context, req TransitionRepositoryStatusRequest) (project.Repository, error)

	// RecordRepositoryProbeAttempt appends one row to the append-only
	// repository_probe_attempts evidence log (V3-02,
	// docs/design/01-system-design.md §6.1) — never updated or replaced
	// once written. req.JobID is UNIQUE at the storage layer: this is what
	// "active probe idempotent" (§6.1) means concretely — the same durable
	// job can never produce two attempt rows, no matter how many times its
	// own claim is retried after a crash.
	RecordRepositoryProbeAttempt(ctx context.Context, req RecordRepositoryProbeAttemptRequest) (RepositoryProbeAttempt, error)
	// ListRepositoryProbeAttempts returns every RepositoryProbeAttempt for
	// repositoryID, oldest-CreatedAt-first — the "probe history" evidence
	// docs/design/01-system-design.md's own API sketch names ("GET
	// /repositories/{id}/onboarding | trạng thái/error/probe history có
	// thể hành động").
	ListRepositoryProbeAttempts(ctx context.Context, repositoryID string) ([]RepositoryProbeAttempt, error)

	// CreateComponent inserts a new Component row after verifying
	// req.RepositoryID names a Repository that actually exists and that
	// its own project_id matches req.ProjectID exactly —
	// ErrCrossProjectReference otherwise (the Repository's own stored
	// project is always what is checked, never trusted from the
	// request, the same "resolve from the referenced row itself" rule
	// publishSharedDefinitionVersionTx's own cross-project dependency
	// check already follows).
	CreateComponent(ctx context.Context, req CreateComponentRequest) (project.Component, error)
	// GetComponent returns the Component with the given ID, or
	// ErrPersistenceNotFound.
	GetComponent(ctx context.Context, id string) (project.Component, error)

	// AssignComponentPack appends a new ComponentPackAssignment row after
	// verifying req.ComponentID names a Component that actually exists
	// and that its own project_id matches req.ProjectID —
	// ErrCrossProjectReference otherwise. It never mutates or replaces an
	// existing assignment (see project.ComponentPackAssignment's own doc
	// comment): a second assignment for the same Component is always a
	// new row with its own EffectiveAt.
	AssignComponentPack(ctx context.Context, req AssignComponentPackRequest) (project.ComponentPackAssignment, error)
	// ListComponentPackAssignments returns every ComponentPackAssignment
	// for componentID, ordered oldest-EffectiveAt-first.
	ListComponentPackAssignments(ctx context.Context, componentID string) ([]project.ComponentPackAssignment, error)
	// GetEffectiveComponentPackAssignment returns the ComponentPackAssignment
	// with the greatest EffectiveAt that is still <= at — "the pack
	// version resolved configuration should use as of time T" — or
	// ErrPersistenceNotFound if componentID has no assignment effective
	// by then.
	GetEffectiveComponentPackAssignment(ctx context.Context, componentID string, at time.Time) (project.ComponentPackAssignment, error)
}

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

// JobsRepository gains its first real method now (V3-01,
// docs/design/05-v3-project-workspace.md): EnqueueJob, composed inside the
// same transaction as RegisterRepository's own Repository-row insert, so
// the REPOSITORY_PROBE job it enqueues is genuinely atomic with the
// Repository becoming REGISTERING — "RegisterRepository atomically tạo
// record REGISTERING và probe job/outbox nguyên tử"
// (docs/architecture/04-go-core-spec.md §4.1) is impossible to satisfy
// with the pre-existing sqlite.Store.EnqueueJob alone, since that method
// always opens (and commits) its own separate transaction. ClaimJob/
// HeartbeatJob/CompleteJob/AcquireWriteLeases stay outside this interface
// (Store's existing JobQueue/WriteLeaseManager cover them) until a later
// task genuinely needs one of those composed inside a shared Tx too — the
// same "don't add speculatively" discipline every other placeholder
// concern here follows.
type JobsRepository interface {
	EnqueueJob(ctx context.Context, req EnqueueJobRequest) (DurableJob, error)
}

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
