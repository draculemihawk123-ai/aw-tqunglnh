// Package embeddedui is V8-08's own "build UI và đóng gói vào binary
// `aw`/`aw.exe`" (docs/design/10-v8-alpha-hardening.md V8-08): a real,
// compiled-in copy of the V7 UI's own `pnpm build` output, so a genuine
// release artifact of this binary never needs a separately-shipped
// `web/dist` directory alongside it the way `aw serve --ui-dist` (V7-02A)
// still requires for local development.
//
// dist/index.html is checked into version control as a deliberate,
// recognizable PLACEHOLDER (see its own comment) so every ordinary `go
// build`/`go vet`/`go test` in this repo keeps compiling unchanged without
// ever needing to run `pnpm build` first — `//go:embed` requires at least
// one real file to exist at compile time. cmd/aw-release-build (V8-08's own
// release build tool) replaces the WHOLE dist/ directory with a real `pnpm
// build` output immediately before compiling a release binary; Extract
// tells the two cases apart at runtime by checking for that same
// placeholder marker, never by a separate build tag or flag a normal `go
// build` of this repo could silently get wrong.
package embeddedui

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// placeholderMarker must appear verbatim in dist/index.html's own checked-
// in placeholder content — matches dist/index.html's own comment.
const placeholderMarker = "AGENT-WORKFLOW-EMBEDDED-UI-PLACEHOLDER"

// IsEmbedded reports whether this binary carries a real embedded UI build,
// without extracting anything to disk — the cheap check `aw version
// --json`'s own manifest (V8-08's "version/schema/adapter manifest" bar)
// needs, kept separate from Extract so reporting the manifest never costs a
// throwaway temp-directory write.
func IsEmbedded() (bool, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return false, fmt.Errorf("embeddedui: sub dist: %w", err)
	}
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return false, fmt.Errorf("embeddedui: read index.html: %w", err)
	}
	return !strings.Contains(string(data), placeholderMarker), nil
}

// Extract reports whether this binary carries a REAL embedded UI build
// (embedded == true) and, if so, materializes it under destDir (creating
// destDir if needed) in exactly the shape `aw serve --ui-dist` already
// expects (index.html + assets/) — the caller can then treat destDir
// exactly like a real --ui-dist value. embedded == false (this binary was
// built the ordinary way, without cmd/aw-release-build) leaves destDir
// untouched and returns nil/false/nil, the safe default matching aw
// serve's own pre-V8-08 "no --ui-dist given" behavior.
func Extract(destDir string) (indexHTML []byte, embedded bool, err error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, false, fmt.Errorf("embeddedui: sub dist: %w", err)
	}
	return extractFS(sub, destDir)
}

// extractFS is Extract's own pure, testable core: fsys stands in for
// distFS's own "dist" subtree, so a unit test can simulate both the
// checked-in placeholder and a real build output via fstest.MapFS,
// without needing a second real compiled-in fixture tree.
func extractFS(fsys fs.FS, destDir string) (indexHTML []byte, embedded bool, err error) {
	data, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return nil, false, fmt.Errorf("embeddedui: read index.html: %w", err)
	}
	if strings.Contains(string(data), placeholderMarker) {
		return nil, false, nil
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, false, fmt.Errorf("embeddedui: create %s: %w", destDir, err)
	}
	walkErr := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		target := filepath.Join(destDir, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, readErr := fs.ReadFile(fsys, path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, content, 0o644)
	})
	if walkErr != nil {
		return nil, false, fmt.Errorf("embeddedui: extract to %s: %w", destDir, walkErr)
	}
	return data, true, nil
}
