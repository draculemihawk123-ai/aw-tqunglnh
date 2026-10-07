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

## `PROVIDER_ENV_INSUFFICIENT` in `aw doctor` / an agent that cannot start its CLI

When a provider executable is configured (`--claude-executable` / `--codex-executable`), `aw doctor` runs the
CLI's `--version` probe with exactly the variables in `--env-allowlist` and, if that fails, reports
`provider_environment:<provider> [CAPABILITY] DEGRADED: PROVIDER_ENV_INSUFFICIENT: ...` with the reason (for
example `it exited with code 1`) and the variable names it was run with. An agent process gets no environment
except the names that BOTH the AgentProfile's `envAllowlist` and `aw worker --env-allowlist` contain, so the
usual fix is to allow what the CLI needs — typically `PATH` and `HOME` (on Windows also `USERPROFILE` and
`SystemRoot`) — in the worker's `--env-allowlist`, list the same names, spelled identically (matching is
case-sensitive), in the profile's `envAllowlist` (publish a new profile version and a workflow version pinning
it), and give `aw doctor` the same list (`--env-allowlist` or `AW_ENV_ALLOWLIST`) so it checks what the worker
does. No wrapper script is needed. Details: [05-providers-and-isolation.md](05-providers-and-isolation.md#env-allowlist).
If the doctor check is healthy yet an agent still cannot use a variable, compare the profile's list with the
worker's: a name listed in only one of them is not passed (the node's `agentInheritedEnvironment` in its
execution-profile decision artifact shows what was).

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

## An agent attempt `FAILED` with `EXECUTION_FAILED` and the provider said why (V9-20)

When the provider CLI itself reports that the run failed — a session or usage limit ("You've hit your limit ·
resets 5pm"), an authentication problem, a refused request — the attempt ends `FAILED` / `EXECUTION_FAILED`, and
`aw run timeline <runId>` (and `GET /runs/{id}/timeline`) now shows the provider's own words as the attempt's
`failureDetail`:

```
# "failureCode": "EXECUTION_FAILED",
# "failureDetail": "Claude reported a failed result: You've hit your limit · resets 5pm"
```

The text is the CLI's reason, whitespace collapsed, cut at 500 characters and redacted like every stored event. A
session limit is not something to fix in the task: wait for the reset, then run again (`retry-task` in the guides, or
`aw run start` with the same workflow version once the `RUN_FAILED` blocker is resolved). An attempt that failed
before V9-20 has the code but no detail, and so does a failure the provider did not report itself (a crash, a
timeout).

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

## Agent attempt `FAILED` with `OUTCOME_REJECTED`

```bash
aw run timeline <runId>
# an EXECUTION_ATTEMPT entry: "terminationReason": "OUTCOME_REJECTED"
```

A node with more than one outcome needs the agent to end its last message with exactly one
`<agentkit-outcome>…</agentkit-outcome>` marker naming one of the node's outcomes. The agent's prompt lists the
allowed outcomes (`taskContract.allowedOutcomes`) and, for a node with a choice, the marker syntax
(`taskContract.outcomeProtocol`) — see [04-authoring-workflows.md](04-authoring-workflows.md). `OUTCOME_REJECTED`
(failure code `VALIDATION_FAILED`) means the agent's answer was wrong, in any of four ways: no marker was reported on
a node with a choice, the marker was repeated (a second one in an earlier message), the marker was malformed, or it
named an outcome outside that list. The attempt is `FAILED` and the run does not follow any edge. It is **not** a
provider outage: before this was changed the last three were reported as `EXECUTION_FAILED` with failure code
`PROVIDER_UNAVAILABLE`, which an attempt policy listing `PROVIDER_UNAVAILABLE` as retryable would retry. Now it
retries only if the policy's `retryableErrorCodes` lists `VALIDATION_FAILED`. A stream that never delivers its
terminal event, or is not valid JSONL, is still a provider failure (`PROVIDER_UNAVAILABLE`), not `OUTCOME_REJECTED`.
An attempt scheduled before the instruction-schema upgrade still has the old prompt (no list), so a multi-outcome
agent from before the upgrade may need the outcomes in its Skill until it is re-run.

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

## Work item won't reach READY: "baseline pending" / "baseline FAILED" (V9-08)

`aw work-item readiness` (and `mark-ready`) list, next to the contract problems, one line per repository the work
item may write to whose baseline does not admit a writer:

- `repository R: baseline pending — no baseline has run yet for readiness profile version N` — the baseline has not
  run since the profile was set or changed. It runs by a worker: start `aw worker`, or ask again with
  `aw repository readiness verify <R>`, then `aw repository readiness show <R>`.
- `repository R: baseline FAILED (PRE_EXISTING_FAILURE, attempt A) before any change` — the repository's own
  verification command was already red before any task touched it (the output is in `show`). Fix the repository
  and run `verify`, or — if you accept going on with it — `aw repository readiness accept-exception --attempt-id A
  --reason "..." R`. A task that then fails the same check is told that the failure matches the baseline.
- `... (ENVIRONMENT_ERROR ...)` — the command could not run at all (a missing executable, a timeout): fix the
  environment the worker runs in, then `verify`.

`aw run start` refuses a work item in the same situation (`CONFLICT`, naming the repository), which happens when the
profile changed after the work item became `READY`.

## A run ended `FAILED`: the WorkItem is `BLOCKED` by a `RUN_FAILED` blocker (V9-06, ADR-033)

```bash
aw run diagnostics --project-id <id> <runId>
# "runState": "FAILED", "blockers": [{"blockerId": "<runId>-run-failed-blocker", "type": "RUN_FAILED", "state": "OPEN", ...}]
aw work-item detail --project-id <id> <workItemId>
# card.status "BLOCKED", card.topBlockerType "RUN_FAILED", card.runCount 1, runs: [{"runNumber": 1, "state": "FAILED", ...}]
```

A run that ends `FAILED` no longer leaves its WorkItem `ACTIVE` (where `run start` refused to run again and the only
way out was to cancel the WorkItem and create a new one). It opens one `RUN_FAILED` blocker in the same transaction,
which moves the WorkItem to `BLOCKED` — the board's `BLOCKED` column — and you try again on the **same** WorkItem,
so its messages, evidence and history stay in one place:

1. **Find out why it failed**: `aw run timeline <runId>` (which node, which attempt, the failure code),
   `aw evidence list --project-id <id> <workItemId>` and `aw artifact get ...` (a `COMMAND` that exited on its own
   leaves a `COMMAND_EXECUTION` record with the exit code and stderr, see below).
2. **Decide what to do with the worktree.** Nothing resets it: the next run starts from whatever the failed run left
   in the repository worktree (`aw repository-workspace diff`, `aw repository-workspace log`). Undo what the next run
   must not see, before the next step.
3. **Resolve the blocker**: `aw blocker resolve --mode RESOLVED --reason "<why it is fine to run again>" <blockerId>`.
   The WorkItem becomes `READY`. The preconditions are the ones every blocker has: no run of the WorkItem still in
   progress and no `QUARANTINED` repository workspace in its family (`aw repository-workspace show`) — the newest
   generation of each repository is the one that counts, so after `aw repository-workspace reconcile` recreated it
   ([06-source-control-and-releases.md](06-source-control-and-releases.md#a-quarantined-worktree-and-how-it-is-recovered-v9-18))
   the old quarantined row no longer blocks.
4. **Start the next run**: `aw run start --workflow-version-id <the same workflow version> --idempotency-key <new key> <workItemId>`.

What does not change:

- **`RUN_FAILED` can be resolved but never waived.** `--mode WAIVED` is refused with `this blocker type can never be
  waived` (HTTP 409). To give up on the work, cancel the WorkItem instead: `aw work-item cancel`.
- **The workflow version stays the one pinned in the WorkItem's contract.** `aw run start` with a different
  `--workflow-version-id` is refused (`requested workflow version does not match the work item's pinned version`) even
  though the WorkItem is `READY`; there is no option to repin. To run a different workflow version, or under a
  different contract, create a new WorkItem.
- **Completion only counts the current run's evidence.** The `FAILED` evidence of the earlier run neither blocks nor
  helps the new one: a required evidence kind must be produced again by the new run. The earlier run's messages stay
  on the WorkItem and keep flowing into the new run's context.
- While the blocker is open, `aw run start` returns the usual `work item is not READY`.
- A run that fails while its WorkItem is already being cancelled opens no blocker: the cancellation closes the
  WorkItem out to `CANCELLED`.
- A run that fails the completion policy (`COMPLETION_POLICY_FAILED`) or is cancelled (`RUN_CANCELLED`) has its own
  blocker, resolved with the same command (those two can also be waived).

`aw work-item detail` and the web task page list every run of the WorkItem, oldest first (`runs`), and the board card
and the task header show the run count (`runCount`). The count on the board is projected like the rest of the card:
after an upgrade from a build older than V9-06, run `aw projection rebuild --project-id <id> --projection-name workitem` once so the cards of WorkItems that
already ran show their count (see [10-upgrade-and-rollback.md](10-upgrade-and-rollback.md)); `runs` is always exact.

## The worker died (or was killed) while an agent was writing (V9-19)

When a worker stops without cleaning up — the process was killed, the machine rebooted — the attempt it was running
cannot report anything. Once the worker's job lease runs out (`--lease-ttl`, 30 s by default) the recovery sweep ends
that attempt as `INDETERMINATE` / `OWNERSHIP_LOST_MUTATING` (it held the repository's write lease, so aw cannot assume
nothing was written) and the run goes on with a new attempt, or ends `FAILED` if the node has no attempts left.

The write lease the dead attempt held is **taken over as soon as the sweep has ended that attempt**: the next attempt
(automatic or `retry-task`) gets the worktree right away. Before V9-19 the lease was kept for the node's whole
`timeoutSeconds` plus 2 minutes, so every retry failed with `CONFLICT` ("repository workspace already has an active
writer") for that long — 32 minutes for a 30-minute agent timeout. A lease whose attempt is still `RUNNING` is not taken
over, whatever its age.

What to check after a crash:

- **The worktree.** Nothing resets it. If the agent had already changed files, the next attempt starts from them;
  `aw repository-workspace diff` shows what is there, and `git checkout -- .` / `git clean -fd` in the worktree
  discards it. If the agent had committed on its own, HEAD moved and the worktree is `QUARANTINED` instead — see
  [06-source-control-and-releases.md](06-source-control-and-releases.md#a-quarantined-worktree-and-how-it-is-recovered-v9-18).
- **A stray agent process (Linux and macOS).** On Windows an agent process dies with the worker that started it (the
  worker's job object). On Linux and macOS a worker killed with `SIGKILL` can leave its agent process running, and aw
  does not look for it: the takeover assumes the attempt is gone. Look for the orphan (`ps` for the provider CLI,
  working directory = the worktree) and stop it before the retry if one is there.

## `run start` fails with "work item is not READY"

Call `aw work-item mark-ready --expected-version <n> <workItemId>` first — `readiness: true` from `aw
work-item readiness` only means the WorkItem's OWN contract is complete enough to become ready; it doesn't
itself transition the status. `--expected-version` must match the WorkItem's real current version (from
`work-item show`/the previous mutation's own response) — a stale version is a real `CONFLICT`.

If the WorkItem is `BLOCKED` instead of `BACKLOG`, an earlier run left a blocker open — most often a `RUN_FAILED`
one after a failed run: resolve it first (the previous section), then `aw run start` works again.

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
