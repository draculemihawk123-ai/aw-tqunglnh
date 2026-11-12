// Package secretstore is V12-29 (docs/design/15-v12-ui-operations.md, UX-34, ADR-061): the secrets of a repository — what its gates,
// commands and QA app (ADR-062) need — stored encrypted (AES-256-GCM, the repository id and the name as additional data) under a data
// key the operating system protects (ports.SecretKeyProvider), never returned by any read: a caller sees names only. A COMMAND or gate
// resolves them by name through Resolver (the store first, then the worker's environment, as before); a resolved value is added to the
// execution's redaction, and never reaches a prompt, a context snapshot, evidence or a log.
package secretstore

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// MaxValueBytes bounds a value.
const MaxValueBytes = 64 << 10

// Errors.
var (
	ErrInvalidName  = errors.New("secretstore: a secret name is an environment variable name: letters, digits and _, not starting with a digit, at most 128 characters")
	ErrInvalidValue = errors.New("secretstore: a secret value must be 1 to 65536 bytes with no NUL")
	ErrNoSecret     = errors.New("secretstore: no such secret")
)

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ValidateName checks a secret name.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

func aad(repositoryID, name string) []byte {
	return []byte("aw-secret\x00" + repositoryID + "\x00" + name)
}

func seal(ctx context.Context, keys ports.SecretKeyProvider, repositoryID, name, value string) (ciphertext, nonce []byte, keyID string, err error) {
	key, keyID, err := keys.DataKey(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, "", err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, "", err
	}
	return gcm.Seal(nil, nonce, []byte(value), aad(repositoryID, name)), nonce, keyID, nil
}

func open(ctx context.Context, keys ports.SecretKeyProvider, rec ports.RepositorySecretRecord) (string, error) {
	key, keyID, err := keys.DataKey(ctx)
	if err != nil {
		return "", err
	}
	if keyID != rec.KeyID {
		return "", fmt.Errorf("%w: secret %s was stored under key %s, this machine has %s", ports.ErrSecretKeyUnavailable, rec.Name, rec.KeyID, keyID)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, rec.Nonce, rec.Ciphertext, aad(rec.RepositoryID, rec.Name))
	if err != nil {
		return "", fmt.Errorf("secretstore: secret %s does not decrypt (wrong key or altered row)", rec.Name)
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SecretName is what a read shows: the name, never the value.
type SecretName struct {
	Name      string    `json:"name"`
	Version   uint64    `json:"version"`
	UpdatedBy string    `json:"updatedBy"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SecretList is a repository's secret names and how the key is protected.
type SecretList struct {
	RepositoryID string       `json:"repositoryId"`
	Protection   string       `json:"protection"`
	Secrets      []SecretName `json:"secrets"`
}

// Deps is what the commands need.
type Deps struct {
	UnitOfWork ports.UnitOfWork
	Keys       ports.SecretKeyProvider
}

func checkRepository(ctx context.Context, tx ports.Tx, projectID, repositoryID string) error {
	repo, err := tx.Catalog().GetRepository(ctx, repositoryID)
	if err != nil {
		return err
	}
	if string(repo.ProjectID) != projectID {
		return fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, repositoryID)
	}
	return nil
}

// List reads a repository's secret names.
func List(ctx context.Context, d Deps, projectID, repositoryID string) (SecretList, error) {
	out := SecretList{RepositoryID: repositoryID, Secrets: []SecretName{}}
	if d.Keys != nil {
		out.Protection = d.Keys.Kind()
	}
	err := d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		if err := checkRepository(ctx, tx, projectID, repositoryID); err != nil {
			return err
		}
		records, err := tx.Secrets().ListRepositorySecrets(ctx, repositoryID)
		for _, r := range records {
			out.Secrets = append(out.Secrets, SecretName{Name: r.Name, Version: r.Version, UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt})
		}
		return err
	})
	return out, err
}

// SetResult names what was stored (never the value).
type SetResult struct {
	RepositoryID string   `json:"repositoryId"`
	Names        []string `json:"names"`
	Removed      bool     `json:"removed,omitempty"`
}

func replay(ctx context.Context, tx ports.Tx, cmd ports.Command, out any) (bool, error) {
	receipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil || !found {
		return false, err
	}
	if receipt.RequestHash != cmd.RequestHash {
		return false, ports.ErrReceiptConflict
	}
	return true, json.Unmarshal([]byte(receipt.ResultJSON), out)
}

func record(ctx context.Context, tx ports.Tx, cmd ports.Command, result any) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return tx.Receipts().Record(ctx, ports.Receipt{
		Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
		RequestHash: cmd.RequestHash, ResultJSON: string(body), CreatedAt: cmd.RequestedAt,
	})
}

// Set stores (or replaces) secrets of a repository — one, or the variables of a .env file. The caller's command hash must not hold the
// values in clear (the HTTP and CLI layers hash a digest).
func Set(ctx context.Context, d Deps, cmd ports.Command, projectID, repositoryID string, values map[string]string) (SetResult, error) {
	if len(values) == 0 {
		return SetResult{}, ErrInvalidValue
	}
	names := make([]string, 0, len(values))
	for name, value := range values {
		if err := ValidateName(name); err != nil {
			return SetResult{}, fmt.Errorf("%w (%q)", err, name)
		}
		if value == "" || len(value) > MaxValueBytes || strings.ContainsRune(value, 0) {
			return SetResult{}, fmt.Errorf("%w (%s)", ErrInvalidValue, name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if d.Keys == nil {
		return SetResult{}, ports.ErrSecretKeyUnavailable
	}
	// Encrypt outside the transaction (the key may come from an OS store).
	sealed := map[string]ports.RepositorySecretRecord{}
	for _, name := range names {
		ciphertext, nonce, keyID, err := seal(ctx, d.Keys, repositoryID, name, values[name])
		if err != nil {
			return SetResult{}, err
		}
		sealed[name] = ports.RepositorySecretRecord{ProjectID: projectID, RepositoryID: repositoryID, Name: name, Ciphertext: ciphertext, Nonce: nonce, KeyID: keyID,
			UpdatedBy: cmd.Actor, UpdatedAt: cmd.RequestedAt}
	}
	var result SetResult
	err := d.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if done, err := replay(ctx, tx, cmd, &result); err != nil || done {
			return err
		}
		if err := checkRepository(ctx, tx, projectID, repositoryID); err != nil {
			return err
		}
		for _, name := range names {
			var expected uint64
			if existing, err := tx.Secrets().GetRepositorySecret(ctx, repositoryID, name); err == nil {
				expected = existing.Version
			} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
				return err
			}
			if _, err := tx.Secrets().PutRepositorySecret(ctx, sealed[name], expected); err != nil {
				return err
			}
		}
		result = SetResult{RepositoryID: repositoryID, Names: names}
		return record(ctx, tx, cmd, result)
	})
	return result, err
}

// Delete removes a secret.
func Delete(ctx context.Context, d Deps, cmd ports.Command, projectID, repositoryID, name string) (SetResult, error) {
	var result SetResult
	err := d.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if done, err := replay(ctx, tx, cmd, &result); err != nil || done {
			return err
		}
		if err := checkRepository(ctx, tx, projectID, repositoryID); err != nil {
			return err
		}
		if _, err := tx.Secrets().GetRepositorySecret(ctx, repositoryID, name); err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				return fmt.Errorf("%w: %s", ErrNoSecret, name)
			}
			return err
		}
		if err := tx.Secrets().DeleteRepositorySecret(ctx, repositoryID, name); err != nil {
			return err
		}
		result = SetResult{RepositoryID: repositoryID, Names: []string{name}, Removed: true}
		return record(ctx, tx, cmd, result)
	})
	return result, err
}

// ParseDotEnv reads a .env file: NAME=value lines, `export ` allowed, # comments, values in single or double quotes (double quotes
// understand \n, \" and \\). A malformed line is an error naming its line number (never its content: it may hold a value).
func ParseDotEnv(text string) (map[string]string, error) {
	out := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || ValidateName(name) != nil {
			return nil, fmt.Errorf("secretstore: .env line %d is not NAME=value", n)
		}
		value = strings.TrimSpace(value)
		switch {
		case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
			value = strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(value[1 : len(value)-1])
		case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
			value = value[1 : len(value)-1]
		default:
			if i := strings.Index(value, " #"); i >= 0 {
				value = strings.TrimSpace(value[:i])
			}
		}
		if value == "" {
			continue // an empty value is not a secret; it is left out
		}
		out[name] = value
	}
	return out, scanner.Err()
}

// Resolver implements ports.ScopedSecretResolver: the repository store first, in the order given, then Fallback (the worker's
// environment). The unscoped Resolve is Fallback alone.
type Resolver struct {
	Deps     Deps
	Fallback ports.SecretResolver
}

var _ ports.ScopedSecretResolver = Resolver{}

// Resolve implements ports.SecretResolver.
func (r Resolver) Resolve(ctx context.Context, name string) (string, error) {
	if r.Fallback == nil {
		return "", fmt.Errorf("%w: %s", ErrNoSecret, name)
	}
	return r.Fallback.Resolve(ctx, name)
}

// ResolveScoped implements ports.ScopedSecretResolver.
func (r Resolver) ResolveScoped(ctx context.Context, repositoryIDs []string, name string) (string, error) {
	var found *ports.RepositorySecretRecord
	if err := r.Deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		for _, id := range repositoryIDs {
			rec, err := tx.Secrets().GetRepositorySecret(ctx, id, name)
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			found = &rec
			return nil
		}
		return nil
	}); err != nil {
		return "", err
	}
	if found == nil {
		return r.Resolve(ctx, name)
	}
	if r.Deps.Keys == nil {
		return "", ports.ErrSecretKeyUnavailable
	}
	return open(ctx, r.Deps.Keys, *found)
}
