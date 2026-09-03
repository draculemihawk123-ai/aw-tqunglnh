-- V3-07 initialization/readiness evidence
-- (docs/design/05-v3-project-workspace.md V3-07,
-- docs/harness-engineering/06-lec-06-khoi-tao-la-phase-rieng.md HE-06-M02/
-- HE-06-M03/HE-06-M06, docs/harness-engineering/12-lec-12-clean-handoff.md
-- HE-12-M03). See internal/domain/readiness's own package doc comment for
-- the full design reasoning (why Repository-level, why argv-only, why a
-- narrow blocker rather than the shared `blockers` table sketch).

-- readiness_profiles: the canonical setup/verification recipe HE-06-M02
-- requires, one row per Repository. SetReadinessProfile is a plain upsert
-- (the latest call always wins) -- no citation in this task's own scope
-- asks for a versioned recipe history the way component_pack_assignments'
-- own append-only "what was in effect at time T" requirement does.
-- setup_* columns are all nullable together (a Profile's own Setup is
-- optional, internal/domain/readiness.Profile's own doc comment);
-- verification_* columns are NOT NULL -- every Profile has a real
-- Verification command, this task's own baseline check itself.
CREATE TABLE readiness_profiles (
    repository_id TEXT PRIMARY KEY REFERENCES repositories(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    setup_executable TEXT,
    setup_argv_json TEXT,
    setup_working_directory TEXT,
    setup_timeout_seconds INTEGER,
    verification_executable TEXT NOT NULL,
    verification_argv_json TEXT NOT NULL,
    verification_working_directory TEXT NOT NULL,
    verification_timeout_seconds INTEGER NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- readiness_baseline_attempts: append-only pre-change baseline evidence
-- (HE-06-M03 "runner MUST chạy readiness/baseline checks và lưu output
-- thật trước khi cho writer hoạt động", HE-12-M03 "regression không được
-- che bởi lỗi có sẵn") -- mirrors repository_probe_attempts's own
-- append-only, UNIQUE(job_id) idempotent-evidence shape
-- (0008_repository_probe_attempts.sql) applied to a RepositoryWorkspace's
-- own baseline check instead of a Repository's own onboarding probe.
-- outcome is exactly one of GREEN|RED|ENVIRONMENT_ERROR
-- (internal/domain/readiness.BaselineOutcome): GREEN/RED both mean the
-- command genuinely ran to completion (exit_code populated, error_code/
-- error_message NULL); ENVIRONMENT_ERROR means it could not even be
-- observed to run (error_code/error_message populated, exit_code NULL) --
-- this is what "pin baseline debt, không giả PASS" means concretely: RED
-- is written as RED, never silently reported GREEN, and ENVIRONMENT_ERROR
-- is never conflated with either. stdout/stderr excerpts are bounded at
-- the application layer (internal/app/readinesscheck) before being
-- written here -- this schema stores whatever bounded text it is given,
-- it does not itself enforce a length cap.
CREATE TABLE readiness_baseline_attempts (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    repository_workspace_id TEXT NOT NULL REFERENCES repository_workspaces(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    job_id TEXT NOT NULL REFERENCES durable_jobs(id),
    stage TEXT NOT NULL CHECK (stage IN ('SETUP','VERIFICATION')),
    outcome TEXT NOT NULL CHECK (outcome IN ('GREEN','RED','ENVIRONMENT_ERROR')),
    exit_code INTEGER,
    duration_ms INTEGER NOT NULL,
    stdout_excerpt TEXT NOT NULL,
    stderr_excerpt TEXT NOT NULL,
    error_code TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (job_id)
);

CREATE INDEX readiness_baseline_attempts_workspace_idx
    ON readiness_baseline_attempts(repository_workspace_id, created_at);

-- readiness_environment_blockers: V3-07's own narrow, scoped "typed
-- environment blocker" -- deliberately NOT the shared cross-cutting
-- `blockers` table docs/design/01-system-design.md §6.1 sketches
-- (id, work_item/run/node, type, reason, status, resolution mode/actor/
-- reason/decision_artifact_id, created/resolved): that table's own anchor
-- (work_item/run/node) does not fit this task's own trigger point (a
-- RepositoryWorkspace, no run/node aggregate exists until V4), its own
-- listed `type` values (COMPLETION_POLICY_FAILED, RUN_CANCELLED,
-- SCOPE_EXPANSION_REQUIRED, four admission-reason types) are ALL
-- runtime/V4-era concepts nothing in this codebase can populate yet, and
-- nothing has created that table in any migration to date (confirmed by
-- grep across every prior migration file). Building the shared table now,
-- prematurely, with fields this task cannot populate and a `type` domain
-- that overlaps nothing this task actually produces would be exactly the
-- kind of speculative, ungrounded scope 00-roadmap.md §3 warns against.
-- Anchored to repository_workspace_id -- the one real aggregate this
-- task's own trigger point has in hand. At most one OPEN row per
-- RepositoryWorkspace at a time (the partial UNIQUE index below); a fresh
-- non-ENVIRONMENT_ERROR attempt resolves it
-- (internal/app/readinesscheck.Handler's own doc comment).
CREATE TABLE readiness_environment_blockers (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    repository_workspace_id TEXT NOT NULL REFERENCES repository_workspaces(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    job_id TEXT NOT NULL REFERENCES durable_jobs(id),
    type TEXT NOT NULL CHECK (type = 'BASELINE_ENVIRONMENT_ERROR'),
    reason TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('OPEN','RESOLVED')),
    created_at TEXT NOT NULL,
    resolved_at TEXT
);

CREATE UNIQUE INDEX readiness_environment_blockers_open_idx
    ON readiness_environment_blockers(repository_workspace_id)
    WHERE status = 'OPEN';
