-- V12-18 (docs/design/15-v12-ui-operations.md, UX-20, ADR-053): an operator's message can be aimed. Two columns on messages:
--
-- target_node_key: the message reaches only the prompts of attempts of the node with this key (NULL = every node that reads the
-- chat, as before). A note written for `repro` ("do not change product code") no longer reaches `fix`.
--
-- next_attempt_only: the message reaches only the first attempt scheduled after it (of the target node when one is set). Whether it
-- was delivered is read from the attempts' context snapshots (message_refs_json, omitted_message_refs_json), never stored here: a
-- Message stays append-only.
--
-- Plain ADD COLUMN, no rebuild: NULL and 0 keep every existing message exactly as it was.
ALTER TABLE messages ADD COLUMN target_node_key TEXT;
ALTER TABLE messages ADD COLUMN next_attempt_only INTEGER NOT NULL DEFAULT 0 CHECK (next_attempt_only IN (0, 1));
