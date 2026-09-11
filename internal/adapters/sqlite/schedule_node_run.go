package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// repositoryScopeDTO is work.RepositoryScope's own JSON wire shape — that
// domain type's fields are all unexported (only accessor methods), so a
// plain json.Marshal/Unmarshal of the type itself would see nothing; this
// DTO is the explicit serialize/deserialize boundary, round-tripped
// through work.NewRepositoryScope's own validating constructor on the way
// back in (never bypassing it, even for data this same package already
// wrote).
type repositoryScopeDTO struct {
	FamilyID            string   `json:"familyId"`
	AddedInScopeVersion uint64   `json:"addedInScopeVersion"`
	RepositoryID        string   `json:"repositoryId"`
	Access              string   `json:"access"`
	PathScopes          []string `json:"pathScopes"`
	Reason              string   `json:"reason"`
	AddedBy             string   `json:"addedBy"`
	AddedAt             string   `json:"addedAt"`
}

func encodeEffectiveScopeJSON(scopes []work.RepositoryScope) (string, error) {
	dtos := make([]repositoryScopeDTO, len(scopes))
	for i, s := range scopes {
		dtos[i] = repositoryScopeDTO{
			FamilyID: string(s.FamilyID()), AddedInScopeVersion: s.AddedInScopeVersion(),
			RepositoryID: string(s.RepositoryID()), Access: string(s.Access()),
			PathScopes: s.PathScopes(), Reason: s.Reason(), AddedBy: s.AddedBy(),
			AddedAt: s.AddedAt().UTC().Format(time.RFC3339Nano),
		}
	}
	encoded, err := json.Marshal(dtos)
	if err != nil {
		return "", fmt.Errorf("encode effective scope: %w", err)
	}
	return string(encoded), nil
}

func decodeEffectiveScopeJSON(raw string) ([]work.RepositoryScope, error) {
	var dtos []repositoryScopeDTO
	if err := json.Unmarshal([]byte(raw), &dtos); err != nil {
		return nil, fmt.Errorf("unmarshal effective scope: %w", err)
	}
	scopes := make([]work.RepositoryScope, len(dtos))
	for i, dto := range dtos {
		addedAt, err := time.Parse(time.RFC3339Nano, dto.AddedAt)
		if err != nil {
			return nil, fmt.Errorf("parse effective scope[%d] addedAt: %w", i, err)
		}
		scope, err := work.NewRepositoryScope(
			work.TaskFamilyID(dto.FamilyID), dto.AddedInScopeVersion, project.RepositoryID(dto.RepositoryID),
			work.RepositoryAccess(dto.Access), dto.PathScopes, dto.Reason, dto.AddedBy, addedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("reconstruct effective scope[%d]: %w", i, err)
		}
		scopes[i] = scope
	}
	return scopes, nil
}

// ScheduleNodeRun implements ports.RuntimeRepository (V4-04): the CAS that
// closes an executable NodeRun's own scheduling decision. See that
// interface method's own doc comment for why this is a separate method
// from TransitionNodeRun.
func (r runtimeRepository) ScheduleNodeRun(ctx context.Context, req ports.ScheduleNodeRunRequest) (runtime.NodeRun, error) {
	return scheduleNodeRunTx(ctx, r.tx, req)
}

func scheduleNodeRunTx(ctx context.Context, tx *sql.Tx, req ports.ScheduleNodeRunRequest) (runtime.NodeRun, error) {
	if req.NodeRunID == "" {
		return runtime.NodeRun{}, errors.New("node run id is required")
	}
	if req.ExecutionProfileHash == "" {
		return runtime.NodeRun{}, errors.New("execution profile hash is required")
	}
	effectiveScopeJSON, err := encodeEffectiveScopeJSON(req.EffectiveScope)
	if err != nil {
		return runtime.NodeRun{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
UPDATE node_runs
SET state = 'QUEUED', effective_scope_json = ?, execution_profile_hash = ?, manifest_revision = ?,
    version = version + 1, updated_at = ?
WHERE id = ? AND state = 'PENDING' AND version = ?`,
		effectiveScopeJSON, req.ExecutionProfileHash, req.ManifestRevision, now,
		req.NodeRunID, req.ExpectedVersion,
	)
	if err != nil {
		return runtime.NodeRun{}, MapSQLiteError(fmt.Errorf("schedule node run %s: %w", req.NodeRunID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.NodeRun{}, fmt.Errorf("read schedule node run result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM node_runs WHERE id = ?`, req.NodeRunID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return runtime.NodeRun{}, fmt.Errorf("%w: node run %s", ports.ErrPersistenceNotFound, req.NodeRunID)
		}
		if lookupErr != nil {
			return runtime.NodeRun{}, MapSQLiteError(fmt.Errorf("check stale schedule node run: %w", lookupErr))
		}
		return runtime.NodeRun{}, fmt.Errorf(
			"%w: node run %s expected PENDING@%d", ports.ErrOptimisticConflict, req.NodeRunID, req.ExpectedVersion,
		)
	}
	return loadNodeRunByID(ctx, tx, runtime.NodeRunID(req.NodeRunID))
}

// CreateExecutionAttempt implements ports.RuntimeRepository (V4-04):
// inserts the first ExecutionAttempt for an executable NodeRun.
func (r runtimeRepository) CreateExecutionAttempt(ctx context.Context, attempt runtime.ExecutionAttempt) (runtime.ExecutionAttempt, error) {
	return createExecutionAttemptTx(ctx, r.tx, attempt)
}

func createExecutionAttemptTx(ctx context.Context, tx *sql.Tx, attempt runtime.ExecutionAttempt) (runtime.ExecutionAttempt, error) {
	if attempt.ID == "" || attempt.NodeRunID == "" || attempt.ExecutionProfileHash == "" {
		return runtime.ExecutionAttempt{}, errors.New("execution attempt id, node run id and execution profile hash are required")
	}
	if attempt.AttemptNumber == 0 {
		return runtime.ExecutionAttempt{}, errors.New("execution attempt number must be greater than zero")
	}

	var nodeRunExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM node_runs WHERE id = ?`, string(attempt.NodeRunID)).Scan(&nodeRunExists)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.ExecutionAttempt{}, fmt.Errorf("%w: node run %s", ports.ErrPersistenceNotFound, attempt.NodeRunID)
	}
	if err != nil {
		return runtime.ExecutionAttempt{}, MapSQLiteError(fmt.Errorf("resolve execution attempt's node run: %w", err))
	}

	revisions := []workspace.Revision{}
	if attempt.InputRevisionSet != nil {
		revisions = attempt.InputRevisionSet.Entries()
	}
	revisionSetJSON, err := json.Marshal(revisions)
	if err != nil {
		return runtime.ExecutionAttempt{}, fmt.Errorf("marshal execution attempt input revision set: %w", err)
	}

	var providerKey any
	if attempt.ProviderKey != "" {
		providerKey = attempt.ProviderKey
	}
	// contextSnapshotID is populated now (V5-04): every production
	// scheduling path added from V5-04 onward sets attempt.ContextSnapshotID
	// before calling this method (schedule.go/finalize.go) — nil for
	// every pre-V5-04 caller, preserving this column's own existing
	// implicit-NULL behavior for them exactly.
	var contextSnapshotID any
	if attempt.ContextSnapshotID != nil {
		contextSnapshotID = string(*attempt.ContextSnapshotID)
	}
	// lastCheckpointID is populated now (V5-13, 2026-09-11): a FRESH_START
	// replacement Attempt (recovery_reaper.go's own consumeFreshStart)
	// pins this to the real Checkpoint it recovers from — the column
	// itself has existed since migration 0001 but nothing ever wrote to
	// it before this task gave it real meaning. nil for every ordinary
	// (non-recovery) Attempt, preserving the same implicit-NULL default
	// every prior caller already got.
	var lastCheckpointID any
	if attempt.LastCheckpointID != nil {
		lastCheckpointID = string(*attempt.LastCheckpointID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := tx.ExecContext(ctx, `
INSERT INTO execution_attempts (
    id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
    context_snapshot_id, input_revision_set_json, last_checkpoint_id, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(attempt.ID), string(attempt.NodeRunID), attempt.AttemptNumber, string(attempt.State),
		providerKey, attempt.ExecutionProfileHash, contextSnapshotID, string(revisionSetJSON), lastCheckpointID, attempt.Version, now, now,
	); err != nil {
		var existing int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM execution_attempts WHERE id = ?`, attempt.ID).Scan(&existing)
		if lookupErr == nil {
			return runtime.ExecutionAttempt{}, fmt.Errorf("%w: execution attempt %s", ports.ErrPersistenceAlreadyExists, attempt.ID)
		}
		return runtime.ExecutionAttempt{}, MapSQLiteError(fmt.Errorf("create execution attempt %s: %w", attempt.ID, err))
	}
	return attempt, nil
}
