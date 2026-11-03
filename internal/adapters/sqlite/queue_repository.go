package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// queueRepository implements ports.QueueRepository over work_item_queue_entries (V12-19, migration 0058).
type queueRepository struct {
	tx *sql.Tx
}

const queueColumns = `work_item_id, project_id, family_id, position, waits_for_work_item_id, state, run_id, problem, version, created_at, updated_at`

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r queueRepository) PutQueueEntry(ctx context.Context, entry ports.QueueEntry, expectedVersion uint64) (ports.QueueEntry, error) {
	existing, err := r.GetQueueEntry(ctx, entry.WorkItemID)
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		if expectedVersion != 0 {
			return ports.QueueEntry{}, err
		}
		entry.Version = 1
		if _, err := r.tx.ExecContext(ctx, `INSERT INTO work_item_queue_entries (`+queueColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			entry.WorkItemID, entry.ProjectID, entry.FamilyID, entry.Position, nullable(entry.WaitsForWorkItemID), string(entry.State),
			nullable(entry.RunID), entry.Problem, entry.Version, formatWorkflowTime(entry.CreatedAt), formatWorkflowTime(entry.UpdatedAt),
		); err != nil {
			return ports.QueueEntry{}, MapSQLiteError(fmt.Errorf("insert queue entry: %w", err))
		}
		return entry, nil
	case err != nil:
		return ports.QueueEntry{}, err
	}
	if existing.Version != expectedVersion {
		return ports.QueueEntry{}, fmt.Errorf("%w: queue entry %s is at version %d, not %d", ports.ErrOptimisticConflict, entry.WorkItemID, existing.Version, expectedVersion)
	}
	entry.Version = existing.Version + 1
	entry.CreatedAt = existing.CreatedAt
	res, err := r.tx.ExecContext(ctx, `
UPDATE work_item_queue_entries SET position = ?, waits_for_work_item_id = ?, state = ?, run_id = ?, problem = ?, version = ?, updated_at = ?
WHERE work_item_id = ? AND version = ?`,
		entry.Position, nullable(entry.WaitsForWorkItemID), string(entry.State), nullable(entry.RunID), entry.Problem, entry.Version,
		formatWorkflowTime(entry.UpdatedAt), entry.WorkItemID, expectedVersion)
	if err != nil {
		return ports.QueueEntry{}, MapSQLiteError(fmt.Errorf("update queue entry: %w", err))
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ports.QueueEntry{}, fmt.Errorf("%w: queue entry %s", ports.ErrOptimisticConflict, entry.WorkItemID)
	}
	return entry, nil
}

func (r queueRepository) GetQueueEntry(ctx context.Context, workItemID string) (ports.QueueEntry, error) {
	entry, err := scanQueueEntry(r.tx.QueryRowContext(ctx, `SELECT `+queueColumns+` FROM work_item_queue_entries WHERE work_item_id = ?`, workItemID))
	if errors.Is(err, sql.ErrNoRows) {
		return ports.QueueEntry{}, fmt.Errorf("%w: queue entry %s", ports.ErrPersistenceNotFound, workItemID)
	}
	return entry, err
}

func (r queueRepository) ListQueueEntriesForFamily(ctx context.Context, familyID string) ([]ports.QueueEntry, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT `+queueColumns+` FROM work_item_queue_entries WHERE family_id = ? ORDER BY position, created_at`, familyID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list queue entries: %w", err))
	}
	defer rows.Close()
	var out []ports.QueueEntry
	for rows.Next() {
		entry, err := scanQueueEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (r queueRepository) ListFamiliesWithQueuedEntries(ctx context.Context) ([]string, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT DISTINCT family_id FROM work_item_queue_entries WHERE state = 'QUEUED' ORDER BY family_id`)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list queued families: %w", err))
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func scanQueueEntry(row repositoryRowScanner) (ports.QueueEntry, error) {
	var e ports.QueueEntry
	var waitsFor, runID sql.NullString
	var state, createdAt, updatedAt string
	if err := row.Scan(&e.WorkItemID, &e.ProjectID, &e.FamilyID, &e.Position, &waitsFor, &state, &runID, &e.Problem, &e.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.QueueEntry{}, err
		}
		return ports.QueueEntry{}, MapSQLiteError(fmt.Errorf("scan queue entry: %w", err))
	}
	e.WaitsForWorkItemID, e.RunID, e.State = waitsFor.String, runID.String, ports.QueueState(state)
	var err error
	if e.CreatedAt, err = parseWorkflowTime(createdAt); err != nil {
		return ports.QueueEntry{}, err
	}
	if e.UpdatedAt, err = parseWorkflowTime(updatedAt); err != nil {
		return ports.QueueEntry{}, err
	}
	return e, nil
}
