//go:build !windows

package repoprobe

import "path/filepath"

// canonicalExistingPath resolves path's real, symlink-free form —
// duplicated from internal/adapters/gitworktree's own
// canonical_other.go; see canonical_windows.go's doc comment for why this
// is a deliberate small duplication rather than a shared package.
func canonicalExistingPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
