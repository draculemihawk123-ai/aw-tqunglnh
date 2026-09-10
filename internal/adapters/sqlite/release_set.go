package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// This file gives workRepository its ReleaseSet methods (V5-10A,
// docs/design/07-v5-execution-evidence.md; AK-ARCH-015C). Follows
// work_item_blocker.go's own Tx-composable pattern exactly: one
// xxxTx(ctx, tx *sql.Tx, ...) function per operation, plus a thin
// workRepository method that forwards to it. release_sets holds the
// ReleaseSet's own header row; release_set_repositories (a child table)
// holds its own per-repository entries — inserted/loaded together inside
// the same transaction, never independently.

// CreateReleaseSet implements ports.WorkRepository (V5-10A). Idempotent by
// ID: a duplicate insert (the same deterministic ID a real caller mints)
// returns the already-stored row rather than erroring, the identical
// discipline createWorkItemBlockerTx already establishes.
func (r workRepository) CreateReleaseSet(ctx context.Context, releaseSet work.ReleaseSet) (work.ReleaseSet, error) {
	return createReleaseSetTx(ctx, r.tx, releaseSet)
}

func createReleaseSetTx(ctx context.Context, tx *sql.Tx, releaseSet work.ReleaseSet) (work.ReleaseSet, error) {
	var familyExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM task_families WHERE id = ?`, string(releaseSet.FamilyID)).Scan(&familyExists)
	if errors.Is(err, sql.ErrNoRows) {
		return work.ReleaseSet{}, fmt.Errorf("%w: task family %s", ports.ErrPersistenceNotFound, releaseSet.FamilyID)
	}
	if err != nil {
		return work.ReleaseSet{}, MapSQLiteError(fmt.Errorf("resolve release set task family: %w", err))
	}

	// Every entry must name a repository that already exists —
	// release_set_repositories.repository_id itself REFERENCES
	// repositories(id) (migration 0030), but checked explicitly here first
	// so a bad reference surfaces as ports.ErrPersistenceNotFound rather
	// than an opaque foreign-key-violation error from the insert below.
	for _, entry := range releaseSet.Entries() {
		var repositoryExists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM repositories WHERE id = ?`, string(entry.RepositoryID)).Scan(&repositoryExists)
		if errors.Is(err, sql.ErrNoRows) {
			return work.ReleaseSet{}, fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, entry.RepositoryID)
		}
		if err != nil {
			return work.ReleaseSet{}, MapSQLiteError(fmt.Errorf("resolve release set repository %s: %w", entry.RepositoryID, err))
		}
	}

	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO release_sets (id, project_id, family_id, state, content_hash, created_at, version)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(releaseSet.ID), string(releaseSet.ProjectID), string(releaseSet.FamilyID), string(releaseSet.State),
		releaseSet.ContentHash(), formatWorkflowTime(releaseSet.CreatedAt), releaseSet.Version,
	)
	if insertErr != nil {
		existing, loadErr := loadReleaseSetTx(ctx, tx, string(releaseSet.ID))
		if loadErr != nil {
			return work.ReleaseSet{}, MapSQLiteError(fmt.Errorf("create release set: %w", insertErr))
		}
		return existing, nil
	}

	for _, entry := range releaseSet.Entries() {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO release_set_repositories (release_set_id, repository_id, base_vcs_object_id, result_vcs_object_id, verdict)
VALUES (?, ?, ?, ?, ?)`,
			string(releaseSet.ID), string(entry.RepositoryID), entry.BaseVCSObjectID, entry.ResultVCSObjectID, string(entry.Verdict),
		); err != nil {
			return work.ReleaseSet{}, MapSQLiteError(fmt.Errorf("insert release for repository %s: %w", entry.RepositoryID, err))
		}
	}
	return releaseSet, nil
}

// GetReleaseSet implements ports.WorkRepository.
func (r workRepository) GetReleaseSet(ctx context.Context, id string) (work.ReleaseSet, error) {
	return loadReleaseSetTx(ctx, r.tx, id)
}

func loadReleaseSetTx(ctx context.Context, tx *sql.Tx, id string) (work.ReleaseSet, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, family_id, state, created_at, sealed_at, abandoned_at, version
FROM release_sets WHERE id = ?`, id)

	var releaseSetID, projectID, familyID, state, createdAtRaw string
	var sealedAtRaw, abandonedAtRaw sql.NullString
	var version uint64
	if err := row.Scan(&releaseSetID, &projectID, &familyID, &state, &createdAtRaw, &sealedAtRaw, &abandonedAtRaw, &version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return work.ReleaseSet{}, fmt.Errorf("%w: release set %s", ports.ErrPersistenceNotFound, id)
		}
		return work.ReleaseSet{}, MapSQLiteError(fmt.Errorf("scan release set row: %w", err))
	}
	createdAt, err := parseWorkflowTime(createdAtRaw)
	if err != nil {
		return work.ReleaseSet{}, err
	}

	entryRows, err := tx.QueryContext(ctx, `
SELECT repository_id, base_vcs_object_id, result_vcs_object_id, verdict
FROM release_set_repositories WHERE release_set_id = ? ORDER BY repository_id`, id)
	if err != nil {
		return work.ReleaseSet{}, MapSQLiteError(fmt.Errorf("list release set repositories for %s: %w", id, err))
	}
	var entries []work.RepositoryRelease
	for entryRows.Next() {
		var repositoryID, baseVCSObjectID, resultVCSObjectID, verdict string
		if err := entryRows.Scan(&repositoryID, &baseVCSObjectID, &resultVCSObjectID, &verdict); err != nil {
			entryRows.Close()
			return work.ReleaseSet{}, fmt.Errorf("scan release set repository row: %w", err)
		}
		entries = append(entries, work.RepositoryRelease{
			RepositoryID: project.RepositoryID(repositoryID), BaseVCSObjectID: baseVCSObjectID,
			ResultVCSObjectID: resultVCSObjectID, Verdict: gate.Verdict(verdict),
		})
	}
	if err := entryRows.Err(); err != nil {
		entryRows.Close()
		return work.ReleaseSet{}, fmt.Errorf("iterate release set repository rows: %w", err)
	}
	entryRows.Close()

	releaseSet, err := work.NewReleaseSet(work.ReleaseSetID(releaseSetID), project.ProjectID(projectID), work.TaskFamilyID(familyID), entries, createdAt)
	if err != nil {
		return work.ReleaseSet{}, fmt.Errorf("rebuild release set %s from storage: %w", id, err)
	}
	releaseSet.State = work.ReleaseSetState(state)
	releaseSet.Version = version
	if sealedAtRaw.Valid {
		sealedAt, err := parseWorkflowTime(sealedAtRaw.String)
		if err != nil {
			return work.ReleaseSet{}, err
		}
		releaseSet.SealedAt = &sealedAt
	}
	if abandonedAtRaw.Valid {
		abandonedAt, err := parseWorkflowTime(abandonedAtRaw.String)
		if err != nil {
			return work.ReleaseSet{}, err
		}
		releaseSet.AbandonedAt = &abandonedAt
	}
	return releaseSet, nil
}

// ListReleaseSetsForFamily implements ports.WorkRepository (V5-10A),
// ordered by (created_at, id) for a stable, deterministic result.
func (r workRepository) ListReleaseSetsForFamily(ctx context.Context, familyID string) ([]work.ReleaseSet, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT id FROM release_sets WHERE family_id = ? ORDER BY created_at, id`, familyID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list release sets for family %s: %w", familyID, err))
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan release set id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate release set ids: %w", err)
	}
	rows.Close()

	releaseSets := make([]work.ReleaseSet, 0, len(ids))
	for _, id := range ids {
		releaseSet, err := loadReleaseSetTx(ctx, r.tx, id)
		if err != nil {
			return nil, err
		}
		releaseSets = append(releaseSets, releaseSet)
	}
	return releaseSets, nil
}

// TransitionReleaseSetState implements ports.WorkRepository (V5-10A): the
// fenced CAS that closes a ReleaseSet's own lifecycle — CREATED -> SEALED
// or CREATED -> ABANDONED.
func (r workRepository) TransitionReleaseSetState(ctx context.Context, req ports.TransitionReleaseSetStateRequest) (work.ReleaseSet, error) {
	var sealedAt, abandonedAt any
	switch req.NextState {
	case work.ReleaseSetSealed:
		sealedAt = formatWorkflowTime(req.OccurredAt)
	case work.ReleaseSetAbandoned:
		abandonedAt = formatWorkflowTime(req.OccurredAt)
	}

	result, err := r.tx.ExecContext(ctx, `
UPDATE release_sets
SET state = ?, sealed_at = ?, abandoned_at = ?, version = version + 1
WHERE id = ? AND state = ? AND version = ?`,
		string(req.NextState), sealedAt, abandonedAt,
		req.ReleaseSetID, string(req.ExpectedState), req.ExpectedVersion,
	)
	if err != nil {
		return work.ReleaseSet{}, MapSQLiteError(fmt.Errorf("transition release set %s: %w", req.ReleaseSetID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return work.ReleaseSet{}, fmt.Errorf("read release set transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM release_sets WHERE id = ?`, req.ReleaseSetID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return work.ReleaseSet{}, fmt.Errorf("%w: release set %s", ports.ErrPersistenceNotFound, req.ReleaseSetID)
		}
		if lookupErr != nil {
			return work.ReleaseSet{}, MapSQLiteError(fmt.Errorf("check stale release set transition: %w", lookupErr))
		}
		return work.ReleaseSet{}, fmt.Errorf(
			"%w: release set %s expected %s@%d", ports.ErrOptimisticConflict, req.ReleaseSetID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	return loadReleaseSetTx(ctx, r.tx, req.ReleaseSetID)
}
