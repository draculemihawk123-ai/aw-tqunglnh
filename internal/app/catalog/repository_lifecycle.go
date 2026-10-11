package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// V11-12 (docs/design/14-v11-bugfix.md, ADR-043): correct a wrongly registered repository, retire one, or remove one that never became ACTIVE.
// Every command is one transaction with one idempotent receipt, like RetryRepositoryProbe.

// Stable refusal codes. A refusal changes nothing; the code is what a client turns into words.
const (
	RefusalNotBlocked      = "REPOSITORY_NOT_BLOCKED"
	RefusalInUse           = "REPOSITORY_IN_USE"
	RefusalAlreadyDisabled = "REPOSITORY_ALREADY_DISABLED"
	RefusalProbeInProgress = "REPOSITORY_PROBE_IN_PROGRESS"
	RefusalWasActive       = "REPOSITORY_WAS_ACTIVE"
	RefusalReferenced      = "REPOSITORY_REFERENCED"
	RefusalDeleted         = "REPOSITORY_DELETED"
)

// Refusal is a typed "no" from a repository or project lifecycle command: Code is stable, Items names what stands in the way (the work items,
// workspaces, runs or tables that hold the thing).
type Refusal struct {
	Code    string
	Message string
	Items   []string
}

func (e *Refusal) Error() string {
	if len(e.Items) == 0 {
		return e.Code + ": " + e.Message
	}
	return e.Code + ": " + e.Message + " (" + strings.Join(e.Items, "; ") + ")"
}

// RepositoryRefusal is the name this type had before the project lifecycle shared it.
type RepositoryRefusal = Refusal

// RefusalCode returns the refusal code inside err, or "" when err is not a refusal (repository and project commands alike).
func RefusalCode(err error) string { return RepositoryRefusalCode(err) }

// RefusalItems returns what stands in the way, when err is a refusal that names it.
func RefusalItems(err error) []string { return RepositoryRefusalItems(err) }

// RepositoryRefusalCode returns the refusal code inside err, or "" when err is not a refusal.
func RepositoryRefusalCode(err error) string {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return ""
}

// RepositoryRefusalItems returns what stands in the way, when err is a refusal that names it.
func RepositoryRefusalItems(err error) []string {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Items
	}
	return nil
}

func refuse(code, message string, items ...string) error {
	return &Refusal{Code: code, Message: message, Items: items}
}

// loadRepositoryInProject reloads the repository and proves it belongs to projectID, the discipline every command here shares.
func loadRepositoryInProject(ctx context.Context, tx ports.Tx, repositoryID, projectID string) (project.Repository, error) {
	repo, err := tx.Catalog().GetRepository(ctx, repositoryID)
	if err != nil {
		return project.Repository{}, err
	}
	// V11-13: say "archived" before any business rule can answer with something less helpful.
	if owner, err := tx.Catalog().GetProject(ctx, string(repo.ProjectID)); err == nil && owner.Status == project.ProjectArchived {
		return project.Repository{}, apperror.ProjectArchived(string(repo.ProjectID))
	}
	if string(repo.ProjectID) != projectID {
		return project.Repository{}, fmt.Errorf("%w: repository %s belongs to project %s, not %s", ports.ErrCrossProjectReference, repositoryID, repo.ProjectID, projectID)
	}
	return repo, nil
}

func recordReceipt(ctx context.Context, tx ports.Tx, cmd ports.Command, result any) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal receipt result: %w", err)
	}
	return tx.Receipts().Record(ctx, ports.Receipt{
		Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
		CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON), CreatedAt: cmd.RequestedAt,
	})
}

// replay loads an earlier receipt; replayed reports one was found (result then holds it).
func replay(ctx context.Context, tx ports.Tx, cmd ports.Command, result any) (replayed bool, err error) {
	existing, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil || !found {
		return false, err
	}
	if existing.RequestHash != cmd.RequestHash {
		return false, ports.ErrReceiptConflict
	}
	return true, json.Unmarshal([]byte(existing.ResultJSON), result)
}

func enqueueProbe(ctx context.Context, tx ports.Tx, ids idsource.Source, cmd ports.Command, repositoryID, projectID string) (ports.DurableJob, error) {
	payload, err := json.Marshal(struct {
		RepositoryID string `json:"repositoryId"`
		ProjectID    string `json:"projectId"`
	}{RepositoryID: repositoryID, ProjectID: projectID})
	if err != nil {
		return ports.DurableJob{}, fmt.Errorf("marshal %s job payload: %w", RepositoryProbeJobKind, err)
	}
	return tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(ids.NewID()), ProjectID: project.ProjectID(projectID), Kind: RepositoryProbeJobKind,
		AggregateType: "Repository", AggregateID: repositoryID, Payload: payload,
		AvailableAt: cmd.RequestedAt, MaxClaims: defaultProbeJobMaxClaims, IdempotencyKey: cmd.IdempotencyKey + "-probe",
	})
}

// --- UpdateRepository ---

// UpdateRepositoryRequest is what a caller supplies to UpdateRepository. Name empty keeps the current name. cmd.ExpectedVersion is the version the
// caller saw.
type UpdateRepositoryRequest struct {
	RepositoryID  string
	ProjectID     string
	Name          string
	RemoteLocator string
	DefaultRef    string
}

// UpdateRepositoryResult is what UpdateRepository returns (and a replayed receipt reconstructs).
type UpdateRepositoryResult struct {
	RepositoryID string `json:"repositoryId"`
	ProjectID    string `json:"projectId"`
	Status       string `json:"status"`
	Version      uint64 `json:"version"`
	ProbeJobID   string `json:"probeJobId"`
}

// UpdateRepository corrects a BLOCKED repository's registration: new path / default ref (and optionally name), BLOCKED -> PROBING and a fresh probe
// job, in one transaction. A repository that is not BLOCKED is refused (REPOSITORY_NOT_BLOCKED): an ACTIVE one has workspaces and evidence tied to
// its path, so changing it means disabling it and registering a new one.
func UpdateRepository(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req UpdateRepositoryRequest) (UpdateRepositoryResult, error) {
	if strings.TrimSpace(req.RepositoryID) == "" || strings.TrimSpace(req.ProjectID) == "" {
		return UpdateRepositoryResult{}, errors.New("catalog: RepositoryID and ProjectID are required")
	}
	if cmd.ExpectedVersion == 0 {
		return UpdateRepositoryResult{}, errors.New("catalog: UpdateRepository requires cmd.ExpectedVersion (the Repository version the caller observed)")
	}
	var result UpdateRepositoryResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replay(ctx, tx, cmd, &result); err != nil || replayed {
			return err
		}
		current, err := loadRepositoryInProject(ctx, tx, req.RepositoryID, req.ProjectID)
		if err != nil {
			return err
		}
		if current.Status != project.RepositoryBlocked {
			return refuse(RefusalNotBlocked, fmt.Sprintf("repository %s is %s; only a BLOCKED repository can be corrected (disable an ACTIVE one and register a new one)", req.RepositoryID, current.Status))
		}
		updated, err := tx.Catalog().UpdateRepository(ctx, ports.UpdateRepositoryRequest{
			RepositoryID: req.RepositoryID, ExpectedVersion: cmd.ExpectedVersion,
			Name: req.Name, RemoteLocator: req.RemoteLocator, DefaultRef: req.DefaultRef,
		})
		if err != nil {
			return err
		}
		job, err := enqueueProbe(ctx, tx, ids, cmd, req.RepositoryID, req.ProjectID)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(repositoryUpdatedEventPayload{
			RepositoryID: req.RepositoryID, ProjectID: req.ProjectID, Name: updated.Name,
			RemoteLocator: updated.RemoteLocator, DefaultRef: updated.DefaultRef,
			PreviousName: current.Name, PreviousRemoteLocator: current.RemoteLocator, PreviousDefaultRef: current.DefaultRef,
			Status: string(updated.Status), ProbeJobID: string(job.ID),
		})
		if err != nil {
			return fmt.Errorf("marshal RepositoryUpdated payload: %w", err)
		}
		// Like RepositoryProbeRetried: the repository's own stream already uses Sequence 1, so the event is keyed by this command's id.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-updated", ProjectID: req.ProjectID, AggregateType: "RepositoryChange", AggregateID: cmd.ID, Sequence: 1,
			EventType: RepositoryUpdatedEventType, SchemaVersion: RepositoryUpdatedSchemaVersion, PayloadJSON: string(payload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}
		result = UpdateRepositoryResult{
			RepositoryID: req.RepositoryID, ProjectID: req.ProjectID, Status: string(updated.Status), Version: updated.Version, ProbeJobID: string(job.ID),
		}
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}

// --- DisableRepository ---

// DisableRepositoryRequest is what a caller supplies to DisableRepository.
type DisableRepositoryRequest struct {
	RepositoryID string
	ProjectID    string
	Reason       string
}

// DisableRepositoryResult is what DisableRepository returns.
type DisableRepositoryResult struct {
	RepositoryID   string `json:"repositoryId"`
	ProjectID      string `json:"projectId"`
	Status         string `json:"status"`
	Version        uint64 `json:"version"`
	PreviousStatus string `json:"previousStatus"`
}

// DisableRepository retires a repository: ACTIVE -> DISABLED, or BLOCKED -> DISABLED (a wrong registration leaving). It is refused while anything
// still depends on it, naming each holder, and while a probe is in flight. A disabled repository keeps its history and evidence and gives its name
// back, so the same name can be registered again.
func DisableRepository(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req DisableRepositoryRequest) (DisableRepositoryResult, error) {
	if strings.TrimSpace(req.RepositoryID) == "" || strings.TrimSpace(req.ProjectID) == "" {
		return DisableRepositoryResult{}, errors.New("catalog: RepositoryID and ProjectID are required")
	}
	if cmd.ExpectedVersion == 0 {
		return DisableRepositoryResult{}, errors.New("catalog: DisableRepository requires cmd.ExpectedVersion (the Repository version the caller observed)")
	}
	var result DisableRepositoryResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replay(ctx, tx, cmd, &result); err != nil || replayed {
			return err
		}
		current, err := loadRepositoryInProject(ctx, tx, req.RepositoryID, req.ProjectID)
		if err != nil {
			return err
		}
		switch current.Status {
		case project.RepositoryActive, project.RepositoryBlocked:
		case project.RepositoryDisabled:
			return refuse(RefusalAlreadyDisabled, fmt.Sprintf("repository %s is already disabled", req.RepositoryID))
		default:
			return refuse(RefusalProbeInProgress, fmt.Sprintf("repository %s is %s: wait for the probe to finish, then disable it", req.RepositoryID, current.Status))
		}
		holders, err := tx.Catalog().ListRepositoryHolders(ctx, req.RepositoryID)
		if err != nil {
			return err
		}
		if len(holders) > 0 {
			items := make([]string, 0, len(holders))
			for _, holder := range holders {
				items = append(items, holder.Kind+" "+holder.ID+" — "+holder.Detail)
			}
			return refuse(RefusalInUse, fmt.Sprintf("repository %s is still used by %d unfinished item(s): finish or cancel them first", req.RepositoryID, len(holders)), items...)
		}
		updated, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: req.RepositoryID, ExpectedStatus: current.Status, ExpectedVersion: cmd.ExpectedVersion,
			NextStatus: project.RepositoryDisabled, LastProbeErrorCode: current.LastProbeErrorCode,
		})
		if err != nil {
			return err
		}
		payload, err := json.Marshal(repositoryDisabledEventPayload{
			RepositoryID: req.RepositoryID, ProjectID: req.ProjectID, Name: current.Name, PreviousStatus: string(current.Status), Reason: req.Reason, Actor: cmd.Actor,
		})
		if err != nil {
			return fmt.Errorf("marshal RepositoryDisabled payload: %w", err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-disabled", ProjectID: req.ProjectID, AggregateType: "RepositoryChange", AggregateID: cmd.ID, Sequence: 1,
			EventType: RepositoryDisabledEventType, SchemaVersion: RepositoryDisabledSchemaVersion, PayloadJSON: string(payload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}
		result = DisableRepositoryResult{
			RepositoryID: req.RepositoryID, ProjectID: req.ProjectID, Status: string(updated.Status), Version: updated.Version, PreviousStatus: string(current.Status),
		}
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}

// --- DeleteRepository ---

// DeleteRepositoryRequest is what a caller supplies to DeleteRepository. Reason is required: the deletion is permanent and the event keeps it.
type DeleteRepositoryRequest struct {
	RepositoryID string
	ProjectID    string
	Reason       string
}

// DeleteRepositoryResult is what DeleteRepository returns.
type DeleteRepositoryResult struct {
	RepositoryID string `json:"repositoryId"`
	ProjectID    string `json:"projectId"`
	Name         string `json:"name"`
}

// DeleteRepository removes a repository that never became ACTIVE and that nothing references, with its probe history, and leaves a tombstone so the id
// is never reused. All checks run in the same transaction as the delete. A repository that was ever ACTIVE (even one now DISABLED) is refused: it
// keeps its history and evidence and can only be disabled.
func DeleteRepository(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req DeleteRepositoryRequest) (DeleteRepositoryResult, error) {
	if strings.TrimSpace(req.RepositoryID) == "" || strings.TrimSpace(req.ProjectID) == "" {
		return DeleteRepositoryResult{}, errors.New("catalog: RepositoryID and ProjectID are required")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return DeleteRepositoryResult{}, errors.New("catalog: a reason is required to delete a repository")
	}
	if cmd.ExpectedVersion == 0 {
		return DeleteRepositoryResult{}, errors.New("catalog: DeleteRepository requires cmd.ExpectedVersion (the Repository version the caller observed)")
	}
	var result DeleteRepositoryResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, err := replay(ctx, tx, cmd, &result); err != nil || replayed {
			return err
		}
		current, err := loadRepositoryInProject(ctx, tx, req.RepositoryID, req.ProjectID)
		if err != nil {
			return err
		}
		switch current.Status {
		case project.RepositoryRegistering, project.RepositoryProbing, project.RepositoryBlocked:
		default:
			return refuse(RefusalWasActive, fmt.Sprintf("repository %s is %s: only a repository that never became ACTIVE can be deleted; disable this one instead", req.RepositoryID, current.Status))
		}
		everActive, err := tx.Catalog().RepositoryEverActive(ctx, req.RepositoryID)
		if err != nil {
			return err
		}
		if everActive {
			return refuse(RefusalWasActive, fmt.Sprintf("repository %s was ACTIVE once: it keeps its history and can only be disabled", req.RepositoryID))
		}
		references, err := tx.Catalog().ListRepositoryReferences(ctx, req.RepositoryID)
		if err != nil {
			return err
		}
		if len(references) > 0 {
			items := make([]string, 0, len(references))
			for _, reference := range references {
				items = append(items, fmt.Sprintf("%s: %d row(s)", reference.Table, reference.Count))
			}
			return refuse(RefusalReferenced, fmt.Sprintf("repository %s is referenced and cannot be deleted", req.RepositoryID), items...)
		}
		if err := tx.Catalog().DeleteRepository(ctx, ports.DeleteRepositoryRequest{
			RepositoryID: req.RepositoryID, ExpectedVersion: cmd.ExpectedVersion, Reason: req.Reason, Actor: cmd.Actor, DeletedAt: cmd.RequestedAt,
		}); err != nil {
			if errors.Is(err, ports.ErrRepositoryReferenced) {
				return refuse(RefusalProbeInProgress, err.Error())
			}
			return err
		}
		payload, err := json.Marshal(repositoryDeletedEventPayload{
			RepositoryID: req.RepositoryID, ProjectID: req.ProjectID, Name: current.Name, RemoteLocator: current.RemoteLocator,
			PreviousStatus: string(current.Status), Reason: req.Reason, Actor: cmd.Actor,
		})
		if err != nil {
			return fmt.Errorf("marshal RepositoryDeleted payload: %w", err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-deleted", ProjectID: req.ProjectID, AggregateType: "RepositoryChange", AggregateID: cmd.ID, Sequence: 1,
			EventType: RepositoryDeletedEventType, SchemaVersion: RepositoryDeletedSchemaVersion, PayloadJSON: string(payload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}
		result = DeleteRepositoryResult{RepositoryID: req.RepositoryID, ProjectID: req.ProjectID, Name: current.Name}
		return recordReceipt(ctx, tx, cmd, result)
	})
	return result, err
}
