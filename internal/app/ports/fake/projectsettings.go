package fake

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ProjectSettingsRepository is the in-memory ports.ProjectSettingsRepository (V12-25).
type ProjectSettingsRepository struct {
	settings map[string]ports.ProjectSetting
}

func settingKey(projectID, key string) string { return projectID + "\x00" + key }

func (r *ProjectSettingsRepository) clone() *ProjectSettingsRepository {
	settings := make(map[string]ports.ProjectSetting, len(r.settings))
	for k, v := range r.settings {
		settings[k] = v
	}
	return &ProjectSettingsRepository{settings: settings}
}

// GetProjectSetting mirrors the SQLite repository.
func (r *ProjectSettingsRepository) GetProjectSetting(_ context.Context, projectID, key string) (ports.ProjectSetting, error) {
	s, ok := r.settings[settingKey(projectID, key)]
	if !ok {
		return ports.ProjectSetting{}, fmt.Errorf("fake: %w: setting %s", ports.ErrPersistenceNotFound, key)
	}
	return s, nil
}

// PutProjectSetting mirrors the SQLite repository.
func (r *ProjectSettingsRepository) PutProjectSetting(_ context.Context, s ports.ProjectSetting, expectedVersion uint64) (ports.ProjectSetting, error) {
	if r.settings == nil {
		r.settings = map[string]ports.ProjectSetting{}
	}
	existing, ok := r.settings[settingKey(s.ProjectID, s.Key)]
	if (!ok && expectedVersion != 0) || (ok && existing.Version != expectedVersion) {
		return ports.ProjectSetting{}, fmt.Errorf("fake: %w: setting %s", ports.ErrOptimisticConflict, s.Key)
	}
	s.Version = expectedVersion + 1
	r.settings[settingKey(s.ProjectID, s.Key)] = s
	return s, nil
}

// DeleteProjectSetting mirrors the SQLite repository.
func (r *ProjectSettingsRepository) DeleteProjectSetting(_ context.Context, projectID, key string) error {
	delete(r.settings, settingKey(projectID, key))
	return nil
}
