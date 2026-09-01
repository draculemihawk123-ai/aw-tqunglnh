package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// LoadExecutionAttemptState returns one execution attempt's current state
// and optimistic version — the same narrow, read-only shape as
// spk04_queries.go's LoadNodeRunState, used by acceptance scenarios and
// integration tests to verify TerminateInterruptedAttempt actually moved an
// interrupted attempt out of RUNNING (docs/design/02-v0-spike-verdict.md
// V0-10C), without reaching into Store's unexported db field.
func (s *Store) LoadExecutionAttemptState(ctx context.Context, attemptID runtime.ExecutionAttemptID) (runtime.ExecutionAttemptState, uint64, error) {
	if attemptID == "" {
		return "", 0, errors.New("execution attempt id is required")
	}
	var (
		state   runtime.ExecutionAttemptState
		version uint64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT state, version FROM execution_attempts WHERE id = ?`, attemptID,
	).Scan(&state, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, fmt.Errorf("%w: execution attempt %s", ports.ErrPersistenceNotFound, attemptID)
	}
	if err != nil {
		return "", 0, fmt.Errorf("load execution attempt state: %w", err)
	}
	return state, version, nil
}
