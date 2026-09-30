package sqlite

// V8-10 (docs/design/10-v8-alpha-hardening.md): "nâng DB/config/artifact từ
// release candidate trước mà không sửa migration cũ ... fixture previous-
// version, upgrade, restart, inspect runs/evidence; rollback binary chỉ khi
// schema compatible, nếu không fail với hướng restore backup ... Hoàn thành
// khi: unsupported downgrade không làm hỏng DB im lặng."
//
// A "previous release" database is built here by applying exactly the first
// K embedded migrations. That is not an approximation of what an older
// build produced: migrations are immutable and checksummed (see
// TestMigrate_ChecksumTamperDetected — an edited shipped migration is
// rejected on open), so the first K migrations ARE what a build whose
// highest migration was K applied, byte for byte. Likewise an "older
// binary" is migrateWith(all[:K]).
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// previousReleaseDatabase creates a database at path exactly as a release
// whose highest migration was version would have left it, seeded with real
// rows, and returns the still-open unmigrated-beyond-version store.
func previousReleaseDatabase(t *testing.T, path string, version int) *Store {
	t.Helper()
	ctx := context.Background()
	store := openWithoutMigrating(t, path)
	applyMigrationsThrough(t, ctx, store, version)
	if version >= 24 {
		// durable_jobs plus all three tables holding a live foreign key into
		// it — the exact shape migrations 25/32/34's table rebuilds must
		// carry across (SeedFixtureOwners also creates project "p1").
		seedThreeReferencingRows(t, ctx, store, "p1", "f1", "wi1", "repo1", "rw1", "ws1")
		return store
	}
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO projects(id, name, status, version, created_at, updated_at) VALUES(?,?,?,?,?,?)`,
		"p1", "previous release project", "ACTIVE", 1, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed project at schema version %d: %v", version, err)
	}
	return store
}

// tableRowCounts counts rows in every ordinary table except sqlite_* and
// schema_migrations itself (its growth on upgrade is asserted separately).
func tableRowCounts(t *testing.T, store *Store) map[string]int {
	t.Helper()
	ctx := context.Background()
	rows, err := store.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, name)
	}
	rows.Close()
	counts := make(map[string]int, len(names))
	for _, name := range names {
		var n int
		if err := store.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, name)).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		counts[name] = n
	}
	return counts
}

// appliedMigrations returns schema_migrations as version -> "checksum|applied_at".
func appliedMigrations(t *testing.T, store *Store) map[int]string {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(), `SELECT version, checksum, applied_at FROM schema_migrations`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	applied := map[int]string{}
	for rows.Next() {
		var version int
		var checksum, appliedAt string
		if err := rows.Scan(&version, &checksum, &appliedAt); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		applied[version] = checksum + "|" + appliedAt
	}
	return applied
}

// databaseFingerprint digests the whole schema (sqlite_master) plus every
// schema_migrations row, so a test can prove an operation changed nothing.
func databaseFingerprint(t *testing.T, store *Store) string {
	t.Helper()
	ctx := context.Background()
	rows, err := store.db.QueryContext(ctx, `SELECT type, name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	digest := sha256.New()
	for rows.Next() {
		var kind, name, ddl string
		if err := rows.Scan(&kind, &name, &ddl); err != nil {
			t.Fatalf("scan sqlite_master: %v", err)
		}
		fmt.Fprintf(digest, "%s|%s|%s\n", kind, name, ddl)
	}
	rows.Close()
	applied := appliedMigrations(t, store)
	versions := make([]int, 0, len(applied))
	for v := range applied {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	for _, v := range versions {
		fmt.Fprintf(digest, "migration|%d|%s\n", v, applied[v])
	}
	counts := tableRowCounts(t, store)
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(digest, "rows|%s|%d\n", name, counts[name])
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// requireHealthyDatabase asserts PRAGMA integrity_check is "ok" and
// PRAGMA foreign_key_check reports no violation.
func requireHealthyDatabase(t *testing.T, store *Store, what string) {
	t.Helper()
	ctx := t.Context()
	var integrity string
	if err := store.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("%s: integrity_check: %v", what, err)
	}
	if integrity != "ok" {
		t.Fatalf("%s: integrity_check = %q, want ok", what, integrity)
	}
	rows, err := store.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("%s: foreign_key_check: %v", what, err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatalf("%s: foreign_key_check reported at least one violation", what)
	}
}

func projectName(t *testing.T, store *Store, id string) (string, bool) {
	t.Helper()
	var name string
	err := store.db.QueryRowContext(context.Background(), `SELECT name FROM projects WHERE id = ?`, id).Scan(&name)
	if err != nil {
		return "", false
	}
	return name, true
}

// upgradeMatrixVersions picks the previous-release schema versions the
// upgrade matrix starts from: the very first schema, a mid-history one, the
// version immediately BEFORE each of the three migrations that rebuild a
// table other tables reference (25, 32, 34 — migrationsRequiringForeignKeysOff),
// the last few, and the immediate predecessor of head. Every entry must be
// strictly below head (there is nothing to upgrade at head itself).
func upgradeMatrixVersions(t *testing.T, all []migration) []int {
	t.Helper()
	head := all[len(all)-1].Version
	candidates := []int{1, 10, 24, 31, 33, head - 3, head - 1}
	seen := map[int]bool{}
	var picked []int
	for _, v := range candidates {
		if v < 1 || v >= head || seen[v] {
			continue
		}
		seen[v] = true
		picked = append(picked, v)
	}
	sort.Ints(picked)
	return picked
}

// TestUpgradeMatrix_PreviousReleaseToCurrent is V8-10's upgrade matrix: for
// each previous-release schema version, upgrade through the real production
// Open path and prove (1) every migration that release had already applied
// is untouched — same checksum AND same applied_at, i.e. no old migration
// was edited or re-run — (2) every newer migration is now recorded with the
// embedded checksum, (3) no row in any table the old schema had was lost,
// (4) integrity and foreign-key checks are clean, (5) a restart is a no-op,
// and (6) the seeded project is still readable.
func TestUpgradeMatrix_PreviousReleaseToCurrent(t *testing.T) {
	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	ctx := context.Background()
	for _, version := range upgradeMatrixVersions(t, all) {
		t.Run(fmt.Sprintf("from_schema_%04d", version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "previous-release.db")
			old := previousReleaseDatabase(t, path, version)
			countsBefore := tableRowCounts(t, old)
			appliedBefore := appliedMigrations(t, old)
			wantName, ok := projectName(t, old, "p1")
			if !ok {
				t.Fatalf("seeded project p1 missing before upgrade")
			}
			if err := old.Close(); err != nil {
				t.Fatalf("close previous-release store: %v", err)
			}

			upgraded, err := Open(ctx, path)
			if err != nil {
				t.Fatalf("Open (upgrade from schema %d): %v", version, err)
			}
			defer upgraded.Close()

			appliedAfter := appliedMigrations(t, upgraded)
			if len(appliedAfter) != len(all) {
				t.Fatalf("schema_migrations has %d row(s) after upgrade, want %d (every embedded migration)", len(appliedAfter), len(all))
			}
			for _, m := range all {
				got, present := appliedAfter[m.Version]
				if !present {
					t.Fatalf("migration %d missing after upgrade", m.Version)
				}
				if m.Version <= version {
					if got != appliedBefore[m.Version] {
						t.Fatalf("migration %d's record changed during upgrade: before=%q after=%q (an old migration must never be edited or re-applied)", m.Version, appliedBefore[m.Version], got)
					}
					continue
				}
				if !strings.HasPrefix(got, m.Checksum+"|") {
					t.Fatalf("migration %d recorded with checksum %q, want %s", m.Version, got, m.Checksum)
				}
			}

			countsAfter := tableRowCounts(t, upgraded)
			for table, before := range countsBefore {
				after, present := countsAfter[table]
				if !present {
					t.Fatalf("table %s existed at schema %d but is gone after upgrade", table, version)
				}
				if after != before {
					t.Fatalf("table %s had %d row(s) at schema %d and has %d after upgrade", table, before, version, after)
				}
			}
			requireHealthyDatabase(t, upgraded, fmt.Sprintf("after upgrade from schema %d", version))

			gotName, ok := projectName(t, upgraded, "p1")
			if !ok || gotName != wantName {
				t.Fatalf("project p1 after upgrade = (%q, %v), want (%q, true)", gotName, ok, wantName)
			}

			fingerprint := databaseFingerprint(t, upgraded)
			if err := upgraded.Close(); err != nil {
				t.Fatalf("close upgraded store: %v", err)
			}
			restarted, err := Open(ctx, path)
			if err != nil {
				t.Fatalf("Open (restart after upgrade): %v", err)
			}
			defer restarted.Close()
			if got := databaseFingerprint(t, restarted); got != fingerprint {
				t.Fatalf("restart after upgrade changed the database (schema, migration records or row counts differ)")
			}
		})
	}
}

// requireSchemaNewerError asserts err is a *SchemaNewerThanBinaryError with
// the given versions and a message pointing at backup restore.
func requireSchemaNewerError(t *testing.T, err error, wantDatabase, wantBinary int, wantUnknown []int) *SchemaNewerThanBinaryError {
	t.Helper()
	var schemaErr *SchemaNewerThanBinaryError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("error = %v, want a *SchemaNewerThanBinaryError", err)
	}
	if schemaErr.DatabaseVersion != wantDatabase || schemaErr.BinaryVersion != wantBinary {
		t.Fatalf("versions = (database %d, binary %d), want (%d, %d)", schemaErr.DatabaseVersion, schemaErr.BinaryVersion, wantDatabase, wantBinary)
	}
	if fmt.Sprint(schemaErr.UnknownVersions) != fmt.Sprint(wantUnknown) {
		t.Fatalf("UnknownVersions = %v, want %v", schemaErr.UnknownVersions, wantUnknown)
	}
	if !strings.Contains(err.Error(), "restore a backup") {
		t.Fatalf("error %q does not tell the operator to restore a backup", err)
	}
	return schemaErr
}

// migrationsThrough is what a binary whose highest migration is version
// embeds. Migration numbers are NOT contiguous (there is no 0013), so this
// filters by version rather than slicing by index.
func migrationsThrough(all []migration, version int) []migration {
	var out []migration
	for _, m := range all {
		if m.Version <= version {
			out = append(out, m)
		}
	}
	return out
}

// versionsAfter lists every embedded migration version strictly above version.
func versionsAfter(all []migration, version int) []int {
	var out []int
	for _, m := range all {
		if m.Version > version {
			out = append(out, m.Version)
		}
	}
	return out
}

// TestDowngradeGuard_OlderBinaryRefusesUpgradedDatabase proves an older
// binary (a truncated migration set) refuses a database a newer release
// already migrated — for several distances of downgrade — and that refusing
// changes NOTHING in the database (schema, migration records, rows).
func TestDowngradeGuard_OlderBinaryRefusesUpgradedDatabase(t *testing.T) {
	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	head := all[len(all)-1].Version
	ctx := context.Background()

	path := filepath.Join(t.TempDir(), "upgraded.db")
	current, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := current.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, olderHighest := range []int{head - 1, 20, 1} {
		t.Run(fmt.Sprintf("binary_knows_up_to_%04d", olderHighest), func(t *testing.T) {
			older := openWithoutMigrating(t, path)
			defer older.Close()
			before := databaseFingerprint(t, older)

			err := older.migrateWith(ctx, migrationsThrough(all, olderHighest))
			if err == nil {
				t.Fatalf("an older binary (knows up to %d) opened a database at schema %d without error", olderHighest, head)
			}
			requireSchemaNewerError(t, err, head, olderHighest, versionsAfter(all, olderHighest))

			if after := databaseFingerprint(t, older); after != before {
				t.Fatalf("the refusing binary modified the database (fingerprint changed)")
			}
		})
	}
}

// TestDowngradeGuard_OpenRefusesADatabaseWithAnUnknownMigration runs the
// refusal through the real production Open path (what `aw serve`, `aw
// worker`, `aw doctor` and aw-maintenance all call) with a recorded
// migration version no build carries.
func TestDowngradeGuard_OpenRefusesADatabaseWithAnUnknownMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "from-the-future.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	all, _ := loadMigrations()
	head := all[len(all)-1].Version
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(9999, 'future-checksum', '2099-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("record future migration: %v", err)
	}
	before := databaseFingerprint(t, store)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if reopened, err := Open(ctx, path); err == nil {
		reopened.Close()
		t.Fatal("Open accepted a database recording migration 9999")
	} else {
		requireSchemaNewerError(t, err, 9999, head, []int{9999})
	}

	check := openWithoutMigrating(t, path)
	defer check.Close()
	if after := databaseFingerprint(t, check); after != before {
		t.Fatalf("a refused Open modified the database (fingerprint changed)")
	}
}

// TestDowngradeGuard_MigrationHistoryGapIsRefusedToo covers the other
// mismatch shape: the database recorded a version below this binary's
// highest that this binary does not carry (a different lineage), which is
// not a "newer" database but must be refused just as loudly.
func TestDowngradeGuard_MigrationHistoryGapIsRefusedToo(t *testing.T) {
	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	head := all[len(all)-1].Version
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "other-lineage.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	const dropped = 5
	var without []migration
	for _, m := range all {
		if m.Version != dropped {
			without = append(without, m)
		}
	}
	err = store.migrateWith(ctx, without)
	schemaErr := requireSchemaNewerError(t, err, head, head, []int{dropped})
	if !strings.Contains(schemaErr.Error(), "does not match this build") {
		t.Fatalf("message %q should describe a history mismatch, not a newer schema", schemaErr)
	}
}

// TestDowngradeGuard_SameSchemaVersionRollbackStillOpens is the positive
// control: rolling a binary back is fine exactly when no new migration was
// applied since, i.e. the older binary's migration set equals the
// database's.
func TestDowngradeGuard_SameSchemaVersionRollbackStillOpens(t *testing.T) {
	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	const version = 33
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "same-version.db")
	first := previousReleaseDatabase(t, path, version)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again := openWithoutMigrating(t, path)
	defer again.Close()
	before := databaseFingerprint(t, again)
	if err := again.migrateWith(ctx, migrationsThrough(all, version)); err != nil {
		t.Fatalf("a binary with the database's own migration set must open it: %v", err)
	}
	if after := databaseFingerprint(t, again); after != before {
		t.Fatalf("reopening at the same schema version changed the database")
	}
}

// TestRollbackRehearsal_RestoreBackupTakenBeforeUpgrade is V8-10's failure-
// rollback evidence, end to end: a previous release's database is backed up,
// upgraded, written to by the new release, then the OLD binary is pointed at
// the upgraded database (refused, database untouched), the pre-upgrade backup
// is restored, the old binary runs against the restored copy with its
// original data, and finally the upgrade can be retried from that copy.
func TestRollbackRehearsal_RestoreBackupTakenBeforeUpgrade(t *testing.T) {
	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	head := all[len(all)-1].Version
	const oldVersion = 33
	ctx := context.Background()
	dir := t.TempDir()
	livePath := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "pre-upgrade-backup.db")

	// 1. The previous release's live database, then its pre-upgrade backup
	// (the operator step the upgrade guide requires).
	release := previousReleaseDatabase(t, livePath, oldVersion)
	countsAtBackup := tableRowCounts(t, release)
	if err := release.BackupTo(ctx, backupPath); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	if err := release.Close(); err != nil {
		t.Fatalf("Close previous release: %v", err)
	}

	// 2. Upgrade, and let the new release write something the backup lacks.
	upgraded, err := Open(ctx, livePath)
	if err != nil {
		t.Fatalf("Open (upgrade): %v", err)
	}
	if _, err := upgraded.db.ExecContext(ctx,
		`INSERT INTO projects(id, name, status, version, created_at, updated_at) VALUES('p-after-upgrade','written by the new release','ACTIVE',1,'2026-09-30T00:00:00Z','2026-09-30T00:00:00Z')`); err != nil {
		t.Fatalf("new release write: %v", err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatalf("Close upgraded: %v", err)
	}

	// 3. Naive rollback: the old binary against the upgraded database is
	// refused and leaves it exactly as it was.
	naive := openWithoutMigrating(t, livePath)
	fingerprintUpgraded := databaseFingerprint(t, naive)
	err = naive.migrateWith(ctx, migrationsThrough(all, oldVersion))
	requireSchemaNewerError(t, err, head, oldVersion, versionsAfter(all, oldVersion))
	if got := databaseFingerprint(t, naive); got != fingerprintUpgraded {
		t.Fatalf("the refused rollback modified the upgraded database")
	}
	if _, ok := projectName(t, naive, "p-after-upgrade"); !ok {
		t.Fatalf("the new release's own write was lost by a refused rollback")
	}
	if err := naive.Close(); err != nil {
		t.Fatalf("Close naive: %v", err)
	}

	// 4. Supported rollback: restore the backup into a fresh file; the old
	// binary opens it with its original data and without the new write.
	restoredPath := filepath.Join(dir, "restored.db")
	snapshot, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if err := os.WriteFile(restoredPath, snapshot, 0o600); err != nil {
		t.Fatalf("materialize backup: %v", err)
	}
	restoredOld := openWithoutMigrating(t, restoredPath)
	if err := restoredOld.migrateWith(ctx, migrationsThrough(all, oldVersion)); err != nil {
		t.Fatalf("the old binary must open the restored pre-upgrade backup: %v", err)
	}
	if _, ok := projectName(t, restoredOld, "p1"); !ok {
		t.Fatalf("original project p1 missing from the restored backup")
	}
	if _, ok := projectName(t, restoredOld, "p-after-upgrade"); ok {
		t.Fatalf("the post-upgrade write is present in a backup taken before the upgrade")
	}
	countsRestored := tableRowCounts(t, restoredOld)
	for table, want := range countsAtBackup {
		if countsRestored[table] != want {
			t.Fatalf("restored table %s has %d row(s), want %d (as at backup time)", table, countsRestored[table], want)
		}
	}
	requireHealthyDatabase(t, restoredOld, "restored backup under the old binary")
	if err := restoredOld.Close(); err != nil {
		t.Fatalf("Close restoredOld: %v", err)
	}

	// 5. The upgrade can be retried from the restored copy.
	retried, err := Open(ctx, restoredPath)
	if err != nil {
		t.Fatalf("Open (retry upgrade from restored backup): %v", err)
	}
	defer retried.Close()
	if got := len(appliedMigrations(t, retried)); got != len(all) {
		t.Fatalf("after retry schema_migrations has %d row(s), want %d", got, len(all))
	}
	requireHealthyDatabase(t, retried, "retried upgrade")
}
