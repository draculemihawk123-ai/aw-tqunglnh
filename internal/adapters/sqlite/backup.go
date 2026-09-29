package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

var _ ports.DatabaseBackup = (*Store)(nil)

// BackupTo is V8-06's own "SQLite safe backup API"
// (docs/design/10-v8-alpha-hardening.md V8-06). It runs SQLite's own
// documented online-backup mechanism — `VACUUM INTO` — which the engine
// itself guarantees is a fully consistent, point-in-time snapshot safe to
// run concurrently with this same Store's own real readers and writers (it
// takes only the same SHARED read lock an ordinary read transaction would,
// never blocking a concurrent writer under WAL mode, and never observing a
// partially-committed transaction). This is why V8-06 never needed to
// invent its own "pause writes for a backup window" mechanism: the engine's
// own guarantee already covers it.
//
// destPath must not already exist — `VACUUM INTO` refuses to overwrite an
// existing file, the same "never silently clobber" discipline this
// codebase's own artifact/workspace adapters already follow elsewhere; a
// caller that wants to replace a previous backup must remove or rename it
// first, an explicit decision this method never makes on the caller's
// behalf.
func (s *Store) BackupTo(ctx context.Context, destPath string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite: BackupTo called on a nil or closed Store")
	}
	abs, err := filepath.Abs(destPath)
	if err != nil {
		return fmt.Errorf("sqlite: resolve backup destination: %w", err)
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("sqlite: backup destination %s already exists — remove or rename it first", abs)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("sqlite: stat backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return fmt.Errorf("sqlite: create backup destination directory: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, abs); err != nil {
		return fmt.Errorf("sqlite: VACUUM INTO %s: %w", abs, err)
	}
	return nil
}
