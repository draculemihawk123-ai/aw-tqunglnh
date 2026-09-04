package ports

import (
	"context"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
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

	// GetWorkflowVersion is populated now (V4-01A/V4-02,
	// docs/design/06-v4-runtime-engine.md): StartWorkflowRun's first real
	// caller needs to resolve an already-published WorkflowVersion by ID —
	// composed inside the same Tx as everything else it validates/creates —
	// which neither LoadVersion (shared kinds only) nor ListVersions
	// (definition-scoped listing) can do. It returns the identical
	// workflow.WorkflowVersion WorkflowPersistence.LoadWorkflowVersion
	// already resolves for the spike-era caller, or
	// ErrPersistenceNotFound.
	GetWorkflowVersion(ctx context.Context, versionID string) (workflow.WorkflowVersion, error)
}

// RuntimeRepository is populated now (V4-01,
// docs/design/06-v4-runtime-engine.md): the schema/repository-contract
// foundation for the whole V4 runtime engine. It does not yet expose
// WorkflowRun/NodeRun/ExecutionAttempt/ContextSnapshot/Checkpoint
// persistence — WorkflowPersistence (persistence.go) already covers those
// for the spike, and superseding it into this Tx-composable accessor is
// deferred to whichever later V4 task first needs one of those methods
// composed inside the same transaction as a new concern here (V4-02's
// StartWorkflowRun transaction is the most likely first caller). This
// task's own five new aggregates have no such pre-existing spike-era
// surface, so they get real methods here from the start, the same
// "populated now" treatment AdapterBuildRepository/ReadinessRepository
// already received for the same reason.
type RuntimeRepository interface {
	// CreateWorkflowRun is populated now (V4-02,
	// docs/design/06-v4-runtime-engine.md): the Tx-composable counterpart
	// of WorkflowPersistence.StartWorkflowRun (persistence.go), needed so a
	// command handler can create the run atomically alongside its
	// ExecutionManifest, START NodeRun, domain event and advance job in one
	// UnitOfWork call — the exact "deferred until a later V4 task needs it
	// composed inside the same Tx" moment this interface's own doc comment
	// anticipated. It performs the identical pin/identity validation
	// StartWorkflowRun does (run must be CREATED at version 1, pinned
	// WorkflowVersionID/Hash/DependencyManifest must match the real
	// published WorkflowVersion) but writes through the given Tx instead of
	// opening its own transaction, and returns ErrPersistenceAlreadyExists
	// for a reused RunID exactly like the spike-era method does.
	CreateWorkflowRun(ctx context.Context, run runtime.WorkflowRun) (runtime.WorkflowRun, error)

	// CreateNodeRun is populated now (V4-02): inserts a new NodeRun
	// activation after verifying nodeRun.RunID names a WorkflowRun that
	// exists. Unlike ExecutionAttempt (whose full column set was already
	// persisted from the spike), node_runs' schema does not yet carry
	// EffectiveScope/ExecutionProfileHash — this method persists exactly
	// the columns 0001_initial_schema.sql already has (id, run_id,
	// node_key, activation_sequence, iteration, state, selected_outcome,
	// input_state_hash, version), the same boundary the pre-existing
	// spike-era DispatchNodeIntent/loadNodeRunByID (node_dispatch.go)
	// already draw; persisting the rest is deferred to whichever later V4
	// task first needs it for a real executable node (V4-04's own "resolve
	// effective scope/profile/context inputs").
	CreateNodeRun(ctx context.Context, nodeRun runtime.NodeRun) (runtime.NodeRun, error)

	// GetWorkflowRun is populated now (V4-03,
	// docs/design/06-v4-runtime-engine.md): the scheduler's own "Advance"
	// routing decision needs to re-read a Run's pinned WorkflowVersionID and
	// current SharedState inside the same Tx as everything else it reads/
	// writes, composed the same way CreateWorkflowRun's own sibling reader
	// (StartWorkflowRun's caller) already resolves WorkItem/WorkspaceSet —
	// this is the identical "populated now" reader half CreateWorkflowRun's
	// own writer half already anticipated. Returns ErrPersistenceNotFound
	// for an unknown RunID.
	GetWorkflowRun(ctx context.Context, id string) (runtime.WorkflowRun, error)

	// GetNodeRun is populated now (V4-03): the scheduler's own "Advance"
	// routing decision reads the NodeRun it is asked to route away from —
	// its current State (idempotent-replay check, mirroring
	// workspaceprovision.Handler's own "idempotent early-return" discipline
	// for an already-terminal target), NodeKey and ActivationSequence — all
	// inside the same Tx the eventual TransitionNodeRun/CreateNodeRun calls
	// use. Returns ErrPersistenceNotFound for an unknown NodeRunID.
	GetNodeRun(ctx context.Context, id string) (runtime.NodeRun, error)

	// TransitionNodeRun is populated now (V4-03): the CAS that closes a
	// NodeRun's own routing decision — State/SelectedOutcome/Version — the
	// same optimistic-CAS shape ports.TransitionWorkItemStatusRequest
	// already uses for WorkItem.Status (V4-02). It is deliberately narrow,
	// like that sibling method: no general NodeRunState legality check the
	// way project.CanTransitionRepositoryStatus enforces — the only
	// transition V4-03's own scheduler needs today is RUNNING->SUCCEEDED
	// with a resolved, allow-listed outcome (GC-INV-11); a stale CAS
	// (ExpectedState/ExpectedVersion mismatch) is ErrOptimisticConflict,
	// ErrPersistenceNotFound for an unknown NodeRunID.
	TransitionNodeRun(ctx context.Context, req TransitionNodeRunRequest) (runtime.NodeRun, error)

	// CreateExecutionManifest inserts the one immutable ExecutionManifest a
	// WorkflowRun ever has (GC-INV-06). manifest.RunID must name a
	// WorkflowRun that exists, and manifest.WorkflowVersionID's own
	// WorkflowDefinition must be either installation-shared (nil
	// ProjectID) or belong to the exact same Project as that WorkflowRun —
	// ErrCrossProjectReference otherwise, resolved from the referenced rows
	// themselves, never trusted from the caller. A second call for the same
	// RunID with byte-identical CompiledSnapshotHash/DependencyManifest/
	// BaseRevisionSet is idempotent and returns the already-stored
	// manifest; a second call with any different pin is
	// ErrImmutableVersionConflict — "workflow/compiled dependency/adapter
	// pins không đổi" (this task's own Hoàn thành khi) admits no update
	// path at all, idempotent or otherwise.
	CreateExecutionManifest(ctx context.Context, manifest runtime.ExecutionManifest) (runtime.ExecutionManifest, error)
	// GetExecutionManifest returns the ExecutionManifest for runID, or
	// ErrPersistenceNotFound.
	GetExecutionManifest(ctx context.Context, runID string) (runtime.ExecutionManifest, error)

	// AppendRunManifestAmendment inserts one new RunManifestAmendment
	// (ADR-011) after verifying amendment.RunID names a WorkflowRun that
	// already has an ExecutionManifest and that amendment.PreviousRevision
	// equals the run's current highest amendment revision (0 if it has
	// none yet) — ErrOptimisticConflict on a gap or replay, never a second
	// row for the same next revision. The initial ExecutionManifest itself
	// is never touched: an amendment is always a new, append-only row.
	AppendRunManifestAmendment(ctx context.Context, amendment runtime.RunManifestAmendment) (runtime.RunManifestAmendment, error)
	// ListRunManifestAmendments returns every RunManifestAmendment for
	// runID, oldest-Revision-first.
	ListRunManifestAmendments(ctx context.Context, runID string) ([]runtime.RunManifestAmendment, error)

	// CreateBranchToken inserts a new, ACTIVE BranchToken (HE-14-M09) after
	// verifying token.RunID names a WorkflowRun that exists. A second call
	// for the same (RunID, ForkKey, BranchKey) with an identical
	// CurrentNodeKey is idempotent and returns the already-stored token; a
	// second call with a different CurrentNodeKey is
	// ErrOptimisticConflict — creating is not how a token's CurrentNodeKey
	// advances (that mutation belongs to whichever later task first needs
	// it, V4-10/V4-11).
	CreateBranchToken(ctx context.Context, token runtime.BranchToken) (runtime.BranchToken, error)
	// GetBranchToken returns the BranchToken for (runID, forkKey,
	// branchKey), or ErrPersistenceNotFound.
	GetBranchToken(ctx context.Context, runID, forkKey, branchKey string) (runtime.BranchToken, error)
	// ListBranchTokensForRun returns every BranchToken for runID.
	ListBranchTokensForRun(ctx context.Context, runID string) ([]runtime.BranchToken, error)

	// RecordDecisionArtifact inserts one new, immutable DecisionArtifact
	// (HE-03-M08). There is no corresponding update/delete method, now or
	// ever: every call is a plain append.
	RecordDecisionArtifact(ctx context.Context, artifact runtime.DecisionArtifact) (runtime.DecisionArtifact, error)
	// GetDecisionArtifact returns the DecisionArtifact with the given ID,
	// or ErrPersistenceNotFound.
	GetDecisionArtifact(ctx context.Context, id string) (runtime.DecisionArtifact, error)

	// RecordRunCancellationIntent inserts the one durable cancellation
	// intent a WorkflowRun ever has. It is idempotent by RunID: a second
	// call for a run that already has an intent returns the existing row
	// unchanged (matching its own actor/reason or not — idempotency here
	// means "an intent for this run already exists", not "with this exact
	// payload"), never a second row and never an error, since ADR-020
	// requires CancelRun itself to be idempotent per run.
	RecordRunCancellationIntent(ctx context.Context, intent runtime.RunCancellationIntent) (runtime.RunCancellationIntent, error)
	// GetRunCancellationIntent returns the RunCancellationIntent for runID,
	// or ErrPersistenceNotFound.
	GetRunCancellationIntent(ctx context.Context, runID string) (runtime.RunCancellationIntent, error)

	// RecordWorkItemCancellationIntent is RecordRunCancellationIntent's own
	// counterpart at the WorkItem level, with the identical
	// idempotent-by-WorkItemID contract.
	RecordWorkItemCancellationIntent(ctx context.Context, intent runtime.WorkItemCancellationIntent) (runtime.WorkItemCancellationIntent, error)
	// GetWorkItemCancellationIntent returns the WorkItemCancellationIntent
	// for workItemID, or ErrPersistenceNotFound.
	GetWorkItemCancellationIntent(ctx context.Context, workItemID string) (runtime.WorkItemCancellationIntent, error)
}

// TransitionNodeRunRequest is an optimistic compare-and-swap request for
// NodeRun.State/SelectedOutcome (V4-03), mirroring
// TransitionWorkItemStatusRequest's identical CAS shape. SelectedOutcome is
// only meaningful — and should only ever be non-empty — when NextState is
// runtime.NodeRunSucceeded, the same "only meaningful for one specific
// NextState" discipline TransitionWorkspaceSetStateRequest.BaseRevisionSet
// already documents for itself.
type TransitionNodeRunRequest struct {
	NodeRunID       string
	ExpectedState   runtime.NodeRunState
	ExpectedVersion uint64
	NextState       runtime.NodeRunState
	SelectedOutcome string
}

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

	// HasActiveJobForAggregateIDs is populated now (V3-11,
	// docs/design/05-v3-project-workspace.md): a read-only existence check
	// over durable_jobs for "no active job" — one of
	// RequestWorkspaceSetRelease's own three eligibility checks (this
	// task's own Mục tiêu line). It reports whether any durable_jobs row in
	// a non-terminal state (JobAvailable or JobLeased — JobSucceeded/
	// JobFailed/JobDead/JobCancelled never count) has one of aggregateIDs as
	// its own AggregateID. Every V3 job kind that targets a WorkspaceSet or
	// one of its RepositoryWorkspaces stores that exact ID as AggregateID
	// (internal/app/work.WorkspaceProvisionJobKind's own AggregateType
	// "WorkspaceSet"; internal/app/workspacereconcile.WorkspaceReconciliationJobKind's
	// own AggregateType "RepositoryWorkspace"), so a caller passes the
	// target WorkspaceSetID together with every one of its
	// RepositoryWorkspace IDs to cover both. Returns false, nil for an
	// empty aggregateIDs.
	HasActiveJobForAggregateIDs(ctx context.Context, aggregateIDs []string) (bool, error)
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
