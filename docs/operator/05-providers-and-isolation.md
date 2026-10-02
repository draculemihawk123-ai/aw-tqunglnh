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
definition lists in its `envAllowlist`.
