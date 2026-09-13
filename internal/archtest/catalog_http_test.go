// This file is V6-03A's own architecture proof
// (docs/design/08-v6-api-projections.md V6-03A's own "Không làm: không
// duplicate Doctor, expose helper CreateComponent, hoặc giả sync success
// trước probe"): internal/delivery/httpapi/catalog must never call
// internal/app/catalog.CreateComponent (or, equivalently, the underlying
// ports.CatalogRepository.CreateComponent persistence method directly) —
// every Component this package's own GET routes can ever return was
// inserted by V3-02's own onboarding-probe worker
// (internal/app/repositoryprobe), never by anything reachable from HTTP.
// Mirrors command_envelope_test.go's own
// TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt "parse real source, walk
// the AST, fail on a forbidden call" idiom, scoped to this one
// subpackage and this one forbidden selector name.
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

// TestHTTPAPICatalogNeverCallsCreateComponent walks every non-test .go
// file in internal/delivery/httpapi/catalog and fails if any of them
// calls `.CreateComponent(` on anything — the only real call sites for
// that exact selector name in this codebase are
// internal/app/catalog.CreateComponent (the free function) and
// ports.CatalogRepository.CreateComponent (the persistence method it
// wraps). Either one, reachable from this HTTP package, would mean a
// caller could create a Component through the API — exactly what V6-03A's
// own "Không làm" line forbids: components are read-only from HTTP,
// discovered only by repository onboarding/probe.
func TestHTTPAPICatalogNeverCallsCreateComponent(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "catalog")

	const forbiddenSelector = "CreateComponent"

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
			if selector.Sel.Name == forbiddenSelector {
				t.Errorf("%s:%d: internal/delivery/httpapi/catalog calls .%s(...) — this package must never create a Component itself (V6-03A's own \"Không làm\"); every Component is discovered by repository onboarding/probe, never by an HTTP-reachable call",
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
		t.Fatal("no internal/delivery/httpapi/catalog files were checked — this test needs updating alongside the implementation")
	}
}
