package main

// Exercises the exact production entrypoints (runBackup/runRestore) the
// compiled binary's own main() calls — mirrors cmd/aw/cli_test.go's own
// convention of testing the real dispatch functions directly rather than
// spawning a subprocess.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func seedRealDB(t *testing.T, path string) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: "p1", Name: "p1"})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func TestBackupThenRestore_RealBinaryEntrypoints_RoundTrips(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "aw.db")
	seedRealDB(t, dbPath)

	backupDir := filepath.Join(root, "backup")
	if err := runBackup([]string{"--db", dbPath, "--out", backupDir}); err != nil {
		t.Fatalf("runBackup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "snapshot.db")); err != nil {
		t.Fatalf("expected snapshot.db: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "manifest.json")); err != nil {
		t.Fatalf("expected manifest.json: %v", err)
	}

	restoreRoot := filepath.Join(root, "restore")
	artifactRoot := filepath.Join(root, "artifacts")
	if err := runRestore([]string{"--backup", backupDir, "--into", restoreRoot, "--artifact-root", artifactRoot}); err != nil {
		t.Fatalf("runRestore: %v", err)
	}

	restoredDBPath := filepath.Join(restoreRoot, "restored.db")
	restoredStore, err := sqlite.Open(context.Background(), restoredDBPath)
	if err != nil {
		t.Fatalf("open restored database: %v", err)
	}
	defer restoredStore.Close()
	restoredUOW := sqlite.NewUnitOfWork(restoredStore)
	if err := restoredUOW.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		project, err := tx.Catalog().GetProject(context.Background(), "p1")
		if err != nil {
			return err
		}
		if project.ID != "p1" {
			t.Fatalf("restored project ID = %q, want p1", project.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("read real project from restored database: %v", err)
	}
}

func TestRunBackup_MissingFlags_ReturnsUsageError(t *testing.T) {
	if err := runBackup(nil); err == nil {
		t.Fatal("runBackup with no flags succeeded, want an error")
	}
}

func TestRunRestore_MissingFlags_ReturnsUsageError(t *testing.T) {
	if err := runRestore(nil); err == nil {
		t.Fatal("runRestore with no flags succeeded, want an error")
	}
}

func TestRunRestore_RefusesAnAlreadyMaterializedTempRoot(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "aw.db")
	seedRealDB(t, dbPath)
	backupDir := filepath.Join(root, "backup")
	if err := runBackup([]string{"--db", dbPath, "--out", backupDir}); err != nil {
		t.Fatalf("runBackup: %v", err)
	}

	restoreRoot := filepath.Join(root, "restore")
	artifactRoot := filepath.Join(root, "artifacts")
	if err := runRestore([]string{"--backup", backupDir, "--into", restoreRoot, "--artifact-root", artifactRoot}); err != nil {
		t.Fatalf("first runRestore: %v", err)
	}
	// A second restore into the SAME temp root must refuse — never silently
	// overwrite a previously restored database.
	if err := runRestore([]string{"--backup", backupDir, "--into", restoreRoot, "--artifact-root", artifactRoot}); err == nil {
		t.Fatal("second runRestore into the same --into root succeeded, want a refusal")
	}
}
