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
  -worker-id aw worker
        identity string recorded in this process' own config.Config for GET /doctor's config-validity check; this process does not itself run the lease/reaper worker pool (run aw worker for that; it has its own --worker-id) (default "aw-serve")
  -workspace-root string
        root directory for real Git worktree-backed workspace storage (internal/adapters/gitworktree.Provider) that the source/diff/repository-log inspection routes read through
```

`--db`/`--artifact-root`/`--workspace-root` are required (missing = a real startup error, not a silent
default). `--ui-dist` is genuinely optional — since V8-08, a release binary built with `cmd/aw-release-build`
serves its own embedded UI even with `--ui-dist` omitted; a plain `go build ./cmd/aw` (no embedded UI) falls
back to "no UI" exactly as before V8-08.

`--host`/`--port` only ever bind loopback — an external bind is refused at startup, never merely a config
recommendation (`docs/design/01-system-design.md`'s own "external bind bị từ chối").

## `aw worker` — every real flag

```
$ aw worker -h
Usage of worker:
  -artifact-root aw serve
        artifact storage root directory (must already exist; the same root aw serve uses)
  -claude-executable string
        path to the Claude CLI executable to register as an agent provider (omitted = not registered; AGENT nodes pinned to it cannot run)
  -codex-executable string
        path to the Codex CLI executable to register as an agent provider (omitted = not registered; AGENT nodes pinned to it cannot run)
  -completion-interval duration
        how often the completion orchestrator looks for runs waiting in VERIFYING (default 1s)
  -db aw serve
        sqlite database path (the same file aw serve uses)
  -env-allowlist string
        comma-separated names of parent environment variables a spawned provider/command process may inherit (default: none)
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

Env allowlist (`--env-allowlist`) is empty by default — a spawned provider/command process inherits NOTHING
from the worker's own environment unless a variable name is explicitly listed here.

## Global options (every `aw <resource> <action>` command)

```
Global options (any resource command; env AW_DB, AW_ARTIFACT_ROOT, ...):
  --db <path>                 sqlite database of the installation
  --artifact-root <dir>       artifact storage root
  --workspace-root <dir>      Git worktree storage root (workspace/source commands)
  --claude-executable <path>  register the Claude CLI as a live provider
  --codex-executable <path>   register the Codex CLI as a live provider
```

Each has an environment-variable fallback (`AW_DB`, `AW_ARTIFACT_ROOT`, `AW_WORKSPACE_ROOT`,
`AW_CLAUDE_EXECUTABLE`, `AW_CODEX_EXECUTABLE`) so a long-running shell session doesn't need to repeat them on
every invocation:

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
