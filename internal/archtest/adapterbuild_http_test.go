// This file is V6-10J's own architecture proof
// (docs/design/08-v6-api-projections.md V6-10J's own "Không làm: ... direct
// prober/process" line, plus this task's own explicit "Verify: ... handler
// architecture spy" instruction), mirroring run_control_test.go's and
// recovery_http_test.go's own identical "parse real source, walk go/ast,
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

// TestAdapterBuildHTTPNeverReachesProcessOrFilesystemDirectly walks every
// non-test .go file in internal/delivery/httpapi/adapterbuild and fails if
// any of them calls a selector literally named "Capabilities" (the real,
// live ports.AgentExecutor.Capabilities method — spawns a real provider CLI
// process, exactly what cmd/aw/adapter.go's own newAgentExecutor path does
// for the CLI's convenience but this HTTP package must never replicate) or
// "HashExecutableFile"/"hashExecutableFile" (internal/app/adapterbuild's own
// exported/lowercase-spelled real filesystem-hashing helper — the same two
// spellings internal/archtest/boundary_test.go's own
// TestProbeAdapterBuildTransactionNeverCallsFilesystemOrProcess already
// checks for the application-command layer, applied here to the HTTP layer
// one level up). Any of these reached directly from this package's own HTTP
// handlers would mean a "thin dispatcher" had grown a second, competing
// path to the executable/process — this package must only ever hand a
// caller-supplied request straight to the real, already-hardened
// appadapterbuild.ProbeAdapterBuild/RegisterAdapterBuild commands (this
// task's own "Thực hiện: ... thin dispatch only"), which are the ONE place
// any such I/O is allowed to happen.
func TestAdapterBuildHTTPNeverReachesProcessOrFilesystemDirectly(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "internal", "delivery", "httpapi", "adapterbuild")

	forbiddenSelectors := map[string]bool{
		"Capabilities": true, "HashExecutableFile": true, "hashExecutableFile": true,
	}
	forbiddenImports := map[string]bool{
		`"os/exec"`: true,
		`"github.com/taQuangLing/agent-workflow/internal/adapters/process"`:          true,
		`"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"`: true,
		`"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"`:  true,
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
				t.Errorf("%s: internal/delivery/httpapi/adapterbuild imports %s — this package must never reach a real process/executable-adapter package directly, only through appadapterbuild.ProbeAdapterBuild/RegisterAdapterBuild",
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
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if forbiddenSelectors[fun.Sel.Name] {
					t.Errorf("%s:%d: internal/delivery/httpapi/adapterbuild calls .%s(...) — this package must only ever dispatch the real public ProbeAdapterBuild/RegisterAdapterBuild commands (V6-10J's own \"thin dispatch only\"), never a live process/executor or the raw filesystem-hashing helper directly",
						path, fset.Position(call.Pos()).Line, fun.Sel.Name)
				}
			case *ast.Ident:
				if forbiddenSelectors[fun.Name] {
					t.Errorf("%s:%d: internal/delivery/httpapi/adapterbuild calls %s(...) directly — this package must only ever dispatch the real public ProbeAdapterBuild/RegisterAdapterBuild commands, never the raw filesystem-hashing helper itself",
						path, fset.Position(call.Pos()).Line, fun.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("no internal/delivery/httpapi/adapterbuild files were checked — this test needs updating alongside the implementation")
	}
}
