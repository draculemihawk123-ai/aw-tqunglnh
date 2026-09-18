package apicontract

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// TestDetectBreakingChanges_DetectsEveryInjectedBreak is this package's
// own "prove the guard actually works" test (this codebase's established
// discipline — see e.g. internal/delivery/httpapi/route_test.go's own
// duplicate-registration panic tests): it builds a synthetic old Contract,
// derives four different new Contracts by injecting exactly ONE breaking
// change into a copy each time (a removed operation, a changed method, a
// changed path, a changed scope), and asserts DetectBreakingChanges
// reports EXACTLY that one change and nothing else. It deliberately never
// touches the real, evolving production contract — a synthetic fixture
// means this test can never accidentally start passing (or failing) for a
// reason unrelated to DetectBreakingChanges' own logic.
func TestDetectBreakingChanges_DetectsEveryInjectedBreak(t *testing.T) {
	base := Contract{
		Version: "1",
		Operations: []Operation{
			{OperationID: "createThing", Method: "POST", Path: "/things", ScopeKind: "PROJECT"},
			{OperationID: "getThing", Method: "GET", Path: "/things/{id}", ScopeKind: "PROJECT"},
			{OperationID: "listThings", Method: "GET", Path: "/things", ScopeKind: "INSTALLATION"},
		},
	}

	cloneOps := func() []Operation {
		out := make([]Operation, len(base.Operations))
		copy(out, base.Operations)
		return out
	}

	t.Run("removed operation", func(t *testing.T) {
		ops := cloneOps()
		ops = append(ops[:1], ops[2:]...) // drop getThing
		newContract := Contract{Version: "1", Operations: ops}

		changes := DetectBreakingChanges(base, newContract)
		mustHaveExactly(t, changes, BreakingChange{
			Kind: "REMOVED_OPERATION", OperationID: "getThing",
			Detail: "GET /things/{id} is no longer registered",
		})
	})

	t.Run("method changed", func(t *testing.T) {
		ops := cloneOps()
		ops[0].Method = "PUT" // createThing POST -> PUT
		newContract := Contract{Version: "1", Operations: ops}

		changes := DetectBreakingChanges(base, newContract)
		mustHaveExactly(t, changes, BreakingChange{
			Kind: "METHOD_CHANGED", OperationID: "createThing",
			Detail: "POST -> PUT (path /things)",
		})
	})

	t.Run("path changed (rename)", func(t *testing.T) {
		ops := cloneOps()
		ops[1].Path = "/widgets/{id}" // getThing renamed path
		newContract := Contract{Version: "1", Operations: ops}

		changes := DetectBreakingChanges(base, newContract)
		mustHaveExactly(t, changes, BreakingChange{
			Kind: "PATH_CHANGED", OperationID: "getThing",
			Detail: "/things/{id} -> /widgets/{id}",
		})
	})

	t.Run("scope changed", func(t *testing.T) {
		ops := cloneOps()
		ops[2].ScopeKind = "PROJECT" // listThings INSTALLATION -> PROJECT
		newContract := Contract{Version: "1", Operations: ops}

		changes := DetectBreakingChanges(base, newContract)
		mustHaveExactly(t, changes, BreakingChange{
			Kind: "SCOPE_CHANGED", OperationID: "listThings",
			Detail: "INSTALLATION -> PROJECT",
		})
	})

	t.Run("additive change is not breaking", func(t *testing.T) {
		ops := cloneOps()
		ops = append(ops, Operation{OperationID: "deleteThing", Method: "DELETE", Path: "/things/{id}", ScopeKind: "PROJECT"})
		newContract := Contract{Version: "1", Operations: ops}

		changes := DetectBreakingChanges(base, newContract)
		if len(changes) != 0 {
			t.Fatalf("adding a new operation must not be reported as breaking, got %v", changes)
		}
	})

	t.Run("identical contract has no changes", func(t *testing.T) {
		changes := DetectBreakingChanges(base, base)
		if len(changes) != 0 {
			t.Fatalf("comparing a contract against itself must report zero changes, got %v", changes)
		}
	})
}

func mustHaveExactly(t *testing.T, got []BreakingChange, want BreakingChange) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("got %d breaking changes, want exactly 1: %v", len(got), got)
	}
	if got[0] != want {
		t.Fatalf("got breaking change %+v, want %+v", got[0], want)
	}
}

// TestBreakingChangeGate_RealContractHasNoBreakingChangesFromGolden is
// V6-12's own "breaking-change diff gate" Verify bullet applied to the
// REAL, currently-committed golden fixture: it decodes
// testdata/golden/contract.json (the last deliberately-committed contract
// snapshot) and the freshly generated contract from the real production
// route composition, and fails — by NAME, listing every offending
// operationId and what changed — if DetectBreakingChanges finds anything.
//
// A passing run here does not by itself mean the two contracts are
// IDENTICAL (an additive change, e.g. a brand new operation, would still
// make TestContract_MatchesGoldenFixture fail while this test stays
// green) — the two tests are deliberately complementary: the golden test
// catches ANY drift and demands a conscious fixture update, this one
// specifically calls out whether that drift would have broken an existing
// client.
func TestBreakingChangeGate_RealContractHasNoBreakingChangesFromGolden(t *testing.T) {
	goldenData, err := os.ReadFile(goldenContractPath)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", goldenContractPath, err)
	}
	var golden Contract
	if err := json.Unmarshal(goldenData, &golden); err != nil {
		t.Fatalf("decode golden fixture: %v", err)
	}

	fresh, _ := generateContractJSON(t)

	changes := DetectBreakingChanges(golden, fresh)
	if len(changes) == 0 {
		return
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].OperationID != changes[j].OperationID {
			return changes[i].OperationID < changes[j].OperationID
		}
		return changes[i].Kind < changes[j].Kind
	})
	t.Errorf("%d breaking change(s) versus %s — review each one, then decide whether the change is "+
		"actually intended before regenerating the golden fixture:", len(changes), goldenContractPath)
	for _, c := range changes {
		t.Errorf("  %s", c.String())
	}
}
