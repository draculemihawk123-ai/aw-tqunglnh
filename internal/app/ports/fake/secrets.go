package fake

import (
	"context"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// SecretsRepository is the in-memory ports.SecretsRepository (V12-29).
type SecretsRepository struct {
	secrets map[string]ports.RepositorySecretRecord
}

func secretKey(repositoryID, name string) string { return repositoryID + "\x00" + name }

func (r *SecretsRepository) clone() *SecretsRepository {
	out := &SecretsRepository{secrets: map[string]ports.RepositorySecretRecord{}}
	for k, v := range r.secrets {
		out.secrets[k] = v
	}
	return out
}

// GetRepositorySecret mirrors the SQLite repository.
func (r *SecretsRepository) GetRepositorySecret(_ context.Context, repositoryID, name string) (ports.RepositorySecretRecord, error) {
	s, ok := r.secrets[secretKey(repositoryID, name)]
	if !ok {
		return ports.RepositorySecretRecord{}, fmt.Errorf("fake: %w: secret %s", ports.ErrPersistenceNotFound, name)
	}
	return s, nil
}

// PutRepositorySecret mirrors the SQLite repository.
func (r *SecretsRepository) PutRepositorySecret(_ context.Context, rec ports.RepositorySecretRecord, expectedVersion uint64) (ports.RepositorySecretRecord, error) {
	if r.secrets == nil {
		r.secrets = map[string]ports.RepositorySecretRecord{}
	}
	existing, ok := r.secrets[secretKey(rec.RepositoryID, rec.Name)]
	if (!ok && expectedVersion != 0) || (ok && existing.Version != expectedVersion) {
		return ports.RepositorySecretRecord{}, fmt.Errorf("fake: %w: secret %s", ports.ErrOptimisticConflict, rec.Name)
	}
	rec.Version = expectedVersion + 1
	r.secrets[secretKey(rec.RepositoryID, rec.Name)] = rec
	return rec, nil
}

// DeleteRepositorySecret mirrors the SQLite repository.
func (r *SecretsRepository) DeleteRepositorySecret(_ context.Context, repositoryID, name string) error {
	delete(r.secrets, secretKey(repositoryID, name))
	return nil
}

// ListRepositorySecrets mirrors the SQLite repository.
func (r *SecretsRepository) ListRepositorySecrets(_ context.Context, repositoryID string) ([]ports.RepositorySecretRecord, error) {
	var out []ports.RepositorySecretRecord
	for _, s := range r.secrets {
		if s.RepositoryID == repositoryID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
