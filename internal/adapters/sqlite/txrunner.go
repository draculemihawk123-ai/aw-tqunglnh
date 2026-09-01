package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
)

// RunSerializedWrite runs fn inside a transaction for claim/version/
// JournalPosition allocation (docs/design/03-v1-alpha-foundation.md
// V1-04A). Every connection this Store opens already carries
// _txlock=immediate on its DSN (see db.go), so every transaction already
// takes its write-intent lock at BEGIN time — this wrapper's entire job
// is to keep that fact an adapter-internal detail: a caller names the
// semantic contract it needs, never the SQL (BEGIN IMMEDIATE) that
// satisfies it. Pure lock contention is retried a bounded number of times
// before being mapped to a typed, retryable error; fn's own returned
// error (e.g. a confirmed CAS/version conflict the caller detected by
// reloading state) passes through MapSQLiteError unchanged in shape but
// is never treated as retryable contention.
func (s *Store) RunSerializedWrite(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.runTx(ctx, fn)
}

// RunReadOnly runs fn inside a transaction for read-only work. It exists
// as its own named method — even though SQLite does not give this
// connection pool a mechanically distinct read-only transaction mode —
// so a call site documents its own intent instead of every caller having
// to reason about whether RunSerializedWrite's write-lock semantics were
// actually required.
func (s *Store) RunReadOnly(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.runTx(ctx, fn)
}

const maxBusyRetryAttempts = 8

func (s *Store) runTx(ctx context.Context, fn func(*sql.Tx) error) error {
	for attempt := 0; attempt < maxBusyRetryAttempts; attempt++ {
		err := s.runTxOnce(ctx, fn)
		if err == nil {
			return nil
		}
		if !isSQLiteBusy(err) || attempt == maxBusyRetryAttempts-1 {
			return MapSQLiteError(err)
		}
		delay := time.Duration(1<<attempt) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("unreachable transaction retry state")
}

func (s *Store) runTxOnce(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// MapSQLiteError converts a raw SQLite driver error into Alpha's typed
// apperror.Error, so nothing above this adapter boundary ever branches on
// a SQL error string (docs/design/00-roadmap.md §7). Pure lock contention
// (SQLITE_BUSY/SQLITE_LOCKED surviving every bounded retry) maps to
// CodeUnavailable — retryable, since the exact same operation trying
// again later may simply succeed once the contending writer finishes.
// It never produces CodeConflict: a real version/CAS loss can only be
// confirmed by a caller reloading state and comparing an expected
// version against what is actually stored, which this generic mapper has
// no way to do on its own — a caller that has confirmed a genuine CAS
// loss constructs that apperror.Error itself (see ports.ErrWriteLeaseConflict-
// style sentinels for one existing pattern) instead of routing through
// here. A nil err returns nil; an err that is already an *apperror.Error
// passes through unchanged, so wrapping is idempotent for a caller that
// already mapped its own error before returning it from fn.
func MapSQLiteError(err error) error {
	if err == nil {
		return nil
	}
	var already *apperror.Error
	if errors.As(err, &already) {
		return err
	}
	if isSQLiteBusy(err) {
		return apperror.Wrap(apperror.CodeUnavailable, "sqlite: lock contention", true, err)
	}
	return apperror.Wrap(apperror.CodeInternal, "sqlite: unexpected error", false, err)
}
