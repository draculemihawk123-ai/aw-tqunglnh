package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file gives workRepository (declared as a placeholder struct in
// unitofwork.go) its first real methods (V3-04,
// docs/design/05-v3-project-workspace.md): the persistence half of
// internal/app/work.CreateRootWorkItem, the single public command that
// atomically creates a root WorkItem, its owning TaskFamily, a WorkspaceSet
// intent and the initial RepositoryScope grants (AK-ARCH-011, GC-INV-01).
// It follows catalog.go's own Tx-composable pattern exactly: one
// xxxTx(ctx, tx *sql.Tx, ...) function per operation, plus a thin
// workRepository method that forwards to it using the shared *sql.Tx the
// struct already carries.

// --- WorkItem ---

// CreateWorkItem implements ports.WorkRepository.
func (r workRepository) CreateWorkItem(ctx context.Context, item work.WorkItem) (work.WorkItem, error) {
	return createWorkItemTx(ctx, r.tx, item)
}

func createWorkItemTx(ctx context.Context, tx *sql.Tx, item work.WorkItem) (work.WorkItem, error) {
	if item.ID == "" || item.ProjectID == "" || item.FamilyID == "" {
		return work.WorkItem{}, errors.New("work item id, project id and family id are required")
	}

	var projectExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, string(item.ProjectID)).Scan(&projectExists)
	if errors.Is(err, sql.ErrNoRows) {
		return work.WorkItem{}, fmt.Errorf("%w: project %s", ports.ErrPersistenceNotFound, item.ProjectID)
	}
	if err != nil {
		return work.WorkItem{}, MapSQLiteError(fmt.Errorf("resolve work item project: %w", err))
	}

	var familyExists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM task_families WHERE id = ?`, string(item.FamilyID)).Scan(&familyExists)
	if errors.Is(err, sql.ErrNoRows) {
		return work.WorkItem{}, fmt.Errorf("%w: task family %s", ports.ErrPersistenceNotFound, item.FamilyID)
	}
	if err != nil {
		return work.WorkItem{}, MapSQLiteError(fmt.Errorf("resolve work item family: %w", err))
	}

	var parentID any
	if item.ParentID != nil {
		parentID = string(*item.ParentID)
	}
	var parentJoinPolicy any
	if item.ParentJoinPolicy != "" {
		parentJoinPolicy = string(item.ParentJoinPolicy)
	}
	var sourceNodeRunID any
	if item.SourceNodeRunID != nil {
		sourceNodeRunID = string(*item.SourceNodeRunID)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO work_items (id, project_id, kind, parent_id, family_id, title, status, version, parent_join_policy, source_node_run_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(item.ID), string(item.ProjectID), string(item.Kind), parentID, string(item.FamilyID),
		item.Title, string(item.Status), item.Version, parentJoinPolicy, sourceNodeRunID, now, now,
	); err != nil {
		return work.WorkItem{}, MapSQLiteError(fmt.Errorf("create work item: %w", err))
	}
	return item, nil
}

// GetWorkItem implements ports.WorkRepository.
func (r workRepository) GetWorkItem(ctx context.Context, id string) (work.WorkItem, error) {
	return getWorkItemTx(ctx, r.tx, id)
}

func getWorkItemTx(ctx context.Context, tx *sql.Tx, id string) (work.WorkItem, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, kind, parent_id, family_id, title, status, version, parent_join_policy, source_node_run_id
FROM work_items WHERE id = ?`, id)
	item, err := scanWorkItemRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return work.WorkItem{}, fmt.Errorf("%w: work item %s", ports.ErrPersistenceNotFound, id)
	}
	return item, err
}

func scanWorkItemRow(row repositoryRowScanner) (work.WorkItem, error) {
	var id, projectID, kind, familyID, title, status string
	var parentID, parentJoinPolicy, sourceNodeRunID sql.NullString
	var version uint64
	if err := row.Scan(&id, &projectID, &kind, &parentID, &familyID, &title, &status, &version, &parentJoinPolicy, &sourceNodeRunID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return work.WorkItem{}, err
		}
		return work.WorkItem{}, MapSQLiteError(fmt.Errorf("scan work item row: %w", err))
	}
	item := work.WorkItem{
		ID: work.WorkItemID(id), ProjectID: project.ProjectID(projectID), Kind: work.WorkItemKind(kind),
		FamilyID: work.TaskFamilyID(familyID), Title: title, Status: work.WorkItemStatus(status), Version: version,
	}
	if parentID.Valid {
		pid := work.WorkItemID(parentID.String)
		item.ParentID = &pid
	}
	if parentJoinPolicy.Valid {
		item.ParentJoinPolicy = work.JoinPolicy(parentJoinPolicy.String)
	}
	if sourceNodeRunID.Valid {
		nodeRunID := work.SourceNodeRunID(sourceNodeRunID.String)
		item.SourceNodeRunID = &nodeRunID
	}
	return item, nil
}

// --- TaskFamily ---

// CreateTaskFamily implements ports.WorkRepository.
func (r workRepository) CreateTaskFamily(ctx context.Context, family work.TaskFamily) (work.TaskFamily, error) {
	return createTaskFamilyTx(ctx, r.tx, family)
}

func createTaskFamilyTx(ctx context.Context, tx *sql.Tx, family work.TaskFamily) (work.TaskFamily, error) {
	if family.ID == "" || family.ProjectID == "" || family.RootWorkItemID == "" {
		return work.TaskFamily{}, errors.New("task family id, project id and root work item id are required")
	}

	var projectExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, string(family.ProjectID)).Scan(&projectExists)
	if errors.Is(err, sql.ErrNoRows) {
		return work.TaskFamily{}, fmt.Errorf("%w: project %s", ports.ErrPersistenceNotFound, family.ProjectID)
	}
	if err != nil {
		return work.TaskFamily{}, MapSQLiteError(fmt.Errorf("resolve task family project: %w", err))
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_families (id, project_id, root_work_item_id, scope_version, status, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(family.ID), string(family.ProjectID), string(family.RootWorkItemID), family.ScopeVersion,
		string(family.Status), family.Version, now, now,
	); err != nil {
		return work.TaskFamily{}, MapSQLiteError(fmt.Errorf("create task family: %w", err))
	}
	return family, nil
}

// GetTaskFamily implements ports.WorkRepository.
func (r workRepository) GetTaskFamily(ctx context.Context, id string) (work.TaskFamily, error) {
	return getTaskFamilyTx(ctx, r.tx, id)
}

func getTaskFamilyTx(ctx context.Context, tx *sql.Tx, id string) (work.TaskFamily, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, root_work_item_id, scope_version, status, version FROM task_families WHERE id = ?`, id)
	family, err := scanTaskFamilyRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return work.TaskFamily{}, fmt.Errorf("%w: task family %s", ports.ErrPersistenceNotFound, id)
	}
	return family, err
}

func scanTaskFamilyRow(row repositoryRowScanner) (work.TaskFamily, error) {
	var id, projectID, rootWorkItemID, status string
	var scopeVersion, version uint64
	if err := row.Scan(&id, &projectID, &rootWorkItemID, &scopeVersion, &status, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return work.TaskFamily{}, err
		}
		return work.TaskFamily{}, MapSQLiteError(fmt.Errorf("scan task family row: %w", err))
	}
	return work.TaskFamily{
		ID: work.TaskFamilyID(id), ProjectID: project.ProjectID(projectID), RootWorkItemID: work.WorkItemID(rootWorkItemID),
		ScopeVersion: scopeVersion, Status: work.TaskFamilyStatus(status), Version: version,
	}, nil
}

// --- WorkspaceSet ---

// CreateWorkspaceSet implements ports.WorkRepository.
func (r workRepository) CreateWorkspaceSet(ctx context.Context, set workspace.WorkspaceSet) (workspace.WorkspaceSet, error) {
	return createWorkspaceSetTx(ctx, r.tx, set)
}

func createWorkspaceSetTx(ctx context.Context, tx *sql.Tx, set workspace.WorkspaceSet) (workspace.WorkspaceSet, error) {
	if set.ID == "" || set.ProjectID == "" || set.FamilyID == "" {
		return workspace.WorkspaceSet{}, errors.New("workspace set id, project id and family id are required")
	}

	var familyExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM task_families WHERE id = ?`, string(set.FamilyID)).Scan(&familyExists)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.WorkspaceSet{}, fmt.Errorf("%w: task family %s", ports.ErrPersistenceNotFound, set.FamilyID)
	}
	if err != nil {
		return workspace.WorkspaceSet{}, MapSQLiteError(fmt.Errorf("resolve workspace set family: %w", err))
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO workspace_sets (id, project_id, family_id, state, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(set.ID), string(set.ProjectID), string(set.FamilyID), string(set.State), set.Version, now, now,
	); err != nil {
		return workspace.WorkspaceSet{}, MapSQLiteError(fmt.Errorf("create workspace set: %w", err))
	}
	return set, nil
}

// GetWorkspaceSetByFamilyID implements ports.WorkRepository.
func (r workRepository) GetWorkspaceSetByFamilyID(ctx context.Context, familyID string) (workspace.WorkspaceSet, error) {
	return getWorkspaceSetByFamilyIDTx(ctx, r.tx, familyID)
}

func getWorkspaceSetByFamilyIDTx(ctx context.Context, tx *sql.Tx, familyID string) (workspace.WorkspaceSet, error) {
	row := tx.QueryRowContext(ctx, `
SELECT `+workspaceSetColumns+` FROM workspace_sets WHERE family_id = ?`, familyID)
	set, err := scanWorkspaceSetRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.WorkspaceSet{}, fmt.Errorf("%w: workspace set for family %s", ports.ErrPersistenceNotFound, familyID)
	}
	return set, err
}

// workspaceSetColumns is shared by every read of a workspace_sets row
// (GetWorkspaceSetByFamilyID and TransitionWorkspaceSetState's own
// UPDATE...RETURNING below) so both stay in the exact column order
// scanWorkspaceSetRow expects.
const workspaceSetColumns = `id, project_id, family_id, state, version, base_revision_set_json, base_revision_set_hash`

func scanWorkspaceSetRow(row repositoryRowScanner) (workspace.WorkspaceSet, error) {
	var id, projectID, familyID, state string
	var version uint64
	var baseRevisionSetJSON, baseRevisionSetHash sql.NullString
	if err := row.Scan(&id, &projectID, &familyID, &state, &version, &baseRevisionSetJSON, &baseRevisionSetHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workspace.WorkspaceSet{}, err
		}
		return workspace.WorkspaceSet{}, MapSQLiteError(fmt.Errorf("scan workspace set row: %w", err))
	}
	set := workspace.WorkspaceSet{
		ID: workspace.WorkspaceSetID(id), ProjectID: project.ProjectID(projectID), FamilyID: work.TaskFamilyID(familyID),
		State: workspace.WorkspaceSetState(state), Version: version,
	}
	if baseRevisionSetJSON.Valid {
		var entries []workspace.Revision
		if err := json.Unmarshal([]byte(baseRevisionSetJSON.String), &entries); err != nil {
			return workspace.WorkspaceSet{}, fmt.Errorf("unmarshal workspace set %s base revision set: %w", id, err)
		}
		// Re-run through NewRevisionSet rather than trusting the stored JSON
		// directly (the same "round-trip a stored row back through the exact
		// same normalization/validation every write already passed"
		// discipline scanRepositoryScopeRow already follows above) and
		// cross-check the stored hash against the freshly recomputed one —
		// a real, if cheap, integrity check that base_revision_set_hash was
		// never edited independently of base_revision_set_json.
		revisionSet, err := workspace.NewRevisionSet(entries)
		if err != nil {
			return workspace.WorkspaceSet{}, fmt.Errorf("reconstruct workspace set %s base revision set: %w", id, err)
		}
		if baseRevisionSetHash.Valid && revisionSet.ContentHash() != baseRevisionSetHash.String {
			return workspace.WorkspaceSet{}, fmt.Errorf(
				"workspace set %s base revision set hash mismatch: stored=%s recomputed=%s",
				id, baseRevisionSetHash.String, revisionSet.ContentHash())
		}
		set.BaseRevisionSet = &revisionSet
	}
	return set, nil
}

// --- RepositoryScope ---

// AddRepositoryScope implements ports.WorkRepository.
func (r workRepository) AddRepositoryScope(ctx context.Context, scope work.RepositoryScope) (work.RepositoryScope, error) {
	return addRepositoryScopeTx(ctx, r.tx, scope)
}

func addRepositoryScopeTx(ctx context.Context, tx *sql.Tx, scope work.RepositoryScope) (work.RepositoryScope, error) {
	if scope.FamilyID() == "" || scope.RepositoryID() == "" {
		return work.RepositoryScope{}, errors.New("repository scope family id and repository id are required")
	}

	var familyProjectID string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM task_families WHERE id = ?`, string(scope.FamilyID())).Scan(&familyProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return work.RepositoryScope{}, fmt.Errorf("%w: task family %s", ports.ErrPersistenceNotFound, scope.FamilyID())
	}
	if err != nil {
		return work.RepositoryScope{}, MapSQLiteError(fmt.Errorf("resolve repository scope family: %w", err))
	}

	var repositoryExists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM repositories WHERE id = ?`, string(scope.RepositoryID())).Scan(&repositoryExists)
	if errors.Is(err, sql.ErrNoRows) {
		return work.RepositoryScope{}, fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, scope.RepositoryID())
	}
	if err != nil {
		return work.RepositoryScope{}, MapSQLiteError(fmt.Errorf("resolve repository scope repository: %w", err))
	}

	pathsJSON, err := json.Marshal(scope.PathScopes())
	if err != nil {
		return work.RepositoryScope{}, fmt.Errorf("marshal repository scope paths: %w", err)
	}

	createdAt := scope.AddedAt().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO family_repository_scopes (family_id, project_id, repository_id, scope_version, access, paths_json, reason, added_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(scope.FamilyID()), familyProjectID, string(scope.RepositoryID()), scope.AddedInScopeVersion(),
		string(scope.Access()), string(pathsJSON), scope.Reason(), scope.AddedBy(), createdAt,
	); err != nil {
		return work.RepositoryScope{}, MapSQLiteError(fmt.Errorf("add repository scope: %w", err))
	}
	return scope, nil
}

// ListFamilyRepositoryScopes implements ports.WorkRepository, ordered by
// (scope_version, repository_id, access) for a stable, deterministic
// result a test can assert on exactly.
func (r workRepository) ListFamilyRepositoryScopes(ctx context.Context, familyID string) ([]work.RepositoryScope, error) {
	return listFamilyRepositoryScopesTx(ctx, r.tx, familyID)
}

func listFamilyRepositoryScopesTx(ctx context.Context, tx *sql.Tx, familyID string) ([]work.RepositoryScope, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT family_id, repository_id, scope_version, access, paths_json, reason, added_by, created_at
FROM family_repository_scopes WHERE family_id = ? ORDER BY scope_version, repository_id, access`, familyID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list family repository scopes: %w", err))
	}
	defer rows.Close()

	var result []work.RepositoryScope
	for rows.Next() {
		scope, err := scanRepositoryScopeRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate family repository scopes: %w", err))
	}
	return result, nil
}

func scanRepositoryScopeRow(row repositoryRowScanner) (work.RepositoryScope, error) {
	var familyID, repositoryID, access, pathsJSON, reason, addedBy, createdAtText string
	var scopeVersion uint64
	if err := row.Scan(&familyID, &repositoryID, &scopeVersion, &access, &pathsJSON, &reason, &addedBy, &createdAtText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return work.RepositoryScope{}, err
		}
		return work.RepositoryScope{}, MapSQLiteError(fmt.Errorf("scan repository scope row: %w", err))
	}
	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		return work.RepositoryScope{}, fmt.Errorf("unmarshal repository scope paths: %w", err)
	}
	createdAt, err := parseDBTime(createdAtText)
	if err != nil {
		return work.RepositoryScope{}, err
	}
	// Re-run through work.NewRepositoryScope rather than populating the
	// struct's own private fields directly (it has none exported to set):
	// this round-trips a stored row back through the exact same
	// normalization/validation every write already passed, so a scope this
	// method returns is always a genuinely valid domain value, never a
	// bare reinterpretation of raw columns.
	return work.NewRepositoryScope(
		work.TaskFamilyID(familyID), scopeVersion, project.RepositoryID(repositoryID),
		work.RepositoryAccess(access), paths, reason, addedBy, createdAt,
	)
}

// --- WorkItem effective scope (V3-05) ---
//
// work_item_effective_scopes is a separate table from
// family_repository_scopes (0011_work_item_effective_scopes.sql's own doc
// comment explains why): a child WorkItem's own effective scope is a
// per-WorkItem subset *declaration*, checked by
// work.ValidateEffectiveScopes against the family's already-audited
// RepositoryScope grants, never a new grant of its own. Reusing
// work.RepositoryScope end to end here (rather than inventing a second,
// near-duplicate "effective scope" domain type) mirrors exactly how
// AddRepositoryScope/ListFamilyRepositoryScopes above already round-trip
// that same type — see the migration's own comment for why this table
// carries reason/added_by/created_at columns too, beyond the design doc's
// own terse row listing.

// AddEffectiveScope implements ports.WorkRepository.
func (r workRepository) AddEffectiveScope(ctx context.Context, workItemID string, scope work.RepositoryScope) (work.RepositoryScope, error) {
	return addEffectiveScopeTx(ctx, r.tx, workItemID, scope)
}

func addEffectiveScopeTx(ctx context.Context, tx *sql.Tx, workItemID string, scope work.RepositoryScope) (work.RepositoryScope, error) {
	if workItemID == "" || scope.RepositoryID() == "" {
		return work.RepositoryScope{}, errors.New("effective scope work item id and repository id are required")
	}

	var workItemProjectID string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM work_items WHERE id = ?`, workItemID).Scan(&workItemProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return work.RepositoryScope{}, fmt.Errorf("%w: work item %s", ports.ErrPersistenceNotFound, workItemID)
	}
	if err != nil {
		return work.RepositoryScope{}, MapSQLiteError(fmt.Errorf("resolve effective scope work item: %w", err))
	}

	var repositoryExists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM repositories WHERE id = ?`, string(scope.RepositoryID())).Scan(&repositoryExists)
	if errors.Is(err, sql.ErrNoRows) {
		return work.RepositoryScope{}, fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, scope.RepositoryID())
	}
	if err != nil {
		return work.RepositoryScope{}, MapSQLiteError(fmt.Errorf("resolve effective scope repository: %w", err))
	}

	pathsJSON, err := json.Marshal(scope.PathScopes())
	if err != nil {
		return work.RepositoryScope{}, fmt.Errorf("marshal effective scope paths: %w", err)
	}

	createdAt := scope.AddedAt().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO work_item_effective_scopes (work_item_id, project_id, family_id, repository_id, scope_version, access, paths_json, reason, added_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		workItemID, workItemProjectID, string(scope.FamilyID()), string(scope.RepositoryID()), scope.AddedInScopeVersion(),
		string(scope.Access()), string(pathsJSON), scope.Reason(), scope.AddedBy(), createdAt,
	); err != nil {
		return work.RepositoryScope{}, MapSQLiteError(fmt.Errorf("add effective scope: %w", err))
	}
	return scope, nil
}

// ListWorkItemEffectiveScopes implements ports.WorkRepository, ordered by
// (repository_id, access) for a stable, deterministic result a test can
// assert on exactly, the same as ListFamilyRepositoryScopes' own ordering
// discipline (minus scope_version, since every row this task's own
// CreateChildWorkItem ever writes for one work item shares a single pinned
// ScopeVersion).
func (r workRepository) ListWorkItemEffectiveScopes(ctx context.Context, workItemID string) ([]work.RepositoryScope, error) {
	return listWorkItemEffectiveScopesTx(ctx, r.tx, workItemID)
}

func listWorkItemEffectiveScopesTx(ctx context.Context, tx *sql.Tx, workItemID string) ([]work.RepositoryScope, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT family_id, repository_id, scope_version, access, paths_json, reason, added_by, created_at
FROM work_item_effective_scopes WHERE work_item_id = ? ORDER BY repository_id, access`, workItemID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list work item effective scopes: %w", err))
	}
	defer rows.Close()

	var result []work.RepositoryScope
	for rows.Next() {
		scope, err := scanRepositoryScopeRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate work item effective scopes: %w", err))
	}
	return result, nil
}

// --- WorkspaceSet state / RepositoryWorkspace (V3-06) ---
//
// This section gives workRepository the persistence half of
// internal/app/workspaceprovision.Handler, the WORKSPACE_PROVISION job
// consumer V3-04's own CreateRootWorkItem already enqueues one of per
// repository in a root task's initial scope. It reuses
// repositoryWorkspaceColumns/scanRepositoryWorkspace from
// workspace_lifecycle.go (same package, V3-09/V3-10/V3-11's own
// QuarantineRepositoryWorkspace/ReleaseRepositoryWorkspace/
// RecreateRepositoryWorkspace) rather than duplicating that column list/scan
// shape a second time — first-generation creation (this section's own job)
// and later-generation lifecycle transitions (workspace_lifecycle.go's own
// job) are two different concerns over the identical row shape.

// TransitionWorkspaceSetState implements ports.WorkRepository (V3-06): see
// that interface method's own doc comment for the full CAS contract.
func (r workRepository) TransitionWorkspaceSetState(ctx context.Context, req ports.TransitionWorkspaceSetStateRequest) (workspace.WorkspaceSet, error) {
	return transitionWorkspaceSetStateTx(ctx, r.tx, req)
}

func transitionWorkspaceSetStateTx(ctx context.Context, tx *sql.Tx, req ports.TransitionWorkspaceSetStateRequest) (workspace.WorkspaceSet, error) {
	if req.WorkspaceSetID == "" {
		return workspace.WorkspaceSet{}, errors.New("workspace set id is required")
	}

	var revisionSetJSON, revisionSetHash sql.NullString
	if req.BaseRevisionSet != nil {
		entries := req.BaseRevisionSet.Entries()
		encoded, err := json.Marshal(entries)
		if err != nil {
			return workspace.WorkspaceSet{}, fmt.Errorf("marshal workspace set base revision set: %w", err)
		}
		revisionSetJSON = sql.NullString{String: string(encoded), Valid: true}
		revisionSetHash = sql.NullString{String: req.BaseRevisionSet.ContentHash(), Valid: true}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := tx.QueryRowContext(ctx, `
UPDATE workspace_sets
SET state = ?, version = version + 1, updated_at = ?,
    base_revision_set_json = COALESCE(?, base_revision_set_json),
    base_revision_set_hash = COALESCE(?, base_revision_set_hash)
WHERE id = ? AND state = ? AND version = ?
RETURNING `+workspaceSetColumns,
		string(req.NextState), now, revisionSetJSON, revisionSetHash,
		req.WorkspaceSetID, string(req.ExpectedState), req.ExpectedVersion,
	)
	set, err := scanWorkspaceSetRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM workspace_sets WHERE id = ?`, req.WorkspaceSetID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return workspace.WorkspaceSet{}, fmt.Errorf("%w: workspace set %s", ports.ErrPersistenceNotFound, req.WorkspaceSetID)
		}
		if lookupErr != nil {
			return workspace.WorkspaceSet{}, MapSQLiteError(fmt.Errorf("check stale workspace set transition: %w", lookupErr))
		}
		return workspace.WorkspaceSet{}, fmt.Errorf(
			"%w: workspace set %s expected %s@%d",
			ports.ErrOptimisticConflict, req.WorkspaceSetID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	if err != nil {
		return workspace.WorkspaceSet{}, MapSQLiteError(fmt.Errorf("transition workspace set %s state: %w", req.WorkspaceSetID, err))
	}
	return set, nil
}

// CreateRepositoryWorkspace implements ports.WorkRepository (V3-06): see
// that interface method's own doc comment for the full contract, including
// why rw is accepted as an already-constructed value and how a UNIQUE
// conflict (GC-INV-03) is mapped to ports.ErrPersistenceAlreadyExists.
func (r workRepository) CreateRepositoryWorkspace(ctx context.Context, rw workspace.RepositoryWorkspace) (workspace.RepositoryWorkspace, error) {
	return createRepositoryWorkspaceTx(ctx, r.tx, rw)
}

func createRepositoryWorkspaceTx(ctx context.Context, tx *sql.Tx, rw workspace.RepositoryWorkspace) (workspace.RepositoryWorkspace, error) {
	if rw.ID == "" || rw.WorkspaceSetID == "" || rw.RepositoryID == "" || rw.Generation == 0 {
		return workspace.RepositoryWorkspace{}, errors.New("repository workspace id, workspace set id, repository id and generation are required")
	}

	// project_id/family_id are not part of the domain struct (they are
	// always implied by WorkspaceSetID — see this method's own interface
	// doc comment) but the repository_workspaces schema carries both
	// redundantly, the same "carried for query-convenience/FK-safety"
	// reason family_repository_scopes/work_item_effective_scopes already
	// do (0011_work_item_effective_scopes.sql). Resolving them here also
	// doubles as this method's own WorkspaceSetID existence check.
	var projectID, familyID string
	err := tx.QueryRowContext(ctx, `SELECT project_id, family_id FROM workspace_sets WHERE id = ?`, string(rw.WorkspaceSetID)).
		Scan(&projectID, &familyID)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("%w: workspace set %s", ports.ErrPersistenceNotFound, rw.WorkspaceSetID)
	}
	if err != nil {
		return workspace.RepositoryWorkspace{}, MapSQLiteError(fmt.Errorf("resolve repository workspace's workspace set: %w", err))
	}

	var repositoryExists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM repositories WHERE id = ?`, string(rw.RepositoryID)).Scan(&repositoryExists)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, rw.RepositoryID)
	}
	if err != nil {
		return workspace.RepositoryWorkspace{}, MapSQLiteError(fmt.Errorf("resolve repository workspace's repository: %w", err))
	}

	var branchRef, currentRevision, lastProvisionErrorCode any
	if rw.BranchRef != "" {
		branchRef = rw.BranchRef
	}
	if rw.CurrentRevision != "" {
		currentRevision = rw.CurrentRevision
	}
	if rw.LastProvisionErrorCode != nil {
		lastProvisionErrorCode = *rw.LastProvisionErrorCode
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := tx.QueryRowContext(ctx, `
INSERT INTO repository_workspaces (
    id, project_id, workspace_set_id, family_id, repository_id, generation,
    locator, branch_ref, base_revision, current_revision, state, version,
    last_provision_error_code, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING `+repositoryWorkspaceColumns,
		string(rw.ID), projectID, string(rw.WorkspaceSetID), familyID, string(rw.RepositoryID), rw.Generation,
		rw.Locator, branchRef, rw.BaseRevision, currentRevision, string(rw.State), rw.Version,
		lastProvisionErrorCode, now, now,
	)
	created, err := scanRepositoryWorkspace(row)
	if err != nil {
		var existing int
		lookupErr := tx.QueryRowContext(ctx, `
SELECT 1 FROM repository_workspaces WHERE workspace_set_id = ? AND repository_id = ? AND generation = ?`,
			string(rw.WorkspaceSetID), string(rw.RepositoryID), rw.Generation,
		).Scan(&existing)
		if lookupErr == nil {
			return workspace.RepositoryWorkspace{}, fmt.Errorf(
				"%w: repository workspace for workspace set %s repository %s generation %d",
				ports.ErrPersistenceAlreadyExists, rw.WorkspaceSetID, rw.RepositoryID, rw.Generation,
			)
		}
		return workspace.RepositoryWorkspace{}, MapSQLiteError(fmt.Errorf("create repository workspace: %w", err))
	}
	return created, nil
}

// GetRepositoryWorkspace implements ports.WorkRepository (V3-06).
func (r workRepository) GetRepositoryWorkspace(ctx context.Context, workspaceSetID, repositoryID string, generation uint64) (workspace.RepositoryWorkspace, error) {
	return getRepositoryWorkspaceTx(ctx, r.tx, workspaceSetID, repositoryID, generation)
}

func getRepositoryWorkspaceTx(ctx context.Context, tx *sql.Tx, workspaceSetID, repositoryID string, generation uint64) (workspace.RepositoryWorkspace, error) {
	row := tx.QueryRowContext(ctx, `
SELECT `+repositoryWorkspaceColumns+`
FROM repository_workspaces WHERE workspace_set_id = ? AND repository_id = ? AND generation = ?`,
		workspaceSetID, repositoryID, generation)
	rw, err := scanRepositoryWorkspace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.RepositoryWorkspace{}, fmt.Errorf(
			"%w: repository workspace for workspace set %s repository %s generation %d",
			ports.ErrPersistenceNotFound, workspaceSetID, repositoryID, generation)
	}
	if err != nil {
		return workspace.RepositoryWorkspace{}, MapSQLiteError(fmt.Errorf("get repository workspace: %w", err))
	}
	return rw, nil
}

// ListWorkspaceSetRepositoryWorkspaces implements ports.WorkRepository
// (V3-06), ordered by (repository_id, generation) for a stable,
// deterministic result a test can assert on exactly.
func (r workRepository) ListWorkspaceSetRepositoryWorkspaces(ctx context.Context, workspaceSetID string) ([]workspace.RepositoryWorkspace, error) {
	return listWorkspaceSetRepositoryWorkspacesTx(ctx, r.tx, workspaceSetID)
}

func listWorkspaceSetRepositoryWorkspacesTx(ctx context.Context, tx *sql.Tx, workspaceSetID string) ([]workspace.RepositoryWorkspace, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT `+repositoryWorkspaceColumns+`
FROM repository_workspaces WHERE workspace_set_id = ? ORDER BY repository_id, generation`, workspaceSetID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list workspace set repository workspaces: %w", err))
	}
	defer rows.Close()

	var result []workspace.RepositoryWorkspace
	for rows.Next() {
		rw, err := scanRepositoryWorkspace(rows)
		if err != nil {
			return nil, MapSQLiteError(fmt.Errorf("scan repository workspace row: %w", err))
		}
		result = append(result, rw)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate workspace set repository workspaces: %w", err))
	}
	return result, nil
}

// --- TaskFamily ScopeVersion / ScopeExpansionRequest (V3-08) ---
//
// This section gives workRepository the persistence half of
// internal/app/work's four scope-expansion commands
// (RequestScopeExpansion/ApproveScopeExpansion/RejectScopeExpansion/
// WithdrawScopeExpansion, docs/design/05-v3-project-workspace.md V3-08):
// the fenced CAS that bumps a TaskFamily's own ScopeVersion, and CRUD/CAS
// for the scope_expansion_requests table (0015_scope_expansion_requests.sql).

// TransitionTaskFamilyScopeVersion implements ports.WorkRepository (V3-08):
// see that interface method's own doc comment for the full CAS contract.
func (r workRepository) TransitionTaskFamilyScopeVersion(ctx context.Context, req ports.TransitionTaskFamilyScopeVersionRequest) (work.TaskFamily, error) {
	return transitionTaskFamilyScopeVersionTx(ctx, r.tx, req)
}

func transitionTaskFamilyScopeVersionTx(ctx context.Context, tx *sql.Tx, req ports.TransitionTaskFamilyScopeVersionRequest) (work.TaskFamily, error) {
	if req.FamilyID == "" {
		return work.TaskFamily{}, errors.New("task family id is required")
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := tx.QueryRowContext(ctx, `
UPDATE task_families
SET scope_version = scope_version + 1, version = version + 1, updated_at = ?
WHERE id = ? AND scope_version = ? AND version = ?
RETURNING id, project_id, root_work_item_id, scope_version, status, version`,
		now, req.FamilyID, req.ExpectedScopeVersion, req.ExpectedVersion,
	)
	family, err := scanTaskFamilyRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM task_families WHERE id = ?`, req.FamilyID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return work.TaskFamily{}, fmt.Errorf("%w: task family %s", ports.ErrPersistenceNotFound, req.FamilyID)
		}
		if lookupErr != nil {
			return work.TaskFamily{}, MapSQLiteError(fmt.Errorf("check stale task family scope version transition: %w", lookupErr))
		}
		return work.TaskFamily{}, fmt.Errorf(
			"%w: task family %s expected scope version %d@%d",
			ports.ErrOptimisticConflict, req.FamilyID, req.ExpectedScopeVersion, req.ExpectedVersion,
		)
	}
	if err != nil {
		return work.TaskFamily{}, MapSQLiteError(fmt.Errorf("transition task family %s scope version: %w", req.FamilyID, err))
	}
	return family, nil
}

// scopeExpansionRequestColumns is shared by every read of a
// scope_expansion_requests row so both stay in the exact column order
// scanScopeExpansionRequestRow expects.
const scopeExpansionRequestColumns = `
id, project_id, family_id, referenced_work_item_id, requested_grants_json, reason, status,
requested_by, requested_at, decided_by, decided_at, decision_note, approved_scope_version, version`

// CreateScopeExpansionRequest implements ports.WorkRepository (V3-08): see
// that interface method's own doc comment.
func (r workRepository) CreateScopeExpansionRequest(ctx context.Context, req work.ScopeExpansionRequest) (work.ScopeExpansionRequest, error) {
	return createScopeExpansionRequestTx(ctx, r.tx, req)
}

func createScopeExpansionRequestTx(ctx context.Context, tx *sql.Tx, req work.ScopeExpansionRequest) (work.ScopeExpansionRequest, error) {
	if req.ID == "" || req.FamilyID == "" {
		return work.ScopeExpansionRequest{}, errors.New("scope expansion request id and family id are required")
	}

	var familyProjectID string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM task_families WHERE id = ?`, string(req.FamilyID)).Scan(&familyProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return work.ScopeExpansionRequest{}, fmt.Errorf("%w: task family %s", ports.ErrPersistenceNotFound, req.FamilyID)
	}
	if err != nil {
		return work.ScopeExpansionRequest{}, MapSQLiteError(fmt.Errorf("resolve scope expansion request family: %w", err))
	}

	grantsJSON, err := json.Marshal(req.RequestedGrants)
	if err != nil {
		return work.ScopeExpansionRequest{}, fmt.Errorf("marshal scope expansion request grants: %w", err)
	}
	var referencedWorkItemID any
	if req.ReferencedWorkItemID != nil {
		referencedWorkItemID = string(*req.ReferencedWorkItemID)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	requestedAt := req.RequestedAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scope_expansion_requests (
    id, project_id, family_id, referenced_work_item_id, requested_grants_json, reason, status,
    requested_by, requested_at, decided_by, decided_at, decision_note, approved_scope_version, version,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, NULL, ?, ?, ?)`,
		string(req.ID), familyProjectID, string(req.FamilyID), referencedWorkItemID, string(grantsJSON), req.Reason,
		string(req.Status), req.RequestedBy, requestedAt, req.Version, now, now,
	); err != nil {
		return work.ScopeExpansionRequest{}, MapSQLiteError(fmt.Errorf("create scope expansion request: %w", err))
	}
	return req, nil
}

// GetScopeExpansionRequest implements ports.WorkRepository (V3-08).
func (r workRepository) GetScopeExpansionRequest(ctx context.Context, id string) (work.ScopeExpansionRequest, error) {
	return getScopeExpansionRequestTx(ctx, r.tx, id)
}

func getScopeExpansionRequestTx(ctx context.Context, tx *sql.Tx, id string) (work.ScopeExpansionRequest, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+scopeExpansionRequestColumns+` FROM scope_expansion_requests WHERE id = ?`, id)
	req, err := scanScopeExpansionRequestRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return work.ScopeExpansionRequest{}, fmt.Errorf("%w: scope expansion request %s", ports.ErrPersistenceNotFound, id)
	}
	return req, err
}

// ListFamilyScopeExpansionRequests implements ports.WorkRepository (V3-08),
// ordered by (requested_at, id) for a stable, deterministic result a test
// can assert on exactly.
func (r workRepository) ListFamilyScopeExpansionRequests(ctx context.Context, familyID string) ([]work.ScopeExpansionRequest, error) {
	return listFamilyScopeExpansionRequestsTx(ctx, r.tx, familyID)
}

func listFamilyScopeExpansionRequestsTx(ctx context.Context, tx *sql.Tx, familyID string) ([]work.ScopeExpansionRequest, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT `+scopeExpansionRequestColumns+`
FROM scope_expansion_requests WHERE family_id = ? ORDER BY requested_at, id`, familyID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list family scope expansion requests: %w", err))
	}
	defer rows.Close()

	var result []work.ScopeExpansionRequest
	for rows.Next() {
		req, err := scanScopeExpansionRequestRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, req)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate family scope expansion requests: %w", err))
	}
	return result, nil
}

func scanScopeExpansionRequestRow(row repositoryRowScanner) (work.ScopeExpansionRequest, error) {
	var id, projectID, familyID, grantsJSON, reason, status, requestedBy, requestedAtText string
	var referencedWorkItemID, decidedBy, decisionNote, decidedAtText sql.NullString
	var approvedScopeVersion sql.NullInt64
	var version uint64
	if err := row.Scan(
		&id, &projectID, &familyID, &referencedWorkItemID, &grantsJSON, &reason, &status,
		&requestedBy, &requestedAtText, &decidedBy, &decidedAtText, &decisionNote, &approvedScopeVersion, &version,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return work.ScopeExpansionRequest{}, err
		}
		return work.ScopeExpansionRequest{}, MapSQLiteError(fmt.Errorf("scan scope expansion request row: %w", err))
	}

	var grants []work.RequestedGrant
	if err := json.Unmarshal([]byte(grantsJSON), &grants); err != nil {
		return work.ScopeExpansionRequest{}, fmt.Errorf("unmarshal scope expansion request grants: %w", err)
	}
	requestedAt, err := parseDBTime(requestedAtText)
	if err != nil {
		return work.ScopeExpansionRequest{}, err
	}

	req := work.ScopeExpansionRequest{
		ID: work.ScopeExpansionRequestID(id), FamilyID: work.TaskFamilyID(familyID), ProjectID: project.ProjectID(projectID),
		RequestedGrants: grants, Reason: reason, Status: work.ScopeExpansionStatus(status),
		RequestedBy: requestedBy, RequestedAt: requestedAt, Version: version,
	}
	if referencedWorkItemID.Valid {
		workItemID := work.WorkItemID(referencedWorkItemID.String)
		req.ReferencedWorkItemID = &workItemID
	}
	if decidedBy.Valid {
		req.DecidedBy = decidedBy.String
	}
	if decisionNote.Valid {
		req.DecisionNote = decisionNote.String
	}
	if decidedAtText.Valid {
		decidedAt, err := parseDBTime(decidedAtText.String)
		if err != nil {
			return work.ScopeExpansionRequest{}, err
		}
		req.DecidedAt = &decidedAt
	}
	if approvedScopeVersion.Valid {
		v := uint64(approvedScopeVersion.Int64)
		req.ApprovedScopeVersion = &v
	}
	return req, nil
}

// TransitionScopeExpansionRequestStatus implements ports.WorkRepository
// (V3-08): see that interface method's own doc comment for the full CAS
// contract.
func (r workRepository) TransitionScopeExpansionRequestStatus(ctx context.Context, req ports.TransitionScopeExpansionRequestStatusRequest) (work.ScopeExpansionRequest, error) {
	return transitionScopeExpansionRequestStatusTx(ctx, r.tx, req)
}

func transitionScopeExpansionRequestStatusTx(ctx context.Context, tx *sql.Tx, req ports.TransitionScopeExpansionRequestStatusRequest) (work.ScopeExpansionRequest, error) {
	if req.RequestID == "" {
		return work.ScopeExpansionRequest{}, errors.New("scope expansion request id is required")
	}
	if err := work.CanTransitionScopeExpansionStatus(req.ExpectedStatus, req.NextStatus); err != nil {
		return work.ScopeExpansionRequest{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	decidedAt := req.DecidedAt.UTC().Format(time.RFC3339Nano)
	var decisionNote any
	if req.DecisionNote != "" {
		decisionNote = req.DecisionNote
	}
	var approvedScopeVersion any
	if req.ApprovedScopeVersion != nil {
		approvedScopeVersion = *req.ApprovedScopeVersion
	}

	row := tx.QueryRowContext(ctx, `
UPDATE scope_expansion_requests
SET status = ?, decided_by = ?, decided_at = ?, decision_note = ?, approved_scope_version = ?,
    version = version + 1, updated_at = ?
WHERE id = ? AND status = ? AND version = ?
RETURNING `+scopeExpansionRequestColumns,
		string(req.NextStatus), req.DecidedBy, decidedAt, decisionNote, approvedScopeVersion, now,
		req.RequestID, string(req.ExpectedStatus), req.ExpectedVersion,
	)
	updated, err := scanScopeExpansionRequestRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM scope_expansion_requests WHERE id = ?`, req.RequestID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return work.ScopeExpansionRequest{}, fmt.Errorf("%w: scope expansion request %s", ports.ErrPersistenceNotFound, req.RequestID)
		}
		if lookupErr != nil {
			return work.ScopeExpansionRequest{}, MapSQLiteError(fmt.Errorf("check stale scope expansion request transition: %w", lookupErr))
		}
		return work.ScopeExpansionRequest{}, fmt.Errorf(
			"%w: scope expansion request %s expected %s@%d",
			ports.ErrOptimisticConflict, req.RequestID, req.ExpectedStatus, req.ExpectedVersion,
		)
	}
	if err != nil {
		return work.ScopeExpansionRequest{}, MapSQLiteError(fmt.Errorf("transition scope expansion request %s status: %w", req.RequestID, err))
	}
	return updated, nil
}
