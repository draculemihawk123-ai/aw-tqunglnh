package sqlite

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// expectedMigration1Checksum is the SHA256 of migrationV1's exact former
// Go-string content (verified byte-for-byte against
// internal/adapters/sqlite/migrations/0001_initial_schema.sql before this
// file existed). A database migrated by the old hardcoded-string code
// records exactly this checksum for version 1; the new loader must
// compute the identical value, or every already-migrated database
// (docs/design/03-v1-alpha-foundation.md V1-04's "DB migration V1 cũ")
// would fail with a false checksum mismatch on first open.
const expectedMigration1Checksum = "701c2afde1d06980a30ce515e346e544666363c68f286352e52383266e43213a"

func TestMigrate_EmptyDatabase(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-empty.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var checksum string
	if err := store.db.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = 1`).Scan(&checksum); err != nil {
		t.Fatalf("read recorded checksum: %v", err)
	}
	if checksum != expectedMigration1Checksum {
		t.Fatalf("recorded checksum = %s, want %s (must match the pre-V1-04 code's checksum for backward compatibility)", checksum, expectedMigration1Checksum)
	}
}

func TestMigrate_ExistingFixtureDataStillOpens(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-fixture.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO projects(id, name, status, version, created_at, updated_at) VALUES(?,?,?,?,?,?)`,
		"p1", "test project", "ACTIVE", 1, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert into migrated schema: %v", err)
	}
	var name string
	if err := store.db.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = ?`, "p1").Scan(&name); err != nil {
		t.Fatalf("read back from migrated schema: %v", err)
	}
	if name != "test project" {
		t.Fatalf("name = %q, want %q", name, "test project")
	}
}

func TestMigrate_RestartIdempotency(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-restart.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open (first): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	restarted, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open (restart) should succeed against an already-migrated database: %v", err)
	}
	defer restarted.Close()

	var count int
	if err := restarted.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_migrations has %d row(s) for version 1 after restart, want exactly 1 (no duplicate application)", count)
	}

	// Migrate is also safe to call again directly on the same open Store,
	// not just via a fresh Open.
	if err := restarted.Migrate(ctx); err != nil {
		t.Fatalf("second in-process Migrate call should be a no-op, got: %v", err)
	}
}

func TestMigrate_ChecksumTamperDetected(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-tamper.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if _, err := store.db.ExecContext(ctx, `UPDATE schema_migrations SET checksum = 'tampered-checksum' WHERE version = 1`); err != nil {
		t.Fatalf("tamper with recorded checksum: %v", err)
	}

	err = store.Migrate(ctx)
	if err == nil {
		t.Fatal("Migrate should reject a tampered recorded checksum")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v, want it to mention a checksum mismatch", err)
	}
}

// TestMigrate_FailedMigrationRollsBackOnlyItself simulates a migration that
// cannot apply (a pre-existing conflicting table) and proves ONLY that one
// migration's own attempt rolls back — V4-13 correction, confirmed with the
// user: Migrate no longer wraps every pending migration in one shared
// transaction ("a failure at any point rolls back everything"); each
// migration now commits, or rolls back, on its own (migrations.go's own doc
// comment). bootstrapSchema's own CREATE TABLE IF NOT EXISTS runs
// unconditionally, before the per-migration loop even begins, so
// schema_migrations itself always survives now — what a failed migration
// must never leave behind is its OWN row in it. See
// TestMigrate_EarlierMigrationsSurviveALaterFailure for the complementary
// proof that an EARLIER, already-committed migration survives a LATER
// migration's own failure too.
func TestMigrate_FailedMigrationRollsBackOnlyItself(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-rollback.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()

	if _, err := store.db.ExecContext(ctx, `CREATE TABLE projects (id TEXT PRIMARY KEY, conflicting_column TEXT)`); err != nil {
		t.Fatalf("pre-create conflicting table: %v", err)
	}

	err := store.Migrate(ctx)
	if err == nil {
		t.Fatal("Migrate should fail when migration 1's CREATE TABLE conflicts with an existing table")
	}

	var recordedCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&recordedCount); err != nil {
		t.Fatalf("count schema_migrations rows for version 1: %v", err)
	}
	if recordedCount != 0 {
		t.Fatal("migration 1 recorded as applied despite failing, want no row")
	}

	var columnCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name = 'conflicting_column'`).Scan(&columnCount); err != nil {
		t.Fatalf("inspect projects table: %v", err)
	}
	if columnCount != 1 {
		t.Fatal("the pre-existing conflicting table should be untouched after a failed migration")
	}
}

// TestMigrate_EarlierMigrationsSurviveALaterFailure proves the other half
// of V4-13's own atomicity correction: an earlier migration that already
// committed is never undone by a later migration's own failure. Uses two
// synthetic migration values (not the real embedded ones) so this test
// stays entirely decoupled from any real migration's own content — it is
// only ever exercising applyOneMigration's own per-migration transaction
// boundary, not any particular migration's business logic.
func TestMigrate_EarlierMigrationsSurviveALaterFailure(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-partial-failure.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()

	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, bootstrapSchema); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	first := migration{Version: 90001, Name: "synthetic-ok", SQL: `CREATE TABLE synthetic_ok (id TEXT PRIMARY KEY)`, Checksum: "checksum-ok"}
	// second deliberately re-creates the same table first already made, so
	// its own CREATE TABLE fails.
	second := migration{Version: 90002, Name: "synthetic-fail", SQL: `CREATE TABLE synthetic_ok (id TEXT PRIMARY KEY)`, Checksum: "checksum-fail"}

	if err := applyOneMigration(ctx, conn, first); err != nil {
		t.Fatalf("apply first synthetic migration: %v", err)
	}
	assertRecorded := func(version int, want int) {
		t.Helper()
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&count); err != nil {
			t.Fatalf("count schema_migrations for version %d: %v", version, err)
		}
		if count != want {
			t.Fatalf("schema_migrations count for version %d = %d, want %d", version, count, want)
		}
	}
	assertRecorded(first.Version, 1)

	if err := applyOneMigration(ctx, conn, second); err == nil {
		t.Fatal("second synthetic migration should fail: synthetic_ok already exists")
	}

	// The earlier, already-committed migration must survive this later
	// failure — never rolled back, and the table it created still exists.
	assertRecorded(first.Version, 1)
	assertRecorded(second.Version, 0)
	var synthOkExists int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='synthetic_ok'`).Scan(&synthOkExists); err != nil {
		t.Fatalf("check synthetic_ok table: %v", err)
	}
	if synthOkExists != 1 {
		t.Fatal("synthetic_ok table (created by the first, already-committed migration) should still exist")
	}
}

func TestLoadMigrations_SortedByVersionAndChecksumSet(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("loadMigrations returned no migrations")
	}
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].Version >= migrations[i].Version {
			t.Fatalf("migrations not strictly sorted by version: %d then %d", migrations[i-1].Version, migrations[i].Version)
		}
	}
	for _, m := range migrations {
		if m.Checksum == "" {
			t.Fatalf("migration %d (%s) has an empty checksum", m.Version, m.Name)
		}
		if m.SQL == "" {
			t.Fatalf("migration %d (%s) has empty SQL content", m.Version, m.Name)
		}
	}
	if migrations[0].Version != 1 || migrations[0].Checksum != expectedMigration1Checksum {
		t.Fatalf("migration 1 = %+v, want version 1 with checksum %s", migrations[0], expectedMigration1Checksum)
	}
}

// openWithoutMigrating mirrors Open's connection setup exactly but skips
// the store.Migrate(ctx) call, so a test can prepare conflicting state
// before migration runs.
func openWithoutMigrating(t *testing.T, path string) *Store {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}
	uriPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" && uriPath[0] != '/' {
		uriPath = "/" + uriPath
	}
	u := &url.URL{Scheme: "file", Path: uriPath}
	query := u.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_txlock", "immediate")
	u.RawQuery = query.Encode()

	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return &Store{db: db}
}
