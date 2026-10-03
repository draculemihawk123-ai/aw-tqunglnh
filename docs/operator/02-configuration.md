# Configuration reference

## Config precedence (from `docs/design/01-system-design.md` §12, the authoritative source)

- Startup resolves `DatabasePath` as `safe defaults < config file < environment < CLI flags`, then
  opens/migrates SQLite.
- For every field on the **safe-settings allowlist** (below), startup merges as `safe defaults < config file
  < SQLite desired settings < environment < CLI flags`. An environment/flag override that masks a
  persisted SQLite desired setting is reported via `maskedByStartupSource` in `GET /settings/safe` /
  `aw settings show`.
- The effective config is immutable for the lifetime of one process. `aw settings update` (`PUT
  /settings/safe`) only changes the *desired* version and reports `restartRequired: true` — a restart is what
  actually applies it.
- **Never mutable via safe-settings**: `LocalPrincipal`, the session/signing key, `DatabasePath`, `WorkerID`,
  and any raw secret value. These are process-startup-only (CLI flag / config file / environment), by design —
  see [05-providers-and-isolation.md](05-providers-and-isolation.md) for why the principal in particular is
  never a runtime-mutable value.
- The safe-settings surface covers: SQLite path, artifact/workspace roots, worker concurrency, lease
  TTL/heartbeat, provider executable/argv/model, process/output limits, retention, and log level.

```bash
aw settings show                                    # current effective + desired config, and any masking
echo '{...}' | aw settings update --expected-version 1  # change desired config; restart to apply
```

## `aw serve` — every real flag

```
$ aw serve -h
Usage of serve:
  -artifact-root string
        artifact storage root directory
  -claude-executable string
        path to the Claude CLI executable to register as a live agent provider for RetryBlockedActivation's own admission re-checks (omitted = provider not registered, RetryBlockedActivation fails closed with 503 for a build pinned to it)
  -codex-executable string
        path to the Codex CLI executable to register as a live agent provider for RetryBlockedActivation's own admission re-checks (omitted = provider not registered, RetryBlockedActivation fails closed with 503 for a build pinned to it)
  -db string
        sqlite database path
  -env-allowlist string
        comma-separated names of parent environment variables the provider executables' version probe may inherit; give it the same list as the worker's own --env-allowlist (default: none)
  -host string
        loopback bind host (default "127.0.0.1")
  -max-body-bytes int
        maximum accepted request body size in bytes (default 1048576)
  -port int
        bind port (0 = OS-assigned ephemeral port)
  -principal-config string
        path to a trusted JSON config file's localPrincipal.actor/localPrincipal.roles (ADR-028); omitted or missing means the local-operator/[operator] default — this is the only allowed way to select a principal, there is no --actor/--role flag
  -ui-dist pnpm build
        directory containing a built V7 UI (pnpm build output of web/, i.e. web/dist) to serve at / and /assets/; omitted = no UI, aw serve still works exactly as before V7
  -warn-hard-constraints int
        warn when a published skill/layer version or context policy holds more than this many HARD_CONSTRAINT resources (0 = the default of 15; negative = never warn)
  -warn-resource-age-days int
        warn when a published resource's lastVerified is older than this many days (0 = the default of 180; negative = never warn)
  -worker-id aw worker
        identity string recorded in this process' own config.Config for GET /doctor's config-validity check; this process does not itself run the lease/reaper worker pool (run aw worker for that; it has its own --worker-id) (default "aw-serve")
  -workspace-root string
        root directory for real Git worktree-backed workspace storage (internal/adapters/gitworktree.Provider) that the source/diff/repository-log inspection routes read through
```

`--db`/`--artifact-root`/`--workspace-root` are required (missing = a real startup error, not a silent
default). `--ui-dist` is genuinely optional — since V8-08, a release binary built with `cmd/aw-release-build`
serves its own embedded UI even with `--ui-dist` omitted; a plain `go build ./cmd/aw` (no embedded UI) falls
back to "no UI" exactly as before V8-08.

`--warn-resource-age-days` and `--warn-hard-constraints` (V9-10) are the thresholds of the knowledge-hygiene
warnings a definition publish reports — see [04-authoring-workflows.md](04-authoring-workflows.md#keeping-knowledge-honest--warnings-at-publish-and-instruction-files-v9-10).
`aw definition publish` takes the same two as `--warn-resource-age-days` / `--warn-hard-constraints` for a single
invocation. `aw worker --instruction-file-warn-bytes` sets the oversize limit recorded with each instruction file.

`--host`/`--port` only ever bind loopback — an external bind is refused at startup, never merely a config
recommendation (`docs/design/01-system-design.md`'s own "external bind bị từ chối").

## `aw worker` — every real flag

```
$ aw worker -h
Usage of worker:
  -artifact-root aw serve
        artifact storage root directory (must already exist; the same root aw serve uses)
  -claude-effort string
        the Claude CLI's --effort for every task: low, medium, high, xhigh or max (omitted = the CLI's default)
  -claude-executable string
        path to the Claude CLI executable to register as an agent provider (omitted = not registered; AGENT nodes pinned to it cannot run)
  -claude-max-budget-usd float
        the most ONE Claude attempt may spend, in US dollars, enforced by the CLI itself (--max-budget-usd); 0 = no ceiling. What an attempt actually spent is its USAGE_REPORTED event
  -claude-permission-mode string
        the Claude CLI's --permission-mode for every task: acceptEdits, auto, bypassPermissions, dontAsk, manual or plan (omitted = the CLI's default, under which a headless Claude refuses every file write in an aw worktree because it is never a trusted workspace; an agent that must change files needs acceptEdits)
  -codex-executable string
        path to the Codex CLI executable to register as an agent provider (omitted = not registered; AGENT nodes pinned to it cannot run)
  -completion-interval duration
        how often the completion orchestrator looks for runs waiting in VERIFYING (default 1s)
  -db aw serve
        sqlite database path (the same file aw serve uses)
  -env-allowlist string
        comma-separated names of parent environment variables a spawned provider/command process may inherit (default: none); an AGENT process inherits only the names an AgentProfile's envAllowlist also lists, and the provider's version probe inherits all of them
  -instruction-file-warn-bytes int
        size in bytes above which an instruction file the provider CLI loads by itself (CLAUDE.md, AGENTS.md) is recorded in the context snapshot as oversized (0 = the default of 16384)
  -lease-heartbeat duration
        how often an in-flight job's lease is renewed; must be shorter than --lease-ttl (default 10s)
  -lease-ttl duration
        how long a claimed job's lease stays valid without a heartbeat (default 30s)
  -local-commit-write-lease-ttl duration
        how long a release-set local-commit write lease is held without renewal (default 2m0s)
  -poll-interval duration
        how long an idle worker waits before polling for a job again (default 200ms)
  -projection-interval duration
        how often the live projection consumer scans the event journal (default 500ms)
  -projection-rebuild-batch-size int
        how many journal rows one projection rebuild BUILDING/CUTTING_OVER round scans (0 keeps the package default, 500)
  -reaper-interval duration
        pause between two recovery-reaper passes (orphaned attempts, stranded cancellation intents) (default 5s)
  -shutdown-grace duration
        how long in-flight jobs may finish after a shutdown signal before their context is cancelled (default 30s)
  -sweep-interval duration
        pause between two artifact retention sweeps (default 1h0m0s)
  -worker-concurrency int
        maximum jobs run at once (default 4)
  -worker-id string
        lease-owner identity for this process; must be unique among running workers (default "aw-worker-<pid>")
  -workspace-root aw serve
        root directory for Git worktree-backed workspaces (the same root aw serve uses)
```

`--db`/`--artifact-root`/`--workspace-root` MUST point at the same three locations the `aw serve` process for
this installation uses — `aw worker` never creates its own separate database. Running more than one `aw
worker` process against the same `--db` is supported (each needs its own `--worker-id`) — real-lease fencing
(`--lease-ttl`/`--lease-heartbeat`) is what keeps two workers from double-processing the same job.

## The agent's environment — `--env-allowlist` and the profile's `envAllowlist`

Every process `aw` spawns starts from an EMPTY environment. The only variables it gets from the worker's own
environment are ones whose NAME is allowed explicitly. For agents, `--env-allowlist` (empty by default) is the
operator's list of what may ever be passed on:

```bash
aw worker ... --claude-executable /usr/local/bin/claude --env-allowlist PATH,HOME
```

For an `AGENT` node (since V9-05) the provider process — the Claude or Codex CLI — inherits exactly the
**intersection** of two lists of variable names:

- the `envAllowlist` of the AgentProfile version the node pins (the author's request, see
  [04-authoring-workflows.md](04-authoring-workflows.md)), and
- the `--env-allowlist` of the `aw worker` that executes the attempt (the operator's ceiling).

So a profile can never widen what the operator allowed; a profile that declares nothing, or a worker started
without `--env-allowlist`, gives the agent an empty environment exactly as before. Rules worth knowing:

- **Names are matched exactly and are case-sensitive.** Write each name identically in both lists. On Windows
  write `PATH`, not `Path`, in both.
- **Only names are ever recorded.** The names the agent receives are fixed when the node is scheduled and
  written into its execution profile (`agentInheritedEnvironment` in the `<nodeRunId>-execution-profile-v1`
  decision artifact, and therefore into the execution profile hash). The values are read from the worker's
  environment at the moment the process is spawned and `aw` never stores, logs, hashes or puts them in evidence.
  (If the agent itself prints a value, for example by running `env`, that text is recorded as ordinary agent
  output — allow only variables you are content for the agent to read.)
- **The worker that executes the attempt applies its own list again.** A node scheduled by one worker may be
  executed by another (or by the same worker after a restart with different flags); the agent gets the pinned
  names that THIS worker also allows, never more. A retry or a recovery attempt of the same node starts from
  the same pinned list.
- **No wrapper script is needed any more.** Operators used to wrap the provider CLI in a script that
  hard-coded `HOME` and `PATH` — environment recorded nowhere. Allow `PATH` and `HOME` (on Windows also
  `USERPROFILE` and `SystemRoot`) in `--env-allowlist`, list them in the profile's `envAllowlist`, and point
  `--claude-executable` at the real CLI.
- **`COMMAND` and `MACHINE_GATE` nodes are unchanged:** they inherit the names their own Command definition
  lists in its `envAllowlist`.

`--env-allowlist` also governs the provider executable's own `--version` probe. `aw worker` runs it at startup
and again at every admission of an AGENT attempt (to detect a changed executable), `aw serve` runs it at
startup, and the one-shot commands that need the provider run it too; all of them inherit the names in the list
they were given, so a CLI that needs `HOME` or `PATH` even to print its version works without a wrapper. Give
`aw serve` and the one-shot commands (the global option below, or `AW_ENV_ALLOWLIST`) the same list as the
worker.

`aw doctor` checks the result. For every configured provider executable it runs that probe with exactly the
`--env-allowlist` it was given — the widest environment any profile can ever receive — and reports a
`provider_environment:<provider>` check. When the executable cannot run in that environment the check is
`DEGRADED` and its detail starts with the stable code `PROVIDER_ENV_INSUFFICIENT:`, followed by the reason
(for example `it exited with code 1`) and the variable NAMES it was run with; the remediation says which
allowlists to extend. It never prints a variable value. See
[05-providers-and-isolation.md](05-providers-and-isolation.md#env-allowlist).

## Global options (every `aw <resource> <action>` command)

```
Global options (any resource command; env AW_DB, AW_ARTIFACT_ROOT, ...):
  --db <path>                 sqlite database of the installation
  --artifact-root <dir>       artifact storage root
  --workspace-root <dir>      Git worktree storage root (workspace/source commands)
  --claude-executable <path>  register the Claude CLI as a live provider
  --codex-executable <path>   register the Codex CLI as a live provider
  --env-allowlist <names>     comma-separated variable names a provider's version probe may inherit
                              (give it the same list as 'aw worker --env-allowlist'; 'aw doctor' reports
                              whether the provider can run in that environment)
```

Each has an environment-variable fallback (`AW_DB`, `AW_ARTIFACT_ROOT`, `AW_WORKSPACE_ROOT`,
`AW_CLAUDE_EXECUTABLE`, `AW_CODEX_EXECUTABLE`, `AW_ENV_ALLOWLIST`) so a long-running shell session doesn't need
to repeat them on every invocation:

```bash
export AW_DB=./aw-install/aw.db AW_ARTIFACT_ROOT=./aw-install/artifacts AW_WORKSPACE_ROOT=./aw-install/workspaces
aw project list   # no --db/--artifact-root/--workspace-root needed now
```

CLI flags always win over the environment variable of the same name.

## `--principal-config`

The ONLY way to select which local principal (actor + roles) a process runs as — there is deliberately no
`--actor`/`--role` flag anywhere (ADR-028: "không có per-command impersonation flag"). Omitted or missing means
the `local-operator`/`[operator]` default. Points at a trusted JSON file with `localPrincipal.actor`/
`localPrincipal.roles`.
