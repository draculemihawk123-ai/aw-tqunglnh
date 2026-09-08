package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// This file gives messageRepository its methods (V5-02,
// docs/design/07-v5-execution-evidence.md) — the durable, append-only
// task-chat Message row. Follows work_item_blocker.go's own Tx-composable
// pattern exactly: one xxxTx(ctx, tx *sql.Tx, ...) function per operation,
// plus a thin messageRepository method that forwards to it.
type messageRepository struct{ tx *sql.Tx }

var _ ports.MessageRepository = messageRepository{}

// AppendMessage implements ports.MessageRepository.
func (r messageRepository) AppendMessage(ctx context.Context, req ports.AppendMessageRequest) (message.Message, error) {
	return appendMessageTx(ctx, r.tx, req)
}

func appendMessageTx(ctx context.Context, tx *sql.Tx, req ports.AppendMessageRequest) (message.Message, error) {
	// req.WorkItemID's own STORED project is always what is checked
	// against req.ProjectID, never trusted from the caller — the same
	// cross-project discipline CreateComponent/AssignComponentPack already
	// follow for their own referenced-row checks.
	var workItemProjectID string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM work_items WHERE id = ?`, req.WorkItemID).Scan(&workItemProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return message.Message{}, fmt.Errorf("%w: work item %s", ports.ErrPersistenceNotFound, req.WorkItemID)
	}
	if err != nil {
		return message.Message{}, MapSQLiteError(fmt.Errorf("resolve message work item: %w", err))
	}
	if workItemProjectID != req.ProjectID {
		return message.Message{}, fmt.Errorf("%w: work item %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, req.WorkItemID, workItemProjectID, req.ProjectID)
	}

	var attemptID *runtime.ExecutionAttemptID
	if req.AttemptID != "" {
		// Resolve the Attempt's OWN WorkItem/Project by tracing
		// execution_attempts -> node_runs -> workflow_runs — never just an
		// existence check (audit finding, 2026-09-08: a bare "does this ID
		// exist" check would let a caller link a Message to an Attempt from
		// a completely different WorkItem or Project).
		var attemptWorkItemID, attemptProjectID string
		err := tx.QueryRowContext(ctx, `
SELECT wr.work_item_id, wr.project_id
FROM execution_attempts ea
JOIN node_runs nr ON nr.id = ea.node_run_id
JOIN workflow_runs wr ON wr.id = nr.run_id
WHERE ea.id = ?`, req.AttemptID).Scan(&attemptWorkItemID, &attemptProjectID)
		if errors.Is(err, sql.ErrNoRows) {
			return message.Message{}, fmt.Errorf("%w: execution attempt %s", ports.ErrPersistenceNotFound, req.AttemptID)
		}
		if err != nil {
			return message.Message{}, MapSQLiteError(fmt.Errorf("resolve message attempt: %w", err))
		}
		if attemptWorkItemID != req.WorkItemID || attemptProjectID != req.ProjectID {
			return message.Message{}, fmt.Errorf("%w: execution attempt %s belongs to work item %s / project %s, not %s / %s",
				ports.ErrCrossWorkItemReference, req.AttemptID, attemptWorkItemID, attemptProjectID, req.WorkItemID, req.ProjectID)
		}
		id := runtime.ExecutionAttemptID(req.AttemptID)
		attemptID = &id
	}

	var maxSequence uint64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sequence), 0) FROM messages WHERE work_item_id = ?`, req.WorkItemID,
	).Scan(&maxSequence); err != nil {
		return message.Message{}, MapSQLiteError(fmt.Errorf("resolve next message sequence: %w", err))
	}

	m, err := message.NewMessage(
		message.ID(req.ID), project.ProjectID(req.ProjectID), work.WorkItemID(req.WorkItemID), attemptID,
		maxSequence+1, req.Actor, req.Role, artifact.ID(req.ContentArtifactID), req.CorrelationID, req.CreatedAt,
	)
	if err != nil {
		return message.Message{}, err
	}

	var attemptIDColumn any
	if req.AttemptID != "" {
		attemptIDColumn = req.AttemptID
	}
	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO messages (id, project_id, work_item_id, attempt_id, sequence, actor, role, content_artifact_id, correlation_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(m.ID), string(m.ProjectID), string(m.WorkItemID), attemptIDColumn, m.Sequence,
		m.Actor, string(m.Role), string(m.ContentArtifactID), m.CorrelationID, formatWorkflowTime(m.CreatedAt),
	)
	if insertErr == nil {
		return m, nil
	}
	// Idempotent-by-ID: a duplicate insert of the identical ID returns the
	// ALREADY-stored row (with its originally resolved Sequence) — the
	// MAX+1/NewMessage work above is simply discarded rather than ever
	// persisted a second time.
	existing, loadErr := loadMessageTx(ctx, tx, req.ID)
	if loadErr != nil {
		return message.Message{}, MapSQLiteError(fmt.Errorf("append message: %w", insertErr))
	}
	return existing, nil
}

// GetMessage implements ports.MessageRepository.
func (r messageRepository) GetMessage(ctx context.Context, id string) (message.Message, error) {
	return loadMessageTx(ctx, r.tx, id)
}

func loadMessageTx(ctx context.Context, tx *sql.Tx, id string) (message.Message, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, work_item_id, attempt_id, sequence, actor, role, content_artifact_id, correlation_id, created_at
FROM messages WHERE id = ?`, id)
	m, err := scanMessageRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return message.Message{}, fmt.Errorf("%w: message %s", ports.ErrPersistenceNotFound, id)
	}
	return m, err
}

func scanMessageRow(row repositoryRowScanner) (message.Message, error) {
	var id, projectID, workItemID, actor, role, contentArtifactID, correlationID, createdAtRaw string
	var attemptIDRaw sql.NullString
	var sequence uint64
	if err := row.Scan(
		&id, &projectID, &workItemID, &attemptIDRaw, &sequence, &actor, &role, &contentArtifactID, &correlationID, &createdAtRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return message.Message{}, err
		}
		return message.Message{}, MapSQLiteError(fmt.Errorf("scan message row: %w", err))
	}
	createdAt, err := parseWorkflowTime(createdAtRaw)
	if err != nil {
		return message.Message{}, err
	}
	var attemptID *runtime.ExecutionAttemptID
	if attemptIDRaw.Valid {
		id := runtime.ExecutionAttemptID(attemptIDRaw.String)
		attemptID = &id
	}
	return message.NewMessage(
		message.ID(id), project.ProjectID(projectID), work.WorkItemID(workItemID), attemptID,
		sequence, actor, message.Role(role), artifact.ID(contentArtifactID), correlationID, createdAt,
	)
}

// ListMessagesForWorkItem implements ports.MessageRepository, ordered by
// sequence — the full, canonical task chat for workItemID.
func (r messageRepository) ListMessagesForWorkItem(ctx context.Context, workItemID string) ([]message.Message, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT id FROM messages WHERE work_item_id = ? ORDER BY sequence`, workItemID,
	)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list messages for work item %s: %w", workItemID, err))
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan message id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate message ids: %w", err)
	}
	rows.Close()

	messages := make([]message.Message, 0, len(ids))
	for _, id := range ids {
		m, err := loadMessageTx(ctx, r.tx, id)
		if err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, nil
}
