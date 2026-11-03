//go:build darwin

package secretkey

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// platformProvider on macOS: the login Keychain (`security`), one generic password per installation.
func platformProvider(root string) ports.SecretKeyProvider {
	return &cached{inner: keychainProvider{account: account(root)}}
}

type keychainProvider struct{ account string }

func (keychainProvider) Kind() string { return "keychain" }

func (k keychainProvider) DataKey(ctx context.Context) ([]byte, string, error) {
	out, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", service, "-a", k.account, "-w").Output()
	if err == nil {
		key, decodeErr := hex.DecodeString(strings.TrimSpace(string(out)))
		if decodeErr != nil || len(key) != KeyBytes {
			return nil, "", fmt.Errorf("%w: the Keychain item %s/%s is not a data key", ports.ErrSecretKeyUnavailable, service, k.account)
		}
		return key, keyID("keychain", key), nil
	}
	key, err := newKey()
	if err != nil {
		return nil, "", err
	}
	// -w reads the password from the next argument; -U updates an item that appeared meanwhile.
	cmd := exec.CommandContext(ctx, "security", "add-generic-password", "-s", service, "-a", k.account, "-l", "aw secret store", "-U", "-w", hex.EncodeToString(key))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("%w: store the data key in the Keychain: %v %s", ports.ErrSecretKeyUnavailable, err, strings.TrimSpace(stderr.String()))
	}
	return key, keyID("keychain", key), nil
}
