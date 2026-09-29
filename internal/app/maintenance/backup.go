// Package maintenance is V8-06 — "SQLite backup/restore và corruption
// diagnostics" (docs/design/10-v8-alpha-hardening.md V8-06, ROADMAP-§7):
// "local operator sao lưu/khôi phục consistent DB + artifact manifest" (a
// local operator backs up and restores a consistent database plus artifact
// manifest). Deliberately scoped to ONE local installation — "không hứa
// sync/merge hai installs" (never promises syncing or merging two separate
// installations); this package has no notion of a remote peer at all.
package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
)

// ManifestSchemaVersion is Manifest's own on-disk schema version — bumped
// whenever ManifestEntry's own field set changes, so a future Restore can
// tell an old manifest apart from a corrupt one rather than silently
// misreading it.
const ManifestSchemaVersion = 1

// ManifestEntry is one artifact's own real inventory record — V8-06's own
// "artifact inventory/hash" bar. Deliberately carries only durable metadata
// (never the artifact's own bytes): the backup's real content lives in the
// DB snapshot's own `artifacts` table already; this manifest exists so a
// LATER restore-verification pass (VerifyRestoredArtifacts) can check the
// artifact-store's own real files against exactly this same list without
// re-opening the (possibly since-relocated) original database.
type ManifestEntry struct {
	ID             string `json:"id"`
	ProjectID      string `json:"projectId"`
	Locator        string `json:"locator"`
	ContentHash    string `json:"contentHash"`
	Size           int64  `json:"size"`
	MediaType      string `json:"mediaType"`
	RetentionClass string `json:"retentionClass"`
	AttachState    string `json:"attachState"`
}

// Manifest is the backup's own artifact inventory, written alongside (never
// inside) the real DB snapshot.
type Manifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	GeneratedAt   time.Time       `json:"generatedAt"`
	Artifacts     []ManifestEntry `json:"artifacts"`
}

// BackupDeps is everything Backup reads.
type BackupDeps struct {
	// Database is the real store to snapshot — see ports.DatabaseBackup's
	// own doc comment for why this is safe to call while the store is
	// actively serving real concurrent readers/writers.
	Database ports.DatabaseBackup
	// UnitOfWork reads the real, current artifact inventory for the
	// manifest — a separate read from the DB snapshot itself (BackupTo's
	// own VACUUM INTO already captured the `artifacts` table's own rows as
	// of its own consistent point in time; this read may observe a
	// slightly later point if a write lands in between, which is why the
	// manifest is a best-effort CROSS-CHECK for restore-time verification,
	// never the backup's own source of truth for what the restored DB
	// itself contains).
	UnitOfWork ports.UnitOfWork
	Clock      clock.Clock
}

// BackupRequest names where Backup writes its two real outputs.
type BackupRequest struct {
	// DBDestPath is where the real, consistent DB snapshot lands — must not
	// already exist (ports.DatabaseBackup.BackupTo's own contract).
	DBDestPath string
	// ManifestDestPath is where the real artifact inventory (JSON) lands —
	// must not already exist, the same "never silently clobber" discipline
	// the DB snapshot itself follows.
	ManifestDestPath string
}

// BackupResult reports what Backup actually wrote.
type BackupResult struct {
	DBDestPath       string
	ManifestDestPath string
	ArtifactCount    int
	GeneratedAt      time.Time
}

// Backup takes a real, consistent snapshot of the durable database plus a
// real inventory manifest of every artifact this installation currently
// knows about — V8-06's own two real outputs, "consistent DB + artifact
// manifest." Deliberately does NOT copy the artifact-store's own real
// content files: V8-06's own scope is the DATABASE'S consistency and the
// manifest that lets a LATER restore verify the artifact store separately
// (an operator's own backup of the artifact-store root, by whatever real
// filesystem-level means they already use for that directory, is assumed —
// this package only ever proves whether that separately-backed-up content
// still matches what the manifest says should be there).
func Backup(ctx context.Context, deps BackupDeps, req BackupRequest) (BackupResult, error) {
	if req.DBDestPath == "" {
		return BackupResult{}, fmt.Errorf("maintenance: DBDestPath is required")
	}
	if req.ManifestDestPath == "" {
		return BackupResult{}, fmt.Errorf("maintenance: ManifestDestPath is required")
	}
	if _, err := os.Stat(req.ManifestDestPath); err == nil {
		return BackupResult{}, fmt.Errorf("maintenance: manifest destination %s already exists — remove or rename it first", req.ManifestDestPath)
	} else if !os.IsNotExist(err) {
		return BackupResult{}, fmt.Errorf("maintenance: stat manifest destination: %w", err)
	}

	var artifacts []artifact.Artifact
	if err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		artifacts, err = tx.Artifacts().ListAllArtifacts(ctx)
		return err
	}); err != nil {
		return BackupResult{}, fmt.Errorf("maintenance: read artifact inventory: %w", err)
	}

	// The real DB snapshot is taken AFTER reading the inventory, never
	// before: a manifest describing artifacts from a MOMENT LATER than the
	// snapshot it accompanies would be strictly more current, never less —
	// the safe direction for a restore-time cross-check that only ever
	// flags "manifest says this should exist" against real files, never
	// mutates anything based on the snapshot's own exact row set.
	if err := deps.Database.BackupTo(ctx, req.DBDestPath); err != nil {
		return BackupResult{}, fmt.Errorf("maintenance: backup database: %w", err)
	}

	generatedAt := deps.Clock.Now()
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, GeneratedAt: generatedAt, Artifacts: make([]ManifestEntry, len(artifacts))}
	for i, a := range artifacts {
		manifest.Artifacts[i] = ManifestEntry{
			ID: string(a.ID), ProjectID: string(a.ProjectID), Locator: a.Locator, ContentHash: a.ContentHash,
			Size: a.Size, MediaType: a.MediaType, RetentionClass: string(a.RetentionClass), AttachState: string(a.AttachState),
		}
	}
	document, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupResult{}, fmt.Errorf("maintenance: encode manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(req.ManifestDestPath), 0o700); err != nil {
		return BackupResult{}, fmt.Errorf("maintenance: create manifest destination directory: %w", err)
	}
	if err := os.WriteFile(req.ManifestDestPath, document, 0o600); err != nil {
		return BackupResult{}, fmt.Errorf("maintenance: write manifest: %w", err)
	}

	return BackupResult{
		DBDestPath: req.DBDestPath, ManifestDestPath: req.ManifestDestPath,
		ArtifactCount: len(artifacts), GeneratedAt: generatedAt,
	}, nil
}

// ReadManifest loads a Manifest a prior Backup call wrote.
func ReadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("maintenance: read manifest %s: %w", path, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("maintenance: decode manifest %s: %w", path, err)
	}
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return Manifest{}, fmt.Errorf("maintenance: manifest %s has schema version %d, this build reads %d", path, manifest.SchemaVersion, ManifestSchemaVersion)
	}
	return manifest, nil
}
