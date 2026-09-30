// Command aw-maintenance is V8-06's own "explicit maintenance command"
// (docs/design/10-v8-alpha-hardening.md V8-06, ROADMAP-§7): a local
// operator's own backup/restore tool for ONE installation's real sqlite
// database plus its real artifact inventory manifest. It never promises
// syncing or merging two separate installations (V8-06's own explicit
// "không hứa sync/merge hai installs").
//
// This is its own standalone binary, the same way cmd/v6-gate,
// cmd/v8-security-gate and cmd/docs-coverage-check already are, rather than
// a new `aw <resource> <action>` command or a new cmd/aw process-level
// subcommand: docs/design/08-v6-api-projections.md's own "Không làm" line
// is explicit that the CLI_LOCAL set cmd/aw's own dispatch owns
// (serve/worker/help/version/evidence verify) is CLOSED — "no new
// leaf/route" — and a backup/restore tool has no natural HTTP-parity
// counterpart to route through the resource-command registry either (it
// operates on files the process isn't even running against).
//
//	aw-maintenance backup --db <path> --out <backup-dir>
//	aw-maintenance restore --backup <backup-dir> --into <temp-root> --artifact-root <path>
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/maintenance"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "backup":
		err = runBackup(os.Args[2:])
	case "restore":
		err = runRestore(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "aw-maintenance: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "aw-maintenance:", err)
		os.Exit(1)
	}
}

const usage = `Usage: aw-maintenance <command> [flags]

Commands:
  backup   take a consistent database snapshot + artifact inventory manifest
  restore  materialize a backup into a fresh temp root and verify it

Run 'aw-maintenance <command> -h' for command-specific flags.
`

// runBackup composes the real adapters directly (this binary is its own
// composition root, the same license cmd/v6-gate/cmd/docs-coverage-check
// already exercise) and calls internal/app/maintenance.Backup — safe to run
// against a database another real `aw serve`/`aw worker` process is
// actively using at the same time (ports.DatabaseBackup's own doc comment).
func runBackup(args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	dbPath := flags.String("db", "", "path to the live installation's sqlite database")
	outDir := flags.String("out", "", "destination directory for the backup (created if absent; must not already contain a snapshot.db or manifest.json)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *outDir == "" {
		return errors.New("--db and --out are required")
	}

	ctx := context.Background()
	store, err := sqlite.Open(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	result, err := maintenance.Backup(ctx, maintenance.BackupDeps{
		Database: store, UnitOfWork: uow, Clock: clock.System{},
	}, maintenance.BackupRequest{
		DBDestPath:       filepath.Join(*outDir, "snapshot.db"),
		ManifestDestPath: filepath.Join(*outDir, "manifest.json"),
	})
	if err != nil {
		return err
	}
	document, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	fmt.Printf("%s\n", document)
	fmt.Printf("aw-maintenance: backup complete — %d artifact(s) inventoried\n", result.ArtifactCount)
	return nil
}

// runRestore materializes a backup's own DB snapshot into a FRESH temp
// root (V8-06's own "restore temp root" Verify bar: never overwrite a live
// installation directly), confirms the restored database genuinely opens
// through the real production Open path (migrations included), and cross-
// checks the backup's own artifact manifest against a real, operator-
// supplied artifact-store root — reporting exactly which entries are
// missing or corrupt rather than silently proceeding.
func runRestore(args []string) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	backupDir := flags.String("backup", "", "the backup directory a prior 'aw-maintenance backup' produced")
	into := flags.String("into", "", "a fresh, empty temp root to materialize the restored database into (created if absent)")
	artifactRoot := flags.String("artifact-root", "", "the artifact-store root to verify the manifest against (the operator's own separately-restored artifact content)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *backupDir == "" || *into == "" || *artifactRoot == "" {
		return errors.New("--backup, --into, and --artifact-root are required")
	}

	if err := os.MkdirAll(*into, 0o700); err != nil {
		return fmt.Errorf("create restore temp root: %w", err)
	}
	restoredDBPath := filepath.Join(*into, "restored.db")
	if _, err := os.Stat(restoredDBPath); err == nil {
		return fmt.Errorf("%s already exists — --into must be a fresh, empty temp root", restoredDBPath)
	}
	srcDB, err := os.ReadFile(filepath.Join(*backupDir, "snapshot.db"))
	if err != nil {
		return fmt.Errorf("read backup snapshot: %w", err)
	}
	if err := os.WriteFile(restoredDBPath, srcDB, 0o600); err != nil {
		return fmt.Errorf("materialize restored database: %w", err)
	}

	ctx := context.Background()
	restoredStore, err := sqlite.Open(ctx, restoredDBPath)
	if err != nil {
		var newer *sqlite.SchemaNewerThanBinaryError
		if errors.As(err, &newer) {
			// V8-10: not a damaged backup — a backup a NEWER release took,
			// being restored by an older aw-maintenance.
			return fmt.Errorf("this backup was taken by a newer release than this aw-maintenance supports — restore it with the aw-maintenance of the release that took it (the backup itself is intact): %w", err)
		}
		return fmt.Errorf("open restored database (this is the real production Open path, including migrations — a genuine failure here means the backup is not usable): %w", err)
	}
	defer restoredStore.Close()

	manifest, err := maintenance.ReadManifest(filepath.Join(*backupDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("read backup manifest: %w", err)
	}
	restoredArtifactStore, err := artifactstore.New(*artifactRoot)
	if err != nil {
		return fmt.Errorf("open artifact store at %s: %w", *artifactRoot, err)
	}
	report := maintenance.VerifyRestoredArtifacts(ctx, restoredArtifactStore, manifest)

	document, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	fmt.Printf("%s\n", document)
	fmt.Printf("aw-maintenance: restored database opened successfully at %s\n", restoredDBPath)
	if !report.Clean() {
		return fmt.Errorf("restore completed with %d missing and %d corrupt artifact(s) — see the report above for which ones", report.MissingCount, report.CorruptCount)
	}
	fmt.Println("aw-maintenance: restore verified clean — every manifest entry is present and intact")
	return nil
}
