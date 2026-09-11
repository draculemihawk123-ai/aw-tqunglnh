package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const bootstrapSchema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    checksum TEXT NOT NULL,
    applied_at TEXT NOT NULL
);`

// migration is one numbered SQL asset (docs/design/03-v1-alpha-foundation.md
// V1-04): Version is parsed from its filename, Checksum is computed once
// at load time from the embedded bytes, and Name is the human-readable
// remainder of the filename for diagnostics.
type migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

var migrationFilenamePattern = regexp.MustCompile(`^(\d{4})_(.+)\.sql$`)

// loadMigrations reads every embedded migrations/*.sql file and returns
// them in version order. It fails loudly on a filename that does not
// match the NNNN_name.sql convention or on a duplicate version rather
// than silently picking one.
func loadMigrations() ([]migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	migrations := make([]migration, 0, len(entries))
	seen := make(map[int]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationFilenamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("migration file %s does not match NNNN_name.sql", entry.Name())
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("migration file %s: invalid version: %w", entry.Name(), err)
		}
		if existing, dup := seen[version]; dup {
			return nil, fmt.Errorf("duplicate migration version %d: %s and %s", version, existing, entry.Name())
		}
		seen[version] = entry.Name()

		content, err := migrationFiles.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration file %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(content)
		migrations = append(migrations, migration{
			Version:  version,
			Name:     match[2],
			SQL:      string(content),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

// migrationsRequiringForeignKeysOff names every migration version that must
// run with PRAGMA foreign_keys=OFF for its own duration (V4-13, confirmed
// with the user before writing this file): SQLite has no ALTER TABLE ...
// DROP/MODIFY CONSTRAINT, so widening a CHECK constraint on a table other
// tables hold real foreign-key references into (durable_jobs, referenced by
// write_leases.holder_job_id, repository_probe_attempts.job_id and
// readiness_evidence's own two job_id columns) requires the full
// create-copy-drop-rename rebuild SQLite's own documentation describes for
// exactly this case — and that rebuild's own DROP TABLE step is rejected by
// SQLite's FK enforcement while any live referencing row exists, unless
// foreign_keys is off. That pragma is a documented no-op when merely issued
// INSIDE an already-open transaction (verified empirically against this
// project's own modernc.org/sqlite driver before writing this code), so a
// migration that needs it names itself here and gets routed through
// applyMigrationWithForeignKeysOff (below) instead of the ordinary path —
// every other migration is completely unaffected. Migration 25 is this
// project's first migration to ever need it (durable_jobs.job_class's own
// CONTROL allow-list, migration 23, deliberately excluded RECOVERY_REAPER
// "as a separate, future decision" — V4-13 is that decision, and by the
// time it lands durable_jobs already has real production rows from every
// merged V4 task, so the identical-shape-but-empty-table rebuild technique
// 0002/0003/0006/0016/0021's own doc comments already establish no longer
// applies here).
var migrationsRequiringForeignKeysOff = map[int]bool{
	25: true,
	// 32 (V5-14, 0032_artifacts_purged_state.sql) widens artifacts'
	// own attach_state CHECK the identical way — see that migration's own
	// doc comment: messages.content_artifact_id REFERENCES artifacts(id)
	// (0028_messages.sql) means artifacts is not empty by the time this
	// migration runs against any real database.
	32: true,
}

// Migrate applies every pending numbered migration in version order. Each
// migration commits — its own schema change and its own schema_migrations
// checksum record together — in ONE transaction of its own: a migration is
// atomic and exactly-once with respect to itself, and resumable between
// migrations (a later migration's own failure never rolls back an earlier
// one that already committed). An already-recorded migration is never
// re-applied; its checksum is instead verified against the embedded SQL, so
// a migration file edited after it shipped is caught rather than silently
// ignored — "cấm sửa migration đã ghi checksum" is enforced by the loader
// itself, not by convention.
//
// V4-13 correction (confirmed with the user before making this change):
// this used to wrap every pending migration in ONE SHARED transaction
// ("a failure at any point rolls back everything this call would otherwise
// have applied"). That guarantee could never support a migration that
// genuinely needs PRAGMA foreign_keys=OFF for its own duration — see
// migrationsRequiringForeignKeysOff's own doc comment — since that pragma
// is a no-op inside an already-open transaction, and this project's first
// migration to need it (25) could not honor it under the old design at
// all. The whole call is still pinned to one *sql.Conn (rather than letting
// connection pooling hand different statements to different underlying
// connections): PRAGMA foreign_keys is connection-scoped, so migration 25's
// own OFF/ON toggle would be invisible to any other connection anyway, and
// every OTHER migration's own read/apply/record sequence still needs to
// observe the exact same connection's own view of schema_migrations as the
// one before and after it in this same call.
func (s *Store) Migrate(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, bootstrapSchema); err != nil {
		return fmt.Errorf("bootstrap migrations: %w", err)
	}

	for _, m := range migrations {
		if err := applyOneMigration(ctx, conn, m); err != nil {
			return err
		}
	}
	return nil
}

// applyOneMigration applies migration m if it has not been applied yet
// (verifying its checksum instead, with no write at all, if it has), routing
// through applyMigrationWithForeignKeysOff for the small set of migrations
// migrationsRequiringForeignKeysOff names.
func applyOneMigration(ctx context.Context, conn *sql.Conn, m migration) error {
	var applied string
	err := conn.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = ?`, m.Version).Scan(&applied)
	switch {
	case err == nil:
		if applied != m.Checksum {
			return fmt.Errorf("migration %d (%s) checksum mismatch: database=%s code=%s — an applied migration must never be edited", m.Version, m.Name, applied, m.Checksum)
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
		// Not yet applied — falls through to apply it below.
	default:
		return fmt.Errorf("read migration %d (%s) state: %w", m.Version, m.Name, err)
	}

	if migrationsRequiringForeignKeysOff[m.Version] {
		return applyMigrationWithForeignKeysOff(ctx, conn, m)
	}

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration %d (%s): %w", m.Version, m.Name, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("apply migration %d (%s): %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(?, ?, ?)`,
		m.Version, m.Checksum, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record migration %d (%s): %w", m.Version, m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d (%s): %w", m.Version, m.Name, err)
	}
	return nil
}

// applyMigrationWithForeignKeysOff runs m with PRAGMA foreign_keys=OFF for
// its own duration — the exact procedure SQLite's own documentation
// describes for changing the schema of a table other tables hold live
// foreign-key references into: disable enforcement OUTSIDE any transaction
// (verified both before and after toggling, so a silent no-op is caught
// immediately rather than surfacing as a confusing failure deep inside the
// rebuild itself), perform the rebuild inside its own transaction, run
// PRAGMA foreign_key_check inside that SAME transaction before ever
// recording the migration as applied (a single violation rolls the whole
// migration back — m.SQL's own rebuild is trusted only once this check
// proves every foreign key in the database still resolves), then restore
// foreign_keys=ON again on every return path — success or failure — via
// defer, so a mid-rebuild error never leaves the connection with
// enforcement silently disabled for whatever runs after Migrate returns.
func applyMigrationWithForeignKeysOff(ctx context.Context, conn *sql.Conn, m migration) (err error) {
	if err := verifyForeignKeysPragma(ctx, conn, true); err != nil {
		return fmt.Errorf("migration %d (%s): before disabling foreign_keys: %w", m.Version, m.Name, err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("migration %d (%s): disable foreign_keys: %w", m.Version, m.Name, err)
	}
	if err := verifyForeignKeysPragma(ctx, conn, false); err != nil {
		return fmt.Errorf("migration %d (%s): after disabling foreign_keys: %w", m.Version, m.Name, err)
	}

	defer func() {
		if _, restoreErr := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); restoreErr != nil && err == nil {
			err = fmt.Errorf("migration %d (%s): re-enable foreign_keys: %w", m.Version, m.Name, restoreErr)
			return
		}
		if verifyErr := verifyForeignKeysPragma(ctx, conn, true); verifyErr != nil && err == nil {
			err = fmt.Errorf("migration %d (%s): after re-enabling foreign_keys: %w", m.Version, m.Name, verifyErr)
		}
	}()

	tx, beginErr := conn.BeginTx(ctx, &sql.TxOptions{})
	if beginErr != nil {
		return fmt.Errorf("begin migration %d (%s): %w", m.Version, m.Name, beginErr)
	}
	defer func() { _ = tx.Rollback() }()

	if _, execErr := tx.ExecContext(ctx, m.SQL); execErr != nil {
		return fmt.Errorf("apply migration %d (%s): %w", m.Version, m.Name, execErr)
	}

	violationRows, checkErr := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if checkErr != nil {
		return fmt.Errorf("migration %d (%s): foreign_key_check: %w", m.Version, m.Name, checkErr)
	}
	violations := 0
	for violationRows.Next() {
		violations++
	}
	iterErr := violationRows.Err()
	violationRows.Close()
	if iterErr != nil {
		return fmt.Errorf("migration %d (%s): iterate foreign_key_check: %w", m.Version, m.Name, iterErr)
	}
	if violations != 0 {
		return fmt.Errorf("migration %d (%s): foreign_key_check found %d violation(s) after rebuild, rolled back", m.Version, m.Name, violations)
	}

	if _, recordErr := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(?, ?, ?)`,
		m.Version, m.Checksum, time.Now().UTC().Format(time.RFC3339Nano)); recordErr != nil {
		return fmt.Errorf("record migration %d (%s): %w", m.Version, m.Name, recordErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit migration %d (%s): %w", m.Version, m.Name, commitErr)
	}
	return nil
}

// verifyForeignKeysPragma reads PRAGMA foreign_keys back and fails loudly if
// it does not match want — the explicit "did that actually take effect"
// check applyMigrationWithForeignKeysOff's own doc comment describes,
// standing in for trusting a bare PRAGMA statement's own success silently.
func verifyForeignKeysPragma(ctx context.Context, conn *sql.Conn, want bool) error {
	var got int
	if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&got); err != nil {
		return fmt.Errorf("query foreign_keys pragma: %w", err)
	}
	wantInt := 0
	if want {
		wantInt = 1
	}
	if got != wantInt {
		return fmt.Errorf("foreign_keys pragma = %d, want %d", got, wantInt)
	}
	return nil
}
