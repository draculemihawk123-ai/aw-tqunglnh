package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// This file gives *Store its ports.LocalCommitWriteLeaseManager methods
// (V6-10E, docs/design/08-v6-api-projections.md V6-10E) — see that
// interface's own doc comment (internal/app/ports/releasesetlocalcommit.go)
// for why this is a separate mechanism from AcquireWriteLeases/
// HeartbeatWriteLeases/ValidateWriteLease/ReleaseWriteLeases (scheduling.go),
// never a reuse of them. Every method here operates on s.db directly, its
// own short transaction — mirroring AcquireWriteLeases/ReleaseWriteLeases'
// own "runs outside whatever ports.Tx a caller might otherwise be composing"
// shape exactly, since a real caller (internal/app/releasesetcommit's own
// worker) always calls these OUTSIDE any ports.UnitOfWork transaction
// (docs/architecture/04-go-core-spec.md §11.1: no filesystem/process call
// inside an application transaction — these are plain SQL, but kept outside
// for the identical reason AcquireWriteLeases itself gives: a caller must
// never be able to compose this into a WithSerializedWrite closure).

func (s *Store) AcquireLocalCommitWriteLease(
	ctx context.Context,
	request ports.AcquireLocalCommitWriteLeaseRequest,
) (ports.LocalCommitWriteLeaseGrant, error) {
	for attempt := 0; attempt < 8; attempt++ {
		grant, err := s.acquireLocalCommitWriteLeaseOnce(ctx, request)
		if err == nil || !isSQLiteBusy(err) || attempt == 7 {
			return grant, err
		}
		delay := time.Duration(1<<attempt) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ports.LocalCommitWriteLeaseGrant{}, ctx.Err()
		case <-timer.C:
		}
	}
	return ports.LocalCommitWriteLeaseGrant{}, errors.New("unreachable local commit write lease retry state")
}

func (s *Store) acquireLocalCommitWriteLeaseOnce(
	ctx context.Context,
	request ports.AcquireLocalCommitWriteLeaseRequest,
) (ports.LocalCommitWriteLeaseGrant, error) {
	if err := validateAcquireLocalCommitWriteLease(request); err != nil {
		return ports.LocalCommitWriteLeaseGrant{}, err
	}
	modifier, _ := leaseModifier(request.TTL)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return ports.LocalCommitWriteLeaseGrant{}, fmt.Errorf("begin local commit write lease acquisition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var activeJob int
	err = tx.QueryRowContext(ctx, `
SELECT 1
FROM durable_jobs
WHERE id = ? AND state = 'LEASED' AND lease_owner = ? AND lease_token = ?
  AND julianday(lease_until) > julianday('now')`,
		request.JobLease.JobID, request.JobLease.Owner, request.JobLease.Token).Scan(&activeJob)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.LocalCommitWriteLeaseGrant{}, ports.ErrJobLeaseLost
	}
	if err != nil {
		return ports.LocalCommitWriteLeaseGrant{}, fmt.Errorf("validate job lease before local commit write lease acquisition: %w", err)
	}

	var fenceToken uint64
	var leaseUntilText string
	err = tx.QueryRowContext(ctx, `
INSERT INTO local_commit_write_leases (
    repository_workspace_id, generation, fence_token, holder_job_id, holder_job_lease_token, lease_owner, lease_until
)
SELECT rw.id, rw.generation, 1, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?)
FROM repository_workspaces AS rw
WHERE rw.id = ? AND rw.repository_id = ? AND rw.generation = ? AND rw.state = 'READY'
ON CONFLICT(repository_workspace_id, generation) DO UPDATE SET
    fence_token = local_commit_write_leases.fence_token + 1,
    holder_job_id = excluded.holder_job_id,
    holder_job_lease_token = excluded.holder_job_lease_token,
    lease_owner = excluded.lease_owner,
    lease_until = excluded.lease_until
WHERE julianday(local_commit_write_leases.lease_until) <= julianday('now')
RETURNING fence_token, lease_until`,
		request.JobLease.JobID, request.JobLease.Token, request.JobLease.Owner, modifier,
		string(request.Target.RepositoryWorkspaceID), string(request.Target.RepositoryID), request.Target.Generation,
	).Scan(&fenceToken, &leaseUntilText)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.LocalCommitWriteLeaseGrant{}, fmt.Errorf("%w: workspace=%s generation=%d",
			ports.ErrLocalCommitWriteLeaseConflict, request.Target.RepositoryWorkspaceID, request.Target.Generation)
	}
	if err != nil {
		return ports.LocalCommitWriteLeaseGrant{}, fmt.Errorf("acquire local commit write lease for %s: %w", request.Target.RepositoryWorkspaceID, err)
	}
	leaseUntil, err := parseDBTime(leaseUntilText)
	if err != nil {
		return ports.LocalCommitWriteLeaseGrant{}, err
	}
	if err := tx.Commit(); err != nil {
		return ports.LocalCommitWriteLeaseGrant{}, fmt.Errorf("commit local commit write lease acquisition: %w", err)
	}
	return ports.LocalCommitWriteLeaseGrant{
		RepositoryID:          request.Target.RepositoryID,
		RepositoryWorkspaceID: request.Target.RepositoryWorkspaceID,
		Generation:            request.Target.Generation,
		FenceToken:            fenceToken,
		HolderJobID:           request.JobLease.JobID,
		HolderJobLeaseToken:   request.JobLease.Token,
		Owner:                 request.JobLease.Owner,
		LeaseUntil:            leaseUntil,
	}, nil
}

func (s *Store) ValidateLocalCommitWriteLease(ctx context.Context, grant ports.LocalCommitWriteLeaseGrant) error {
	if err := validateLocalCommitWriteLeaseGrant(grant); err != nil {
		return err
	}
	var valid int
	err := s.db.QueryRowContext(ctx, `
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
		string(grant.HolderJobID), grant.HolderJobLeaseToken, grant.Owner).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ErrLocalCommitWriteLeaseLost
	}
	if err != nil {
		return fmt.Errorf("validate local commit write lease: %w", err)
	}
	return nil
}

func (s *Store) ReleaseLocalCommitWriteLease(ctx context.Context, grant ports.LocalCommitWriteLeaseGrant) error {
	if err := validateLocalCommitWriteLeaseGrant(grant); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE local_commit_write_leases
SET lease_until = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE repository_workspace_id = ? AND generation = ? AND fence_token = ?
  AND holder_job_id = ? AND holder_job_lease_token = ? AND lease_owner = ?
  AND julianday(lease_until) > julianday('now')`,
		string(grant.RepositoryWorkspaceID), grant.Generation, grant.FenceToken,
		string(grant.HolderJobID), grant.HolderJobLeaseToken, grant.Owner)
	if err != nil {
		return fmt.Errorf("release local commit write lease for %s: %w", grant.RepositoryWorkspaceID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read local commit write lease release result: %w", err)
	}
	if affected != 1 {
		return ports.ErrLocalCommitWriteLeaseLost
	}
	return nil
}

func validateAcquireLocalCommitWriteLease(request ports.AcquireLocalCommitWriteLeaseRequest) error {
	if err := validateJobLease(request.JobLease); err != nil {
		return err
	}
	if request.Target.RepositoryID == "" || request.Target.RepositoryWorkspaceID == "" || request.Target.Generation == 0 {
		return errors.New("local commit write lease target repository, workspace and generation are required")
	}
	if _, err := leaseModifier(request.TTL); err != nil {
		return err
	}
	return nil
}

func validateLocalCommitWriteLeaseGrant(grant ports.LocalCommitWriteLeaseGrant) error {
	if grant.RepositoryID == "" || grant.RepositoryWorkspaceID == "" || grant.Generation == 0 ||
		grant.FenceToken == 0 || grant.HolderJobID == "" || grant.HolderJobLeaseToken == 0 ||
		strings.TrimSpace(grant.Owner) == "" {
		return errors.New("local commit write lease proof is incomplete")
	}
	return nil
}

var _ ports.LocalCommitWriteLeaseManager = (*Store)(nil)
