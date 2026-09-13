package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// This file gives workRepository its ReleaseSetLocalCommit methods (V6-10E,
// docs/design/08-v6-api-projections.md V6-10E). Follows release_set.go's own
// Tx-composable pattern exactly: one xxxTx(ctx, tx *sql.Tx, ...) function per
// operation, plus a thin workRepository method that forwards to it.

// CreateReleaseSetLocalCommit implements ports.WorkRepository (V6-10E).
// Idempotent by ID (a duplicate insert of the same deterministic ID a real
// caller mints returns the already-stored row, mirroring
// createReleaseSetTx's own discipline). Before ever inserting, checks
// intent.Marker is not already used by a DIFFERENT id —
// ports.ErrLocalCommitMarkerCollision, checked explicitly rather than left
// to surface as an opaque UNIQUE-constraint violation from the insert below
// (release_set_local_commits.marker), the same "check tường minh trước khi
// insert" discipline createReleaseSetTx's own repository-existence check
// already established for this codebase's FK-vs-typed-error convention.
func (r workRepository) CreateReleaseSetLocalCommit(ctx context.Context, intent work.ReleaseSetLocalCommit) (work.ReleaseSetLocalCommit, error) {
	return createReleaseSetLocalCommitTx(ctx, r.tx, intent)
}

func createReleaseSetLocalCommitTx(ctx context.Context, tx *sql.Tx, intent work.ReleaseSetLocalCommit) (work.ReleaseSetLocalCommit, error) {
	var releaseSetExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM release_sets WHERE id = ?`, string(intent.ReleaseSetID)).Scan(&releaseSetExists)
	if errors.Is(err, sql.ErrNoRows) {
		return work.ReleaseSetLocalCommit{}, fmt.Errorf("%w: release set %s", ports.ErrPersistenceNotFound, intent.ReleaseSetID)
	}
	if err != nil {
		return work.ReleaseSetLocalCommit{}, MapSQLiteError(fmt.Errorf("resolve release set local commit release set: %w", err))
	}
	var workspaceExists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM repository_workspaces WHERE id = ?`, string(intent.RepositoryWorkspaceID)).Scan(&workspaceExists)
	if errors.Is(err, sql.ErrNoRows) {
		return work.ReleaseSetLocalCommit{}, fmt.Errorf("%w: repository workspace %s", ports.ErrPersistenceNotFound, intent.RepositoryWorkspaceID)
	}
	if err != nil {
		return work.ReleaseSetLocalCommit{}, MapSQLiteError(fmt.Errorf("resolve release set local commit repository workspace: %w", err))
	}

	var existingMarkerID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM release_set_local_commits WHERE marker = ?`, intent.Marker).Scan(&existingMarkerID)
	if err == nil && existingMarkerID != string(intent.ID) {
		return work.ReleaseSetLocalCommit{}, fmt.Errorf("%w: marker %s already used by operation %s", ports.ErrLocalCommitMarkerCollision, intent.Marker, existingMarkerID)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return work.ReleaseSetLocalCommit{}, MapSQLiteError(fmt.Errorf("check release set local commit marker: %w", err))
	}

	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO release_set_local_commits (
    id, project_id, release_set_id, expected_release_set_version, repository_workspace_id, repository_id,
    expected_generation, expected_workspace_version, actor, message, author_name, author_email, message_hash,
    marker, state, job_id, created_at, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(intent.ID), string(intent.ProjectID), string(intent.ReleaseSetID), intent.ExpectedReleaseSetVersion,
		string(intent.RepositoryWorkspaceID), string(intent.RepositoryID), intent.ExpectedGeneration, intent.ExpectedWorkspaceVersion,
		intent.Actor, intent.Message, intent.AuthorName, intent.AuthorEmail, intent.MessageHash, intent.Marker,
		string(intent.State), nullableString(intent.JobID), formatWorkflowTime(intent.CreatedAt), intent.Version,
	)
	if insertErr != nil {
		existing, loadErr := loadReleaseSetLocalCommitTx(ctx, tx, string(intent.ID))
		if loadErr != nil {
			return work.ReleaseSetLocalCommit{}, MapSQLiteError(fmt.Errorf("create release set local commit: %w", insertErr))
		}
		return existing, nil
	}
	return intent, nil
}

// GetReleaseSetLocalCommit implements ports.WorkRepository.
func (r workRepository) GetReleaseSetLocalCommit(ctx context.Context, id string) (work.ReleaseSetLocalCommit, error) {
	return loadReleaseSetLocalCommitTx(ctx, r.tx, id)
}

func loadReleaseSetLocalCommitTx(ctx context.Context, tx *sql.Tx, id string) (work.ReleaseSetLocalCommit, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, release_set_id, expected_release_set_version, repository_workspace_id, repository_id,
       expected_generation, expected_workspace_version, actor, message, author_name, author_email, message_hash,
       marker, state, failure_reason, parent_vcs_object_id, result_vcs_object_id, job_id, created_at, completed_at, version
FROM release_set_local_commits WHERE id = ?`, id)

	var intent work.ReleaseSetLocalCommit
	var releaseSetLocalCommitID, projectID, releaseSetID, repositoryWorkspaceID, repositoryID string
	var actor, message, authorName, authorEmail, messageHash, marker, state string
	var failureReason, parentVCSObjectID, resultVCSObjectID, jobID sql.NullString
	var createdAtRaw string
	var completedAtRaw sql.NullString
	if err := row.Scan(
		&releaseSetLocalCommitID, &projectID, &releaseSetID, &intent.ExpectedReleaseSetVersion, &repositoryWorkspaceID, &repositoryID,
		&intent.ExpectedGeneration, &intent.ExpectedWorkspaceVersion, &actor, &message, &authorName, &authorEmail, &messageHash,
		&marker, &state, &failureReason, &parentVCSObjectID, &resultVCSObjectID, &jobID, &createdAtRaw, &completedAtRaw, &intent.Version,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return work.ReleaseSetLocalCommit{}, fmt.Errorf("%w: release set local commit %s", ports.ErrPersistenceNotFound, id)
		}
		return work.ReleaseSetLocalCommit{}, MapSQLiteError(fmt.Errorf("scan release set local commit row: %w", err))
	}
	createdAt, err := parseWorkflowTime(createdAtRaw)
	if err != nil {
		return work.ReleaseSetLocalCommit{}, err
	}

	intent.ID = work.ReleaseSetLocalCommitID(releaseSetLocalCommitID)
	intent.ProjectID = project.ProjectID(projectID)
	intent.ReleaseSetID = work.ReleaseSetID(releaseSetID)
	intent.RepositoryWorkspaceID = repositoryWorkspaceID
	intent.RepositoryID = project.RepositoryID(repositoryID)
	intent.Actor = actor
	intent.Message = message
	intent.AuthorName = authorName
	intent.AuthorEmail = authorEmail
	intent.MessageHash = messageHash
	intent.Marker = marker
	intent.State = work.ReleaseSetLocalCommitState(state)
	intent.FailureReason = work.ReleaseSetLocalCommitFailureReason(failureReason.String)
	intent.ParentVCSObjectID = parentVCSObjectID.String
	intent.ResultVCSObjectID = resultVCSObjectID.String
	intent.JobID = jobID.String
	intent.CreatedAt = createdAt
	if completedAtRaw.Valid {
		completedAt, err := parseWorkflowTime(completedAtRaw.String)
		if err != nil {
			return work.ReleaseSetLocalCommit{}, err
		}
		intent.CompletedAt = &completedAt
	}
	return intent, nil
}

// PinReleaseSetLocalCommitParent implements ports.WorkRepository (V6-10E):
// see that method's own doc comment for the full contract. Never a terminal
// transition — State is left exactly as it was (must be REQUESTED, checked
// via the CAS WHERE clause itself, same as every other fenced update in
// this codebase).
func (r workRepository) PinReleaseSetLocalCommitParent(ctx context.Context, req ports.PinReleaseSetLocalCommitParentRequest) (work.ReleaseSetLocalCommit, error) {
	result, err := r.tx.ExecContext(ctx, `
UPDATE release_set_local_commits
SET parent_vcs_object_id = ?, version = version + 1
WHERE id = ? AND state = 'REQUESTED' AND version = ?`,
		req.ParentVCSObjectID, req.ReleaseSetLocalCommitID, req.ExpectedVersion,
	)
	if err != nil {
		return work.ReleaseSetLocalCommit{}, MapSQLiteError(fmt.Errorf("pin release set local commit %s parent: %w", req.ReleaseSetLocalCommitID, err))
	}
	if err := requireSingleRowAffected(ctx, r.tx, result, "release_set_local_commits", req.ReleaseSetLocalCommitID, req.ExpectedVersion); err != nil {
		return work.ReleaseSetLocalCommit{}, err
	}
	return loadReleaseSetLocalCommitTx(ctx, r.tx, req.ReleaseSetLocalCommitID)
}

// TransitionReleaseSetLocalCommitToCommitted implements ports.WorkRepository
// (V6-10E): the fenced CAS that closes this operation's own lifecycle as
// COMMITTED.
func (r workRepository) TransitionReleaseSetLocalCommitToCommitted(ctx context.Context, req ports.TransitionReleaseSetLocalCommitToCommittedRequest) (work.ReleaseSetLocalCommit, error) {
	result, err := r.tx.ExecContext(ctx, `
UPDATE release_set_local_commits
SET state = 'COMMITTED', parent_vcs_object_id = ?, result_vcs_object_id = ?, completed_at = ?, version = version + 1
WHERE id = ? AND state = 'REQUESTED' AND version = ?`,
		req.ParentVCSObjectID, req.ResultVCSObjectID, formatWorkflowTime(req.OccurredAt),
		req.ReleaseSetLocalCommitID, req.ExpectedVersion,
	)
	if err != nil {
		return work.ReleaseSetLocalCommit{}, MapSQLiteError(fmt.Errorf("commit release set local commit %s: %w", req.ReleaseSetLocalCommitID, err))
	}
	if err := requireSingleRowAffected(ctx, r.tx, result, "release_set_local_commits", req.ReleaseSetLocalCommitID, req.ExpectedVersion); err != nil {
		return work.ReleaseSetLocalCommit{}, err
	}
	return loadReleaseSetLocalCommitTx(ctx, r.tx, req.ReleaseSetLocalCommitID)
}

// TransitionReleaseSetLocalCommitToFailed implements ports.WorkRepository
// (V6-10E): the fenced CAS that closes this operation's own lifecycle as
// FAILED with a typed FailureReason.
func (r workRepository) TransitionReleaseSetLocalCommitToFailed(ctx context.Context, req ports.TransitionReleaseSetLocalCommitToFailedRequest) (work.ReleaseSetLocalCommit, error) {
	if !req.FailureReason.IsValid() {
		return work.ReleaseSetLocalCommit{}, fmt.Errorf("release set local commit failure reason %q is not a recognized value", req.FailureReason)
	}
	result, err := r.tx.ExecContext(ctx, `
UPDATE release_set_local_commits
SET state = 'FAILED', failure_reason = ?, completed_at = ?, version = version + 1
WHERE id = ? AND state = 'REQUESTED' AND version = ?`,
		string(req.FailureReason), formatWorkflowTime(req.OccurredAt),
		req.ReleaseSetLocalCommitID, req.ExpectedVersion,
	)
	if err != nil {
		return work.ReleaseSetLocalCommit{}, MapSQLiteError(fmt.Errorf("fail release set local commit %s: %w", req.ReleaseSetLocalCommitID, err))
	}
	if err := requireSingleRowAffected(ctx, r.tx, result, "release_set_local_commits", req.ReleaseSetLocalCommitID, req.ExpectedVersion); err != nil {
		return work.ReleaseSetLocalCommit{}, err
	}
	return loadReleaseSetLocalCommitTx(ctx, r.tx, req.ReleaseSetLocalCommitID)
}

// requireSingleRowAffected is the shared "exactly one row updated, or
// classify why not" tail every fenced UPDATE above needs — factored out
// since three call sites here need the identical
// ErrPersistenceNotFound/ErrOptimisticConflict classification
// loadReleaseSetLocalCommitTx's own siblings (TransitionReleaseSetState,
// etc.) each re-derive inline; this file's own three near-identical CAS
// updates are what finally makes sharing it worthwhile.
func requireSingleRowAffected(ctx context.Context, tx *sql.Tx, result sql.Result, table, id string, expectedVersion uint64) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s update result: %w", table, err)
	}
	if affected == 1 {
		return nil
	}
	var exists int
	lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE id = ?`, id).Scan(&exists)
	if errors.Is(lookupErr, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s %s", ports.ErrPersistenceNotFound, table, id)
	}
	if lookupErr != nil {
		return MapSQLiteError(fmt.Errorf("check stale %s transition: %w", table, lookupErr))
	}
	return fmt.Errorf("%w: %s %s expected version %d", ports.ErrOptimisticConflict, table, id, expectedVersion)
}

// ValidateLocalCommitWriteLeaseFencing implements ports.WorkRepository
// (V6-10E): see that method's own doc comment for the full contract. Mirrors
// validateActiveWriteLeaseInTx's own shape (workflow_store.go) minus the
// execution_attempts join this task's own LocalCommitWriteLeaseGrant has no
// counterpart for.
func (r workRepository) ValidateLocalCommitWriteLeaseFencing(ctx context.Context, lease ports.JobLease, grant ports.LocalCommitWriteLeaseGrant) error {
	var valid int
	err := r.tx.QueryRowContext(ctx, `
SELECT 1
FROM local_commit_write_leases AS lease
JOIN durable_jobs AS job ON job.id = lease.holder_job_id
JOIN repository_workspaces AS rw ON rw.id = lease.repository_workspace_id
WHERE lease.repository_workspace_id = ? AND lease.generation = ? AND lease.fence_token = ?
  AND lease.holder_job_id = ? AND lease.holder_job_lease_token = ? AND lease.lease_owner = ?
  AND julianday(lease.lease_until) > julianday('now')
  AND job.state = 'LEASED' AND job.lease_owner = lease.lease_owner AND job.lease_token = lease.holder_job_lease_token
  AND julianday(job.lease_until) > julianday('now')
  AND rw.generation = lease.generation AND rw.state = 'READY'`,
		string(grant.RepositoryWorkspaceID), grant.Generation, grant.FenceToken,
		string(lease.JobID), lease.Token, lease.Owner,
	).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ErrLocalCommitWriteLeaseLost
	}
	if err != nil {
		return MapSQLiteError(fmt.Errorf("validate local commit write lease fencing: %w", err))
	}
	return nil
}
