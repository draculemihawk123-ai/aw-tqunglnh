-- V4-01 runtime schema evolution (docs/design/06-v4-runtime-engine.md;
-- docs/architecture/02-architecture-decisions.md ADR-011/ADR-020/ADR-021;
-- docs/architecture/04-go-core-spec.md §4.5/§5 GC-INV-06). Three groups of
-- change:
--
-- 1. execution_attempts/workflow_runs widen their own state CHECK
--    constraint to the full canonical enum go-core-spec §4.5 already
--    declares (execution_attempts + BLOCKED; workflow_runs + VERIFYING,
--    CANCELLING). Both tables are rebuilt with the same plain
--    DROP TABLE x; CREATE TABLE x (...) shape 0002/0003/0006 already
--    established as safe here, for the identical underlying reason 0006's
--    own comment gives at length: nothing in this codebase has ever
--    written a real (non-test-fixture) row into workflow_runs, node_runs
--    or execution_attempts through any application command — no runtime
--    engine exists before this task (V4-02 is the first task that gives
--    StartWorkflowRun a real, atomic, application-facing transaction) — so
--    both tables, and everything that references them, have zero rows at
--    migration time. Every test fixture that inserts into these tables
--    (internal/adapters/sqlite/fixtures.go, crashworker_fixtures.go) does
--    so against an already-fully-migrated database, never mid-upgrade.
--    node_runs/context_snapshots/write_leases/checkpoints/agent_events/
--    evidence all declare a schema-level FK into one of these two tables
--    but are not themselves rebuilt: SQLite's FK enforcement only ever
--    blocks a DROP TABLE when a *row* in a referencing table points at a
--    row actually being removed (0006's own regression proof,
--    migration_0006_test.go) — with zero rows on both sides there is
--    nothing to check, and recreating the parent table under the same
--    name transparently satisfies every already-declared child FK again.
--
--    VERIFYING is added now even though V4-01 itself has no caller that
--    ever produces it (only V4-12's "END chuyển run sang VERIFYING" does):
--    doing the full state-set rebuild once, now, while workflow_runs is
--    still empty, avoids a second destructive rebuild later at V4-12, by
--    which point V4-02..V4-11 will have given the table real production
--    rows and this exact DROP TABLE technique would no longer be safe.
--    CANCELLING is the one V4-01's own task text names directly.
--
-- 2. Six new tables V4 owns evolving/introducing per
--    docs/design/06-v4-runtime-engine.md V4-01's own "Phạm vi": the
--    immutable per-run pin bundle (execution_manifests) and its
--    append-only scope-expansion ledger (run_manifest_amendments); fork
--    branch identity (branch_tokens, HE-14-M09); the durable-decision
--    ledger required to exist before recovery/completion
--    (decision_artifacts, HE-03-M08); and both cancellation-intent tables
--    (run_cancellation_intents, work_item_cancellation_intents) the
--    ADR-020 cancellation protocol requires to exist ahead of V4-12B/
--    V4-12C actually driving them.
--
-- 3. agent_events (already existing from the V0/V1-04 migration spike,
--    preserved as-is until now per V4-01's own "checkpoints/agent_events
--    đã tồn tại từ migration spike và được V1-04 bảo toàn; task này là nơi
--    chúng tiến hóa cho Alpha") gains artifact_refs_json, matching
--    design/01-system-design.md §6.3's own row for it: "attempt/sequence/
--    kind/schema/redacted payload/artifact refs". A plain ADD COLUMN is
--    enough here (no CHECK constraint involved), defaulted to an empty
--    JSON array so it stays NOT NULL with zero existing rows to migrate.

DROP TABLE execution_attempts;

CREATE TABLE execution_attempts (
    id TEXT PRIMARY KEY,
    node_run_id TEXT NOT NULL REFERENCES node_runs(id),
    attempt_no INTEGER NOT NULL CHECK (attempt_no > 0),
    state TEXT NOT NULL CHECK (state IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','TIMED_OUT','CANCELLED','LOST','INDETERMINATE','BLOCKED')),
    provider_key TEXT,
    provider_session_ref TEXT,
    execution_profile_hash TEXT NOT NULL,
    context_snapshot_id TEXT,
    input_revision_set_json TEXT NOT NULL,
    last_checkpoint_id TEXT,
    termination_reason TEXT,
    version INTEGER NOT NULL CHECK (version > 0),
    started_at TEXT,
    finished_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (node_run_id, attempt_no)
);

DROP TABLE workflow_runs;

CREATE TABLE workflow_runs (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    workflow_version_id TEXT NOT NULL REFERENCES workflow_versions(id),
    family_id TEXT NOT NULL REFERENCES task_families(id),
    scope_version INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('CREATED','RUNNING','WAITING','BLOCKED','VERIFYING','CANCELLING','SUCCEEDED','FAILED','CANCELLED')),
    shared_state_json TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    started_at TEXT,
    finished_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

ALTER TABLE agent_events ADD COLUMN artifact_refs_json TEXT NOT NULL DEFAULT '[]';

-- execution_manifests: the immutable initial pin bundle GC-INV-06 requires
-- ("WorkflowRun pin WorkflowVersion, CompiledSnapshotHash và dependency
-- manifest trong suốt lifetime"). run_id is UNIQUE — exactly one manifest
-- per run, ever; there is no update path, only CreateExecutionManifest
-- (idempotent by identical content) at the repository layer.
-- execution_profile_hash/context_route_policy_hash are nullable: no task
-- before V4-01 resolves either at run-start time, so both stay empty until
-- a real caller has one to pin, mirroring 0006's own
-- last_probe_error_code "column ready, nobody required to fill it yet"
-- precedent.
CREATE TABLE execution_manifests (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE REFERENCES workflow_runs(id),
    workflow_version_id TEXT NOT NULL REFERENCES workflow_versions(id),
    compiled_snapshot_hash TEXT NOT NULL,
    dependency_manifest_json TEXT NOT NULL,
    base_revision_set_json TEXT NOT NULL,
    execution_profile_hash TEXT,
    context_route_policy_hash TEXT,
    created_at TEXT NOT NULL
);

-- run_manifest_amendments: append-only scope-expansion ledger (ADR-011).
-- Revision is a per-run counter starting at 1; previous_revision is 0 for
-- the first amendment (amending the initial manifest directly) and must
-- equal the run's prior highest revision otherwise — the repository layer
-- enforces that continuity (it requires reading the current high-water
-- mark first), UNIQUE(run_id, revision) is the storage-layer backstop.
-- There is no update or delete path: AppendRunManifestAmendment only ever
-- inserts.
CREATE TABLE run_manifest_amendments (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    revision INTEGER NOT NULL CHECK (revision > 0),
    previous_revision INTEGER NOT NULL CHECK (previous_revision >= 0),
    approved_scope_version INTEGER NOT NULL CHECK (approved_scope_version > 0),
    reason TEXT NOT NULL,
    approved_by TEXT NOT NULL,
    approved_at TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (run_id, revision)
);

CREATE INDEX idx_run_manifest_amendments_run ON run_manifest_amendments(run_id, revision);

-- branch_tokens: persisted, queue-order-independent FORK branch identity
-- (HE-14-M09; design/01-system-design.md §6.3: "run/fork/branch/current
-- node/state/version; unique run+fork+branch"). fork_key is the FORK
-- NodeRun's own node_key; branch_key is the declared branch identifier
-- within that fork. V4-01 only persists tokens (Create/Get/List); the FORK
-- activation logic that creates them (V4-10) and the JOIN policy that reads
-- them (V4-11) own the actual lifecycle.
CREATE TABLE branch_tokens (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    fork_key TEXT NOT NULL,
    branch_key TEXT NOT NULL,
    current_node_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('ACTIVE','SUCCEEDED','FAILED','CANCELLED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (run_id, fork_key, branch_key)
);

-- decision_artifacts: the durable, versioned decision ledger HE-03-M08
-- requires ("quyết định hệ trọng và lý do MUST được lưu trong decision
-- artifact có version, không chỉ trong transcript"). kind is intentionally
-- unconstrained by CHECK — each later task that produces a new kind of
-- decision (V4-09 approval, V4-12A scope waive, V4-13 recovery, V5-11
-- completion) names its own value, the same way agent_events.kind stays
-- open. Immutable and append-only from its first row: no update/delete
-- path exists or ever will.
CREATE TABLE decision_artifacts (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    kind TEXT NOT NULL,
    policy_version TEXT NOT NULL,
    input_json TEXT NOT NULL,
    result_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_decision_artifacts_project_kind ON decision_artifacts(project_id, kind);

-- run_cancellation_intents: the durable, idempotent-per-run intent
-- ADR-020's cancellation protocol requires to exist before a WorkflowRun
-- may ever move to CANCELLING. run_id is UNIQUE: at most one intent per
-- run, ever — a second CancelRun call observes and returns this same row.
-- state distinguishes "committed, quiesce not yet finished" (REQUESTED)
-- from "Run reached CANCELLED" (COMPLETED) for V4-13's recovery reaper
-- ("một intent đã commit nhưng coordinator chết trước khi quiesce xong
-- phải được tiếp tục"). The actual quiesce coordinator that drives
-- REQUESTED -> COMPLETED is V4-12B's own scope.
CREATE TABLE run_cancellation_intents (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    run_id TEXT NOT NULL UNIQUE REFERENCES workflow_runs(id),
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('REQUESTED','COMPLETED')),
    requested_at TEXT NOT NULL
);

-- work_item_cancellation_intents: CancelWorkItem's own durable intent
-- (ADR-020: "một task nhiều run nên intent theo Run là không đủ").
-- work_item_id is UNIQUE for the identical "idempotent, at most one ever"
-- reason as run_cancellation_intents above. The CAS fences this intent
-- imposes on ResolveWorkItemBlocker/StartWorkflowRun are V4-02/V4-12C's
-- own scope, not this table's.
CREATE TABLE work_item_cancellation_intents (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL UNIQUE REFERENCES work_items(id),
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('REQUESTED','COMPLETED')),
    requested_at TEXT NOT NULL
);
