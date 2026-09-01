DROP TABLE domain_events;

CREATE TABLE domain_events (
    id TEXT PRIMARY KEY,
    project_id TEXT,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    journal_position INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    payload_json TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (aggregate_type, aggregate_id, sequence),
    UNIQUE (journal_position)
);

CREATE TABLE outbox (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE REFERENCES domain_events(id),
    topic TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'AVAILABLE',
    available_at TEXT NOT NULL,
    lease_owner TEXT,
    lease_token INTEGER NOT NULL DEFAULT 0,
    lease_until TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_outbox_status_available ON outbox(status, available_at);
