package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file gives runtimeRepository its Evidence methods (V5-09/V5-10
// acceptance-gap remediation, 2026-09-10 post-merge review). It reuses the
// `evidence` table migration 0001 already declared — a durable, full-
// lineage record per terminal criterion (MACHINE_GATE) or execution
// (COMMAND/AGENT), written for the first time by this remediation.
// Encodes Revisions/ArtifactReferences as JSON exactly like checkpoints'
// own revision_set_json/artifact_refs_json (checkpoint_store.go).

// CreateEvidence implements ports.RuntimeRepository. Idempotent by ID: a
// duplicate insert (the same deterministic ID a redelivered finalize
// re-derives) returns the already-stored row rather than erroring — the
// identical discipline createWorkItemBlockerTx/createReleaseSetTx already
// establish.
func (r runtimeRepository) CreateEvidence(ctx context.Context, evidence runtime.Evidence) (runtime.Evidence, error) {
	return createEvidenceTx(ctx, r.tx, evidence)
}

func createEvidenceTx(ctx context.Context, tx *sql.Tx, evidence runtime.Evidence) (runtime.Evidence, error) {
	revisionJSON, err := json.Marshal(evidence.Revisions.Entries())
	if err != nil {
		return runtime.Evidence{}, fmt.Errorf("encode evidence revisions: %w", err)
	}
	artifactJSON, err := json.Marshal(evidence.ArtifactReferences)
	if err != nil {
		return runtime.Evidence{}, fmt.Errorf("encode evidence artifact manifest: %w", err)
	}
	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO evidence (
    id, project_id, work_item_id, run_id, node_run_id, attempt_id, kind, verdict,
    artifact_manifest_json, revision_set_json, policy_version, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(evidence.ID), string(evidence.ProjectID), string(evidence.WorkItemID), string(evidence.RunID),
		string(evidence.NodeRunID), string(evidence.AttemptID), evidence.Kind, evidence.Verdict,
		string(artifactJSON), string(revisionJSON), evidence.PolicyVersion, formatWorkflowTime(evidence.CreatedAt),
	)
	if insertErr != nil {
		existing, loadErr := loadEvidenceTx(ctx, tx, string(evidence.ID))
		if loadErr != nil {
			return runtime.Evidence{}, MapSQLiteError(fmt.Errorf("create evidence: %w", insertErr))
		}
		return existing, nil
	}
	return evidence, nil
}

// GetEvidence implements ports.RuntimeRepository.
func (r runtimeRepository) GetEvidence(ctx context.Context, id string) (runtime.Evidence, error) {
	return loadEvidenceTx(ctx, r.tx, id)
}

func loadEvidenceTx(ctx context.Context, tx *sql.Tx, id string) (runtime.Evidence, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, work_item_id, run_id, node_run_id, attempt_id, kind, verdict,
       artifact_manifest_json, revision_set_json, policy_version, created_at
FROM evidence WHERE id = ?`, id)
	return scanEvidenceRow(row, id)
}

// ListEvidenceForAttempt implements ports.RuntimeRepository, ordered by
// Kind for a stable, deterministic result.
func (r runtimeRepository) ListEvidenceForAttempt(ctx context.Context, attemptID string) ([]runtime.Evidence, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT id, project_id, work_item_id, run_id, node_run_id, attempt_id, kind, verdict,
       artifact_manifest_json, revision_set_json, policy_version, created_at
FROM evidence WHERE attempt_id = ? ORDER BY kind`, attemptID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list evidence for attempt %s: %w", attemptID, err))
	}
	defer rows.Close()

	var result []runtime.Evidence
	for rows.Next() {
		evidence, err := scanEvidenceRowFromRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, evidence)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence rows for attempt %s: %w", attemptID, err)
	}
	return result, nil
}

// ListEvidenceForWorkItem implements ports.RuntimeRepository (V6-07B),
// ordered by (created_at, kind) for a stable, deterministic result.
func (r runtimeRepository) ListEvidenceForWorkItem(ctx context.Context, workItemID string) ([]runtime.Evidence, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT id, project_id, work_item_id, run_id, node_run_id, attempt_id, kind, verdict,
       artifact_manifest_json, revision_set_json, policy_version, created_at
FROM evidence WHERE work_item_id = ? ORDER BY created_at, kind`, workItemID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list evidence for work item %s: %w", workItemID, err))
	}
	defer rows.Close()

	var result []runtime.Evidence
	for rows.Next() {
		evidence, err := scanEvidenceRowFromRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, evidence)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence rows for work item %s: %w", workItemID, err)
	}
	return result, nil
}

type evidenceRowScanner interface {
	Scan(dest ...any) error
}

func scanEvidenceRow(row *sql.Row, id string) (runtime.Evidence, error) {
	evidence, err := scanEvidenceRowFromRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.Evidence{}, fmt.Errorf("%w: evidence %s", ports.ErrPersistenceNotFound, id)
	}
	return evidence, err
}

func scanEvidenceRowFromRows(scanner evidenceRowScanner) (runtime.Evidence, error) {
	var (
		id, projectID, workItemID, runID, nodeRunID, attemptID string
		kind, verdict                                          string
		artifactJSON, revisionJSON                             string
		policyVersion, createdAtRaw                            string
	)
	if err := scanner.Scan(
		&id, &projectID, &workItemID, &runID, &nodeRunID, &attemptID, &kind, &verdict,
		&artifactJSON, &revisionJSON, &policyVersion, &createdAtRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runtime.Evidence{}, err
		}
		return runtime.Evidence{}, fmt.Errorf("scan evidence row: %w", err)
	}
	var artifactReferences []string
	if err := json.Unmarshal([]byte(artifactJSON), &artifactReferences); err != nil {
		return runtime.Evidence{}, fmt.Errorf("decode evidence artifact manifest: %w", err)
	}
	var revisions []workspace.Revision
	if err := json.Unmarshal([]byte(revisionJSON), &revisions); err != nil {
		return runtime.Evidence{}, fmt.Errorf("decode evidence revisions: %w", err)
	}
	revisionSet, err := workspace.NewRevisionSet(revisions)
	if err != nil {
		return runtime.Evidence{}, err
	}
	createdAt, err := parseWorkflowTime(createdAtRaw)
	if err != nil {
		return runtime.Evidence{}, err
	}
	return runtime.NewEvidence(
		runtime.EvidenceID(id), project.ProjectID(projectID), workdomain.WorkItemID(workItemID), runtime.WorkflowRunID(runID),
		runtime.NodeRunID(nodeRunID), runtime.ExecutionAttemptID(attemptID), kind, verdict, artifactReferences, revisionSet,
		policyVersion, createdAt,
	)
}
