package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// forgeRepository implements ports.ForgeRepository (V12-28, migration 0061).
type forgeRepository struct {
	tx *sql.Tx
}

const remoteColumns = `repository_workspace_id, project_id, repository_id, remote, remote_branch, pushed_revision, pull_request_number,
	pull_request_url, base_branch, merged_revision, version, updated_by, updated_at`

func (r forgeRepository) GetRemoteBranch(ctx context.Context, repositoryWorkspaceID string) (ports.RemoteBranchRecord, error) {
	var rec ports.RemoteBranchRecord
	var updatedAt string
	err := r.tx.QueryRowContext(ctx, `SELECT `+remoteColumns+` FROM repository_workspace_remotes WHERE repository_workspace_id = ?`, repositoryWorkspaceID).Scan(
		&rec.RepositoryWorkspaceID, &rec.ProjectID, &rec.RepositoryID, &rec.Remote, &rec.RemoteBranch, &rec.PushedRevision, &rec.PullRequestNumber,
		&rec.PullRequestURL, &rec.BaseBranch, &rec.MergedRevision, &rec.Version, &rec.UpdatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.RemoteBranchRecord{}, fmt.Errorf("%w: remote branch of %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
	}
	if err != nil {
		return ports.RemoteBranchRecord{}, MapSQLiteError(fmt.Errorf("read remote branch: %w", err))
	}
	rec.UpdatedAt, err = parseWorkflowTime(updatedAt)
	return rec, err
}

func (r forgeRepository) PutRemoteBranch(ctx context.Context, rec ports.RemoteBranchRecord, expectedVersion uint64) (ports.RemoteBranchRecord, error) {
	existing, err := r.GetRemoteBranch(ctx, rec.RepositoryWorkspaceID)
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		if expectedVersion != 0 {
			return ports.RemoteBranchRecord{}, fmt.Errorf("%w: remote branch of %s", ports.ErrOptimisticConflict, rec.RepositoryWorkspaceID)
		}
		rec.Version = 1
		if _, err := r.tx.ExecContext(ctx, `INSERT INTO repository_workspace_remotes (`+remoteColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rec.RepositoryWorkspaceID, rec.ProjectID, rec.RepositoryID, rec.Remote, rec.RemoteBranch, rec.PushedRevision, rec.PullRequestNumber,
			rec.PullRequestURL, rec.BaseBranch, rec.MergedRevision, rec.Version, rec.UpdatedBy, formatWorkflowTime(rec.UpdatedAt)); err != nil {
			return ports.RemoteBranchRecord{}, MapSQLiteError(fmt.Errorf("insert remote branch: %w", err))
		}
		return rec, nil
	case err != nil:
		return ports.RemoteBranchRecord{}, err
	}
	if existing.Version != expectedVersion {
		return ports.RemoteBranchRecord{}, fmt.Errorf("%w: remote branch of %s is at version %d", ports.ErrOptimisticConflict, rec.RepositoryWorkspaceID, existing.Version)
	}
	rec.Version = existing.Version + 1
	if _, err := r.tx.ExecContext(ctx, `UPDATE repository_workspace_remotes SET remote = ?, remote_branch = ?, pushed_revision = ?, pull_request_number = ?,
	pull_request_url = ?, base_branch = ?, merged_revision = ?, version = ?, updated_by = ?, updated_at = ? WHERE repository_workspace_id = ? AND version = ?`,
		rec.Remote, rec.RemoteBranch, rec.PushedRevision, rec.PullRequestNumber, rec.PullRequestURL, rec.BaseBranch, rec.MergedRevision, rec.Version,
		rec.UpdatedBy, formatWorkflowTime(rec.UpdatedAt), rec.RepositoryWorkspaceID, expectedVersion); err != nil {
		return ports.RemoteBranchRecord{}, MapSQLiteError(fmt.Errorf("update remote branch: %w", err))
	}
	return rec, nil
}

func (r forgeRepository) ApprovedCommitRevisions(ctx context.Context, repositoryWorkspaceID string) ([]string, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT c.result_vcs_object_id FROM release_set_local_commits c
JOIN release_set_local_commit_selections s ON s.release_set_local_commit_id = c.id
WHERE c.repository_workspace_id = ? AND c.state = 'COMMITTED' AND c.result_vcs_object_id IS NOT NULL AND s.approval_request_id IS NOT NULL
ORDER BY c.completed_at`, repositoryWorkspaceID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list approved commits: %w", err))
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		out = append(out, sha)
	}
	return out, rows.Err()
}
