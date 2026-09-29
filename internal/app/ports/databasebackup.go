package ports

import "context"

// DatabaseBackup is V8-06's own "SQLite safe backup API"
// (docs/design/10-v8-alpha-hardening.md V8-06) — the one operation the
// application layer needs from the durable store to take a real, consistent
// backup, kept as its own minimal port (rather than growing UnitOfWork
// itself) since it is the one durable-store operation that is NOT expressed
// as a transaction: it operates on the store as a whole, at a single
// consistent point in time, never inside a caller-managed Tx.
type DatabaseBackup interface {
	// BackupTo writes a full, consistent, point-in-time snapshot of the
	// store to destPath, which must not already exist. Safe to call while
	// the store is actively serving real concurrent readers and writers —
	// the underlying engine's own online-backup guarantee, not something
	// this port's own caller needs to coordinate (no "pause writes" window
	// to open/close around this call).
	BackupTo(ctx context.Context, destPath string) error
}
