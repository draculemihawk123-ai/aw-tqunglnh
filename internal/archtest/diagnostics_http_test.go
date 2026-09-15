// This file is V6-06C's own architecture proof
// (docs/design/08-v6-api-projections.md V6-06C's own "Không làm" line: no
// internal execute-command capability, never a way to trigger execution,
// never a self-resolved blocker/Run/WorkItem state), mirroring
// run_control_test.go's (V6-06) and recovery_http_test.go's (V6-06D) own
// identical "parse real source, walk go/ast, fail on forbidden call" idiom.
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

// TestDiagnosticsHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly
// walks every non-test .go file in internal/delivery/httpapi/diagnostics and
// fails if any of them calls a selector literally named "Handle" (this
// codebase's own consistent job-consumer/execution entrypoint name),
// "AdvanceRun"/"FinalizeExecutionAttempt" (internal/app/runtime's own
// scheduler/attempt-completion entry points), "Capabilities" (the ONE real
// I/O call adapterbuild.VerifyNoDrift itself performs to spawn a configured
// executable — this package's own doc comment: a GET must never itself
// trigger process execution, no matter how narrow), or any of the internal
// CAS transition primitives every real recovery/mutation command in this
// codebase already uses internally on a caller's behalf —
// "TransitionNodeRun", "TransitionExecutionAttempt",
// "TransitionWorkflowRunState", "TransitionWorkItemStatus" and
// "TransitionWorkItemBlockerState" (ports.RuntimeRepository/
// ports.WorkRepository's own CAS methods). Any of these reached directly
// from this package's own one HTTP handler would mean a "safe, read-only
// diagnosis" surface had grown either a hidden execute-command capability or
// real mutation authority of its own — exactly what this task's own Phạm vi
// line forbids.
func TestDiagnosticsHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "diagnostics")

	forbiddenSelectors := map[string]bool{
		"Handle": true, "AdvanceRun": true, "FinalizeExecutionAttempt": true, "Capabilities": true,
		"TransitionNodeRun": true, "TransitionExecutionAttempt": true,
		"TransitionWorkflowRunState": true, "TransitionWorkItemStatus": true, "TransitionWorkItemBlockerState": true,
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
				t.Errorf("%s:%d: internal/delivery/httpapi/diagnostics calls .%s(...) — this package must only ever dispatch the real public runtime.GetRunDiagnostics read query (this task's own \"handler chỉ dispatch\"), never a scheduler/worker entry point, a live process probe, or a raw state-transition primitive directly",
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
		t.Fatal("no internal/delivery/httpapi/diagnostics files were checked — this test needs updating alongside the implementation")
	}
}
