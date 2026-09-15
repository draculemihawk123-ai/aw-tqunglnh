// This file is V6-10D's own architecture proof
// (docs/design/08-v6-api-projections.md V6-10D's own Verify line: "an
// architecture test proving NO filesystem/process concrete import" — and
// its own Không làm line: "handler no file open, Git spawn, revision
// resolution or terminal"). Mirrors workspace_delivery_boundary_test.go's
// own TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor idiom
// (parse real source, walk the import list, fail on a forbidden import)
// scoped down to exactly this one task's own subpackage,
// internal/delivery/httpapi/workspaceinspection, rather than restated as a
// second copy of that test's whole-package walk.
//
// internal/delivery/httpapi/workspaceinspection's own routes.go doc comment
// already explains WHY this is true by construction, not merely by
// convention: every one of its three handlers dispatches exactly one call
// to the one already-injected *appinspection.Queries (a real
// internal/app/workspaceinspection.Queries, built once at the composition
// root — cmd/aw/serve.go) and maps the result/error onto HTTP — nothing in
// this package ever opens a file, spawns a process, or resolves a Git
// revision itself, because it structurally cannot: doing any of those would
// require importing "os", "os/exec", or an internal/adapters/... package
// (internal/adapters/gitworktree.Provider is the one real, concrete
// ports.WorkspaceInspectionReader — the only thing in this codebase that
// ever actually shells out to `git` or reads a file off disk for this
// query), and this test proves none of those imports is ever present.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkspaceInspectionHTTPNeverImportsFilesystemOrProcess walks every
// non-test .go file in internal/delivery/httpapi/workspaceinspection and
// fails if any of them imports "os", "os/exec", or any package under
// github.com/taQuangLing/agent-workflow/internal/adapters — the identical
// forbidden-import set
// TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor already checks
// for the whole internal/delivery/httpapi tree (this subpackage included,
// since that test's own filepath.WalkDir already recurses into it) —
// restated here, scoped to exactly this task's own package, so the
// invariant is self-evident from this one task's own test file without a
// reader having to trust that a DIFFERENT task's whole-package test still
// covers it.
func TestWorkspaceInspectionHTTPNeverImportsFilesystemOrProcess(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "workspaceinspection")

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
				t.Errorf("%s imports %s — internal/delivery/httpapi/workspaceinspection must never reach the filesystem or a process directly (V6-10D's own \"Không làm\": handler no file open, Git spawn, revision resolution or terminal)",
					path, imp.Path.Value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("no internal/delivery/httpapi/workspaceinspection files were checked — this test needs updating alongside the implementation")
	}
}
