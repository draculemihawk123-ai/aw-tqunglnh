-- V11-11 (docs/design/14-v11-bugfix.md, ADR-042): the engine writes its own milestones into a work item's task chat (a node started, the
-- agent's last words, how a node ended, an approval waiting). Two columns on messages:
--
-- display_only: such a message is for people. The context scheduler leaves it out of the messages block of every later prompt, so turning
-- the feature on cannot change what an agent is shown (no extra tokens, no feedback loop). 0 for every existing message and for every
-- message a person or an API client appends.
--
-- node_run_id: the node run the message is about (nullable; a person's message has none), so the chat can link to the node's progress.
-- attempt_id already exists for the attempt.
ALTER TABLE messages ADD COLUMN display_only INTEGER NOT NULL DEFAULT 0 CHECK (display_only IN (0, 1));
ALTER TABLE messages ADD COLUMN node_run_id TEXT REFERENCES node_runs(id);
