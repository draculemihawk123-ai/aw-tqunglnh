# Troubleshooting

## Start here: `aw doctor`

```bash
aw doctor          # human-readable
aw doctor --json   # machine-readable
```

Real output from a healthy installation (verified in [01-quickstart.md](01-quickstart.md)):

```
status: HEALTHY
restartRequired: false
checks:
  - process_liveness [LIVENESS] HEALTHY: process is running and able to respond
  - app_config [READINESS] HEALTHY: startup configuration is valid
  - database [READINESS] HEALTHY: database is reachable
  - artifact_root [READINESS] HEALTHY: <path> exists and is writable
  - git [READINESS] HEALTHY: git version <version>
  - safe_settings [READINESS] HEALTHY: persisted safe settings desired document decodes cleanly
  - isolation_enforcement [CAPABILITY] HEALTHY: OPERATOR_TRUSTED_LOCAL isolation is enforceable; ...
```

`status` is `HEALTHY` only when every `LIVENESS`/`READINESS` check is healthy (a `CAPABILITY` check like
`isolation_enforcement` describes what's AVAILABLE, not a hard requirement for healthy). `restartRequired:
true` means a `aw settings update` changed the desired config since this process started — restart to apply
it (see [02-configuration.md](02-configuration.md)).

## `database schema version N is newer than this binary supports`

```
aw: open database: database schema version 42 is newer than this binary supports (highest migration it knows: 41; ...) — the database was migrated by a newer release and this binary has NOT modified it; ...
```

You started an older `aw` (or `aw-maintenance`) against a database a newer release already migrated. Nothing
was changed. Run the newer binary, or restore the backup taken before the upgrade and use the older binary
on that — see [10-upgrade-and-rollback.md](10-upgrade-and-rollback.md). `aw version --json` shows which
schema a binary expects (`schemaVersion`).

## Repository stuck in `BLOCKED`

```bash
aw repository list <projectId>
# "status": "BLOCKED", "lastProbeErrorCode": "NOT_FOUND"
```

The `remoteLocator` path you registered doesn't resolve from the `aw worker` process's own perspective —
commonly a relative path, or a shell-specific path (an MSYS/Git-Bash `/tmp/...`-style path on Windows is NOT
the same path the native Go process sees). Register a NEW repository ID with the corrected ABSOLUTE,
OS-native path — there is no "edit repository" command, so fixing this always means registering again with a
new ID.

## Gate/command "could not be spawned" / "not a valid Win32 application" / permission denied

Your Command document's referenced script doesn't match the OS the worker process actually runs on — a `.sh`
script with a shebang line has no interpreter on native Windows; a script missing its executable bit fails on
Linux/macOS. Fix by publishing a NEW skill version with the OS-correct script, then a new command version
pointing at it, then a new gate/workflow version pointing forward through that chain — Definitions are
immutable per version, so there is never an "edit and retry" for a published document; always publish forward.
See [01-quickstart.md](01-quickstart.md)'s own real walkthrough of hitting and fixing exactly this.

## Work item won't reach READY

```bash
aw work-item readiness --project-id <id> <workItemId>
# "ready": false, "problems": ["behavior is required", "verification spec is required", ...]
```

The WorkItem has no real readiness contract. Supply one at creation time (`"contract": {"schemaVersion": 1,
"behavior": "...", "verificationSpec": "...", "riskLevel": "LOW"|"MEDIUM"|"HIGH",
"acceptanceCriteria": [{"description": "...", "verificationRef": "..."}]}`) — there is no separate
"set contract" command; a WorkItem's contract is supplied once, at creation, in the SAME request body as
`title`/`effectiveScope`.

## `run start` fails with "work item is not READY"

Call `aw work-item mark-ready --expected-version <n> <workItemId>` first — `readiness: true` from `aw
work-item readiness` only means the WorkItem's OWN contract is complete enough to become ready; it doesn't
itself transition the status. `--expected-version` must match the WorkItem's real current version (from
`work-item show`/the previous mutation's own response) — a stale version is a real `CONFLICT`.

## `RESYNC_REQUIRED` on a paginated list

A cursor from an earlier page was replayed against a DIFFERENT filter/sort than the one it was minted under
(or the underlying data generation changed underneath it). This is a deliberate, typed signal — never a
silently wrong page — start the walk over from an unfiltered/first-page request.

## "high-impact command requires confirmation" / "flag provided but not defined: -yes"

- If you got the FIRST message: add `--yes` (you're running non-interactively — piped stdin or `--json`).
- If you got the SECOND message: you added `--yes` to a command that isn't gated behind confirmation at all —
  remove it. See [03-cli-reference.md](03-cli-reference.md)'s own `--yes` convention section.

## `flag provided but not defined` for a flag you know exists

Flags must come BEFORE the positional argument, never after (`aw definition publish --kind POLICY --yes
my-id`, not `aw definition publish my-id --kind POLICY --yes`) — see
[03-cli-reference.md](03-cli-reference.md)'s own "positional arguments come after flags" section.

## Nothing happens after `repository register` / a mutation that should trigger async work

Confirm `aw worker` is actually running against the SAME `--db`/`--artifact-root`/`--workspace-root` as `aw
serve` — `aw serve` alone never probes repositories, executes workflow nodes, or updates projections; it only
serves the HTTP/CLI surface and accepts commands. See [01-quickstart.md](01-quickstart.md) step 2.

## Where to look next

- A specific command's exact flags/body shape: `aw <resource> <action> -h`, or
  [03-cli-reference.md](03-cli-reference.md).
- Why a run reached a given terminal state: `aw run diagnostics --project-id <id> <runId>` (blockers, orphaned
  attempts, repository workspace states) and `aw evidence list --project-id <id> <workItemId>` (the real
  verdict + artifact for every node that ran).
- Everything this installation's database/artifacts actually contain, independent of any in-flight run: `aw
  work-item show`, `aw run show`, `aw project list`, `aw repository list` — all real, live queries, never a
  cached or stale summary.
