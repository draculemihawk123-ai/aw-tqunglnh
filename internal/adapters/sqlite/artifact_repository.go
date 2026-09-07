package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// This file gives artifactRepository its methods (V5-01,
// docs/design/07-v5-execution-evidence.md; ADR-017) — the durable Artifact
// metadata row over V1's content-addressed ArtifactStore. Follows
// work_item_blocker.go's own Tx-composable pattern exactly: one
// xxxTx(ctx, tx *sql.Tx, ...) function per operation, plus a thin
// artifactRepository method that forwards to it.
type artifactRepository struct{ tx *sql.Tx }

var _ ports.ArtifactRepository = artifactRepository{}

// InsertArtifact implements ports.ArtifactRepository.
func (r artifactRepository) InsertArtifact(ctx context.Context, a artifact.Artifact) (artifact.Artifact, error) {
	return insertArtifactTx(ctx, r.tx, a)
}

func insertArtifactTx(ctx context.Context, tx *sql.Tx, a artifact.Artifact) (artifact.Artifact, error) {
	var projectExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, string(a.ProjectID)).Scan(&projectExists)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.Artifact{}, fmt.Errorf("%w: project %s", ports.ErrPersistenceNotFound, a.ProjectID)
	}
	if err != nil {
		return artifact.Artifact{}, MapSQLiteError(fmt.Errorf("resolve artifact project: %w", err))
	}

	sensitivity, err := sensitivityToDB(a.Sensitivity)
	if err != nil {
		return artifact.Artifact{}, err
	}
	var expiresAt any
	if a.ExpiresAt != nil {
		expiresAt = formatWorkflowTime(*a.ExpiresAt)
	}

	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO artifacts (id, project_id, locator, content_hash, size, media_type, sensitivity, redacted,
                        retention_class, attach_state, hold, expires_at, created_at, version)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(a.ID), string(a.ProjectID), a.Locator, a.ContentHash, a.Size, a.MediaType, sensitivity, a.Redacted,
		string(a.RetentionClass), string(a.AttachState), a.Hold, expiresAt, formatWorkflowTime(a.CreatedAt), a.Version,
	)
	if insertErr == nil {
		return a, nil
	}
	existing, loadErr := loadArtifactTx(ctx, tx, string(a.ID))
	if loadErr != nil {
		return artifact.Artifact{}, MapSQLiteError(fmt.Errorf("insert artifact: %w", insertErr))
	}
	return existing, nil
}

// GetArtifact implements ports.ArtifactRepository.
func (r artifactRepository) GetArtifact(ctx context.Context, id string) (artifact.Artifact, error) {
	return loadArtifactTx(ctx, r.tx, id)
}

func loadArtifactTx(ctx context.Context, tx *sql.Tx, id string) (artifact.Artifact, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, locator, content_hash, size, media_type, sensitivity, redacted,
       retention_class, attach_state, hold, expires_at, created_at, version
FROM artifacts WHERE id = ?`, id)
	a, err := scanArtifactRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.Artifact{}, fmt.Errorf("%w: artifact %s", ports.ErrPersistenceNotFound, id)
	}
	return a, err
}

func scanArtifactRow(row repositoryRowScanner) (artifact.Artifact, error) {
	var id, projectID, locator, contentHash, mediaType, sensitivityRaw string
	var retentionClass, attachState, createdAtRaw string
	var redacted, hold bool
	var size int64
	var version uint64
	var expiresAtRaw sql.NullString
	if err := row.Scan(
		&id, &projectID, &locator, &contentHash, &size, &mediaType, &sensitivityRaw, &redacted,
		&retentionClass, &attachState, &hold, &expiresAtRaw, &createdAtRaw, &version,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return artifact.Artifact{}, err
		}
		return artifact.Artifact{}, MapSQLiteError(fmt.Errorf("scan artifact row: %w", err))
	}
	sensitivity, err := sensitivityFromDB(sensitivityRaw)
	if err != nil {
		return artifact.Artifact{}, err
	}
	createdAt, err := parseWorkflowTime(createdAtRaw)
	if err != nil {
		return artifact.Artifact{}, err
	}
	var expiresAt *time.Time
	if expiresAtRaw.Valid {
		parsed, err := parseWorkflowTime(expiresAtRaw.String)
		if err != nil {
			return artifact.Artifact{}, err
		}
		expiresAt = &parsed
	}
	return artifact.NewArtifact(
		artifact.ID(id), project.ProjectID(projectID), locator, contentHash, size, mediaType,
		sensitivity, redacted, artifact.RetentionClass(retentionClass), artifact.AttachState(attachState),
		hold, expiresAt, createdAt, version,
	)
}

// TransitionArtifactAttachState implements ports.ArtifactRepository: the
// fenced CAS that closes an artifact's own Orphan/Attached lifecycle.
func (r artifactRepository) TransitionArtifactAttachState(ctx context.Context, req ports.TransitionArtifactAttachStateRequest) (artifact.Artifact, error) {
	result, err := r.tx.ExecContext(ctx, `
UPDATE artifacts SET attach_state = ?, version = version + 1
WHERE id = ? AND attach_state = ? AND version = ?`,
		string(req.NextState), req.ArtifactID, string(req.ExpectedState), req.ExpectedVersion,
	)
	if err != nil {
		return artifact.Artifact{}, MapSQLiteError(fmt.Errorf("transition artifact attach state %s: %w", req.ArtifactID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return artifact.Artifact{}, fmt.Errorf("read artifact attach state transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM artifacts WHERE id = ?`, req.ArtifactID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return artifact.Artifact{}, fmt.Errorf("%w: artifact %s", ports.ErrPersistenceNotFound, req.ArtifactID)
		}
		if lookupErr != nil {
			return artifact.Artifact{}, MapSQLiteError(fmt.Errorf("check stale artifact attach state transition: %w", lookupErr))
		}
		return artifact.Artifact{}, fmt.Errorf(
			"%w: artifact %s expected %s@%d", ports.ErrOptimisticConflict, req.ArtifactID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	return loadArtifactTx(ctx, r.tx, req.ArtifactID)
}

// SetArtifactHold implements ports.ArtifactRepository: the fenced CAS that
// flips Hold independent of AttachState.
func (r artifactRepository) SetArtifactHold(ctx context.Context, req ports.SetArtifactHoldRequest) (artifact.Artifact, error) {
	result, err := r.tx.ExecContext(ctx, `
UPDATE artifacts SET hold = ?, version = version + 1
WHERE id = ? AND version = ?`,
		req.Hold, req.ArtifactID, req.ExpectedVersion,
	)
	if err != nil {
		return artifact.Artifact{}, MapSQLiteError(fmt.Errorf("set artifact hold %s: %w", req.ArtifactID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return artifact.Artifact{}, fmt.Errorf("read artifact hold update result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM artifacts WHERE id = ?`, req.ArtifactID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return artifact.Artifact{}, fmt.Errorf("%w: artifact %s", ports.ErrPersistenceNotFound, req.ArtifactID)
		}
		if lookupErr != nil {
			return artifact.Artifact{}, MapSQLiteError(fmt.Errorf("check stale artifact hold update: %w", lookupErr))
		}
		return artifact.Artifact{}, fmt.Errorf(
			"%w: artifact %s expected version %d", ports.ErrOptimisticConflict, req.ArtifactID, req.ExpectedVersion,
		)
	}
	return loadArtifactTx(ctx, r.tx, req.ArtifactID)
}

// ListOrphanedArtifacts implements ports.ArtifactRepository, ordered by
// (created_at, id) for a stable, deterministic result a test can assert on
// exactly.
func (r artifactRepository) ListOrphanedArtifacts(ctx context.Context, olderThan time.Time) ([]artifact.Artifact, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT id FROM artifacts WHERE attach_state = ? AND created_at <= ? ORDER BY created_at, id`,
		string(artifact.Orphan), formatWorkflowTime(olderThan),
	)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list orphaned artifacts: %w", err))
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan orphaned artifact id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate orphaned artifact ids: %w", err)
	}
	rows.Close()

	artifacts := make([]artifact.Artifact, 0, len(ids))
	for _, id := range ids {
		a, err := loadArtifactTx(ctx, r.tx, id)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, nil
}

// sensitivityToDB/sensitivityFromDB round-trip redact.Sensitivity (a plain
// int enum with no String() method of its own) through this table's own
// closed-set TEXT column — no other adapter in this codebase persists
// redact.Sensitivity yet (V5-01 is its first production wiring), so there
// is no existing convention to reuse.
func sensitivityToDB(s redact.Sensitivity) (string, error) {
	switch s {
	case redact.Public:
		return "PUBLIC", nil
	case redact.Sensitive:
		return "SENSITIVE", nil
	case redact.Secret:
		return "SECRET", nil
	default:
		return "", fmt.Errorf("sqlite: unknown artifact sensitivity %d", s)
	}
}

func sensitivityFromDB(raw string) (redact.Sensitivity, error) {
	switch raw {
	case "PUBLIC":
		return redact.Public, nil
	case "SENSITIVE":
		return redact.Sensitive, nil
	case "SECRET":
		return redact.Secret, nil
	default:
		return 0, fmt.Errorf("sqlite: unknown stored artifact sensitivity %q", raw)
	}
}
