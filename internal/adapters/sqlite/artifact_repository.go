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

	// V5-14: refuse a fresh insert against a Locator a sweep is currently
	// mid-purge on — see ClaimArtifactLocatorForPurge's own doc comment for
	// why this check exists. Read fresh, inside this same transaction, so
	// it observes any claim already committed by a concurrent sweep.
	var claimed int
	claimErr := tx.QueryRowContext(ctx, `SELECT 1 FROM artifact_locator_purge_claims WHERE locator = ?`, a.Locator).Scan(&claimed)
	if claimErr == nil {
		return artifact.Artifact{}, fmt.Errorf("%w: locator %s is claimed for purge", ports.ErrPersistenceAlreadyExists, a.Locator)
	}
	if !errors.Is(claimErr, sql.ErrNoRows) {
		return artifact.Artifact{}, MapSQLiteError(fmt.Errorf("check artifact locator purge claim: %w", claimErr))
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

// ListArtifactsByLocator implements ports.ArtifactRepository.
func (r artifactRepository) ListArtifactsByLocator(ctx context.Context, locator string) ([]artifact.Artifact, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT id FROM artifacts WHERE locator = ? ORDER BY id`, locator)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list artifacts by locator: %w", err))
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan artifact id by locator: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate artifact ids by locator: %w", err)
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

// ClaimArtifactLocatorForPurge implements ports.ArtifactRepository: a plain
// INSERT against the claim table's own PRIMARY KEY(locator) — a second
// claim attempt against an already-claimed Locator hits that same PK and
// is mapped to ErrPersistenceAlreadyExists, exactly like every other
// idempotency-key/PK conflict in this codebase.
func (r artifactRepository) ClaimArtifactLocatorForPurge(ctx context.Context, locator, claimOwner string, claimedAt time.Time) error {
	if _, err := r.tx.ExecContext(ctx, `
INSERT INTO artifact_locator_purge_claims (locator, claim_owner, claimed_at) VALUES (?, ?, ?)`,
		locator, claimOwner, formatWorkflowTime(claimedAt),
	); err != nil {
		var existing int
		lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM artifact_locator_purge_claims WHERE locator = ?`, locator).Scan(&existing)
		if lookupErr == nil {
			return fmt.Errorf("%w: artifact locator %s", ports.ErrPersistenceAlreadyExists, locator)
		}
		return MapSQLiteError(fmt.Errorf("claim artifact locator %s for purge: %w", locator, err))
	}
	return nil
}

// ReleaseArtifactLocatorClaim implements ports.ArtifactRepository.
// Idempotent: a Locator with no open claim is left exactly as it is
// (zero rows affected is not an error).
func (r artifactRepository) ReleaseArtifactLocatorClaim(ctx context.Context, locator string) error {
	if _, err := r.tx.ExecContext(ctx, `DELETE FROM artifact_locator_purge_claims WHERE locator = ?`, locator); err != nil {
		return MapSQLiteError(fmt.Errorf("release artifact locator %s purge claim: %w", locator, err))
	}
	return nil
}

// GetArtifactSweepState implements ports.ArtifactRepository, mirroring
// runtimeRepository.GetRecoveryReaperState exactly.
func (r artifactRepository) GetArtifactSweepState(ctx context.Context) (ports.ArtifactSweepState, error) {
	var generation, version uint64
	var dryRun bool
	err := r.tx.QueryRowContext(ctx,
		`SELECT generation, dry_run, version FROM artifact_sweep_state WHERE id = 'singleton'`,
	).Scan(&generation, &dryRun, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ArtifactSweepState{}, fmt.Errorf("%w: artifact sweep state", ports.ErrPersistenceNotFound)
	}
	if err != nil {
		return ports.ArtifactSweepState{}, MapSQLiteError(fmt.Errorf("load artifact sweep state: %w", err))
	}
	return ports.ArtifactSweepState{Generation: generation, DryRun: dryRun, Version: version}, nil
}

// AdvanceArtifactSweepGeneration implements ports.ArtifactRepository: the
// fenced CAS that bumps Generation by exactly one, mirroring
// runtimeRepository.AdvanceRecoveryReaperGeneration exactly.
func (r artifactRepository) AdvanceArtifactSweepGeneration(ctx context.Context, req ports.AdvanceArtifactSweepGenerationRequest) (ports.ArtifactSweepState, error) {
	now := formatWorkflowTime(time.Now().UTC())
	result, err := r.tx.ExecContext(ctx, `
UPDATE artifact_sweep_state
SET generation = generation + 1, version = version + 1, updated_at = ?
WHERE id = 'singleton' AND generation = ? AND version = ?`,
		now, req.ExpectedGeneration, req.ExpectedVersion,
	)
	if err != nil {
		return ports.ArtifactSweepState{}, MapSQLiteError(fmt.Errorf("advance artifact sweep generation: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ports.ArtifactSweepState{}, fmt.Errorf("read artifact sweep generation advance result: %w", err)
	}
	if affected != 1 {
		return ports.ArtifactSweepState{}, fmt.Errorf(
			"%w: artifact sweep state expected generation=%d version=%d",
			ports.ErrOptimisticConflict, req.ExpectedGeneration, req.ExpectedVersion,
		)
	}
	return r.GetArtifactSweepState(ctx)
}

// SetArtifactSweepDryRun implements ports.ArtifactRepository: the fenced
// CAS an explicit operator action uses to flip DryRun.
func (r artifactRepository) SetArtifactSweepDryRun(ctx context.Context, req ports.SetArtifactSweepDryRunRequest) (ports.ArtifactSweepState, error) {
	now := formatWorkflowTime(time.Now().UTC())
	result, err := r.tx.ExecContext(ctx, `
UPDATE artifact_sweep_state
SET dry_run = ?, version = version + 1, updated_at = ?
WHERE id = 'singleton' AND version = ?`,
		req.DryRun, now, req.ExpectedVersion,
	)
	if err != nil {
		return ports.ArtifactSweepState{}, MapSQLiteError(fmt.Errorf("set artifact sweep dry run: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ports.ArtifactSweepState{}, fmt.Errorf("read artifact sweep dry run update result: %w", err)
	}
	if affected != 1 {
		return ports.ArtifactSweepState{}, fmt.Errorf(
			"%w: artifact sweep state expected version=%d", ports.ErrOptimisticConflict, req.ExpectedVersion,
		)
	}
	return r.GetArtifactSweepState(ctx)
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
