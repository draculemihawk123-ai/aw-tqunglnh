
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('ACTIVE','ARCHIVED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE repositories (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    name TEXT NOT NULL,
    local_path TEXT NOT NULL,
    default_ref TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('ACTIVE','DISABLED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (project_id, name)
);

CREATE TABLE task_families (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    root_work_item_id TEXT NOT NULL UNIQUE,
    scope_version INTEGER NOT NULL CHECK (scope_version > 0),
    status TEXT NOT NULL CHECK (status IN ('ACTIVE','BLOCKED','COMPLETED','CANCELLED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE work_items (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    kind TEXT NOT NULL CHECK (kind IN ('ROOT','CHILD')),
    parent_id TEXT REFERENCES work_items(id),
    family_id TEXT NOT NULL REFERENCES task_families(id),
    title TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('BACKLOG','READY','ACTIVE','BLOCKED','DONE','CANCELLED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE family_repository_scopes (
    family_id TEXT NOT NULL REFERENCES task_families(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    scope_version INTEGER NOT NULL CHECK (scope_version > 0),
    access TEXT NOT NULL CHECK (access IN ('READ','WRITE')),
    paths_json TEXT NOT NULL,
    reason TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (family_id, repository_id, scope_version, access)
);

CREATE TABLE workspace_sets (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    family_id TEXT NOT NULL UNIQUE REFERENCES task_families(id),
    state TEXT NOT NULL CHECK (state IN ('REQUESTED','PROVISIONING','READY','BLOCKED','RELEASING','RELEASED','FAILED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE repository_workspaces (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    workspace_set_id TEXT NOT NULL REFERENCES workspace_sets(id),
    family_id TEXT NOT NULL REFERENCES task_families(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    generation INTEGER NOT NULL CHECK (generation > 0),
    locator TEXT NOT NULL,
    branch_ref TEXT,
    base_revision TEXT NOT NULL,
    current_revision TEXT,
    state TEXT NOT NULL CHECK (state IN ('PROVISIONING','READY','QUARANTINED','RELEASING','RELEASED','FAILED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (workspace_set_id, repository_id, generation),
    UNIQUE (family_id, repository_id, generation)
);

CREATE TABLE workflow_definitions (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id),
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('DRAFT','ACTIVE','ARCHIVED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE workflow_versions (
    id TEXT PRIMARY KEY,
    definition_id TEXT NOT NULL REFERENCES workflow_definitions(id),
    version_no INTEGER NOT NULL CHECK (version_no > 0),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    canonical_content TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    dependency_manifest TEXT NOT NULL,
    published_by TEXT NOT NULL,
    published_at TEXT NOT NULL,
    UNIQUE (definition_id, version_no),
    UNIQUE (definition_id, content_hash)
);

CREATE TABLE workflow_runs (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    workflow_version_id TEXT NOT NULL REFERENCES workflow_versions(id),
    family_id TEXT NOT NULL REFERENCES task_families(id),
    scope_version INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('CREATED','RUNNING','WAITING','BLOCKED','SUCCEEDED','FAILED','CANCELLED')),
    shared_state_json TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    started_at TEXT,
    finished_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE node_runs (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    node_key TEXT NOT NULL,
    activation_sequence INTEGER NOT NULL CHECK (activation_sequence > 0),
    iteration INTEGER NOT NULL CHECK (iteration >= 0),
    state TEXT NOT NULL CHECK (state IN ('PENDING','READY','QUEUED','RUNNING','WAITING','BLOCKED','SUCCEEDED','FAILED','SKIPPED','CANCELLED')),
    selected_outcome TEXT,
    input_state_hash TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (run_id, node_key, activation_sequence)
);

CREATE TABLE execution_attempts (
    id TEXT PRIMARY KEY,
    node_run_id TEXT NOT NULL REFERENCES node_runs(id),
    attempt_no INTEGER NOT NULL CHECK (attempt_no > 0),
    state TEXT NOT NULL CHECK (state IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','TIMED_OUT','CANCELLED','LOST','INDETERMINATE')),
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

CREATE TABLE context_snapshots (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    attempt_id TEXT NOT NULL REFERENCES execution_attempts(id),
    canonical_content TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    revision_set_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (attempt_id, content_hash)
);

CREATE TABLE durable_jobs (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    kind TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('AVAILABLE','LEASED','SUCCEEDED','FAILED','DEAD','CANCELLED')),
    available_at TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    claim_count INTEGER NOT NULL DEFAULT 0,
    max_claims INTEGER NOT NULL CHECK (max_claims > 0),
    lease_owner TEXT,
    lease_token INTEGER NOT NULL DEFAULT 0,
    lease_until TEXT,
    heartbeat_at TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    last_error_code TEXT,
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX durable_jobs_claim_idx
    ON durable_jobs (state, available_at, priority DESC, created_at);

CREATE TABLE write_leases (
    repository_workspace_id TEXT NOT NULL REFERENCES repository_workspaces(id),
    generation INTEGER NOT NULL CHECK (generation > 0),
    fence_token INTEGER NOT NULL CHECK (fence_token > 0),
    holder_job_id TEXT NOT NULL REFERENCES durable_jobs(id),
    holder_job_lease_token INTEGER NOT NULL CHECK (holder_job_lease_token > 0),
    holder_attempt_id TEXT NOT NULL REFERENCES execution_attempts(id),
    lease_owner TEXT NOT NULL,
    lease_until TEXT NOT NULL,
    heartbeat_at TEXT NOT NULL,
    PRIMARY KEY (repository_workspace_id, generation)
);

CREATE TABLE checkpoints (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    node_run_id TEXT NOT NULL REFERENCES node_runs(id),
    attempt_id TEXT NOT NULL REFERENCES execution_attempts(id),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    canonical_event_sequence INTEGER NOT NULL,
    context_snapshot_id TEXT,
    revision_set_json TEXT NOT NULL,
    shared_state_hash TEXT NOT NULL,
    artifact_refs_json TEXT NOT NULL,
    provider_session_ref TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (attempt_id, sequence)
);

CREATE TABLE agent_events (
    id TEXT PRIMARY KEY,
    attempt_id TEXT NOT NULL REFERENCES execution_attempts(id),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    kind TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    payload_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (attempt_id, sequence)
);

CREATE TABLE evidence (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    node_run_id TEXT NOT NULL REFERENCES node_runs(id),
    attempt_id TEXT NOT NULL REFERENCES execution_attempts(id),
    kind TEXT NOT NULL,
    verdict TEXT NOT NULL,
    artifact_manifest_json TEXT NOT NULL,
    revision_set_json TEXT NOT NULL,
    policy_version TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE domain_events (
    id TEXT PRIMARY KEY,
    project_id TEXT,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    event_type TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    payload_json TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (aggregate_type, aggregate_id, sequence)
);

CREATE TABLE command_receipts (
    actor TEXT NOT NULL,
    project_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    command_type TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    result_json TEXT,
    error_code TEXT,
    created_at TEXT NOT NULL,
    PRIMARY KEY (actor, project_id, idempotency_key, command_type)
);