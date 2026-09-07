package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
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
	// Wait is populated now (V4-08, docs/design/06-v4-runtime-engine.md):
	// the same "gets a real interface from the start" treatment
	// AdapterBuilds/Readiness above already established for a concern this
	// task owns end to end — WaitRegistration/WaitSignal persistence (see
	// WaitRepository's own doc comment, internal/app/ports/wait.go).
	Wait() WaitRepository
	// Approvals is populated now (V4-09, docs/design/06-v4-runtime-engine.md):
	// the same treatment for ApprovalRequest persistence (see
	// ApprovalRepository's own doc comment, internal/app/ports/approval.go).
	Approvals() ApprovalRepository
	// Artifacts is populated now (V5-01, docs/design/07-v5-execution-evidence.md):
	// the same "gets a real interface from the start" treatment
	// AdapterBuilds/Readiness/Wait/Approvals above already established —
	// the durable Artifact metadata row this task adds on top of the
	// existing V1 ArtifactStore (see ArtifactRepository's own doc comment,
	// internal/app/ports/artifactrecord.go).
	Artifacts() ArtifactRepository
	// Messages is populated now (V5-02, docs/design/07-v5-execution-evidence.md):
	// the durable, append-only task-chat Message row (see
	// MessageRepository's own doc comment, internal/app/ports/message.go).
	Messages() MessageRepository
	// ContextSnapshots is populated now (V5-04, docs/design/07-v5-execution-evidence.md):
	// the durable, immutable per-Attempt manifest (see
	// ContextSnapshotRepository's own doc comment,
	// internal/app/ports/contextsnapshot.go).
	ContextSnapshots() ContextSnapshotRepository
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

	// GetMaxNodeIteration is populated now (V4-07,
	// docs/design/06-v4-runtime-engine.md): reports the highest Iteration
	// value any existing NodeRun row for (runID, nodeKey) already carries —
	// found is false when this exact node key has never been activated in
	// this Run at all (its first-ever activation is Iteration=0; a fresh
	// reactivation via a business-rework edge is Iteration = this max + 1).
	// Deliberately a real MAX query, never a raw COUNT(*) of every NodeRun
	// row for that key (confirmed with the user before writing this task's
	// code): a later task (V4-12A scope-expansion reactivation) creates an
	// ADDITIONAL NodeRun row for an already-visited key that COPIES its
	// predecessor's own Iteration forward rather than incrementing it, and
	// a COUNT(*)-based scheme would silently consume cycle budget for that
	// unrelated reason. Iteration is a business-cycle generation number,
	// not a row-sequence-number of every NodeRun activation for that key.
	GetMaxNodeIteration(ctx context.Context, runID, nodeKey string) (iteration uint32, found bool, err error)

	// ListNodeRunsForRun is populated now (V4-12,
	// docs/design/06-v4-runtime-engine.md): every NodeRun activation for
	// runID, across every node key — the raw material
	// reconcileRunTerminalityTx (internal/app/runtime/completion.go)
	// classifies into live/blocked/terminal groups to decide whether the
	// Run can still make progress. Deliberately unfiltered (not
	// state-scoped at the query layer) so the classification logic itself
	// stays the single source of truth for which states count as which
	// group, rather than splitting that policy across a SQL WHERE clause
	// and Go code.
	ListNodeRunsForRun(ctx context.Context, runID string) ([]runtime.NodeRun, error)

	// ListExecutionAttemptsForRun is populated now (V4-12B,
	// docs/design/06-v4-runtime-engine.md): every ExecutionAttempt whose
	// own NodeRun belongs to runID, across every node key — ExecutionAttempt
	// itself carries no RunID column (only NodeRunID), so this joins
	// through node_runs at the persistence layer rather than making every
	// caller do that join by hand. The CANCEL_RUN_COORDINATOR job's own
	// sweep uses this to find every QUEUED (created but never started)
	// Attempt of a Run and CAS it CANCELLED with
	// TerminationReasonRunCancelledBeforeStart — "không Attempt nào được
	// để mắc kẹt ở QUEUED". Deliberately unfiltered by state, the same
	// "classification is the caller's own job" discipline
	// ListNodeRunsForRun already established.
	ListExecutionAttemptsForRun(ctx context.Context, runID string) ([]runtime.ExecutionAttempt, error)

	// TransitionWorkflowRunState is populated now (V4-12): the fenced CAS
	// over WorkflowRun.State/Version — the Run-level counterpart of
	// TransitionNodeRun, used exactly twice by this codebase so far: END
	// reached with no other live/blocked NodeRun (RUNNING/WAITING ->
	// VERIFYING) and no live/blocked NodeRun remaining with no END reached
	// (RUNNING/WAITING -> FAILED). ExpectedVersion mismatch is
	// ErrOptimisticConflict, ErrPersistenceNotFound for an unknown RunID.
	TransitionWorkflowRunState(ctx context.Context, req TransitionWorkflowRunStateRequest) (runtime.WorkflowRun, error)

	// UpdateWorkflowRunSharedState is populated now (V4-03 correction,
	// docs/design/06-v4-runtime-engine.md — HE-14-M04's own "shared-state
	// typed writes", found missing during review of this task's first
	// pass): a narrow CAS over just WorkflowRun.SharedState/Version, the
	// shared-state half of what the spike-era Store.CompareAndSwapWorkflowRun
	// (persistence.go's own WorkflowRunTransition) does combined with a
	// State transition — AdvanceRun's own typed shared-state patch applies
	// while the Run stays in the exact same State throughout (routing a
	// node never itself changes WorkflowRunState), so reusing that
	// combined CAS would force an artificial no-op State transition just
	// to reach the SharedState write. ErrOptimisticConflict on a stale
	// ExpectedVersion, ErrPersistenceNotFound for an unknown RunID.
	UpdateWorkflowRunSharedState(ctx context.Context, req UpdateWorkflowRunSharedStateRequest) (runtime.WorkflowRun, error)

	// ScheduleNodeRun is populated now (V4-04,
	// docs/design/06-v4-runtime-engine.md): the CAS that closes an
	// executable NodeRun's own scheduling decision — PENDING->QUEUED, with
	// EffectiveScope/ExecutionProfileHash/ManifestRevision pinned together
	// in the same CAS (GC-INV-08's own "pin input hash, effective scope và
	// execution profile trước attempt đầu tiên" — input hash is already
	// pinned at CreateNodeRun time, V4-03; these two siblings plus the
	// exact manifest revision are what V4-04 adds). Deliberately a
	// SEPARATE method from TransitionNodeRun (V4-03's own routing-outcome
	// CAS): that one only ever touches State/SelectedOutcome for the
	// "which edge did this node take" concern AdvanceRun owns, and has no
	// reason to also carry three new scheduling-only columns.
	// ErrOptimisticConflict on a stale ExpectedVersion (including a
	// NodeRun that is no longer PENDING — the idempotent-replay case a
	// caller checks BEFORE calling this, the same discipline AdvanceRun's
	// own idempotent early-return already uses), ErrPersistenceNotFound
	// for an unknown NodeRunID.
	ScheduleNodeRun(ctx context.Context, req ScheduleNodeRunRequest) (runtime.NodeRun, error)

	// CreateExecutionAttempt is populated now (V4-04): inserts the first
	// ExecutionAttempt (AttemptNumber must be 1 — a later technical retry
	// creating AttemptNumber 2+ is V4-06's own scope) for an executable
	// NodeRun, after verifying attempt.NodeRunID names a NodeRun that
	// exists — the execution_attempts counterpart of CreateNodeRun's own
	// "verify the parent exists" discipline. execution_attempts.execution_profile_hash
	// has been NOT NULL since the spike, so attempt.ExecutionProfileHash
	// must be non-empty — runtime.NewExecutionAttempt's own constructor
	// already enforces this before a caller ever reaches this method.
	CreateExecutionAttempt(ctx context.Context, attempt runtime.ExecutionAttempt) (runtime.ExecutionAttempt, error)

	// GetExecutionAttempt is populated now (V4-05,
	// docs/design/06-v4-runtime-engine.md): a NodeSchedulingHandler-adjacent
	// worker (ExecuteNodeHandler) needs to re-load the exact Attempt it is
	// driving — its current State/Version — before transitioning or fenced-
	// finalizing it, composed inside the same Tx as everything else that
	// transition writes. Returns ErrPersistenceNotFound for an unknown
	// AttemptID.
	GetExecutionAttempt(ctx context.Context, id string) (runtime.ExecutionAttempt, error)

	// TransitionExecutionAttempt is populated now (V4-05): the CAS that
	// moves an ExecutionAttempt from one State/Version to a NextState —
	// QUEUED->RUNNING (ExecuteNodeHandler's own unfenced claim-time
	// transition; the job's own already-valid LEASED state at the moment
	// workerpool.Pool invoked the handler is fencing enough for this one)
	// or RUNNING->{SUCCEEDED,FAILED,TIMED_OUT,CANCELLED} (the terminal half
	// of FinalizeExecutionAttempt's own fenced finalize, composed alongside
	// the JobLease/WriteLease fencing checks that method owns — this method
	// itself performs no fencing, exactly like TransitionNodeRun performs
	// none; a caller assembling a fenced transition is responsible for
	// validating fencing itself, before or alongside calling this). A stale
	// caller (wrong ExpectedState/ExpectedVersion) gets ErrOptimisticConflict,
	// never a silent overwrite. TerminationReason is required exactly when
	// NextState is terminal (never for QUEUED->RUNNING).
	TransitionExecutionAttempt(ctx context.Context, req TransitionExecutionAttemptRequest) (runtime.ExecutionAttempt, error)

	// ValidateWriteLeaseFencing is populated now (V4-05): a read-only check
	// that grant is still the authoritative WriteLease for its own
	// (RepositoryWorkspaceID, Generation) — same fence token, same holder
	// JobLease (owner/token), same holder AttemptID, lease/job both still
	// unexpired — composed inside FinalizeExecutionAttempt's own transaction
	// so GC-INV-17's "kết quả worker chỉ được accept khi... mọi WriteLease
	// liên quan còn đúng fencing token" is checked at the exact moment the
	// Attempt's own terminal state is about to be accepted, not as an
	// earlier, separately-racing pre-check. Returns ErrWriteLeaseLost if
	// grant no longer validates.
	ValidateWriteLeaseFencing(ctx context.Context, lease JobLease, grant WriteLeaseGrant) error

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
	// for the same (ForkNodeRunID, BranchKey) — V4-10's own correction:
	// scoped to the exact FORK activation, not merely its NodeKey, since
	// V4-07's own cycle budget can legitimately re-enter the same FORK
	// node key more than once in one Run — with an identical
	// CurrentNodeKey is idempotent and returns the already-stored token; a
	// second call with a different CurrentNodeKey is ErrOptimisticConflict
	// — creating is not how a token's CurrentNodeKey advances (that is
	// TransitionBranchToken's own job).
	CreateBranchToken(ctx context.Context, token runtime.BranchToken) (runtime.BranchToken, error)
	// GetBranchToken returns the BranchToken for (forkNodeRunID,
	// branchKey), or ErrPersistenceNotFound.
	GetBranchToken(ctx context.Context, forkNodeRunID, branchKey string) (runtime.BranchToken, error)
	// GetBranchTokenByID returns the BranchToken named by id, or
	// ErrPersistenceNotFound. Populated now (V4-10): the lookup a NodeRun's
	// own BranchTokenID reference needs — that field only ever carries the
	// bare token ID, not the (ForkNodeRunID, BranchKey) pair
	// GetBranchToken itself is keyed on.
	GetBranchTokenByID(ctx context.Context, id string) (runtime.BranchToken, error)
	// ListBranchTokensForRun returns every BranchToken for runID.
	ListBranchTokensForRun(ctx context.Context, runID string) ([]runtime.BranchToken, error)
	// ListBranchTokensForFork is populated now (V4-11,
	// docs/design/06-v4-runtime-engine.md): the exact token set a JOIN's
	// own readiness policy (ALL/ANY/QUORUM) is evaluated against, scoped
	// to ONE fork occurrence (forkNodeRunID) — never the whole Run
	// (ListBranchTokensForRun's own scope), since a V4-07 cycle can
	// legitimately reactivate the same FORK node key more than once in
	// one Run, and each occurrence's own branches must never be counted
	// toward a different occurrence's own verdict.
	ListBranchTokensForFork(ctx context.Context, forkNodeRunID string) ([]runtime.BranchToken, error)
	// TransitionBranchToken is populated now (V4-10): the fenced CAS that
	// advances a branch's own CurrentNodeKey as it moves through the
	// graph, and that terminalizes it (SUCCEEDED on reaching its own
	// JOIN, FAILED/CANCELLED when the NodeRun it currently names
	// terminates badly) — always composed inside the SAME transaction as
	// whatever NodeRun transition/creation caused it, never independently.
	// ExpectedVersion mismatch is ErrOptimisticConflict, mirroring every
	// other CAS in this codebase.
	TransitionBranchToken(ctx context.Context, req TransitionBranchTokenRequest) (runtime.BranchToken, error)

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
	// TransitionRunCancellationIntentState is populated now (V4-12B): the
	// fenced CAS the CANCEL_RUN_COORDINATOR job's own handler uses to mark
	// an intent COMPLETED once its own quiesce sweep has finished — the
	// durable signal V4-13's own recovery reaper will one day read to tell
	// "coordinator finished" apart from "coordinator died mid-sweep, retry
	// it". No Version field exists on RunCancellationIntent (it is a
	// two-state machine; ExpectedState alone is already the CAS), so this
	// is fenced purely by (RunID, ExpectedState) — ErrOptimisticConflict
	// when the row's own current State does not match ExpectedState (a
	// duplicate coordinator delivery observing an already-COMPLETED intent
	// is exactly this case, and is always a safe, idempotent no-op for the
	// caller to treat it as).
	TransitionRunCancellationIntentState(ctx context.Context, req TransitionRunCancellationIntentStateRequest) (runtime.RunCancellationIntent, error)

	// RecordWorkItemCancellationIntent is RecordRunCancellationIntent's own
	// counterpart at the WorkItem level, with the identical
	// idempotent-by-WorkItemID contract.
	RecordWorkItemCancellationIntent(ctx context.Context, intent runtime.WorkItemCancellationIntent) (runtime.WorkItemCancellationIntent, error)
	// GetWorkItemCancellationIntent returns the WorkItemCancellationIntent
	// for workItemID, or ErrPersistenceNotFound.
	GetWorkItemCancellationIntent(ctx context.Context, workItemID string) (runtime.WorkItemCancellationIntent, error)
	// TransitionWorkItemCancellationIntentState is populated now (V4-12C):
	// TransitionRunCancellationIntentState's own counterpart at the WorkItem
	// level — the fenced CAS CancelWorkItem's own reconciliation step
	// (internal/app/runtime/cancel_work_item.go) uses to mark an intent
	// COMPLETED once every Run belonging to the WorkItem has reached a
	// genuinely terminal state. Fenced purely by (WorkItemID, ExpectedState),
	// identically to its Run-level sibling (WorkItemCancellationIntent
	// carries no Version column either).
	TransitionWorkItemCancellationIntentState(ctx context.Context, req TransitionWorkItemCancellationIntentStateRequest) (runtime.WorkItemCancellationIntent, error)

	// GetRecoveryReaperState is populated now (V4-13,
	// docs/design/06-v4-runtime-engine.md): reads the one, singleton
	// recovery_reaper_state row (migration 26) — RecoveryReaperState's own
	// doc comment explains why a global cursor row exists at all. Returns
	// ErrPersistenceNotFound only if migration 26 itself somehow never ran
	// (the row is seeded by that migration; a real caller should never
	// observe this).
	GetRecoveryReaperState(ctx context.Context) (RecoveryReaperState, error)
	// AdvanceRecoveryReaperGeneration is populated now (V4-13): the fenced
	// CAS the recovery reaper's own self-rescheduling job uses to bump
	// generation by exactly one — the identical req.ExpectedGeneration/
	// req.ExpectedVersion CAS discipline every other transition in this
	// codebase already uses. ErrOptimisticConflict on a stale caller.
	AdvanceRecoveryReaperGeneration(ctx context.Context, req AdvanceRecoveryReaperGenerationRequest) (RecoveryReaperState, error)

	// ListRunCancellationIntentsByState is populated now (V4-13): every
	// RunCancellationIntent currently in state — the recovery reaper's own
	// read this task's task text names directly ("Reaper cũng quét
	// run_cancellation_intents ... còn dở"), scoped to state so the reaper
	// only ever loads the still-REQUESTED stragglers it actually needs to
	// resume, never the (potentially much larger over time) COMPLETED set.
	ListRunCancellationIntentsByState(ctx context.Context, state runtime.CancellationIntentState) ([]runtime.RunCancellationIntent, error)
	// ListWorkItemCancellationIntentsByState mirrors
	// ListRunCancellationIntentsByState at the WorkItem level.
	ListWorkItemCancellationIntentsByState(ctx context.Context, state runtime.CancellationIntentState) ([]runtime.WorkItemCancellationIntent, error)

	// ListOrphanedRunningExecutionAttempts is populated now (V4-13): every
	// ExecutionAttempt currently RUNNING whose own driving EXECUTE_NODE job
	// (AggregateType='ExecutionAttempt', AggregateID=the attempt's own ID —
	// the exact convention finalize.go/schedule.go's own EnqueueJob calls
	// already establish) is no longer actively, provably held by a live
	// worker: the job row does not exist at all, is not currently LEASED, or
	// is LEASED but its own lease_until has already passed as of asOf. This
	// is deliberately a real evidence-based join rather than a bare
	// staleness heuristic on the Attempt's own updated_at (ADR-020's own
	// "không suy... từ... mà từ bằng chứng bền vững" discipline, applied
	// here to "is this attempt orphaned" the same way it already governs
	// "did this attempt's own side effect happen") — an Attempt whose job is
	// still genuinely LEASED with time remaining is never returned, no
	// matter how long it has sat RUNNING.
	ListOrphanedRunningExecutionAttempts(ctx context.Context, asOf time.Time) ([]runtime.ExecutionAttempt, error)

	// GetWriteLeaseRepositoryWorkspaceForAttempt is populated now (V4-13):
	// the one RepositoryWorkspace attemptID's own write_leases row (if any)
	// names — found is false for a read-only attempt that never acquired
	// one. Alpha's own existing worker.ReconcileInterruptedAttempt
	// primitive (internal/app/worker/interruption.go, SPK-04/SPK-09) is
	// itself only ever shaped for a single (RepositoryWorkspaceID,
	// PinnedRevision) pair per attempt — this query matches that same
	// established single-workspace assumption, not a new one this task
	// introduces.
	GetWriteLeaseRepositoryWorkspaceForAttempt(ctx context.Context, attemptID string) (repositoryWorkspaceID string, found bool, err error)

	// ListWorkflowRunsForWorkItem is populated now (V4-12C,
	// docs/design/06-v4-runtime-engine.md): every WorkflowRun a WorkItem has
	// ever had, across its full history — a WorkItem policy allows only one
	// ACTIVE-driving Run at a time (StartWorkflowRun's own READY-only gate),
	// but a WorkItem can accumulate several over its lifetime (an earlier
	// Run cancelled/blocked, the WorkItem cycling back to READY, a later Run
	// started). CancelWorkItem's own quiesce sweep and
	// ResolveWorkItemBlocker's own "no Run nonterminal" precondition both
	// need the complete set, not just the current one — deliberately
	// unfiltered by state, the same "classification is the caller's own job"
	// discipline ListNodeRunsForRun/ListExecutionAttemptsForRun already
	// established.
	ListWorkflowRunsForWorkItem(ctx context.Context, workItemID string) ([]runtime.WorkflowRun, error)

	// CreateScopeExpansionOrigin is populated now (V4-12A,
	// docs/design/06-v4-runtime-engine.md): inserts the durable link
	// between one BLOCKED ExecutionAttempt and the RESERVED RequestID a
	// REQUEST_SCOPE_EXPANSION job will use to raise the real
	// work.ScopeExpansionRequest — created in the SAME transaction that
	// CASes the Attempt/NodeRun to BLOCKED, never afterward.
	// ErrPersistenceAlreadyExists for a reused AttemptID (an Attempt is
	// finalized at most once).
	CreateScopeExpansionOrigin(ctx context.Context, origin runtime.ScopeExpansionOrigin) (runtime.ScopeExpansionOrigin, error)
	// GetScopeExpansionOriginByAttemptID returns the ScopeExpansionOrigin
	// keyed on attemptID, or ErrPersistenceNotFound.
	GetScopeExpansionOriginByAttemptID(ctx context.Context, attemptID string) (runtime.ScopeExpansionOrigin, error)
	// GetScopeExpansionOriginByRequestID returns the ScopeExpansionOrigin
	// whose own RequestID matches — the lookup ApproveScopeExpansion
	// (internal/app/work, V3-08) uses to decide whether a just-approved
	// request has a runtime origin at all (a manually/UI-created request
	// never does) before enqueuing a SCOPE_EXPANSION_RECONCILE job.
	// ErrPersistenceNotFound when no origin references requestID.
	GetScopeExpansionOriginByRequestID(ctx context.Context, requestID string) (runtime.ScopeExpansionOrigin, error)
	// TransitionScopeExpansionOrigin is populated now (V4-12A): the fenced
	// CAS the SCOPE_EXPANSION_RECONCILE job's own self-rescheduling state
	// machine uses for every observed transition — bumping PollGeneration
	// alongside enqueuing a successor job (so a duplicate delivery of the
	// SAME attempt can never mint two successors), and setting
	// ReconcileStatus/ReactivatedNodeRunID on a terminal outcome.
	// ExpectedVersion mismatch is ErrOptimisticConflict.
	TransitionScopeExpansionOrigin(ctx context.Context, req TransitionScopeExpansionOriginRequest) (runtime.ScopeExpansionOrigin, error)
}

// TransitionScopeExpansionOriginRequest is the CAS request for
// RuntimeRepository.TransitionScopeExpansionOrigin (V4-12A).
// NextPollGeneration/NextReactivatedNodeRunID are optional: nil leaves the
// corresponding column unchanged, letting one CAS method serve both the
// "still pending, just bumped the poll generation" transition (only
// NextPollGeneration set) and the "terminal outcome" transitions (
// NextReconcileStatus always set; NextReactivatedNodeRunID additionally set
// only for the REACTIVATED outcome).
type TransitionScopeExpansionOriginRequest struct {
	AttemptID                string
	ExpectedVersion          uint64
	NextReconcileStatus      runtime.ScopeExpansionReconcileStatus
	NextPollGeneration       *uint64
	NextReactivatedNodeRunID *string
}

// TransitionBranchTokenRequest is the CAS request for
// RuntimeRepository.TransitionBranchToken (V4-10). NextCurrentNodeKey and
// NextState are always both supplied: an ordinary hop within a branch sets
// NextState to runtime.BranchTokenActive (unchanged) alongside the new
// CurrentNodeKey; reaching the branch's own JOIN sets NextState to
// runtime.BranchTokenSucceeded alongside the JOIN's own NodeKey; a branch
// NodeRun terminating badly sets NextState to
// runtime.BranchTokenFailed/BranchTokenCancelled, leaving CurrentNodeKey
// at whatever node actually failed.
type TransitionBranchTokenRequest struct {
	BranchTokenID      string
	ExpectedVersion    uint64
	NextState          runtime.BranchTokenState
	NextCurrentNodeKey string
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

// TransitionWorkflowRunStateRequest is the CAS request for
// RuntimeRepository.TransitionWorkflowRunState (V4-12).
type TransitionWorkflowRunStateRequest struct {
	RunID           string
	ExpectedState   runtime.WorkflowRunState
	ExpectedVersion uint64
	NextState       runtime.WorkflowRunState
	// NextCancelEpoch is populated now (V4-12B): nil leaves
	// WorkflowRun.CancelEpoch untouched (every pre-V4-12B caller); non-nil
	// sets it in the SAME CAS as the State transition — CancelRun's own
	// only real use, bumping NULL->1 in the identical transaction that
	// moves the Run to CANCELLING.
	NextCancelEpoch *uint64
}

// TransitionRunCancellationIntentStateRequest is the CAS request for
// RuntimeRepository.TransitionRunCancellationIntentState (V4-12B).
type TransitionRunCancellationIntentStateRequest struct {
	RunID         string
	ExpectedState runtime.CancellationIntentState
	NextState     runtime.CancellationIntentState
}

// TransitionWorkItemCancellationIntentStateRequest is the CAS request for
// RuntimeRepository.TransitionWorkItemCancellationIntentState (V4-12C),
// mirroring TransitionRunCancellationIntentStateRequest exactly at the
// WorkItem level.
type TransitionWorkItemCancellationIntentStateRequest struct {
	WorkItemID    string
	ExpectedState runtime.CancellationIntentState
	NextState     runtime.CancellationIntentState
}

// RecoveryReaperState is the recovery reaper's own singleton generation
// cursor (V4-13, migration 26) — see that migration's own doc comment for
// why a single global row, rather than a per-Run/per-Attempt origin like
// ScopeExpansionOrigin.PollGeneration, is this coordinator's own fencing
// primitive.
type RecoveryReaperState struct {
	Generation uint64
	Version    uint64
}

// AdvanceRecoveryReaperGenerationRequest is the CAS request for
// RuntimeRepository.AdvanceRecoveryReaperGeneration (V4-13).
type AdvanceRecoveryReaperGenerationRequest struct {
	ExpectedGeneration uint64
	ExpectedVersion    uint64
}

// UpdateWorkflowRunSharedStateRequest is the CAS request for
// RuntimeRepository.UpdateWorkflowRunSharedState (V4-03 correction):
// SharedState replaces the row's entire shared_state_json — the caller
// (AdvanceRun) is responsible for merging its own patch into the
// already-loaded current value first, the same "read-modify-write inside
// one Tx" shape every other CAS in this codebase uses.
type UpdateWorkflowRunSharedStateRequest struct {
	RunID           string
	ExpectedVersion uint64
	SharedState     json.RawMessage
}

// ScheduleNodeRunRequest is the CAS request for
// RuntimeRepository.ScheduleNodeRun (V4-04). ExpectedVersion must observe
// the NodeRun as PENDING (the CAS itself enforces the PENDING->QUEUED
// transition; the caller does not separately state ExpectedState the way
// TransitionNodeRunRequest does, since QUEUED has exactly one legal
// predecessor for this task's own scope).
type ScheduleNodeRunRequest struct {
	NodeRunID            string
	ExpectedVersion      uint64
	EffectiveScope       []work.RepositoryScope
	ExecutionProfileHash string
	ManifestRevision     uint64
}

// TransitionExecutionAttemptRequest is the CAS request for
// RuntimeRepository.TransitionExecutionAttempt (V4-05).
type TransitionExecutionAttemptRequest struct {
	AttemptID         string
	ExpectedState     runtime.ExecutionAttemptState
	ExpectedVersion   uint64
	NextState         runtime.ExecutionAttemptState
	TerminationReason runtime.TerminationReason
	// FailureCode is populated now (V4-06): the exact errorcode.Code a
	// terminal FAILED or TIMED_OUT attempt classifies as — see
	// runtime.ExecutionAttempt.FailureCode's own doc comment. Blank for
	// every other NextState.
	FailureCode errorcode.Code
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
// HeartbeatJob/AcquireWriteLeases stay outside this interface (Store's
// existing JobQueue/WriteLeaseManager cover them) until a later task
// genuinely needs one of those composed inside a shared Tx too —
// ValidateActiveJob/CompleteJob are that later task (V4-05,
// FinalizeExecutionAttempt's own fenced finalize, GC-INV-17/18): they
// needed to be composed alongside the SAME transaction's Attempt CAS and
// domain event, which the pre-existing flat Store.CompleteJob (its own
// always-separate transaction) cannot satisfy.
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

	// ValidateActiveJob is populated now (V4-05): a read-only fencing check
	// that lease still names a job LEASED (state, owner, token, unexpired
	// lease_until) with the exact given (aggregateType, aggregateID) — the
	// GC-INV-17/18 "worker's proposed result is only accepted when its
	// JobLease still has the correct fencing token" check, run at the START
	// of FinalizeExecutionAttempt's own transaction so a lease that was
	// already lost never gets as far as touching the Attempt's own row at
	// all. Returns ErrJobLeaseLost if lease no longer validates.
	ValidateActiveJob(ctx context.Context, lease JobLease, aggregateType, aggregateID string) error

	// CompleteJob is populated now (V4-05): the Tx-composable counterpart
	// of Store.CompleteJob (JobQueue), for FinalizeExecutionAttempt's own
	// fenced finalize — see this interface's own doc comment for why this
	// needed to move here rather than staying flat. Same fencing semantics
	// as the flat method: state LEASED, owner/token match, lease_until
	// still unexpired; ErrJobLeaseLost otherwise.
	CompleteJob(ctx context.Context, lease JobLease) error

	// FenceAndCancelRunJobs is populated now (V4-12B,
	// docs/design/06-v4-runtime-engine.md): CancelRun's own atomic "fence
	// every RUN_WORK job of this run" step, run in the SAME transaction as
	// the RunCancellationIntent record and the CAS to CANCELLING. For
	// every non-terminal (AVAILABLE or LEASED) RUN_WORK job whose RunID
	// matches runID and whose own CancelEpoch is still nil: sets
	// CancelEpoch (so it can never be claimed — or, for one already
	// LEASED, re-validated at a worker's own two checkpoints — again),
	// and additionally CASes an AVAILABLE one straight to JobCancelled
	// (nothing was ever dispatched for it, so there is nothing to wait
	// on) — a LEASED one stays LEASED; the worker already holding it will
	// notice the fence at its own checkpoints. A CONTROL job is never
	// touched, by construction (this method only ever targets
	// JobClassRunWork rows). Returns the number of jobs affected.
	FenceAndCancelRunJobs(ctx context.Context, runID string) (int64, error)
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
