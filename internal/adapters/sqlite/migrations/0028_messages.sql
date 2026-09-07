-- messages: the durable, append-only task-chat row (V5-02,
-- docs/design/07-v5-execution-evidence.md; HE-02-M05, HE-05-M07) — the
-- canonical message store this platform owns instead of trusting any CLI
-- provider's own session transcript. "Conversation" is not a separate row
-- here (go-core-spec §4.6's own sketch has none): every Message sharing
-- one work_item_id, ordered by sequence, IS the conversation.
--
-- content_artifact_id always references a durable, hash-verified
-- artifacts row (V5-01, migration 0027) — this table never inlines
-- message content itself (HE-11-S06: "DB chỉ giữ metadata... ArtifactStore
-- chỉ giữ content").
--
-- sequence is resolved by the application — MAX(sequence)+1 scoped to
-- work_item_id, computed and used inside the SAME transaction as the
-- INSERT (see internal/adapters/sqlite/message_repository.go) — never
-- client-supplied and never a separate allocator table. UNIQUE(work_item_id,
-- sequence) is a defense-in-depth backstop: WithSerializedWrite's own
-- single-writer BEGIN IMMEDIATE semantics already prevent a real race, this
-- constraint just makes a logic bug fail loudly instead of silently
-- duplicating a sequence number.
--
-- attempt_id is nullable: a message with no execution context yet (e.g. a
-- human's initial task description before any Run starts) leaves it NULL.
--
-- No version column: a Message is never updated or deleted once inserted
-- (this task's own "task chat append-only thuộc platform" goal) — the same
-- append-only, no-update-path discipline decision_artifacts already uses.
CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    attempt_id TEXT REFERENCES execution_attempts(id),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    actor TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('USER', 'ASSISTANT', 'SYSTEM', 'TOOL')),
    content_artifact_id TEXT NOT NULL REFERENCES artifacts(id),
    correlation_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (work_item_id, sequence)
);

CREATE INDEX idx_messages_work_item_sequence ON messages(work_item_id, sequence);
