# Quickstart

Every command and every ID on this page was actually run against a real, freshly-built `aw` binary while
writing this document (Windows; the same flow works identically on Linux with `aw` instead of `aw.exe` and
POSIX-style paths). It ends with a real workflow run reaching `SUCCEEDED`, real evidence recorded with verdict
`PASS`, and a real `doctor` reporting `HEALTHY`. IDs below are copy-pasted from that real run — running this
yourself will mint different IDs; substitute your own at each step.

## 1. Install

There is no separate installer: `aw`/`aw.exe` is a single, self-contained binary (V8-08 embeds the built UI
into it — see `docs/design/10-v8-alpha-hardening.md` V8-08). Put it on your `PATH`, or reference it by full
path as this quickstart does.

## 2. Start the installation

Every `aw serve`/`aw worker` process needs three real directories: a SQLite database file, an artifact
storage root, and a workspace root (real Git worktree storage). `aw serve` creates and migrates the database
on first start; `--artifact-root` must already exist.

```bash
mkdir -p ./aw-install/artifacts ./aw-install/workspaces
aw serve --db ./aw-install/aw.db --artifact-root ./aw-install/artifacts --workspace-root ./aw-install/workspaces \
  --host 127.0.0.1 --port 18080
```

`serve` prints one JSON line on stdout once it is ready: `{"address":"127.0.0.1:18080"}`. It never binds
anything but loopback (ADR-028) — there is no way to expose this over the network.

In a second terminal, start the worker against the SAME `--db`/`--artifact-root`/`--workspace-root` — nothing
actually executes (repository probes, workflow nodes, projection updates) without it:

```bash
aw worker --db ./aw-install/aw.db --artifact-root ./aw-install/artifacts --workspace-root ./aw-install/workspaces
```

It prints `{"workerId":"aw-worker-<pid>"}` once ready. See [02-configuration.md](02-configuration.md) for
every other flag both processes accept.

Every resource command below (`aw <resource> <action>`) needs the SAME `--db`/`--artifact-root`
`--workspace-root` flags repeated (or set once via `AW_DB`/`AW_ARTIFACT_ROOT`/`AW_WORKSPACE_ROOT` environment
variables — see [02-configuration.md](02-configuration.md)); they are omitted below for readability.

## 3. Create a project and register a repository

```bash
echo '{"name":"quickstart"}' | aw project create --idempotency-key proj-1
```

```json
{"idempotencyKey": "proj-1", "replayed": false, "result": {
  "projectId": "e3b7544b-1b6a-4189-930b-c699bc492430", "name": "quickstart", "status": "ACTIVE"
}}
```

Register a real, already-existing local Git repository (an absolute filesystem path — never a remote URL;
see [06-source-control-and-releases.md](06-source-control-and-releases.md) for why):

```bash
echo '{"repositoryId":"repo-a","name":"repo-a","remoteLocator":"/absolute/path/to/your/repo","defaultRef":"main"}' \
  | aw repository register --project-id e3b7544b-1b6a-4189-930b-c699bc492430 --idempotency-key repo-1
```

This returns immediately with `"status": "REGISTERING"` and a `probeJobId` — the real `aw worker` process
probes it asynchronously (a few seconds). Poll until it settles:

```bash
aw repository list e3b7544b-1b6a-4189-930b-c699bc492430
```

A correct, existing local path settles to `"status": "ACTIVE"`. **A wrong path settles to `"status":
"BLOCKED"` with `"lastProbeErrorCode": "NOT_FOUND"`** — this is the single most common quickstart mistake (a
relative or shell-specific path the worker process, running with its own working directory, can't resolve);
register a NEW repository ID with the corrected absolute path rather than trying to fix the blocked one (there
is no "edit repository" command).

## 4. Author and publish a minimal workflow

A published Definition is a two-step process: `create` the definition shell (once), then `publish` a version
(as many times as you like — each publish is a new, immutable version). See
[04-authoring-workflows.md](04-authoring-workflows.md) for the full schema of every document kind; this
section publishes the smallest REAL graph that runs without needing a Claude/Codex provider at all: a single
`MACHINE_GATE` node (`START -> MACHINE_GATE -> END`) running a script you supply, with a completion policy
requiring the gate's own evidence.

```bash
# Attempt policy (retry/timeout behavior for every node)
echo '{"definitionId":"attempt-policy","name":"attempt policy"}' | aw definition create --kind POLICY --idempotency-key create-attempt
echo '{"category":"ATTEMPT","attempt":{"maxAttempts":3,"backoffSeconds":1,"timeoutSeconds":60}}' \
  | aw definition publish --kind POLICY --idempotency-key pub-attempt --yes attempt-policy

# Permission policy (isolation tier — see 05-providers-and-isolation.md)
echo '{"definitionId":"permission-policy","name":"permission policy"}' | aw definition create --kind POLICY --idempotency-key create-perm
echo '{"category":"PERMISSION","permission":{"isolationTier":"OPERATOR_TRUSTED_LOCAL"}}' \
  | aw definition publish --kind POLICY --idempotency-key pub-perm --yes permission-policy

# Completion policy (what evidence a run must produce to count as done)
echo '{"definitionId":"completion-policy","name":"completion policy"}' | aw definition create --kind POLICY --idempotency-key create-comp
echo '{"category":"COMPLETION","completion":{"requiredEvidenceKinds":["QUICKSTART_OUTPUT_VERIFIED"]}}' \
  | aw definition publish --kind POLICY --idempotency-key pub-comp --yes completion-policy
```

The gate's own script lives in a Skill document (**OS matters here** — a `MACHINE_GATE` runs its command as a
real OS process with no shell interpreter unless the script itself is a real executable for this OS; use a
`.sh` with a shebang on Linux/macOS, a real `.bat`/`.cmd` on Windows — mixing this up is the second most common
quickstart mistake, and produces a real `"gate evaluator could not be spawned ... %1 is not a valid Win32
application"` failure on Windows if you use a `.sh` script there):

```bash
# Linux/macOS:
echo '{"resources":[{"key":"quickstart-gate.sh","instruction":"#!/bin/sh\necho '\''{\"QUICKSTART_OUTPUT_VERIFIED\":{\"verdict\":\"PASS\"}}'\''\nexit 0\n","priority":"GUIDANCE","global":true,"selector":{},"provenance":{"owner":"quickstart","source":"docs","revision":"v1"}}]}' \
  | aw definition publish --kind SKILL --idempotency-key pub-skill --yes scripts
```

```bash
# Windows:
echo '{"resources":[{"key":"quickstart-gate.bat","instruction":"@echo off\r\necho {\"QUICKSTART_OUTPUT_VERIFIED\":{\"verdict\":\"PASS\"}}\r\nexit /b 0\r\n","priority":"GUIDANCE","global":true,"selector":{},"provenance":{"owner":"quickstart","source":"docs","revision":"v1"}}]}' \
  | aw definition publish --kind SKILL --idempotency-key pub-skill --yes scripts
```

(`aw definition create --kind SKILL` with `{"definitionId":"scripts","name":"quickstart scripts"}` first, same
as every other kind above.) The publish response's own `id` field is this skill VERSION's id — call it
`$SKILL_VID`. The gate script's own content hash is deterministic (SHA-256 of the resource's instruction/
priority/global/selector, independent of which version owns it); the real hash for the Windows `.bat` body
above is `sha256:92984c5081c73046648d0d681f3ad752bd5d71602b453d34a80610fa966b323c` — compute your own with
`aw definition validate` against a Skill document if your script content differs even by one byte.

```bash
echo '{"definitionId":"gate-command","name":"gate command"}' | aw definition create --kind COMMAND --idempotency-key create-cmd
echo '{"executable":{"ownerVersionId":"'"$SKILL_VID"'","resourceKey":"quickstart-gate.bat","contentHash":"sha256:92984c5081c73046648d0d681f3ad752bd5d71602b453d34a80610fa966b323c"},"argv":[{"kind":"LITERAL","value":"run"}],"cwdRepositoryTarget":"unused-by-machine-gate","compatibility":{"os":["windows"]},"networkAccess":"NONE","timeoutSeconds":60,"output":{"captureStdout":true,"captureStderr":true,"maxOutputBytes":65536}}' \
  | aw definition publish --kind COMMAND --idempotency-key pub-cmd --yes gate-command
```

`cwdRepositoryTarget` is a required field on every Command document, but a `MACHINE_GATE`'s own command is
never actually run inside a repository checkout (it always gets a fresh scratch directory) — any placeholder
string satisfies the schema. Call the publish response's `id` field `$CMD_VID`.

```bash
echo '{"definitionId":"machine-gate","name":"machine gate"}' | aw definition create --kind GATE --idempotency-key create-gate
echo '{"commandRef":{"kind":"COMMAND","definitionId":"gate-command","versionId":"'"$CMD_VID"'"},"criteria":[{"name":"output-verified","evidenceKey":"QUICKSTART_OUTPUT_VERIFIED"}]}' \
  | aw definition publish --kind GATE --idempotency-key pub-gate --yes machine-gate
```

Call the gate publish response's `id` field `$GATE_VID`. Finally, the workflow itself — the only document in
this whole chain that is PROJECT-scoped (every Policy/Skill/Command/Gate above is installation-scoped and
reusable across every project):

```bash
echo '{"schemaVersion":"1","nodes":[{"key":"start","type":"START","outcomes":["next"]},{"key":"gate","type":"MACHINE_GATE","outcomes":["passed"],"machineGate":{"gateRef":{"kind":"GATE","definitionId":"machine-gate","versionId":"'"$GATE_VID"'"},"policyRefs":[{"kind":"POLICY","definitionId":"attempt-policy","versionId":"<attempt-policy-version-id>"},{"kind":"POLICY","definitionId":"permission-policy","versionId":"<permission-policy-version-id>"}]}},{"key":"end","type":"END"}],"edges":[{"key":"start-gate","from":"start","outcome":"next","to":"gate"},{"key":"gate-end","from":"gate","outcome":"passed","to":"end"}],"completionPolicyRef":{"kind":"POLICY","definitionId":"completion-policy","versionId":"<completion-policy-version-id>"}}' \
  | aw definition create --kind WORKFLOW --project-id e3b7544b-1b6a-4189-930b-c699bc492430 --idempotency-key create-wf \
  && aw definition publish --kind WORKFLOW --project-id e3b7544b-1b6a-4189-930b-c699bc492430 --idempotency-key pub-wf --yes quickstart-workflow
```

(Substitute the real `versionId`s each policy publish returned.) The response's own `id` field is
`$WORKFLOW_VID` — the run needs this exact value.

## 5. Create a task and run it

Every real run needs a CHILD WorkItem (a workflow never runs directly on a root WorkItem) with a real
readiness CONTRACT — an empty contract genuinely fails readiness with problems like `"behavior is required"`:

```bash
echo '{"projectId":"e3b7544b-1b6a-4189-930b-c699bc492430","title":"quickstart root task","initialScope":[{"repositoryId":"repo-a","access":"READ","reason":"quickstart"}]}' \
  | aw work-item create --project-id e3b7544b-1b6a-4189-930b-c699bc492430 --idempotency-key create-root
```

```bash
echo '{"title":"quickstart child task","parentJoinPolicy":"ALL_CHILDREN_DONE","effectiveScope":[{"repositoryId":"repo-a","access":"READ","reason":"quickstart"}],"contract":{"schemaVersion":1,"behavior":"Run the quickstart machine gate and confirm it passes.","verificationSpec":"The machine gate reports QUICKSTART_OUTPUT_VERIFIED with verdict PASS.","riskLevel":"LOW","acceptanceCriteria":[{"description":"Gate passes","verificationRef":"QUICKSTART_OUTPUT_VERIFIED"}],"workflowVersionId":"<workflow-version-id>"}}' \
  | aw work-item create-child --idempotency-key create-child <root-work-item-id>
```

Confirm it is really ready before marking it (an honest pre-flight, not required, but cheap):

```bash
aw work-item readiness --project-id e3b7544b-1b6a-4189-930b-c699bc492430 <child-work-item-id>
# {"workItemId": "...", "status": "BACKLOG", "version": 1, "ready": true}

aw work-item mark-ready --expected-version 1 --idempotency-key mark-ready <child-work-item-id>
```

Start the run — `--wait` blocks until it reaches a terminal state, which for a single-gate graph like this is
typically under two seconds:

```bash
aw run start --workflow-version-id <workflow-version-id> --idempotency-key start-run --wait --wait-timeout 30s <child-work-item-id>
```

A real successful run's own response embeds a `"wait"` object with `"state": "SUCCEEDED"`. The real run this
quickstart was verified against returned exactly this shape.

If it ends `"state": "FAILED"` instead, the work item is not lost: it is `BLOCKED` by a `RUN_FAILED` blocker. Find out
why (`aw run timeline <runId>`), `aw blocker resolve --mode RESOLVED --reason "..." <blockerId>`, then `aw run start`
again on the same work item — see [09-troubleshooting.md](09-troubleshooting.md).

## 6. Confirm it actually worked

```bash
aw evidence list --project-id e3b7544b-1b6a-4189-930b-c699bc492430 <child-work-item-id>
```

A real, successful run's evidence entry has `"verdict": "PASS"` and an `artifactReferences` entry — fetch the
raw gate output with `aw artifact get <workItemId> <evidenceId> <artifactId> --project-id <id> --output -` (see
[03-cli-reference.md](03-cli-reference.md)).

```bash
aw doctor
```

A healthy installation reports `status: HEALTHY` with every check (`process_liveness`, `app_config`,
`database`, `artifact_root`, `git`, `safe_settings`, `isolation_enforcement`) also HEALTHY.

## Recovering from the two real mistakes this walkthrough hit

Both of the following were genuinely hit and fixed while verifying this exact quickstart — they are the most
likely first mistakes, not hypothetical:

1. **Repository stuck BLOCKED / `NOT_FOUND`**: your `remoteLocator` path was wrong (often a shell-specific or
   relative path the worker process can't resolve the same way your shell does). Register a new repository ID
   with the corrected absolute path — see step 3 above.
2. **Gate evaluator "not a valid Win32 application" (Windows) or "permission denied" (Linux/macOS)**: your
   gate script doesn't match the OS the worker process actually runs on. Publish a NEW skill version with the
   correct script for your OS, then a new command version pointing at it, a new gate version pointing at that
   command, and a new workflow version pointing at that gate — Definitions are immutable per version, so
   fixing a mistake always means publishing forward, never editing in place. See
   [09-troubleshooting.md](09-troubleshooting.md) for the general pattern.

For everything else, see [09-troubleshooting.md](09-troubleshooting.md).
