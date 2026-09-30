# Backup and restore

## Why this is a separate binary, not `aw backup`

`aw`'s own process-level command set (`serve`/`worker`/`help`/`version`) is a documented closed list
(`docs/design/08-v6-api-projections.md:799`: *"no new leaf/route"*) — a backup/restore tool operates on the
checkout's own files, not a running installation's HTTP surface, so it has no natural
`aw <resource> <action>` shape either. `cmd/aw-maintenance` is its own standalone binary (V8-06), built the
same way as `cmd/v6-gate`/`cmd/v8-security-gate`/`cmd/aw-release-build`.

## What it does NOT promise

**One local installation only — never syncing or merging two separate installations.** If you need to move an
installation to a different machine, `backup` + `restore` there is the supported path; there is no concept of
two installations staying in sync.

## Backup

```
$ aw-maintenance backup -h
Usage of backup:
  -db string
        path to the live installation's sqlite database
  -out string
        destination directory for the backup (created if absent; must not already contain a snapshot.db or manifest.json)
```

```bash
aw-maintenance backup --db ./aw-install/aw.db --out ./my-backup
```

Safe to run while a real `aw serve`/`aw worker` process is actively using the same database — it uses
SQLite's own `VACUUM INTO`, which only ever takes a shared read lock (never blocks a concurrent writer under
WAL mode). Produces two files: `snapshot.db` (a real, consistent point-in-time copy of the whole database) and
`manifest.json` (every artifact this installation knows about — ID, locator, content hash, size, retention
class, attach state — but never the artifact BYTES themselves; back up your own `--artifact-root` directory
separately, by whatever filesystem means you already use for it).

## Restore

```
$ aw-maintenance restore -h
Usage of restore:
  -artifact-root string
        the artifact-store root to verify the manifest against (the operator's own separately-restored artifact content)
  -backup string
        the backup directory a prior 'aw-maintenance backup' produced
  -into string
        a fresh, empty temp root to materialize the restored database into (created if absent)
```

```bash
aw-maintenance restore --backup ./my-backup --into ./restored --artifact-root ./restored-artifacts
```

Three things happen, all real: the backup's `snapshot.db` is copied into `--into` (refuses if `--into` already
has a `restored.db` — never silently overwrites a previous restore attempt); the restored database is opened
through the REAL production `sqlite.Open` path (migrations included — a genuine open failure here means the
backup itself is not usable, not restore-tool-specific bug); and every entry in `manifest.json` is checked
against `--artifact-root` (your own separately-restored artifact content), reporting each one `OK` / `MISSING`
/ `CORRUPT` — a `Purged` manifest entry is correctly never flagged missing (its real content was legitimately,
durably deleted by a real retention sweep before the backup was even taken — see
[07-evidence-and-retention.md](07-evidence-and-retention.md)).

A clean restore prints `restore verified clean — every manifest entry is present and intact` and exits `0`; any
missing/corrupt entry prints exactly which ones and exits non-zero — point `--artifact-root` at your own
correctly-restored artifact directory and rerun if you see this.
