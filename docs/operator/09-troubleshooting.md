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
new ID. An ID is never reused: `aw repository register` with an ID (or a name, within the project) that already
exists fails with a typed `CONFLICT` — `persistent record already exists: repository <id>` (HTTP 409, exit code 1) —
and registers nothing; the same is true of `aw definition create` with a `definitionId` that already names a
Definition of any kind (`persistent record already exists: definition <id>`). Replaying the SAME
`--idempotency-key` is the only way to get the original result back.

## Gate/command "could not be spawned" / "not a valid Win32 application" / permission denied

Your Command document's referenced script doesn't match the OS the worker process actually runs on — a `.sh`
script with a shebang line has no interpreter on native Windows; a script missing its executable bit fails on
Linux/macOS. Fix by publishing a NEW skill version with the OS-correct script, then a new command version
pointing at it, then a new gate/workflow version pointing forward through that chain — Definitions are
immutable per version, so there is never an "edit and retry" for a published document; always publish forward.
See [01-quickstart.md](01-quickstart.md)'s own real walkthrough of hitting and fixing exactly this.

## Attempt `FAILED` with `SCOPE_VIOLATION`

```bash
aw run timeline <runId>
# an EXECUTION_ATTEMPT entry: "failureCode": "SCOPE_VIOLATION",
#   "failureDetail": "2 path(s) outside the granted scope: repo-a:leaked.txt; repo-a:docs/x.md"
```

The agent changed something its WorkItem's `pathScopes` (or, for a CHECKER/gate, its read-only mount) does not
allow. This is a verdict about what the attempt WROTE — not a provider outage (`PROVIDER_UNAVAILABLE`), and not
retryable. `failureDetail` names the violating paths (at most 20, then `and N more`; known secrets inside a path
are masked); the same field is in `GET /runs/{id}/timeline`. Either widen the WorkItem's scope with a scope
expansion, or fix the agent/skill so it stays inside the paths it was granted. An attempt that failed this way
before the field existed has the failure code but no `failureDetail`.

## A COMMAND or gate failed — where is the reason?

```bash
aw evidence list --project-id <id> <workItemId> --kind COMMAND_EXECUTION
# "verdict": "FAILED", "artifactReferences": ["<artifactId>"]
aw artifact get <workItemId> <evidenceId> <artifactId> --project-id <id> --output -
# {"exitCode":1,"argv":[...],"cwd":"...","durationMillis":812,"truncated":false,"stderr":"FAIL: TestAdd ..."}
```

Since V9-02 a `COMMAND` that exited on its own leaves a `COMMAND_EXECUTION` evidence row even when the exit code was
not 0 (verdict `FAILED`): the artifact has the exit code, argv, working directory, duration, whether the output was
cut (`truncated`) and the redacted stdout/stderr the command's `output` contract captures — no need to re-run the
test in a fresh worktree to learn what failed. A gate's non-`PASS` verdict has always left one row per criterion.
A timeout, a kill or a spawn failure leaves no such row — those are technical errors; the timeline's
`failureCode` / `failureDetail` is where to look. If the node declares `failureOutcome` (see
[04-authoring-workflows.md](04-authoring-workflows.md)) a failure of the check is a `SUCCEEDED` NodeRun with that
outcome, so look at the NodeRun's `selectedOutcome` rather than at a failed attempt.

## Agent attempt `FAILED` with `OUTCOME_REJECTED` (or after reporting an unknown outcome)

```bash
aw run timeline <runId>
# an EXECUTION_ATTEMPT entry: "terminationReason": "OUTCOME_REJECTED"
```

A node with more than one outcome needs the agent to end its last message with exactly one
`<agentkit-outcome>…</agentkit-outcome>` marker naming one of the node's outcomes. The agent's prompt lists the
allowed outcomes (`taskContract.allowedOutcomes`) and, for a node with a choice, the marker syntax
(`taskContract.outcomeProtocol`) — see [04-authoring-workflows.md](04-authoring-workflows.md). `OUTCOME_REJECTED`
means no marker was reported at all; a marker that was repeated, malformed, or named an outcome outside that list is
a provider protocol error instead (`EXECUTION_FAILED` with failure code `PROVIDER_UNAVAILABLE`). Either way the attempt
is `FAILED` and the run does not follow any edge. An attempt scheduled
before the instruction-schema upgrade still has the old prompt (no list), so a multi-outcome agent from before the
upgrade may need the outcomes in its Skill until it is re-run.

## Local commit `FAILED` with `NO_CHANGES`

```bash
aw release-set local-commit status --project-id <id> <releaseSetId> <localCommitId>
# "state": "FAILED", "failureReason": "NO_CHANGES"
```

The repository workspace's worktree had nothing to commit, so no commit was created and the job did not retry.
Make the change in the worktree, then run `aw release-set local-commit` again with the same fields and a NEW
`--idempotency-key` — a `NO_CHANGES` failure never blocks the retry, and it works on a sealed ReleaseSet as well
(a sealed ReleaseSet cannot be abandoned: `SEALED` and `ABANDONED` are both final).

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
