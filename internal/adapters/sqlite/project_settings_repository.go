package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// projectSettingsRepository implements ports.ProjectSettingsRepository over project_settings (V12-25, migration 0060).
type projectSettingsRepository struct {
	tx *sql.Tx
}

func (r projectSettingsRepository) GetProjectSetting(ctx context.Context, projectID, key string) (ports.ProjectSetting, error) {
	var s ports.ProjectSetting
	var updatedAt string
	err := r.tx.QueryRowContext(ctx, `SELECT project_id, key, value, version, updated_by, updated_at FROM project_settings WHERE project_id = ? AND key = ?`,
		projectID, key).Scan(&s.ProjectID, &s.Key, &s.Value, &s.Version, &s.UpdatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ProjectSetting{}, fmt.Errorf("%w: setting %s of project %s", ports.ErrPersistenceNotFound, key, projectID)
	}
	if err != nil {
		return ports.ProjectSetting{}, MapSQLiteError(fmt.Errorf("read project setting: %w", err))
	}
	s.UpdatedAt, err = parseWorkflowTime(updatedAt)
	return s, err
}

func (r projectSettingsRepository) PutProjectSetting(ctx context.Context, s ports.ProjectSetting, expectedVersion uint64) (ports.ProjectSetting, error) {
	existing, err := r.GetProjectSetting(ctx, s.ProjectID, s.Key)
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		if expectedVersion != 0 {
			return ports.ProjectSetting{}, fmt.Errorf("%w: setting %s", ports.ErrOptimisticConflict, s.Key)
		}
		s.Version = 1
		if _, err := r.tx.ExecContext(ctx, `INSERT INTO project_settings (project_id, key, value, version, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			s.ProjectID, s.Key, s.Value, s.Version, s.UpdatedBy, formatWorkflowTime(s.UpdatedAt)); err != nil {
			return ports.ProjectSetting{}, MapSQLiteError(fmt.Errorf("insert project setting: %w", err))
		}
		return s, nil
	case err != nil:
		return ports.ProjectSetting{}, err
	}
	if existing.Version != expectedVersion {
		return ports.ProjectSetting{}, fmt.Errorf("%w: setting %s is at version %d", ports.ErrOptimisticConflict, s.Key, existing.Version)
	}
	s.Version = existing.Version + 1
	if _, err := r.tx.ExecContext(ctx, `UPDATE project_settings SET value = ?, version = ?, updated_by = ?, updated_at = ? WHERE project_id = ? AND key = ? AND version = ?`,
		s.Value, s.Version, s.UpdatedBy, formatWorkflowTime(s.UpdatedAt), s.ProjectID, s.Key, expectedVersion); err != nil {
		return ports.ProjectSetting{}, MapSQLiteError(fmt.Errorf("update project setting: %w", err))
	}
	return s, nil
}

func (r projectSettingsRepository) DeleteProjectSetting(ctx context.Context, projectID, key string) error {
	if _, err := r.tx.ExecContext(ctx, `DELETE FROM project_settings WHERE project_id = ? AND key = ?`, projectID, key); err != nil {
		return MapSQLiteError(fmt.Errorf("delete project setting: %w", err))
	}
	return nil
}
