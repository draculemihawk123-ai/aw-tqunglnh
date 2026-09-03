package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// repositoryWorkspaceColumns is shared by every reader of a
// repository_workspaces row across this package: the pre-existing
// Quarantine/Release/Recreate methods below (V3-09/V3-10/V3-11) and V3-06's
// own first-generation creation path in work.go. last_provision_error_code
// (V3-06) is appended at the end deliberately — RecreateRepositoryWorkspace's
// own INSERT below never lists it explicitly, so a recreated (successful)
// row simply gets it as NULL by SQLite's own column-default behavior,
// exactly correct for a row that was never a first-provision failure.
const repositoryWorkspaceColumns = `
id, workspace_set_id, repository_id, generation, locator, branch_ref,
base_revision, current_revision, state, version, last_provision_error_code`

// QuarantineRepositoryWorkspace is the fenced CAS transition from READY to
// QUARANTINED. It commits the state change and its correlated domain event
// in one transaction, guarded on (id, state=READY, version): a second caller
// racing on the same workspace gets ports.ErrOptimisticConflict, never a
// second quarantine event for it.
func (s *Store) QuarantineRepositoryWorkspace(ctx context.Context, update ports.QuarantineRepositoryWorkspaceUpdate) error {
	if update.RepositoryWorkspaceID == "" || update.EventID == "" || strings.TrimSpace(update.Reason) == "" {
		return errors.New("workspace quarantine update is incomplete")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin workspace quarantine: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	timestamp := formatWorkflowTime(update.OccurredAt)
	var projectID string
	err = tx.QueryRowContext(ctx, `
UPDATE repository_workspaces
SET state = 'QUARANTINED', version = version + 1, updated_at = ?
WHERE id = ? AND state = 'READY' AND version = ?
RETURNING project_id`,
		timestamp, update.RepositoryWorkspaceID, update.ExpectedVersion,
	).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: repository workspace %s expected READY@%d",
			ports.ErrOptimisticConflict, update.RepositoryWorkspaceID, update.ExpectedVersion)
	}
	if err != nil {
		return fmt.Errorf("quarantine repository workspace %s: %w", update.RepositoryWorkspaceID, err)
	}

	if err := appendRepositoryWorkspaceEvent(ctx, tx, repositoryWorkspaceEvent{
		id:            update.EventID,
		projectID:     projectID,
		workspaceID:   update.RepositoryWorkspaceID,
		eventType:     "REPOSITORY_WORKSPACE_QUARANTINED",
		correlationID: update.CorrelationID,
		timestamp:     timestamp,
		payload: map[string]string{
			"repositoryWorkspaceId": string(update.RepositoryWorkspaceID),
			"reason":                update.Reason,
		},
	}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workspace quarantine: %w", err)
	}
	return nil
}

// ReleaseRepositoryWorkspace is the fenced CAS transition from READY to
// RELEASED. There is no path out of QUARANTINED here by design: a
// quarantined generation can only be superseded by RecreateRepositoryWorkspace,
// never cleaned up in place, because a stale writer may still hold a process
// handle on it (docs/design/02-v0-spike-verdict.md V0-07).
func (s *Store) ReleaseRepositoryWorkspace(ctx context.Context, update ports.ReleaseRepositoryWorkspaceUpdate) error {
	if update.RepositoryWorkspaceID == "" || update.EventID == "" {
		return errors.New("workspace release update is incomplete")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin workspace release: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	timestamp := formatWorkflowTime(update.OccurredAt)
	var projectID string
	err = tx.QueryRowContext(ctx, `
UPDATE repository_workspaces
SET state = 'RELEASED', version = version + 1, updated_at = ?
WHERE id = ? AND state = 'READY' AND version = ?
RETURNING project_id`,
		timestamp, update.RepositoryWorkspaceID, update.ExpectedVersion,
	).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		var currentState string
		lookupErr := tx.QueryRowContext(ctx,
			`SELECT state FROM repository_workspaces WHERE id = ?`, update.RepositoryWorkspaceID,
		).Scan(&currentState)
		if lookupErr == nil && currentState == string(workspace.RepositoryWorkspaceQuarantined) {
			return fmt.Errorf("%w: repository workspace %s", ports.ErrWorkspaceQuarantined, update.RepositoryWorkspaceID)
		}
		return fmt.Errorf("%w: repository workspace %s expected READY@%d",
			ports.ErrOptimisticConflict, update.RepositoryWorkspaceID, update.ExpectedVersion)
	}
	if err != nil {
		return fmt.Errorf("release repository workspace %s: %w", update.RepositoryWorkspaceID, err)
	}

	if err := appendRepositoryWorkspaceEvent(ctx, tx, repositoryWorkspaceEvent{
		id:            update.EventID,
		projectID:     projectID,
		workspaceID:   update.RepositoryWorkspaceID,
		eventType:     "REPOSITORY_WORKSPACE_RELEASED",
		correlationID: update.CorrelationID,
		timestamp:     timestamp,
		payload: map[string]string{
			"repositoryWorkspaceId": string(update.RepositoryWorkspaceID),
		},
	}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workspace release: %w", err)
	}
	return nil
}

// RecreateRepositoryWorkspace reconciles a QUARANTINED RepositoryWorkspace by
// inserting the next generation as a new, independent row. The previous
// generation's row is never mutated further: it remains QUARANTINED
// permanently as evidence, and every write lease/finalization fence keyed to
// it (repository_workspace_id, generation) stays invalid forever, because
// the joins those checks use require state = 'READY' on that exact row.
func (s *Store) RecreateRepositoryWorkspace(
	ctx context.Context,
	request ports.RecreateRepositoryWorkspaceRequest,
) (workspace.RepositoryWorkspace, error) {
	if request.PreviousRepositoryWorkspaceID == "" || request.NewRepositoryWorkspaceID == "" ||
		request.EventID == "" || strings.TrimSpace(request.Locator) == "" || strings.TrimSpace(request.BaseRevision) == "" {
		return workspace.RepositoryWorkspace{}, errors.New("workspace recreate request is incomplete")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("begin workspace recreate: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var projectID, workspaceSetID, familyID, repositoryID string
	var previousGeneration uint64
	err = tx.QueryRowContext(ctx, `
SELECT project_id, workspace_set_id, family_id, repository_id, generation
FROM repository_workspaces
WHERE id = ? AND version = ? AND state = 'QUARANTINED'`,
		request.PreviousRepositoryWorkspaceID, request.PreviousExpectedVersion,
	).Scan(&projectID, &workspaceSetID, &familyID, &repositoryID, &previousGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.RepositoryWorkspace{}, fmt.Errorf(
			"%w: repository workspace %s expected QUARANTINED@%d",
			ports.ErrOptimisticConflict, request.PreviousRepositoryWorkspaceID, request.PreviousExpectedVersion)
	}
	if err != nil {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("load quarantined repository workspace: %w", err)
	}

	timestamp := formatWorkflowTime(request.OccurredAt)
	row := tx.QueryRowContext(ctx, `
INSERT INTO repository_workspaces (
    id, project_id, workspace_set_id, family_id, repository_id, generation,
    locator, branch_ref, base_revision, current_revision, state, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'READY', 1, ?, ?)
RETURNING `+repositoryWorkspaceColumns,
		request.NewRepositoryWorkspaceID, projectID, workspaceSetID, familyID, repositoryID, previousGeneration+1,
		request.Locator, request.BranchRef, request.BaseRevision, request.BaseRevision, timestamp, timestamp,
	)
	created, err := scanRepositoryWorkspace(row)
	if err != nil {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("recreate repository workspace: %w", err)
	}

	if err := appendRepositoryWorkspaceEvent(ctx, tx, repositoryWorkspaceEvent{
		id:            request.EventID,
		projectID:     projectID,
		workspaceID:   created.ID,
		eventType:     "REPOSITORY_WORKSPACE_RECREATED",
		correlationID: request.CorrelationID,
		timestamp:     timestamp,
		payload: map[string]string{
			"previousRepositoryWorkspaceId": string(request.PreviousRepositoryWorkspaceID),
			"repositoryWorkspaceId":         string(created.ID),
			"generation":                    fmt.Sprintf("%d", created.Generation),
		},
	}); err != nil {
		return workspace.RepositoryWorkspace{}, err
	}

	if err := tx.Commit(); err != nil {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("commit workspace recreate: %w", err)
	}
	return created, nil
}

type repositoryWorkspaceEvent struct {
	id            string
	projectID     string
	workspaceID   workspace.RepositoryWorkspaceID
	eventType     string
	correlationID string
	timestamp     string
	payload       map[string]string
}

func appendRepositoryWorkspaceEvent(ctx context.Context, tx *sql.Tx, event repositoryWorkspaceEvent) error {
	payload, err := json.Marshal(event.payload)
	if err != nil {
		return fmt.Errorf("encode repository workspace event: %w", err)
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(sequence), 0) + 1
FROM domain_events
WHERE aggregate_type = 'RepositoryWorkspace' AND aggregate_id = ?`, event.workspaceID,
	).Scan(&sequence); err != nil {
		return fmt.Errorf("allocate repository workspace event sequence: %w", err)
	}
	journalPosition, err := allocateJournalPosition(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO domain_events(
    id, project_id, aggregate_type, aggregate_id, sequence, journal_position,
    event_type, schema_version, payload_json, correlation_id, created_at
) VALUES (?, ?, 'RepositoryWorkspace', ?, ?, ?, ?, 1, ?, ?, ?)`,
		event.id, event.projectID, event.workspaceID, sequence, journalPosition, event.eventType, string(payload), event.correlationID, event.timestamp,
	); err != nil {
		return fmt.Errorf("append repository workspace event: %w", err)
	}
	return nil
}

func scanRepositoryWorkspace(scanner rowScanner) (workspace.RepositoryWorkspace, error) {
	var result workspace.RepositoryWorkspace
	var branchRef sql.NullString
	var currentRevision sql.NullString
	var lastProvisionErrorCode sql.NullString
	if err := scanner.Scan(
		&result.ID,
		&result.WorkspaceSetID,
		&result.RepositoryID,
		&result.Generation,
		&result.Locator,
		&branchRef,
		&result.BaseRevision,
		&currentRevision,
		&result.State,
		&result.Version,
		&lastProvisionErrorCode,
	); err != nil {
		return workspace.RepositoryWorkspace{}, err
	}
	result.BranchRef = branchRef.String
	result.CurrentRevision = currentRevision.String
	if lastProvisionErrorCode.Valid {
		code := lastProvisionErrorCode.String
		result.LastProvisionErrorCode = &code
	}
	return result, nil
}

var _ ports.WorkspaceLifecycle = (*Store)(nil)
