-- V11-10 (docs/design/14-v11-bugfix.md, ADR-041): "redo with the newest workflow version". A WorkItem pins one workflow
-- version for good, so redoing a task after the definition changed cancels the old WorkItem and creates a new sibling with
-- the same content; this table is the link between the two, so the Board can say "replaces X" on the new one and
-- "replaced by Y" on the old one.
--
-- new_work_item_id is the primary key (a WorkItem replaces at most one other) and old_work_item_id is UNIQUE (a WorkItem is
-- replaced at most once): redos form a chain. Both reference work_items, and the pair is written in the same transaction
-- that creates the new WorkItem, so a row never names a WorkItem that does not exist.
CREATE TABLE work_item_supersessions (
    new_work_item_id TEXT PRIMARY KEY REFERENCES work_items(id),
    old_work_item_id TEXT NOT NULL UNIQUE REFERENCES work_items(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    actor TEXT NOT NULL CHECK (length(trim(actor)) > 0),
    reason TEXT NOT NULL CHECK (length(trim(reason)) > 0),
    created_at TEXT NOT NULL,
    CHECK (new_work_item_id <> old_work_item_id)
);
