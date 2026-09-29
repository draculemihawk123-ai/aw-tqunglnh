package maintenance_test

// This file exercises the real stack end to end — real sqlite persistence,
// a real filesystem-backed ports.ArtifactStore (internal/adapters/
// artifactstore, real files on real disk), and the real SQLite `VACUUM
// INTO` backup mechanism (internal/adapters/sqlite's own BackupTo) —
// mirroring internal/app/artifactsweep/sweep_sqlite_test.go's own
// established "real stack, not fakes" discipline for this exact family of
// job.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/maintenance"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

type backupFixture struct {
	store    *sqlite.Store
	uow      ports.UnitOfWork
	artStore *artifactstore.Store
	root     string
}

func newBackupFixture(t *testing.T) *backupFixture {
	t.Helper()
	root := t.TempDir()
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "src.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	artStore, err := artifactstore.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	const projectID = "project-1"
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return &backupFixture{store: store, uow: uow, artStore: artStore, root: root}
}

func (f *backupFixture) seedArtifact(t *testing.T, id string, body []byte, state artifact.AttachState, createdAt time.Time) artifact.Artifact {
	t.Helper()
	ctx := context.Background()
	ref, err := f.artStore.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put %s: %v", id, err)
	}
	a, err := artifact.NewArtifact(
		artifact.ID(id), project.ProjectID("project-1"), ref.Locator, ref.SHA256, ref.Size, "text/plain",
		ref.Sensitivity, ref.Redacted, artifact.RetentionCanonicalContext, state, false, nil, createdAt, 1,
	)
	if err != nil {
		t.Fatalf("construct artifact %s: %v", id, err)
	}
	var inserted artifact.Artifact
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		inserted, err = tx.Artifacts().InsertArtifact(ctx, a)
		return err
	}); err != nil {
		t.Fatalf("InsertArtifact %s: %v", id, err)
	}
	return inserted
}

// seedPurgedArtifact inserts a row that already reflects a completed V5-14
// sweep: the AttachState is Purged (real production rows never go back to
// Orphan/Attached), but — matching artifactsweep's own real Purge behavior,
// which marks the row Purged without ever deleting it — this helper never
// calls artStore.Put at all, so there genuinely is no real content on disk
// at this row's own Locator, exactly like a real purged row.
func (f *backupFixture) seedPurgedArtifact(t *testing.T, id string, fakeLocator string, createdAt time.Time) artifact.Artifact {
	t.Helper()
	ctx := context.Background()
	a, err := artifact.NewArtifact(
		artifact.ID(id), project.ProjectID("project-1"), fakeLocator, fakeLocator, 4, "text/plain",
		0, false, artifact.RetentionRawOutputTemp, artifact.Purged, false, artifact.ComputeExpiresAt(artifact.RetentionRawOutputTemp, createdAt), createdAt, 1,
	)
	if err != nil {
		t.Fatalf("construct purged artifact %s: %v", id, err)
	}
	var inserted artifact.Artifact
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		inserted, err = tx.Artifacts().InsertArtifact(ctx, a)
		return err
	}); err != nil {
		t.Fatalf("InsertArtifact purged %s: %v", id, err)
	}
	return inserted
}

func TestBackup_ProducesOpenableConsistentSnapshotAndAccurateManifest(t *testing.T) {
	f := newBackupFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)

	f.seedArtifact(t, "a-attached", []byte("attached content"), artifact.Attached, now)
	f.seedArtifact(t, "a-orphan", []byte("orphan content"), artifact.Orphan, now)
	f.seedPurgedArtifact(t, "a-purged", "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", now)

	dbDest := filepath.Join(f.root, "backup", "snapshot.db")
	manifestDest := filepath.Join(f.root, "backup", "manifest.json")
	result, err := maintenance.Backup(ctx, maintenance.BackupDeps{
		Database: f.store, UnitOfWork: f.uow, Clock: clock.NewFixed(now),
	}, maintenance.BackupRequest{DBDestPath: dbDest, ManifestDestPath: manifestDest})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if result.ArtifactCount != 3 {
		t.Fatalf("ArtifactCount = %d, want 3", result.ArtifactCount)
	}
	if !result.GeneratedAt.Equal(now) {
		t.Fatalf("GeneratedAt = %v, want %v", result.GeneratedAt, now)
	}

	// The real DB snapshot must genuinely open and contain the same real
	// rows — not merely a file that happens to exist.
	restoredStore, err := sqlite.Open(ctx, dbDest)
	if err != nil {
		t.Fatalf("open the real DB snapshot: %v", err)
	}
	defer restoredStore.Close()
	restoredUOW := sqlite.NewUnitOfWork(restoredStore)
	var restoredArtifacts int
	if err := restoredUOW.WithReadOnly(ctx, func(tx ports.Tx) error {
		all, err := tx.Artifacts().ListAllArtifacts(ctx)
		restoredArtifacts = len(all)
		return err
	}); err != nil {
		t.Fatalf("read artifacts from the restored snapshot: %v", err)
	}
	if restoredArtifacts != 3 {
		t.Fatalf("restored snapshot has %d artifact rows, want 3", restoredArtifacts)
	}

	manifest, err := maintenance.ReadManifest(manifestDest)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if len(manifest.Artifacts) != 3 {
		t.Fatalf("manifest has %d entries, want 3", len(manifest.Artifacts))
	}

	// Positive control: verifying the manifest against the SAME real
	// artifact store the backup was taken from must report everything OK
	// (the purged entry skipped, not flagged missing).
	report := maintenance.VerifyRestoredArtifacts(ctx, f.artStore, manifest)
	if !report.Clean() {
		t.Fatalf("report not clean against the original store: %+v", report)
	}
	if report.TotalArtifacts != 2 {
		t.Fatalf("TotalArtifacts = %d, want 2 (purged entry must be skipped)", report.TotalArtifacts)
	}
	if report.OKCount != 2 {
		t.Fatalf("OKCount = %d, want 2", report.OKCount)
	}
}

func TestBackup_RefusesToOverwriteAnExistingManifest(t *testing.T) {
	f := newBackupFixture(t)
	ctx := context.Background()
	dbDest := filepath.Join(f.root, "backup", "snapshot.db")
	manifestDest := filepath.Join(f.root, "backup", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestDest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(manifestDest, []byte("{}"), 0o644); err != nil {
		t.Fatalf("pre-create manifest: %v", err)
	}
	_, err := maintenance.Backup(ctx, maintenance.BackupDeps{
		Database: f.store, UnitOfWork: f.uow, Clock: clock.System{},
	}, maintenance.BackupRequest{DBDestPath: dbDest, ManifestDestPath: manifestDest})
	if err == nil {
		t.Fatal("Backup succeeded despite an existing manifest at the destination — must never silently overwrite")
	}
}

func TestVerifyRestoredArtifacts_MissingAndCorruptAreDistinguished(t *testing.T) {
	f := newBackupFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	f.seedArtifact(t, "a-ok", []byte("this one stays intact"), artifact.Attached, now)
	corrupted := f.seedArtifact(t, "a-corrupt", []byte("this one will be corrupted on disk"), artifact.Attached, now)

	dbDest := filepath.Join(f.root, "backup", "snapshot.db")
	manifestDest := filepath.Join(f.root, "backup", "manifest.json")
	if _, err := maintenance.Backup(ctx, maintenance.BackupDeps{
		Database: f.store, UnitOfWork: f.uow, Clock: clock.System{},
	}, maintenance.BackupRequest{DBDestPath: dbDest, ManifestDestPath: manifestDest}); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	manifest, err := maintenance.ReadManifest(manifestDest)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}

	// Simulate restoring into a BRAND NEW, empty artifact-store root — every
	// real entry must report MISSING.
	emptyRoot := filepath.Join(f.root, "restored-empty")
	emptyStore, err := artifactstore.New(emptyRoot)
	if err != nil {
		t.Fatalf("artifactstore.New(empty): %v", err)
	}
	missingReport := maintenance.VerifyRestoredArtifacts(ctx, emptyStore, manifest)
	if missingReport.MissingCount != 2 || missingReport.OKCount != 0 || missingReport.CorruptCount != 0 {
		t.Fatalf("missingReport = %+v, want 2 missing, 0 ok, 0 corrupt", missingReport)
	}
	if missingReport.Clean() {
		t.Fatal("a report with real missing artifacts must never report Clean()")
	}

	// Now genuinely corrupt one real object file on disk directly, leaving
	// the other one intact, and verify against the ORIGINAL store's own
	// root — this proves MISSING and CORRUPT are told apart, not both
	// collapsed into one generic "problem" bucket.
	digest := strings.TrimPrefix(corrupted.Locator, "sha256:")
	objectPath := filepath.Join(f.root, "artifacts", "objects", digest[0:2], digest[2:4], digest)
	if err := os.WriteFile(objectPath, []byte("tampered bytes, wrong hash now"), 0o644); err != nil {
		t.Fatalf("corrupt object file: %v", err)
	}
	mixedReport := maintenance.VerifyRestoredArtifacts(ctx, f.artStore, manifest)
	if mixedReport.OKCount != 1 || mixedReport.CorruptCount != 1 || mixedReport.MissingCount != 0 {
		t.Fatalf("mixedReport = %+v, want 1 ok, 1 corrupt, 0 missing", mixedReport)
	}
	var corruptFinding *maintenance.ArtifactRestoreFinding
	for i := range mixedReport.Findings {
		if mixedReport.Findings[i].ArtifactID == "a-corrupt" {
			corruptFinding = &mixedReport.Findings[i]
		}
	}
	if corruptFinding == nil || corruptFinding.Status != maintenance.ArtifactRestoreCorrupt {
		t.Fatalf("no CORRUPT finding for a-corrupt; findings=%+v", mixedReport.Findings)
	}
}
