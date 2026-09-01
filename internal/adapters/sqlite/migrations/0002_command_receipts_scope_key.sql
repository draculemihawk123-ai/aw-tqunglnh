DROP TABLE command_receipts;

CREATE TABLE command_receipts (
    actor TEXT NOT NULL,
    scope_key TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    command_type TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    result_json TEXT,
    error_code TEXT,
    created_at TEXT NOT NULL,
    PRIMARY KEY (actor, scope_key, idempotency_key, command_type)
);
