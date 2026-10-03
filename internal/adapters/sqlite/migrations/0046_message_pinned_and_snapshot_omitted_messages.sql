-- messages.pinned and attempt_context_snapshots.omitted_message_refs_json:
-- V9-07's message budget (docs/design/12-v9-harness-alignment.md V9-07, gap G7).
--
-- A CONTEXT policy may now declare `messages: {maxBytes, keepLatest}`. When it
-- does, the scheduler keeps the newest keepLatest messages and every pinned
-- message in the attempt's prompt, fills what is left of maxBytes with older
-- messages newest first, and turns the rest into references. Two facts have to
-- be durable for that to be auditable and repeatable:
--
-- 1. messages.pinned: which messages the operator pinned. It is chosen when a
--    message is appended and never changes (a Message is append-only), so a
--    plain NOT NULL column with a default is the whole change; every message
--    written before this migration is simply not pinned. The CHECK keeps the
--    column a boolean.
--
-- 2. attempt_context_snapshots.omitted_message_refs_json: the messages the
--    budget left out of that snapshot's message_refs_json, each with the
--    reason (contextsnapshot.OmittedMessageRef). NULL means "none omitted":
--    every snapshot written before this migration, and every snapshot whose
--    policy declared no `messages` budget. The value takes part in the
--    snapshot's manifest_hash (contextsnapshot.canonicalManifest, omitted
--    while empty), so the tamper check on load covers it and a snapshot
--    without omissions recomputes exactly the hash it was written with.
--
-- Plain ADD COLUMN, no rebuild, no backfill: both columns are valid for the
-- rows that already exist (the same shape as migrations 0018, 0042 and 0044).
ALTER TABLE messages ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1));

ALTER TABLE attempt_context_snapshots ADD COLUMN omitted_message_refs_json TEXT;
