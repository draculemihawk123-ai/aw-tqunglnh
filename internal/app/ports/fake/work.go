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
	// repositoryWorkspaces is keyed by "workspaceSetID/repositoryID/generation"
	// (V3-06), mirroring the real adapter's own
	// UNIQUE(workspace_set_id, repository_id, generation) (GC-INV-03): a
	// second CreateRepositoryWorkspace call for the same key overwrites
	// nothing, it is rejected exactly like the real adapter's own
	// constraint conflict — see CreateRepositoryWorkspace below.
	repositoryWorkspaces map[string]workspace.RepositoryWorkspace
	// scopeExpansionRequests is keyed by ScopeExpansionRequest.ID (V3-08).
	scopeExpansionRequests map[string]work.ScopeExpansionRequest
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
	repositoryWorkspaces := make(map[string]workspace.RepositoryWorkspace, len(w.repositoryWorkspaces))
	for k, v := range w.repositoryWorkspaces {
		repositoryWorkspaces[k] = v
	}
	scopeExpansionRequests := make(map[string]work.ScopeExpansionRequest, len(w.scopeExpansionRequests))
	for k, v := range w.scopeExpansionRequests {
		scopeExpansionRequests[k] = v
	}
	return &WorkRepository{
		catalog: catalog, workItems: workItems, taskFamilies: taskFamilies,
		workspaceSets: workspaceSets, repositoryScopes: repositoryScopes, effectiveScopes: effectiveScopes,
		repositoryWorkspaces: repositoryWorkspaces, scopeExpansionRequests: scopeExpansionRequests,
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

// --- WorkspaceSet state / RepositoryWorkspace (V3-06) ---

// findWorkspaceSetByID linear-scans workspaceSets (keyed by FamilyID, not
// ID — mirroring the real table's own UNIQUE(family_id) rather than a
// second by-ID index) for the set whose own ID matches. Fine for the fake's
// small in-memory fixture sizes; the real sqlite adapter indexes by primary
// key directly.
func (w *WorkRepository) findWorkspaceSetByID(id string) (familyID string, set workspace.WorkspaceSet, ok bool) {
	for fid, s := range w.workspaceSets {
		if string(s.ID) == id {
			return fid, s, true
		}
	}
	return "", workspace.WorkspaceSet{}, false
}

// TransitionWorkspaceSetState mirrors sqlite's transitionWorkspaceSetStateTx:
// the identical CAS TransitionRepositoryStatus's own fake counterpart
// already performs for Repository, applied here to WorkspaceSet.State.
// req.BaseRevisionSet, when non-nil, is stored directly onto the returned/
// persisted WorkspaceSet's own BaseRevisionSet field (V3-06 added this
// field to the shared workspace.WorkspaceSet domain type precisely so both
// this fake and the real sqlite adapter round-trip the identical value
// through the identical struct, rather than a side channel only one of the
// two exposes).
func (w *WorkRepository) TransitionWorkspaceSetState(_ context.Context, req ports.TransitionWorkspaceSetStateRequest) (workspace.WorkspaceSet, error) {
	familyID, set, ok := w.findWorkspaceSetByID(req.WorkspaceSetID)
	if !ok {
		return workspace.WorkspaceSet{}, fmt.Errorf("fake: %w: workspace set %s", ports.ErrPersistenceNotFound, req.WorkspaceSetID)
	}
	if set.State != req.ExpectedState || set.Version != req.ExpectedVersion {
		return workspace.WorkspaceSet{}, fmt.Errorf("fake: %w: workspace set %s expected %s@%d",
			ports.ErrOptimisticConflict, req.WorkspaceSetID, req.ExpectedState, req.ExpectedVersion)
	}
	set.State = req.NextState
	set.Version++
	if req.BaseRevisionSet != nil {
		set.BaseRevisionSet = req.BaseRevisionSet
	}
	w.workspaceSets[familyID] = set
	return set, nil
}

// CreateRepositoryWorkspace mirrors sqlite's createRepositoryWorkspaceTx:
// rw.WorkspaceSetID must name a WorkspaceSet that already exists and
// rw.RepositoryID must name a Repository that already exists (resolved
// from the shared CatalogRepository) — ErrPersistenceNotFound otherwise.
// The composite (WorkspaceSetID, RepositoryID, Generation) key mirrors the
// real table's own UNIQUE constraint (GC-INV-03): a second call for the
// same key is rejected with ErrPersistenceAlreadyExists, never silently
// overwritten.
func (w *WorkRepository) CreateRepositoryWorkspace(_ context.Context, rw workspace.RepositoryWorkspace) (workspace.RepositoryWorkspace, error) {
	if _, _, ok := w.findWorkspaceSetByID(string(rw.WorkspaceSetID)); !ok {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("fake: %w: workspace set %s", ports.ErrPersistenceNotFound, rw.WorkspaceSetID)
	}
	if _, ok := w.catalog.repositories[string(rw.RepositoryID)]; !ok {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("fake: %w: repository %s", ports.ErrPersistenceNotFound, rw.RepositoryID)
	}
	key := repositoryWorkspaceKey(string(rw.WorkspaceSetID), string(rw.RepositoryID), rw.Generation)
	if _, exists := w.repositoryWorkspaces[key]; exists {
		return workspace.RepositoryWorkspace{}, fmt.Errorf(
			"fake: %w: repository workspace for workspace set %s repository %s generation %d",
			ports.ErrPersistenceAlreadyExists, rw.WorkspaceSetID, rw.RepositoryID, rw.Generation)
	}
	if w.repositoryWorkspaces == nil {
		w.repositoryWorkspaces = map[string]workspace.RepositoryWorkspace{}
	}
	w.repositoryWorkspaces[key] = rw
	return rw, nil
}

// GetRepositoryWorkspace mirrors sqlite's getRepositoryWorkspaceTx.
func (w *WorkRepository) GetRepositoryWorkspace(_ context.Context, workspaceSetID, repositoryID string, generation uint64) (workspace.RepositoryWorkspace, error) {
	rw, ok := w.repositoryWorkspaces[repositoryWorkspaceKey(workspaceSetID, repositoryID, generation)]
	if !ok {
		return workspace.RepositoryWorkspace{}, fmt.Errorf(
			"fake: %w: repository workspace for workspace set %s repository %s generation %d",
			ports.ErrPersistenceNotFound, workspaceSetID, repositoryID, generation)
	}
	return rw, nil
}

// ListWorkspaceSetRepositoryWorkspaces mirrors sqlite's
// listWorkspaceSetRepositoryWorkspacesTx, ordered by (RepositoryID,
// Generation) for a stable, deterministic result a test can assert on
// exactly, the same as the real adapter's own ORDER BY.
func (w *WorkRepository) ListWorkspaceSetRepositoryWorkspaces(_ context.Context, workspaceSetID string) ([]workspace.RepositoryWorkspace, error) {
	var result []workspace.RepositoryWorkspace
	for _, rw := range w.repositoryWorkspaces {
		if string(rw.WorkspaceSetID) == workspaceSetID {
			result = append(result, rw)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RepositoryID != result[j].RepositoryID {
			return result[i].RepositoryID < result[j].RepositoryID
		}
		return result[i].Generation < result[j].Generation
	})
	return result, nil
}

func repositoryWorkspaceKey(workspaceSetID, repositoryID string, generation uint64) string {
	return fmt.Sprintf("%s/%s/%d", workspaceSetID, repositoryID, generation)
}

// HasActiveWriteLease always reports false, nil (V3-11): this fake never
// models write_leases at all — ports.WriteLeaseManager itself has no fake
// counterpart anywhere in this codebase (it is only ever exercised against
// real sqlite, see internal/adapters/sqlite/scheduling_test.go's own V3-09
// coverage), so there is no in-memory lease state here to consult. A
// RequestWorkspaceSetRelease unit test that needs to exercise the "active
// lease blocks release" path does so against real sqlite instead (see
// internal/app/workspacerelease's own commands_sqlite_test.go), the same
// "checkable for real today, not faked" treatment this method's own
// ports.WorkRepository doc comment already documents.
func (w *WorkRepository) HasActiveWriteLease(_ context.Context, _ []string) (bool, error) {
	return false, nil
}

// GetRepositoryWorkspaceByID mirrors sqlite's getRepositoryWorkspaceByIDTx
// (V3-10): a linear scan of repositoryWorkspaces for the row whose own ID
// matches, same "fine for the fake's small in-memory fixture sizes" trade-off
// findWorkspaceSetByID above already makes, then resolves FamilyID via that
// same helper.
func (w *WorkRepository) GetRepositoryWorkspaceByID(_ context.Context, id string) (ports.RepositoryWorkspaceRecord, error) {
	for _, rw := range w.repositoryWorkspaces {
		if string(rw.ID) != id {
			continue
		}
		familyID, _, ok := w.findWorkspaceSetByID(string(rw.WorkspaceSetID))
		if !ok {
			return ports.RepositoryWorkspaceRecord{}, fmt.Errorf("fake: %w: workspace set %s", ports.ErrPersistenceNotFound, rw.WorkspaceSetID)
		}
		return ports.RepositoryWorkspaceRecord{Workspace: rw, FamilyID: familyID}, nil
	}
	return ports.RepositoryWorkspaceRecord{}, fmt.Errorf("fake: %w: repository workspace %s", ports.ErrPersistenceNotFound, id)
}

// --- TaskFamily ScopeVersion / ScopeExpansionRequest (V3-08) ---

// TransitionTaskFamilyScopeVersion mirrors sqlite's
// transitionTaskFamilyScopeVersionTx: the identical CAS
// TransitionWorkspaceSetState's own fake counterpart already performs for
// WorkspaceSet.State, applied here to TaskFamily.ScopeVersion — always
// exactly +1, never a caller-supplied target (see
// ports.WorkRepository.TransitionTaskFamilyScopeVersion's own doc comment).
func (w *WorkRepository) TransitionTaskFamilyScopeVersion(_ context.Context, req ports.TransitionTaskFamilyScopeVersionRequest) (work.TaskFamily, error) {
	family, ok := w.taskFamilies[req.FamilyID]
	if !ok {
		return work.TaskFamily{}, fmt.Errorf("fake: %w: task family %s", ports.ErrPersistenceNotFound, req.FamilyID)
	}
	if family.ScopeVersion != req.ExpectedScopeVersion || family.Version != req.ExpectedVersion {
		return work.TaskFamily{}, fmt.Errorf("fake: %w: task family %s expected scope version %d@%d",
			ports.ErrOptimisticConflict, req.FamilyID, req.ExpectedScopeVersion, req.ExpectedVersion)
	}
	family.ScopeVersion++
	family.Version++
	w.taskFamilies[req.FamilyID] = family
	return family, nil
}

// CreateScopeExpansionRequest mirrors sqlite's createScopeExpansionRequestTx:
// req.FamilyID must name a TaskFamily that already exists —
// ErrPersistenceNotFound otherwise.
func (w *WorkRepository) CreateScopeExpansionRequest(_ context.Context, req work.ScopeExpansionRequest) (work.ScopeExpansionRequest, error) {
	if _, ok := w.taskFamilies[string(req.FamilyID)]; !ok {
		return work.ScopeExpansionRequest{}, fmt.Errorf("fake: %w: task family %s", ports.ErrPersistenceNotFound, req.FamilyID)
	}
	if w.scopeExpansionRequests == nil {
		w.scopeExpansionRequests = map[string]work.ScopeExpansionRequest{}
	}
	w.scopeExpansionRequests[string(req.ID)] = req
	return req, nil
}

// GetScopeExpansionRequest mirrors sqlite's getScopeExpansionRequestTx.
func (w *WorkRepository) GetScopeExpansionRequest(_ context.Context, id string) (work.ScopeExpansionRequest, error) {
	req, ok := w.scopeExpansionRequests[id]
	if !ok {
		return work.ScopeExpansionRequest{}, fmt.Errorf("fake: %w: scope expansion request %s", ports.ErrPersistenceNotFound, id)
	}
	return req, nil
}

// ListFamilyScopeExpansionRequests mirrors sqlite's
// listFamilyScopeExpansionRequestsTx, ordered by (RequestedAt, ID) for a
// stable, deterministic result a test can assert on exactly.
func (w *WorkRepository) ListFamilyScopeExpansionRequests(_ context.Context, familyID string) ([]work.ScopeExpansionRequest, error) {
	var result []work.ScopeExpansionRequest
	for _, req := range w.scopeExpansionRequests {
		if string(req.FamilyID) == familyID {
			result = append(result, req)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].RequestedAt.Equal(result[j].RequestedAt) {
			return result[i].RequestedAt.Before(result[j].RequestedAt)
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

// TransitionScopeExpansionRequestStatus mirrors sqlite's
// transitionScopeExpansionRequestStatusTx: the identical CAS discipline
// TransitionTaskFamilyScopeVersion/TransitionWorkspaceSetState above already
// perform, applied here to ScopeExpansionRequest.Status.
func (w *WorkRepository) TransitionScopeExpansionRequestStatus(_ context.Context, req ports.TransitionScopeExpansionRequestStatusRequest) (work.ScopeExpansionRequest, error) {
	existing, ok := w.scopeExpansionRequests[req.RequestID]
	if !ok {
		return work.ScopeExpansionRequest{}, fmt.Errorf("fake: %w: scope expansion request %s", ports.ErrPersistenceNotFound, req.RequestID)
	}
	if existing.Status != req.ExpectedStatus || existing.Version != req.ExpectedVersion {
		return work.ScopeExpansionRequest{}, fmt.Errorf("fake: %w: scope expansion request %s expected %s@%d",
			ports.ErrOptimisticConflict, req.RequestID, req.ExpectedStatus, req.ExpectedVersion)
	}
	existing.Status = req.NextStatus
	existing.DecidedBy = req.DecidedBy
	decidedAt := req.DecidedAt
	existing.DecidedAt = &decidedAt
	existing.DecisionNote = req.DecisionNote
	existing.ApprovedScopeVersion = req.ApprovedScopeVersion
	existing.Version++
	w.scopeExpansionRequests[req.RequestID] = existing
	return existing, nil
}
