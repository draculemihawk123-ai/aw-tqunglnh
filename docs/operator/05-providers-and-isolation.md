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

## Env allowlist

A spawned provider or command process inherits NOTHING from the worker's own process environment by default.
`aw worker --env-allowlist NAME1,NAME2` is the only way to let specific named variables through — see
[02-configuration.md](02-configuration.md).
