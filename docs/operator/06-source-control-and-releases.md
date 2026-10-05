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
[01-quickstart.md](01-quickstart.md)'s own `provisionedRepositories` response field). Two commands manage its
own lifecycle directly:

```bash
aw repository-workspace reconcile --project-id <id> --expected-version <n> --idempotency-key r-1 <id>
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

Every one of these three takes `--byte-limit`/`--file-limit`/`--line-limit` (server-default if omitted) — a
genuinely huge diff or file is truncated, never silently loaded in full into memory or into your terminal.
`--workspace-set-id`/`--repository-id`/a revision + its own generation number are all required — a revision is
always named relative to the specific workspace generation it belongs to, since a workspace can be
provisioned, released, and re-provisioned (a new generation) over its own lifetime.
