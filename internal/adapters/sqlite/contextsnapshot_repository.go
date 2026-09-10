package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file gives contextSnapshotRepository its methods (V5-04,
// docs/design/07-v5-execution-evidence.md) — the durable, immutable
// per-Attempt manifest. Follows work_item_blocker.go's own Tx-composable
// pattern exactly: one xxxTx(ctx, tx *sql.Tx, ...) function per operation,
// plus a thin contextSnapshotRepository method that forwards to it.
type contextSnapshotRepository struct{ tx *sql.Tx }

var _ ports.ContextSnapshotRepository = contextSnapshotRepository{}

// CreateSnapshot implements ports.ContextSnapshotRepository.
func (r contextSnapshotRepository) CreateSnapshot(ctx context.Context, snapshot contextsnapshot.Snapshot) (contextsnapshot.Snapshot, error) {
	return createSnapshotTx(ctx, r.tx, snapshot)
}

func createSnapshotTx(ctx context.Context, tx *sql.Tx, snapshot contextsnapshot.Snapshot) (contextsnapshot.Snapshot, error) {
	// snapshot.AttemptID's own row must already exist — enforced by this
	// table's own foreign key, but resolved explicitly first for a clean
	// ErrPersistenceNotFound rather than a raw FK-violation error, the
	// same discipline every other Tx-composable repository in this
	// codebase already follows for its own required references.
	var attemptExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM execution_attempts WHERE id = ?`, string(snapshot.AttemptID)).Scan(&attemptExists)
	if errors.Is(err, sql.ErrNoRows) {
		return contextsnapshot.Snapshot{}, fmt.Errorf("%w: execution attempt %s", ports.ErrPersistenceNotFound, snapshot.AttemptID)
	}
	if err != nil {
		return contextsnapshot.Snapshot{}, MapSQLiteError(fmt.Errorf("resolve context snapshot attempt: %w", err))
	}

	messageRefsJSON, err := json.Marshal(snapshot.MessageRefs)
	if err != nil {
		return contextsnapshot.Snapshot{}, fmt.Errorf("marshal context snapshot message refs: %w", err)
	}
	resourceRefsJSON, err := json.Marshal(snapshot.ResourceRefs)
	if err != nil {
		return contextsnapshot.Snapshot{}, fmt.Errorf("marshal context snapshot resource refs: %w", err)
	}
	evidenceRefsJSON, err := json.Marshal(snapshot.EvidenceRefs)
	if err != nil {
		return contextsnapshot.Snapshot{}, fmt.Errorf("marshal context snapshot evidence refs: %w", err)
	}
	revisionSetJSON, err := json.Marshal(snapshot.Revisions.Entries())
	if err != nil {
		return contextsnapshot.Snapshot{}, fmt.Errorf("marshal context snapshot revision set: %w", err)
	}

	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO attempt_context_snapshots (id, project_id, work_item_id, attempt_id, message_refs_json, resource_refs_json, evidence_refs_json, revision_set_json, manifest_hash, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(snapshot.ID), string(snapshot.ProjectID), string(snapshot.WorkItemID), string(snapshot.AttemptID),
		string(messageRefsJSON), string(resourceRefsJSON), string(evidenceRefsJSON), string(revisionSetJSON), snapshot.ManifestHash,
		formatWorkflowTime(snapshot.CreatedAt),
	)
	if insertErr == nil {
		return snapshot, nil
	}
	existing, loadErr := loadSnapshotTx(ctx, tx, `id = ?`, string(snapshot.ID))
	if loadErr != nil {
		return contextsnapshot.Snapshot{}, MapSQLiteError(fmt.Errorf("create context snapshot: %w", insertErr))
	}
	return existing, nil
}

// GetSnapshot implements ports.ContextSnapshotRepository.
func (r contextSnapshotRepository) GetSnapshot(ctx context.Context, id string) (contextsnapshot.Snapshot, error) {
	return loadSnapshotTx(ctx, r.tx, `id = ?`, id)
}

// GetSnapshotByAttemptID implements ports.ContextSnapshotRepository.
func (r contextSnapshotRepository) GetSnapshotByAttemptID(ctx context.Context, attemptID string) (contextsnapshot.Snapshot, error) {
	return loadSnapshotTx(ctx, r.tx, `attempt_id = ?`, attemptID)
}

func loadSnapshotTx(ctx context.Context, tx *sql.Tx, whereClause string, arg string) (contextsnapshot.Snapshot, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, work_item_id, attempt_id, message_refs_json, resource_refs_json, evidence_refs_json, revision_set_json, manifest_hash, created_at
FROM attempt_context_snapshots WHERE `+whereClause, arg)
	snapshot, err := scanSnapshotRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return contextsnapshot.Snapshot{}, fmt.Errorf("%w: context snapshot (%s = %s)", ports.ErrPersistenceNotFound, whereClause, arg)
	}
	return snapshot, err
}

func scanSnapshotRow(row repositoryRowScanner) (contextsnapshot.Snapshot, error) {
	var id, projectID, workItemID, attemptID, messageRefsRaw, resourceRefsRaw, evidenceRefsRaw, revisionSetRaw, manifestHash, createdAtRaw string
	if err := row.Scan(&id, &projectID, &workItemID, &attemptID, &messageRefsRaw, &resourceRefsRaw, &evidenceRefsRaw, &revisionSetRaw, &manifestHash, &createdAtRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contextsnapshot.Snapshot{}, err
		}
		return contextsnapshot.Snapshot{}, MapSQLiteError(fmt.Errorf("scan context snapshot row: %w", err))
	}

	var messageRefs []contextsnapshot.MessageRef
	if err := json.Unmarshal([]byte(messageRefsRaw), &messageRefs); err != nil {
		return contextsnapshot.Snapshot{}, fmt.Errorf("decode stored context snapshot message refs: %w", err)
	}
	var resourceRefs []contextsnapshot.ResourceRef
	if err := json.Unmarshal([]byte(resourceRefsRaw), &resourceRefs); err != nil {
		return contextsnapshot.Snapshot{}, fmt.Errorf("decode stored context snapshot resource refs: %w", err)
	}
	var evidenceRefs []contextsnapshot.EvidenceRef
	if err := json.Unmarshal([]byte(evidenceRefsRaw), &evidenceRefs); err != nil {
		return contextsnapshot.Snapshot{}, fmt.Errorf("decode stored context snapshot evidence refs: %w", err)
	}
	var revisionEntries []workspace.Revision
	if err := json.Unmarshal([]byte(revisionSetRaw), &revisionEntries); err != nil {
		return contextsnapshot.Snapshot{}, fmt.Errorf("decode stored context snapshot revision set: %w", err)
	}
	revisions, err := workspace.NewRevisionSet(revisionEntries)
	if err != nil {
		return contextsnapshot.Snapshot{}, err
	}
	createdAt, err := parseWorkflowTime(createdAtRaw)
	if err != nil {
		return contextsnapshot.Snapshot{}, err
	}

	// Reconstruct via the domain constructor — the same "rebuild through
	// NewX, not a bare struct literal" discipline rebuildAdapterBuild
	// already uses, so a stored row that no longer re-derives its own
	// recorded ManifestHash is caught as tamper/corruption here rather
	// than silently trusted.
	rebuilt, err := contextsnapshot.NewSnapshot(
		contextsnapshot.ID(id), project.ProjectID(projectID), work.WorkItemID(workItemID), contextsnapshot.AttemptID(attemptID),
		messageRefs, resourceRefs, evidenceRefs, revisions, createdAt,
	)
	if err != nil {
		return contextsnapshot.Snapshot{}, err
	}
	if rebuilt.ManifestHash != manifestHash {
		return contextsnapshot.Snapshot{}, fmt.Errorf("%w: context snapshot %s manifest hash mismatch (stored=%s recomputed=%s)",
			ports.ErrImmutableVersionConflict, id, manifestHash, rebuilt.ManifestHash)
	}
	return rebuilt, nil
}
