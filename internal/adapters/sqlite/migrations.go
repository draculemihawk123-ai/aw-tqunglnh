package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
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

// Migrate applies every pending numbered migration in version order,
// inside one transaction: a failure at any point rolls back everything
// this call would otherwise have applied, never leaving the schema
// half-migrated. An already-recorded migration is never re-applied; its
// checksum is instead verified against the embedded SQL, so a migration
// file edited after it shipped is caught rather than silently ignored —
// "cấm sửa migration đã ghi checksum" is enforced by the loader itself,
// not by convention.
func (s *Store) Migrate(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, bootstrapSchema); err != nil {
		return fmt.Errorf("bootstrap migrations: %w", err)
	}

	for _, m := range migrations {
		var applied string
		err := tx.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = ?`, m.Version).Scan(&applied)
		switch {
		case err == nil:
			if applied != m.Checksum {
				return fmt.Errorf("migration %d (%s) checksum mismatch: database=%s code=%s — an applied migration must never be edited", m.Version, m.Name, applied, m.Checksum)
			}
		case err == sql.ErrNoRows:
			if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
				return fmt.Errorf("apply migration %d (%s): %w", m.Version, m.Name, err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(?, ?, ?)`,
				m.Version, m.Checksum, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return fmt.Errorf("record migration %d (%s): %w", m.Version, m.Name, err)
			}
		default:
			return fmt.Errorf("read migration %d (%s) state: %w", m.Version, m.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}
