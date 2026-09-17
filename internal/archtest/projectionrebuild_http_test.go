// This file is V6-09B's own architecture proof
// (docs/design/08-v6-api-projections.md V6-09B's own "Không làm: handler
// không call worker/row store or infer latest operation" line, plus this
// task's own explicit "Verify: ... architecture dispatch spy" instruction),
// mirroring adapterbuild_http_test.go's and recovery_http_test.go's own
// "parse real source, walk go/ast, fail on forbidden import/call" idiom
// exactly.
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

// TestProjectionRebuildHTTPNeverReachesWorkerOrRowStoreDirectly walks every
// non-test .go file in internal/delivery/httpapi/projectionrebuild and
// fails if any of them imports internal/app/projection (V6-08/V6-08A's own
// live-consumer/classification/worker package), internal/app/projectionrebuildworker
// (V6-09A's own not-yet-fully-exercised rebuild worker) or
// internal/adapters/sqlite (a direct SQLite fast path — forbidden for every
// HTTP/CLI package by this repo's own root contract line 1), OR calls a
// selector literally named after one of ports.ProjectionRepository's or
// ports.ProjectionRebuildRepository's own raw CRUD/CAS methods
// (GetActiveGeneration, GetProjectionCheckpoint, UpsertProjectionRow,
// UpsertProjectionCheckpoint, ListProjectionRows, GetProjectionRow,
// RecordProjectionPoison, ListProjectionPoison, AcquireOrRenewConsumerLease,
// CutoverProjectionGeneration, DiscardGeneration, CreateOperation,
// GetActiveOperation, AdvanceOperation) or the Tx accessors that reach them
// (Projections, ProjectionRebuilds) or a transaction opener
// (WithReadOnly, WithSerializedWrite). Any of these reached directly from
// this package's own HTTP handlers would mean a "thin dispatcher" had grown
// a second, competing path to the row store or the rebuild worker instead
// of trusting the one real, already-tested internal/app/projectionrebuild
// package (RequestProjectionRebuild, GetProjectionRebuildStatus,
// GetProjectionStatus) this task's own routes exist to wrap — this task's
// own "Thực hiện: route only dispatches V6-09's own command/queries" line.
func TestProjectionRebuildHTTPNeverReachesWorkerOrRowStoreDirectly(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "projectionrebuild")

	forbiddenImports := map[string]bool{
		`"github.com/taQuangLing/agent-workflow/internal/app/projection"`:              true,
		`"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuildworker"`: true,
		`"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"`:             true,
	}
	forbiddenSelectors := map[string]bool{
		"GetActiveGeneration": true, "GetProjectionCheckpoint": true, "UpsertProjectionRow": true,
		"UpsertProjectionCheckpoint": true, "ListProjectionRows": true, "GetProjectionRow": true,
		"RecordProjectionPoison": true, "ListProjectionPoison": true, "AcquireOrRenewConsumerLease": true,
		"CutoverProjectionGeneration": true, "DiscardGeneration": true, "CreateOperation": true,
		"GetActiveOperation": true, "AdvanceOperation": true,
		"Projections": true, "ProjectionRebuilds": true,
		"WithReadOnly": true, "WithSerializedWrite": true,
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
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range file.Imports {
			if forbiddenImports[imp.Path.Value] {
				t.Errorf("%s: internal/delivery/httpapi/projectionrebuild imports %s — this package must never reach the projection worker/consumer or a raw SQLite adapter directly, only through internal/app/projectionrebuild's own public functions",
					fset.Position(imp.Pos()), imp.Path.Value)
			}
		}

		fullFile, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		checked++
		ast.Inspect(fullFile, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if forbiddenSelectors[selector.Sel.Name] {
				t.Errorf("%s:%d: internal/delivery/httpapi/projectionrebuild calls .%s(...) — this package must only ever dispatch the real public internal/app/projectionrebuild.RequestProjectionRebuild/GetProjectionRebuildStatus/GetProjectionStatus functions (V6-09B's own \"route only dispatches V6-09's own command/queries\"), never a raw ports.Tx/ProjectionRepository/ProjectionRebuildRepository accessor or transaction opener directly",
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
		t.Fatal("no internal/delivery/httpapi/projectionrebuild files were checked — this test needs updating alongside the implementation")
	}
}
