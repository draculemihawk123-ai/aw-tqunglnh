// This file is V6-15M's own architecture proof
// (docs/design/08-v6-api-projections.md:773-781's own Không làm line: "no
// push/fetch/PR/merge/rebase/force-push — this is LOCAL only"). There is no
// push/fetch/remote-mutating port anywhere in this codebase to runtime-spy
// on (ports.LocalCommitCreator's own doc comment: "CreateLocalCommit is the
// only Git-mutating method this port — or any port in this codebase —
// declares"), so the correct, stronger proof is this package's own
// architectural absence of any path to real Git/filesystem/process state or
// the real asynchronous worker — walked here the identical way
// releaseset_delivery_boundary_test.go's own
// TestDeliveryReleaseSetRoutesNeverReachGitOrWorker already proves it for
// internal/delivery/httpapi/releaseset, scoped instead to this task's own
// new internal/delivery/cli/releaseset package:
//
//   - Import-level: internal/delivery/cli/releaseset must never import
//     "os", "os/exec" or any internal/adapters/... package — most pointedly
//     internal/adapters/gitworktree, the one real
//     ports.LocalCommitCreator/ports.LocalCommitMarkerReader implementation
//     this codebase has.
//   - Call-level: this package legitimately imports
//     internal/app/releasesetcommit (for its exported
//     RequestReleaseSetLocalCommit and
//     RequestReleaseSetLocalCommitRequest/Result), which ALSO happens to
//     export ExecuteReleaseSetLocalCommit — the real, real-Git worker
//     entrypoint (execute.go). Importing a package that also exports a
//     forbidden capability does not by itself prove this package never
//     calls it, so this test additionally scans every call expression for
//     the exact forbidden selector names: ExecuteReleaseSetLocalCommit
//     itself, and CreateLocalCommit (ports.LocalCommitCreator's own single
//     Git-mutating method).
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

// TestDeliveryCLIReleaseSetNeverReachesGitOrWorker walks every non-test .go
// file in internal/delivery/cli/releaseset and fails if any of them:
//
//  1. imports "os", "os/exec", or any package under
//     github.com/taQuangLing/agent-workflow/internal/adapters — no direct
//     Git/filesystem/process reach from this leaf's own CLI dispatch.
//  2. calls ExecuteReleaseSetLocalCommit (internal/app/releasesetcommit's
//     own real-Git worker entrypoint) or CreateLocalCommit
//     (ports.LocalCommitCreator's own single Git-mutating method) — both
//     belong only to a durable-job worker (a separate `aw worker` process),
//     never this leaf's own synchronous dispatch.
func TestDeliveryCLIReleaseSetNeverReachesGitOrWorker(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "cli", "releaseset")

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
				t.Errorf("%s imports %s — internal/delivery/cli/releaseset must never reach Git/filesystem/process state directly (V6-15M's own \"Không làm\": LOCAL only, no push/fetch/PR/merge/rebase/force-push, no direct worker/Git call from the CLI)",
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
				t.Errorf("%s:%d: internal/delivery/cli/releaseset calls .%s(...) — this package may only dispatch the public internal/app/work.CreateReleaseSet/SealReleaseSet/AbandonReleaseSet/GetReleaseSet/ListReleaseSetsForFamily/GetReleaseSetLocalCommitStatus and internal/app/releasesetcommit.RequestReleaseSetLocalCommit; it must never call the real worker or a real Git-mutating port method itself (V6-15M's own \"Không làm\")",
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
		t.Fatal("no internal/delivery/cli/releaseset files were checked — this test needs updating alongside the implementation")
	}
}
