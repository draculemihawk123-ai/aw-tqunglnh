package ports

import (
	"context"
	"errors"
	"time"
)

// V12-29 (ADR-061): the repository secret store.

// RepositorySecretRecord is one stored secret: its value only as ciphertext.
type RepositorySecretRecord struct {
	ProjectID    string
	RepositoryID string
	Name         string
	Ciphertext   []byte
	Nonce        []byte
	KeyID        string
	Version      uint64
	UpdatedBy    string
	UpdatedAt    time.Time
}

// SecretsRepository is the Tx accessor of repository secrets.
type SecretsRepository interface {
	GetRepositorySecret(ctx context.Context, repositoryID, name string) (RepositorySecretRecord, error)
	// PutRepositorySecret inserts (expectedVersion 0) or replaces (the stored version; ErrOptimisticConflict otherwise) the secret.
	PutRepositorySecret(ctx context.Context, record RepositorySecretRecord, expectedVersion uint64) (RepositorySecretRecord, error)
	DeleteRepositorySecret(ctx context.Context, repositoryID, name string) error
	// ListRepositorySecrets returns the repository's secrets, by name (the ciphertext included; a caller shows names only).
	ListRepositorySecrets(ctx context.Context, repositoryID string) ([]RepositorySecretRecord, error)
}

// SecretKeyProvider holds the data key the store encrypts with, outside the database, protected by the operating system.
type SecretKeyProvider interface {
	// DataKey returns the 32-byte data key and its id, creating it on first use.
	DataKey(ctx context.Context) (key []byte, keyID string, err error)
	// Kind names the protection ("dpapi", "keychain", "secret-service", "file").
	Kind() string
}

// ScopedSecretResolver resolves a secret for a command that runs in some repositories: the repository store first (in the given
// order), then whatever an unscoped SecretResolver resolves (the worker's environment).
type ScopedSecretResolver interface {
	SecretResolver
	ResolveScoped(ctx context.Context, repositoryIDs []string, name string) (string, error)
}

// ErrSecretKeyUnavailable: the data key cannot be read (another user, another machine, a removed key file).
var ErrSecretKeyUnavailable = errors.New("ports: the secret store's data key is not available on this machine")
