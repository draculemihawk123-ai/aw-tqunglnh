// This file is V6-02's own architecture proof (docs/design/08-v6-api-projections.md
// V6-02's own Verify line: "architecture test delivery không ghi receipt/
// repository transaction"): internal/delivery/httpapi must never write a
// command receipt or open a write transaction itself — the one read-only
// receipt lookup it performs (httpapi.LookupReceipt, receiptreplay.go) is
// a latency/UX fast path only; the real, authoritative receipt write
// always happens inside the actual application command's own transaction
// (internal/app/catalog.CreateProject and every sibling command this
// codebase already ships), which this package only ever calls into, never
// duplicates. Mirrors TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess's
// own "parse real source, walk the AST, fail on a forbidden call" idiom.
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

// TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt walks every non-test
// .go file in internal/delivery/httpapi and fails if any of them calls
// `.Record(` on anything (the only real call site for that name in this
// codebase is ports.ReceiptsRepository.Record) or `.WithSerializedWrite(`
// (this package must only ever use the read-only half of
// ports.UnitOfWork — WithReadOnly, for httpapi.LookupReceipt's own fast
// path) — either would mean this package started acting as a second
// receipt-writing authority, which V6-02's own "Không làm" line forbids
// outright.
func TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi")

	forbiddenSelectors := map[string]bool{"Record": true, "WithSerializedWrite": true}

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
				t.Errorf("%s:%d: internal/delivery/httpapi calls .%s(...) — this package must never write or record a command receipt itself (V6-02's own \"Không làm\"); only the real application command's own transaction may do that",
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
