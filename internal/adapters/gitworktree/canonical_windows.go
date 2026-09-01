//go:build windows

package gitworktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// filepath.EvalSymlinks can fail with Access Denied while walking protected
// Windows profile ancestors even when the configured directory itself is
// accessible. Resolving an opened directory handle provides the same reparse-
// point protection without requiring traversal rights on every ancestor.
func canonicalExistingPath(path string) (string, error) {
	directory, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer directory.Close()

	buffer := make([]uint16, 512)
	for {
		length, err := windows.GetFinalPathNameByHandle(
			windows.Handle(directory.Fd()),
			&buffer[0],
			uint32(len(buffer)),
			0,
		)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			resolved := windows.UTF16ToString(buffer[:length])
			resolved = normalizeWindowsDevicePath(resolved)
			return filepath.Clean(resolved), nil
		}
		if length > 32*1024 {
			return "", fmt.Errorf("resolved Windows path exceeds maximum length")
		}
		buffer = make([]uint16, length+1)
	}
}

func normalizeWindowsDevicePath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}
