package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// runAppRepository implements ports.RunAppRepository (V12-30, migration 0063).
type runAppRepository struct {
	tx *sql.Tx
}

func (r runAppRepository) GetRunAppProfile(ctx context.Context, repositoryID string) (ports.RunAppProfileRecord, error) {
	var p ports.RunAppProfileRecord
	var updatedAt string
	err := r.tx.QueryRowContext(ctx, `SELECT repository_id, project_id, document, version, updated_by, updated_at FROM run_app_profiles WHERE repository_id = ?`,
		repositoryID).Scan(&p.RepositoryID, &p.ProjectID, &p.Document, &p.Version, &p.UpdatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.RunAppProfileRecord{}, fmt.Errorf("%w: run-app profile of %s", ports.ErrPersistenceNotFound, repositoryID)
	}
	if err != nil {
		return ports.RunAppProfileRecord{}, MapSQLiteError(err)
	}
	p.UpdatedAt, err = parseWorkflowTime(updatedAt)
	return p, err
}

func (r runAppRepository) PutRunAppProfile(ctx context.Context, p ports.RunAppProfileRecord, expectedVersion uint64) (ports.RunAppProfileRecord, error) {
	existing, err := r.GetRunAppProfile(ctx, p.RepositoryID)
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		if expectedVersion != 0 {
			return ports.RunAppProfileRecord{}, fmt.Errorf("%w: run-app profile", ports.ErrOptimisticConflict)
		}
		p.Version = 1
		_, err = r.tx.ExecContext(ctx, `INSERT INTO run_app_profiles (repository_id, project_id, document, version, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			p.RepositoryID, p.ProjectID, p.Document, p.Version, p.UpdatedBy, formatWorkflowTime(p.UpdatedAt))
		return p, MapSQLiteError(err)
	case err != nil:
		return ports.RunAppProfileRecord{}, err
	}
	if existing.Version != expectedVersion {
		return ports.RunAppProfileRecord{}, fmt.Errorf("%w: run-app profile is at version %d", ports.ErrOptimisticConflict, existing.Version)
	}
	p.Version = existing.Version + 1
	_, err = r.tx.ExecContext(ctx, `UPDATE run_app_profiles SET document = ?, version = ?, updated_by = ?, updated_at = ? WHERE repository_id = ? AND version = ?`,
		p.Document, p.Version, p.UpdatedBy, formatWorkflowTime(p.UpdatedAt), p.RepositoryID, expectedVersion)
	return p, MapSQLiteError(err)
}

const sessionColumns = `id, project_id, repository_id, repository_workspace_id, state, profile_version, requested_by, worker_id, deadline_at, started_at,
	ended_at, problem, version, created_at`

func optionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatWorkflowTime(*t)
}

func scanSession(row repositoryRowScanner) (ports.RunAppSession, error) {
	var s ports.RunAppSession
	var state, deadline, created string
	var started, ended sql.NullString
	if err := row.Scan(&s.ID, &s.ProjectID, &s.RepositoryID, &s.RepositoryWorkspaceID, &state, &s.ProfileVersion, &s.RequestedBy, &s.WorkerID, &deadline,
		&started, &ended, &s.Problem, &s.Version, &created); err != nil {
		return ports.RunAppSession{}, err
	}
	s.State = ports.RunAppSessionState(state)
	var err error
	if s.DeadlineAt, err = parseWorkflowTime(deadline); err != nil {
		return ports.RunAppSession{}, err
	}
	if s.CreatedAt, err = parseWorkflowTime(created); err != nil {
		return ports.RunAppSession{}, err
	}
	for _, pair := range []struct {
		raw sql.NullString
		dst **time.Time
	}{{started, &s.StartedAt}, {ended, &s.EndedAt}} {
		if pair.raw.Valid {
			t, err := parseWorkflowTime(pair.raw.String)
			if err != nil {
				return ports.RunAppSession{}, err
			}
			*pair.dst = &t
		}
	}
	return s, nil
}

func (r runAppRepository) CreateRunAppSession(ctx context.Context, s ports.RunAppSession) (ports.RunAppSession, error) {
	s.Version = 1
	_, err := r.tx.ExecContext(ctx, `INSERT INTO run_app_sessions (`+sessionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.ProjectID, s.RepositoryID, s.RepositoryWorkspaceID, string(s.State), s.ProfileVersion, s.RequestedBy, s.WorkerID,
		formatWorkflowTime(s.DeadlineAt), optionalTime(s.StartedAt), optionalTime(s.EndedAt), s.Problem, s.Version, formatWorkflowTime(s.CreatedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ports.RunAppSession{}, fmt.Errorf("%w: repository %s already has a running app session", ports.ErrOptimisticConflict, s.RepositoryID)
	}
	return s, MapSQLiteError(err)
}

func (r runAppRepository) GetRunAppSession(ctx context.Context, id string) (ports.RunAppSession, error) {
	s, err := scanSession(r.tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM run_app_sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ports.RunAppSession{}, fmt.Errorf("%w: run-app session %s", ports.ErrPersistenceNotFound, id)
	}
	return s, MapSQLiteError(err)
}

func (r runAppRepository) LatestRunAppSession(ctx context.Context, repositoryWorkspaceID string) (ports.RunAppSession, error) {
	s, err := scanSession(r.tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM run_app_sessions WHERE repository_workspace_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, repositoryWorkspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return ports.RunAppSession{}, fmt.Errorf("%w: run-app session of %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
	}
	return s, MapSQLiteError(err)
}

func (r runAppRepository) ListRunAppSessionsInStates(ctx context.Context, states []ports.RunAppSessionState) ([]ports.RunAppSession, error) {
	if len(states) == 0 {
		return nil, nil
	}
	args := make([]any, len(states))
	marks := make([]string, len(states))
	for i, st := range states {
		args[i], marks[i] = string(st), "?"
	}
	rows, err := r.tx.QueryContext(ctx, `SELECT `+sessionColumns+` FROM run_app_sessions WHERE state IN (`+strings.Join(marks, ",")+`) ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, MapSQLiteError(err)
	}
	defer rows.Close()
	var out []ports.RunAppSession
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, MapSQLiteError(err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r runAppRepository) UpdateRunAppSession(ctx context.Context, s ports.RunAppSession, expectedVersion uint64) (ports.RunAppSession, error) {
	s.Version = expectedVersion + 1
	res, err := r.tx.ExecContext(ctx, `UPDATE run_app_sessions SET state = ?, worker_id = ?, deadline_at = ?, started_at = ?, ended_at = ?, problem = ?, version = ?
WHERE id = ? AND version = ?`, string(s.State), s.WorkerID, formatWorkflowTime(s.DeadlineAt), optionalTime(s.StartedAt), optionalTime(s.EndedAt), s.Problem,
		s.Version, s.ID, expectedVersion)
	if err != nil {
		return ports.RunAppSession{}, MapSQLiteError(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ports.RunAppSession{}, fmt.Errorf("%w: run-app session %s", ports.ErrOptimisticConflict, s.ID)
	}
	return s, nil
}
