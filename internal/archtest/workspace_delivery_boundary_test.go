// This file is V6-10B's own architecture proof
// (docs/design/08-v6-api-projections.md V6-10B's own Không làm line: "no
// internal Execute command, Git/filesystem direct call or writer grant from
// HTTP"). Mirrors TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt's own
// "parse real source, walk the AST, fail on a forbidden call/import" idiom
// (command_envelope_test.go) and
// TestRequestWorkspaceReconciliationNeverImportsWorkspaceIO's own forbidden-
// import set (boundary_test.go) — combined into one walk here since this
// task's own boundary is both an import-level and a call-level concern:
//
//   - Import-level: internal/delivery/httpapi must never import "os",
//     "os/exec" or any internal/adapters/... package — every real Git/
//     filesystem/process call in this codebase goes through one of those,
//     so forbidding the imports forbids the calls structurally, not just by
//     naming convention.
//   - Call-level: even without importing an adapter directly,
//     internal/delivery/httpapi DOES legitimately import
//     internal/app/workspacereconcile (for its exported
//     RequestWorkspaceReconciliation, ErrWorkspaceNotReconcilable, and job
//     kind constant) and internal/app/work (for NewEligibilityAuthority) —
//     both of which also happen to export their own real I/O-reaching
//     functions/methods (ExecuteWorkspaceReconciliation; ReleaseSet's own
//     work.EligibilityAuthority is read-only, but ports.WorkspaceLifecycle's
//     mutating methods and ports.WriteLeaseManager.AcquireWriteLeases are
//     reachable via any tx.Work() call). Importing a package that ALSO
//     happens to export a forbidden capability does not by itself prove HTTP
//     never calls it, so this test additionally scans every call expression
//     for the exact forbidden selector names.
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

// TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor walks every
// non-test .go file in internal/delivery/httpapi (the whole package, not
// just this task's own new files — mirroring
// TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt's identical whole-package
// scope, so the invariant holds regardless which future file in this
// package might otherwise regress it) and fails if any of them:
//
//  1. imports "os", "os/exec", or any package under
//     github.com/taQuangLing/agent-workflow/internal/adapters — no direct
//     Git/filesystem/process reach from HTTP.
//  2. calls any of the internal job-handler executors
//     (ExecuteWorkspaceReconciliation, ExecuteWorkspaceSetRelease — the
//     latter does not exist in this codebase yet, V5-14's own future
//     authority; listed defensively so this test starts failing the moment
//     anyone adds one here rather than only once it exists) — those belong
//     only to a durable-job worker, never a request handler.
//  3. calls any ports.WorkspaceLifecycle mutating method
//     (QuarantineRepositoryWorkspace, ReleaseRepositoryWorkspace,
//     RecreateRepositoryWorkspace) or ports.WriteLeaseManager's own
//     AcquireWriteLeases — "no writer grant from HTTP" as a real, checked
//     call-graph fact, not merely a review convention. HasActiveWriteLease
//     (a plain read) is deliberately NOT in this forbidden set — it is the
//     one real, already-tested existence check this task's own read query
//     (internal/app/workspacestate) legitimately calls.
func TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi")

	forbiddenImportLiterals := map[string]bool{`"os"`: true, `"os/exec"`: true}
	const forbiddenImportPrefix = "agent-workflow/internal/adapters"

	forbiddenSelectors := map[string]bool{
		"ExecuteWorkspaceReconciliation": true,
		"ExecuteWorkspaceSetRelease":     true,
		"QuarantineRepositoryWorkspace":  true,
		"ReleaseRepositoryWorkspace":     true,
		"RecreateRepositoryWorkspace":    true,
		"AcquireWriteLeases":             true,
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
				t.Errorf("%s imports %s — internal/delivery/httpapi must never reach Git/filesystem/process state directly (V6-10B's own \"Không làm\": no Git/filesystem direct call from HTTP)",
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
				t.Errorf("%s:%d: internal/delivery/httpapi calls .%s(...) — this package may only dispatch the public workspacerelease.RequestWorkspaceSetRelease/workspacereconcile.RequestWorkspaceReconciliation commands and read-only workspacestate queries; it must never call an internal job-handler executor or a ports.WorkspaceLifecycle/ports.WriteLeaseManager mutating method itself (V6-10B's own \"Không làm\": no internal Execute command or writer grant from HTTP)",
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
		t.Fatal("no internal/delivery/httpapi files were checked — this test needs updating alongside the implementation")
	}
}
