-- V12-29 (docs/design/15-v12-ui-operations.md, UX-34, ADR-061): secrets of a repository — the values its gates, commands and the app it
-- runs for QA (ADR-062) need (a DB password, an R2 key), so they no longer sit in a file of the repository. One row per (repository,
-- name). value_ciphertext is AES-256-GCM under a data key that is NOT in this database (the operating system protects it: DPAPI,
-- Keychain, Secret Service, or a 0600 key file), with the repository id and the name as additional data, so a row copied to another
-- repository or name does not decrypt. A backup of the database alone does not reveal a value.
CREATE TABLE repository_secrets (
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    project_id TEXT NOT NULL REFERENCES projects(id),
    value_ciphertext BLOB NOT NULL,
    value_nonce BLOB NOT NULL,
    key_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    updated_by TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (repository_id, name)
);
