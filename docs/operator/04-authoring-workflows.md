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
