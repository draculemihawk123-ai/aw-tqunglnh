package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// V11-13 (docs/design/14-v11-bugfix.md, ADR-044): rename a project, archive it (instead of deleting it) and restore it. An ARCHIVED project is read-only:
// the persistence layer refuses every command that would record a receipt in its scope, and its jobs are not claimed.

// Stable refusal codes for the project lifecycle commands (same Refusal type as the repository ones).
const (
	RefusalProjectBusy            = "PROJECT_BUSY"
	RefusalProjectArchived        = apperror.ProjectArchivedCode
	RefusalProjectNotArchived     = "PROJECT_NOT_ARCHIVED"
	RefusalProjectAlreadyArchived = "PROJECT_ALREADY_ARCHIVED"
)

func loadProject(ctx context.Context, tx ports.Tx, scope ports.CommandScope, projectID string) (project.Project, error) {
	scopeProject, ok := scope.ProjectID()
	if !ok || scopeProject != projectID {
		return project.Project{}, fmt.Errorf("%w: command scope does not name project %s", ports.ErrScopeMismatch, projectID)
	}
	return tx.Catalog().GetProject(ctx, projectID)
}

// --- RenameProject ---

// RenameProjectRequest is what a caller supplies to RenameProject; cmd.ExpectedVersion is the version the caller saw.
type RenameProjectRequest struct {
	ProjectID string
	Name      string
}

// RenameProjectResult is what RenameProject returns (and a replayed receipt reconstructs).
type RenameProjectResult struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Version   uint64 `json:"version"`
}

// RenameProject gives an ACTIVE project a new name: the id does not change, names need not be unique. An ARCHIVED project is refused
// (PROJECT_ARCHIVED): restore it first.
func RenameProject(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req RenameProjectRequest) (RenameProjectResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return RenameProjectResult{}, errors.New("catalog: ProjectID is required")
	}
	if cmd.ExpectedVersion == 0 {
		return RenameProjectResult{}, errors.New("catalog: RenameProject requires cmd.ExpectedVersion (the Project version the caller observed)")
	}
	name, err := project.NormalizeProjectName(req.Name)
	if err != nil {
		return RenameProjectResult{}, err
	}
	var result RenameProjectResult
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replay(ctx, tx, cmd, &result); err != nil || replayed {
			return err
		}
		current, err := loadProject(ctx, tx, cmd.Scope, req.ProjectID)
		if err != nil {
			return err
		}
		if current.Status == project.ProjectArchived {
			return apperror.ProjectArchived(req.ProjectID)
		}
		updated, err := tx.Catalog().RenameProject(ctx, ports.RenameProjectRequest{ProjectID: req.ProjectID, ExpectedVersion: cmd.ExpectedVersion, Name: name})
		if err != nil {
			return err
		}
		payload, err := json.Marshal(projectRenamedEventPayload{ProjectID: req.ProjectID, PreviousName: current.Name, Name: updated.Name, Actor: cmd.Actor})
		if err != nil {
			return fmt.Errorf("marshal ProjectRenamed payload: %w", err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-renamed", ProjectID: req.ProjectID, AggregateType: "ProjectChange", AggregateID: cmd.ID, Sequence: 1,
			EventType: ProjectRenamedEventType, SchemaVersion: ProjectRenamedSchemaVersion, PayloadJSON: string(payload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}
		result = RenameProjectResult{ProjectID: req.ProjectID, Name: updated.Name, Status: string(updated.Status), Version: updated.Version}
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}

// --- ArchiveProject / RestoreProject ---

// ArchiveProjectRequest is what a caller supplies to ArchiveProject.
type ArchiveProjectRequest struct {
	ProjectID string
	Reason    string
}

// RestoreProjectRequest is what a caller supplies to RestoreProject.
type RestoreProjectRequest struct {
	ProjectID string
}

// ProjectStatusResult is what ArchiveProject and RestoreProject return.
type ProjectStatusResult struct {
	ProjectID      string `json:"projectId"`
	Status         string `json:"status"`
	PreviousStatus string `json:"previousStatus"`
	Version        uint64 `json:"version"`
}

// ArchiveProject makes a project read-only and hides it from the default list. It is refused (PROJECT_BUSY) while anything is still running or open in
// it, and the refusal lists each thing: unfinished runs, ACTIVE/BLOCKED work items, jobs holding a lease, open release sets. Nothing is cancelled for you.
// No data is deleted: evidence and history stay readable.
func ArchiveProject(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req ArchiveProjectRequest) (ProjectStatusResult, error) {
	return changeProjectStatus(ctx, uow, cmd, req.ProjectID, strings.TrimSpace(req.Reason), project.ProjectActive, project.ProjectArchived)
}

// RestoreProject makes an ARCHIVED project writable again, exactly as it was.
func RestoreProject(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req RestoreProjectRequest) (ProjectStatusResult, error) {
	return changeProjectStatus(ctx, uow, cmd, req.ProjectID, "", project.ProjectArchived, project.ProjectActive)
}

func changeProjectStatus(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, projectID, reason string, from, to project.ProjectStatus) (ProjectStatusResult, error) {
	if strings.TrimSpace(projectID) == "" {
		return ProjectStatusResult{}, errors.New("catalog: ProjectID is required")
	}
	if cmd.ExpectedVersion == 0 {
		return ProjectStatusResult{}, errors.New("catalog: " + string(cmd.Type) + " requires cmd.ExpectedVersion (the Project version the caller observed)")
	}
	var result ProjectStatusResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replay(ctx, tx, cmd, &result); err != nil || replayed {
			return err
		}
		current, err := loadProject(ctx, tx, cmd.Scope, projectID)
		if err != nil {
			return err
		}
		if current.Status != from {
			if to == project.ProjectArchived {
				return refuse(RefusalProjectAlreadyArchived, fmt.Sprintf("project %s is already archived", projectID))
			}
			return refuse(RefusalProjectNotArchived, fmt.Sprintf("project %s is not archived", projectID))
		}
		if to == project.ProjectArchived {
			holders, err := tx.Catalog().ListProjectHolders(ctx, projectID)
			if err != nil {
				return err
			}
			if len(holders) > 0 {
				items := make([]string, 0, len(holders))
				for _, holder := range holders {
					items = append(items, holder.Kind+" "+holder.ID+" — "+holder.Detail)
				}
				return refuse(RefusalProjectBusy, fmt.Sprintf("project %s still has %d running or open item(s): finish or cancel them, then archive it", projectID, len(holders)), items...)
			}
		}
		updated, err := tx.Catalog().TransitionProjectStatus(ctx, ports.TransitionProjectStatusRequest{
			ProjectID: projectID, ExpectedStatus: from, ExpectedVersion: cmd.ExpectedVersion, NextStatus: to,
		})
		if err != nil {
			return err
		}
		payload, err := json.Marshal(projectStatusChangedEventPayload{ProjectID: projectID, Name: current.Name, PreviousStatus: string(from), Status: string(to), Reason: reason, Actor: cmd.Actor})
		if err != nil {
			return fmt.Errorf("marshal project status event payload: %w", err)
		}
		// One Append per event type so the event catalog scan can prove each emitted key from a constant.
		if to == project.ProjectArchived {
			if err := tx.Events().Append(ctx, ports.DomainEvent{
				ID: cmd.ID + "-status", ProjectID: projectID, AggregateType: "ProjectChange", AggregateID: cmd.ID, Sequence: 1,
				EventType: ProjectArchivedEventType, SchemaVersion: ProjectArchivedSchemaVersion, PayloadJSON: string(payload),
				CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
			}); err != nil {
				return err
			}
		} else {
			if err := tx.Events().Append(ctx, ports.DomainEvent{
				ID: cmd.ID + "-status", ProjectID: projectID, AggregateType: "ProjectChange", AggregateID: cmd.ID, Sequence: 1,
				EventType: ProjectRestoredEventType, SchemaVersion: ProjectRestoredSchemaVersion, PayloadJSON: string(payload),
				CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
			}); err != nil {
				return err
			}
		}
		result = ProjectStatusResult{ProjectID: projectID, Status: string(updated.Status), PreviousStatus: string(from), Version: updated.Version}
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}
