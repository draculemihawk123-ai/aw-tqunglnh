-- repository_probe_attempts is the append-only onboarding-probe evidence
-- log V3-01 deliberately left for V3-02 to own
-- (docs/design/05-v3-project-workspace.md V3-02,
-- docs/design/01-system-design.md §6.1: "repository/job/state/result/
-- error/time | append-only; active probe idempotent"). One row is written
-- per REPOSITORY_PROBE durable job that reaches a definitive verdict about
-- the Repository it was claimed for (ACTIVE or BLOCKED) -- never for a job
-- that crashes/errors before reaching one (that job is simply retried by
-- the existing durable-job lease/recovery mechanism, or eventually goes
-- DEAD, with no attempt row of its own). This is V3-02's own "job success
-- vs. business outcome" distinction made concrete: a probe that correctly
-- determines a repository is unusable (BLOCKED, with error_code/
-- error_message populated) is the job doing its job correctly, so state is
-- always 'SUCCEEDED' for every row this task's own code ever writes;
-- 'FAILED' is reserved schema headroom for a future change that also wants
-- to log a crashed attempt (no code in this task writes it).
--
-- UNIQUE(job_id) is what "active probe idempotent" means concretely at the
-- storage layer: the exact same durable job can never produce two attempt
-- rows, no matter how many times its own lease is lost and its claim
-- retried after a crash between the second CAS transaction committing and
-- the job being marked SUCCEEDED (V3-02's own handler detects that already-
-- resolved state and returns early rather than re-running the probe or
-- re-inserting a row). RetryRepositoryProbe always mints a brand new
-- durable_jobs row for a genuinely new attempt (never resurrects a dead
-- one), so this constraint never blocks a real, intentional retry -- it
-- only ever blocks a duplicate write for one specific job.
--
-- result/error_code/error_message/base_commit/dirty are all nullable
-- because exactly one pair is ever populated for a given row: a BLOCKED
-- result carries error_code/error_message and leaves base_commit/dirty
-- NULL; an ACTIVE result carries base_commit/dirty and leaves error_code/
-- error_message NULL. error_code is one of apperror's typed Code strings
-- (internal/app/apperror) -- never a raw SQL/provider error string
-- (docs/architecture/04-go-core-spec.md §18's "Error trả UI/CLI không
-- chứa SQL, argv secret, environment, token hoặc raw provider output").
CREATE TABLE repository_probe_attempts (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    job_id TEXT NOT NULL REFERENCES durable_jobs(id),
    state TEXT NOT NULL CHECK (state IN ('SUCCEEDED','FAILED')),
    result TEXT CHECK (result IN ('ACTIVE','BLOCKED')),
    error_code TEXT,
    error_message TEXT,
    base_commit TEXT,
    dirty INTEGER CHECK (dirty IN (0,1)),
    created_at TEXT NOT NULL,
    UNIQUE (job_id)
);

CREATE INDEX repository_probe_attempts_repository_idx
    ON repository_probe_attempts(repository_id, created_at);
