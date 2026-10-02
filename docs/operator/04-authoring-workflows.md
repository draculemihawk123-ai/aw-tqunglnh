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
```

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
outcomes an agent may report (see the same section).

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

**Which schema an attempt gets** is recorded on its context snapshot (`instructionSchemaVersion`, set when the
attempt is scheduled), not decided by the binary that happens to assemble it. Attempts scheduled before the upgrade
that introduced this have no recorded version and keep the **v1** shape (`taskContract`, `checkFailures` when
present, `messages`, `resources` — no priorities, no outcomes) byte for byte, and so do their retries and recovery
attempts, which clone the snapshot with its version. Every attempt scheduled afterwards gets v2. Schema v2 is encoded
without HTML escaping, so `<` and `&` appear as written, not as `<` / `&`.

## AGENT_PROFILE — real shape (from the same proven fixture)

```json
{"providerKey": "claude", "model": "your-model-name", "toolRefs": ["read_file"],
 "contextPolicyRef": {"kind": "POLICY", "definitionId": "...", "versionId": "..."},
 "compatibility": {"os": ["linux"]}, "budget": {"maxTokens": 4096}}
```

## BLOCK, LAYER, ENGINEERING_PACK

These compose already-published definitions into reusable authoring units (a Block groups nodes/policies a
workflow can pull in wholesale; a Layer/EngineeringPack groups skills/policies for a whole team or repository
convention). Neither was exercised by this documentation's own real verification pass — consult
`docs/design/04-v2-definition-plane.md` and `internal/domain/{block,layer,engineeringpack}` for their exact
schemas before authoring one, and treat this section as a pointer, not a verified reference, until a future
pass exercises them end to end.

## `aw definition list` / `show` / `versions`, `aw version show` / `aw version diff`

Read-only queries over everything published so far:

```bash
aw definition list [--project-id <id>]                  # every definition, any kind
aw definition show <definitionId>                        # one definition's own metadata
aw definition versions <definitionId>                     # every published version, newest first
aw version show <versionId> [--project-id <id>]            # one version's full compiled document
aw version diff <versionIdA> <versionIdB> [--project-id <id>]  # field-level diff between two versions
```
