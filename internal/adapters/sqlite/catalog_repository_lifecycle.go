package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// V11-12 (ADR-043): correct, retire and remove a repository. The lifecycle commands (internal/app/catalog) compose these in one transaction.

// UpdateRepository implements ports.CatalogRepository.
func (r catalogRepository) UpdateRepository(ctx context.Context, req ports.UpdateRepositoryRequest) (project.Repository, error) {
	return updateRepositoryTx(ctx, r.tx, req)
}

func updateRepositoryTx(ctx context.Context, tx *sql.Tx, req ports.UpdateRepositoryRequest) (project.Repository, error) {
	current, err := getRepositoryTx(ctx, tx, req.RepositoryID)
	if err != nil {
		return project.Repository{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = current.Name
	}
	// The same validation a registration gets: a blank locator or ref is refused before anything is written.
	validated, err := project.NewRepository(current.ID, current.ProjectID, name, req.RemoteLocator, req.DefaultRef)
	if err != nil {
		return project.Repository{}, err
	}
	if err := project.CanTransitionRepositoryStatus(project.RepositoryBlocked, project.RepositoryProbing); err != nil {
		return project.Repository{}, err
	}
	if validated.Name != current.Name {
		var taken int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM repositories WHERE project_id = ? AND name = ? AND id <> ? AND status <> 'DISABLED'`,
			string(current.ProjectID), validated.Name, req.RepositoryID).Scan(&taken)
		if err == nil {
			return project.Repository{}, fmt.Errorf("%w: repository name %q in project %s", ports.ErrPersistenceAlreadyExists, validated.Name, current.ProjectID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return project.Repository{}, MapSQLiteError(fmt.Errorf("check repository name: %w", err))
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
UPDATE repositories
SET name = ?, local_path = ?, default_ref = ?, status = 'PROBING', last_probe_error_code = NULL, version = version + 1, updated_at = ?
WHERE id = ? AND status = 'BLOCKED' AND version = ?`,
		validated.Name, validated.RemoteLocator, validated.DefaultRef, now, req.RepositoryID, req.ExpectedVersion)
	if err != nil {
		return project.Repository{}, mapAlreadyExists(fmt.Errorf("update repository %s: %w", req.RepositoryID, err), "repository "+req.RepositoryID)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return project.Repository{}, MapSQLiteError(fmt.Errorf("read repository update result: %w", err))
	}
	if affected != 1 {
		return project.Repository{}, fmt.Errorf("%w: repository %s expected BLOCKED@%d, is %s@%d",
			ports.ErrOptimisticConflict, req.RepositoryID, req.ExpectedVersion, current.Status, current.Version)
	}
	return getRepositoryTx(ctx, tx, req.RepositoryID)
}

// ListRepositoryHolders implements ports.CatalogRepository.
func (r catalogRepository) ListRepositoryHolders(ctx context.Context, repositoryID string) ([]ports.RepositoryHolder, error) {
	return listRepositoryHoldersTx(ctx, r.tx, repositoryID)
}

func listRepositoryHoldersTx(ctx context.Context, tx *sql.Tx, repositoryID string) ([]ports.RepositoryHolder, error) {
	queries := []struct{ kind, query string }{
		{"WORK_ITEM", `
SELECT DISTINCT w.id, w.title || ' (' || w.status || ')'
FROM work_item_effective_scopes s JOIN work_items w ON w.id = s.work_item_id
WHERE s.repository_id = ? AND w.status NOT IN ('DONE','CANCELLED') ORDER BY w.id`},
		{"TASK_FAMILY", `
SELECT DISTINCT f.id, 'family of ' || f.root_work_item_id || ' (' || f.status || ')'
FROM family_repository_scopes s JOIN task_families f ON f.id = s.family_id
WHERE s.repository_id = ? AND f.status NOT IN ('COMPLETED','CANCELLED') ORDER BY f.id`},
		{"WORKSPACE", `
SELECT id, 'workspace ' || state FROM repository_workspaces
WHERE repository_id = ? AND state NOT IN ('RELEASED','FAILED') ORDER BY id`},
		{"RELEASE_SET", `
SELECT DISTINCT rs.id, 'release set ' || rs.state
FROM release_set_repositories rr JOIN release_sets rs ON rs.id = rr.release_set_id
WHERE rr.repository_id = ? AND rs.state = 'CREATED' ORDER BY rs.id`},
	}
	var holders []ports.RepositoryHolder
	for _, q := range queries {
		rows, err := tx.QueryContext(ctx, q.query, repositoryID)
		if err != nil {
			return nil, MapSQLiteError(fmt.Errorf("list %s holders of repository %s: %w", q.kind, repositoryID, err))
		}
		for rows.Next() {
			var id, detail string
			if err := rows.Scan(&id, &detail); err != nil {
				rows.Close()
				return nil, MapSQLiteError(fmt.Errorf("scan %s holder: %w", q.kind, err))
			}
			holders = append(holders, ports.RepositoryHolder{Kind: q.kind, ID: id, Detail: detail})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, MapSQLiteError(fmt.Errorf("iterate %s holders: %w", q.kind, err))
		}
	}
	return holders, nil
}

// RepositoryEverActive implements ports.CatalogRepository.
func (r catalogRepository) RepositoryEverActive(ctx context.Context, repositoryID string) (bool, error) {
	var found int
	err := r.tx.QueryRowContext(ctx,
		`SELECT 1 FROM repository_probe_attempts WHERE repository_id = ? AND result = 'ACTIVE' LIMIT 1`, repositoryID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, MapSQLiteError(fmt.Errorf("check repository %s was ever active: %w", repositoryID, err))
	}
	return true, nil
}

// ListRepositoryReferences implements ports.CatalogRepository.
func (r catalogRepository) ListRepositoryReferences(ctx context.Context, repositoryID string) ([]ports.RepositoryReference, error) {
	return listRepositoryReferencesTx(ctx, r.tx, repositoryID)
}

func listRepositoryReferencesTx(ctx context.Context, tx *sql.Tx, repositoryID string) ([]ports.RepositoryReference, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT m.name, f."from"
FROM sqlite_master m, pragma_foreign_key_list(m.name) f
WHERE m.type = 'table' AND f."table" = 'repositories' AND m.name <> 'repository_probe_attempts'
ORDER BY m.name, f."from"`)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("find tables referencing repositories: %w", err))
	}
	type column struct{ table, name string }
	var columns []column
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.table, &c.name); err != nil {
			rows.Close()
			return nil, MapSQLiteError(fmt.Errorf("scan referencing table: %w", err))
		}
		columns = append(columns, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate referencing tables: %w", err))
	}

	counts := map[string]int{}
	for _, c := range columns {
		var n int
		// Table and column names come from sqlite_master, never from a caller.
		query := fmt.Sprintf(`SELECT COUNT(*) FROM %q WHERE %q = ?`, c.table, c.name)
		if err := tx.QueryRowContext(ctx, query, repositoryID).Scan(&n); err != nil {
			return nil, MapSQLiteError(fmt.Errorf("count %s rows referencing repository %s: %w", c.table, repositoryID, err))
		}
		if n > 0 {
			counts[c.table] += n
		}
	}
	tables := make([]string, 0, len(counts))
	for table := range counts {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	references := make([]ports.RepositoryReference, 0, len(tables))
	for _, table := range tables {
		references = append(references, ports.RepositoryReference{Table: table, Count: counts[table]})
	}
	return references, nil
}

// DeleteRepository implements ports.CatalogRepository.
func (r catalogRepository) DeleteRepository(ctx context.Context, req ports.DeleteRepositoryRequest) error {
	return deleteRepositoryTx(ctx, r.tx, req)
}

func deleteRepositoryTx(ctx context.Context, tx *sql.Tx, req ports.DeleteRepositoryRequest) error {
	current, err := getRepositoryTx(ctx, tx, req.RepositoryID)
	if err != nil {
		return err
	}
	if current.Version != req.ExpectedVersion {
		return fmt.Errorf("%w: repository %s expected version %d, is %d", ports.ErrOptimisticConflict, req.RepositoryID, req.ExpectedVersion, current.Version)
	}
	references, err := listRepositoryReferencesTx(ctx, tx, req.RepositoryID)
	if err != nil {
		return err
	}
	if len(references) > 0 {
		parts := make([]string, 0, len(references))
		for _, reference := range references {
			parts = append(parts, fmt.Sprintf("%s=%d", reference.Table, reference.Count))
		}
		return fmt.Errorf("%w: repository %s (%s)", ports.ErrRepositoryReferenced, req.RepositoryID, strings.Join(parts, ", "))
	}
	var running int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM durable_jobs WHERE aggregate_type = 'Repository' AND aggregate_id = ? AND state = 'LEASED'`,
		req.RepositoryID).Scan(&running); err != nil {
		return MapSQLiteError(fmt.Errorf("check running probe of repository %s: %w", req.RepositoryID, err))
	}
	if running > 0 {
		return fmt.Errorf("%w: repository %s has a probe running", ports.ErrRepositoryReferenced, req.RepositoryID)
	}

	now := req.DeletedAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `DELETE FROM repository_probe_attempts WHERE repository_id = ?`, req.RepositoryID); err != nil {
		return MapSQLiteError(fmt.Errorf("delete probe history of repository %s: %w", req.RepositoryID, err))
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE durable_jobs SET state = 'CANCELLED', updated_at = ?, version = version + 1
WHERE aggregate_type = 'Repository' AND aggregate_id = ? AND state IN ('AVAILABLE','FAILED')`, now, req.RepositoryID); err != nil {
		return MapSQLiteError(fmt.Errorf("cancel probe jobs of repository %s: %w", req.RepositoryID, err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO deleted_repositories (id, project_id, name, local_path, reason, actor, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		req.RepositoryID, string(current.ProjectID), current.Name, current.RemoteLocator, req.Reason, req.Actor, now); err != nil {
		return MapSQLiteError(fmt.Errorf("record deleted repository %s: %w", req.RepositoryID, err))
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM repositories WHERE id = ? AND version = ?`, req.RepositoryID, req.ExpectedVersion)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("delete repository %s: %w", req.RepositoryID, err))
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("%w: repository %s changed while it was being deleted", ports.ErrOptimisticConflict, req.RepositoryID)
	}
	return nil
}

// repositoryIDDeleted reports whether a tombstone exists for id.
func repositoryIDDeleted(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM deleted_repositories WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, MapSQLiteError(fmt.Errorf("check deleted repository %s: %w", id, err))
	}
	return true, nil
}
