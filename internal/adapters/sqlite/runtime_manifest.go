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
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// --- ExecutionManifest ---

// CreateExecutionManifest implements ports.RuntimeRepository (V4-01): the
// one immutable pin bundle a WorkflowRun ever has (GC-INV-06). It resolves
// the run's own project and the pinned WorkflowVersion's own definition
// project from their rows directly (never trusting the caller's claim),
// the same ErrCrossProjectReference discipline
// internal/adapters/sqlite/catalog.go's createComponentTx already
// establishes.
func (r runtimeRepository) CreateExecutionManifest(ctx context.Context, manifest runtime.ExecutionManifest) (runtime.ExecutionManifest, error) {
	return createExecutionManifestTx(ctx, r.tx, manifest)
}

func createExecutionManifestTx(ctx context.Context, tx *sql.Tx, manifest runtime.ExecutionManifest) (runtime.ExecutionManifest, error) {
	var runProjectID string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM workflow_runs WHERE id = ?`, string(manifest.RunID)).Scan(&runProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.ExecutionManifest{}, fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceNotFound, manifest.RunID)
	}
	if err != nil {
		return runtime.ExecutionManifest{}, MapSQLiteError(fmt.Errorf("resolve execution manifest run: %w", err))
	}

	var versionDefinitionProject sql.NullString
	err = tx.QueryRowContext(ctx, `
SELECT d.project_id
FROM workflow_versions AS v
JOIN workflow_definitions AS d ON d.id = v.definition_id
WHERE v.id = ?`, string(manifest.WorkflowVersionID)).Scan(&versionDefinitionProject)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.ExecutionManifest{}, fmt.Errorf("%w: workflow version %s", ports.ErrPersistenceNotFound, manifest.WorkflowVersionID)
	}
	if err != nil {
		return runtime.ExecutionManifest{}, MapSQLiteError(fmt.Errorf("resolve execution manifest workflow version: %w", err))
	}
	if versionDefinitionProject.Valid && versionDefinitionProject.String != runProjectID {
		return runtime.ExecutionManifest{}, fmt.Errorf(
			"%w: execution manifest workflow version %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, manifest.WorkflowVersionID, versionDefinitionProject.String, runProjectID,
		)
	}

	dependencyJSON, err := json.Marshal(manifest.DependencyManifest)
	if err != nil {
		return runtime.ExecutionManifest{}, fmt.Errorf("marshal execution manifest dependency manifest: %w", err)
	}
	revisionsJSON, err := json.Marshal(manifest.BaseRevisionSet.Entries())
	if err != nil {
		return runtime.ExecutionManifest{}, fmt.Errorf("marshal execution manifest base revision set: %w", err)
	}

	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO execution_manifests (
    id, run_id, workflow_version_id, compiled_snapshot_hash, dependency_manifest_json,
    base_revision_set_json, execution_profile_hash, context_route_policy_hash, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(manifest.ID), string(manifest.RunID), string(manifest.WorkflowVersionID), manifest.CompiledSnapshotHash,
		string(dependencyJSON), string(revisionsJSON),
		nullableString(manifest.ExecutionProfileHash), nullableString(manifest.ContextRoutePolicyHash),
		formatWorkflowTime(manifest.CreatedAt),
	)
	if insertErr == nil {
		return manifest, nil
	}

	existing, loadErr := loadExecutionManifestTx(ctx, tx, string(manifest.RunID))
	if loadErr != nil {
		return runtime.ExecutionManifest{}, MapSQLiteError(fmt.Errorf("create execution manifest: %w", insertErr))
	}
	if sameExecutionManifestPins(existing, manifest) {
		return existing, nil
	}
	return runtime.ExecutionManifest{}, fmt.Errorf("%w: execution manifest for run %s", ports.ErrImmutableVersionConflict, manifest.RunID)
}

func sameExecutionManifestPins(left, right runtime.ExecutionManifest) bool {
	leftManifest, errLeft := json.Marshal(left.DependencyManifest)
	rightManifest, errRight := json.Marshal(right.DependencyManifest)
	leftRevisions, errLeftRevisions := json.Marshal(left.BaseRevisionSet.Entries())
	rightRevisions, errRightRevisions := json.Marshal(right.BaseRevisionSet.Entries())
	return errLeft == nil && errRight == nil && errLeftRevisions == nil && errRightRevisions == nil &&
		left.WorkflowVersionID == right.WorkflowVersionID &&
		left.CompiledSnapshotHash == right.CompiledSnapshotHash &&
		string(leftManifest) == string(rightManifest) &&
		string(leftRevisions) == string(rightRevisions)
}

// GetExecutionManifest implements ports.RuntimeRepository.
func (r runtimeRepository) GetExecutionManifest(ctx context.Context, runID string) (runtime.ExecutionManifest, error) {
	return loadExecutionManifestTx(ctx, r.tx, runID)
}

func loadExecutionManifestTx(ctx context.Context, tx *sql.Tx, runID string) (runtime.ExecutionManifest, error) {
	var (
		id                     string
		workflowVersionID      string
		compiledSnapshotHash   string
		dependencyManifestRaw  string
		baseRevisionSetRaw     string
		executionProfileHash   sql.NullString
		contextRoutePolicyHash sql.NullString
		createdAtRaw           string
	)
	err := tx.QueryRowContext(ctx, `
SELECT id, workflow_version_id, compiled_snapshot_hash, dependency_manifest_json,
       base_revision_set_json, execution_profile_hash, context_route_policy_hash, created_at
FROM execution_manifests WHERE run_id = ?`, runID).Scan(
		&id, &workflowVersionID, &compiledSnapshotHash, &dependencyManifestRaw,
		&baseRevisionSetRaw, &executionProfileHash, &contextRoutePolicyHash, &createdAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.ExecutionManifest{}, fmt.Errorf("%w: execution manifest for run %s", ports.ErrPersistenceNotFound, runID)
	}
	if err != nil {
		return runtime.ExecutionManifest{}, MapSQLiteError(fmt.Errorf("load execution manifest: %w", err))
	}
	var dependencyManifest workflow.DependencyManifest
	if err := json.Unmarshal([]byte(dependencyManifestRaw), &dependencyManifest); err != nil {
		return runtime.ExecutionManifest{}, fmt.Errorf("decode execution manifest dependency manifest: %w", err)
	}
	var revisionEntries []workspace.Revision
	if err := json.Unmarshal([]byte(baseRevisionSetRaw), &revisionEntries); err != nil {
		return runtime.ExecutionManifest{}, fmt.Errorf("decode execution manifest base revision set: %w", err)
	}
	revisionSet, err := workspace.NewRevisionSet(revisionEntries)
	if err != nil {
		return runtime.ExecutionManifest{}, fmt.Errorf("rebuild execution manifest base revision set: %w", err)
	}
	createdAt, err := parseWorkflowTime(createdAtRaw)
	if err != nil {
		return runtime.ExecutionManifest{}, err
	}
	return runtime.ExecutionManifest{
		ID:                     runtime.ExecutionManifestID(id),
		RunID:                  runtime.WorkflowRunID(runID),
		WorkflowVersionID:      workflow.WorkflowVersionID(workflowVersionID),
		CompiledSnapshotHash:   compiledSnapshotHash,
		DependencyManifest:     dependencyManifest,
		BaseRevisionSet:        revisionSet,
		ExecutionProfileHash:   executionProfileHash.String,
		ContextRoutePolicyHash: contextRoutePolicyHash.String,
		CreatedAt:              createdAt,
	}, nil
}

// --- RunManifestAmendment ---

// AppendRunManifestAmendment implements ports.RuntimeRepository (V4-01,
// ADR-011). It never touches execution_manifests: every call is a new,
// append-only row here.
func (r runtimeRepository) AppendRunManifestAmendment(ctx context.Context, amendment runtime.RunManifestAmendment) (runtime.RunManifestAmendment, error) {
	return appendRunManifestAmendmentTx(ctx, r.tx, amendment)
}

func appendRunManifestAmendmentTx(ctx context.Context, tx *sql.Tx, amendment runtime.RunManifestAmendment) (runtime.RunManifestAmendment, error) {
	var manifestExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM execution_manifests WHERE run_id = ?`, string(amendment.RunID)).Scan(&manifestExists)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.RunManifestAmendment{}, fmt.Errorf("%w: execution manifest for run %s", ports.ErrPersistenceNotFound, amendment.RunID)
	}
	if err != nil {
		return runtime.RunManifestAmendment{}, MapSQLiteError(fmt.Errorf("resolve run manifest amendment run: %w", err))
	}

	var highest uint64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(revision), 0) FROM run_manifest_amendments WHERE run_id = ?`,
		string(amendment.RunID)).Scan(&highest); err != nil {
		return runtime.RunManifestAmendment{}, MapSQLiteError(fmt.Errorf("resolve run manifest amendment high water mark: %w", err))
	}
	if amendment.PreviousRevision != highest {
		return runtime.RunManifestAmendment{}, fmt.Errorf(
			"%w: run %s expected previous revision %d, got %d",
			ports.ErrOptimisticConflict, amendment.RunID, highest, amendment.PreviousRevision,
		)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO run_manifest_amendments (
    id, run_id, revision, previous_revision, approved_scope_version,
    reason, approved_by, approved_at, content_hash, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(amendment.ID), string(amendment.RunID), amendment.Revision, amendment.PreviousRevision,
		amendment.ApprovedScopeVersion, amendment.Reason, amendment.ApprovedBy,
		formatWorkflowTime(amendment.ApprovedAt), amendment.ContentHash, formatWorkflowTime(amendment.ApprovedAt),
	); err != nil {
		return runtime.RunManifestAmendment{}, MapSQLiteError(fmt.Errorf("append run manifest amendment: %w", err))
	}
	return amendment, nil
}

// ListRunManifestAmendments implements ports.RuntimeRepository, oldest
// (lowest Revision) first.
func (r runtimeRepository) ListRunManifestAmendments(ctx context.Context, runID string) ([]runtime.RunManifestAmendment, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT id, revision, previous_revision, approved_scope_version, reason, approved_by, approved_at, content_hash
FROM run_manifest_amendments WHERE run_id = ? ORDER BY revision`, runID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list run manifest amendments: %w", err))
	}
	defer rows.Close()

	var amendments []runtime.RunManifestAmendment
	for rows.Next() {
		var (
			id                   string
			revision             uint64
			previousRevision     uint64
			approvedScopeVersion uint64
			reason               string
			approvedBy           string
			approvedAtRaw        string
			contentHash          string
		)
		if err := rows.Scan(&id, &revision, &previousRevision, &approvedScopeVersion, &reason, &approvedBy, &approvedAtRaw, &contentHash); err != nil {
			return nil, fmt.Errorf("scan run manifest amendment: %w", err)
		}
		approvedAt, err := parseWorkflowTime(approvedAtRaw)
		if err != nil {
			return nil, err
		}
		amendments = append(amendments, runtime.RunManifestAmendment{
			ID: runtime.RunManifestAmendmentID(id), RunID: runtime.WorkflowRunID(runID),
			Revision: revision, PreviousRevision: previousRevision, ApprovedScopeVersion: approvedScopeVersion,
			Reason: reason, ApprovedBy: approvedBy, ApprovedAt: approvedAt, ContentHash: contentHash,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run manifest amendments: %w", err)
	}
	return amendments, nil
}

// --- BranchToken ---

// CreateBranchToken implements ports.RuntimeRepository (V4-01, HE-14-M09).
// A second call for the same (RunID, ForkKey, BranchKey) is idempotent when
// CurrentNodeKey matches; V4-01 does not otherwise mutate a token — that
// belongs to whichever later task (V4-10/V4-11) first needs it.
func (r runtimeRepository) CreateBranchToken(ctx context.Context, token runtime.BranchToken) (runtime.BranchToken, error) {
	return createBranchTokenTx(ctx, r.tx, token)
}

func createBranchTokenTx(ctx context.Context, tx *sql.Tx, token runtime.BranchToken) (runtime.BranchToken, error) {
	var runExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM workflow_runs WHERE id = ?`, string(token.RunID)).Scan(&runExists)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.BranchToken{}, fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceNotFound, token.RunID)
	}
	if err != nil {
		return runtime.BranchToken{}, MapSQLiteError(fmt.Errorf("resolve branch token run: %w", err))
	}

	now := formatWorkflowTime(time.Now())
	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO branch_tokens (
    id, run_id, fork_key, branch_key, current_node_key, state, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(token.ID), string(token.RunID), token.ForkKey, token.BranchKey, token.CurrentNodeKey,
		string(token.State), token.Version, now, now,
	)
	if insertErr == nil {
		return token, nil
	}

	existing, loadErr := loadBranchTokenTx(ctx, tx, string(token.RunID), token.ForkKey, token.BranchKey)
	if loadErr != nil {
		return runtime.BranchToken{}, MapSQLiteError(fmt.Errorf("create branch token: %w", insertErr))
	}
	if existing.CurrentNodeKey == token.CurrentNodeKey {
		return existing, nil
	}
	return runtime.BranchToken{}, fmt.Errorf("%w: branch token %s/%s/%s", ports.ErrOptimisticConflict, token.RunID, token.ForkKey, token.BranchKey)
}

// GetBranchToken implements ports.RuntimeRepository.
func (r runtimeRepository) GetBranchToken(ctx context.Context, runID, forkKey, branchKey string) (runtime.BranchToken, error) {
	return loadBranchTokenTx(ctx, r.tx, runID, forkKey, branchKey)
}

func loadBranchTokenTx(ctx context.Context, tx *sql.Tx, runID, forkKey, branchKey string) (runtime.BranchToken, error) {
	var (
		id             string
		currentNodeKey string
		state          string
		version        uint64
	)
	err := tx.QueryRowContext(ctx, `
SELECT id, current_node_key, state, version FROM branch_tokens
WHERE run_id = ? AND fork_key = ? AND branch_key = ?`, runID, forkKey, branchKey).Scan(&id, &currentNodeKey, &state, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.BranchToken{}, fmt.Errorf("%w: branch token %s/%s/%s", ports.ErrPersistenceNotFound, runID, forkKey, branchKey)
	}
	if err != nil {
		return runtime.BranchToken{}, MapSQLiteError(fmt.Errorf("load branch token: %w", err))
	}
	return runtime.BranchToken{
		ID: runtime.BranchTokenID(id), RunID: runtime.WorkflowRunID(runID), ForkKey: forkKey, BranchKey: branchKey,
		CurrentNodeKey: currentNodeKey, State: runtime.BranchTokenState(state), Version: version,
	}, nil
}

// ListBranchTokensForRun implements ports.RuntimeRepository, ordered by
// (ForkKey, BranchKey).
func (r runtimeRepository) ListBranchTokensForRun(ctx context.Context, runID string) ([]runtime.BranchToken, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT id, fork_key, branch_key, current_node_key, state, version
FROM branch_tokens WHERE run_id = ? ORDER BY fork_key, branch_key`, runID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list branch tokens: %w", err))
	}
	defer rows.Close()

	var tokens []runtime.BranchToken
	for rows.Next() {
		var (
			id             string
			forkKey        string
			branchKey      string
			currentNodeKey string
			state          string
			version        uint64
		)
		if err := rows.Scan(&id, &forkKey, &branchKey, &currentNodeKey, &state, &version); err != nil {
			return nil, fmt.Errorf("scan branch token: %w", err)
		}
		tokens = append(tokens, runtime.BranchToken{
			ID: runtime.BranchTokenID(id), RunID: runtime.WorkflowRunID(runID), ForkKey: forkKey, BranchKey: branchKey,
			CurrentNodeKey: currentNodeKey, State: runtime.BranchTokenState(state), Version: version,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate branch tokens: %w", err)
	}
	return tokens, nil
}

// --- DecisionArtifact ---

// RecordDecisionArtifact implements ports.RuntimeRepository (V4-01,
// HE-03-M08). It is a plain, immutable append: there is no update path.
func (r runtimeRepository) RecordDecisionArtifact(ctx context.Context, artifact runtime.DecisionArtifact) (runtime.DecisionArtifact, error) {
	return recordDecisionArtifactTx(ctx, r.tx, artifact)
}

func recordDecisionArtifactTx(ctx context.Context, tx *sql.Tx, artifact runtime.DecisionArtifact) (runtime.DecisionArtifact, error) {
	var projectExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, string(artifact.ProjectID)).Scan(&projectExists)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.DecisionArtifact{}, fmt.Errorf("%w: project %s", ports.ErrPersistenceNotFound, artifact.ProjectID)
	}
	if err != nil {
		return runtime.DecisionArtifact{}, MapSQLiteError(fmt.Errorf("resolve decision artifact project: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO decision_artifacts (id, project_id, kind, policy_version, input_json, result_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(artifact.ID), string(artifact.ProjectID), artifact.Kind, artifact.PolicyVersion,
		string(artifact.Input), string(artifact.Result), formatWorkflowTime(artifact.CreatedAt),
	); err != nil {
		var existing int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM decision_artifacts WHERE id = ?`, string(artifact.ID)).Scan(&existing)
		if lookupErr == nil {
			return runtime.DecisionArtifact{}, fmt.Errorf("%w: decision artifact %s", ports.ErrPersistenceAlreadyExists, artifact.ID)
		}
		return runtime.DecisionArtifact{}, MapSQLiteError(fmt.Errorf("record decision artifact: %w", err))
	}
	return artifact, nil
}

// GetDecisionArtifact implements ports.RuntimeRepository.
func (r runtimeRepository) GetDecisionArtifact(ctx context.Context, id string) (runtime.DecisionArtifact, error) {
	var (
		projectID     string
		kind          string
		policyVersion string
		inputRaw      string
		resultRaw     string
		createdAtRaw  string
	)
	err := r.tx.QueryRowContext(ctx, `
SELECT project_id, kind, policy_version, input_json, result_json, created_at
FROM decision_artifacts WHERE id = ?`, id).Scan(&projectID, &kind, &policyVersion, &inputRaw, &resultRaw, &createdAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.DecisionArtifact{}, fmt.Errorf("%w: decision artifact %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return runtime.DecisionArtifact{}, MapSQLiteError(fmt.Errorf("load decision artifact: %w", err))
	}
	createdAt, err := parseWorkflowTime(createdAtRaw)
	if err != nil {
		return runtime.DecisionArtifact{}, err
	}
	return runtime.DecisionArtifact{
		ID: runtime.DecisionArtifactID(id), ProjectID: project.ProjectID(projectID), Kind: kind, PolicyVersion: policyVersion,
		Input: json.RawMessage(inputRaw), Result: json.RawMessage(resultRaw), CreatedAt: createdAt,
	}, nil
}

// --- RunCancellationIntent ---

// RecordRunCancellationIntent implements ports.RuntimeRepository (V4-01,
// ADR-020). Idempotent by RunID: a second call for a run that already has
// an intent returns the existing row unchanged, never a second row.
func (r runtimeRepository) RecordRunCancellationIntent(ctx context.Context, intent runtime.RunCancellationIntent) (runtime.RunCancellationIntent, error) {
	return recordRunCancellationIntentTx(ctx, r.tx, intent)
}

func recordRunCancellationIntentTx(ctx context.Context, tx *sql.Tx, intent runtime.RunCancellationIntent) (runtime.RunCancellationIntent, error) {
	var runProjectID string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM workflow_runs WHERE id = ?`, string(intent.RunID)).Scan(&runProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.RunCancellationIntent{}, fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceNotFound, intent.RunID)
	}
	if err != nil {
		return runtime.RunCancellationIntent{}, MapSQLiteError(fmt.Errorf("resolve run cancellation intent run: %w", err))
	}
	if runProjectID != string(intent.ProjectID) {
		return runtime.RunCancellationIntent{}, fmt.Errorf(
			"%w: run cancellation intent run %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, intent.RunID, runProjectID, intent.ProjectID,
		)
	}

	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO run_cancellation_intents (id, project_id, run_id, actor, reason, state, requested_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(intent.ID), string(intent.ProjectID), string(intent.RunID), intent.Actor, intent.Reason,
		string(intent.State), formatWorkflowTime(intent.RequestedAt),
	)
	if insertErr == nil {
		return intent, nil
	}
	existing, loadErr := loadRunCancellationIntentTx(ctx, tx, string(intent.RunID))
	if loadErr != nil {
		return runtime.RunCancellationIntent{}, MapSQLiteError(fmt.Errorf("record run cancellation intent: %w", insertErr))
	}
	return existing, nil
}

// GetRunCancellationIntent implements ports.RuntimeRepository.
func (r runtimeRepository) GetRunCancellationIntent(ctx context.Context, runID string) (runtime.RunCancellationIntent, error) {
	return loadRunCancellationIntentTx(ctx, r.tx, runID)
}

func loadRunCancellationIntentTx(ctx context.Context, tx *sql.Tx, runID string) (runtime.RunCancellationIntent, error) {
	var (
		id             string
		projectID      string
		actor          string
		reason         string
		state          string
		requestedAtRaw string
	)
	err := tx.QueryRowContext(ctx, `
SELECT id, project_id, actor, reason, state, requested_at
FROM run_cancellation_intents WHERE run_id = ?`, runID).Scan(&id, &projectID, &actor, &reason, &state, &requestedAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.RunCancellationIntent{}, fmt.Errorf("%w: run cancellation intent for run %s", ports.ErrPersistenceNotFound, runID)
	}
	if err != nil {
		return runtime.RunCancellationIntent{}, MapSQLiteError(fmt.Errorf("load run cancellation intent: %w", err))
	}
	requestedAt, err := parseWorkflowTime(requestedAtRaw)
	if err != nil {
		return runtime.RunCancellationIntent{}, err
	}
	return runtime.RunCancellationIntent{
		ID: runtime.RunCancellationIntentID(id), ProjectID: project.ProjectID(projectID), RunID: runtime.WorkflowRunID(runID),
		Actor: actor, Reason: reason, State: runtime.CancellationIntentState(state), RequestedAt: requestedAt,
	}, nil
}

// --- WorkItemCancellationIntent ---

// RecordWorkItemCancellationIntent implements ports.RuntimeRepository
// (V4-01, ADR-020). Idempotent by WorkItemID, mirroring
// RecordRunCancellationIntent exactly.
func (r runtimeRepository) RecordWorkItemCancellationIntent(ctx context.Context, intent runtime.WorkItemCancellationIntent) (runtime.WorkItemCancellationIntent, error) {
	return recordWorkItemCancellationIntentTx(ctx, r.tx, intent)
}

func recordWorkItemCancellationIntentTx(ctx context.Context, tx *sql.Tx, intent runtime.WorkItemCancellationIntent) (runtime.WorkItemCancellationIntent, error) {
	var workItemProjectID string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM work_items WHERE id = ?`, string(intent.WorkItemID)).Scan(&workItemProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.WorkItemCancellationIntent{}, fmt.Errorf("%w: work item %s", ports.ErrPersistenceNotFound, intent.WorkItemID)
	}
	if err != nil {
		return runtime.WorkItemCancellationIntent{}, MapSQLiteError(fmt.Errorf("resolve work item cancellation intent work item: %w", err))
	}
	if workItemProjectID != string(intent.ProjectID) {
		return runtime.WorkItemCancellationIntent{}, fmt.Errorf(
			"%w: work item cancellation intent work item %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, intent.WorkItemID, workItemProjectID, intent.ProjectID,
		)
	}

	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO work_item_cancellation_intents (id, project_id, work_item_id, actor, reason, state, requested_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(intent.ID), string(intent.ProjectID), string(intent.WorkItemID), intent.Actor, intent.Reason,
		string(intent.State), formatWorkflowTime(intent.RequestedAt),
	)
	if insertErr == nil {
		return intent, nil
	}
	existing, loadErr := loadWorkItemCancellationIntentTx(ctx, tx, string(intent.WorkItemID))
	if loadErr != nil {
		return runtime.WorkItemCancellationIntent{}, MapSQLiteError(fmt.Errorf("record work item cancellation intent: %w", insertErr))
	}
	return existing, nil
}

// GetWorkItemCancellationIntent implements ports.RuntimeRepository.
func (r runtimeRepository) GetWorkItemCancellationIntent(ctx context.Context, workItemID string) (runtime.WorkItemCancellationIntent, error) {
	return loadWorkItemCancellationIntentTx(ctx, r.tx, workItemID)
}

func loadWorkItemCancellationIntentTx(ctx context.Context, tx *sql.Tx, workItemID string) (runtime.WorkItemCancellationIntent, error) {
	var (
		id             string
		projectID      string
		actor          string
		reason         string
		state          string
		requestedAtRaw string
	)
	err := tx.QueryRowContext(ctx, `
SELECT id, project_id, actor, reason, state, requested_at
FROM work_item_cancellation_intents WHERE work_item_id = ?`, workItemID).Scan(&id, &projectID, &actor, &reason, &state, &requestedAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.WorkItemCancellationIntent{}, fmt.Errorf("%w: work item cancellation intent for work item %s", ports.ErrPersistenceNotFound, workItemID)
	}
	if err != nil {
		return runtime.WorkItemCancellationIntent{}, MapSQLiteError(fmt.Errorf("load work item cancellation intent: %w", err))
	}
	requestedAt, err := parseWorkflowTime(requestedAtRaw)
	if err != nil {
		return runtime.WorkItemCancellationIntent{}, err
	}
	return runtime.WorkItemCancellationIntent{
		ID: runtime.WorkItemCancellationIntentID(id), ProjectID: project.ProjectID(projectID), WorkItemID: work.WorkItemID(workItemID),
		Actor: actor, Reason: reason, State: runtime.CancellationIntentState(state), RequestedAt: requestedAt,
	}, nil
}
