# Upgrade and rollback

Every statement here is backed by a real test (V8-10): the upgrade matrix and backup-restore rehearsal in
`internal/adapters/sqlite/upgrade_rollback_test.go`, and the real-binary refusal test in
`internal/integration/v6accept/upgrade_rollback_test.go`.

## What "upgrade" means for this installation

Your installation's durable state is the SQLite database plus your `--artifact-root` directory. A new `aw`
build carries a numbered list of schema migrations; **opening the database with a new build applies whatever
migrations that database has not seen yet, automatically, exactly once each.** There is no separate "migrate"
command.

- Migrations are immutable once shipped. Each one's checksum is recorded in the database (`schema_migrations`),
  and opening a database whose recorded checksum no longer matches the binary's copy fails — a shipped
  migration is never edited, only followed by a new one.
- Each migration commits in its own transaction. Migrations that have to rebuild a table other tables point
  at additionally run a foreign-key check before they are recorded, and roll back if a single reference would
  break.
- Your artifact files are not touched by a schema upgrade.

Find out which schema a binary expects, without opening any database:

```bash
aw version --json    # "schemaVersion" is the highest migration this binary carries
```

Configuration: the safe settings you changed with `aw settings update` are stored in the database and move
with it. The optional JSON config file is yours and is not versioned; keys a binary does not know are ignored
without a warning, so after a rollback check the effective values with `aw settings show`.

## Before you upgrade: take a backup with the CURRENT release

The upgrade is one-way (see below), so the backup is your only way back. Take it **before** the new binary
ever touches the database:

```bash
aw-maintenance backup --db ./aw-install/aw.db --out ./pre-upgrade-backup    # run the OLD release's tool
```

Run the **old** release's `aw-maintenance`, not the new one. `aw-maintenance` opens the database through the
same startup path as `aw` itself, so a new build's `aw-maintenance backup` would migrate the live database
first and then back up the already-upgraded copy — not a pre-upgrade backup. If you no longer have the old
tool, stop `aw serve`/`aw worker` and copy `aw.db` together with any `aw.db-wal` and `aw.db-shm` files
beside it instead.

Back up your `--artifact-root` directory too (see [08-backup-and-restore.md](08-backup-and-restore.md)).

## Upgrading

1. Stop `aw serve` and `aw worker`.
2. Take the backup above.
3. Replace the binaries.
4. Start `aw serve` / `aw worker` (or run `aw doctor`). The first open applies the pending migrations.
5. Run `aw doctor` and confirm `status: HEALTHY`; your projects, runs and evidence are read back from the
   same database as before.

Restarting after an upgrade is a no-op: a second open applies nothing and changes nothing.

**Upgrading to the build that adds the `RUN_FAILED` blocker (V9-06)** applies one migration (it rebuilds the
`blockers` table to allow the new type; existing rows are copied unchanged). Two things to know:

- A WorkItem whose run **already** ended `FAILED` before the upgrade is still `ACTIVE` — no blocker is opened
  retroactively. Cancel it as before (`aw work-item cancel`) and create a new WorkItem, or leave it; only runs that
  fail after the upgrade open a `RUN_FAILED` blocker and can be rerun on the same WorkItem (see
  [09-troubleshooting.md](09-troubleshooting.md)).
- The board cards of WorkItems that ran before the upgrade show no run count (`runCount` is counted from the
  journal as it is projected). Run `aw projection rebuild --project-id <id> --projection-name workitem` once per project to build a fresh projection generation from the whole
  journal; `aw work-item detail` lists the exact runs (`runs`) regardless.

## Rollback

**A binary older than your database refuses to open it.** If a newer release already migrated the database,
the older binary stops at startup and leaves the database exactly as it found it:

```
aw: open database: database schema version 42 is newer than this binary supports (highest migration it knows: 41; unknown applied migration(s): [42]) — the database was migrated by a newer release and this binary has NOT modified it; run that release's (or a newer) binary, or restore a backup taken before the upgrade (docs/operator/10-upgrade-and-rollback.md)
```

Migrations are not reversible and no release marks itself compatible with an older binary, so the rule is
strict: a binary only runs against a database whose applied migrations it fully knows.

| Situation | What works |
|---|---|
| Rolled back the binary, but no new migration was applied yet (`schemaVersion` of both builds is equal) | Start the old binary — it opens the database normally. |
| Rolled back after the new build migrated the database | The old binary refuses. Restore the pre-upgrade backup (below), then start the old binary. |
| Want the new release again after a rollback | Open the restored database with the new binary; the upgrade is simply retried from the restored copy. |

### Restoring the pre-upgrade backup

```bash
aw-maintenance restore --backup ./pre-upgrade-backup --into ./restored --artifact-root ./restored-artifacts
```

Then point the old binary's `--db` at `./restored/restored.db`. **Anything the new release wrote after the
backup was taken is not in the backup** — a rollback is a return to the moment of the backup, not a
downgrade of the newer database.

### One limit you cannot fix from here

The refusal lives in the binary that performs it. A binary built **before** V8-10 has no such check and will
open a newer database without complaint and run older SQL against a schema it does not understand. The
refusal protects every binary built from V8-10 onward; for anything older, the backup in the previous section
is the only protection. This is why taking the backup first is not optional.
