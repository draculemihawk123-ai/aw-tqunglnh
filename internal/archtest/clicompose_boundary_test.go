// This file is V6-15O's own architecture guard for the two packages it adds:
// internal/delivery/clicompose (the CLI composition/router) and
// internal/delivery/parity (the four-way checker). ADR-028: "CLI là delivery
// adapter ngang hàng với HTTP ... MUST NOT ghi SQLite, gọi Git/process/
// provider trực tiếp" and "Composition root được phép wire concrete adapters;
// handler CLI chỉ phụ thuộc application ports/use cases". clicompose is
// composition of LEAF code, not a composition root — the concrete adapters
// are built by cmd/aw and handed in through clicompose.Deps — so it (and the
// checker, whose non-test code reads only descriptors, routes and a parsed
// document) may import no adapter at all, and never a worker package
// directly. Test files are deliberately outside the check (`go list` without
// -test): the parity package's own tests build a real SQLite-backed server,
// exactly like internal/delivery/httpapi/apicontract's do.
package archtest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

var composeBoundaryPatterns = []string{"./internal/delivery/clicompose/...", "./internal/delivery/parity/..."}

func TestDeliveryCLIComposeNeverImportsAdaptersOrWorkers(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	output := goList(t, moduleRoot, append([]string{"-json"}, composeBoundaryPatterns...)...)

	type goListPackage struct {
		ImportPath string
		Imports    []string
		Deps       []string
	}

	const module = "agent-workflow/internal/"
	forbiddenTransitive := []string{
		module + "adapters/sqlite",
		module + "adapters/gitworktree",
		module + "adapters/providers",
		module + "adapters/process",
		module + "adapters/artifactstore",
	}
	forbiddenDirect := []string{module + "adapters/", module + "app/worker", module + "app/workerpool"}

	decoder := json.NewDecoder(bytes.NewReader(output))
	checked := 0
	for decoder.More() {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		checked++
		for _, dep := range pkg.Deps {
			for _, forbidden := range forbiddenTransitive {
				if strings.Contains(dep, forbidden) {
					t.Errorf("%s depends (even transitively) on %s — the CLI composition must never reach a concrete adapter (ADR-028); cmd/aw builds adapters and passes them in as clicompose.Deps", pkg.ImportPath, dep)
				}
			}
		}
		for _, imp := range pkg.Imports {
			for _, forbidden := range forbiddenDirect {
				if strings.Contains(imp, forbidden) {
					t.Errorf("%s directly imports %s — forbidden for the CLI composition and the parity checker", pkg.ImportPath, imp)
				}
			}
		}
	}
	if checked < 2 {
		t.Fatalf("only %d packages checked — the go list patterns %v matched too little", checked, composeBoundaryPatterns)
	}
}
