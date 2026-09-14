// This file is V6-06's own architecture proof (docs/design/08-v6-api-projections.md
// V6-06's own "Không làm" line: "không... inline worker execution", plus
// this task's own explicit review instruction to add "an architecture test
// in this same style (no scheduler/worker fast path reachable from HTTP)"),
// mirroring command_envelope_test.go's own "parse real source, walk go/ast,
// fail on forbidden call" idiom exactly.
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

// TestRunControlHTTPNeverReachesSchedulerOrWorker walks every non-test .go
// file in internal/delivery/httpapi/run and fails if any of them calls a
// selector literally named "Handle" (this codebase's own consistent
// job-consumer entrypoint name — internal/app/runtime.ExecuteNodeHandler.Handle,
// runtime.CancelRunCoordinatorHandler.Handle, internal/app/workspaceprovision.Handler.Handle,
// and every sibling job handler in this codebase share this exact method
// name), or "AdvanceRun"/"FinalizeExecutionAttempt" (internal/app/runtime's
// own scheduler/attempt-completion entry points, internal/app/runtime/schedule.go
// and finalize.go) — any of which reached directly from an HTTP handler
// would let a POST synchronously drive a Run's own quiesce/routing work
// within the request itself, contradicting this task's own "cancel trả
// CANCELLING, không giả CANCELLED; handler chỉ dispatch" line: the real
// asynchronous ADVANCE_RUN/CANCEL_RUN_COORDINATOR jobs (enqueued by the
// real runtime.StartWorkflowRun/runtime.CancelRun commands this package DOES
// call) are the only things ever allowed to do that work, on their own
// separate worker path.
func TestRunControlHTTPNeverReachesSchedulerOrWorker(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "run")

	forbiddenSelectors := map[string]bool{"Handle": true, "AdvanceRun": true, "FinalizeExecutionAttempt": true}

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
				t.Errorf("%s:%d: internal/delivery/httpapi/run calls .%s(...) — this package must only ever dispatch the real public runtime.StartWorkflowRun/runtime.CancelRun commands (V6-06's own \"handler chỉ dispatch\"), never a scheduler/worker entry point directly",
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
		t.Fatal("no internal/delivery/httpapi/run files were checked — this test needs updating alongside the implementation")
	}
}
