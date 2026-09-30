# CLI reference

## Why this page doesn't list every flag by hand

`aw help` and `aw <resource> <action> -h` are themselves the authoritative, always-in-sync source for exactly
which flags a command takes — they are generated from the same flag definitions the command actually parses,
so they can never drift from real behavior the way a hand-copied static table could. This page documents the
STRUCTURE and CONVENTIONS every command shares (verified against the real binary below), then tells you where
to get the definitive per-command detail. This is a deliberate choice, not a gap: a copy of hundreds of flags
transcribed by hand into this file would go stale the next time a command's flags change, while `-h` never can.

## The real, complete command surface

```
$ aw help
Usage: aw <command> [flags]

Process commands:
  serve       start the local API/control-plane server
  worker      start an embedded worker process
  help        show this help
  version     print the aw build version

Resource commands (aw <resource> <action> [flags]):
  adapter                list|probe|register|show
  approval               resolve
  artifact               get
  blocker                resolve
  component              list
  context-snapshot       show
  definition             create|list|publish|show|validate|versions
  doctor
  events                 watch
  evidence               list|verify
  health                 live|ready
  message                append|list|upload-attachment
  node-run               retry-blocked
  pack-assignment        assign|list
  project                create|list|show
  projection             rebuild|rebuild-status|status
  release-set            abandon|create|list|local-commit|seal|show
  repository             list|onboarding|register|retry-probe
  repository-workspace   diff|log|reconcile|source
  run                    cancel|diagnostics|graph|show|start|timeline
  scope-expansion        approve|reject|request|withdraw
  settings               show|update
  version                diff|show
  wait                   signal
  work-item              cancel|create|create-child|list|mark-ready|readiness|show
  workspace-set          release|show

Global options (any resource command; env AW_DB, AW_ARTIFACT_ROOT, ...):
  --db <path>                 sqlite database of the installation
  --artifact-root <dir>       artifact storage root
  --workspace-root <dir>      Git worktree storage root (workspace/source commands)
  --claude-executable <path>  register the Claude CLI as a live provider
  --codex-executable <path>   register the Codex CLI as a live provider

Machine-readable output: add --json (one JSON document on stdout; a failure is
one typed error document). High-impact commands require --yes when stdin is not
a terminal or --json is set. Actor and roles are never flags: --principal-config
selects the trusted principal file.

Run 'aw <command> -h' for command-specific flags.
```

This IS `CLI_LOCAL`'s own closed process-command set (`serve`/`worker`/`help`/`version` — every other
top-level word is a "resource" routed through a shared resource-command layer, V6-15O). `evidence verify` also
runs the pre-V6 offline bundle verifier when invoked with `--evidence-dir`/`--suite` flags instead of a
`--project-id` — see `cmd/aw/cli.go`'s own `isLegacyBundleVerify`.

## Shared conventions across every resource command

- **Body input**: every mutation reads a JSON request body from stdin (pipe it in) or `--file <path>` — never
  as inline flags. A query command (`list`/`show`/etc.) takes no body.
- **`--idempotency-key`**: every mutation accepts one; omit it and the command mints one for you and returns
  it in the response's own `idempotencyKey` field. Replaying the SAME key with the SAME request returns
  `"replayed": true` and the ORIGINAL result — never re-executes.
- **`--expected-version`**: every update-shaped command (never a create) requires this — the CLI equivalent of
  HTTP's `If-Match`. A stale version is a real, typed `CONFLICT`, never silently overwritten.
- **`--project-id`**: required for anything project-scoped; omitted for genuinely installation-scoped commands
  (`project create`, `definition create --kind POLICY` with no project, etc.) — see
  [04-authoring-workflows.md](04-authoring-workflows.md) for which definition kinds are project- vs
  installation-scoped.
- **`--principal-config`**: selects the trusted actor/roles file; never a `--actor`/`--role` flag (ADR-028).
- **`--yes`**: required for a "high-impact" mutation whenever stdin is not an interactive terminal OR `--json`
  is set (i.e., every scripted/piped invocation) — an interactive terminal session gets a real confirmation
  prompt instead. `--yes` is a flag `clicompose` strips before the command's own flag parsing runs, so it can
  appear anywhere in the argument list; a NON-high-impact command (e.g. `definition create`) does not
  recognize `--yes` at all and rejects it as an unknown flag if you pass it anyway — try without it first if
  you hit `flag provided but not defined: -yes`.
- **`--wait`/`--wait-timeout`**: on commands that kick off asynchronous work (`run start`, `release-set
  local-commit`), blocks and polls until the affected job/run reaches a terminal state. Purely observational —
  it never itself executes, retries, or cancels anything; omitting it returns immediately with the initial
  (non-terminal) state.
- **`--json`**: machine-readable single-document output on every command, success or failure — a failure is
  one typed error document (`{"error": {"code": ..., "message": ...}}`), never a bare stack trace or a
  human-formatted message mixed into stdout.
- **Exit codes**: `0` success, `1` failure (a real, typed application error — check the JSON error body's own
  `code`), `2` usage error (bad flags, missing required arguments — the command never even attempted the
  operation).

## Positional arguments come after flags

Every resource command that takes a positional argument (an ID) expects flags FIRST, then the positional
argument last — standard Go `flag` package behavior (it stops recognizing flags at the first non-flag
argument). `aw definition publish --kind POLICY --yes my-policy-id` works; `aw definition publish my-policy-id
--kind POLICY --yes` does not (the reordered flags are read as extra positional arguments and rejected).

## Two worked examples (real, verified)

```bash
# Query: no body, --project-id required for a project-scoped resource
aw work-item show --project-id <projectId> <workItemId>

# Mutation: body via stdin, --expected-version required (this is an update), --yes since it's non-interactive
echo '{}' | aw work-item mark-ready --expected-version 1 --idempotency-key my-key --yes <workItemId>
```

See [01-quickstart.md](01-quickstart.md) for a complete, real, multi-command walkthrough from a fresh
installation through a completed run, and [08-backup-and-restore.md](08-backup-and-restore.md) for the
separate `aw-maintenance` binary (backup/restore is NOT a resource command — see that page for why).
