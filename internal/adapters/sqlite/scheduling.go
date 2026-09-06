package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

const durableJobColumns = `
id, project_id, kind, aggregate_type, aggregate_id, payload_json, state,
available_at, priority, claim_count, max_claims, lease_owner, lease_token,
lease_until, heartbeat_at, idempotency_key, last_error_code, version,
created_at, updated_at, run_id, job_class, cancel_epoch`

type rowScanner interface {
	Scan(...any) error
}

// sqlQueryRower is satisfied by both *sql.DB and *sql.Tx: the common
// surface enqueueJobTx needs so it runs identically whether called from
// Store.EnqueueJob's own single-statement autocommit (its pre-existing
// external behavior, unchanged by this extraction) or from
// jobsRepository.EnqueueJob's already-open transaction (V3-01: the first
// caller that needs a durable job enqueued atomically with other writes
// in the same transaction — RegisterRepository's own Repository-row +
// probe-job atomicity, docs/design/05-v3-project-workspace.md).
type sqlQueryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) EnqueueJob(ctx context.Context, request ports.EnqueueJobRequest) (ports.DurableJob, error) {
	return enqueueJobTx(ctx, s.db, request)
}

func enqueueJobTx(ctx context.Context, q sqlQueryRower, request ports.EnqueueJobRequest) (ports.DurableJob, error) {
	if err := validateEnqueueJob(request); err != nil {
		return ports.DurableJob{}, err
	}
	jobClass := ports.ClassifyJobKind(strings.TrimSpace(request.Kind))
	runID := strings.TrimSpace(request.RunID)
	// "Enqueue RUN_WORK mới CAS rằng Run còn non-cancelling"
	// (docs/design/06-v4-runtime-engine.md, V4-12B): a defense-in-depth
	// backstop — every real call site in internal/app/runtime already
	// checks the Run's own state before ever attempting to create new
	// work, so this should never fire in the normal flow, but the INSERT
	// itself is still the one place this invariant is actually enforced.
	// A separate upfront SELECT (rather than folding the check into the
	// INSERT itself via INSERT...SELECT...WHERE) keeps this readable; it
	// is race-free for the Tx-scoped jobsRepository.EnqueueJob path (every
	// real RUN_WORK+RunID caller in this codebase) since both
	// QueryRowContext calls share the SAME already-open r.tx. The flat,
	// non-transactional Store.EnqueueJob(ctx, request) — s.db, not a
	// Tx — has the identical narrow TOCTOU window every other flat/Tx
	// method pair in this file already has (completeJobTx et al.); no
	// real caller in this codebase enqueues a run-scoped RUN_WORK job
	// through that flat path.
	if jobClass == ports.JobClassRunWork && runID != "" {
		var cancelling int
		err := q.QueryRowContext(ctx, `SELECT 1 FROM workflow_runs WHERE id = ? AND state IN ('CANCELLING', 'CANCELLED')`, runID).Scan(&cancelling)
		if err == nil {
			return ports.DurableJob{}, ports.ErrRunCancelling
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return ports.DurableJob{}, MapSQLiteError(fmt.Errorf("check run %s cancelling before enqueue: %w", runID, err))
		}
	}
	payload := request.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	availableAt := ""
	if !request.AvailableAt.IsZero() {
		availableAt = request.AvailableAt.UTC().Format(time.RFC3339Nano)
	}
	var runIDColumn any
	if runID != "" {
		runIDColumn = runID
	}
	var projectIDColumn any
	if request.ProjectID != "" {
		projectIDColumn = request.ProjectID
	}

	row := q.QueryRowContext(ctx, `
INSERT INTO durable_jobs (
    id, project_id, kind, aggregate_type, aggregate_id, payload_json, state,
    available_at, priority, claim_count, max_claims, lease_token,
    idempotency_key, version, created_at, updated_at, run_id, job_class
) VALUES (?, ?, ?, ?, ?, ?, 'AVAILABLE',
    COALESCE(NULLIF(?, ''), strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ?, 0, ?, 0, ?, 1,
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), ?, ?)
RETURNING `+durableJobColumns,
		request.ID,
		projectIDColumn,
		strings.TrimSpace(request.Kind),
		strings.TrimSpace(request.AggregateType),
		strings.TrimSpace(request.AggregateID),
		string(payload),
		availableAt,
		request.Priority,
		request.MaxClaims,
		strings.TrimSpace(request.IdempotencyKey),
		runIDColumn,
		string(jobClass),
	)
	job, err := scanDurableJob(row)
	if err != nil {
		// A conflict on durable_jobs.idempotency_key (UNIQUE,
		// 0001_initial_schema.sql) is this table's own idempotent-enqueue
		// guarantee, not a genuine failure: a caller that derives its own
		// job's IdempotencyKey deterministically from the aggregate it is
		// about to reconcile (internal/app/workspacereconcile's own
		// "workspace-reconciliation:<id>@<version>" key, V3-10) relies on
		// this exact conflict to reject a second, concurrent request for an
		// already-in-flight reconciliation, mirroring
		// createRepositoryWorkspaceTx's own identical "insert failed, a row
		// for the unique key already exists" fallback lookup (work.go)
		// rather than a raw, unclassified SQLite error.
		idempotencyKey := strings.TrimSpace(request.IdempotencyKey)
		if idempotencyKey != "" {
			var existingID string
			lookupErr := q.QueryRowContext(ctx,
				`SELECT id FROM durable_jobs WHERE idempotency_key = ?`, idempotencyKey,
			).Scan(&existingID)
			if lookupErr == nil {
				return ports.DurableJob{}, fmt.Errorf(
					"%w: durable job with idempotency key %s (existing job %s)",
					ports.ErrPersistenceAlreadyExists, idempotencyKey, existingID)
			}
		}
		return ports.DurableJob{}, fmt.Errorf("enqueue durable job: %w", err)
	}
	return job, nil
}

// jobsRepository implements ports.JobsRepository (V3-01): the
// Tx-composable equivalent of Store.EnqueueJob above, for a command
// handler (internal/app/catalog.RegisterRepository) that needs the job
// insert and whatever Repository-row/event/receipt writes it performs
// alongside it to commit as one atomic transaction.
func (r jobsRepository) EnqueueJob(ctx context.Context, request ports.EnqueueJobRequest) (ports.DurableJob, error) {
	return enqueueJobTx(ctx, r.tx, request)
}

// HasActiveJobForAggregateIDs implements ports.JobsRepository (V3-11): see
// that interface method's own doc comment for the full contract.
func (r jobsRepository) HasActiveJobForAggregateIDs(ctx context.Context, aggregateIDs []string) (bool, error) {
	return hasActiveJobForAggregateIDsTx(ctx, r.tx, aggregateIDs)
}

func hasActiveJobForAggregateIDsTx(ctx context.Context, tx *sql.Tx, aggregateIDs []string) (bool, error) {
	if len(aggregateIDs) == 0 {
		return false, nil
	}
	placeholders := make([]string, len(aggregateIDs))
	args := make([]any, len(aggregateIDs))
	for i, id := range aggregateIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `
SELECT 1 FROM durable_jobs
WHERE aggregate_id IN (` + strings.Join(placeholders, ",") + `)
  AND state IN ('AVAILABLE', 'LEASED')
LIMIT 1`
	var exists int
	err := tx.QueryRowContext(ctx, query, args...).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, MapSQLiteError(fmt.Errorf("check active durable job: %w", err))
	}
	return true, nil
}

// FenceAndCancelRunJobs implements ports.JobsRepository (V4-12B): see that
// interface method's own doc comment for the full contract. One UPDATE
// achieves both fencing (every matching row, AVAILABLE or LEASED) and
// terminal-state-flipping (AVAILABLE only) — a CASE expression rather than
// two separate statements, so this is exactly one atomic write.
func (r jobsRepository) FenceAndCancelRunJobs(ctx context.Context, runID string) (int64, error) {
	if strings.TrimSpace(runID) == "" {
		return 0, errors.New("workflow run id is required")
	}
	result, err := r.tx.ExecContext(ctx, `
UPDATE durable_jobs
SET cancel_epoch = 1,
    state = CASE WHEN state = 'AVAILABLE' THEN 'CANCELLED' ELSE state END,
    version = version + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE run_id = ? AND job_class = 'RUN_WORK' AND state IN ('AVAILABLE', 'LEASED') AND cancel_epoch IS NULL`,
		runID,
	)
	if err != nil {
		return 0, MapSQLiteError(fmt.Errorf("fence and cancel run work jobs for run %s: %w", runID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read fence and cancel run work jobs result: %w", err)
	}
	return affected, nil
}

func (s *Store) ClaimJob(
	ctx context.Context,
	owner string,
	ttl time.Duration,
) (ports.DurableJob, ports.JobLease, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return ports.DurableJob{}, ports.JobLease{}, errors.New("job lease owner is required")
	}
	modifier, err := leaseModifier(ttl)
	if err != nil {
		return ports.DurableJob{}, ports.JobLease{}, err
	}

	// V4-12B: claim CAS is split by class ("Claim CAS tách theo class") —
	// a RUN_WORK job additionally requires cancel_epoch IS NULL, so a job
	// fenced by its owning Run's own cancellation intent can never be
	// claimed again; a CONTROL job (most importantly the very
	// CANCEL_RUN_COORDINATOR job that does the fencing) is never subject
	// to cancel_epoch at all. One candidate query with a compound
	// predicate — rather than two separate SQL statements the caller
	// picks between — since Pool.runWorker claims homogeneously and has
	// no notion of "claim only CONTROL jobs right now"; the two partial
	// indexes from migration 0023 already let SQLite's own planner serve
	// either branch efficiently.
	row := s.db.QueryRowContext(ctx, `
WITH candidate AS (
    SELECT id
    FROM durable_jobs
    WHERE state = 'AVAILABLE'
      AND claim_count < max_claims
      AND julianday(available_at) <= julianday('now')
      AND (job_class = 'CONTROL' OR (job_class = 'RUN_WORK' AND cancel_epoch IS NULL))
    ORDER BY priority DESC, created_at, id
    LIMIT 1
)
UPDATE durable_jobs
SET state = 'LEASED',
    claim_count = claim_count + 1,
    lease_owner = ?,
    lease_token = lease_token + 1,
    lease_until = strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?),
    heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    version = version + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = (SELECT id FROM candidate)
  AND state = 'AVAILABLE'
RETURNING `+durableJobColumns, owner, modifier)
	job, err := scanDurableJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.DurableJob{}, ports.JobLease{}, ports.ErrNoJobAvailable
	}
	if err != nil {
		return ports.DurableJob{}, ports.JobLease{}, fmt.Errorf("claim durable job: %w", err)
	}
	lease := leaseFromJob(job)
	return job, lease, nil
}

func (s *Store) HeartbeatJob(
	ctx context.Context,
	lease ports.JobLease,
	ttl time.Duration,
) (ports.JobLease, error) {
	if err := validateJobLease(lease); err != nil {
		return ports.JobLease{}, err
	}
	modifier, err := leaseModifier(ttl)
	if err != nil {
		return ports.JobLease{}, err
	}
	var leaseUntilText string
	err = s.db.QueryRowContext(ctx, `
UPDATE durable_jobs
SET lease_until = strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?),
    heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    version = version + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?
  AND state = 'LEASED'
  AND lease_owner = ?
  AND lease_token = ?
  AND julianday(lease_until) > julianday('now')
RETURNING lease_until`, modifier, lease.JobID, lease.Owner, lease.Token).Scan(&leaseUntilText)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.JobLease{}, ports.ErrJobLeaseLost
	}
	if err != nil {
		return ports.JobLease{}, fmt.Errorf("heartbeat durable job: %w", err)
	}
	leaseUntil, err := parseDBTime(leaseUntilText)
	if err != nil {
		return ports.JobLease{}, err
	}
	lease.LeaseUntil = leaseUntil
	return lease, nil
}

func (s *Store) CompleteJob(ctx context.Context, lease ports.JobLease) error {
	return completeJobTx(ctx, s.db, lease)
}

// CompleteJob implements ports.JobsRepository (V4-05): the Tx-composable
// twin of Store.CompleteJob above, for FinalizeExecutionAttempt's own
// fenced finalize — see that interface method's own doc comment for why.
func (r jobsRepository) CompleteJob(ctx context.Context, lease ports.JobLease) error {
	return completeJobTx(ctx, r.tx, lease)
}

// completeJobTx is Store.CompleteJob/jobsRepository.CompleteJob's shared
// implementation — sqlExecContexter is satisfied by both *sql.DB (Store's
// own always-separate transaction) and *sql.Tx (a caller's already-open
// one), the same "one exec function, two callers" extraction
// enqueueJobTx already established above.
type sqlExecContexter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func completeJobTx(ctx context.Context, q sqlExecContexter, lease ports.JobLease) error {
	if err := validateJobLease(lease); err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `
UPDATE durable_jobs
SET state = 'SUCCEEDED',
    lease_until = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    version = version + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?
  AND state = 'LEASED'
  AND lease_owner = ?
  AND lease_token = ?
  AND julianday(lease_until) > julianday('now')`, lease.JobID, lease.Owner, lease.Token)
	if err != nil {
		return fmt.Errorf("complete durable job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read complete durable job result: %w", err)
	}
	if affected != 1 {
		return ports.ErrJobLeaseLost
	}
	return nil
}

// ValidateActiveJob implements ports.JobsRepository (V4-05): see that
// interface method's own doc comment for the full contract — mirrors
// workflow_store.go's own validateActiveFinalizationJob, generalized to an
// arbitrary (aggregateType, aggregateID) rather than hardcoding
// AggregateType='WorkflowRun'.
func (r jobsRepository) ValidateActiveJob(ctx context.Context, lease ports.JobLease, aggregateType, aggregateID string) error {
	if err := validateJobLease(lease); err != nil {
		return err
	}
	var valid int
	err := r.tx.QueryRowContext(ctx, `
SELECT 1 FROM durable_jobs
WHERE id = ? AND aggregate_type = ? AND aggregate_id = ?
  AND state = 'LEASED' AND lease_owner = ? AND lease_token = ?
  AND julianday(lease_until) > julianday('now')`,
		lease.JobID, aggregateType, aggregateID, lease.Owner, lease.Token,
	).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ErrJobLeaseLost
	}
	if err != nil {
		return fmt.Errorf("validate active job %s: %w", lease.JobID, err)
	}
	return nil
}

func (s *Store) RecoverExpiredJobs(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE durable_jobs
SET state = CASE WHEN claim_count >= max_claims THEN 'DEAD' ELSE 'AVAILABLE' END,
    available_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    lease_owner = NULL,
    lease_until = NULL,
    heartbeat_at = NULL,
    last_error_code = 'LEASE_EXPIRED',
    version = version + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE state = 'LEASED'
  AND julianday(lease_until) <= julianday('now')`)
	if err != nil {
		return 0, fmt.Errorf("recover expired durable jobs: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read recover durable jobs result: %w", err)
	}
	return affected, nil
}

func (s *Store) AcquireWriteLeases(
	ctx context.Context,
	request ports.AcquireWriteLeasesRequest,
) ([]ports.WriteLeaseGrant, error) {
	// SQLite permits only one writer at a time. A competing short transaction
	// can therefore surface SQLITE_BUSY before it reaches the semantic lease
	// conflict check. Retry only that transient infrastructure condition so
	// callers consistently observe the domain result (grant or conflict).
	for attempt := 0; attempt < 8; attempt++ {
		grants, err := s.acquireWriteLeasesOnce(ctx, request)
		if err == nil || !isSQLiteBusy(err) || attempt == 7 {
			return grants, err
		}
		delay := time.Duration(1<<attempt) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, errors.New("unreachable write lease retry state")
}

func (s *Store) acquireWriteLeasesOnce(
	ctx context.Context,
	request ports.AcquireWriteLeasesRequest,
) ([]ports.WriteLeaseGrant, error) {
	if err := validateAcquireWriteLeases(request); err != nil {
		return nil, err
	}
	modifier, _ := leaseModifier(request.TTL)
	targets := sortedLeaseTargets(request.Targets)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin write lease acquisition: %w", err)
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
		return nil, ports.ErrJobLeaseLost
	}
	if err != nil {
		return nil, fmt.Errorf("validate job lease before write lease acquisition: %w", err)
	}

	grants := make([]ports.WriteLeaseGrant, 0, len(targets))
	for _, target := range targets {
		var fenceToken uint64
		var leaseUntilText string
		err := tx.QueryRowContext(ctx, `
INSERT INTO write_leases (
    repository_workspace_id, generation, fence_token, holder_job_id,
    holder_job_lease_token, holder_attempt_id, lease_owner, lease_until, heartbeat_at
)
SELECT rw.id, rw.generation, 1, ?, ?, ?, ?,
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?),
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM repository_workspaces AS rw
WHERE rw.id = ? AND rw.repository_id = ? AND rw.generation = ? AND rw.state = 'READY'
ON CONFLICT(repository_workspace_id, generation) DO UPDATE SET
    fence_token = write_leases.fence_token + 1,
    holder_job_id = excluded.holder_job_id,
    holder_job_lease_token = excluded.holder_job_lease_token,
    holder_attempt_id = excluded.holder_attempt_id,
    lease_owner = excluded.lease_owner,
    lease_until = excluded.lease_until,
    heartbeat_at = excluded.heartbeat_at
WHERE julianday(write_leases.lease_until) <= julianday('now')
RETURNING fence_token, lease_until`,
			request.JobLease.JobID,
			request.JobLease.Token,
			request.AttemptID,
			request.JobLease.Owner,
			modifier,
			target.RepositoryWorkspaceID,
			target.RepositoryID,
			target.Generation,
		).Scan(&fenceToken, &leaseUntilText)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: workspace=%s generation=%d",
				ports.ErrWriteLeaseConflict, target.RepositoryWorkspaceID, target.Generation)
		}
		if err != nil {
			return nil, fmt.Errorf("acquire write lease for %s: %w", target.RepositoryWorkspaceID, err)
		}
		leaseUntil, err := parseDBTime(leaseUntilText)
		if err != nil {
			return nil, err
		}
		grants = append(grants, ports.WriteLeaseGrant{
			RepositoryID:          target.RepositoryID,
			RepositoryWorkspaceID: target.RepositoryWorkspaceID,
			Generation:            target.Generation,
			FenceToken:            fenceToken,
			HolderJobID:           request.JobLease.JobID,
			HolderJobLeaseToken:   request.JobLease.Token,
			HolderAttemptID:       request.AttemptID,
			Owner:                 request.JobLease.Owner,
			LeaseUntil:            leaseUntil,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit write lease acquisition: %w", err)
	}
	return grants, nil
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "sqlite_busy")
}

func (s *Store) HeartbeatWriteLeases(
	ctx context.Context,
	grants []ports.WriteLeaseGrant,
	ttl time.Duration,
) ([]ports.WriteLeaseGrant, error) {
	if len(grants) == 0 {
		return nil, errors.New("at least one write lease grant is required")
	}
	modifier, err := leaseModifier(ttl)
	if err != nil {
		return nil, err
	}
	ordered := sortedWriteLeaseGrants(grants)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin write lease heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for index := range ordered {
		grant := &ordered[index]
		if err := validateWriteLeaseGrant(*grant); err != nil {
			return nil, err
		}
		var leaseUntilText string
		err := tx.QueryRowContext(ctx, `
UPDATE write_leases
SET lease_until = strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?),
    heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE repository_workspace_id = ? AND generation = ? AND fence_token = ?
  AND holder_job_id = ? AND holder_job_lease_token = ?
  AND holder_attempt_id = ? AND lease_owner = ?
  AND julianday(lease_until) > julianday('now')
  AND EXISTS (
      SELECT 1 FROM durable_jobs AS job
      WHERE job.id = write_leases.holder_job_id
        AND job.state = 'LEASED'
        AND job.lease_owner = write_leases.lease_owner
        AND job.lease_token = write_leases.holder_job_lease_token
        AND julianday(job.lease_until) > julianday('now')
  )
  AND EXISTS (
      SELECT 1 FROM repository_workspaces AS rw
      WHERE rw.id = write_leases.repository_workspace_id
        AND rw.generation = write_leases.generation
        AND rw.state = 'READY'
  )
  AND EXISTS (
      SELECT 1 FROM execution_attempts AS attempt
      WHERE attempt.id = write_leases.holder_attempt_id
        AND attempt.state = 'RUNNING'
  )
RETURNING lease_until`, modifier, grant.RepositoryWorkspaceID, grant.Generation,
			grant.FenceToken, grant.HolderJobID, grant.HolderJobLeaseToken,
			grant.HolderAttemptID, grant.Owner).Scan(&leaseUntilText)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ports.ErrWriteLeaseLost
		}
		if err != nil {
			return nil, fmt.Errorf("heartbeat write lease for %s: %w", grant.RepositoryWorkspaceID, err)
		}
		leaseUntil, err := parseDBTime(leaseUntilText)
		if err != nil {
			return nil, err
		}
		grant.LeaseUntil = leaseUntil
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit write lease heartbeat: %w", err)
	}
	return ordered, nil
}

func (s *Store) ValidateWriteLease(ctx context.Context, grant ports.WriteLeaseGrant) error {
	if err := validateWriteLeaseGrant(grant); err != nil {
		return err
	}
	var valid int
	err := s.db.QueryRowContext(ctx, `
SELECT 1
FROM write_leases AS lease
JOIN durable_jobs AS job ON job.id = lease.holder_job_id
JOIN repository_workspaces AS rw ON rw.id = lease.repository_workspace_id
JOIN execution_attempts AS attempt ON attempt.id = lease.holder_attempt_id
WHERE lease.repository_workspace_id = ? AND lease.generation = ? AND lease.fence_token = ?
  AND lease.holder_job_id = ? AND lease.holder_job_lease_token = ?
  AND lease.holder_attempt_id = ? AND lease.lease_owner = ?
  AND julianday(lease.lease_until) > julianday('now')
  AND job.state = 'LEASED' AND job.lease_owner = lease.lease_owner
  AND job.lease_token = lease.holder_job_lease_token
  AND julianday(job.lease_until) > julianday('now')
  AND rw.generation = lease.generation AND rw.state = 'READY'
  AND attempt.state = 'RUNNING'`,
		grant.RepositoryWorkspaceID, grant.Generation, grant.FenceToken,
		grant.HolderJobID, grant.HolderJobLeaseToken, grant.HolderAttemptID, grant.Owner).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ErrWriteLeaseLost
	}
	if err != nil {
		return fmt.Errorf("validate write lease: %w", err)
	}
	return nil
}

func (s *Store) ReleaseWriteLeases(ctx context.Context, grants []ports.WriteLeaseGrant) error {
	if len(grants) == 0 {
		return errors.New("at least one write lease grant is required")
	}
	ordered := sortedWriteLeaseGrants(grants)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin write lease release: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, grant := range ordered {
		if err := validateWriteLeaseGrant(grant); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
UPDATE write_leases
SET lease_until = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE repository_workspace_id = ? AND generation = ? AND fence_token = ?
  AND holder_job_id = ? AND holder_job_lease_token = ?
  AND holder_attempt_id = ? AND lease_owner = ?
  AND julianday(lease_until) > julianday('now')`,
			grant.RepositoryWorkspaceID, grant.Generation, grant.FenceToken,
			grant.HolderJobID, grant.HolderJobLeaseToken, grant.HolderAttemptID, grant.Owner)
		if err != nil {
			return fmt.Errorf("release write lease for %s: %w", grant.RepositoryWorkspaceID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read write lease release result: %w", err)
		}
		if affected != 1 {
			return ports.ErrWriteLeaseLost
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit write lease release: %w", err)
	}
	return nil
}

func validateEnqueueJob(request ports.EnqueueJobRequest) error {
	if request.ID == "" || strings.TrimSpace(request.Kind) == "" ||
		strings.TrimSpace(request.AggregateType) == "" || strings.TrimSpace(request.AggregateID) == "" ||
		strings.TrimSpace(request.IdempotencyKey) == "" {
		return errors.New("durable job identities, kind, aggregate and idempotency key are required")
	}
	if err := ports.ValidateJobScope(strings.TrimSpace(request.Kind), request.ProjectID, request.RunID); err != nil {
		return err
	}
	if request.MaxClaims == 0 {
		return errors.New("durable job max claims must be greater than zero")
	}
	if len(request.Payload) != 0 && !json.Valid(request.Payload) {
		return errors.New("durable job payload must be valid JSON")
	}
	return nil
}

func validateJobLease(lease ports.JobLease) error {
	if lease.JobID == "" || strings.TrimSpace(lease.Owner) == "" || lease.Token == 0 {
		return errors.New("job lease id, owner and token are required")
	}
	return nil
}

func validateAcquireWriteLeases(request ports.AcquireWriteLeasesRequest) error {
	if err := validateJobLease(request.JobLease); err != nil {
		return err
	}
	if request.AttemptID == "" {
		return errors.New("write lease attempt id is required")
	}
	if len(request.Targets) == 0 {
		return errors.New("at least one write lease target is required")
	}
	if _, err := leaseModifier(request.TTL); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(request.Targets))
	for _, target := range request.Targets {
		if target.RepositoryID == "" || target.RepositoryWorkspaceID == "" || target.Generation == 0 {
			return errors.New("write lease target repository, workspace and generation are required")
		}
		key := fmt.Sprintf("%s\x00%d", target.RepositoryWorkspaceID, target.Generation)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate write lease target %s generation %d",
				target.RepositoryWorkspaceID, target.Generation)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateWriteLeaseGrant(grant ports.WriteLeaseGrant) error {
	if grant.RepositoryID == "" || grant.RepositoryWorkspaceID == "" || grant.Generation == 0 ||
		grant.FenceToken == 0 || grant.HolderJobID == "" || grant.HolderJobLeaseToken == 0 ||
		grant.HolderAttemptID == "" ||
		strings.TrimSpace(grant.Owner) == "" {
		return errors.New("write lease proof is incomplete")
	}
	return nil
}

func leaseModifier(ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", errors.New("lease TTL must be greater than zero")
	}
	return fmt.Sprintf("+%.9f seconds", ttl.Seconds()), nil
}

func leaseFromJob(job ports.DurableJob) ports.JobLease {
	lease := ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken}
	if job.LeaseUntil != nil {
		lease.LeaseUntil = *job.LeaseUntil
	}
	return lease
}

func scanDurableJob(scanner rowScanner) (ports.DurableJob, error) {
	var job ports.DurableJob
	var projectID sql.NullString
	var payload string
	var state string
	var availableAtText string
	var leaseOwner sql.NullString
	var leaseUntilText sql.NullString
	var heartbeatAtText sql.NullString
	var lastErrorCode sql.NullString
	var createdAtText string
	var updatedAtText string
	var runID sql.NullString
	var jobClass string
	var cancelEpoch sql.NullInt64
	if err := scanner.Scan(
		&job.ID, &projectID, &job.Kind, &job.AggregateType, &job.AggregateID,
		&payload, &state, &availableAtText, &job.Priority, &job.ClaimCount,
		&job.MaxClaims, &leaseOwner, &job.LeaseToken, &leaseUntilText,
		&heartbeatAtText, &job.IdempotencyKey, &lastErrorCode, &job.Version,
		&createdAtText, &updatedAtText, &runID, &jobClass, &cancelEpoch,
	); err != nil {
		return ports.DurableJob{}, err
	}
	job.ProjectID = project.ProjectID(projectID.String)
	job.Payload = json.RawMessage(payload)
	job.State = ports.JobState(state)
	job.LeaseOwner = leaseOwner.String
	job.LastErrorCode = lastErrorCode.String
	job.RunID = runID.String
	job.JobClass = ports.JobClass(jobClass)
	if cancelEpoch.Valid {
		epoch := uint64(cancelEpoch.Int64)
		job.CancelEpoch = &epoch
	}
	var err error
	if job.AvailableAt, err = parseDBTime(availableAtText); err != nil {
		return ports.DurableJob{}, err
	}
	if job.CreatedAt, err = parseDBTime(createdAtText); err != nil {
		return ports.DurableJob{}, err
	}
	if job.UpdatedAt, err = parseDBTime(updatedAtText); err != nil {
		return ports.DurableJob{}, err
	}
	if leaseUntilText.Valid {
		parsed, err := parseDBTime(leaseUntilText.String)
		if err != nil {
			return ports.DurableJob{}, err
		}
		job.LeaseUntil = &parsed
	}
	if heartbeatAtText.Valid {
		parsed, err := parseDBTime(heartbeatAtText.String)
		if err != nil {
			return ports.DurableJob{}, err
		}
		job.HeartbeatAt = &parsed
	}
	return job, nil
}

func parseDBTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse SQLite UTC timestamp %q: %w", value, err)
	}
	return parsed, nil
}

func sortedLeaseTargets(targets []ports.WorkspaceLeaseTarget) []ports.WorkspaceLeaseTarget {
	result := append([]ports.WorkspaceLeaseTarget(nil), targets...)
	sort.Slice(result, func(left, right int) bool {
		if result[left].RepositoryID != result[right].RepositoryID {
			return result[left].RepositoryID < result[right].RepositoryID
		}
		if result[left].RepositoryWorkspaceID != result[right].RepositoryWorkspaceID {
			return result[left].RepositoryWorkspaceID < result[right].RepositoryWorkspaceID
		}
		return result[left].Generation < result[right].Generation
	})
	return result
}

func sortedWriteLeaseGrants(grants []ports.WriteLeaseGrant) []ports.WriteLeaseGrant {
	result := append([]ports.WriteLeaseGrant(nil), grants...)
	sort.Slice(result, func(left, right int) bool {
		if result[left].RepositoryID != result[right].RepositoryID {
			return result[left].RepositoryID < result[right].RepositoryID
		}
		if result[left].RepositoryWorkspaceID != result[right].RepositoryWorkspaceID {
			return result[left].RepositoryWorkspaceID < result[right].RepositoryWorkspaceID
		}
		return result[left].Generation < result[right].Generation
	})
	return result
}

var _ ports.JobQueue = (*Store)(nil)
var _ ports.WriteLeaseManager = (*Store)(nil)
