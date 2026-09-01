package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store owns the SQLite connection pool used by the spike. Domain and
// application packages never receive *sql.DB or *sql.Tx values.
type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite path: %w", err)
	}
	uriPath := filepath.ToSlash(abs)
	// url.URL treats a Windows drive prefix as a URI authority unless the
	// slash that denotes an absolute file URI is present (file:///C:/...).
	if filepath.VolumeName(abs) != "" && uriPath[0] != '/' {
		uriPath = "/" + uriPath
	}
	u := &url.URL{Scheme: "file", Path: uriPath}
	query := u.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "synchronous(FULL)")
	// Every BeginTx call in this package writes (docs/design/02-v0-spike-verdict.md
	// V0-11A): a plain DEFERRED transaction only takes its SHARED read lock
	// up front and lazily upgrades to a write lock on its first write
	// statement. When two such transactions both already hold a SHARED lock
	// and race to upgrade simultaneously, SQLite returns SQLITE_BUSY without
	// ever invoking the busy-handler — busy_timeout above does not apply to
	// that specific case, only to ordinary lock contention. _txlock=immediate
	// makes every transaction on this connection acquire its write-intent
	// (RESERVED) lock at BEGIN time instead, so concurrent writers contend
	// for that lock the normal way busy_timeout does cover.
	query.Add("_txlock", "immediate")
	u.RawQuery = query.Encode()

	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(0)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := verifyDurabilityPragmas(pingCtx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	store := &Store{db: db}
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// sqliteSynchronousOff is PRAGMA synchronous's integer value for OFF —
// the one durability mode Alpha must never run under, since it is the
// only setting where SQLite can lose committed transactions on a power
// loss or OS crash instead of just an application crash
// (docs/design/03-v1-alpha-foundation.md V1-04A's "cấm hạ xuống OFF").
const sqliteSynchronousOff = 0

// verifyDurabilityPragmas re-reads the DSN-level pragmas db.go's Open sets
// (foreign_keys, journal_mode, synchronous) directly from the connection
// instead of trusting the DSN silently took effect, and fails closed if
// any of them did not — "bật và verify WAL lúc startup" is a real
// assertion, not a comment next to the DSN string. The _pragma DSN
// parameter modernc.org/sqlite uses applies at connection-open time for
// every physical connection in the pool, not just the one this check
// happens to run on, so verifying the connection Ping already established
// is representative of every connection Open's pool will hand out.
func verifyDurabilityPragmas(ctx context.Context, db *sql.DB) error {
	var foreignKeys int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify foreign_keys pragma: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("foreign_keys pragma is %d, want enabled (1)", foreignKeys)
	}

	var journalMode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		return fmt.Errorf("verify journal_mode pragma: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("journal_mode pragma is %q, want wal", journalMode)
	}

	var synchronous int
	if err := db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
		return fmt.Errorf("verify synchronous pragma: %w", err)
	}
	if synchronous == sqliteSynchronousOff {
		return fmt.Errorf("synchronous pragma is OFF (%d) — durability mode must never be lowered to OFF", synchronous)
	}

	return nil
}
