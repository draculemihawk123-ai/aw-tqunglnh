// Package secretkey keeps the repository secret store's data key (V12-29, ADR-061) outside the database, protected by the operating
// system: Windows DPAPI (CurrentUser), macOS Keychain, Linux Secret Service when it is there, otherwise a 0600 key file. The key is 32
// random bytes, made on first use. Whoever holds the database alone cannot decrypt a secret.
package secretkey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// KeyBytes is the AES-256 key size.
const KeyBytes = 32

// service names the key in the OS stores.
const service = "aw-secret-store"

// account identifies this installation in an OS store: two installations on one machine get two keys.
func account(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	sum := sha256.Sum256([]byte(abs))
	return "aw-" + hex.EncodeToString(sum[:8])
}

func newKey() ([]byte, error) {
	key := make([]byte, KeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("secretkey: generate a data key: %w", err)
	}
	return key, nil
}

func keyID(kind string, key []byte) string {
	sum := sha256.Sum256(key)
	return kind + ":" + hex.EncodeToString(sum[:6])
}

// New returns the provider for this operating system; root is the installation's artifact root (where a key file lives).
func New(root string) ports.SecretKeyProvider {
	return platformProvider(root)
}

type cached struct {
	inner ports.SecretKeyProvider
	key   []byte
	id    string
}

func (c *cached) Kind() string { return c.inner.Kind() }

func (c *cached) DataKey(ctx context.Context) ([]byte, string, error) {
	if c.key != nil {
		return c.key, c.id, nil
	}
	key, id, err := c.inner.DataKey(ctx)
	if err != nil {
		return nil, "", err
	}
	c.key, c.id = key, id
	return key, id, nil
}
