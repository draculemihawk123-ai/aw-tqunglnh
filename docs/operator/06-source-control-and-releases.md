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
