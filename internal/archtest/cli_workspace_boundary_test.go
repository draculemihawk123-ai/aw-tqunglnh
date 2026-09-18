// This file is V6-15L's own architecture proof
// (docs/design/08-v6-api-projections.md V6-15L's own Không làm line: "no
// Git command, arbitrary path or interactive terminal" — and its own
// Verify line's "architecture" bullet). Mirrors
// internal/archtest/workspace_delivery_boundary_test.go's and
// internal/archtest/workspace_inspection_http_test.go's identical "parse
// real source, walk the import list, fail on a forbidden import" idiom,
// scoped to exactly this task's own new package,
// internal/delivery/cli/workspace.
//
// This is deliberately a fresh AST walk scoped to ONLY this package's own
// directory (never internal/delivery/cli/workspace/... transitively, and
// never internal/delivery/cli itself) — internal/delivery/cli/output.go's
// own WriteBinaryOutput legitimately imports "os" to stream
// `repository-workspace source --output <path>` to a real file, and this
// package legitimately calls that function; a transitive check (the way
// internal/archtest/cli_boundary_test.go's own
// TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters walks the whole
// internal/delivery/cli/... tree's DEPENDENCY closure for adapter
// packages) would therefore produce a false positive on "os" specifically
// for this package if scoped that way — see that file's own doc comment
// for the identical reasoning. Scoping the walk to this package's own
// DIRECT imports only (not the whole tree's dependency closure) avoids
// that false positive while still proving this package's own code never
// itself imports "os"/"os/exec" or an internal/adapters/... package.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeliveryCLIWorkspaceNeverImportsFilesystemOrProcess walks every
// non-test .go file directly inside internal/delivery/cli/workspace (not
// its own subdirectories — it has none) and fails if any of them imports
// "os", "os/exec", or any package under
// github.com/taQuangLing/agent-workflow/internal/adapters — no direct
// Git/filesystem/process reach from this CLI leaf, mirroring
// internal/archtest/workspace_inspection_http_test.go's own identical
// "no adapter reach" proof for that leaf's own HTTP counterpart.
func TestDeliveryCLIWorkspaceNeverImportsFilesystemOrProcess(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "cli", "workspace")

	forbiddenImportLiterals := map[string]bool{`"os"`: true, `"os/exec"`: true}
	const forbiddenImportPrefix = "agent-workflow/internal/adapters"

	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		checked++

		for _, imp := range file.Imports {
			if forbiddenImportLiterals[imp.Path.Value] || strings.Contains(imp.Path.Value, forbiddenImportPrefix) {
				t.Errorf("%s imports %s — internal/delivery/cli/workspace must never reach the filesystem, a process, or a concrete Git/persistence adapter directly (V6-15L's own \"Không làm\": no Git command, arbitrary path or interactive terminal)",
					path, imp.Path.Value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("no internal/delivery/cli/workspace files were checked — this test needs updating alongside the implementation")
	}
}
