package ports

import (
	"context"

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
}
