# Authoring workflows

## The publish lifecycle

Every definition is `create` once (mints a `definitionId` + `kind`, installation- or project-scoped), then
`publish` any number of times (each publish is a new, immutable, numbered version — never edit an existing
version in place). `aw definition validate` runs the identical schema/dependency checks `publish` does without
actually publishing — use it to check a document before committing to a version number.

```bash
echo '{"definitionId":"<id>","name":"<human name>"}' | aw definition create --kind <KIND> [--project-id <id>]
echo '<document JSON>' | aw definition publish --kind <KIND> [--project-id <id>] --yes <id>
```

## The 9 definition kinds (closed set)

`WORKFLOW`, `BLOCK`, `SKILL`, `LAYER`, `ENGINEERING_PACK`, `AGENT_PROFILE`, `COMMAND`, `GATE`, `POLICY`
(`internal/domain/definition/lifecycle.go`). Only `WORKFLOW` is ever project-scoped — every other kind is
installation-scoped and reusable across every project (confirmed: publishing `POLICY`/`SKILL`/`COMMAND`/`GATE`
in [01-quickstart.md](01-quickstart.md) never passed `--project-id`).

## POLICY — real, verified shapes

A Policy document has one `category` and exactly the ONE matching rule set for it:

```json
{"category": "ATTEMPT", "attempt": {"maxAttempts": 3, "backoffSeconds": 1, "timeoutSeconds": 60}}
{"category": "PERMISSION", "permission": {"isolationTier": "OPERATOR_TRUSTED_LOCAL", "grantedCapabilities": ["INTEGRATION_MULTI_REPOSITORY_WRITE"]}}
{"category": "COMPLETION", "completion": {"requiredEvidenceKinds": ["MY_EVIDENCE_KEY"]}}
{"category": "CONTEXT", "context": {"selector": ["my-context-selector"], "budget": {"maxTokens": 4096}}}
{"category": "CONTEXT", "context": {"selector": ["my-context-selector"], "budget": {"maxTokens": 4096},
                                    "messages": {"maxBytes": 32768, "keepLatest": 2}}}
```

The optional `context.messages` block bounds how much of the task chat an attempt's prompt carries — see
[Keeping the prompt bounded across rework rounds](#keeping-the-prompt-bounded-across-rework-rounds-v9-07).

Categories: `ATTEMPT`, `COMPLETION`, `PERMISSION`, `CONTEXT`, `CLEANUP` (`internal/domain/policy/policy.go`).
`PERMISSION`'s own `isolationTier` is either `OPERATOR_TRUSTED_LOCAL` or `ENFORCED_ISOLATED` — see
[05-providers-and-isolation.md](05-providers-and-isolation.md) for why only the first is usable in Alpha.

## SKILL — real, verified shape

A Skill document is a list of named, versioned resources (scripts, prompt fragments) a Command or AgentProfile
can reference by key:

```json
{"resources": [{"key": "my-script.sh", "instruction": "#!/bin/sh\necho done\nexit 0\n",
  "priority": "GUIDANCE", "global": true, "selector": {},
  "provenance": {"owner": "you", "source": "docs", "revision": "v1"}}]}
```

Each resource's own content hash (needed by any Command that references it) is a deterministic SHA-256 of
`instruction`/`priority`/`global`/`selector` — independent of which SkillVersion owns it, so republishing the
identical script content under a new version number reuses the identical hash.

For an agent, `priority` (`HARD_CONSTRAINT`, `REQUIRED_PROCEDURE`, `GUIDANCE`, `REFERENCE`) decides where the resource
appears in its prompt and how it is labelled — see
[What an agent receives](#what-an-agent-receives--the-instruction-artifact-v9-03-adr-032). Write the rules that must
hold as `HARD_CONSTRAINT`; the agent reads them first and again at the very end. A Skill does **not** need to list the
outcomes an agent may report (see the same section). `selector` decides *which* attempts get the resource — see
[Routing knowledge to a code area](#routing-knowledge-to-a-code-area--resource-selectors-v9-04).

## COMMAND — real, verified shape

```json
{"executable": {"ownerVersionId": "<skill-version-id>", "resourceKey": "my-script.sh", "contentHash": "sha256:..."},
 "argv": [{"kind": "LITERAL", "value": "run"}],
 "cwdRepositoryTarget": "<repositoryId-or-placeholder>",
 "compatibility": {"os": ["linux"]},
 "networkAccess": "NONE",
 "timeoutSeconds": 60,
 "output": {"captureStdout": true, "captureStderr": true, "maxOutputBytes": 65536}}
```

`cwdRepositoryTarget` must name a real repository ID **for a COMMAND node** (resolved against that node run's
own effective scope at execution time — a repository not in scope fails admission); for a **MACHINE_GATE**'s
own command, this field is required by the schema but never actually resolved (a `MACHINE_GATE` always runs
its command against a fresh scratch directory, never a repository checkout) — any placeholder string is
correct there, verified in [01-quickstart.md](01-quickstart.md)'s own real run.

## GATE — real, verified shape

```json
{"commandRef": {"kind": "COMMAND", "definitionId": "<id>", "versionId": "<versionId>"},
 "criteria": [{"name": "my-criterion", "evidenceKey": "MY_EVIDENCE_KEY"}]}
```

The referenced command's own stdout is expected to be JSON matching `{"<evidenceKey>": {"verdict":
"PASS"|"FAIL"|"ERROR"}}` — confirmed via the real gate evaluator output format `aw artifact get` returned in
[01-quickstart.md](01-quickstart.md)'s own walkthrough.

## WORKFLOW — real, verified shape

```json
{"schemaVersion": "1",
 "nodes": [
   {"key": "start", "type": "START", "outcomes": ["next"]},
   {"key": "gate", "type": "MACHINE_GATE", "outcomes": ["passed"],
    "machineGate": {"gateRef": {"kind": "GATE", "definitionId": "...", "versionId": "..."},
                    "policyRefs": [{"kind": "POLICY", "definitionId": "...", "versionId": "..."}]}},
   {"key": "end", "type": "END"}
 ],
 "edges": [
   {"key": "start-gate", "from": "start", "outcome": "next", "to": "gate"},
   {"key": "gate-end", "from": "gate", "outcome": "passed", "to": "end"}
 ],
 "completionPolicyRef": {"kind": "POLICY", "definitionId": "...", "versionId": "..."}}
```

Node types (closed set): `START`, `END`, `AGENT`, `COMMAND`, `MACHINE_GATE`, `APPROVAL`, `WAIT`, `ROUTER`,
`FORK`, `JOIN` (`internal/domain/workflow/workflow.go`). Every graph needs exactly the edges naming which
`outcome` of which node leads to which next node — an outcome with no matching edge is a real publish-time
validation error, not a runtime surprise.

**`MACHINE_GATE` is the one node type usable unmodified regardless of which repository a WorkItem is scoped
to** (it never resolves `cwdRepositoryTarget`, always runs against a fresh scratch directory) — this is why
[01-quickstart.md](01-quickstart.md)'s own minimal, provider-free example uses it. An `AGENT` node needs a
real registered provider (see [05-providers-and-isolation.md](05-providers-and-isolation.md)) and an
`AGENT_PROFILE` definition; a `COMMAND` node needs the target repository actually in the running WorkItem's own
effective scope. Both are real, working node types — this documentation's own real verification pass covered
`MACHINE_GATE` end to end; the AGENT/COMMAND node shapes below are transcribed from this repo's own passing
`internal/integration/v6accept` acceptance suite (a real, CI-proven multi-node graph), not independently
re-verified while writing this page:

```json
{"key": "maker", "type": "AGENT", "outcomes": ["done"],
 "agent": {"profileRef": {"kind": "AGENT_PROFILE", "definitionId": "...", "versionId": "..."},
           "policyRefs": [...], "adapterBuildId": "<adapter-build-id>", "role": "MAKER"}}
```

```json
{"key": "test_a", "type": "COMMAND", "outcomes": ["passed"],
 "command": {"commandRef": {"kind": "COMMAND", "definitionId": "...", "versionId": "..."}, "policyRefs": [...]}}
```

## Sending a failed check back to the maker — `failureOutcome` (V9-02, ADR-031)

By default a `COMMAND` that exits non-zero, or a `MACHINE_GATE` whose overall verdict is `FAIL`, fails its attempt
and — after the ATTEMPT policy's retries — the node and the run. To draw "the check failed, go back to the maker"
declare an optional `failureOutcome` on the `command` / `machineGate` config: the one of the node's own outcomes a
**functional** failure routes to, so the node finishes `SUCCEEDED` with that outcome (no retry — the result is
deterministic) and the edge leaving it can lead back to the maker. A loop needs a bound as for any other cycle:
put a `cyclePolicy` on a node of the loop (the maker, or the check itself) whose `escalationOutcome` edge leaves it.

```json
{"key": "build", "type": "AGENT", "outcomes": ["done", "escalated"],
 "cyclePolicy": {"maxIterations": 2, "escalationOutcome": "escalated"},
 "agent": {"profileRef": {...}, "role": "MAKER", "adapterBuildId": "...", "policyRefs": [...]}},
{"key": "test", "type": "COMMAND", "outcomes": ["passed", "failed"],
 "command": {"commandRef": {...}, "policyRefs": [...], "failureOutcome": "failed"}}
```
```json
{"key": "build-test", "from": "build", "outcome": "done", "to": "test"},
{"key": "test-end", "from": "test", "outcome": "passed", "to": "end"},
{"key": "test-build", "from": "test", "outcome": "failed", "to": "build"},
{"key": "build-escalated", "from": "build", "outcome": "escalated", "to": "needs-human"}
```

Publishing rejects `failureOutcome` unless the outcomes the check itself can select — every declared outcome except
the node's own `cyclePolicy.escalationOutcome` — are **exactly two**: the `failureOutcome` and one success outcome.
A loop with no `cyclePolicy` is rejected like any unbounded cycle. A node that does not declare `failureOutcome`
behaves exactly as before, and its published workflow keeps its hash.

Only a **functional** failure takes that route:

| Result | Without `failureOutcome` | With `failureOutcome` |
|---|---|---|
| `COMMAND` exits 0, output not cut; gate verdict `PASS` | success outcome | success outcome |
| `COMMAND` exits on its own with a non-zero code (even if its output was cut); gate verdict `FAIL` | attempt `FAILED`, retry, NodeRun `FAILED` | NodeRun `SUCCEEDED` with the `failureOutcome`; evidence verdict `FAILED` / `FAIL` |
| timeout, killed, cannot be spawned, exit 0 with cut output, `SCOPE_VIOLATION`, lost lease; gate verdict `ERROR` / `NOT_RUN` | attempt `FAILED` / retry / `INDETERMINATE` | the same — never the `failureOutcome` |

A `COMMAND` always leaves a `COMMAND_EXECUTION` evidence row once its process has finished on its own, whatever the
exit code and whether or not it declares a `failureOutcome`: the artifact holds the argv, the working directory, the
exit code, the duration, a `truncated` flag and the redacted stdout/stderr (only the streams the command's `output`
contract captures, bounded by `maxOutputBytes`), so set `captureStderr` on a check whose failure the maker should be
able to read. A check whose **latest** activation failed does not satisfy a completion policy (`FAILED` is not a
passing verdict), so `fail → fix → pass` completes and a run that ends on a failed check does not.

A run can still end `FAILED` — a check without a `failureOutcome`, a technical error, an exhausted retry budget. The
WorkItem is then `BLOCKED` by a `RUN_FAILED` blocker rather than stuck `ACTIVE`: after you resolve it
(`aw blocker resolve --mode RESOLVED`, never `WAIVED`) the **same** WorkItem runs again with the same pinned workflow
version, and only the new run's evidence counts toward completion — see
[09-troubleshooting.md](09-troubleshooting.md#a-run-ended-failed-the-workitem-is-blocked-by-a-run_failed-blocker-v9-06-adr-033).

When the maker runs again through that edge, its context snapshot carries the evidence of the failing attempt and its
prompt gets a `checkFailures` section: `what` failed, `why` (the tail of stderr, or stdout if stderr was empty, for a
command; the criteria that did not pass for a gate — bounded to 4 KiB) and `fix`. Only the latest failure is shown.

## What an agent receives — the instruction artifact (V9-03, ADR-032)

An `AGENT` node's prompt is one JSON document, written to the provider's stdin and pinned as the attempt's
instruction artifact (the same bytes, so the same context snapshot always gives the same artifact and hash). Since
V9-03 it has this shape (**schema v2**, keys in exactly this order):

```json
{"schemaVersion": 2,
 "hardConstraints": [{"ownerVersionId": "...", "resourceKey": "...", "priority": "HARD_CONSTRAINT", "contentHash": "...", "content": "..."}],
 "taskContract": {"workItemId": "...", "title": "...", "behavior": "...", "acceptanceCriteria": ["..."],
                  "verificationSpec": "...", "riskLevel": "HIGH",
                  "allowedOutcomes": ["approved", "rework"], "outcomeProtocol": "..."},
 "checkFailures": [{"checkNode": "...", "evidenceIds": ["..."], "what": "...", "why": "...", "fix": "..."}],
 "resources": [{"ownerVersionId": "...", "resourceKey": "...", "priority": "GUIDANCE", "contentHash": "...", "content": "..."}],
 "messages": [{"messageId": "...", "role": "USER", "content": "..."}],
 "omittedMessages": [{"messageId": "...", "sequence": 3, "actor": "...", "role": "TOOL", "createdAt": "...", "reason": "BUDGET_EXCEEDED"}],
 "closingChecklist": {"hardConstraintKeys": ["..."], "allowedOutcomes": ["approved", "rework"]}}
```

- **`hardConstraints`** are the resources whose `priority` is `HARD_CONSTRAINT`, first in the document, and
  **`closingChecklist.hardConstraintKeys`** repeats their `resourceKey`s last, together with the allowed outcomes — the
  start and the end of a long prompt are what an agent attends to. Within the list they keep the order the context
  resolver pinned them in (by `resourceKey`, then owner version, then content hash).
- **`resources`** holds every other resource, each with its `priority`: `REQUIRED_PROCEDURE` first, then `GUIDANCE`,
  then `REFERENCE`, in the pinned order inside each priority. The priority comes from the Skill/Layer resource itself;
  nothing else to configure.
- **`riskLevel`** is the WorkItem contract's `riskLevel`, verbatim.
- **`allowedOutcomes`** are the outcomes the node declares **except** its `cyclePolicy.escalationOutcome` (the engine
  assigns that one itself when the loop budget is spent; an agent never selects it), in the order the workflow
  declares them. You no longer copy this list into a Skill.
- **`outcomeProtocol`** is present only when the node has **more than one** allowed outcome; with exactly one, the
  engine derives the outcome and asks for no marker. It is the same engine-owned text for every node: end the final
  message with exactly one `<agentkit-outcome>{"schemaVersion":1,"outcome":"NAME"}</agentkit-outcome>` as the very
  last thing written, `NAME` spelled as listed. The engine enforces it: a missing marker (when there is a
  choice), a second marker in an earlier message, a malformed one, or an outcome outside the list fails the attempt
  (all four are `OUTCOME_REJECTED`; see [09-troubleshooting.md](09-troubleshooting.md)). There are no per-outcome
  descriptions yet; the list is the names only.
- **`checkFailures`** is only present for a maker sent back by a failing check (see the section above), right after
  the task contract.
- **`omittedMessages`** is only present when the node's context policy declares a `messages` budget that left some
  messages out; it follows `messages` (see below).

**Which schema an attempt gets** is recorded on its context snapshot (`instructionSchemaVersion`, set when the
attempt is scheduled), not decided by the binary that happens to assemble it. Attempts scheduled before the upgrade
that introduced this have no recorded version and keep the **v1** shape (`taskContract`, `checkFailures` when
present, `messages`, `resources` — no priorities, no outcomes) byte for byte, and so do their retries and recovery
attempts, which clone the snapshot with its version. Every attempt scheduled afterwards gets v2. Schema v2 is encoded
without HTML escaping, so `<` and `&` appear as written, not as `<` / `&`.

## Keeping the prompt bounded across rework rounds (V9-07)

Every round of a rework loop appends to the WorkItem's task chat (a failure log, a review, a human reply), and
without a limit every later prompt carries all of it, so the prompt grows round after round. A CONTEXT policy can
declare a budget for the chat:

```json
{"category": "CONTEXT", "context": {"selector": ["..."], "budget": {"maxTokens": 65536},
                                    "messages": {"maxBytes": 32768, "keepLatest": 2}}}
```

When an attempt is scheduled, the node's context policy decides which messages go into its prompt **in full**:

1. the newest `keepLatest` messages and every **pinned** message are always included, whatever their size;
2. the remaining capacity is `maxBytes` minus what those cost (or zero), spent on the other messages **newest
   first** — a message that does not fit is skipped, and a smaller older one may still fit;
3. every message that is not included becomes an entry in the prompt's `omittedMessages`: its `messageId`,
   `sequence`, `actor`, `role` and `createdAt`, plus the `reason` (`BUDGET_EXCEEDED`) — never its content.

So the message content in a prompt never exceeds the larger of `maxBytes` and the size of the always-kept messages,
however many rounds have run, and the newest message (for a rework round, the failure it is meant to fix) is always
there. `maxBytes` counts the stored message content in bytes, the same unit as `budget.maxTokens` for resources;
`keepLatest` must be at least 1 and `maxBytes` positive, or publishing the policy fails.

The decision is recorded on the attempt's context snapshot (`omittedMessageRefs`, shown by
`aw message context-snapshot` and the context-snapshot routes) and takes part in its manifest hash, so "what did this
attempt not see, and why" is answerable after the fact, and the same snapshot always renders the same prompt. A
technical retry or recovery of the attempt keeps the same included and omitted messages.

A policy without a `messages` block behaves exactly as before: every message of the WorkItem is in every prompt, and
snapshots already stored are unaffected.

**Pinning a message** keeps it in every later prompt however old it is — use it for the requirement or the
constraint that must not scroll away:

```bash
aw message append <workItemId> --project-id <p> --pinned --file requirement.txt
```

The HTTP route takes `"pinned": true` in the `appendMessage` body. A message is pinned when it is appended and never
changes afterwards (the chat is append-only); pinned messages count toward the budget, so pinning many large messages
leaves less room for the rest.

## Setting up a repository before an agent writes — readiness profile and baseline (V9-08)

Initialization is a phase of its own with evidence, not something an agent discovers by failing. A repository
can declare a **readiness profile**: an optional `setup` command (install dependencies) and a `verification`
command (the repository's own checks). `aw` runs them on a workspace **before any task changes it** — that run
is the **baseline** — and a work item that may **write** to the repository is admitted only when the baseline
passed.

```bash
# declare (or change) the profile; commands are an executable plus argv, never a shell string
cat > profile.json <<'EOF'
{"setup": {"executable": "npm", "argv": ["ci"], "timeoutSeconds": 600},
 "verification": {"executable": "npm", "argv": ["test"], "timeoutSeconds": 600}}
EOF
aw repository readiness set --file profile.json <repositoryId>

aw repository readiness show <repositoryId>       # the profile and, per workspace, where its baseline stands
aw repository readiness verify <repositoryId>     # run the baseline again (after fixing the repository)
aw repository readiness accept-exception --attempt-id <attemptId> --reason "<why>" <repositoryId>
```

The same operations exist over HTTP (`repositoriesReadiness`, `repositoriesReadinessProfileSet`,
`repositoriesReadinessVerify`, `repositoriesReadinessAcceptException`) and in the UI (Projects → a repository →
**Readiness**), with the same behavior.

**When the baseline runs.** On every `READY` workspace of the repository when the profile is set or changed, on a
workspace as soon as it becomes `READY` (a new work item's `WorkspaceSet`), and whenever you ask with `verify`. It
runs by a worker (`aw worker`), so `show` reports `PENDING` until a worker has run it. A repository **without** a
profile is not gated at all: nothing changes for it.

**Baseline state** (per workspace, in `show`):

| State | Meaning | Admits a writer |
|---|---|---|
| `NOT_REQUIRED` | the repository has no profile | yes |
| `PENDING` | the profile has no baseline yet for its **current version**, or the workspace is not `READY` | no |
| `PASS` | the latest baseline for the current profile version passed | yes |
| `FAIL` | the latest baseline did not pass and nobody accepted it | no |
| `EXCEPTION_ACCEPTED` | the latest baseline failed and an operator accepted that failure | yes |

A baseline only vouches for the profile version it ran, so **changing the profile makes every earlier result
stale** (back to `PENDING` until the new baseline runs).

**A failed baseline is classified, and it is not the task's fault.** `failureKind` says which:
`PRE_EXISTING_FAILURE` — the verification (or setup) command ran and exited non-zero, so the repository's own
checks were already red; `ENVIRONMENT_ERROR` — the command could not be observed to run (missing executable,
timeout, cancelled). Either way it describes the repository or its environment **before** any task touched it.

**What it gates.** A work item that may `WRITE` to a repository with a profile cannot become `READY`
(`aw work-item mark-ready`; `aw work-item readiness` lists the same problems) and a run cannot start for it
(`aw run start` → `CONFLICT`) unless the baseline is `PASS` or `EXCEPTION_ACCEPTED`. Read-only access is never
gated. For a root work item the repository scope of its task family is what counts; a child work item uses its
own effective scope.

**Accepting an exception.** `accept-exception` admits writers although the baseline failed. It needs a reason, is
attributed to you (the configured principal) and the time, covers **that failed attempt only** — a later failing
baseline, or a baseline of a changed profile, needs its own acceptance — and leaves the failed attempt untouched on
the record. Only a failed attempt of the repository's current profile version can be accepted.

**Telling a regression from a failure that was already there.** When a check after an agent fails and the workflow
sends the maker back (`failureOutcome`, above), each entry of `checkFailures` carries a `baseline` line when a
write repository has a baseline: either *its baseline passed before this task started, so the failure comes from
the changes made during this task*, or *its baseline had already failed* (and whether an operator accepted it), *so
a failure that matches the baseline is not caused by this task*. The line is derived from the baseline as of the
moment the maker's context was pinned, so the same snapshot always renders the same prompt.

## Keeping knowledge honest — warnings at publish, and instruction files (V9-10)

Two ways knowledge goes wrong without anyone deciding it should: a rule nobody has checked in a year keeps being
handed to agents, and a long entry file the provider CLI loads by itself swamps the prompt. Both now leave a trace.
Neither blocks anything — they are warnings and an audit record.

**Warnings when you publish.** `aw definition publish` (and `POST .../publish`) returns a `warnings` list next to
the version it created, when there is something to say:

- a resource whose provenance `lastVerified` is older than the configured age (default **180 days**):
  `skill version V: resource "R" was last verified 400 days ago (2025-09-01), older than the 180 days ...` —
  re-check the rule and publish it again with a new `lastVerified`. A resource with only a `revision` is never
  flagged (nothing dates it);
- too many `HARD_CONSTRAINT` resources for an agent to hold (default **15**): in one Skill/Layer version, and across
  the resources one **CONTEXT policy** (the route an AgentProfile pins) delivers to an agent. Keep what must never
  be broken as `HARD_CONSTRAINT` and make the rest `REQUIRED_PROCEDURE` or `GUIDANCE`.

```bash
aw definition publish --kind SKILL --file skill.json --warn-resource-age-days 90 --warn-hard-constraints 10 <id>
aw serve ... --warn-resource-age-days 90 --warn-hard-constraints 10     # the default for the HTTP API and the UI
```

`0` takes the default, a negative number turns that warning off. The warnings are computed when the version is
published; replaying the same publish (same idempotency key) returns the version without them.

**Instruction files the provider CLI loads by itself.** Claude Code reads `CLAUDE.md` and Codex reads `AGENTS.md`
from the worktree they run in. That text reaches the agent outside the instruction artifact above, so no budget
counts it. When a node is scheduled, the files its provider declares are looked up in the worktrees of the
repositories the work item may touch (read-only access included) and **pinned in the attempt's context snapshot**:
repository, file name, SHA-256 and size — never the content — plus the oversize limit in force (default
**16384 bytes**, `aw worker --instruction-file-warn-bytes`). `aw context-snapshot show` (and `aw message context-snapshot`) and the snapshot routes
show them under `repositoryInstructionFiles`; a file over the limit has `oversized: true` and a `warning` that says
so. Only a regular file in the worktree root counts (a symlink is not followed). A snapshot of an attempt that had
no such file, or whose provider declares none, is unchanged and hashes as before.

Both are advisory: a file that cannot be read, or a worktree that cannot be resolved, simply contributes nothing,
and the oversize flag changes nothing about how the attempt runs. Shorten the file, or move the detail into a Skill
or Layer resource where it is prioritized, budgeted and versioned.

## Routing knowledge to a code area — resource selectors (V9-04)

A Skill or Layer resource says *when* it applies with its `selector` (a resource with an empty selector must be
`"global": true`). One agent profile and one context route can therefore serve every area of a repository: pin all the
resources in the route, tag each one for its area, and the engine picks the right ones for each attempt. You do **not**
need an agent profile (or a context route) per area. (The document below is a Layer; a Skill resource has the same
fields with `instruction` in place of `convention`.)

```json
{"resources": [
  {"key": "backend-layout", "convention": "...", "priority": "GUIDANCE",
   "selector": {"pathTags": ["backend"]}, "provenance": {"owner": "...", "source": "...", "revision": "v1"}},
  {"key": "api-rules", "convention": "...", "priority": "HARD_CONSTRAINT",
   "selector": {"componentTags": ["backend"]}, "provenance": {"owner": "...", "source": "...", "revision": "v1"}},
  {"key": "review-checklist", "convention": "...", "priority": "GUIDANCE",
   "selector": {"blockKinds": ["CHECKER"]}, "provenance": {"owner": "...", "source": "...", "revision": "v1"}},
  {"key": "shared", "convention": "...", "priority": "GUIDANCE", "global": true, "selector": {},
   "provenance": {"owner": "...", "source": "...", "revision": "v1"}}]}
```

**How a selector is evaluated.** When the engine schedules an `AGENT` node it resolves the node's context route against
the facts of that attempt. A resource applies when **every dimension its selector declares** matches (AND across
`componentTags`, `pathTags`, `taskKinds`, `blockKinds`, `riskClasses`), and a dimension matches when **any one** of its
values does (OR within a dimension). A dimension the selector leaves out puts no restriction on it. A resource that does
not apply is not loaded and is recorded as `NOT_APPLICABLE`. These are the facts, per dimension:

| Dimension | Matched against | Match |
|---|---|---|
| `componentTags` | The **names** of the project's Components that the WorkItem touches (below) | exact |
| `pathTags` | The path scopes of the WorkItem's effective scope (below) | path overlap |
| `blockKinds` | The node's role: `MAKER` or `CHECKER` (the `role` of the `AGENT` node; `MAKER` when unset) | exact |
| `taskKinds` | The WorkItem's kind: `ROOT` or `CHILD` | exact |
| `riskClasses` | The WorkItem contract's `riskLevel`, verbatim | exact |

`blockKinds` accepts any string and publishing does not reject a value other than `MAKER` or `CHECKER`, but only those
two ever occur: only `AGENT` nodes resolve a context route, so a resource tagged for any other value is never loaded.

**`componentTags` — Components.** A Component is a named directory of a repository; the repository probe creates one per
top-level directory when you register a repository (`Name` and `Path` are both the directory name, hidden directories
are skipped; list them with `aw component list <projectId>`). Write the **name** in `componentTags`, not an ID. A Component counts as touched when
its repository is in the WorkItem's effective scope (a `READ` entry counts as much as a `WRITE` one) **and** its path
overlaps a path scope of that repository's entry. A scope entry with **no** `pathScopes` covers the whole repository, so it
touches every Component of that repository.

**`pathTags` — paths.** A path tag is a relative directory path, written like a path scope (`backend`, `services/api`;
`\` is read as `/`, `./backend/` as `backend`). It matches when the WorkItem's effective scope has an entry with no
`pathScopes` (the whole repository contains every path), **or** the tag *overlaps* one of the scope's path scopes: they
are equal, or one is a directory ancestor of the other, compared by whole path segments.

| WorkItem path scope | `pathTags: ["backend"]` | `pathTags: ["backend/src/db"]` | `pathTags: ["services/api"]` |
|---|---|---|---|
| `backend` | applies | applies (the task may touch it) | no |
| `backend/src` | applies | applies | no |
| `services/apix` | no | no | **no** (`apix` is not `api`) |
| none (whole repository) | applies | applies | applies |

A path tag is a directory prefix, not a glob: `backend/**` matches nothing but a scope that literally contains such a
path. A tag that is absolute, contains `..`, or is empty or `.` never matches, not even for a whole-repository scope.
`pathTags` do not name a repository: with a WorkItem scoped to `backend` in two repositories, a `backend` tag applies
to both; use `componentTags` (Component names are per repository) when that matters. A WorkItem with no effective
scope at all receives no path- or component-tagged resource.

**When it is decided.** The scope is read when the node run is scheduled and the result is pinned in that attempt's
context snapshot. A node run scheduled after an approved scope expansion therefore sees the expanded scope; a retry or
recovery attempt reuses the snapshot and so the resources it already pinned.

**Seeing why a resource was or was not loaded.** The `CONTEXT_RESOLUTION_V1` decision artifact
(`<nodeRunId>-context-resolution-v1` in the `decision_artifacts` table) records, per attempt, every resource with
`SELECTED` / `NOT_APPLICABLE` / `BUDGET_EXCEEDED` as its result, and, as its input, the facts the route was resolved
against, in this key order (sets are sorted and never `null`):

```json
{"componentTags":["backend"],"pathTags":["backend"],"wholeRepositoryScope":false,"blockKind":"MAKER","taskKind":"CHILD","riskClass":""}
```

`wholeRepositoryScope` is `true` when at least one scope entry has no `pathScopes`. If a resource you expected is
missing, compare its selector with this input first. `aw context-snapshot show <workItemId> <snapshotId> --project-id
<projectId>` lists what the attempt actually pinned.

## AGENT_PROFILE — real shape (from the same proven fixture)

```json
{"providerKey": "claude", "model": "your-model-name", "toolRefs": ["read_file"],
 "contextPolicyRef": {"kind": "POLICY", "definitionId": "...", "versionId": "..."},
 "compatibility": {"os": ["linux"]}, "budget": {"maxTokens": 4096}}
```

### `envAllowlist` — which environment variables the agent process asks for (V9-05)

An optional list of variable **names** (never values) the agent's provider process asks to inherit from its
worker. Without it the agent starts with an empty environment, as before:

```json
{"providerKey": "claude", "model": "your-model-name", "envAllowlist": ["HOME", "PATH"],
 "contextPolicyRef": {"kind": "POLICY", "definitionId": "...", "versionId": "..."},
 "compatibility": {"os": ["linux"]}, "budget": {"maxTokens": 4096}}
```

It is a **request, not a grant**: the agent inherits only the names that are in this list AND in the
operator's `aw worker --env-allowlist`, matched exactly and case-sensitively (write `PATH`, not `Path`, on
Windows too). A name the operator did not allow is silently not passed; the node's execution profile
(`agentInheritedEnvironment`) shows what was. This is what replaces a wrapper script that hard-coded `HOME`/`PATH` around the provider
CLI — see [05-providers-and-isolation.md](05-providers-and-isolation.md#env-allowlist) and
[02-configuration.md](02-configuration.md).

Publish rules: each entry must be non-empty, contain no `=` (a `NAME=value` pair would put a value into a
definition), no NUL character and no whitespace, and appear once; the list is a set, so order does not matter
(it is stored sorted) and changing it publishes a new version with a new hash. A profile that omits the field,
or lists none, keeps the exact hash it had before the field existed.

## BLOCK, LAYER, ENGINEERING_PACK

These compose already-published definitions into reusable authoring units (a Block groups nodes/policies a
workflow can pull in wholesale; a Layer/EngineeringPack groups skills/policies for a whole team or repository
convention). Neither was exercised by this documentation's own real verification pass — consult
`docs/design/04-v2-definition-plane.md` and `internal/domain/{block,layer,engineeringpack}` for their exact
schemas before authoring one, and treat this section as a pointer, not a verified reference, until a future
pass exercises them end to end. A Layer resource carries the same `selector` as a Skill resource, evaluated by the same
rules ([Routing knowledge to a code area](#routing-knowledge-to-a-code-area--resource-selectors-v9-04)).

## `aw definition list` / `show` / `versions`, `aw version show` / `aw version diff`

Read-only queries over everything published so far:

```bash
aw definition list [--project-id <id>]                  # every definition, any kind
aw definition show <definitionId>                        # one definition's own metadata
aw definition versions <definitionId>                     # every published version, newest first
aw version show <versionId> [--project-id <id>]            # one version's full compiled document
aw version diff <versionIdA> <versionIdB> [--project-id <id>]  # field-level diff between two versions
```
