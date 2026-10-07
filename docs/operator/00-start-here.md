# Agent Kit operator documentation — start here

> V8-09 (`docs/design/10-v8-alpha-hardening.md` V8-09): first-run/operator documentation for the Alpha
> release. This directory documents **capability that has been verified against the real binary** — every
> command shown here was actually run against a real `aw serve`/`aw worker` install while this documentation
> was written (see `baocaov8checklist.md`'s own V8-09 section for the full real transcript this quickstart
> was extracted from). It never documents aspirational or planned behavior.

## What this is

Agent Kit (`aw`) is a local, single-operator control plane for running AI-agent workflows against your own
Git repositories: you register repositories, author a workflow graph (agents, commands, gates, approvals),
and `aw` drives real runs against real repository worktrees, recording every decision as durable, queryable
evidence. It is not a multi-tenant SaaS product — one `aw` installation serves one operator's own machine
(ADR-025/ADR-028).

## Where things are

| You want to... | Read |
|---|---|
| Install and run your first workflow | [01-quickstart.md](01-quickstart.md) |
| Understand every `aw serve`/`aw worker` flag and config precedence | [02-configuration.md](02-configuration.md) |
| Look up an `aw <resource> <action>` command, its flags, JSON shape, or exit code | [03-cli-reference.md](03-cli-reference.md) |
| Write your own Policy/Skill/Command/Gate/Workflow documents | [04-authoring-workflows.md](04-authoring-workflows.md) |
| Register a Claude/Codex provider, or understand isolation tiers | [05-providers-and-isolation.md](05-providers-and-isolation.md) |
| Understand ReleaseSet, local-only Git commits, and source/diff/log viewing | [06-source-control-and-releases.md](06-source-control-and-releases.md) |
| Understand evidence, artifact retention, and what gets cleaned up when | [07-evidence-and-retention.md](07-evidence-and-retention.md) |
| Back up or restore an installation | [08-backup-and-restore.md](08-backup-and-restore.md) |
| Upgrade to a new release, or roll back one | [10-upgrade-and-rollback.md](10-upgrade-and-rollback.md) |
| Diagnose a failure | [09-troubleshooting.md](09-troubleshooting.md) |
| Follow one complete worked example — building a Spring Boot + React application with `aw`, from an empty repository to merged features, run with a real Claude CLI (in Vietnamese) | [../guides/todolist-spring-react/README.md](../guides/todolist-spring-react/README.md) |

## Out of scope (explicitly, so you stop looking here for it)

- **No interactive browser terminal.** `repository-workspace source`/`diff`/`log` give you read-only,
  paginated file/diff/history views over a real Git object store — never a shell into a running container or
  an in-browser terminal emulator. See [06-source-control-and-releases.md](06-source-control-and-releases.md).
- **No multi-installation sync.** Backup/restore covers ONE local installation; it never promises to merge or
  sync two separate installations. See [08-backup-and-restore.md](08-backup-and-restore.md).
- **No remote Git push/fetch/clone.** Every repository this installation touches is read directly off the
  local filesystem path you register (`remoteLocator`); ReleaseSet commits land in that same local repository
  as ordinary local commits — nothing is ever pushed to a remote (`06-source-control-and-releases.md`).
- **No real OS-level sandbox in Alpha.** Only `OPERATOR_TRUSTED_LOCAL` isolation is available; a workflow node
  pinned to `ENFORCED_ISOLATED` fails closed rather than silently running unsandboxed — see
  [05-providers-and-isolation.md](05-providers-and-isolation.md).
- **No PostgreSQL, no second persistence adapter.** SQLite is the only Alpha persistence engine.
- **No multi-tenant/cloud deployment.** One `aw` installation, one local operator, one machine.

## Fresh-run self-test

A clean session — no prior context beyond this documentation — should be able to answer, with a citation into
one of the files above, for each of the five flows named in V8-09's own scope (install & first run; register a
repository; author & publish a workflow; run a task; recover from a failure):

- **WHAT** does this flow accomplish, and why would an operator do it?
- **WHERE** in this documentation (which file, which section) is it covered?
- **HOW** exactly — which real `aw` command(s), with which real flags?
- **DONE** — what observable signal (a JSON field, an exit code, a `doctor` check) proves the flow actually
  succeeded?
- **Out of scope** — what does this flow deliberately NOT cover (linked from the list above where relevant)?

If any of the five answers can't be sourced from a file in this directory, that is a real gap in this
documentation, not something to explain verbally — file it the same way any other real gap in this repo gets
tracked.
