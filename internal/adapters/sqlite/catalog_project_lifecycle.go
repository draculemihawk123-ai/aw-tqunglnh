package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// V11-13 (ADR-044): rename, archive and restore a project. An ARCHIVED project is read-only: every command that records a receipt in its scope is
// refused (guardProjectWritable below), and its background jobs are not claimed.

// RenameProject implements ports.CatalogRepository.
func (r catalogRepository) RenameProject(ctx context.Context, req ports.RenameProjectRequest) (project.Project, error) {
	name, err := project.NormalizeProjectName(req.Name)
	if err != nil {
		return project.Project{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := r.tx.ExecContext(ctx, `
UPDATE projects SET name = ?, version = version + 1, updated_at = ?
WHERE id = ? AND status = 'ACTIVE' AND version = ?`, name, now, req.ProjectID, req.ExpectedVersion)
	if err != nil {
		return project.Project{}, MapSQLiteError(fmt.Errorf("rename project %s: %w", req.ProjectID, err))
	}
	return r.afterProjectUpdate(ctx, result, req.ProjectID, fmt.Sprintf("expected ACTIVE@%d", req.ExpectedVersion))
}

// TransitionProjectStatus implements ports.CatalogRepository.
func (r catalogRepository) TransitionProjectStatus(ctx context.Context, req ports.TransitionProjectStatusRequest) (project.Project, error) {
	if err := project.CanTransitionProjectStatus(req.ExpectedStatus, req.NextStatus); err != nil {
		return project.Project{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := r.tx.ExecContext(ctx, `
UPDATE projects SET status = ?, version = version + 1, updated_at = ?
WHERE id = ? AND status = ? AND version = ?`, string(req.NextStatus), now, req.ProjectID, string(req.ExpectedStatus), req.ExpectedVersion)
	if err != nil {
		return project.Project{}, MapSQLiteError(fmt.Errorf("transition project %s: %w", req.ProjectID, err))
	}
	return r.afterProjectUpdate(ctx, result, req.ProjectID, fmt.Sprintf("expected %s@%d", req.ExpectedStatus, req.ExpectedVersion))
}

func (r catalogRepository) afterProjectUpdate(ctx context.Context, result sql.Result, projectID, expectation string) (project.Project, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return project.Project{}, MapSQLiteError(fmt.Errorf("read project update result: %w", err))
	}
	if affected != 1 {
		if _, err := getProjectTx(ctx, r.tx, projectID); err != nil {
			return project.Project{}, err
		}
		return project.Project{}, fmt.Errorf("%w: project %s %s", ports.ErrOptimisticConflict, projectID, expectation)
	}
	return getProjectTx(ctx, r.tx, projectID)
}

// ListProjectHolders implements ports.CatalogRepository.
func (r catalogRepository) ListProjectHolders(ctx context.Context, projectID string) ([]ports.ProjectHolder, error) {
	queries := []struct{ kind, query string }{
		{"WORKFLOW_RUN", `SELECT id, 'run ' || state || ' of work item ' || work_item_id FROM workflow_runs
WHERE project_id = ? AND state NOT IN ('SUCCEEDED','FAILED','CANCELLED') ORDER BY id`},
		{"WORK_ITEM", `SELECT id, title || ' (' || status || ')' FROM work_items
WHERE project_id = ? AND status IN ('ACTIVE','BLOCKED') ORDER BY id`},
		{"JOB", `SELECT id, kind || ' job holding a lease' FROM durable_jobs
WHERE project_id = ? AND state = 'LEASED' ORDER BY id`},
		{"RELEASE_SET", `SELECT id, 'release set ' || state FROM release_sets
WHERE project_id = ? AND state = 'CREATED' ORDER BY id`},
	}
	var holders []ports.ProjectHolder
	for _, q := range queries {
		rows, err := r.tx.QueryContext(ctx, q.query, projectID)
		if err != nil {
			return nil, MapSQLiteError(fmt.Errorf("list %s holders of project %s: %w", q.kind, projectID, err))
		}
		for rows.Next() {
			var id, detail string
			if err := rows.Scan(&id, &detail); err != nil {
				rows.Close()
				return nil, MapSQLiteError(fmt.Errorf("scan %s holder: %w", q.kind, err))
			}
			holders = append(holders, ports.ProjectHolder{Kind: q.kind, ID: id, Detail: detail})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, MapSQLiteError(fmt.Errorf("iterate %s holders: %w", q.kind, err))
		}
	}
	return holders, nil
}

// commandsAllowedInArchivedProject are the only commands that may record a receipt in an ARCHIVED project's scope: the two that archive and restore it.
var commandsAllowedInArchivedProject = map[string]bool{"ArchiveProject": true, "RestoreProject": true}

// guardProjectWritable refuses a receipt recorded in the scope of an ARCHIVED project. Every idempotent command ends by recording its receipt in the same
// transaction as its writes, so refusing here rolls the whole command back: one chokepoint for "an archived project is read-only", instead of a check in each
// of the commands. A scope with no project row (a fixture, an installation-wide command) is not guarded.
func guardProjectWritable(ctx context.Context, tx *sql.Tx, receipt ports.Receipt) error {
	projectID, ok := receipt.Scope.ProjectID()
	if !ok || commandsAllowedInArchivedProject[strings.TrimSpace(receipt.CommandType)] {
		return nil
	}
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM projects WHERE id = ?`, projectID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return MapSQLiteError(fmt.Errorf("check project %s is writable: %w", projectID, err))
	}
	if project.ProjectStatus(status) == project.ProjectArchived {
		return apperror.ProjectArchived(projectID)
	}
	return nil
}
