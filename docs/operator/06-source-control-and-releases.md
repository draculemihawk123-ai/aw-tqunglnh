# Source control and releases

## Local-only Git — the core constraint

Every repository `aw` touches is a real, local Git repository at the absolute filesystem path you registered
(`remoteLocator` — see [01-quickstart.md](01-quickstart.md)). `aw` never pushes, fetches, or clones from a
remote — `internal/adapters/gitworktree.Provider` (the ONE real Git adapter this codebase has) never calls any
of those operations anywhere in its own implementation. A "release" in Agent Kit is a real, local commit
landing in your own repository's own history — nothing more, nothing networked.

## ReleaseSet lifecycle

```bash
echo '{"familyId":"<taskFamilyId>"}' | aw release-set create --project-id <id> --idempotency-key rs-1
```

A ReleaseSet groups one or more real local commits a task family produces before they are considered "done."
Once created:

```bash
echo '{"authorName":"you","authorEmail":"you@example.com","message":"my commit",
       "releaseSetId":"<id>","repositoryWorkspaceId":"<id>",
       "expectedReleaseSetVersion":1,"expectedWorkspaceVersion":1}' \
  | aw release-set local-commit --project-id <id> --idempotency-key commit-1 --wait --wait-timeout 30s
```

This is a REAL `git commit` against the real repository workspace, fenced by BOTH the ReleaseSet's own
optimistic-concurrency version AND the RepositoryWorkspace's own version — two workers racing to commit into
the same workspace get a real, typed `CONFLICT` on the loser, never a silently corrupted history. `--wait`
blocks until the commit job reaches a terminal state.

If the repository workspace's worktree has nothing to commit (no staged, unstaged or untracked change), the
commit does not retry: the operation ends immediately in state `FAILED` with `failureReason: "NO_CHANGES"` (visible
in the `--wait` result, in `aw release-set local-commit status`, and in the `ReleaseSetLocalCommitFailed` event),
and no commit is created. `NO_CHANGES` is a failure reason on the operation, not an error code. Edit the worktree
and run `aw release-set local-commit` again with a new `--idempotency-key` and the same fields — a failed
`NO_CHANGES` operation never blocks the retry (reusing the old key would only replay the old result).

A committed local commit also moves the repository workspace forward: in the same transaction that marks the
operation `COMMITTED`, the workspace's `currentRevision` becomes the new commit and its `version` goes up by one
(read the new `version` before the next `aw release-set local-commit`; the old `expectedWorkspaceVersion` is now
stale). The next WorkflowRun started for the family pins that `currentRevision` as the revision it starts from, so
its `MACHINE_GATE` nodes compare against the commit the worktree is really on. Before V9-16 the run kept pinning
the revision the family was first provisioned from, and every `MACHINE_GATE` of a run started after a local commit
failed with `VALIDATION_FAILED` (the worktree HEAD no longer matched the pinned revision). Runs that were already
started keep the revision they pinned; only runs started after the upgrade benefit.

```bash
aw release-set seal --expected-version <n> --idempotency-key seal-1 <releaseSetId>   # no more commits allowed
aw release-set abandon --expected-version <n> --idempotency-key abandon-1 <releaseSetId>  # discard, never applied
aw release-set show <releaseSetId>
aw release-set list --project-id <id>
```

## Repository workspace lifecycle

A `RepositoryWorkspace` is the real, provisioned Git worktree a WorkItem's own `WorkspaceSet` holds for one
repository — created automatically when a root WorkItem's `initialScope` names that repository (see
[01-quickstart.md](01-quickstart.md)'s own `provisionedRepositories` response field). Three commands manage its
own lifecycle directly:

```bash
aw repository-workspace reconcile --project-id <id> --expected-version <n> --idempotency-key r-1 <id>
aw repository-workspace adopt-head --project-id <id> --expected-version <n> --idempotency-key a-1 <id>   # see below
aw workspace-set release --project-id <id> --expected-version <n> --idempotency-key rel-1 <workspaceSetId>
```

### A `QUARANTINED` worktree and how it is recovered (V9-18)

A worktree is quarantined when aw cannot rule out that an interrupted attempt changed it: the worker died or the run
was cancelled while a `MAKER` held the write lease **and** the worktree's HEAD is no longer the commit the attempt
started from (typically an agent that ran `git commit` itself). Editing files without committing does not quarantine —
HEAD is unchanged, so the attempt is treated as clean and its changes are simply what the next run finds in the
worktree. Nothing writes to a quarantined worktree and `aw workspace-set release` refuses while one is current.

`aw repository-workspace reconcile` inspects it. A worktree with **uncommitted changes stays quarantined** (the
evidence is never reset for you): clean or discard them yourself, then reconcile again. A **clean** one is
**recreated** as the next generation of the same repository:

- the new generation starts from the quarantined generation's `currentRevision` — the last commit aw itself recorded
  on it (every ReleaseSet local commit advances it, see above) — so the tasks the family already committed are still
  in its history. Anything the interrupted attempt committed on its own stays on the old generation's branch, as
  evidence, and is not carried over. Before V9-18 the new generation started from the repository's default branch and
  silently dropped the family's earlier commits;
- if the repository has a readiness profile, the new generation gets its baseline job like a first generation does
  (`aw repository readiness show <repo>` lists the baseline per workspace). Without it the baseline stayed `PENDING`
  and no work item could become `READY` for writing;
- the old generation's row remains `QUARANTINED` for good, as evidence. Only the **newest** generation of a repository
  counts when aw asks whether a family still has a quarantined worktree, so a superseded one no longer blocks
  `aw blocker resolve`. Runs started afterwards pin the new generation.

`aw workspace-set show` lists every generation; pick the one with the highest `generation` (the `READY` one) when a
script needs the current worktree.

### A commit made outside aw (V11-01, ADR-034)

aw records, for each repository workspace, the revision it expects the worktree to be on: `currentRevision` (see
`aw workspace-set show`). It only moves forward through an aw local commit (above) or `adopt-head` (below). If you —
or an IDE — run `git commit` in the worktree directly, HEAD moves and aw no longer agrees with Git. aw never papers
over that: nothing you committed outside aw is treated as aw's own until you say so.

What aw does when HEAD and `currentRevision` differ:

- `aw run start` (and `POST /work-items/{id}/runs`) refuse with `WORKSPACE_HEAD_DRIFT: …`, naming the repository, both
  revisions, how many commits were made outside aw and the exact `adopt-head` command. This check needs the Git
  worktree: `aw run start` makes it when `--workspace-root` / `AW_WORKSPACE_ROOT` is set, and skips it otherwise.
- A run that was already started keeps the revision it pinned (a run's manifest never changes). Its first
  `MACHINE_GATE` fails closed with `VALIDATION_FAILED`, and the attempt's `failureDetail` in `aw run timeline` is the
  same typed `WORKSPACE_HEAD_DRIFT` explanation, with the commits (hash, author, subject). Cancel that run and start
  again after you resolve the drift.
- `aw release-set local-commit` does not build a commit on top of the outside commits: the operation ends `FAILED`
  with `failureReason: "HEAD_DRIFT"` before any `git commit` (a failure reason on the operation, like `NO_CHANGES`).

To **keep** the outside commits, record them:

```bash
aw repository-workspace adopt-head --project-id <id> --expected-version <n> --idempotency-key a-1 <repositoryWorkspaceId>
# result: previousRevision, adoptedRevision, adoptedCommitCount, adoptedCommits[] (commit, author, subject), version
```

`adopt-head` is synchronous and is refused unless all of these hold: the workspace is `READY` (a `QUARANTINED` one is
refused with `WORKSPACE_QUARANTINED`; use `reconcile`), no attempt holds the write lease, the worktree is clean (no
uncommitted change), HEAD descends from the recorded `currentRevision` in a straight line (it does not after a
`git reset` or a rebase), and HEAD really differs from it. On success it sets `currentRevision` to HEAD, adds one to the
workspace `version`, and appends a `WorkspaceHeadAdopted` event with the commits, their authors and the operator.
Repeating the same `--idempotency-key` replays the result; a stale `--expected-version` is refused. The code in those
commits did not pass any aw gate, but from now on aw treats it as part of the family's history — the gates of later
runs still run on it. The kit wraps this as `kit/scripts/adopt-head.sh`.

To **not** keep them, put the worktree back on the recorded revision (for example `git -C <worktree> reset --hard
<currentRevision>`) before the next run; `aw` never resets a worktree for you.

## Read-only source/diff/log — never an interactive terminal

Three commands give paginated, read-only views over a real repository workspace's own Git object store — this
is the ENTIRE "browse the code" surface; there is deliberately no shell/terminal into a running workspace:

```bash
# Read one file's real content at an exact revision
aw repository-workspace source --project-id <id> --repository-id <repoId> --workspace-set-id <wsId> \
  --revision <commitId> --revision-generation <gen> --path path/to/file.go --output -

# A real unified diff between two revisions
aw repository-workspace diff --project-id <id> --repository-id <repoId> --workspace-set-id <wsId> \
  --base-revision <commitA> --base-revision-generation <genA> \
  --result-revision <commitB> --result-revision-generation <genB>

# Paginated commit history, anchored at a real commit
aw repository-workspace log --project-id <id> --repository-id <repoId> --workspace-set-id <wsId> \
  --anchor <commitId> --anchor-generation <gen> [--cursor <commitId>] [--limit <n>]
```

### Uncommitted changes — what an approval gate has to show

A commit-to-commit diff is empty at an `APPROVAL` gate: the agent wrote files into the worktree, but nothing is committed
yet, so `currentRevision` has not moved. To see what the agent changed, ask for the worktree itself:

```bash
aw repository-workspace diff --project-id <id> --repository-id <repoId> --workspace-set-id <wsId> \
  --base-revision <currentRevision> --base-revision-generation <gen> --working-tree
```

(HTTP: the same `GET .../diff` route with `result=WORKING_TREE`.) The answer carries `uncommitted: true`, lists every
modified, added, deleted and untracked file (never ignored files) with line counts, a `contentHash` per file and a
`changeSetDigest` over the whole set, plus a unified patch. A binary file is a name and a size, never content; a symlink is
a name, never followed. The web UI's Workspace tab shows this view by default when the worktree has changes, and an
approval card in the Graph tab says "N files changed, +x −y" with a link to it.

It is read-only (`GIT_OPTIONAL_LOCKS=0`, no `git add`, no index write, HEAD unchanged), and it is **refused with 409 while an
attempt holds the write lease** on that repository workspace: the worktree is in flux, so wait for the attempt or cancel it.
A worktree with more than 20000 changed paths is refused (422): add the generated files to `.gitignore` or remove them. The
base revision must still be one `aw` recorded for the workspace.

Every one of these three takes `--byte-limit`/`--file-limit`/`--line-limit` (server-default if omitted) — a
genuinely huge diff or file is truncated, never silently loaded in full into memory or into your terminal.
`--workspace-set-id`/`--repository-id`/a revision + its own generation number are all required — a revision is
always named relative to the specific workspace generation it belongs to, since a workspace can be
provisioned, released, and re-provisioned (a new generation) over its own lifetime.
