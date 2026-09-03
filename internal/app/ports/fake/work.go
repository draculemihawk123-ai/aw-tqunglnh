package fake

import (
	"context"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// WorkRepository is an in-memory ports.WorkRepository — V3-04 gives this
// concern its first real behavior, so internal/app/work.CreateRootWorkItem
// can be tested without sqlite (the same V1-05 discipline
// fake.CatalogRepository already follows for its own concern). catalog is a
// pointer to the same Tx's CatalogRepository, mirroring how the real sqlite
// adapter's workRepository and catalogRepository both share one *sql.Tx —
// CreateTaskFamily/AddRepositoryScope need to check a Project/Repository
// actually exists, and that existence lives in CatalogRepository's own
// maps, not duplicated here.
type WorkRepository struct {
	catalog *CatalogRepository

	workItems        map[string]work.WorkItem
	taskFamilies     map[string]work.TaskFamily
	workspaceSets    map[string]workspace.WorkspaceSet // by FamilyID (mirrors UNIQUE(family_id))
	repositoryScopes map[string][]work.RepositoryScope // by FamilyID, insertion order
	effectiveScopes  map[string][]work.RepositoryScope // by WorkItemID, insertion order (V3-05)
}

var _ ports.WorkRepository = (*WorkRepository)(nil)

// cloneWith deep-copies w's own state, attaching it to catalog (the same
// Tx.clone() call's already-cloned CatalogRepository) rather than w's own
// stale catalog pointer — so a failed or read-only attempt's WorkRepository
// state never leaks into the next attempt any more than its
// CatalogRepository counterpart does.
func (w *WorkRepository) cloneWith(catalog *CatalogRepository) *WorkRepository {
	workItems := make(map[string]work.WorkItem, len(w.workItems))
	for k, v := range w.workItems {
		workItems[k] = v
	}
	taskFamilies := make(map[string]work.TaskFamily, len(w.taskFamilies))
	for k, v := range w.taskFamilies {
		taskFamilies[k] = v
	}
	workspaceSets := make(map[string]workspace.WorkspaceSet, len(w.workspaceSets))
	for k, v := range w.workspaceSets {
		workspaceSets[k] = v
	}
	repositoryScopes := make(map[string][]work.RepositoryScope, len(w.repositoryScopes))
	for k, v := range w.repositoryScopes {
		repositoryScopes[k] = append([]work.RepositoryScope(nil), v...)
	}
	effectiveScopes := make(map[string][]work.RepositoryScope, len(w.effectiveScopes))
	for k, v := range w.effectiveScopes {
		effectiveScopes[k] = append([]work.RepositoryScope(nil), v...)
	}
	return &WorkRepository{
		catalog: catalog, workItems: workItems, taskFamilies: taskFamilies,
		workspaceSets: workspaceSets, repositoryScopes: repositoryScopes, effectiveScopes: effectiveScopes,
	}
}

// CreateWorkItem mirrors sqlite's createWorkItemTx: item.FamilyID must name
// a TaskFamily that already exists — ErrPersistenceNotFound otherwise. It
// does not itself re-check item.ProjectID (CreateTaskFamily already
// verified the family's own project when the family was created, and a
// WorkItem's FamilyID -> TaskFamily -> ProjectID chain is exactly what
// GC-INV-01 already ties together).
func (w *WorkRepository) CreateWorkItem(_ context.Context, item work.WorkItem) (work.WorkItem, error) {
	if _, ok := w.taskFamilies[string(item.FamilyID)]; !ok {
		return work.WorkItem{}, fmt.Errorf("fake: %w: task family %s", ports.ErrPersistenceNotFound, item.FamilyID)
	}
	if w.workItems == nil {
		w.workItems = map[string]work.WorkItem{}
	}
	w.workItems[string(item.ID)] = item
	return item, nil
}

func (w *WorkRepository) GetWorkItem(_ context.Context, id string) (work.WorkItem, error) {
	item, ok := w.workItems[id]
	if !ok {
		return work.WorkItem{}, fmt.Errorf("fake: %w: work item %s", ports.ErrPersistenceNotFound, id)
	}
	return item, nil
}

// CreateTaskFamily mirrors sqlite's createTaskFamilyTx: family.ProjectID
// must name a Project that already exists (resolved from the shared
// CatalogRepository) — ErrPersistenceNotFound otherwise.
func (w *WorkRepository) CreateTaskFamily(_ context.Context, family work.TaskFamily) (work.TaskFamily, error) {
	if _, ok := w.catalog.projects[string(family.ProjectID)]; !ok {
		return work.TaskFamily{}, fmt.Errorf("fake: %w: project %s", ports.ErrPersistenceNotFound, family.ProjectID)
	}
	if w.taskFamilies == nil {
		w.taskFamilies = map[string]work.TaskFamily{}
	}
	w.taskFamilies[string(family.ID)] = family
	return family, nil
}

func (w *WorkRepository) GetTaskFamily(_ context.Context, id string) (work.TaskFamily, error) {
	family, ok := w.taskFamilies[id]
	if !ok {
		return work.TaskFamily{}, fmt.Errorf("fake: %w: task family %s", ports.ErrPersistenceNotFound, id)
	}
	return family, nil
}

// CreateWorkspaceSet mirrors sqlite's createWorkspaceSetTx: set.FamilyID
// must name a TaskFamily that already exists — ErrPersistenceNotFound
// otherwise.
func (w *WorkRepository) CreateWorkspaceSet(_ context.Context, set workspace.WorkspaceSet) (workspace.WorkspaceSet, error) {
	if _, ok := w.taskFamilies[string(set.FamilyID)]; !ok {
		return workspace.WorkspaceSet{}, fmt.Errorf("fake: %w: task family %s", ports.ErrPersistenceNotFound, set.FamilyID)
	}
	if w.workspaceSets == nil {
		w.workspaceSets = map[string]workspace.WorkspaceSet{}
	}
	w.workspaceSets[string(set.FamilyID)] = set
	return set, nil
}

func (w *WorkRepository) GetWorkspaceSetByFamilyID(_ context.Context, familyID string) (workspace.WorkspaceSet, error) {
	set, ok := w.workspaceSets[familyID]
	if !ok {
		return workspace.WorkspaceSet{}, fmt.Errorf("fake: %w: workspace set for family %s", ports.ErrPersistenceNotFound, familyID)
	}
	return set, nil
}

// AddRepositoryScope mirrors sqlite's addRepositoryScopeTx: scope.FamilyID()
// must name a TaskFamily that already exists and scope.RepositoryID() must
// name a Repository that already exists (resolved from the shared
// CatalogRepository) — ErrPersistenceNotFound otherwise. Same-project and
// ACTIVE-repository validation is deliberately not this method's job (see
// ports.WorkRepository.AddRepositoryScope's own doc comment): the calling
// command handler already resolved and checked the Repository via
// tx.Catalog() before ever reaching here.
func (w *WorkRepository) AddRepositoryScope(_ context.Context, scope work.RepositoryScope) (work.RepositoryScope, error) {
	if _, ok := w.taskFamilies[string(scope.FamilyID())]; !ok {
		return work.RepositoryScope{}, fmt.Errorf("fake: %w: task family %s", ports.ErrPersistenceNotFound, scope.FamilyID())
	}
	if _, ok := w.catalog.repositories[string(scope.RepositoryID())]; !ok {
		return work.RepositoryScope{}, fmt.Errorf("fake: %w: repository %s", ports.ErrPersistenceNotFound, scope.RepositoryID())
	}
	if w.repositoryScopes == nil {
		w.repositoryScopes = map[string][]work.RepositoryScope{}
	}
	w.repositoryScopes[string(scope.FamilyID())] = append(w.repositoryScopes[string(scope.FamilyID())], scope)
	return scope, nil
}

// ListFamilyRepositoryScopes mirrors sqlite's listFamilyRepositoryScopesTx,
// ordered by (AddedInScopeVersion, RepositoryID, Access) for a stable,
// deterministic result a test can assert on exactly, the same as the real
// adapter's own ORDER BY.
func (w *WorkRepository) ListFamilyRepositoryScopes(_ context.Context, familyID string) ([]work.RepositoryScope, error) {
	scopes := append([]work.RepositoryScope(nil), w.repositoryScopes[familyID]...)
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].AddedInScopeVersion() != scopes[j].AddedInScopeVersion() {
			return scopes[i].AddedInScopeVersion() < scopes[j].AddedInScopeVersion()
		}
		if scopes[i].RepositoryID() != scopes[j].RepositoryID() {
			return scopes[i].RepositoryID() < scopes[j].RepositoryID()
		}
		return scopes[i].Access() < scopes[j].Access()
	})
	return scopes, nil
}

// AddEffectiveScope mirrors sqlite's addEffectiveScopeTx (V3-05):
// workItemID must name a WorkItem that already exists and
// scope.RepositoryID() must name a Repository that already exists
// (resolved from the shared CatalogRepository) — ErrPersistenceNotFound
// otherwise. Same-family-subset validation is deliberately not this
// method's job (see ports.WorkRepository.AddEffectiveScope's own doc
// comment): the calling command handler already ran
// work.ValidateEffectiveScopes before ever reaching here.
func (w *WorkRepository) AddEffectiveScope(_ context.Context, workItemID string, scope work.RepositoryScope) (work.RepositoryScope, error) {
	if _, ok := w.workItems[workItemID]; !ok {
		return work.RepositoryScope{}, fmt.Errorf("fake: %w: work item %s", ports.ErrPersistenceNotFound, workItemID)
	}
	if _, ok := w.catalog.repositories[string(scope.RepositoryID())]; !ok {
		return work.RepositoryScope{}, fmt.Errorf("fake: %w: repository %s", ports.ErrPersistenceNotFound, scope.RepositoryID())
	}
	if w.effectiveScopes == nil {
		w.effectiveScopes = map[string][]work.RepositoryScope{}
	}
	w.effectiveScopes[workItemID] = append(w.effectiveScopes[workItemID], scope)
	return scope, nil
}

// ListWorkItemEffectiveScopes mirrors sqlite's listWorkItemEffectiveScopesTx,
// ordered by (RepositoryID, Access) for a stable, deterministic result a
// test can assert on exactly, the same as the real adapter's own ORDER BY.
func (w *WorkRepository) ListWorkItemEffectiveScopes(_ context.Context, workItemID string) ([]work.RepositoryScope, error) {
	scopes := append([]work.RepositoryScope(nil), w.effectiveScopes[workItemID]...)
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].RepositoryID() != scopes[j].RepositoryID() {
			return scopes[i].RepositoryID() < scopes[j].RepositoryID()
		}
		return scopes[i].Access() < scopes[j].Access()
	})
	return scopes, nil
}
