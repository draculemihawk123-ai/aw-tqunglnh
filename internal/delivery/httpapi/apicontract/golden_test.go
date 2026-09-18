package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// goldenContractPath is the one committed fixture this whole test file
// (and breaking_test.go's own "real" gate) reads: the exact JSON
// apicontract.Build produces for the real, fully-composed production
// route set (internal/delivery/httpcompose.ComposeRoutes, built with real
// temporary infrastructure — see composetest_test.go's own
// buildRealRegistry), marshaled with json.MarshalIndent(contract, "", "
// ") plus a trailing newline — exactly what generateContractJSON below
// reproduces.
const goldenContractPath = "testdata/golden/contract.json"

// generateContractJSON reproduces the fresh Contract for the CURRENT
// production route composition, serialized the identical way the
// committed golden fixture was generated — both this test file and
// breaking_test.go's own gate call it so there is exactly one definition
// of "what does freshly generated look like" in this package.
func generateContractJSON(t *testing.T) (Contract, []byte) {
	t.Helper()
	routes := buildRealRegistry(t)
	contract := Build(routes.Descriptors())
	data, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		t.Fatalf("marshal contract: %v", err)
	}
	data = append(data, '\n')
	return contract, data
}

// TestContract_MatchesGoldenFixture is V6-12's own "golden-file schema
// validation" Verify bullet: it regenerates the contract fresh, from the
// real production route composition, and byte-for-byte compares it
// against the committed testdata/golden/contract.json. ANY change to the
// real route set — additive or breaking — makes this fail, forcing a
// developer to look at the diff and consciously regenerate+commit the
// golden fixture (see breaking.go's own doc comment for why this,
// combined with breaking_test.go's own labeled gate, is the whole
// "acknowledgment mechanism" V6-12's own breaking-diff gate needs, with no
// separate version-marker field).
func TestContract_MatchesGoldenFixture(t *testing.T) {
	_, got := generateContractJSON(t)

	want, err := os.ReadFile(filepath.Join(goldenContractPath))
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", goldenContractPath, err)
	}

	if string(got) != string(want) {
		t.Errorf("generated contract does not match %s.\n"+
			"If this is an intentional route change, regenerate the fixture "+
			"(see this test's own doc comment) and review the diff before committing.\n"+
			"--- got ---\n%s\n--- want ---\n%s", goldenContractPath, got, want)
	}
}

// TestContract_IsDeterministic proves apicontract.Build produces
// byte-identical output across repeated calls against the SAME underlying
// route registry — Contract.Operations is sorted by OperationID
// specifically so the golden comparison above never flakes on map/slice
// iteration order.
func TestContract_IsDeterministic(t *testing.T) {
	routes := buildRealRegistry(t)
	descriptors := routes.Descriptors()

	first, err := json.Marshal(Build(descriptors))
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	for i := 0; i < 5; i++ {
		next, err := json.Marshal(Build(descriptors))
		if err != nil {
			t.Fatalf("marshal iteration %d: %v", i, err)
		}
		if string(first) != string(next) {
			t.Fatalf("Build is non-deterministic: iteration %d differs from the first", i)
		}
	}
}
