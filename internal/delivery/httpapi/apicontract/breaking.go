package apicontract

import "fmt"

// BreakingChange is one compatibility break DetectBreakingChanges found
// between an old (previously committed golden) Contract and a new
// (freshly generated) one.
type BreakingChange struct {
	Kind        string `json:"kind"`
	OperationID string `json:"operationId"`
	Detail      string `json:"detail"`
}

// String renders a BreakingChange as a single human-readable line — every
// caller of DetectBreakingChanges that fails a test on a non-empty result
// uses this for the failure message, so a developer reading `go test`
// output sees exactly which operation broke and how, not just a raw diff.
func (b BreakingChange) String() string {
	return fmt.Sprintf("%s %s: %s", b.Kind, b.OperationID, b.Detail)
}

// DetectBreakingChanges compares old (the previously committed/golden
// Contract) against new (a freshly generated one) and reports every change
// V6-12 defines as breaking for an HTTP client already coded against old:
//
//   - REMOVED_OPERATION — an operationId present in old is absent from new.
//   - METHOD_CHANGED — an operationId present in both now has a different
//     HTTP method.
//   - PATH_CHANGED — an operationId present in both now has a different
//     path template.
//   - SCOPE_CHANGED — an operationId present in both now has a different
//     ScopeKind (INSTALLATION vs PROJECT) — a client's own authorization/
//     routing decision for that call would silently become wrong even
//     though the method/path/operationId all still match.
//
// A NEW operationId appearing in new that was absent from old is
// deliberately NOT reported — adding an operation is additive, not
// breaking. Field-level schema changes (a response losing/renaming a
// field) are also deliberately out of this gate's scope: SchemaRef is a
// best-effort, shallow reflection (see contract.go's own doc comment), and
// a harmless-in-practice Go-side refactor (reordering fields, adding
// `omitempty`) would produce false positives that train reviewers to
// ignore this gate. The four checks above are the ones V6-12's own Verify
// bullet ("at minimum: a removed operationId, a removed/renamed route
// path, a changed HTTP method for an existing operationId") names or
// directly implies (SCOPE_CHANGED is this package's own addition, for the
// same reason PATH/METHOD changed are: it silently changes what a client
// must do to call successfully, without renaming the operationId a client
// already depends on).
//
// The gate mechanism itself is deliberately the simplest of the two the
// design doc's own V6-12 entry allows: this function does not require a
// version-marker bump anywhere. Instead, golden_test.go's own plain
// byte-for-byte golden comparison already fails on ANY contract change
// (breaking or not), forcing a developer to consciously regenerate and
// commit testdata/golden/contract.json — a visible, reviewable diff in the
// PR. breaking_test.go's own gate test additionally calls
// DetectBreakingChanges(golden, freshlyGenerated) and fails LOUDLY, by
// name, when the change is breaking — giving a reviewer an explicit,
// labeled signal ("REMOVED_OPERATION getRun: ...") instead of leaving them
// to infer breakage from a raw JSON diff. Both failures point at the same
// remedy (review the diff, then update the golden fixture deliberately),
// so no separate version-marker field is needed.
func DetectBreakingChanges(old, new Contract) []BreakingChange {
	newByID := make(map[string]Operation, len(new.Operations))
	for _, op := range new.Operations {
		newByID[op.OperationID] = op
	}

	var changes []BreakingChange
	// Walk old.Operations (already OperationID-sorted by Build) for a
	// deterministic result order.
	for _, oldOp := range old.Operations {
		newOp, stillExists := newByID[oldOp.OperationID]
		if !stillExists {
			changes = append(changes, BreakingChange{
				Kind: "REMOVED_OPERATION", OperationID: oldOp.OperationID,
				Detail: fmt.Sprintf("%s %s is no longer registered", oldOp.Method, oldOp.Path),
			})
			continue
		}
		if newOp.Method != oldOp.Method {
			changes = append(changes, BreakingChange{
				Kind: "METHOD_CHANGED", OperationID: oldOp.OperationID,
				Detail: fmt.Sprintf("%s -> %s (path %s)", oldOp.Method, newOp.Method, oldOp.Path),
			})
		}
		if newOp.Path != oldOp.Path {
			changes = append(changes, BreakingChange{
				Kind: "PATH_CHANGED", OperationID: oldOp.OperationID,
				Detail: fmt.Sprintf("%s -> %s", oldOp.Path, newOp.Path),
			})
		}
		if newOp.ScopeKind != oldOp.ScopeKind {
			changes = append(changes, BreakingChange{
				Kind: "SCOPE_CHANGED", OperationID: oldOp.OperationID,
				Detail: fmt.Sprintf("%s -> %s", oldOp.ScopeKind, newOp.ScopeKind),
			})
		}
	}
	return changes
}
