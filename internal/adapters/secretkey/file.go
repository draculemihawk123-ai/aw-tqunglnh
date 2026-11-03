package secretkey

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// FileProvider keeps the key in a 0600 file. On Windows the file content is a DPAPI blob instead (see dpapi_windows.go): on NTFS a
// 0600 mode protects nothing.
type FileProvider struct {
	Path string
	// protect/unprotect wrap the key before it is written and after it is read (DPAPI on Windows; nil elsewhere).
	protect, unprotect func([]byte) ([]byte, error)
	kind               string
}

// NewFileProvider keeps the key in <root>/secret-store.key.
func NewFileProvider(root string) *FileProvider {
	return &FileProvider{Path: filepath.Join(root, "secret-store.key"), kind: "file"}
}

// Kind implements ports.SecretKeyProvider.
func (p *FileProvider) Kind() string { return p.kind }

// DataKey implements ports.SecretKeyProvider.
func (p *FileProvider) DataKey(context.Context) ([]byte, string, error) {
	raw, err := os.ReadFile(p.Path)
	switch {
	case err == nil:
		key := raw
		if p.unprotect != nil {
			if key, err = p.unprotect(raw); err != nil {
				return nil, "", fmt.Errorf("%w: %v", ports.ErrSecretKeyUnavailable, err)
			}
		}
		if len(key) != KeyBytes {
			return nil, "", fmt.Errorf("%w: %s is not a %d-byte key", ports.ErrSecretKeyUnavailable, p.Path, KeyBytes)
		}
		return key, keyID(p.kind, key), nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, "", fmt.Errorf("%w: %v", ports.ErrSecretKeyUnavailable, err)
	}
	key, err := newKey()
	if err != nil {
		return nil, "", err
	}
	stored := key
	if p.protect != nil {
		if stored, err = p.protect(key); err != nil {
			return nil, "", fmt.Errorf("secretkey: protect the data key: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
		return nil, "", err
	}
	// O_EXCL: two processes starting at once never write two different keys.
	f, err := os.OpenFile(p.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return p.DataKey(context.Background())
	}
	if err != nil {
		return nil, "", err
	}
	if _, err := f.Write(stored); err != nil {
		f.Close()
		return nil, "", err
	}
	if err := f.Close(); err != nil {
		return nil, "", err
	}
	return key, keyID(p.kind, key), nil
}
