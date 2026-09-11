package ports

import (
	"context"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// WorkRepository is populated now (V3-04, docs/design/05-v3-project-workspace.md):
// CreateRootWorkItem is the one public command that atomically creates a
// root WorkItem, its owning TaskFamily, a WorkspaceSet intent and the
// initial RepositoryScope grants (AK-ARCH-011, GC-INV-01) — this interface
// gives that command handler the persistence surface it composes inside one
// ports.Tx, alongside whatever Jobs()/Events()/Receipts() it writes in the
// same transaction.
//
// Each Create* method takes an already-constructed, already-validated
// domain value (built via the domain package's own pure constructor —
// work.NewRootWorkItem/work.NewTaskFamily/workspace.NewWorkspaceSet/
// work.NewRepositoryScope) rather than a flat request-of-primitives DTO the
// way ports.RegisterRepositoryRequest is shaped: this concern creates
// several closely coupled aggregates inside one command (WorkItem ->
// TaskFamily -> WorkspaceSet -> N RepositoryScope grants, each one's own
// domain constructor validating against the aggregate built just before
// it — e.g. NewTaskFamily needs the already-built root WorkItem to check
// Kind/ParentID/FamilyID agree), so construction/validation happens once in
// the application command handler, not re-derived from primitives at each
// of four separate persistence call sites. This mirrors
// DefinitionsRepository.PublishWorkflowVersion's own "accept an
// already-constructed domain value" convention (that method's own
// already-compiled workflow.WorkflowVersion candidate), not
// CatalogRepository.RegisterRepository's "construct the domain value
// inside the persistence method" convention — the two existing conventions
// in this codebase differ precisely on whether the caller already had to
// build the domain value for its own purposes before persisting it, which
// here it does (root/family/set/scope are all validated in sequence before
// any of them can be safely persisted, since a later one's own validity
// depends on the earlier one's exact fields).
//
// Persistence order matters: work_items.family_id is a real foreign key
// against task_families(id) (0001_initial_schema.sql), so a caller must
// call CreateTaskFamily before CreateWorkItem — the reverse of the
// aggregates' own conceptual creation order ("a root WorkItem creates its
// TaskFamily", GC-INV-01) but required by the schema's own FK direction.
// workspace_sets.family_id is the same kind of FK, so CreateWorkspaceSet
// must also follow CreateTaskFamily (its order relative to CreateWorkItem
// does not matter, since nothing references work_items from
// workspace_sets or family_repository_scopes).
type WorkRepository interface {
	// CreateWorkItem inserts a new WorkItem row after verifying
	// item.ProjectID names a Project that exists and item.FamilyID names a
	// TaskFamily that exists — ErrPersistenceNotFound otherwise. It never
	// mutates item; the return value is item itself for symmetry with
	// CatalogRepository's own Create* methods.
	CreateWorkItem(ctx context.Context, item work.WorkItem) (work.WorkItem, error)
	// GetWorkItem returns the WorkItem with the given ID, or
	// ErrPersistenceNotFound.
	GetWorkItem(ctx context.Context, id string) (work.WorkItem, error)

	// TransitionWorkItemStatus is populated now (V4-02,
	// docs/design/06-v4-runtime-engine.md): the first real caller of a
	// WorkItemStatus CAS is StartWorkflowRun's own READY->ACTIVE transition
	// ("ACTIVE cần WorkspaceSet ready và run started",
	// docs/design/01-system-design.md §5.2). It mirrors
	// TransitionRepositoryStatus/TransitionWorkspaceSetState's identical CAS
	// shape: req.ExpectedVersion must match the row's current version and
	// its current status must equal req.ExpectedStatus, or the whole call
	// fails with ErrOptimisticConflict — ErrPersistenceNotFound if the
	// WorkItem does not exist at all. This is deliberately narrow (no
	// general WorkItemStatus state-machine legality check the way
	// project.CanTransitionRepositoryStatus enforces for Repository): the
	// only transition any real caller needs today is READY->ACTIVE, and a
	// stale CAS observing WorkItem is already ACTIVE (a second
	// StartWorkflowRun call racing on the same WorkItem, or one called while
	// a prior run is still active) is exactly how "một WorkItem policy chỉ
	// có số active run cho phép" (this task's own Hoàn thành khi) is
	// enforced — structurally, by this same CAS, not by a separate active-run
	// counter.
	TransitionWorkItemStatus(ctx context.Context, req TransitionWorkItemStatusRequest) (work.WorkItem, error)

	// CreateTaskFamily inserts a new TaskFamily row after verifying
	// family.ProjectID names a Project that exists — ErrPersistenceNotFound
	// otherwise. It does not itself verify family.RootWorkItemID names an
	// existing WorkItem row: CreateRootWorkItem always calls this before
	// CreateWorkItem (see this interface's own doc comment on persistence
	// order), and root_work_item_id carries no FK constraint at the schema
	// level for exactly that reason (0001_initial_schema.sql).
	CreateTaskFamily(ctx context.Context, family work.TaskFamily) (work.TaskFamily, error)
	// GetTaskFamily returns the TaskFamily with the given ID, or
	// ErrPersistenceNotFound.
	GetTaskFamily(ctx context.Context, id string) (work.TaskFamily, error)

	// CreateWorkspaceSet inserts a new WorkspaceSet row after verifying
	// set.FamilyID names a TaskFamily that exists — ErrPersistenceNotFound
	// otherwise.
	CreateWorkspaceSet(ctx context.Context, set workspace.WorkspaceSet) (workspace.WorkspaceSet, error)
	// GetWorkspaceSetByFamilyID returns the WorkspaceSet for familyID (the
	// table's own UNIQUE(family_id): one TaskFamily has at most one
	// WorkspaceSet, ever), or ErrPersistenceNotFound.
	GetWorkspaceSetByFamilyID(ctx context.Context, familyID string) (workspace.WorkspaceSet, error)

	// AddRepositoryScope inserts a new RepositoryScope row after verifying
	// scope.FamilyID() names a TaskFamily that exists and
	// scope.RepositoryID() names a Repository that exists —
	// ErrPersistenceNotFound otherwise. It does not itself check
	// same-project or ACTIVE-repository (CreateRootWorkItem's own
	// "same-project/ACTIVE-repository validation" line): those need a
	// Project/Repository lookup this Work-concern repository has no reason
	// to duplicate CatalogRepository.GetRepository for — the calling
	// command handler resolves each repository via tx.Catalog() first and
	// only calls this method once that check already passed. The table's
	// own PRIMARY KEY (family_id, repository_id, scope_version, access)
	// rejects an exact duplicate grant outright.
	AddRepositoryScope(ctx context.Context, scope work.RepositoryScope) (work.RepositoryScope, error)
	// ListFamilyRepositoryScopes returns every RepositoryScope granted to
	// familyID, ordered by (scope_version, repository_id, access) for a
	// stable, deterministic result a test can assert on exactly.
	ListFamilyRepositoryScopes(ctx context.Context, familyID string) ([]work.RepositoryScope, error)

	// AddEffectiveScope inserts a new work_item_effective_scopes row for
	// workItemID after verifying workItemID names a WorkItem that exists and
	// scope.RepositoryID() names a Repository that exists —
	// ErrPersistenceNotFound otherwise (V3-05, docs/design/05-v3-project-workspace.md;
	// docs/design/01-system-design.md §6.1's own work_item_effective_scopes
	// row). It does not itself check that scope is actually a subset of
	// familyID's own approved grants — that is
	// work.ValidateEffectiveScopes's job (GC-INV-05), already run by the
	// calling command handler (CreateChildWorkItem) against the real
	// persisted family grants before this method is ever reached, the same
	// "construct/validate in the app layer, persist an already-valid domain
	// value" discipline AddRepositoryScope already follows for its own
	// family-level counterpart. Reuses work.RepositoryScope as the
	// persisted value's own type rather than a second, near-duplicate
	// "effective scope" type — see work.go's own "WorkItem effective scope"
	// doc comment for why.
	AddEffectiveScope(ctx context.Context, workItemID string, scope work.RepositoryScope) (work.RepositoryScope, error)
	// ListWorkItemEffectiveScopes returns every RepositoryScope declared as
	// workItemID's own effective scope, ordered by (repository_id, access)
	// for a stable, deterministic result a test can assert on exactly.
	ListWorkItemEffectiveScopes(ctx context.Context, workItemID string) ([]work.RepositoryScope, error)

	// TransitionWorkspaceSetState is populated now (V3-06,
	// docs/design/05-v3-project-workspace.md): an optimistic
	// compare-and-swap on a WorkspaceSet's own State, the persistence half
	// of internal/app/workspaceprovision.Handler's own state-aggregation
	// step — the exact same CAS discipline
	// CatalogRepository.TransitionRepositoryStatus already established for
	// Repository (req.ExpectedVersion and the row's current State must both
	// match, or the whole call fails with ports.ErrOptimisticConflict;
	// ports.ErrPersistenceNotFound if the WorkspaceSet does not exist at
	// all). It does not itself decide which transitions are legal — that is
	// workspaceprovision.Handler's own job, the same way
	// TransitionRepositoryStatus defers to project.CanTransitionRepositoryStatus
	// instead of duplicating that graph — it only performs the fenced
	// write, optionally persisting req.BaseRevisionSet in the same
	// statement when req.NextState is workspace.WorkspaceSetReady (never
	// partially: a set only ever gets a base RevisionSet once it is
	// genuinely READY, per this task's own "base RevisionSet after all
	// required ready").
	TransitionWorkspaceSetState(ctx context.Context, req TransitionWorkspaceSetStateRequest) (workspace.WorkspaceSet, error)

	// CreateRepositoryWorkspace inserts a new RepositoryWorkspace row
	// (V3-06) after verifying rw.WorkspaceSetID names a WorkspaceSet that
	// exists and rw.RepositoryID names a Repository that exists —
	// ErrPersistenceNotFound otherwise. rw is an already-constructed domain
	// value (mirroring CreateWorkItem/CreateTaskFamily/CreateWorkspaceSet's
	// own "accept an already-constructed value" convention above): the
	// READY-bound happy path is workspace.NewRepositoryWorkspace (locator/
	// baseRevision already resolved by the real
	// ports.WorkspaceProvider.Provision call the caller already ran,
	// entirely outside this — or any — transaction, per
	// workspaceprovision.Handler's own doc comment), while a FAILED outcome
	// (the provider call itself failed, so there is no real locator/
	// baseRevision to record) is a plain struct literal the caller builds
	// directly — workspace.RepositoryWorkspace carries no private fields
	// precisely so a caller can do this when a failure genuinely has no
	// happy-path value to satisfy NewRepositoryWorkspace's own non-empty
	// invariants. The table's own UNIQUE(workspace_set_id, repository_id,
	// generation) (0001_initial_schema.sql, GC-INV-03: "Mỗi WorkspaceSetID +
	// RepositoryID + Generation có tối đa một RepositoryWorkspace") rejects
	// a second row for the same triple outright, mapped to
	// ErrPersistenceAlreadyExists rather than a raw SQL conflict — this
	// task's own "map the resulting SQLite conflict to a sensible typed
	// error" line.
	CreateRepositoryWorkspace(ctx context.Context, rw workspace.RepositoryWorkspace) (workspace.RepositoryWorkspace, error)
	// GetRepositoryWorkspace returns the RepositoryWorkspace for
	// (workspaceSetID, repositoryID, generation), or
	// ErrPersistenceNotFound — the crash-recovery idempotency check
	// workspaceprovision.Handler's own Handle runs first, mirroring
	// repositoryprobe.Handler's identical "already resolved" reclaim check
	// for a different aggregate.
	GetRepositoryWorkspace(ctx context.Context, workspaceSetID, repositoryID string, generation uint64) (workspace.RepositoryWorkspace, error)
	// QuarantineRepositoryWorkspace is the tx-composable twin of
	// WorkspaceLifecycle.QuarantineRepositoryWorkspace below (V5-15D) — the
	// SAME fenced READY->QUARANTINED CAS plus its correlated domain event,
	// callable inside an already-open transaction. Needed by runtime's own
	// cancellation-finalization boundary, which must quarantine a mutated
	// workspace atomically alongside CASing the owning Attempt/NodeRun and
	// reconciling Run terminality — never as a separate, non-atomic commit
	// that could leave those steps inconsistent with each other after a
	// crash between them.
	QuarantineRepositoryWorkspace(ctx context.Context, update QuarantineRepositoryWorkspaceUpdate) error
	// ListWorkspaceSetRepositoryWorkspaces returns every RepositoryWorkspace
	// row for workspaceSetID (every generation, every state), ordered by
	// (repository_id, generation) for a stable, deterministic result a test
	// can assert on exactly — the state-aggregation step's own "is every
	// required repository now READY" read.
	ListWorkspaceSetRepositoryWorkspaces(ctx context.Context, workspaceSetID string) ([]workspace.RepositoryWorkspace, error)

	// GetRepositoryWorkspaceByID returns the RepositoryWorkspace row for id,
	// together with the FamilyID of the TaskFamily whose WorkspaceSet owns
	// it (V3-10, docs/design/05-v3-project-workspace.md): an operator- or
	// API-triggered reconciliation request names a RepositoryWorkspace by
	// its own bare ID alone — it has no reason to already know the
	// WorkspaceSetID/RepositoryID/Generation composite key
	// GetRepositoryWorkspace above requires — and
	// internal/app/workspacereconcile.RequestWorkspaceReconciliation needs
	// FamilyID specifically to build the fresh ports.ProvisionSpec a later
	// RECREATE decision issues. repository_workspaces.family_id is already
	// stored on every row (see CreateRepositoryWorkspace's own doc comment:
	// "carried for query-convenience/FK-safety") but workspace.RepositoryWorkspace
	// itself never exposes it, so this returns it alongside rather than
	// growing that domain type for one caller's own narrow need. Returns
	// ErrPersistenceNotFound if no row exists for id.
	GetRepositoryWorkspaceByID(ctx context.Context, id string) (RepositoryWorkspaceRecord, error)

	// TransitionTaskFamilyScopeVersion is populated now (V3-08,
	// docs/design/05-v3-project-workspace.md): the fenced CAS that increments
	// a TaskFamily's own ScopeVersion (and its generic Version) by exactly
	// one, atomically — internal/app/work.ApproveScopeExpansion's own single
	// mutation to the family row itself. It mirrors
	// TransitionWorkspaceSetState/TransitionRepositoryStatus's identical CAS
	// discipline (req.ExpectedScopeVersion and req.ExpectedVersion must both
	// match the row's current values, or the whole call fails with
	// ports.ErrOptimisticConflict; ports.ErrPersistenceNotFound if the
	// TaskFamily does not exist at all) — but, unlike those two, there is no
	// separate "NextState"/"NextStatus" to choose: ScopeVersion only ever
	// moves forward by one, so this method always computes
	// ExpectedScopeVersion+1 itself rather than taking a caller-supplied
	// target. ApproveScopeExpansion's own doc comment names the exact
	// ordering this establishes: the family's own ScopeVersion is bumped
	// FIRST, inside the same transaction, and the newly-approved
	// RepositoryScope grant(s) are then written at that already-bumped
	// value (AddedInScopeVersion = the returned TaskFamily.ScopeVersion) —
	// never the reverse — so at every point after this call returns, no
	// grant this transaction is about to write can ever be mistaken for
	// exceeding the family's own current ScopeVersion.
	TransitionTaskFamilyScopeVersion(ctx context.Context, req TransitionTaskFamilyScopeVersionRequest) (work.TaskFamily, error)

	// CreateScopeExpansionRequest inserts a new PENDING ScopeExpansionRequest
	// row (V3-08) after verifying req.FamilyID names a TaskFamily that
	// exists — ErrPersistenceNotFound otherwise. req is an already-
	// constructed, already-validated domain value (work.NewScopeExpansionRequest
	// — the same "construct/validate in the app layer via the domain
	// package's own pure constructor, persist an already-valid value"
	// discipline CreateWorkItem/CreateTaskFamily/CreateWorkspaceSet above
	// already follow). It never mutates req; the return value is req itself
	// for symmetry with those same methods.
	CreateScopeExpansionRequest(ctx context.Context, req work.ScopeExpansionRequest) (work.ScopeExpansionRequest, error)
	// GetScopeExpansionRequest returns the ScopeExpansionRequest with the
	// given ID, or ErrPersistenceNotFound.
	GetScopeExpansionRequest(ctx context.Context, id string) (work.ScopeExpansionRequest, error)
	// ListFamilyScopeExpansionRequests returns every ScopeExpansionRequest
	// ever created for familyID (every status, every decision), ordered by
	// (requested_at, id) for a stable, deterministic result a test can
	// assert on exactly.
	ListFamilyScopeExpansionRequests(ctx context.Context, familyID string) ([]work.ScopeExpansionRequest, error)
	// TransitionScopeExpansionRequestStatus is the fenced CAS transition
	// that moves a ScopeExpansionRequest from PENDING to one of its three
	// terminal decisions (work.CanTransitionScopeExpansionStatus's own
	// closed set) — the identical req.ExpectedStatus/req.ExpectedVersion
	// CAS discipline TransitionWorkspaceSetState/TransitionRepositoryStatus
	// already establish, applied here to ScopeExpansionStatus.
	// req.ApprovedScopeVersion is only ever non-nil when req.NextStatus is
	// work.ScopeExpansionApproved (ApproveScopeExpansion's own single write
	// to the decided request row, recording which family ScopeVersion its
	// grants were persisted at); nil for REJECTED/WITHDRAWN.
	TransitionScopeExpansionRequestStatus(ctx context.Context, req TransitionScopeExpansionRequestStatusRequest) (work.ScopeExpansionRequest, error)

	// CreateWorkItemBlocker is populated now (V4-12C,
	// docs/design/06-v4-runtime-engine.md): inserts a new, OPEN
	// work.WorkItemBlocker row after verifying blocker.WorkItemID names a
	// WorkItem that exists — ErrPersistenceNotFound otherwise. Idempotent by
	// ID: every real producer (transitionRunToCancelledTx's own RUN_CANCELLED
	// blocker, requestScopeExpansionTx's own SCOPE_EXPANSION_REQUIRED
	// blocker) mints a deterministic ID from its own originating aggregate
	// (RunID/AttemptID), so a duplicate delivery of the same underlying
	// transition returns the already-created row rather than a second one —
	// the identical "insert; on identical-key conflict, load and return the
	// existing row" discipline RecordWorkItemCancellationIntent already
	// establishes for its own aggregate.
	CreateWorkItemBlocker(ctx context.Context, blocker work.WorkItemBlocker) (work.WorkItemBlocker, error)
	// GetWorkItemBlocker returns the WorkItemBlocker with the given ID, or
	// ErrPersistenceNotFound.
	GetWorkItemBlocker(ctx context.Context, id string) (work.WorkItemBlocker, error)
	// ListWorkItemBlockersForWorkItem returns every WorkItemBlocker ever
	// opened for workItemID (every state), ordered by (OpenedAt, ID) for a
	// stable, deterministic result a test can assert on exactly —
	// ResolveWorkItemBlocker's own "how many OPEN blockers remain" count and
	// CancelWorkItem's own audit trail both read the full, unfiltered set.
	ListWorkItemBlockersForWorkItem(ctx context.Context, workItemID string) ([]work.WorkItemBlocker, error)
	// TransitionWorkItemBlockerState is the fenced CAS that closes a
	// blocker's own lifecycle — OPEN -> RESOLVED or OPEN -> WAIVED — the
	// identical req.ExpectedState/req.ExpectedVersion CAS discipline every
	// other transition method in this codebase already uses.
	// ErrOptimisticConflict on a stale caller (including a blocker that is
	// already RESOLVED/WAIVED — the idempotent-no-op case
	// ResolveWorkItemBlocker's own caller checks BEFORE calling this, the
	// same discipline every other idempotent-replay check in this codebase
	// already uses), ErrPersistenceNotFound for an unknown BlockerID.
	TransitionWorkItemBlockerState(ctx context.Context, req TransitionWorkItemBlockerStateRequest) (work.WorkItemBlocker, error)

	// CreateReleaseSet is populated now (V5-10A,
	// docs/design/07-v5-execution-evidence.md; AK-ARCH-015C): inserts a new,
	// CREATED work.ReleaseSet row after verifying releaseSet.FamilyID names
	// a TaskFamily that exists — ErrPersistenceNotFound otherwise.
	// Idempotent by ID, the identical "insert; on identical-key conflict,
	// load and return the existing row" discipline CreateWorkItemBlocker
	// already establishes.
	CreateReleaseSet(ctx context.Context, releaseSet work.ReleaseSet) (work.ReleaseSet, error)
	// GetReleaseSet returns the ReleaseSet with the given ID, or
	// ErrPersistenceNotFound.
	GetReleaseSet(ctx context.Context, id string) (work.ReleaseSet, error)
	// ListReleaseSetsForFamily returns every ReleaseSet ever created for
	// familyID (every state, across every completion attempt that family
	// has ever gone through), ordered by (CreatedAt, ID) for a stable,
	// deterministic result a test can assert on exactly.
	ListReleaseSetsForFamily(ctx context.Context, familyID string) ([]work.ReleaseSet, error)
	// TransitionReleaseSetState is the fenced CAS that closes a
	// ReleaseSet's own lifecycle — CREATED -> SEALED or CREATED -> ABANDONED
	// — the identical req.ExpectedState/req.ExpectedVersion CAS discipline
	// TransitionWorkItemBlockerState already uses. ErrOptimisticConflict on
	// a stale caller (including a ReleaseSet that is already SEALED/ABANDONED
	// — the idempotent-no-op "duplicate seal" case a caller checks BEFORE
	// calling this, the same discipline ResolveWorkItemBlocker's own caller
	// already uses), ErrPersistenceNotFound for an unknown ReleaseSetID.
	TransitionReleaseSetState(ctx context.Context, req TransitionReleaseSetStateRequest) (work.ReleaseSet, error)

	// HasActiveWriteLease is populated now (V3-11,
	// docs/design/05-v3-project-workspace.md): a read-only existence check
	// over write_leases for "no active lease" — one of
	// RequestWorkspaceSetRelease's own three eligibility checks (this
	// task's own Mục tiêu line). It reports whether any of
	// repositoryWorkspaceIDs currently holds a live (non-expired)
	// write_leases row. This is deliberately NOT
	// ports.WriteLeaseManager.AcquireWriteLeases/ValidateWriteLease: those
	// mutate or validate one already-held WriteLeaseGrant for a worker that
	// is actually claiming write access; this is a plain existence read for
	// an eligibility check, composed inside the exact same ports.Tx as the
	// rest of RequestWorkspaceSetRelease's own eligibility check and
	// intent-write/job-enqueue, so the whole thing commits — or fails to
	// even begin — atomically. Returns false, nil for an empty
	// repositoryWorkspaceIDs.
	HasActiveWriteLease(ctx context.Context, repositoryWorkspaceIDs []string) (bool, error)
}

// RepositoryWorkspaceRecord pairs a RepositoryWorkspace with the FamilyID
// of the TaskFamily whose WorkspaceSet owns it — see
// WorkRepository.GetRepositoryWorkspaceByID's own doc comment for why this
// is not just workspace.RepositoryWorkspace itself.
type RepositoryWorkspaceRecord struct {
	Workspace workspace.RepositoryWorkspace
	FamilyID  string
}

// TransitionWorkspaceSetStateRequest is an optimistic compare-and-swap
// request for WorkspaceSet.State (V3-06, mirroring
// TransitionRepositoryStatusRequest's identical CAS shape in
// ports/catalog.go). BaseRevisionSet is only meaningful — and should only
// ever be non-nil — when NextState is workspace.WorkspaceSetReady: the base
// RevisionSet is computed and persisted in the same CAS transaction that
// flips the set to READY, never before or after (this task's own "base
// RevisionSet after all required ready", computed only once, never
// partially).
type TransitionWorkspaceSetStateRequest struct {
	WorkspaceSetID  string
	ExpectedState   workspace.WorkspaceSetState
	ExpectedVersion uint64
	NextState       workspace.WorkspaceSetState
	BaseRevisionSet *workspace.RevisionSet
}

// TransitionTaskFamilyScopeVersionRequest is the fenced CAS request for
// WorkRepository.TransitionTaskFamilyScopeVersion (V3-08): see that
// interface method's own doc comment for the full contract, including why
// there is no separate "next version" field.
type TransitionTaskFamilyScopeVersionRequest struct {
	FamilyID             string
	ExpectedScopeVersion uint64
	ExpectedVersion      uint64
}

// TransitionWorkItemStatusRequest is an optimistic compare-and-swap request
// for WorkItem.Status (V4-02), mirroring TransitionWorkspaceSetStateRequest's
// identical CAS shape.
type TransitionWorkItemStatusRequest struct {
	WorkItemID      string
	ExpectedStatus  work.WorkItemStatus
	ExpectedVersion uint64
	NextStatus      work.WorkItemStatus
}

// TransitionScopeExpansionRequestStatusRequest is the fenced CAS request for
// WorkRepository.TransitionScopeExpansionRequestStatus (V3-08): see that
// interface method's own doc comment for the full contract.
// DecidedBy/DecidedAt are always set (every one of the three terminal
// transitions is a real, attributed decision); DecisionNote is only ever
// populated by RejectScopeExpansion in the real command layer today, but
// stays a plain optional string here rather than being restricted to one
// caller — nothing about this port method itself requires DecisionNote be
// empty for an approval or a withdrawal.
type TransitionScopeExpansionRequestStatusRequest struct {
	RequestID            string
	ExpectedStatus       work.ScopeExpansionStatus
	ExpectedVersion      uint64
	NextStatus           work.ScopeExpansionStatus
	DecidedBy            string
	DecidedAt            time.Time
	DecisionNote         string
	ApprovedScopeVersion *uint64
}

// TransitionWorkItemBlockerStateRequest is the CAS request for
// WorkRepository.TransitionWorkItemBlockerState (V4-12C). ResolvedAt/
// ResolvedBy are always set (every terminal transition is a real, attributed
// decision — either ResolveWorkItemBlocker's own caller-supplied Actor, or
// the internal scope-expansion reconcile flow's own system actor);
// DecisionArtifactID is only ever non-empty when NextState is
// work.BlockerWaived.
type TransitionWorkItemBlockerStateRequest struct {
	BlockerID          string
	ExpectedState      work.BlockerState
	ExpectedVersion    uint64
	NextState          work.BlockerState
	ResolvedAt         time.Time
	ResolvedBy         string
	ResolutionNote     string
	DecisionArtifactID string
}

// TransitionReleaseSetStateRequest is what a caller supplies to
// TransitionReleaseSetState (V5-10A). OccurredAt is recorded as SealedAt
// when NextState is SEALED, or AbandonedAt when NextState is ABANDONED —
// never both, since CREATED only ever transitions to exactly one of them.
type TransitionReleaseSetStateRequest struct {
	ReleaseSetID    string
	ExpectedState   work.ReleaseSetState
	ExpectedVersion uint64
	NextState       work.ReleaseSetState
	OccurredAt      time.Time
}
