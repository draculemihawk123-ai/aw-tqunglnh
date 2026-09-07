package contextassembler

import (
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func candidate(key, ownerVersion, hash string, priority definition.PriorityClass, selector Selector, size int) Candidate {
	return Candidate{
		Identity:   definition.ResourceIdentity{OwnerVersionID: ownerVersion, ResourceKey: key, ContentHash: hash},
		Priority:   priority,
		Selector:   selector,
		Provenance: Provenance{Owner: "owner-" + key, Source: "source-" + key},
		Payload:    make([]byte, size),
	}
}

func bigBudget() Budget { return Budget{MaxBytes: 1 << 20} }

// --- Selector matrix: component/task/block/risk ---

func TestMatchesContext_GlobalSelector_MatchesEveryContext(t *testing.T) {
	s := Selector{}
	if !s.MatchesContext(ResolutionContext{}) {
		t.Fatal("empty Selector should match an empty context")
	}
	if !s.MatchesContext(ResolutionContext{ComponentTags: []string{"api"}, RiskClass: "high"}) {
		t.Fatal("empty Selector should match any populated context")
	}
}

func TestMatchesContext_SelectorMatrix(t *testing.T) {
	cases := []struct {
		name     string
		selector Selector
		ctx      ResolutionContext
		want     bool
	}{
		{"ComponentTags match", Selector{ComponentTags: []string{"api", "web"}}, ResolutionContext{ComponentTags: []string{"web"}}, true},
		{"ComponentTags no overlap", Selector{ComponentTags: []string{"api"}}, ResolutionContext{ComponentTags: []string{"web"}}, false},
		{"PathTags match", Selector{PathTags: []string{"services/api"}}, ResolutionContext{PathTags: []string{"services/api", "docs"}}, true},
		{"PathTags no overlap", Selector{PathTags: []string{"services/api"}}, ResolutionContext{PathTags: []string{"docs"}}, false},
		{"TaskKinds match", Selector{TaskKinds: []string{"bug-fix"}}, ResolutionContext{TaskKind: "bug-fix"}, true},
		{"TaskKinds mismatch", Selector{TaskKinds: []string{"bug-fix"}}, ResolutionContext{TaskKind: "feature"}, false},
		{"BlockKinds match", Selector{BlockKinds: []string{"AGENT"}}, ResolutionContext{BlockKind: "AGENT"}, true},
		{"BlockKinds mismatch", Selector{BlockKinds: []string{"AGENT"}}, ResolutionContext{BlockKind: "COMMAND"}, false},
		{"RiskClasses match", Selector{RiskClasses: []string{"high"}}, ResolutionContext{RiskClass: "high"}, true},
		{"RiskClasses mismatch", Selector{RiskClasses: []string{"high"}}, ResolutionContext{RiskClass: "low"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.selector.MatchesContext(tc.ctx); got != tc.want {
				t.Fatalf("MatchesContext = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchesContext_MultipleDimensions_AreANDedTogether(t *testing.T) {
	// A Selector declaring BOTH ComponentTags and RiskClasses must match
	// BOTH, not just one — this package's own documented AND-across-
	// declared-dimensions answer to the question V2-06 left open.
	s := Selector{ComponentTags: []string{"api"}, RiskClasses: []string{"high"}}

	if s.MatchesContext(ResolutionContext{ComponentTags: []string{"api"}, RiskClass: "low"}) {
		t.Fatal("component matches but risk does not — should not match under AND semantics")
	}
	if s.MatchesContext(ResolutionContext{ComponentTags: []string{"web"}, RiskClass: "high"}) {
		t.Fatal("risk matches but component does not — should not match under AND semantics")
	}
	if !s.MatchesContext(ResolutionContext{ComponentTags: []string{"api"}, RiskClass: "high"}) {
		t.Fatal("both dimensions match — should match")
	}
}

// --- Basic resolution: applicability, ordering ---

func TestResolve_OnlyApplicableCandidatesAreSelected(t *testing.T) {
	applicable := candidate("k1", "v1", "h1", definition.PriorityGuidance, Selector{RiskClasses: []string{"high"}}, 10)
	notApplicable := candidate("k2", "v1", "h2", definition.PriorityGuidance, Selector{RiskClasses: []string{"low"}}, 10)

	result, err := Resolve([]Candidate{applicable, notApplicable}, ResolutionContext{RiskClass: "high"}, bigBudget(), ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(result.Selected) != 1 || result.Selected[0].Identity.ResourceKey != "k1" {
		t.Fatalf("Selected = %+v, want exactly k1", result.Selected)
	}
	if len(result.Excluded) != 1 || result.Excluded[0].Reason != ReasonNotApplicable {
		t.Fatalf("Excluded = %+v, want exactly k2 with ReasonNotApplicable", result.Excluded)
	}
}

func TestResolve_OrdersHardConstraintFirstThenByPriority(t *testing.T) {
	reference := candidate("k-ref", "v1", "h1", definition.PriorityReference, Selector{}, 10)
	guidance := candidate("k-guide", "v1", "h1", definition.PriorityGuidance, Selector{}, 10)
	required := candidate("k-req", "v1", "h1", definition.PriorityRequiredProcedure, Selector{}, 10)
	hard := candidate("k-hard", "v1", "h1", definition.PriorityHardConstraint, Selector{}, 10)

	result, err := Resolve([]Candidate{reference, guidance, required, hard}, ResolutionContext{}, bigBudget(), ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(result.Selected) != 4 {
		t.Fatalf("len(Selected) = %d, want 4", len(result.Selected))
	}
	order := []string{result.Selected[0].Identity.ResourceKey, result.Selected[1].Identity.ResourceKey, result.Selected[2].Identity.ResourceKey, result.Selected[3].Identity.ResourceKey}
	want := []string{"k-hard", "k-req", "k-guide", "k-ref"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestResolve_DeterministicTieBreakByResourceKey(t *testing.T) {
	b := candidate("k-b", "v1", "h1", definition.PriorityGuidance, Selector{}, 10)
	a := candidate("k-a", "v1", "h1", definition.PriorityGuidance, Selector{}, 10)

	result, err := Resolve([]Candidate{b, a}, ResolutionContext{}, bigBudget(), ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(result.Selected) != 2 || result.Selected[0].Identity.ResourceKey != "k-a" || result.Selected[1].Identity.ResourceKey != "k-b" {
		t.Fatalf("Selected = %+v, want [k-a, k-b] regardless of input order", result.Selected)
	}
}

// --- Deduplication ---

func TestResolve_DeduplicatesIdenticalIdentity(t *testing.T) {
	c := candidate("k1", "v1", "h1", definition.PriorityGuidance, Selector{}, 100)
	// The exact same candidate, reached twice (e.g. via two different Pack
	// paths).
	result, err := Resolve([]Candidate{c, c}, ResolutionContext{}, Budget{MaxBytes: 150}, ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(result.Selected) != 1 {
		t.Fatalf("len(Selected) = %d, want 1 (deduplicated)", len(result.Selected))
	}
	if result.SpentBytes != 100 {
		t.Fatalf("SpentBytes = %d, want 100 (charged once, not twice)", result.SpentBytes)
	}
}

// --- Conflict detection ---

func TestResolve_HardConstraintConflict_DifferentContentHash(t *testing.T) {
	a := candidate("shared-key", "v1", "hash-a", definition.PriorityHardConstraint, Selector{}, 10)
	b := candidate("shared-key", "v2", "hash-b", definition.PriorityHardConstraint, Selector{}, 10)

	_, err := Resolve([]Candidate{a, b}, ResolutionContext{}, bigBudget(), ByteCostEstimator{})
	var conflictErr *HardConstraintConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("err = %v, want *HardConstraintConflictError", err)
	}
	if len(conflictErr.Conflicts) != 1 || conflictErr.Conflicts[0].ResourceKey != "shared-key" {
		t.Fatalf("Conflicts = %+v, want exactly one conflict on shared-key", conflictErr.Conflicts)
	}
	if len(conflictErr.Conflicts[0].Candidates) != 2 {
		t.Fatalf("Conflicts[0].Candidates = %+v, want both colliding candidates", conflictErr.Conflicts[0].Candidates)
	}
}

func TestResolve_NoConflict_SameContentHash_DifferentOwner(t *testing.T) {
	// Same ResourceKey and ContentHash (identical content), republished
	// under a different OwnerVersionID — not deduplicated (different
	// Identity), but also not a conflict (no disagreement in content).
	a := candidate("shared-key", "v1", "same-hash", definition.PriorityHardConstraint, Selector{}, 10)
	b := candidate("shared-key", "v2", "same-hash", definition.PriorityHardConstraint, Selector{}, 10)

	result, err := Resolve([]Candidate{a, b}, ResolutionContext{}, bigBudget(), ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(result.Selected) != 2 {
		t.Fatalf("len(Selected) = %d, want 2 (both included, no conflict)", len(result.Selected))
	}
}

func TestResolve_NoConflict_DifferentHash_NeitherHardConstraint(t *testing.T) {
	a := candidate("shared-key", "v1", "hash-a", definition.PriorityGuidance, Selector{}, 10)
	b := candidate("shared-key", "v2", "hash-b", definition.PriorityGuidance, Selector{}, 10)

	result, err := Resolve([]Candidate{a, b}, ResolutionContext{}, bigBudget(), ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v (GUIDANCE disagreement is not a fail-closed conflict)", err)
	}
	if len(result.Selected) != 2 {
		t.Fatalf("len(Selected) = %d, want 2", len(result.Selected))
	}
}

func TestResolve_NoConflict_WhenOneSideIsNotApplicable(t *testing.T) {
	// Two HARD_CONSTRAINT candidates disagree, but only one is applicable
	// to this context — conflict detection only considers applicable
	// candidates.
	a := candidate("shared-key", "v1", "hash-a", definition.PriorityHardConstraint, Selector{RiskClasses: []string{"high"}}, 10)
	b := candidate("shared-key", "v2", "hash-b", definition.PriorityHardConstraint, Selector{RiskClasses: []string{"low"}}, 10)

	result, err := Resolve([]Candidate{a, b}, ResolutionContext{RiskClass: "high"}, bigBudget(), ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(result.Selected) != 1 || result.Selected[0].Identity.OwnerVersionID != "v1" {
		t.Fatalf("Selected = %+v, want exactly the one applicable candidate", result.Selected)
	}
}

func TestResolve_MultipleConflicts_AllReported(t *testing.T) {
	a1 := candidate("key-a", "v1", "hash-1", definition.PriorityHardConstraint, Selector{}, 10)
	a2 := candidate("key-a", "v2", "hash-2", definition.PriorityHardConstraint, Selector{}, 10)
	b1 := candidate("key-b", "v1", "hash-1", definition.PriorityHardConstraint, Selector{}, 10)
	b2 := candidate("key-b", "v2", "hash-2", definition.PriorityHardConstraint, Selector{}, 10)

	_, err := Resolve([]Candidate{a1, a2, b1, b2}, ResolutionContext{}, bigBudget(), ByteCostEstimator{})
	var conflictErr *HardConstraintConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("err = %v, want *HardConstraintConflictError", err)
	}
	if len(conflictErr.Conflicts) != 2 {
		t.Fatalf("len(Conflicts) = %d, want 2 (both key-a and key-b)", len(conflictErr.Conflicts))
	}
	if conflictErr.Conflicts[0].ResourceKey != "key-a" || conflictErr.Conflicts[1].ResourceKey != "key-b" {
		t.Fatalf("Conflicts = %+v, want sorted [key-a, key-b]", conflictErr.Conflicts)
	}
}

// --- Budget ---

func TestResolve_HardConstraintNeverDropped_EvenUnderTightBudget(t *testing.T) {
	hard := candidate("k-hard", "v1", "h1", definition.PriorityHardConstraint, Selector{}, 50)
	reference := candidate("k-ref", "v1", "h1", definition.PriorityReference, Selector{}, 50)

	result, err := Resolve([]Candidate{hard, reference}, ResolutionContext{}, Budget{MaxBytes: 60}, ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(result.Selected) != 1 || result.Selected[0].Identity.ResourceKey != "k-hard" {
		t.Fatalf("Selected = %+v, want only k-hard", result.Selected)
	}
	if len(result.Excluded) != 1 || result.Excluded[0].Reason != ReasonBudgetExceeded {
		t.Fatalf("Excluded = %+v, want k-ref with ReasonBudgetExceeded", result.Excluded)
	}
}

func TestResolve_RequiredContextExceedsBudget_HardConstraintAlone(t *testing.T) {
	hard := candidate("k-hard", "v1", "h1", definition.PriorityHardConstraint, Selector{}, 100)

	_, err := Resolve([]Candidate{hard}, ResolutionContext{}, Budget{MaxBytes: 50}, ByteCostEstimator{})
	var budgetErr *RequiredContextExceedsBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("err = %v, want *RequiredContextExceedsBudgetError", err)
	}
	if budgetErr.RequiredBytes != 100 || budgetErr.AvailableBytes != 50 {
		t.Fatalf("budgetErr = %+v, want RequiredBytes=100 AvailableBytes=50", budgetErr)
	}
}

func TestResolve_ReservedBytes_ReducesAvailableCapacity(t *testing.T) {
	guidance := candidate("k1", "v1", "h1", definition.PriorityGuidance, Selector{}, 60)

	result, err := Resolve([]Candidate{guidance}, ResolutionContext{}, Budget{MaxBytes: 100, ReservedBytes: 50}, ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Only 50 bytes available (100-50); the 60-byte candidate must not fit.
	if len(result.Selected) != 0 {
		t.Fatalf("Selected = %+v, want empty (candidate exceeds the reserved-adjusted budget)", result.Selected)
	}
	if len(result.Excluded) != 1 || result.Excluded[0].Reason != ReasonBudgetExceeded {
		t.Fatalf("Excluded = %+v, want ReasonBudgetExceeded", result.Excluded)
	}
}

func TestResolve_ReservedBytes_ExceedingMaxBytes_LeavesZeroAvailable(t *testing.T) {
	guidance := candidate("k1", "v1", "h1", definition.PriorityGuidance, Selector{}, 1)

	result, err := Resolve([]Candidate{guidance}, ResolutionContext{}, Budget{MaxBytes: 10, ReservedBytes: 999}, ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(result.Selected) != 0 {
		t.Fatalf("Selected = %+v, want empty when ReservedBytes >= MaxBytes", result.Selected)
	}
}

func TestResolve_GreedyContinuesPastASkippedCandidate(t *testing.T) {
	big := candidate("k-big", "v1", "h1", definition.PriorityGuidance, Selector{}, 80)
	small := candidate("k-small", "v1", "h1", definition.PriorityGuidance, Selector{}, 10)

	// big sorts before small (ResourceKey "k-big" < "k-small"); with a
	// 50-byte budget, big must not fit but small still should.
	result, err := Resolve([]Candidate{big, small}, ResolutionContext{}, Budget{MaxBytes: 50}, ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(result.Selected) != 1 || result.Selected[0].Identity.ResourceKey != "k-small" {
		t.Fatalf("Selected = %+v, want exactly k-small (greedy must not stop at the first miss)", result.Selected)
	}
	if len(result.Excluded) != 1 || result.Excluded[0].Identity.ResourceKey != "k-big" || result.Excluded[0].Reason != ReasonBudgetExceeded {
		t.Fatalf("Excluded = %+v, want k-big with ReasonBudgetExceeded", result.Excluded)
	}
}

func TestResolve_RecordsCostModelAndSpentBytes(t *testing.T) {
	c := candidate("k1", "v1", "h1", definition.PriorityGuidance, Selector{}, 42)

	result, err := Resolve([]Candidate{c}, ResolutionContext{}, bigBudget(), ByteCostEstimator{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.CostModel != CostModelUTF8BytesV1 {
		t.Fatalf("CostModel = %q, want %q", result.CostModel, CostModelUTF8BytesV1)
	}
	if result.SpentBytes != 42 {
		t.Fatalf("SpentBytes = %d, want 42", result.SpentBytes)
	}
	if result.Selected[0].Cost != 42 {
		t.Fatalf("Selected[0].Cost = %d, want 42", result.Selected[0].Cost)
	}
}

// --- Determinism ---

func TestResolve_DeterministicAcrossInputOrder(t *testing.T) {
	c1 := candidate("k1", "v1", "h1", definition.PriorityGuidance, Selector{}, 10)
	c2 := candidate("k2", "v1", "h1", definition.PriorityReference, Selector{}, 10)
	c3 := candidate("k3", "v1", "h1", definition.PriorityHardConstraint, Selector{}, 10)
	c4 := candidate("k4", "v1", "h1", definition.PriorityRequiredProcedure, Selector{}, 10)

	orderings := [][]Candidate{
		{c1, c2, c3, c4},
		{c4, c3, c2, c1},
		{c2, c4, c1, c3},
		{c3, c1, c4, c2},
	}

	var first Resolution
	for i, ordering := range orderings {
		result, err := Resolve(ordering, ResolutionContext{}, bigBudget(), ByteCostEstimator{})
		if err != nil {
			t.Fatalf("Resolve (ordering %d): %v", i, err)
		}
		if i == 0 {
			first = result
			continue
		}
		if len(result.Selected) != len(first.Selected) {
			t.Fatalf("ordering %d: len(Selected) = %d, want %d", i, len(result.Selected), len(first.Selected))
		}
		for j := range result.Selected {
			if result.Selected[j].Identity != first.Selected[j].Identity {
				t.Fatalf("ordering %d: Selected[%d] = %+v, want %+v (order must not depend on input order)",
					i, j, result.Selected[j].Identity, first.Selected[j].Identity)
			}
		}
	}
}
