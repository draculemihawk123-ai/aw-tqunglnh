package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

type checkpointRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) StoreCheckpoint(ctx context.Context, checkpoint runtime.Checkpoint) (runtime.Checkpoint, error) {
	revisionJSON, err := json.Marshal(checkpoint.Revisions.Entries())
	if err != nil {
		return runtime.Checkpoint{}, fmt.Errorf("encode checkpoint revisions: %w", err)
	}
	artifactJSON, err := json.Marshal(checkpoint.ArtifactReferences)
	if err != nil {
		return runtime.Checkpoint{}, fmt.Errorf("encode checkpoint artifacts: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO checkpoints(
    id, run_id, node_run_id, attempt_id, sequence, canonical_event_sequence,
    context_snapshot_id, revision_set_json, shared_state_hash, artifact_refs_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		checkpoint.ID,
		checkpoint.RunID,
		checkpoint.NodeRunID,
		checkpoint.AttemptID,
		checkpoint.Sequence,
		checkpoint.CanonicalEventSequence,
		checkpoint.ContextSnapshotID,
		string(revisionJSON),
		checkpoint.SharedStateHash,
		string(artifactJSON),
		formatWorkflowTime(checkpoint.CreatedAt),
	)
	if err == nil {
		return checkpoint, nil
	}
	existing, loadErr := loadCheckpointByID(ctx, s.db, checkpoint.ID)
	if loadErr == nil {
		if checkpointsEqual(existing, checkpoint) {
			return existing, nil
		}
		return runtime.Checkpoint{}, fmt.Errorf("%w: checkpoint %s", ports.ErrImmutableVersionConflict, checkpoint.ID)
	}
	return runtime.Checkpoint{}, fmt.Errorf("store checkpoint: %w", err)
}

func (s *Store) LoadLatestCheckpoint(
	ctx context.Context,
	attemptID runtime.ExecutionAttemptID,
) (runtime.Checkpoint, error) {
	if attemptID == "" {
		return runtime.Checkpoint{}, errors.New("checkpoint attempt id is required")
	}
	return loadCheckpoint(ctx, s.db, `
SELECT id, run_id, node_run_id, attempt_id, sequence, canonical_event_sequence,
       context_snapshot_id, revision_set_json, shared_state_hash, artifact_refs_json, created_at
FROM checkpoints
WHERE attempt_id = ?
ORDER BY sequence DESC
LIMIT 1`, attemptID)
}

func loadCheckpointByID(ctx context.Context, queryer checkpointRowQueryer, id runtime.CheckpointID) (runtime.Checkpoint, error) {
	return loadCheckpoint(ctx, queryer, `
SELECT id, run_id, node_run_id, attempt_id, sequence, canonical_event_sequence,
       context_snapshot_id, revision_set_json, shared_state_hash, artifact_refs_json, created_at
FROM checkpoints
WHERE id = ?`, id)
}

func loadCheckpoint(
	ctx context.Context,
	queryer checkpointRowQueryer,
	statement string,
	argument any,
) (runtime.Checkpoint, error) {
	var (
		id                     runtime.CheckpointID
		runID                  runtime.WorkflowRunID
		nodeRunID              runtime.NodeRunID
		attemptID              runtime.ExecutionAttemptID
		sequence               uint64
		canonicalEventSequence uint64
		contextSnapshotID      runtime.ContextSnapshotID
		revisionJSON           string
		sharedStateHash        string
		artifactJSON           string
		createdAt              string
	)
	err := queryer.QueryRowContext(ctx, statement, argument).Scan(
		&id, &runID, &nodeRunID, &attemptID, &sequence, &canonicalEventSequence,
		&contextSnapshotID, &revisionJSON, &sharedStateHash, &artifactJSON, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.Checkpoint{}, fmt.Errorf("%w: checkpoint", ports.ErrPersistenceNotFound)
	}
	if err != nil {
		return runtime.Checkpoint{}, fmt.Errorf("load checkpoint: %w", err)
	}
	var revisions []workspace.Revision
	if err := json.Unmarshal([]byte(revisionJSON), &revisions); err != nil {
		return runtime.Checkpoint{}, fmt.Errorf("decode checkpoint revisions: %w", err)
	}
	revisionSet, err := workspace.NewRevisionSet(revisions)
	if err != nil {
		return runtime.Checkpoint{}, err
	}
	var artifacts []string
	if err := json.Unmarshal([]byte(artifactJSON), &artifacts); err != nil {
		return runtime.Checkpoint{}, fmt.Errorf("decode checkpoint artifacts: %w", err)
	}
	created, err := parseWorkflowTime(createdAt)
	if err != nil {
		return runtime.Checkpoint{}, err
	}
	return runtime.NewCheckpoint(
		id, runID, nodeRunID, attemptID, sequence, canonicalEventSequence,
		contextSnapshotID, revisionSet, sharedStateHash, artifacts, created,
	)
}

func checkpointsEqual(left, right runtime.Checkpoint) bool {
	leftRevisionJSON, leftRevisionErr := json.Marshal(left.Revisions.Entries())
	rightRevisionJSON, rightRevisionErr := json.Marshal(right.Revisions.Entries())
	leftArtifactJSON, leftArtifactErr := json.Marshal(left.ArtifactReferences)
	rightArtifactJSON, rightArtifactErr := json.Marshal(right.ArtifactReferences)
	return leftRevisionErr == nil && rightRevisionErr == nil && leftArtifactErr == nil && rightArtifactErr == nil &&
		left.ID == right.ID && left.RunID == right.RunID && left.NodeRunID == right.NodeRunID &&
		left.AttemptID == right.AttemptID && left.Sequence == right.Sequence &&
		left.CanonicalEventSequence == right.CanonicalEventSequence &&
		left.ContextSnapshotID == right.ContextSnapshotID && left.SharedStateHash == right.SharedStateHash &&
		left.CreatedAt.Equal(right.CreatedAt) && string(leftRevisionJSON) == string(rightRevisionJSON) &&
		string(leftArtifactJSON) == string(rightArtifactJSON)
}
