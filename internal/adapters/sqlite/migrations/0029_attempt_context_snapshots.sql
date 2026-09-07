-- attempt_context_snapshots: V5-04's own durable, immutable
-- message/resource/RevisionSet manifest bound to exactly one
-- ExecutionAttempt (docs/design/07-v5-execution-evidence.md V5-04;
-- GC-INV-08). This is a NEW, separate table from the pre-existing
-- context_snapshots table (migration 0001) that the legacy
-- runtime.ContextSnapshot type still owns for crash-recovery — the two
-- are deliberately kept apart (see internal/domain/contextsnapshot's own
-- package doc comment for why).
--
-- attempt_id is UNIQUE: each ExecutionAttempt has its own, separate
-- snapshot (never shared/reused across a retry's new Attempt — a
-- technical retry that needs the same effective context clones a NEW row
-- bound to its own new attempt_id instead).
--
-- FK ordering note: this table's own attempt_id REFERENCES
-- execution_attempts(id), so a row here can only be inserted AFTER its
-- owning ExecutionAttempt row already exists. execution_attempts'
-- own context_snapshot_id column (already present since migration 0001,
-- previously dormant) deliberately carries NO foreign key of its own —
-- adding one back into this table would create a circular FK the two
-- inserts could never satisfy in either order within one transaction.
-- The application layer is what enforces that pairing (see
-- internal/app/runtime/schedule.go's own insert-attempt-then-snapshot
-- ordering).
--
-- message_refs_json/resource_refs_json preserve exact ORDER (a JSON
-- array, not sorted) — HE-04-M06 requires this manifest to record the
-- order resources were actually rendered in, unlike revision_set_json
-- (order-independent, canonicalized via RevisionSet's own ContentHash).
CREATE TABLE attempt_context_snapshots (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    attempt_id TEXT NOT NULL REFERENCES execution_attempts(id),
    message_refs_json TEXT NOT NULL,
    resource_refs_json TEXT NOT NULL,
    revision_set_json TEXT NOT NULL,
    manifest_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (attempt_id)
);

CREATE INDEX idx_attempt_context_snapshots_work_item ON attempt_context_snapshots(work_item_id);
