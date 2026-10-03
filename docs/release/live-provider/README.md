# Live provider verification — V9-11

Evidence that the V9 changes work with a **real Claude CLI**, not only with the repository's `fake-claude`.
The scenario is `TestV9LiveClaude_MakerCheckFailReworkCheckerApprovesWithoutAWrapper`
(`internal/integration/v5accept/live_claude_test.go`). It is skipped unless `AW_LIVE_CLAUDE=1`: a run spends real
money and needs a logged-in `claude`. CI never runs it.

```bash
AW_LIVE_CLAUDE=1 AW_LIVE_CLAUDE_OUT=docs/release/live-provider/run-N \
  go test ./internal/integration/v5accept/ -run TestV9LiveClaude -v -count=1 -timeout 30m
```

Optional: `AW_LIVE_CLAUDE_EXECUTABLE`, `AW_LIVE_CLAUDE_MODEL` (default `claude-sonnet-4-6`), `AW_LIVE_CLAUDE_EFFORT`
(default `medium`), `AW_LIVE_CLAUDE_MAX_USD` (a ceiling for ONE attempt, default 0.75).

## What ran

| | |
|---|---|
| Provider | Claude Code `2.1.288`, `claude.exe` on Windows, started by `aw`'s own `claude.Adapter` |
| Model / effort | `claude-sonnet-4-6`, `--effort medium` ("effort 2" of `low, medium, high, xhigh, max`) |
| Stack | real git worktrees, real sqlite store, real worker pool, real `COMMAND` process; only the provider is the point |
| Workflow | `build` (MAKER) → `check` (COMMAND, `failureOutcome`) → `review` (CHECKER, outcomes `approved`/`rework`) → `end`; `check --failed--> build`, `review --rework--> build` |
| Task | "write the capital of France as the first line of `notes.txt`". The check also demands a line `VERIFIED-BY-CHECK` that the task text does not mention, so the first build cannot pass and the run has to go round |
| Knowledge | one Layer, two resources tagged `blockKinds` MAKER / CHECKER (V9-04) |
| Environment | the `AgentProfile`'s `envAllowlist` and the worker's `--env-allowlist` name the same variables (V9-05); no wrapper script |

## Runs

| Directory | What it shows |
|---|---|
| [`run-1-write-denied`](run-1-write-denied/) | Every `Write`/`Edit` of the maker was **denied**. Finding F1. The run still escalated cleanly after the loop budget was spent. |
| [`run-2-pass`](run-2-pass/) | The run that passes the scenario's assertions: build, check ✗, build, check ✗, build, check ✓, review `approved`, end (3 builds, 2 rework rounds, 97 s). `summary.json` has the exact sequence, attempt and evidence ids; `prompts/` and `transcripts/` the rest. Also shows finding F2. |

Two more runs of the same workload (not kept, same shape) needed **3** rework rounds instead of 2: how many rounds the
model needs varies (2–3 in the three runs of the final workload), which is why the scenario asserts the shape of the run and not a fixed count. Run ids are the
fixture's sequential ids (`v5a-7` in every run, each run has its own temporary database); an attempt is identified by
its id in `summary.json` and by the file names under `prompts/` and `transcripts/` (`<node>-<activation>`).

Each bundle holds: `summary.json` (sequence of node runs and attempts, outcomes, evidence ids, findings), `prompts/`
(the instruction artifact each agent attempt received, exactly as handed to the CLI), `transcripts/` (the canonical
agent events `aw` recorded for each attempt), and the final `notes.txt`. Local paths are replaced by `<local-path>`.

## What the real model confirmed

| Claim | Evidence in `run-2-pass` |
|---|---|
| V9-01 — a checker runs after a maker in the same run | `review` is activation 10 after the `build`s, SUCCEEDED, no `SCOPE_VIOLATION` |
| V9-02 — a failing check sends the run back and the maker is told why | `check` outcome `failed` ×N then `passed`; each later build's snapshot pins the failing check's evidence and its prompt carries `checkFailures` quoting the check's message; the maker's transcript cites it |
| V9-03 — the agent picks an outcome from `allowedOutcomes` | the `review` prompt lists `["approved","rework"]` and the marker protocol; the real model ended with `<agentkit-outcome>` `approved` |
| V9-04 — a resource reaches only the node it is for | the build prompts carry `live-maker-role` and not the reviewer's; the review prompt the opposite |
| V9-05 — the agent process gets its environment without a wrapper | the pinned execution profile records the variable **names**; `claude.exe` ran with only those |
| Adapter, admission and drift check against a real binary | the `AdapterBuild` pins the hash of the real `claude.exe` and a live `--version` probe; every admission re-probed it |
| The canonical event stream from the real `stream-json` | `EXECUTION_STARTED`, `ASSISTANT_MESSAGE`, `TOOL_CALL_STARTED/FINISHED`, `EXECUTION_FINISHED` for every attempt, no protocol error |

## Fake CLI vs real CLI

The fake (`cmd/fake-claude`) is deterministic and never reads its prompt, so every V9 scenario with it proves that
`aw` delivers things; only a real model shows whether they are usable.

| | `fake-claude` (`v5accept` V9 scenarios) | Claude CLI, `claude-sonnet-4-6` |
|---|---|---|
| Reads the prompt | no | yes |
| Rework loop (build → check → build) | passes: the check is fail-once-then-pass by a marker file | passes, but only after the model acts on the check's message; needed 2–3 rework rounds |
| Outcome selection (V9-03) | chosen by an environment variable | chosen by the model from the prompt, in the marker protocol |
| Checker | "reads" the maker's diff because the fake writes through the same mount | **cannot see the repository** (F2) |
| File writes in the worktree | always | denied unless the CLI is told to accept edits (F1) |
| Environment | exactly the declared names | exactly the declared names |
| One attempt | milliseconds | 7–35 s |
| Cost | none | about 0.05–0.25 USD per attempt, not recorded by `aw` (F4) |

## Findings

Each is a gap the fake CLI cannot show. None is hidden by the scenario's assertions; F2 is recorded in `summary.json`.

| # | Finding | Owner | Suggested fix |
|---|---|---|---|
| **F1** | A headless Claude CLI **denies every file write** in a worktree it does not trust, and ignores the project's `.claude/settings.json` allow rules there (`Ignoring permissions.allow entries … this workspace has not been trusted`). `aw` creates a new worktree per WorkItem, so the trust dialog can never be answered. The adapter has a `PermissionMode` setting; `aw worker` has no flag for it, so a real Claude still needs a wrapper script for its permission mode (V9-05 removed the wrapper only for the environment). The scenario passes `acceptEdits` through the adapter's config. | provider adapter configuration / `aw worker` (V5-06, V9-05 follow-up) | an `aw worker --claude-permission-mode` flag, documented with the trust behavior |
| **F2** | **The reviewer does not see the repository.** A CHECKER's working directory is an empty scratch directory (its mounts are read-only), and neither its prompt nor its command line says where the repository is. The real model wrote a `notes.txt` of its own into the scratch directory, read it back and answered `approved`: a false approval that no assertion on the outcome can catch. With the fake CLI V9-01 looks fine only because the fake writes through the mount. | V9-01 / V5-12 (how a checker is given its input) | name the read-only mounts to the CLI (`--add-dir`) and in the prompt, or put the maker's diff in the checker's prompt; the executor's existing "no change" check keeps it read-only |
| **F3** | The model did **not act on the check's feedback in the first rework build**: in all three runs that got that far it read `notes.txt`, saw `Paris` and declared the task done (twice it also argued the check contradicted the task); two of the runs needed a second rework before it wrote the demanded line. The `checkFailures` section was in the prompt each time with the right text. | V9-02 prompt wording (`checkFailureFix`) | say that the check is authoritative and must be made to pass, and repeat the failing check in the closing checklist |
| **F4** | `aw worker` cannot pass the CLI an effort level, a spend ceiling or any extra argument; the scenario used the adapter's `StartArgs`. The canonical events drop the CLI's reported cost, so spend per attempt is not recorded. | provider adapter configuration; V5-08A events | adapter settings on `aw worker`; keep `total_cost_usd` in the final event |
| **F5** | A `COMMAND` runs with exactly the environment its Command definition declares — none by default — so a check script cannot call `findstr`, `grep`, `npm`… unless the definition declares `PATH`. The failure is reported accurately (the model saw `'findstr' is not recognized`), but it is easy to hit. | operator documentation | say so where COMMAND environment is documented |

Codex was **not** run. Its compatibility remains `UNVERIFIED`.

## Cost

Five live runs while building the scenario (two failed on the harness' own mistakes — F1's setup and a check script —,
three on the workload), 4–5 Claude attempts each. `aw` did not record the spend (F4); from the CLI's own reports for
single calls (about 0.2 USD for a first call that has to build the prompt cache, 0.05 or less for later calls in the
same few minutes) the total is in the order of 2 USD. Each attempt had a 0.75 USD ceiling (`--max-budget-usd`).
