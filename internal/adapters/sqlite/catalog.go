package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// --- Project ---

// CreateProject implements ports.CatalogRepository (V3-01): inserts a new
// Project row, ACTIVE/generation 1, per project.NewProject's own rule.
func (r catalogRepository) CreateProject(ctx context.Context, req ports.CreateProjectRequest) (project.Project, error) {
	return createProjectTx(ctx, r.tx, req)
}

func createProjectTx(ctx context.Context, tx *sql.Tx, req ports.CreateProjectRequest) (project.Project, error) {
	created, err := project.NewProject(project.ProjectID(req.ID), req.Name)
	if err != nil {
		return project.Project{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO projects (id, name, status, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		string(created.ID), created.Name, string(created.Status), created.Version, now, now,
	); err != nil {
		return project.Project{}, MapSQLiteError(fmt.Errorf("create project: %w", err))
	}
	return created, nil
}

// GetProject implements ports.CatalogRepository.
func (r catalogRepository) GetProject(ctx context.Context, id string) (project.Project, error) {
	return getProjectTx(ctx, r.tx, id)
}

// ListProjects implements ports.CatalogRepository (V6-03): every Project
// row, ID order — mirroring listProjectRepositoriesTx's own plain
// unfiltered-scan shape below (there is no foreign key to filter Project
// itself by; a Project IS the scope unit ADR-025's installation-scoped
// ListProjects query returns in full).
func (r catalogRepository) ListProjects(ctx context.Context) ([]project.Project, error) {
	return listProjectsTx(ctx, r.tx)
}

func listProjectsTx(ctx context.Context, tx *sql.Tx) ([]project.Project, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, name, status, version FROM projects ORDER BY id`)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list projects: %w", err))
	}
	defer rows.Close()

	var result []project.Project
	for rows.Next() {
		var id, name, status string
		var version uint64
		if err := rows.Scan(&id, &name, &status, &version); err != nil {
			return nil, MapSQLiteError(fmt.Errorf("scan project row: %w", err))
		}
		result = append(result, project.Project{ID: project.ProjectID(id), Name: name, Status: project.ProjectStatus(status), Version: version})
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate projects: %w", err))
	}
	return result, nil
}

func getProjectTx(ctx context.Context, tx *sql.Tx, id string) (project.Project, error) {
	var name, status string
	var version uint64
	err := tx.QueryRowContext(ctx,
		`SELECT name, status, version FROM projects WHERE id = ?`, id,
	).Scan(&name, &status, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Project{}, fmt.Errorf("%w: project %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return project.Project{}, MapSQLiteError(fmt.Errorf("load project: %w", err))
	}
	return project.Project{ID: project.ProjectID(id), Name: name, Status: project.ProjectStatus(status), Version: version}, nil
}

// --- Repository ---

// RegisterRepository implements ports.CatalogRepository (V3-01): the
// persistence half of the RegisterRepository command
// (internal/app/catalog.RegisterRepository composes this alongside the
// probe job enqueue and the RepositoryRegistered domain event inside the
// same transaction). req.ProjectID must name a Project that already
// exists — ports.ErrPersistenceNotFound otherwise — so a Repository can
// never be created orphaned from any Project.
func (r catalogRepository) RegisterRepository(ctx context.Context, req ports.RegisterRepositoryRequest) (project.Repository, error) {
	return registerRepositoryTx(ctx, r.tx, req)
}

func registerRepositoryTx(ctx context.Context, tx *sql.Tx, req ports.RegisterRepositoryRequest) (project.Repository, error) {
	created, err := project.NewRepository(
		project.RepositoryID(req.ID), project.ProjectID(req.ProjectID), req.Name, req.RemoteLocator, req.DefaultRef,
	)
	if err != nil {
		return project.Repository{}, err
	}

	var projectExists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, req.ProjectID).Scan(&projectExists)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Repository{}, fmt.Errorf("%w: project %s", ports.ErrPersistenceNotFound, req.ProjectID)
	}
	if err != nil {
		return project.Repository{}, MapSQLiteError(fmt.Errorf("resolve repository project: %w", err))
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO repositories (
    id, project_id, name, local_path, default_ref, status, last_probe_error_code,
    version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
		string(created.ID), string(created.ProjectID), created.Name, created.RemoteLocator, created.DefaultRef,
		string(created.Status), created.Version, now, now,
	); err != nil {
		return project.Repository{}, MapSQLiteError(fmt.Errorf("register repository: %w", err))
	}
	return created, nil
}

// GetRepository implements ports.CatalogRepository.
func (r catalogRepository) GetRepository(ctx context.Context, id string) (project.Repository, error) {
	return getRepositoryTx(ctx, r.tx, id)
}

func getRepositoryTx(ctx context.Context, tx *sql.Tx, id string) (project.Repository, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, name, local_path, default_ref, status, last_probe_error_code, version
FROM repositories WHERE id = ?`, id)
	repo, err := scanRepositoryRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Repository{}, fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, id)
	}
	return repo, err
}

// ListProjectRepositories implements ports.CatalogRepository: it filters
// strictly by the stored project_id foreign-key column — never by
// name/local_path/slug (V3-01's own "Hoàn thành khi: list/filter không
// suy identity từ slug/cwd/remote") — ordered by id for a stable,
// deterministic result a test can assert on exactly.
func (r catalogRepository) ListProjectRepositories(ctx context.Context, projectID string) ([]project.Repository, error) {
	return listProjectRepositoriesTx(ctx, r.tx, projectID)
}

func listProjectRepositoriesTx(ctx context.Context, tx *sql.Tx, projectID string) ([]project.Repository, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id, project_id, name, local_path, default_ref, status, last_probe_error_code, version
FROM repositories WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list project repositories: %w", err))
	}
	defer rows.Close()

	var result []project.Repository
	for rows.Next() {
		repo, err := scanRepositoryRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, repo)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate project repositories: %w", err))
	}
	return result, nil
}

func scanRepositoryRow(row repositoryRowScanner) (project.Repository, error) {
	var id, projectID, name, remoteLocator, defaultRef, status string
	var lastProbeErrorCode sql.NullString
	var version uint64
	if err := row.Scan(&id, &projectID, &name, &remoteLocator, &defaultRef, &status, &lastProbeErrorCode, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return project.Repository{}, err
		}
		return project.Repository{}, MapSQLiteError(fmt.Errorf("scan repository row: %w", err))
	}
	repo := project.Repository{
		ID: project.RepositoryID(id), ProjectID: project.ProjectID(projectID), Name: name,
		VCSKind: project.VCSGit, RemoteLocator: remoteLocator, DefaultRef: defaultRef,
		Status: project.RepositoryStatus(status), Version: version,
	}
	if lastProbeErrorCode.Valid {
		code := lastProbeErrorCode.String
		repo.LastProbeErrorCode = &code
	}
	return repo, nil
}

// repositoryRowScanner is satisfied by both *sql.Row and *sql.Rows —
// mirroring definitionVersionRowScanner's own reasoning in definitions.go.
type repositoryRowScanner interface {
	Scan(dest ...any) error
}

// TransitionRepositoryStatus implements ports.CatalogRepository (V3-02):
// see that interface method's own doc comment for the full CAS contract.
func (r catalogRepository) TransitionRepositoryStatus(ctx context.Context, req ports.TransitionRepositoryStatusRequest) (project.Repository, error) {
	return transitionRepositoryStatusTx(ctx, r.tx, req)
}

func transitionRepositoryStatusTx(ctx context.Context, tx *sql.Tx, req ports.TransitionRepositoryStatusRequest) (project.Repository, error) {
	if err := project.CanTransitionRepositoryStatus(req.ExpectedStatus, req.NextStatus); err != nil {
		return project.Repository{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var lastProbeErrorCode sql.NullString
	if req.LastProbeErrorCode != nil {
		lastProbeErrorCode = sql.NullString{String: *req.LastProbeErrorCode, Valid: true}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE repositories
SET status = ?, last_probe_error_code = ?, version = version + 1, updated_at = ?
WHERE id = ? AND status = ? AND version = ?`,
		string(req.NextStatus), lastProbeErrorCode, now,
		req.RepositoryID, string(req.ExpectedStatus), req.ExpectedVersion,
	)
	if err != nil {
		return project.Repository{}, MapSQLiteError(fmt.Errorf("transition repository %s status: %w", req.RepositoryID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return project.Repository{}, MapSQLiteError(fmt.Errorf("read repository status transition result: %w", err))
	}
	if affected != 1 {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM repositories WHERE id = ?`, req.RepositoryID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return project.Repository{}, fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, req.RepositoryID)
		}
		if lookupErr != nil {
			return project.Repository{}, MapSQLiteError(fmt.Errorf("check stale repository status transition: %w", lookupErr))
		}
		return project.Repository{}, fmt.Errorf(
			"%w: repository %s expected %s@%d",
			ports.ErrOptimisticConflict, req.RepositoryID, req.ExpectedStatus, req.ExpectedVersion,
		)
	}

	return getRepositoryTx(ctx, tx, req.RepositoryID)
}

// --- RepositoryProbeAttempt ---

// RecordRepositoryProbeAttempt implements ports.CatalogRepository (V3-02).
// job_id's UNIQUE constraint (0008_repository_probe_attempts.sql) is what
// makes a duplicate insert for the same durable job a real, storage-level
// impossibility rather than a convention callers must remember to honor.
func (r catalogRepository) RecordRepositoryProbeAttempt(ctx context.Context, req ports.RecordRepositoryProbeAttemptRequest) (ports.RepositoryProbeAttempt, error) {
	return recordRepositoryProbeAttemptTx(ctx, r.tx, req)
}

func recordRepositoryProbeAttemptTx(ctx context.Context, tx *sql.Tx, req ports.RecordRepositoryProbeAttemptRequest) (ports.RepositoryProbeAttempt, error) {
	now := time.Now().UTC()
	var result sql.NullString
	if req.Result != nil {
		result = sql.NullString{String: string(*req.Result), Valid: true}
	}
	errorCode := nullableStringPtr(req.ErrorCode)
	errorMessage := nullableStringPtr(req.ErrorMessage)
	baseCommit := nullableStringPtr(req.BaseCommit)
	var dirty sql.NullInt64
	if req.Dirty != nil {
		value := int64(0)
		if *req.Dirty {
			value = 1
		}
		dirty = sql.NullInt64{Int64: value, Valid: true}
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO repository_probe_attempts (
    id, project_id, repository_id, job_id, state, result,
    error_code, error_message, base_commit, dirty, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.ProjectID, req.RepositoryID, req.JobID, string(req.State), result,
		errorCode, errorMessage, baseCommit, dirty, now.Format(time.RFC3339Nano),
	); err != nil {
		return ports.RepositoryProbeAttempt{}, MapSQLiteError(fmt.Errorf("record repository probe attempt: %w", err))
	}

	return ports.RepositoryProbeAttempt{
		ID: req.ID, ProjectID: req.ProjectID, RepositoryID: req.RepositoryID, JobID: req.JobID,
		State: req.State, Result: req.Result, ErrorCode: req.ErrorCode, ErrorMessage: req.ErrorMessage,
		BaseCommit: req.BaseCommit, Dirty: req.Dirty, CreatedAt: now,
	}, nil
}

// ListRepositoryProbeAttempts implements ports.CatalogRepository,
// oldest-CreatedAt-first.
func (r catalogRepository) ListRepositoryProbeAttempts(ctx context.Context, repositoryID string) ([]ports.RepositoryProbeAttempt, error) {
	return listRepositoryProbeAttemptsTx(ctx, r.tx, repositoryID)
}

func listRepositoryProbeAttemptsTx(ctx context.Context, tx *sql.Tx, repositoryID string) ([]ports.RepositoryProbeAttempt, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id, project_id, repository_id, job_id, state, result, error_code, error_message, base_commit, dirty, created_at
FROM repository_probe_attempts WHERE repository_id = ? ORDER BY julianday(created_at)`, repositoryID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list repository probe attempts: %w", err))
	}
	defer rows.Close()

	var result []ports.RepositoryProbeAttempt
	for rows.Next() {
		attempt, err := scanRepositoryProbeAttemptRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate repository probe attempts: %w", err))
	}
	return result, nil
}

func scanRepositoryProbeAttemptRow(row repositoryRowScanner) (ports.RepositoryProbeAttempt, error) {
	var id, projectID, repositoryID, jobID, state, createdAtText string
	var resultValue, errorCode, errorMessage, baseCommit sql.NullString
	var dirty sql.NullInt64
	if err := row.Scan(&id, &projectID, &repositoryID, &jobID, &state, &resultValue, &errorCode, &errorMessage, &baseCommit, &dirty, &createdAtText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.RepositoryProbeAttempt{}, err
		}
		return ports.RepositoryProbeAttempt{}, MapSQLiteError(fmt.Errorf("scan repository probe attempt row: %w", err))
	}
	createdAt, err := parseDBTime(createdAtText)
	if err != nil {
		return ports.RepositoryProbeAttempt{}, err
	}
	attempt := ports.RepositoryProbeAttempt{
		ID: id, ProjectID: projectID, RepositoryID: repositoryID, JobID: jobID,
		State: ports.RepositoryProbeAttemptState(state), CreatedAt: createdAt,
	}
	if resultValue.Valid {
		status := project.RepositoryStatus(resultValue.String)
		attempt.Result = &status
	}
	if errorCode.Valid {
		attempt.ErrorCode = &errorCode.String
	}
	if errorMessage.Valid {
		attempt.ErrorMessage = &errorMessage.String
	}
	if baseCommit.Valid {
		attempt.BaseCommit = &baseCommit.String
	}
	if dirty.Valid {
		value := dirty.Int64 != 0
		attempt.Dirty = &value
	}
	return attempt, nil
}

// nullableStringPtr converts a Go *string (nil means SQL NULL) to
// sql.NullString for a parameterized query — distinct from this package's
// own nullableString(string) any (command_receipts.go), which instead
// treats an empty string as NULL; the two helpers exist for two genuinely
// different nullability conventions already established at their own call
// sites, not duplicated by accident.
func nullableStringPtr(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

// --- Component ---

// CreateComponent implements ports.CatalogRepository (V3-01). It
// validates req.RepositoryID exists and resolves that Repository's own
// project_id from the row itself, never trusting req.ProjectID's claim —
// a mismatch is rejected as ports.ErrCrossProjectReference, the same
// "resolve from the referenced row" discipline
// publishSharedDefinitionVersionTx already follows for a DependencyPin's
// own project.
func (r catalogRepository) CreateComponent(ctx context.Context, req ports.CreateComponentRequest) (project.Component, error) {
	return createComponentTx(ctx, r.tx, req)
}

func createComponentTx(ctx context.Context, tx *sql.Tx, req ports.CreateComponentRequest) (project.Component, error) {
	created, err := project.NewComponent(
		project.ComponentID(req.ID), project.ProjectID(req.ProjectID), project.RepositoryID(req.RepositoryID),
		req.Name, req.Path, req.Kind,
	)
	if err != nil {
		return project.Component{}, err
	}

	var repositoryProjectID string
	err = tx.QueryRowContext(ctx, `SELECT project_id FROM repositories WHERE id = ?`, req.RepositoryID).Scan(&repositoryProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Component{}, fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, req.RepositoryID)
	}
	if err != nil {
		return project.Component{}, MapSQLiteError(fmt.Errorf("resolve component repository project: %w", err))
	}
	if repositoryProjectID != req.ProjectID {
		return project.Component{}, fmt.Errorf("%w: component repository %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, req.RepositoryID, repositoryProjectID, req.ProjectID)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO components (id, project_id, repository_id, name, path, kind, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(created.ID), string(created.ProjectID), string(created.RepositoryID), created.Name,
		created.Path, created.Kind, created.Version, now, now,
	); err != nil {
		return project.Component{}, MapSQLiteError(fmt.Errorf("create component: %w", err))
	}
	return created, nil
}

// GetComponent implements ports.CatalogRepository.
func (r catalogRepository) GetComponent(ctx context.Context, id string) (project.Component, error) {
	return getComponentTx(ctx, r.tx, id)
}

func getComponentTx(ctx context.Context, tx *sql.Tx, id string) (project.Component, error) {
	var projectID, repositoryID, name, path, kind string
	var version uint64
	err := tx.QueryRowContext(ctx, `
SELECT project_id, repository_id, name, path, kind, version FROM components WHERE id = ?`, id,
	).Scan(&projectID, &repositoryID, &name, &path, &kind, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Component{}, fmt.Errorf("%w: component %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return project.Component{}, MapSQLiteError(fmt.Errorf("load component: %w", err))
	}
	return project.Component{
		ID: project.ComponentID(id), ProjectID: project.ProjectID(projectID), RepositoryID: project.RepositoryID(repositoryID),
		Name: name, Path: path, Kind: kind, Version: version,
	}, nil
}

// --- ComponentPackAssignment ---

// AssignComponentPack implements ports.CatalogRepository (V3-01): it
// validates req.ComponentID exists and resolves that Component's own
// project_id from the row itself (ErrCrossProjectReference on mismatch,
// the same discipline CreateComponent's own Repository check follows),
// then appends a new row — never mutates or replaces an existing
// assignment (project.ComponentPackAssignment's own doc comment).
func (r catalogRepository) AssignComponentPack(ctx context.Context, req ports.AssignComponentPackRequest) (project.ComponentPackAssignment, error) {
	return assignComponentPackTx(ctx, r.tx, req)
}

func assignComponentPackTx(ctx context.Context, tx *sql.Tx, req ports.AssignComponentPackRequest) (project.ComponentPackAssignment, error) {
	created, err := project.NewComponentPackAssignment(
		project.ComponentPackAssignmentID(req.ID), project.ProjectID(req.ProjectID), project.ComponentID(req.ComponentID),
		project.PackVersionID(req.PackVersionID), req.EffectiveAt, req.Actor,
	)
	if err != nil {
		return project.ComponentPackAssignment{}, err
	}

	var componentProjectID string
	err = tx.QueryRowContext(ctx, `SELECT project_id FROM components WHERE id = ?`, req.ComponentID).Scan(&componentProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return project.ComponentPackAssignment{}, fmt.Errorf("%w: component %s", ports.ErrPersistenceNotFound, req.ComponentID)
	}
	if err != nil {
		return project.ComponentPackAssignment{}, MapSQLiteError(fmt.Errorf("resolve component pack assignment project: %w", err))
	}
	if componentProjectID != req.ProjectID {
		return project.ComponentPackAssignment{}, fmt.Errorf("%w: component %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, req.ComponentID, componentProjectID, req.ProjectID)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	effectiveAt := created.EffectiveAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO component_pack_assignments (id, project_id, component_id, pack_version_id, effective_at, actor, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(created.ID), string(created.ProjectID), string(created.ComponentID), string(created.PackVersionID),
		effectiveAt, created.Actor, now,
	); err != nil {
		return project.ComponentPackAssignment{}, MapSQLiteError(fmt.Errorf("assign component pack: %w", err))
	}
	return created, nil
}

// ListComponentPackAssignments implements ports.CatalogRepository,
// oldest-EffectiveAt-first.
func (r catalogRepository) ListComponentPackAssignments(ctx context.Context, componentID string) ([]project.ComponentPackAssignment, error) {
	return listComponentPackAssignmentsTx(ctx, r.tx, componentID)
}

func listComponentPackAssignmentsTx(ctx context.Context, tx *sql.Tx, componentID string) ([]project.ComponentPackAssignment, error) {
	// ORDER BY julianday(effective_at), not the raw TEXT column: two
	// RFC3339Nano timestamps with a different number of fractional-second
	// digits (time.Format trims trailing zeros, so "...:00Z" and
	// "...:00.5Z" are both real outputs) do not sort correctly as plain
	// strings — the same reason every lease/job comparison elsewhere in
	// this adapter (e.g. scheduling.go's "julianday(lease_until) >
	// julianday('now')") wraps timestamp columns in julianday() rather
	// than comparing the TEXT column directly.
	rows, err := tx.QueryContext(ctx, `
SELECT id, project_id, component_id, pack_version_id, effective_at, actor
FROM component_pack_assignments WHERE component_id = ? ORDER BY julianday(effective_at)`, componentID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list component pack assignments: %w", err))
	}
	defer rows.Close()

	var result []project.ComponentPackAssignment
	for rows.Next() {
		assignment, err := scanComponentPackAssignmentRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate component pack assignments: %w", err))
	}
	return result, nil
}

// GetEffectiveComponentPackAssignment implements ports.CatalogRepository:
// the assignment with the greatest effective_at that is still <= at —
// "resolved configuration không phải suy ngầm từ UI"
// (docs/design/01-system-design.md §6.2) made concrete as a query, rather
// than a caller guessing from ListComponentPackAssignments' own ordering.
func (r catalogRepository) GetEffectiveComponentPackAssignment(ctx context.Context, componentID string, at time.Time) (project.ComponentPackAssignment, error) {
	return getEffectiveComponentPackAssignmentTx(ctx, r.tx, componentID, at)
}

func getEffectiveComponentPackAssignmentTx(ctx context.Context, tx *sql.Tx, componentID string, at time.Time) (project.ComponentPackAssignment, error) {
	// julianday(effective_at) <= julianday(?), not a raw TEXT comparison
	// — see listComponentPackAssignmentsTx's own comment for exactly why
	// comparing RFC3339Nano text directly is unsafe.
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, component_id, pack_version_id, effective_at, actor
FROM component_pack_assignments
WHERE component_id = ? AND julianday(effective_at) <= julianday(?)
ORDER BY julianday(effective_at) DESC
LIMIT 1`, componentID, at.UTC().Format(time.RFC3339Nano))
	assignment, err := scanComponentPackAssignmentRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return project.ComponentPackAssignment{}, fmt.Errorf("%w: no component pack assignment for component %s effective at or before %s",
			ports.ErrPersistenceNotFound, componentID, at.UTC().Format(time.RFC3339Nano))
	}
	return assignment, err
}

func scanComponentPackAssignmentRow(row repositoryRowScanner) (project.ComponentPackAssignment, error) {
	var id, projectID, componentID, packVersionID, effectiveAtText, actor string
	if err := row.Scan(&id, &projectID, &componentID, &packVersionID, &effectiveAtText, &actor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return project.ComponentPackAssignment{}, err
		}
		return project.ComponentPackAssignment{}, MapSQLiteError(fmt.Errorf("scan component pack assignment row: %w", err))
	}
	effectiveAt, err := parseDBTime(effectiveAtText)
	if err != nil {
		return project.ComponentPackAssignment{}, err
	}
	return project.ComponentPackAssignment{
		ID: project.ComponentPackAssignmentID(id), ProjectID: project.ProjectID(projectID),
		ComponentID: project.ComponentID(componentID), PackVersionID: project.PackVersionID(packVersionID),
		EffectiveAt: effectiveAt, Actor: actor,
	}, nil
}
