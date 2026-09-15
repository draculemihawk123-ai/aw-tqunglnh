// This file is V6-10F's own architecture proof
// (docs/design/08-v6-api-projections.md V6-10F's own Không làm line:
// "handler must never call a real Git adapter or worker directly ...
// every real Git operation happens later, asynchronously, inside
// internal/app/releasesetcommit's own worker (execute.go), never
// synchronously inside your request handler"). Mirrors
// workspace_delivery_boundary_test.go's own
// TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor idiom exactly,
// scoped to this task's own new subpackage
// (internal/delivery/httpapi/releaseset) and this task's own forbidden
// call/import set:
//
//   - Import-level: internal/delivery/httpapi/releaseset must never import
//     "os", "os/exec" or any internal/adapters/... package — most pointedly
//     internal/adapters/gitworktree, the one real
//     ports.LocalCommitCreator/ports.LocalCommitMarkerReader implementation
//     this codebase has. Every real Git/filesystem/process call in this
//     codebase goes through one of those, so forbidding the imports
//     forbids the calls structurally, not just by naming convention — the
//     identical reasoning workspace_delivery_boundary_test.go's own doc
//     comment already gives.
//   - Call-level: this package legitimately imports
//     internal/app/releasesetcommit (for its exported
//     RequestReleaseSetLocalCommit, RequestReleaseSetLocalCommitRequest/
//     Result, ErrReleaseSetEntryNotFound, ErrWorkspaceNotReady), which ALSO
//     happens to export ExecuteReleaseSetLocalCommit — the real, real-Git
//     worker entrypoint (execute.go). Importing a package that also exports
//     a forbidden capability does not by itself prove this package never
//     calls it, so this test additionally scans every call expression for
//     the exact forbidden selector names: ExecuteReleaseSetLocalCommit
//     itself, and CreateLocalCommit (ports.LocalCommitCreator's own doc
//     comment: "the only Git-mutating method this port — or any port in
//     this codebase — declares").
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeliveryReleaseSetRoutesNeverReachGitOrWorker walks every non-test
// .go file in internal/delivery/httpapi/releaseset and fails if any of them:
//
//  1. imports "os", "os/exec", or any package under
//     github.com/taQuangLing/agent-workflow/internal/adapters — no direct
//     Git/filesystem/process reach from this package's HTTP handlers.
//  2. calls ExecuteReleaseSetLocalCommit (internal/app/releasesetcommit's
//     own real-Git worker entrypoint) or CreateLocalCommit
//     (ports.LocalCommitCreator's own single Git-mutating method) — both
//     belong only to a durable-job worker, never a request handler.
func TestDeliveryReleaseSetRoutesNeverReachGitOrWorker(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "releaseset")

	forbiddenImportLiterals := map[string]bool{`"os"`: true, `"os/exec"`: true}
	const forbiddenImportPrefix = "agent-workflow/internal/adapters"

	forbiddenSelectors := map[string]bool{
		"ExecuteReleaseSetLocalCommit": true,
		"CreateLocalCommit":            true,
	}

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
				t.Errorf("%s imports %s — internal/delivery/httpapi/releaseset must never reach Git/filesystem/process state directly (V6-10F's own \"Không làm\": no Git adapter/worker call from HTTP)",
					path, imp.Path.Value)
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if forbiddenSelectors[selector.Sel.Name] {
				t.Errorf("%s:%d: internal/delivery/httpapi/releaseset calls .%s(...) — this package may only dispatch the public internal/app/work.CreateReleaseSet/SealReleaseSet/AbandonReleaseSet/GetReleaseSet/ListReleaseSetsForFamily/GetReleaseSetLocalCommitStatus and internal/app/releasesetcommit.RequestReleaseSetLocalCommit; it must never call the real worker or a real Git-mutating port method itself (V6-10F's own \"Không làm\": no Git adapter/worker call from HTTP)",
					path, fset.Position(call.Pos()).Line, selector.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("no internal/delivery/httpapi/releaseset files were checked — this test needs updating alongside the implementation")
	}
}
