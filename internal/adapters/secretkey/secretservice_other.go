//go:build !windows && !darwin

package secretkey

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// platformProvider on Linux and other Unix: the Secret Service (`secret-tool`, GNOME Keyring / KWallet) when it answers, otherwise a
// 0600 key file next to the artifact root (a headless server usually has no Secret Service).
func platformProvider(root string) ports.SecretKeyProvider {
	return &cached{inner: &fallbackProvider{primary: secretServiceProvider{account: account(root)}, fallback: NewFileProvider(root)}}
}

type fallbackProvider struct {
	primary  secretServiceProvider
	fallback *FileProvider
	chosen   ports.SecretKeyProvider
}

func (f *fallbackProvider) Kind() string {
	if f.chosen != nil {
		return f.chosen.Kind()
	}
	if f.primary.available() {
		return f.primary.Kind()
	}
	return f.fallback.Kind()
}

func (f *fallbackProvider) DataKey(ctx context.Context) ([]byte, string, error) {
	if f.chosen == nil {
		f.chosen = f.fallback
		if f.primary.available() {
			f.chosen = f.primary
		}
	}
	return f.chosen.DataKey(ctx)
}

type secretServiceProvider struct{ account string }

func (secretServiceProvider) Kind() string { return "secret-service" }

// available: secret-tool exists and the Secret Service answers a lookup (exit 0 found, 1 not found; anything else is no service).
func (s secretServiceProvider) available() bool {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, "secret-tool", "lookup", "service", service, "account", s.account).Run()
	if err == nil {
		return true
	}
	exitErr, ok := err.(*exec.ExitError)
	return ok && exitErr.ExitCode() == 1 && ctx.Err() == nil
}

func (s secretServiceProvider) DataKey(ctx context.Context) ([]byte, string, error) {
	out, err := exec.CommandContext(ctx, "secret-tool", "lookup", "service", service, "account", s.account).Output()
	if err == nil && len(bytes.TrimSpace(out)) > 0 {
		key, decodeErr := hex.DecodeString(strings.TrimSpace(string(out)))
		if decodeErr != nil || len(key) != KeyBytes {
			return nil, "", fmt.Errorf("%w: the Secret Service item %s/%s is not a data key", ports.ErrSecretKeyUnavailable, service, s.account)
		}
		return key, keyID("secret-service", key), nil
	}
	key, err := newKey()
	if err != nil {
		return nil, "", err
	}
	// The secret is read from stdin, never from argv.
	cmd := exec.CommandContext(ctx, "secret-tool", "store", "--label=aw secret store", "service", service, "account", s.account)
	cmd.Stdin = strings.NewReader(hex.EncodeToString(key))
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("%w: store the data key in the Secret Service: %v", ports.ErrSecretKeyUnavailable, err)
	}
	return key, keyID("secret-service", key), nil
}
