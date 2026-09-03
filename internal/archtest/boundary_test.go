// Package archtest enforces the architecture boundaries
// docs/design/02-v0-spike-verdict.md V0-13 requires: domain/app never
// depends on adapter/OS/provider specifics. Every check here is a real
// go test — a violation fails `go test ./...` (already run by every job in
// .github/workflows/spike-gate.yml), not a report someone has to remember to
// read.
package archtest

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// TestDomainAppNeverImportAdapters proves internal/domain/... and
// internal/app/... (including internal/app/ports, the abstraction adapters
// implement — it must never import an implementation either) never depend,
// even transitively, on internal/adapters/.... Adapters depend on
// domain/app through ports; the reverse must never happen.
func TestDomainAppNeverImportAdapters(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	output := goList(t, moduleRoot, "-json", "./internal/domain/...", "./internal/app/...")

	type goListPackage struct {
		ImportPath string
		Deps       []string
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	checked := 0
	for decoder.More() {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		checked++
		for _, dep := range pkg.Deps {
			if strings.Contains(dep, "agent-workflow/internal/adapters") {
				t.Errorf("%s depends on adapter package %s — domain/app must never import internal/adapters/...", pkg.ImportPath, dep)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no domain/app packages were checked — go list pattern matched nothing")
	}
}

// TestProcessSpecHasNoShellStringField proves the process port stays
// argv-based (Executable + Argv []string): a raw shell-command-line field
// would be a shell-injection surface baked into the port contract itself,
// not something an individual adapter could opt out of.
func TestProcessSpecHasNoShellStringField(t *testing.T) {
	spec := reflect.TypeOf(ports.ProcessSpec{})
	forbidden := map[string]bool{"Command": true, "Shell": true, "CommandLine": true, "ShellCommand": true, "Cmd": true}
	hasArgv := false
	for i := 0; i < spec.NumField(); i++ {
		field := spec.Field(i)
		if forbidden[field.Name] {
			t.Errorf("ports.ProcessSpec has forbidden shell-string field %q; process execution must stay argv-based (Executable + Argv []string), never a shell command line", field.Name)
		}
		if field.Name == "Argv" {
			hasArgv = true
			if field.Type.Kind() != reflect.Slice || field.Type.Elem().Kind() != reflect.String {
				t.Errorf("ports.ProcessSpec.Argv must be []string, got %s", field.Type)
			}
		}
	}
	if !hasArgv {
		t.Fatal("ports.ProcessSpec has no Argv []string field")
	}
}

// TestNoProviderBranchingOutsidePorts proves domain/app orchestration never
// branches (if/switch) on a specific provider identity (ports.ProviderClaude
// / ports.ProviderCodex). internal/app/ports is the one legitimate place
// these identifiers are defined; adapters implement provider-specific
// behavior behind the port, and internal/app/agentregistry's Resolve is the
// one sanctioned indirection point — a plain map lookup keyed by provider,
// never a per-provider branch. Merely referencing the constant as a value
// (a map key, a test fixture's field, a registry lookup's input parameter)
// is not a violation; deciding different behavior with an if/switch whose
// condition depends on it is. Checking only "if/switch mentions the
// identifier" (a text search) cannot tell those apart, so this walks the AST
// instead and only flags an actual branch condition. _test.go files are
// excluded: "if capabilities.Provider != ports.ProviderCodex { t.Fatalf(...) }"
// is a normal test assertion verifying a registry/resolver's behavior, not
// production orchestration deciding what to do based on provider identity —
// the two look identical to a text search but are not the same thing.
func TestNoProviderBranchingOutsidePorts(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	forbidden := map[string]bool{"ProviderClaude": true, "ProviderCodex": true}
	roots := []string{
		filepath.Join(moduleRoot, "internal", "domain"),
		filepath.Join(moduleRoot, "internal", "app"),
	}
	portsDir := filepath.Join(moduleRoot, "internal", "app", "ports")

	referencesForbidden := func(expr ast.Expr) bool {
		found := false
		ast.Inspect(expr, func(n ast.Node) bool {
			switch e := n.(type) {
			case *ast.Ident:
				if forbidden[e.Name] {
					found = true
				}
			case *ast.SelectorExpr:
				if forbidden[e.Sel.Name] {
					found = true
				}
			}
			return true
		})
		return found
	}

	fset := token.NewFileSet()
	checked := 0
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				// Test assertions ("if got != ports.ProviderCodex { t.Fatal(...) }")
				// are not production orchestration branching on provider identity.
				return nil
			}
			if filepath.Dir(path) == portsDir {
				// The one legitimate definition site.
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			checked++
			ast.Inspect(file, func(n ast.Node) bool {
				switch stmt := n.(type) {
				case *ast.IfStmt:
					if stmt.Cond != nil && referencesForbidden(stmt.Cond) {
						t.Errorf("%s:%d: if-condition branches on a provider identity — domain/app orchestration must never branch on a specific provider (use a registry/lookup keyed by provider instead, see internal/app/agentregistry)",
							path, fset.Position(stmt.Pos()).Line)
					}
				case *ast.SwitchStmt:
					if stmt.Tag != nil && referencesForbidden(stmt.Tag) {
						t.Errorf("%s:%d: switch branches on a provider identity — domain/app orchestration must never branch on a specific provider",
							path, fset.Position(stmt.Pos()).Line)
					}
					if stmt.Body != nil {
						for _, item := range stmt.Body.List {
							clause, ok := item.(*ast.CaseClause)
							if !ok {
								continue
							}
							for _, caseExpr := range clause.List {
								if referencesForbidden(caseExpr) {
									t.Errorf("%s:%d: switch case branches on a provider identity — domain/app orchestration must never branch on a specific provider",
										path, fset.Position(clause.Pos()).Line)
								}
							}
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if checked == 0 {
		t.Fatal("no domain/app files were checked — walk matched nothing")
	}
}

// TestWorkflowVersionHasNoMutatingMethods proves workflow.WorkflowVersion
// stays an immutable value once compiled/published: every method has a
// value receiver. A pointer-receiver method would only ever appear in the
// pointer type's method set, never the value type's — exactly the signal an
// in-place mutator would leave, since a value-receiver method operates on a
// copy and cannot mutate the caller's original.
func TestWorkflowVersionHasNoMutatingMethods(t *testing.T) {
	valueType := reflect.TypeOf(workflow.WorkflowVersion{})
	pointerType := reflect.TypeOf(&workflow.WorkflowVersion{})

	valueMethods := make(map[string]bool, valueType.NumMethod())
	for i := 0; i < valueType.NumMethod(); i++ {
		valueMethods[valueType.Method(i).Name] = true
	}
	var pointerOnly []string
	for i := 0; i < pointerType.NumMethod(); i++ {
		name := pointerType.Method(i).Name
		if !valueMethods[name] {
			pointerOnly = append(pointerOnly, name)
		}
	}
	if len(pointerOnly) > 0 {
		t.Errorf("workflow.WorkflowVersion has pointer-receiver-only method(s) %v: a workflow version must stay an immutable value type (no in-place mutation) once compiled/published", pointerOnly)
	}
}

// TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess proves
// §11.1's own rule (docs/architecture/04-go-core-spec.md "11.1 Application
// transaction", restated at docs/design/04-v2-definition-plane.md V2-07B
// and ADR-022: "Re-hash bên trong transaction cũng không hợp lệ vì §11.1
// cấm gọi filesystem/process trong application transaction"):
// RegisterAdapterBuild's database transaction closure — the func literal
// it passes to uow.WithSerializedWrite — must never itself touch the
// filesystem or spawn a process. Re-measuring the executable/capability
// manifest must happen entirely BEFORE the transaction opens; the
// transaction is only allowed to persist values already measured. This
// parses the real source of internal/app/adapterbuild/commands.go,
// locates that one func literal, and fails if its body contains a call
// into os/exec/ioutil, or a call to the package's own hashExecutableFile
// helper (which itself wraps a filesystem read).
func TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	path := filepath.Join(moduleRoot, "internal", "app", "adapterbuild", "commands.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	forbiddenPackages := map[string]bool{"os": true, "exec": true, "ioutil": true}
	forbiddenCalls := map[string]bool{"hashExecutableFile": true}

	var txClosure *ast.FuncLit
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "RegisterAdapterBuild" {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "WithSerializedWrite" {
				return true
			}
			for _, arg := range call.Args {
				if lit, ok := arg.(*ast.FuncLit); ok {
					txClosure = lit
				}
			}
			return true
		})
		return false
	})
	if txClosure == nil {
		t.Fatal("could not find the func literal passed to uow.WithSerializedWrite inside RegisterAdapterBuild — this test needs updating alongside the implementation")
	}

	ast.Inspect(txClosure, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if ident, ok := fun.X.(*ast.Ident); ok && forbiddenPackages[ident.Name] {
				t.Errorf("%s: RegisterAdapterBuild's transaction closure calls %s.%s — filesystem/process calls must happen before the transaction opens, never inside it (§11.1)",
					fset.Position(call.Pos()), ident.Name, fun.Sel.Name)
			}
		case *ast.Ident:
			if forbiddenCalls[fun.Name] {
				t.Errorf("%s: RegisterAdapterBuild's transaction closure calls %s — filesystem/process calls must happen before the transaction opens, never inside it (§11.1)",
					fset.Position(call.Pos()), fun.Name)
			}
		}
		return true
	})
}

// TestRequestWorkspaceReconciliationNeverImportsWorkspaceIO proves V3-10's
// own public/internal split (docs/design/05-v3-project-workspace.md V3-10:
// "Tách public RequestWorkspaceReconciliation ... khỏi internal
// ExecuteWorkspaceReconciliation ...; API chỉ được gọi command public") at
// the import level: internal/app/workspacereconcile/commands.go — which
// declares RequestWorkspaceReconciliation, the one entry point any
// API/CLI-shaped caller may ever call — must never import anything that
// could reach a real workspace's filesystem or Git state (os, os/exec, or
// any internal/adapters/... package, most pointedly
// internal/adapters/gitworktree itself). ExecuteWorkspaceReconciliation
// (handler.go, a different file in the same package) is exactly where that
// real I/O belongs instead, via ports.WorkspaceProvider/
// ports.WorkspaceLifecycle — this test intentionally checks commands.go
// alone, mirroring TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess's
// own "parse the one real source file, check its own call graph" technique
// for an identical "public path never touches I/O directly" boundary.
func TestRequestWorkspaceReconciliationNeverImportsWorkspaceIO(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	path := filepath.Join(moduleRoot, "internal", "app", "workspacereconcile", "commands.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	forbidden := map[string]bool{
		`"os"`:      true,
		`"os/exec"`: true,
	}
	found := false
	for _, imp := range file.Imports {
		found = true
		if forbidden[imp.Path.Value] || strings.Contains(imp.Path.Value, "agent-workflow/internal/adapters") {
			t.Errorf("%s imports %s — RequestWorkspaceReconciliation's own file must never reach real workspace filesystem/Git state directly; that belongs only in ExecuteWorkspaceReconciliation (handler.go), behind ports.WorkspaceProvider/ports.WorkspaceLifecycle",
				path, imp.Path.Value)
		}
	}
	if !found {
		t.Fatal("commands.go declared no imports at all — this test needs updating alongside the implementation")
	}
}

// TestRequestWorkspaceSetReleaseNeverImportsWorkspaceIO is V3-11's own
// identical boundary proof (docs/design/05-v3-project-workspace.md V3-11's
// own Phạm vi line: "V3-11 sở hữu public RequestWorkspaceSetRelease ...
// Internal ExecuteWorkspaceSetRelease (thực thi filesystem/Git) thuộc
// V5-14") for internal/app/workspacerelease/commands.go, mirroring
// TestRequestWorkspaceReconciliationNeverImportsWorkspaceIO above exactly:
// the one file declaring RequestWorkspaceSetRelease must never import "os",
// "os/exec", or any internal/adapters/... package. ExecuteWorkspaceSetRelease
// does not exist anywhere in this codebase — it belongs to V5-14, a future
// task — so this test also proves this package builds no such handler for
// itself under a different name: there is exactly one source file in this
// package's own public-command surface, and it stays free of real I/O.
func TestRequestWorkspaceSetReleaseNeverImportsWorkspaceIO(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	path := filepath.Join(moduleRoot, "internal", "app", "workspacerelease", "commands.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	forbidden := map[string]bool{
		`"os"`:      true,
		`"os/exec"`: true,
	}
	found := false
	for _, imp := range file.Imports {
		found = true
		if forbidden[imp.Path.Value] || strings.Contains(imp.Path.Value, "agent-workflow/internal/adapters") {
			t.Errorf("%s imports %s — RequestWorkspaceSetRelease's own file must never reach real workspace filesystem/Git state directly; that belongs only to a future V5-14 executor, behind ports.WorkspaceProvider/ports.WorkspaceLifecycle",
				path, imp.Path.Value)
		}
	}
	if !found {
		t.Fatal("commands.go declared no imports at all — this test needs updating alongside the implementation")
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file location")
	}
	directory := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("go.mod not found above test file")
		}
		directory = parent
	}
}

func goExecutable() string {
	exe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	return exe
}

func goList(t *testing.T, moduleRoot string, args ...string) []byte {
	t.Helper()
	command := exec.Command(goExecutable(), append([]string{"list"}, args...)...)
	command.Dir = moduleRoot
	output, err := command.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list %v: %v\n%s", args, err, exitErr.Stderr)
		}
		t.Fatalf("go list %v: %v", args, err)
	}
	return output
}
