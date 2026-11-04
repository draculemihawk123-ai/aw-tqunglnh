package catalog

// V12-33 (docs/design/15-v12-ui-operations.md, UX-45, ADR-065): where the kit's per-task working documents (frame.md, plan.md) live.
// REPO (the default, as in V10): docs/features/<feature>/tasks/<task>/ in the repository, committed with the task. AW: the worktree's
// aw-private directory .aw/features/<feature>/tasks/<task>/, which git ignores (never committed, never a scope violation), shown on
// the Workspace tab and exportable to the repository. The feature's lasting documents (02-spec.md, 03-design.md, tasks.json) always
// stay in the repository.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// Task document locations.
const (
	TaskDocumentsRepo = "REPO"
	TaskDocumentsAw   = "AW"
)

// ErrInvalidTaskDocuments: the location is neither REPO nor AW.
var ErrInvalidTaskDocuments = errors.New("catalog: the task documents location is REPO or AW")

// TaskDocumentsView is the project's setting and the directory a task's documents go to.
type TaskDocumentsView struct {
	ProjectID string `json:"projectId"`
	Location  string `json:"location"`
	// DirectoryPattern is where the documents of task <task> of feature <feature> are written.
	DirectoryPattern string `json:"directoryPattern"`
	Version          uint64 `json:"version,omitempty"`
}

// TaskDocumentsDirectory is the directory of a task's documents for a location.
func TaskDocumentsDirectory(location, feature, task string) string {
	if location == TaskDocumentsAw {
		return ".aw/features/" + feature + "/tasks/" + task
	}
	return "docs/features/" + feature + "/tasks/" + task
}

func taskDocumentsView(projectID, location string, version uint64) TaskDocumentsView {
	return TaskDocumentsView{ProjectID: projectID, Location: location, Version: version,
		DirectoryPattern: TaskDocumentsDirectory(location, "<feature>", "<task>")}
}

// GetTaskDocuments reads the project's setting (REPO when none is set).
func GetTaskDocuments(ctx context.Context, uow ports.UnitOfWork, projectID string) (TaskDocumentsView, error) {
	view := taskDocumentsView(projectID, TaskDocumentsRepo, 0)
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().GetProject(ctx, projectID); err != nil {
			return err
		}
		s, err := tx.ProjectSettings().GetProjectSetting(ctx, projectID, ports.ProjectSettingTaskDocuments)
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		view = taskDocumentsView(projectID, s.Value, s.Version)
		return nil
	})
	return view, err
}

// SetTaskDocuments sets the project's location, idempotently by cmd's receipt. A running task keeps the directory its contract names.
func SetTaskDocuments(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, projectID, location string) (TaskDocumentsView, error) {
	location = strings.ToUpper(strings.TrimSpace(location))
	if location != TaskDocumentsRepo && location != TaskDocumentsAw {
		return TaskDocumentsView{}, ErrInvalidTaskDocuments
	}
	var result TaskDocumentsView
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replay(ctx, tx, cmd, &result); err != nil || replayed {
			return err
		}
		current, err := loadProject(ctx, tx, cmd.Scope, projectID)
		if err != nil {
			return err
		}
		if current.Status == project.ProjectArchived {
			return apperror.ProjectArchived(projectID)
		}
		var expected uint64
		existing, err := tx.ProjectSettings().GetProjectSetting(ctx, projectID, ports.ProjectSettingTaskDocuments)
		switch {
		case err == nil:
			expected = existing.Version
		case !errors.Is(err, ports.ErrPersistenceNotFound):
			return err
		}
		stored, err := tx.ProjectSettings().PutProjectSetting(ctx, ports.ProjectSetting{
			ProjectID: projectID, Key: ports.ProjectSettingTaskDocuments, Value: location, UpdatedBy: cmd.Actor, UpdatedAt: cmd.RequestedAt,
		}, expected)
		if err != nil {
			return fmt.Errorf("store the task documents location: %w", err)
		}
		result = taskDocumentsView(projectID, stored.Value, stored.Version)
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}
