package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// secretsRepository implements ports.SecretsRepository (V12-29, migration 0062).
type secretsRepository struct {
	tx *sql.Tx
}

const secretColumns = `project_id, repository_id, name, value_ciphertext, value_nonce, key_id, version, updated_by, updated_at`

func scanSecret(row repositoryRowScanner) (ports.RepositorySecretRecord, error) {
	var r ports.RepositorySecretRecord
	var updatedAt string
	if err := row.Scan(&r.ProjectID, &r.RepositoryID, &r.Name, &r.Ciphertext, &r.Nonce, &r.KeyID, &r.Version, &r.UpdatedBy, &updatedAt); err != nil {
		return ports.RepositorySecretRecord{}, err
	}
	var err error
	r.UpdatedAt, err = parseWorkflowTime(updatedAt)
	return r, err
}

func (s secretsRepository) GetRepositorySecret(ctx context.Context, repositoryID, name string) (ports.RepositorySecretRecord, error) {
	r, err := scanSecret(s.tx.QueryRowContext(ctx, `SELECT `+secretColumns+` FROM repository_secrets WHERE repository_id = ? AND name = ?`, repositoryID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return ports.RepositorySecretRecord{}, fmt.Errorf("%w: secret %s of %s", ports.ErrPersistenceNotFound, name, repositoryID)
	}
	if err != nil {
		return ports.RepositorySecretRecord{}, MapSQLiteError(fmt.Errorf("read secret: %w", err))
	}
	return r, nil
}

func (s secretsRepository) PutRepositorySecret(ctx context.Context, r ports.RepositorySecretRecord, expectedVersion uint64) (ports.RepositorySecretRecord, error) {
	existing, err := s.GetRepositorySecret(ctx, r.RepositoryID, r.Name)
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		if expectedVersion != 0 {
			return ports.RepositorySecretRecord{}, fmt.Errorf("%w: secret %s", ports.ErrOptimisticConflict, r.Name)
		}
		r.Version = 1
		if _, err := s.tx.ExecContext(ctx, `INSERT INTO repository_secrets (`+secretColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ProjectID, r.RepositoryID, r.Name, r.Ciphertext, r.Nonce, r.KeyID, r.Version, r.UpdatedBy, formatWorkflowTime(r.UpdatedAt)); err != nil {
			return ports.RepositorySecretRecord{}, MapSQLiteError(fmt.Errorf("insert secret: %w", err))
		}
		return r, nil
	case err != nil:
		return ports.RepositorySecretRecord{}, err
	}
	if existing.Version != expectedVersion {
		return ports.RepositorySecretRecord{}, fmt.Errorf("%w: secret %s is at version %d", ports.ErrOptimisticConflict, r.Name, existing.Version)
	}
	r.Version = existing.Version + 1
	if _, err := s.tx.ExecContext(ctx, `UPDATE repository_secrets SET value_ciphertext = ?, value_nonce = ?, key_id = ?, version = ?, updated_by = ?, updated_at = ?
WHERE repository_id = ? AND name = ? AND version = ?`, r.Ciphertext, r.Nonce, r.KeyID, r.Version, r.UpdatedBy, formatWorkflowTime(r.UpdatedAt), r.RepositoryID, r.Name, expectedVersion); err != nil {
		return ports.RepositorySecretRecord{}, MapSQLiteError(fmt.Errorf("update secret: %w", err))
	}
	return r, nil
}

func (s secretsRepository) DeleteRepositorySecret(ctx context.Context, repositoryID, name string) error {
	if _, err := s.tx.ExecContext(ctx, `DELETE FROM repository_secrets WHERE repository_id = ? AND name = ?`, repositoryID, name); err != nil {
		return MapSQLiteError(fmt.Errorf("delete secret: %w", err))
	}
	return nil
}

func (s secretsRepository) ListRepositorySecrets(ctx context.Context, repositoryID string) ([]ports.RepositorySecretRecord, error) {
	rows, err := s.tx.QueryContext(ctx, `SELECT `+secretColumns+` FROM repository_secrets WHERE repository_id = ? ORDER BY name`, repositoryID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list secrets: %w", err))
	}
	defer rows.Close()
	var out []ports.RepositorySecretRecord
	for rows.Next() {
		r, err := scanSecret(rows)
		if err != nil {
			return nil, MapSQLiteError(fmt.Errorf("scan secret: %w", err))
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
