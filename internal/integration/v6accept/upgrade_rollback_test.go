package v6accept

// V8-10 (docs/design/10-v8-alpha-hardening.md) "Hoàn thành khi: unsupported
// downgrade không làm hỏng DB im lặng", proven against the REAL compiled
// `aw` binary — the same one every operator runs — not just against the
// sqlite adapter the unit-level suite in internal/adapters/sqlite covers
// (upgrade_rollback_test.go there is the upgrade matrix and the backup-
// restore rehearsal; this file is the process-level proof that the refusal
// reaches the operator intact through `aw`'s own startup path).
//
// "A database a newer release already migrated" is constructed by recording
// one extra, higher migration version in a real database's schema_migrations
// table — exactly what the newer release's own Migrate would have left
// behind — and the current binary then plays the OLDER binary that does not
// know it.
import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// awDoctor runs `aw doctor` (a one-shot that opens the database through the
// real production Open path, migrations included) against the given roots.
func awDoctor(t *testing.T, bin, dbPath, artifactRoot, workspaceRoot string) (exitCode int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin, "--db", dbPath, "--artifact-root", artifactRoot, "--workspace-root", workspaceRoot, "doctor")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("start aw doctor: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("aw doctor: %v", err)
			}
			return exitErr.ExitCode(), out.String(), errOut.String()
		}
		return 0, out.String(), errOut.String()
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("aw doctor did not finish within 60s\nstdout: %s\nstderr: %s", out.String(), errOut.String())
		return -1, "", ""
	}
}

// schemaFingerprint digests every sqlite_master row and every
// schema_migrations row of the database file at path, read through a plain
// connection of its own (never the code under test).
func schemaFingerprint(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open %s for inspection: %v", path, err)
	}
	defer db.Close()
	digest := sha256.New()
	rows, err := db.Query(`SELECT type, name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	for rows.Next() {
		var kind, name, ddl string
		if err := rows.Scan(&kind, &name, &ddl); err != nil {
			t.Fatalf("scan sqlite_master: %v", err)
		}
		fmt.Fprintf(digest, "%s|%s|%s\n", kind, name, ddl)
	}
	rows.Close()
	migrations, err := db.Query(`SELECT version, checksum, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer migrations.Close()
	for migrations.Next() {
		var version int
		var checksum, appliedAt string
		if err := migrations.Scan(&version, &checksum, &appliedAt); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		fmt.Fprintf(digest, "migration|%d|%s|%s\n", version, checksum, appliedAt)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func TestV8UpgradeRollback_RealBinaryRefusesNewerDatabaseAndLeavesItUntouched(t *testing.T) {
	requireAcceptance(t)
	bin := builtBinaries(t).aw

	root := t.TempDir()
	dbPath := filepath.Join(root, "aw.db")
	artifactRoot := filepath.Join(root, "artifacts")
	workspaceRoot := filepath.Join(root, "workspaces")
	for _, dir := range []string{artifactRoot, workspaceRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	// 1. The real binary creates and migrates a database, and is healthy on it.
	if code, stdout, stderr := awDoctor(t, bin, dbPath, artifactRoot, workspaceRoot); code != 0 {
		t.Fatalf("aw doctor on a fresh database exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	// 2. A "newer release" already migrated it: one extra recorded migration.
	func() {
		db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
		if err != nil {
			t.Fatalf("open for injection: %v", err)
		}
		defer db.Close()
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(9999, 'newer-release-checksum', '2099-01-01T00:00:00Z')`); err != nil {
			t.Fatalf("record the newer release's migration: %v", err)
		}
	}()
	before := schemaFingerprint(t, dbPath)

	// 3. The binary that does not know migration 9999 must refuse, loudly,
	// with a way out — and must not have touched the database.
	code, stdout, stderr := awDoctor(t, bin, dbPath, artifactRoot, workspaceRoot)
	if code == 0 {
		t.Fatalf("aw doctor opened a database recording an unknown migration without failing\nstdout: %s", stdout)
	}
	for _, want := range []string{"newer than this binary supports", "restore a backup", "9999"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr does not mention %q\nstderr: %s", want, stderr)
		}
	}
	if after := schemaFingerprint(t, dbPath); after != before {
		t.Fatalf("the refusing binary modified the database (schema or migration records changed)")
	}
}
