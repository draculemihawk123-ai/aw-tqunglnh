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
SELECT id, project_id, family_id, state, version FROM workspace_sets WHERE family_id = ?`, familyID)
	set, err := scanWorkspaceSetRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.WorkspaceSet{}, fmt.Errorf("%w: workspace set for family %s", ports.ErrPersistenceNotFound, familyID)
	}
	return set, err
}

func scanWorkspaceSetRow(row repositoryRowScanner) (workspace.WorkspaceSet, error) {
	var id, projectID, familyID, state string
	var version uint64
	if err := row.Scan(&id, &projectID, &familyID, &state, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workspace.WorkspaceSet{}, err
		}
		return workspace.WorkspaceSet{}, MapSQLiteError(fmt.Errorf("scan workspace set row: %w", err))
	}
	return workspace.WorkspaceSet{
		ID: workspace.WorkspaceSetID(id), ProjectID: project.ProjectID(projectID), FamilyID: work.TaskFamilyID(familyID),
		State: workspace.WorkspaceSetState(state), Version: version,
	}, nil
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
