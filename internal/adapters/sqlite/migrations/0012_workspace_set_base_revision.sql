-- workspace_sets.base_revision_set_json / base_revision_set_hash (V3-06,
-- docs/design/05-v3-project-workspace.md: "base RevisionSet after all
-- required ready"; go-core-spec §4.3's own WorkspaceSet/RevisionSet shape).
--
-- A WorkspaceSet's own "base" RevisionSet is the exact-commit snapshot
-- (internal/domain/workspace.RevisionSet, already built) across every one
-- of its required repositories, computed and persisted exactly once, only
-- after the whole set is confirmed READY (never partially, per this task's
-- own "Hoàn thành khi: family chỉ ready khi mọi required repository ready").
-- Both columns are nullable rather than NOT NULL: every WorkspaceSet spends
-- its REQUESTED/PROVISIONING/BLOCKED lifetime (and forever, if it never
-- reaches READY) with no base RevisionSet at all — this is not an
-- always-present column with a temporary placeholder, it genuinely has no
-- value until the one moment it is computed.
--
-- _json stores workspace.RevisionSet.Entries() (already-normalized,
-- sorted-by-RepositoryID []workspace.Revision) as its own canonical JSON
-- encoding; _hash stores the identical value RevisionSet.ContentHash()
-- already computes from that same canonical encoding
-- (internal/domain/workspace/workspace.go's own NewRevisionSet) — carried
-- as its own column, rather than re-derived on every read, so a caller can
-- compare/index on it without re-parsing and re-hashing _json first, the
-- same "store what NewRevisionSet already computed once" reasoning every
-- other content-hash column in this schema already follows.
ALTER TABLE workspace_sets ADD COLUMN base_revision_set_json TEXT;
ALTER TABLE workspace_sets ADD COLUMN base_revision_set_hash TEXT;

-- repository_workspaces.last_provision_error_code (V3-06): mirrors
-- repositories.last_probe_error_code's identical role (0001_initial_schema.sql,
-- V3-02) for the analogous first-provision failure path — a short, typed
-- reason recorded only on a FAILED RepositoryWorkspace row (this task's own
-- "failure partial state": a provisioning attempt that fails is the job
-- doing its job correctly, recorded as data on the row, never a raw
-- provider/SQL error string). NULL for every non-FAILED row, forever.
ALTER TABLE repository_workspaces ADD COLUMN last_provision_error_code TEXT;
