package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// The read-only queries in this file exist so internal/spikeacceptance's
// SPK-04 scenario (docs/design/02-v0-spike-verdict.md V0-10B) can verify
// exactly what internal/adapters/sqlite's crash_resume_*_integration_test.go
// files verify via raw SQL against Store's unexported db field — something a
// caller outside this package can never do. Each is a narrow, single-row
// or single-count lookup, the same shape as the existing LoadWorkflowRun/
// LoadLatestCheckpoint methods; none of them add any write path or business
// rule.

// LoadNodeRunState returns one node run's current state and optimistic
// version. A not-yet-created node run reports ports.ErrPersistenceNotFound.
func (s *Store) LoadNodeRunState(ctx context.Context, nodeRunID string) (runtime.NodeRunState, uint64, error) {
	if nodeRunID == "" {
		return "", 0, errors.New("node run id is required")
	}
	var (
		state   runtime.NodeRunState
		version uint64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT state, version FROM node_runs WHERE id = ?`, nodeRunID,
	).Scan(&state, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, fmt.Errorf("%w: node run %s", ports.ErrPersistenceNotFound, nodeRunID)
	}
	if err != nil {
		return "", 0, fmt.Errorf("load node run state: %w", err)
	}
	return state, version, nil
}

// LoadDurableJobState returns one durable job's current state.
func (s *Store) LoadDurableJobState(ctx context.Context, jobID ports.JobID) (ports.JobState, error) {
	if jobID == "" {
		return "", errors.New("job id is required")
	}
	var state ports.JobState
	err := s.db.QueryRowContext(ctx,
		`SELECT state FROM durable_jobs WHERE id = ?`, jobID,
	).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: durable job %s", ports.ErrPersistenceNotFound, jobID)
	}
	if err != nil {
		return "", fmt.Errorf("load durable job state: %w", err)
	}
	return state, nil
}

// CountDurableJobsByIdempotencyKey answers how many durable jobs exist under
// one idempotency key — used to prove a boundary either dispatched exactly
// one downstream job or dispatched none at all.
func (s *Store) CountDurableJobsByIdempotencyKey(ctx context.Context, idempotencyKey string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM durable_jobs WHERE idempotency_key = ?`, idempotencyKey,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count durable jobs by idempotency key: %w", err)
	}
	return count, nil
}

// CountDomainEvents answers how many domain_events rows exist for one
// aggregate — used to prove a boundary committed exactly the events its
// contract calls for, never zero when it should have committed and never
// more than one from a duplicated/replayed transition.
func (s *Store) CountDomainEvents(ctx context.Context, aggregateType, aggregateID string) (int, error) {
	if aggregateType == "" || aggregateID == "" {
		return 0, errors.New("aggregate type and id are required")
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM domain_events WHERE aggregate_type = ? AND aggregate_id = ?`, aggregateType, aggregateID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count domain events: %w", err)
	}
	return count, nil
}

// LoadRepositoryWorkspaceRevision returns one repository workspace's current
// revision — the durable evidence ReconcileMutatingAttempt compares an
// interrupted mutating attempt's pinned revision against.
func (s *Store) LoadRepositoryWorkspaceRevision(ctx context.Context, repositoryWorkspaceID string) (string, error) {
	if repositoryWorkspaceID == "" {
		return "", errors.New("repository workspace id is required")
	}
	var revision string
	err := s.db.QueryRowContext(ctx,
		`SELECT current_revision FROM repository_workspaces WHERE id = ?`, repositoryWorkspaceID,
	).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: repository workspace %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
	}
	if err != nil {
		return "", fmt.Errorf("load repository workspace revision: %w", err)
	}
	return revision, nil
}
