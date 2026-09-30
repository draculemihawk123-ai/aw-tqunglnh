# Evidence and retention

## Evidence — what it is, real shape

Every gate/command/agent execution this installation runs produces one or more Evidence rows — a durable,
queryable record of what happened, chained to the exact repository revision(s) involved:

```bash
aw evidence list --project-id <id> <workItemId> [--run-id <id>] [--kind <evidenceKind>]
aw artifact get <workItemId> <evidenceId> <artifactId> --project-id <id> --output <path|->
```

A real evidence entry (verified in [01-quickstart.md](01-quickstart.md)'s own successful run):

```json
{"evidenceId": "<attemptId>:<evidenceKind>", "projectId": "...", "workItemId": "...", "runId": "...",
 "nodeRunId": "...", "attemptId": "...", "kind": "QUICKSTART_OUTPUT_VERIFIED", "verdict": "PASS",
 "artifactReferences": ["<artifactId>"],
 "revisions": [{"repositoryId": "...", "vcsObjectId": "<commit>", "workspaceGeneration": 1}],
 "revisionSetHash": "sha256:...", "policyVersion": "sha256:...", "createdAt": "..."}
```

Every evidence row's `revisions` field names the EXACT repository commit(s) the evidence was produced against
— the chain AK-ARCH-021 requires (WorkItem → Run → NodeRun → Attempt → invocation → artifact → exact
repository revision) is real, queryable, and end to end: nothing in this chain is ever a "trust me" summary.

A `MACHINE_GATE`'s own real output artifact is a `application/vnd.agentkit.gate-result+json` document — the
real content this quickstart fetched was `{"overallVerdict":"PASS","criteria":[{"name":"...", "evidenceKey":
"...", "verdict":"PASS"}]}` (or `"ERROR"` with a real `"detail"` string explaining what went wrong, e.g. the
"%1 is not a valid Win32 application" error the quickstart's own troubleshooting section walks through).

## Retention classes

Two, closed (`internal/domain/artifact/artifact.go`):

- **`CANONICAL_CONTEXT`** — never expires. Canonical conversation/context artifacts, and (as of Alpha) every
  artifact this codebase's real producers (`internal/app/message`, every `internal/app/runtime` node executor)
  actually create.
- **`RAW_OUTPUT_TEMP`** — a 7-day TTL from creation. Raw provider/command output not meant to live forever.
  **As of this Alpha, no real code path in this codebase actually constructs one of these** — the retention
  class, its 7-day TTL math, and the sweep worker that would clean it up are all real and exhaustively tested,
  but there is currently no real producer wired to it. Don't be surprised if you never see one in a real
  installation yet.

## The retention sweep

A periodic worker job (`aw worker --sweep-interval`, default 1 hour) that purges eligible `RAW_OUTPUT_TEMP`
artifacts — an artifact is eligible only when past its 7-day grace AND not `Attached` AND not `Hold`ed AND not
sharing its content-addressed locator with any artifact that IS attached/held. A sweep never touches
`CANONICAL_CONTEXT`, never deletes a referenced or held artifact, and never removes the DATABASE ROW even for a
purged artifact — only its real bytes (an audit trail is kept forever; `ADR-017`).

```bash
aw settings update ...   # --sweep-interval is a serve/worker startup flag, not a runtime-mutable safe-setting field
```

## Redaction

A secret value that ever passes through a real command/agent's own output (stdout/stderr, or a stored
artifact) is redacted before it is ever persisted — verified by this repo's own real end-to-end secret-scan
test (`internal/integration/v5accept`): a real secret is walked for across every real artifact-store object AND
the raw on-disk SQLite file itself, confirming zero unredacted occurrences anywhere durable.
