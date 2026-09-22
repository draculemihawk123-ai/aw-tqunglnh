// V6-13's own "handler dependency graph ends at public ports" proof
// (docs/design/08-v6-api-projections.md V6-13: "no provider/Git/SQLite
// concrete in handler"; AK-ARCH-018, AK-ARCH-027, GC-INV-14, HE-10-M04).
// Every existing HTTP archtest guards ONE leaf package; these two cover the
// whole internal/delivery/httpapi tree plus internal/delivery/httpcompose (the
// package that wires every leaf), so a leaf added later is covered on arrival.
//
// Adapters are checked against the full transitive closure: nothing under
// internal/adapters is reachable from the HTTP delivery layer at all today.
// os/exec and the worker packages are checked as DIRECT imports only —
// httpapi legitimately reaches both several hops deep through the doctor/
// diagnostics reporting path (the same caveat cli_boundary_test.go records),
// so a transitive ban would be unsatisfiable. The invariant that matters is
// that no delivery package itself spawns a process or drives a worker.
package archtest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

var httpDeliveryPatterns = []string{"./internal/delivery/httpapi/...", "./internal/delivery/httpcompose/..."}

func TestDeliveryHTTPAPINeverReachesAnyAdapterTransitively(t *testing.T) {
	output := goList(t, findModuleRoot(t), append([]string{"-json"}, httpDeliveryPatterns...)...)

	type goListPackage struct {
		ImportPath string
		Deps       []string
	}

	const forbidden = "agent-workflow/internal/adapters/"
	decoder := json.NewDecoder(bytes.NewReader(output))
	checked := 0
	for decoder.More() {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		checked++
		for _, dep := range pkg.Deps {
			if strings.Contains(dep, forbidden) {
				t.Errorf("%s depends (even transitively) on %s — the HTTP delivery layer must end at public ports, never a SQLite/Git/provider/filesystem adapter (V6-13)", pkg.ImportPath, dep)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no HTTP delivery packages were checked — go list patterns matched nothing")
	}
}

func TestDeliveryHTTPAPINeverDirectlyImportsProcessWorkerOrAdapterPackages(t *testing.T) {
	output := goList(t, findModuleRoot(t), append([]string{"-json"}, httpDeliveryPatterns...)...)

	type goListPackage struct {
		ImportPath string
		Imports    []string
	}

	forbiddenPrefixes := []string{
		"agent-workflow/internal/adapters/",
		"agent-workflow/internal/app/worker",
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
			if imp == "os/exec" {
				t.Errorf("%s directly imports os/exec — the API process must never spawn a CLI/Git process itself (AK-ARCH-018)", pkg.ImportPath)
			}
			for _, prefix := range forbiddenPrefixes {
				if strings.Contains(imp, prefix) {
					t.Errorf("%s directly imports %s — an HTTP handler must never reach a worker or adapter package (AK-ARCH-027, GC-INV-14)", pkg.ImportPath, imp)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no HTTP delivery packages were checked — go list patterns matched nothing")
	}
}
