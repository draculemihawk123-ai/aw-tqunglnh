// This file is V6-06D's own architecture proof
// (docs/design/08-v6-api-projections.md V6-06D's own "Không làm" line:
// "không gọi internal recovery worker hoặc tự sửa blocker/Run state"),
// mirroring run_control_test.go's own "parse real source, walk go/ast, fail
// on forbidden call" idiom exactly (that file is V6-06's own identical proof
// for CancelRun/StartWorkflowRun).
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

// TestRecoveryHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly walks
// every non-test .go file in internal/delivery/httpapi/recovery and fails if
// any of them calls a selector literally named "Handle" (this codebase's own
// consistent job-consumer entrypoint name, e.g.
// internal/app/runtime.ExecuteNodeHandler.Handle,
// internal/app/workspaceprovision.Handler.Handle), "AdvanceRun"/
// "FinalizeExecutionAttempt" (internal/app/runtime's own scheduler/attempt-
// completion entry points), or any of the internal transition primitives
// this package's own three wrapped commands (RetryBlockedActivationHandler.
// Retry, runtime.CancelWorkItem, runtime.ResolveWorkItemBlocker) already use
// internally on the caller's behalf — "TransitionNodeRun",
// "TransitionExecutionAttempt", "TransitionWorkflowRunState" and
// "TransitionWorkItemStatus" (ports.RuntimeRepository/ports.WorkRepository's
// own CAS methods). Any of these reached directly from this package's own
// HTTP handlers would mean a thin dispatcher had grown real business logic
// of its own — self-resolving a blocker or self-transitioning a Run/NodeRun/
// Attempt/WorkItem instead of trusting the one real, already-tested
// application command each route exists to wrap.
func TestRecoveryHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "recovery")

	forbiddenSelectors := map[string]bool{
		"Handle": true, "AdvanceRun": true, "FinalizeExecutionAttempt": true,
		"TransitionNodeRun": true, "TransitionExecutionAttempt": true,
		"TransitionWorkflowRunState": true, "TransitionWorkItemStatus": true,
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
				t.Errorf("%s:%d: internal/delivery/httpapi/recovery calls .%s(...) — this package must only ever dispatch the real public RetryBlockedActivationHandler.Retry/runtime.CancelWorkItem/runtime.ResolveWorkItemBlocker commands (V6-06D's own \"handler chỉ dispatch\"), never a scheduler/worker entry point or a raw state-transition primitive directly",
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
		t.Fatal("no internal/delivery/httpapi/recovery files were checked — this test needs updating alongside the implementation")
	}
}
