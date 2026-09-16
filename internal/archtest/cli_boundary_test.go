// This file is V6-15B's own "architecture import test" verify bullet
// (docs/design/08-v6-api-projections.md V6-15B: "Không làm: no domain
// leaf, SQLite/Git/provider/internal worker import or HTTP-client
// authority"): internal/delivery/cli is the shared framework every future
// `aw <resource> <action>` leaf task (V6-15C onward) builds on top of, and
// it must never itself reach any real persistence/Git/provider
// implementation — that real I/O belongs only inside a later leaf task,
// behind ports, exactly like every existing HTTP endpoint package already
// keeps it. Reusing internal/delivery/httpapi's own SemanticHash/
// LookupReceipt/ReconcileReceipt (V6-15B's own task brief instruction) is
// deliberately NOT forbidden here: "no HTTP-client authority" means this
// package must never itself act as an HTTP client (dispatch a command by
// calling `aw serve` over the network), not that it may never import the
// httpapi Go package for its pure, ports.UnitOfWork-based functions.
//
// SQLite/Git/provider adapters are checked against the FULL transitive
// dependency closure (go list -json's own "Deps" field, the same
// technique TestDomainAppNeverImportAdapters already uses) — confirmed
// httpapi itself never reaches any of the three either, so this is a real,
// satisfiable, strong guarantee. "Internal worker" is checked against
// internal/delivery/cli's own DIRECT imports only (go list -json's own
// "Imports" field): httpapi legitimately, pre-existingly reaches
// internal/app/workerpool several hops deep for its own unrelated
// doctor/diagnostics reporting (confirmed via `go list -deps
// ./internal/delivery/httpapi`), so a transitive check here would fail
// the moment this package imports httpapi at all — exactly what V6-15B's
// own task brief instructs it to do. The meaningful, satisfiable
// invariant is narrower: this package's OWN code must never itself reach
// for a worker package, which a direct-import check proves precisely.
package archtest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDeliveryCLINeverImportsSQLiteGitOrProviderAdapters(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	output := goList(t, moduleRoot, "-json", "./internal/delivery/cli/...")

	type goListPackage struct {
		ImportPath string
		Deps       []string
	}

	const module = "agent-workflow/internal/"
	forbiddenSubstrings := []string{
		module + "adapters/sqlite",
		module + "adapters/gitworktree",
		module + "adapters/providers",
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
			for _, forbidden := range forbiddenSubstrings {
				if strings.Contains(dep, forbidden) {
					t.Errorf("%s depends (even transitively) on %s — internal/delivery/cli must never import a SQLite/Git/provider adapter (V6-15B's own \"Không làm\")",
						pkg.ImportPath, dep)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no internal/delivery/cli packages were checked — go list pattern matched nothing")
	}
}

func TestDeliveryCLINeverDirectlyImportsAnInternalWorkerPackage(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	output := goList(t, moduleRoot, "-json", "./internal/delivery/cli/...")

	type goListPackage struct {
		ImportPath string
		Imports    []string
	}

	const module = "agent-workflow/internal/"
	forbiddenSubstrings := []string{
		module + "app/worker",
		module + "app/workerpool",
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	checked := 0
	for decoder.More() {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		checked++
		for _, imp := range pkg.Imports {
			for _, forbidden := range forbiddenSubstrings {
				if strings.Contains(imp, forbidden) {
					t.Errorf("%s directly imports %s — internal/delivery/cli must never itself reach for an internal worker package (V6-15B's own \"Không làm\")",
						pkg.ImportPath, imp)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no internal/delivery/cli packages were checked — go list pattern matched nothing")
	}
}
