# Providers and isolation

## Registering a provider

`aw serve` and `aw worker` each independently accept `--claude-executable <path>` / `--codex-executable
<path>` — the only two provider kinds this build knows about (`aw version --json`'s own `supportedProviders`
field, V8-08). Omitting one means: any `AGENT` node pinned to that provider fails closed (503 on
`RetryBlockedActivation`'s own admission re-check for `aw serve`; the worker simply can't execute a node
pinned to an unregistered provider). There is no runtime "register a provider" command — it is a process
startup flag on BOTH `serve` and `worker` (they must agree, since either process may need to reason about a
given provider).

```bash
aw serve  ... --claude-executable /path/to/claude
aw worker ... --claude-executable /path/to/claude
```

`aw adapter register`/`aw adapter probe`/`aw adapter list`/`aw adapter show` manage the separate, per-
installation `AdapterBuild` inventory — a real capability manifest for one specific (provider, executable,
version) tuple, checked at run-start time against what an `AGENT_PROFILE`/`WORKFLOW` pins. See `aw adapter -h`
and each subcommand's own `-h` for the exact flags; this is a genuinely different concept from the
`--claude-executable`/`--codex-executable` process flags above (those register the provider AT ALL; adapter
builds are the specific, versioned capability snapshot a workflow pins to).

### The provider CLI updated itself: **Use the new CLI** (V12-05)

When the CLI changes (Claude updates itself), every AGENT node pinned to the old build is blocked `ADAPTER_BUILD_DRIFT`. On the
task page the drift blocker has a **Use the new CLI** action (CLI: `aw work-item use-new-cli <workItemId> --node-run-id <nodeRunId>
[--redo --reason <text>] [--out-of-scope KEEP|PARK|PARK_ALL]`). After your confirmation aw:

1. measures the executable the node is pinned to as it is now and registers that build (`aw adapter list` shows it);
2. publishes a new version of the run's workflow definition in which every AGENT node pinned to the old build pins the new one
   (nothing else in the document changes);
3. with **Then redo this task** (`--redo`), redoes the work item on that version — only when its run has finished. A run that is
   still live keeps its old pin (aw never repins a running run): the result then says to cancel the run and use **Redo with Latest
   Version**.

A CLI that still measures exactly as the pinned build is refused (`409`, "retry the blocked node instead"). After you republish the
project's definitions with the kit (`aw-publish.py`), its workflow is pinned the usual way again. Since V12-05 **Redo with Latest
Version** also shows after a run was cancelled (the buttons come from `aw work-item redo-preview` when the card shows no run).

## Isolation tiers

Two values (`internal/domain/policy/policy.go`):

- **`OPERATOR_TRUSTED_LOCAL`** — the only tier actually usable in Alpha. A node runs as a real local OS
  process with no additional sandboxing beyond this repo's own process-level containment (argv/env
  allowlisting, output limits, network-access declarations on Command documents). This is the tier
  [01-quickstart.md](01-quickstart.md)'s own real permission-policy example uses.
- **`ENFORCED_ISOLATED`** — a real OS-level sandbox (container, VM, or similar). **Not available in Alpha** —
  `aw doctor`'s own `isolation_enforcement` check reports this explicitly: *"ENFORCED_ISOLATED is not
  available in this environment (no real OS-level sandbox yet) and any node pinned to it fails closed rather
  than silently downgrading."* A permission policy that pins `ENFORCED_ISOLATED` publishes successfully (it is
  a valid document) but any run that reaches a node requiring it fails closed at admission — this is a
  deliberate design choice (never silently run an isolation-required node unsandboxed), not a missing feature
  you need to work around.

Check which tier is actually available on your machine with `aw doctor` — the real output this documentation
was verified against:

```
- isolation_enforcement [CAPABILITY] HEALTHY: OPERATOR_TRUSTED_LOCAL isolation is enforceable;
  ENFORCED_ISOLATED is not available in this environment (no real OS-level sandbox yet) and any
  node pinned to it fails closed rather than silently downgrading
```

## Granted capabilities

A `PERMISSION` policy's own `grantedCapabilities` list names specific capability strings a node may need
beyond baseline isolation — e.g. `INTEGRATION_MULTI_REPOSITORY_WRITE` (a node touching more than one
repository's own write scope in a single attempt). Omit it entirely for a node that needs no capability beyond
its own declared repository scope (verified: [01-quickstart.md](01-quickstart.md)'s own permission-policy
document omits it and the run still succeeds, since the MACHINE_GATE example never touches a repository at
all).

## Repository secrets (V12-29, ADR-061)

A value a repository's gates, commands or QA app need (a DB password, a storage key) belongs in the **repository secret
store**, not in a file of the repository. Project overview → a repository's **Secrets**, or:

```bash
printf %s "$DB_PASSWORD" | aw repository secret set --project-id <p> --name CHOCHUYENNHA_DB_PASSWORD <repositoryId>
aw repository secret set --project-id <p> --env-file backend/.env <repositoryId>      # every NAME=value, once
aw repository secret list --project-id <p> <repositoryId>                            # names only
aw repository secret delete --project-id <p> --name R2_KEY <repositoryId>
```

- A value is read from stdin or a file, never from the command line (argv is visible to every user of the machine), and
  no read ever returns it: the UI and the CLI show names only. Importing a `.env` shows its names to confirm and does not
  keep the file. A Spring application reads such a variable directly (`${CHOCHUYENNHA_DB_PASSWORD}`), so there is no
  `.properties` import.
- Values are encrypted (AES-256-GCM, bound to the repository and the name) under a data key kept **outside the database**,
  protected by the operating system: Windows **DPAPI** (only this Windows user on this machine), macOS **Keychain**, Linux
  **Secret Service** when one answers, otherwise a `secret-store.key` file (mode 0600) in `--artifact-root`. `aw serve`,
  `aw worker` and the CLI use the same key when given the same `--artifact-root`.
- A COMMAND's or gate's `secretRefs` are looked up in the store of its cwd repository first, then of the other repositories
  in the node's scope, then — as before — in the worker's environment. A secret of one repository is never given to a
  command that does not run in it. A resolved value is added to that execution's redaction, and never reaches a prompt, a
  context snapshot, evidence or a log.
- `--env-allowlist` still decides which inherited environment variables reach a process; store secrets are given by name,
  in addition.

## Run the application of a worktree for manual QA (V12-30, ADR-062)

A repository has one **Run app profile**: the processes to start (for example a Spring backend and a Next.js frontend), each as an
argv list — never a shell string — with the sub-directory of the worktree it runs in, its port, plain environment variables and the
**names** of repository secrets (above), a preview URL, and an automatic stop (default 120 minutes, at most 1440). The task's
**Workspace** tab shows a **Run app** strip: **Set up… / Profile…**, **Start**, **Stop**, **Logs**, **Open preview**.

```bash
cat > run-app.json <<'JSON'
{"processes": [
   {"name": "backend",  "argv": ["./mvnw", "spring-boot:run"], "dir": "backend",  "port": 8080, "secretNames": ["CHOCHUYENNHA_DB_PASSWORD"]},
   {"name": "frontend", "argv": ["pnpm", "dev"],               "dir": "frontend", "port": 3000, "env": {"NEXT_PUBLIC_API_URL": "http://localhost:8080"}}
 ],
 "previewUrl": "http://localhost:3000", "stopAfterMinutes": 120}
JSON
aw repository run-app set --project-id <p> --file run-app.json <repositoryId>        # --expected-version <n> to replace one
aw repository-workspace run-app start  --project-id <p> <repositoryWorkspaceId>
aw repository-workspace run-app status --project-id <p> <repositoryWorkspaceId>     # state, log tails, why it cannot start
aw repository-workspace run-app stop   --project-id <p> <repositoryWorkspaceId>
```

- **`aw worker` runs it** (`aw serve` only records the request; `aw up` starts both). A worker takes a requested session within two
  seconds, checks every port is free (a port in use ends the session `FAILED: port 8080 (backend) is in use` — stop that program
  first), resolves the secrets from the repository store, then the worker's environment, and starts each process with the worker's
  supervisor (process group on Unix, Job Object on Windows).
- The executable (`argv[0]`) is looked up in the **worker's** `PATH`, and a process inherits only the worker's `--env-allowlist`
  variables, plus the profile's variables and secrets. A dev server usually needs `PATH`, `HOME` (and on Windows `SystemRoot`,
  `APPDATA`, `LOCALAPPDATA`, `USERPROFILE`): allow them with `--env-allowlist` (below). On Windows, give the real program
  (`pnpm.cmd`, `mvnw.cmd`) when the name alone is not found.
- **One application per repository at a time** (its ports are fixed): Start is refused while another worktree of the repository
  runs it, while the worktree is not `READY`, and while an attempt holds the worktree's write lease.
- **It stops, killing every process tree**, on Stop, at the automatic stop, when the worktree is released or quarantined, when an
  attempt takes the write lease, when any of its processes exits (the session ends `FAILED: backend exited (code 1)`), and when the
  worker stops.
- **A worker killed hard** (`kill -9`, a crash): on Windows the Job Object ends the tree with the worker; on Linux each started
  process gets `PR_SET_PDEATHSIG` and dies with the worker. Each session records the worker's and every process's pid and start time
  under `<artifact-root>/run-app/<session>/`; the next tick of any worker that finds the recorded worker gone ends what is left of those
  process groups (only while pid and start time still match, so a reused pid is never killed) and closes the session `FAILED`, naming
  them. A port that is still busy at Start is reported with the program holding it: `port 8080 (backend) is in use: java (pid 4120)
  holds it`; aw never kills a process it did not start.
- **Logs**: one per process, under `<artifact-root>/run-app/<session>/<process>.log` (mode 0600, at most 8 MiB each — later output is
  dropped), with every resolved secret value replaced by `[REDACTED]`. The UI and `status` show the last 16 KiB.
- A running application keeps the profile it started with; a changed profile applies at the next Start.

## Env allowlist

A spawned provider or command process inherits NOTHING from the worker's own process environment by default.
Variables get through only by NAME, and for an agent they have to be named twice (since V9-05):

| Where | What it is | Who writes it |
|---|---|---|
| `aw worker --env-allowlist NAME1,NAME2` | the operator's **ceiling**: names an agent process may ever receive | operator |
| `"envAllowlist": ["NAME1", "NAME2"]` in the AGENT_PROFILE document | the author's **request**: names this profile's agent asks for | profile author |

The agent process inherits the exact, case-sensitive **intersection** of the two. A profile cannot widen the
operator's list, and either list being empty means an empty environment (today's behavior for every profile
that does not use the field). Write each name identically in both lists — on Windows that means `PATH`, not
`Path`, in both.

```bash
# operator: allow what the Claude CLI needs, once
aw worker ... --claude-executable /usr/local/bin/claude --env-allowlist PATH,HOME
```

```json
{"providerKey": "claude", "model": "your-model-name", "envAllowlist": ["HOME", "PATH"],
 "contextPolicyRef": {"kind": "POLICY", "definitionId": "...", "versionId": "..."},
 "compatibility": {"os": ["linux"]}, "budget": {"maxTokens": 4096}}
```

**A wrapper script that hard-codes `HOME`/`PATH` is no longer required.** It used to be the only way to start the
Claude CLI, and the environment it set lived outside every version and hash `aw` keeps. Point
`--claude-executable` at the real CLI instead (on Windows allow `USERPROFILE` and `SystemRoot` as well, if the
CLI needs them).

What is recorded, and what is not:

- The names the agent was given are fixed when the node is scheduled and written, sorted, into the node's
  execution profile as `agentInheritedEnvironment` — visible in the `<nodeRunId>-execution-profile-v1` decision
  artifact and covered by the execution profile hash. A real example (hashes shortened), from a node whose
  profile asked for `AW_V905_E2E_VISIBLE` and `AW_V905_E2E_PROFILE_ONLY` while the worker allowed
  `AW_V905_E2E_VISIBLE` and `AW_V905_E2E_WORKER_ONLY` — only the name both lists contain is pinned:

  ```json
  {"schemaVersion":1,"executor":{"kind":"AGENT","definitionId":"v9-agent-profile-def","versionId":"v9-agent-profile-v1","compiledHash":"sha256:554d…"},
   "policies":[ … ],"providerKey":"claude","model":"fake-model","toolRefs":["read_file"],"maxTokens":4096,"role":"MAKER",
   "agentInheritedEnvironment":["AW_V905_E2E_VISIBLE"],
   "adapterBuild":{ … },"runtimeExecutionConfigHash":"sha256:a089…","timeoutSeconds":120,"isolationTier":"OPERATOR_TRUSTED_LOCAL"}
  ```

  A node whose set is empty has no such field at all, and its hash is what it always was.
- **Values are never recorded by `aw`.** They are read from the worker's environment at the moment the process is
  spawned. `aw` does not store, log, hash or put them in evidence or agent events; the only place they exist is
  the child process. What the agent itself does with them is outside that guarantee: if it prints a variable's
  value (for example by running `env`), that text is recorded like any other agent output. Allow only variables
  you are content for the agent to read.
- The worker that **executes** an attempt applies its own `--env-allowlist` again, so an agent never gets a name
  that worker's operator did not allow even if the node was scheduled by another worker (or by this one before a
  restart with different flags). A retry or recovery attempt of the same node starts from the same pinned list.

The same `--env-allowlist` is what the provider executable's own `--version` probe inherits — `aw worker` runs it
at startup and at every admission, `aw serve` at startup. `aw serve` and the one-shot commands take the flag too
(`aw doctor --env-allowlist PATH,HOME`, or `AW_ENV_ALLOWLIST` in the environment); give them the worker's list.

**`aw doctor` tells you when the provider cannot run in that environment.** For each configured provider
executable it runs the probe with exactly the `--env-allowlist` it was given and reports a
`provider_environment:<provider>` check (the existing `provider:<provider>` check only fingerprints the file).
With too small a list the real output is (here against a stand-in provider that refuses to start without `PATH`,
so the one name it was run with is the variable that tells it so):

```
- provider_environment:claude [CAPABILITY] DEGRADED: PROVIDER_ENV_INSUFFICIENT: the claude executable could not
  run its version probe (it exited with code 17) in the environment an agent would get from this worker, which
  inherits only the variable names: AGENTKIT_HELPER_REQUIRE_ENV
      remediation: Allow the variables the provider CLI needs — typically PATH and HOME, and USERPROFILE and
      SystemRoot on Windows — in `aw worker --env-allowlist` (give `aw doctor` the same list with
      --env-allowlist or AW_ENV_ALLOWLIST), and list the same names, spelled identically, in the AgentProfile's
      envAllowlist: an agent process inherits only the names that BOTH lists contain. ...
```

`PROVIDER_ENV_INSUFFICIENT` is a stable code you can match on; the detail gives the reason (`it exited with code
N`, `the executable could not be started`, `it did not answer within the probe's time limit`, `it exited
normally but printed no version`) and only variable NAMES, never values. The same check appears in `GET /doctor`
when `aw serve` was started with `--env-allowlist`. Doctor says nothing for a provider whose executable path does
not exist — `provider:<provider>` already reports that — and nothing for an installation that configures no
provider executable.

`COMMAND` and `MACHINE_GATE` nodes are not affected by any of this: they inherit the names their own Command
definition lists in its `envAllowlist`. That includes `PATH`: a check script that calls `grep`, `findstr`, `npm`…
finds nothing unless its Command definition declares `PATH` (V9-11, found with a real CLI).

## What a real Claude CLI needs that the fake one does not (V9-11)

The repository's `fake-claude` writes files whenever it is told to and never looks at its prompt. A real Claude
CLI does neither, and two things follow (found by the live run in [`../release/live-provider/README.md`](../release/live-provider/README.md)).

**Permission mode — `aw worker --claude-permission-mode`.** A headless Claude refuses every `Write`/`Edit` in a
worktree it has not been told to trust, and it ignores the project's `.claude/settings.json` allow rules there
(`Ignoring permissions.allow entries … this workspace has not been trusted`). `aw` creates a new worktree for every
WorkItem, so the trust dialog can never be answered; without a mode a MAKER can read but not change anything. Start
the worker with the mode your policy accepts, typically `--claude-permission-mode acceptEdits`; the value is passed
as `--permission-mode` on every task, an unknown value is refused when the worker starts, and the flag is the
operator's decision, not the workflow's: it is not part of any definition and not in the ContextSnapshot. `plan`
and `manual` make the CLI unable to act on its own. The flag does not touch Codex.

**Where the repository is.** A CHECKER's mounts are read-only, so its attempt starts in an empty scratch directory
(GC-INV-25), not in the repository. The Claude adapter therefore grants every mount that is not the working
directory with `--add-dir` and names all mounts, with their absolute paths and access (`WRITABLE` / `READ_ONLY`),
in `--append-system-prompt`; it tells the model that a `READ_ONLY` repository must not be changed. This is not in the
instruction artifact on purpose: the artifact and its hash are a function of the snapshot, and a path belongs to the
machine, not to the snapshot. "Read-only" remains the executor's own check that the mounts are unchanged
afterwards (ADR-030); the sentence to the model is a statement of intent, not the enforcement.

**Effort and spend — `--claude-effort`, `--claude-max-budget-usd`.** Start the worker with the effort level the CLI
should use (`low`, `medium`, `high`, `xhigh` or `max`) and, as a safety net, the most ONE attempt may spend in US
dollars; both are passed to the CLI as `--effort` / `--max-budget-usd` on every task, a resumed attempt included. The
ceiling is enforced by the CLI itself: an attempt that reaches it ends, it does not wait for `aw`. Unknown efforts
and negative or non-finite ceilings are refused when the worker starts. Like the permission mode, neither is part of
any definition or snapshot.

**Isolating the CLI's own configuration — `--claude-safe-mode` (V11-05, ADR-038).** By default the Claude CLI reads more than
`aw` gives it: your `~/.claude` (user skills, agents, slash commands, hooks, MCP servers) and, in the worktree, the repository's
`.claude/` and `CLAUDE.md`. None of that is in the ContextSnapshot, so two identical runs can know different things, a
repository hook that writes into the worktree fails a `MAKER` attempt with `SCOPE_VIOLATION`, and a `CHECKER` pays for the skill
list too (spike V10-17: about 2.4 times the cost of a small node with a large `.claude/`). Start the worker with
`--claude-safe-mode` and every attempt, a resumed one included, runs `claude --safe-mode`: the CLI loads none of those
customizations, while authentication, model selection and the built-in tools work as usual. Checked against Claude Code 2.1.288
with a fixture that has a user skill, a repository skill and a `SessionStart` hook: without the flag both skills are listed and
the hook runs; with it neither skill is listed and no hook event is emitted. It is off by default, so nothing changes for an
installation that relies on its own `~/.claude`, and it needs a CLI that knows `--safe-mode` (an older one refuses the flag and the
attempt fails). Consequences to plan for: the repository's `CLAUDE.md` is **not** loaded either, so conventions that live only
there must move into a Layer or Skill of the kit (they then reach the prompt through the ContextSnapshot); and the instruction-file
oversize warning ([04-authoring-workflows.md](04-authoring-workflows.md)) describes a file the CLI would load, which it no longer
does under this flag. Like the permission mode, effort and budget, the flag is a property of the worker, not of any definition
or snapshot. Codex has no equivalent flag today; the option does nothing for it.

What an attempt actually used is shown, not just capped: the CLI's own token counts and, when it reports one, its cost
are stored as the attempt's `USAGE_REPORTED` event, and `GET /runs/{id}/timeline` / `aw run timeline` /
Graph & Timeline in the UI show them per attempt (`usage`) and summed for the run. They are the provider's figures,
not an `aw` ledger: an attempt that never reached the provider has none, and a CLI that reports no cost reports zero
(the UI says "no cost reported").

**Long attempts (V9-14a).** An attempt that runs for many minutes — a deep analysis on a large model at high effort —
meets three limits that a short one never does:

- **The lease.** A running job renews its lease every `--lease-ttl / 3` (see `--lease-heartbeat`). A renewal that fails
  for a passing reason (a busy database) is retried on the next tick for as long as the lease is still valid; only a
  lease that is really gone (`ErrJobLeaseLost`) or has expired ends the heartbeat. Before this a single failed renewal
  ended it for good, the lease lapsed one TTL later, and the recovery reaper reclaimed — and so re-ran, and re-paid for —
  a job that was still running. If your attempts are long and your disk is slow, a larger `--lease-ttl` (default 30 s)
  gives the retries more room.
- **The attempt timeout** comes from the node's ATTEMPT policy (`timeoutSeconds`, required, no upper limit); the
  AgentProfile has none. Set it to the longest run you accept (for example 3600).
- **The output cap.** Each provider CLI attempt may write 256 MiB of stdout+stderr (the Claude and Codex adapters'
  `Config.OutputLimitBytes`; the supervisor's own default of 10 MiB used to apply and cut a long verbose stream short,
  taking the final result event with it). An attempt that still exceeds the cap now fails as `output_truncated`
  (`ErrOutputTruncated`) rather than as a protocol error. The cap is not yet a flag of `aw worker`.

What an attempt costs when it is retried is `maxAttempts` times its own cost: `--claude-max-budget-usd` caps ONE attempt,
and `aw` never resumes a provider session, so a retry starts from the beginning.
