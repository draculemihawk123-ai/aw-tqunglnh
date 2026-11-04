package ports

import (
	"context"
	"time"
)

// ProjectSettingCommitMessageTemplate (V12-25, ADR-058) is the default commit message of an approval-bound commit.
const ProjectSettingCommitMessageTemplate = "commit_message_template"

// ProjectSettingTaskDocuments (V12-33, ADR-065) is where the kit's per-task documents (frame.md, plan.md) live: REPO or AW.
const ProjectSettingTaskDocuments = "task_documents"

// ProjectSetting is one per-project operator setting.
type ProjectSetting struct {
	ProjectID string
	Key       string
	Value     string
	Version   uint64
	UpdatedBy string
	UpdatedAt time.Time
}

// ProjectSettingsRepository is the Tx accessor of per-project settings.
type ProjectSettingsRepository interface {
	// GetProjectSetting returns the setting, or ErrPersistenceNotFound.
	GetProjectSetting(ctx context.Context, projectID, key string) (ProjectSetting, error)
	// PutProjectSetting inserts (expectedVersion 0) or replaces (the stored version; ErrOptimisticConflict otherwise) the setting.
	PutProjectSetting(ctx context.Context, setting ProjectSetting, expectedVersion uint64) (ProjectSetting, error)
	// DeleteProjectSetting removes the setting (no error when there is none).
	DeleteProjectSetting(ctx context.Context, projectID, key string) error
}
