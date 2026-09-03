-- scope_expansion_requests (V3-08, docs/design/05-v3-project-workspace.md;
-- docs/design/01-system-design.md §6.1's own row: "id, family, requested
-- grants, reason, status, actor/version | only add/upgrade; approval
-- required"; docs/architecture/04-go-core-spec.md §8's command table:
-- RequestScopeExpansion/ApproveScopeExpansion/RejectScopeExpansion/
-- WithdrawScopeExpansion).
--
-- A ScopeExpansionRequest never mutates or removes a family_repository_scopes
-- row (0001_initial_schema.sql); it only ever proposes candidate grants
-- that, once approved, become brand-new family_repository_scopes rows at a
-- brand-new, strictly higher scope_version — this table records the
-- request/decision audit trail, never the grants themselves (internal/app/work.
-- ApproveScopeExpansion is the one place a row here ever causes a new
-- family_repository_scopes row to be written, via the exact same
-- AddRepositoryScope path CreateRootWorkItem/CreateChildWorkItem already use).
--
-- requested_grants_json stores work.RequestedGrant[] (repositoryId/access/
-- pathScopes/reason — already normalized by work.NewScopeExpansionRequest,
-- the identical normalizePathScopes helper NewRepositoryScope itself uses)
-- as its own canonical JSON encoding, the same "store the whole candidate
-- list as one JSON column" choice paths_json already makes elsewhere in this
-- schema, rather than a second child table: unlike family_repository_scopes/
-- work_item_effective_scopes, these candidates are never queried
-- individually (a request is approved or rejected as a whole), so a normalized
-- child table would add join cost this task's own access pattern never needs.
--
-- referenced_work_item_id is nullable — this task's own "block WorkItem/run
-- reference nếu có" judgment call: optional, and when present, only a
-- structural pointer to a real WorkItem in the same family
-- (internal/app/work.RequestScopeExpansion validates this via tx.Work().
-- GetWorkItem before ever writing this row); no runtime engine exists yet
-- (V4) to ever produce a real "run reference", and this table itself never
-- drives that WorkItem's own status — see work.ScopeExpansionRequest's own
-- doc comment for that explicit boundary.
--
-- decided_by/decided_at/decision_note/approved_scope_version are all NULL
-- until a decision is recorded (ApproveScopeExpansion/RejectScopeExpansion/
-- WithdrawScopeExpansion) — mirroring repository_workspaces.
-- last_provision_error_code's own "NULL until the one moment it applies"
-- discipline (0012_workspace_set_base_revision.sql), applied here to a
-- decision instead of a failure. approved_scope_version is set only by
-- ApproveScopeExpansion, recording exactly which TaskFamily.scope_version the
-- request's grants were persisted at (never re-derivable later by simply
-- reading task_families.scope_version, since that column keeps moving
-- forward as later requests are approved).
CREATE TABLE scope_expansion_requests (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    family_id TEXT NOT NULL REFERENCES task_families(id),
    referenced_work_item_id TEXT REFERENCES work_items(id),
    requested_grants_json TEXT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('PENDING','APPROVED','REJECTED','WITHDRAWN')),
    requested_by TEXT NOT NULL,
    requested_at TEXT NOT NULL,
    decided_by TEXT,
    decided_at TEXT,
    decision_note TEXT,
    approved_scope_version INTEGER,
    version INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- The state-aggregation-style read every command in this concern needs:
-- "every PENDING request for this family" (duplicate-approval/withdraw
-- checks) and "every request ever made for this family" (an operator
-- listing/audit view) both filter by family_id first.
CREATE INDEX idx_scope_expansion_requests_family ON scope_expansion_requests(family_id, status);
