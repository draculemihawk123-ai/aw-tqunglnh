-- release_sets: a TaskFamily's own sealed-or-abandoned release record
-- (V5-10A, docs/design/07-v5-execution-evidence.md; AK-ARCH-015C, GC-DS-04)
-- — exactly what a completion decision considered per repository (base
-- revision, result revision, verdict), never a remote-authority record
-- (V5-10A's own locked scope: "không push/PR/merge/force-push"). A family
-- may accumulate more than one ReleaseSet over its own lifetime (one per
-- completion attempt across REWORK cycles) — this table is not 1:1 with
-- task_families the way workspace_sets is.
--
-- state mirrors work.ReleaseSetState's own three-value closed set:
-- CREATED is the only state either terminal state (SEALED, ABANDONED) may
-- ever transition from; once terminal, a row is immutable (see
-- internal/domain/work/release_set.go's own doc comment).
--
-- content_hash is the sha256 canonical-JSON digest of this ReleaseSet's
-- own sorted per-repository entries (release_set_repositories below) —
-- computed once in Go (work.NewReleaseSet) and persisted here rather than
-- recomputed by a query, the same "compute once in the domain
-- constructor, persist the result" discipline workspace.RevisionSet's own
-- ContentHash already establishes.
CREATE TABLE release_sets (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    family_id TEXT NOT NULL REFERENCES task_families(id),
    state TEXT NOT NULL CHECK (state IN ('CREATED', 'SEALED', 'ABANDONED')),
    content_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    sealed_at TEXT,
    abandoned_at TEXT,
    version INTEGER NOT NULL CHECK (version > 0)
);

CREATE INDEX idx_release_sets_family ON release_sets(family_id, state);

-- release_set_repositories: one row per repository a ReleaseSet names —
-- work.RepositoryRelease's own durable shape. verdict reuses gate.Verdict's
-- own 5-value closed set (kept in lockstep with gate.go's own constants by
-- hand, the same cross-package CHECK-constraint discipline blockers.type
-- already establishes for runtime.TerminationReason).
CREATE TABLE release_set_repositories (
    release_set_id TEXT NOT NULL REFERENCES release_sets(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    base_vcs_object_id TEXT NOT NULL,
    result_vcs_object_id TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('PASS', 'FAIL', 'ERROR', 'NOT_RUN', 'NOT_APPLICABLE')),
    PRIMARY KEY (release_set_id, repository_id)
);
