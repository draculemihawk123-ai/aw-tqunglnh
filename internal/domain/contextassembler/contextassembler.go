// Package contextassembler is V5-03's pure resolution algorithm
// (docs/design/07-v5-execution-evidence.md V5-03; HE-04-M05, HE-04-S03,
// HE-13-M08): given every reachable resource "candidate" (a Skill/Layer
// resource, an EngineeringPack-derived pin, a V5-02 Message, or any other
// resource kind a caller projects into Candidate) and the task/attempt
// context to resolve against, Resolve determines which candidates apply,
// detects HARD_CONSTRAINT conflicts among them, orders the rest by
// priority/relevance, and greedily includes candidates until a caller-
// supplied byte budget is exhausted.
//
// This package does no I/O and persists nothing — gathering candidates
// from real Definitions/Messages/Catalog rows and persisting the result
// as a durable, hash-verified ContextSnapshot is V5-04's own separate job
// (HE-04-M06, explicitly NOT one of this task's own Nguồn citations); this
// package only ever returns an in-memory Resolution value. It also never
// touches internal/domain/runtime.ContextSnapshot (a pre-existing,
// differently-shaped spike-era type still wired to the legacy
// WorkflowPersistence interface) — reconciling the two, if ever needed, is
// left entirely to whichever later task actually persists this package's
// own Resolution.
package contextassembler

import (
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// Selector is the applicability declaration HE-04-M02 requires every
// non-global resource to carry: component/path tag, task kind, block kind
// or risk class. It mirrors internal/domain/skill.Selector and
// internal/domain/layer.Selector field-for-field but is declared fresh
// here rather than imported from either: this package must not depend on
// specific authoring kinds (Skill/Layer today, potentially others later)
// — a caller projects whatever kind-specific Selector it has into this
// one shape when building a Candidate.
//
// A Selector with every dimension empty applies to every context — the
// same "Global" resource semantic skill.Resource/layer.Resource already
// express with their own explicit Global bool, expressed here structurally
// instead (see MatchesContext).
type Selector struct {
	ComponentTags []string
	PathTags      []string
	TaskKinds     []string
	BlockKinds    []string
	RiskClasses   []string
}

// ResolutionContext is what a caller resolves candidates against — the
// real Component/task/block/risk facts for one attempt, gathered from
// wherever those actually live (a Component's own tags, a WorkItem's own
// RiskLevel, the executing Block's own kind, ...). ComponentTags/PathTags
// are sets (an attempt's own component/path context can genuinely satisfy
// more than one tag at once); TaskKind/BlockKind/RiskClass are singular —
// an attempt has exactly one of each at a time.
type ResolutionContext struct {
	ComponentTags []string
	PathTags      []string
	TaskKind      string
	BlockKind     string
	RiskClass     string
}

// MatchesContext reports whether s applies to ctx.
//
// V2-06 deliberately left open whether a Selector's own populated
// dimensions combine with AND or OR across each other
// (internal/domain/skill.Selector's own doc comment: "a narrower
// AND-across-dimensions selector language is a context-assembler concern
// for a later task, not a shape this authoring schema needs to
// pre-decide") — this is this package's own explicit answer to that open
// question, flagged here for review since no citation forces this
// specific choice: every dimension s DECLARES (non-empty) must have at
// least one member in common with ctx's own matching dimension
// (AND-across-declared-dimensions, OR-within-one-dimension — the same
// semantics Kubernetes label selectors use). A dimension s leaves empty
// places no restriction from that dimension. The more conservative,
// narrower reading was chosen over a broader OR-across-dimensions
// reading because HE-04's whole lecture is about avoiding over-broad
// instruction loading (its own failure-mode list names "route không có
// điều kiện áp dụng nên mọi resource đều được nạp" — a route with no real
// applicability condition, so every resource gets loaded — as exactly the
// failure this exists to prevent).
func (s Selector) MatchesContext(ctx ResolutionContext) bool {
	return matchSet(s.ComponentTags, ctx.ComponentTags) &&
		matchSet(s.PathTags, ctx.PathTags) &&
		matchSingle(s.TaskKinds, ctx.TaskKind) &&
		matchSingle(s.BlockKinds, ctx.BlockKind) &&
		matchSingle(s.RiskClasses, ctx.RiskClass)
}

func matchSet(declared, actual []string) bool {
	if len(declared) == 0 {
		return true
	}
	actualSet := make(map[string]bool, len(actual))
	for _, a := range actual {
		actualSet[a] = true
	}
	for _, d := range declared {
		if actualSet[d] {
			return true
		}
	}
	return false
}

func matchSingle(declared []string, actual string) bool {
	if len(declared) == 0 {
		return true
	}
	for _, d := range declared {
		if d == actual {
			return true
		}
	}
	return false
}

// Provenance is a resource's own source/owner/review trail — the same
// shape skill.Provenance/layer.Provenance already carry (HE-04-M04),
// declared fresh here for the identical reason Selector is: this package
// must not import a specific authoring kind's own package.
type Provenance struct {
	Owner        string
	Source       string
	LastVerified string
	Revision     string
}

// Candidate is one resource under consideration for inclusion in a
// Resolution — a caller-gathered projection of a Skill/Layer Resource, an
// EngineeringPack-derived pin, a V5-02 Message, or any other resource
// kind, into the one shape Resolve's own applicability/conflict/budget
// logic needs.
type Candidate struct {
	// Identity is ADR-012's own passive-resource identity tuple. Two
	// Candidates with the IDENTICAL Identity (same OwnerVersionID,
	// ResourceKey AND ContentHash) are treated as the same candidate
	// reached twice (e.g. via two different Pack paths) and deduplicated
	// before resolution — see Resolve's own doc comment. Candidates that
	// merely SHARE a ResourceKey with a different ContentHash (a possible
	// real disagreement) or a different OwnerVersionID (a possible
	// republish of identical content under a new version — HE-04-M07's
	// own "single canonical rule" concern) are deliberately NOT deduped
	// further here: HE-04-M07 is not one of this task's own Nguồn
	// citations, and collapsing those cases is a judgment call left to
	// whichever task actually owns that criterion.
	Identity   definition.ResourceIdentity
	Priority   definition.PriorityClass
	Selector   Selector
	Provenance Provenance
	// Payload is the exact canonical bytes this candidate would render as
	// — CostEstimator measures THIS, never a summary/preview of it, so
	// budget accounting always reflects what would actually be included.
	Payload []byte
}

// CostModelUTF8BytesV1 names ByteCostEstimator's own cost model —
// recorded on every Resolution (Resolution.CostModel) so a result is
// never ambiguous about which estimator produced it, and so a later
// change of cost model is always visible rather than silently different.
const CostModelUTF8BytesV1 = "UTF8_BYTES_V1"

// CostEstimator measures how much of the budget one Candidate consumes.
// Deliberately not called a "token" estimate anywhere in this package: no
// real tokenizer is wired yet (docs/architecture/04-go-core-spec.md §19
// has no context/token-budget configuration surface today). A later task
// can substitute a real tokenizer behind this same interface without
// changing Resolve's own contract, as long as it names itself distinctly
// via Name() so every existing Resolution stays honestly labeled with the
// model that actually produced it.
type CostEstimator interface {
	Name() string
	Cost(c Candidate) uint64
}

// ByteCostEstimator is the default CostEstimator: a Candidate's own exact
// UTF-8 byte length.
type ByteCostEstimator struct{}

func (ByteCostEstimator) Name() string { return CostModelUTF8BytesV1 }

func (ByteCostEstimator) Cost(c Candidate) uint64 { return uint64(len(c.Payload)) }

var _ CostEstimator = ByteCostEstimator{}

// Budget bounds how much of the resolved candidate set Resolve may
// include. MaxBytes is the full ceiling; ReservedBytes is capacity Resolve
// must leave unspent for task/code/tool-output context this resolver
// itself never sees (HE-04-S03: "giữ reserved budget cho task, code và
// tool output") — only MaxBytes-ReservedBytes is ever available to
// candidates.
type Budget struct {
	MaxBytes      uint64
	ReservedBytes uint64
}

// available returns the byte capacity left for candidates once
// ReservedBytes is set aside — zero, never negative, if ReservedBytes
// already consumes the whole budget.
func (b Budget) available() uint64 {
	if b.ReservedBytes >= b.MaxBytes {
		return 0
	}
	return b.MaxBytes - b.ReservedBytes
}

// SelectionReason explains why a candidate was, or was not, included in a
// Resolution — HE-04-M06's own "lý do chọn" requirement in spirit (this
// package only ever produces this in memory; V5-04 is what persists it as
// a durable manifest).
type SelectionReason string

const (
	// ReasonSelected: included in Resolution.Selected.
	ReasonSelected SelectionReason = "SELECTED"
	// ReasonNotApplicable: Selector.MatchesContext was false — this
	// candidate never competed for budget at all.
	ReasonNotApplicable SelectionReason = "NOT_APPLICABLE"
	// ReasonBudgetExceeded: applicable, no conflict, but did not fit in
	// the remaining budget once higher-priority/more-relevant candidates
	// had already been included.
	ReasonBudgetExceeded SelectionReason = "BUDGET_EXCEEDED"
)

// ResolvedCandidate is one candidate's own outcome from a Resolve call.
type ResolvedCandidate struct {
	Identity   definition.ResourceIdentity
	Priority   definition.PriorityClass
	Provenance Provenance
	Reason     SelectionReason
	Cost       uint64
}

// Resolution is Resolve's own deterministic result: a pure in-memory
// value, never itself persisted by this package (see this package's own
// doc comment for why).
type Resolution struct {
	// Selected is every candidate Resolve chose to include, in the exact
	// order it should be rendered: HARD_CONSTRAINT first, then
	// REQUIRED_PROCEDURE, GUIDANCE, REFERENCE; within one Priority, by the
	// deterministic tie-breaker Resolve's own doc comment describes.
	Selected []ResolvedCandidate
	// Excluded is every OTHER candidate Resolve considered — both
	// NOT_APPLICABLE and BUDGET_EXCEEDED — so "why wasn't resource X
	// loaded" is always answerable from the Resolution alone, matching
	// this task's own "exact provenance/reason" line and HE-04's UI-facing
	// goal ("giải thích vì sao resource này được nạp").
	Excluded []ResolvedCandidate
	// SpentBytes, ReservedBytes and MaxBytes echo the Budget this
	// Resolution was computed against, plus what was actually spent —
	// CostModel names the CostEstimator that produced every Cost value
	// here, so a Resolution is never ambiguous about how its own numbers
	// were computed.
	SpentBytes    uint64
	ReservedBytes uint64
	MaxBytes      uint64
	CostModel     string
}

// ConflictingCandidate is one of the candidates involved in a
// ConstraintConflict.
type ConflictingCandidate struct {
	Identity   definition.ResourceIdentity
	Provenance Provenance
}

// ConstraintConflict is one detected disagreement: two or more applicable
// candidates share a ResourceKey, at least one is HARD_CONSTRAINT, and
// they do not all share the same ContentHash — a real, unresolved
// disagreement about what a single hard rule actually says, never
// silently resolved by picking whichever candidate a map iteration
// happened to visit first.
type ConstraintConflict struct {
	ResourceKey string
	Candidates  []ConflictingCandidate
}

// HardConstraintConflictError is returned by Resolve when one or more
// ConstraintConflicts exist among applicable candidates (HE-04-M05:
// "resolver MUST phát hiện rule/config mâu thuẫn; giá trị đơn xung đột
// không được tự 'last wins'"). Conflicts always lists every conflict this
// call found — never just the first one a map iteration happens to visit
// — sorted by ResourceKey so two calls over the identical candidate set
// always produce an identical error. Resolve never itself transitions any
// runtime state (it has none to transition — see this package's own doc
// comment) and never returns a partial Resolution alongside this error: a
// caller with real NodeRun/Attempt state (V5-08+) is the one that turns
// this into a BLOCKED admission outcome; the exact TerminationReason for
// that mapping does not exist yet in the state-reason matrix
// (docs/architecture/04-go-core-spec.md §4.5) and is left as an explicit
// gap for whichever task wires that admission path.
type HardConstraintConflictError struct {
	Conflicts []ConstraintConflict
}

func (e *HardConstraintConflictError) Error() string {
	return fmt.Sprintf("contextassembler: %d hard constraint conflict(s) detected", len(e.Conflicts))
}

// RequiredContextExceedsBudgetError is returned by Resolve when the
// combined cost of every applicable HARD_CONSTRAINT candidate alone
// already exceeds budget.available() — HARD_CONSTRAINT candidates are
// never droppable, so Resolve refuses to drop, truncate or silently
// exceed the budget rather than ever produce a Resolution missing a rule
// an attempt MUST follow.
type RequiredContextExceedsBudgetError struct {
	RequiredBytes  uint64
	AvailableBytes uint64
}

func (e *RequiredContextExceedsBudgetError) Error() string {
	return fmt.Sprintf(
		"contextassembler: required HARD_CONSTRAINT context (%d bytes) exceeds available budget (%d bytes)",
		e.RequiredBytes, e.AvailableBytes,
	)
}

// Resolve is this package's own pure resolution algorithm.
//
// Steps, in order:
//  1. Deduplicate candidates by their exact Identity tuple (the same
//     resource reached twice, e.g. via two different Pack paths, counts
//     once).
//  2. Partition into applicable (Selector.MatchesContext(ctx)) and
//     not-applicable; not-applicable candidates never compete for budget
//     and are reported with ReasonNotApplicable.
//  3. Detect HARD_CONSTRAINT conflicts among applicable candidates
//     (grouped by ResourceKey; a group with more than one distinct
//     ContentHash where at least one member is HARD_CONSTRAINT is a
//     conflict). If any exist, Resolve returns immediately with
//     *HardConstraintConflictError and a zero Resolution — conflict
//     detection happens BEFORE budget accounting, since a partial
//     Resolution alongside an unresolved conflict would invite exactly
//     the silent "well it half-worked" outcome HE-04-M05 exists to
//     prevent.
//  4. Every applicable HARD_CONSTRAINT candidate is non-droppable: if
//     their combined cost exceeds budget.available(), Resolve returns
//     *RequiredContextExceedsBudgetError and a zero Resolution rather than
//     drop, truncate, or silently exceed the ceiling. Otherwise all of
//     them are selected.
//  5. Every other applicable candidate is ordered by Priority
//     (REQUIRED_PROCEDURE, then GUIDANCE, then REFERENCE) and, within one
//     Priority, by ResourceKey then OwnerVersionID then ContentHash — an
//     arbitrary but fully deterministic tie-break, never insertion order
//     (which callers have no contract to keep stable) — then greedily
//     walked: a candidate that fits in the remaining budget is selected
//     and its cost deducted; one that doesn't is excluded with
//     ReasonBudgetExceeded, and the walk continues (a later, smaller
//     candidate may still fit even after an earlier, larger one did not).
func Resolve(candidates []Candidate, ctx ResolutionContext, budget Budget, estimator CostEstimator) (Resolution, error) {
	deduped := dedupeByIdentity(candidates)

	var applicable, notApplicable []Candidate
	for _, c := range deduped {
		if c.Selector.MatchesContext(ctx) {
			applicable = append(applicable, c)
		} else {
			notApplicable = append(notApplicable, c)
		}
	}

	if conflicts := detectConflicts(applicable); len(conflicts) > 0 {
		return Resolution{}, &HardConstraintConflictError{Conflicts: conflicts}
	}

	var hardConstraints, rest []Candidate
	for _, c := range applicable {
		if c.Priority == definition.PriorityHardConstraint {
			hardConstraints = append(hardConstraints, c)
		} else {
			rest = append(rest, c)
		}
	}
	sortDeterministic(hardConstraints)
	sortDeterministic(rest)
	sort.SliceStable(rest, func(i, j int) bool {
		return priorityRank(rest[i].Priority) < priorityRank(rest[j].Priority)
	})

	available := budget.available()
	var requiredCost uint64
	for _, c := range hardConstraints {
		requiredCost += estimator.Cost(c)
	}
	if requiredCost > available {
		return Resolution{}, &RequiredContextExceedsBudgetError{RequiredBytes: requiredCost, AvailableBytes: available}
	}

	result := Resolution{
		ReservedBytes: budget.ReservedBytes,
		MaxBytes:      budget.MaxBytes,
		CostModel:     estimator.Name(),
	}
	for _, c := range hardConstraints {
		cost := estimator.Cost(c)
		result.Selected = append(result.Selected, resolvedCandidate(c, ReasonSelected, cost))
		result.SpentBytes += cost
	}

	remaining := available - requiredCost
	for _, c := range rest {
		cost := estimator.Cost(c)
		if cost <= remaining {
			result.Selected = append(result.Selected, resolvedCandidate(c, ReasonSelected, cost))
			result.SpentBytes += cost
			remaining -= cost
		} else {
			result.Excluded = append(result.Excluded, resolvedCandidate(c, ReasonBudgetExceeded, cost))
		}
	}
	for _, c := range notApplicable {
		result.Excluded = append(result.Excluded, resolvedCandidate(c, ReasonNotApplicable, 0))
	}

	return result, nil
}

func resolvedCandidate(c Candidate, reason SelectionReason, cost uint64) ResolvedCandidate {
	return ResolvedCandidate{
		Identity: c.Identity, Priority: c.Priority, Provenance: c.Provenance,
		Reason: reason, Cost: cost,
	}
}

// priorityRank orders REQUIRED_PROCEDURE before GUIDANCE before REFERENCE
// (HARD_CONSTRAINT is never passed to this function — it is always
// selected ahead of and separately from every other Priority).
func priorityRank(p definition.PriorityClass) int {
	switch p {
	case definition.PriorityRequiredProcedure:
		return 0
	case definition.PriorityGuidance:
		return 1
	case definition.PriorityReference:
		return 2
	default:
		return 3
	}
}

// sortDeterministic orders candidates by ResourceKey, then OwnerVersionID,
// then ContentHash — the tie-breaker every ordering step in this package
// uses beneath its own primary sort key (Priority, where relevant).
func sortDeterministic(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].Identity, candidates[j].Identity
		if a.ResourceKey != b.ResourceKey {
			return a.ResourceKey < b.ResourceKey
		}
		if a.OwnerVersionID != b.OwnerVersionID {
			return a.OwnerVersionID < b.OwnerVersionID
		}
		return a.ContentHash < b.ContentHash
	})
}

func dedupeByIdentity(candidates []Candidate) []Candidate {
	seen := make(map[definition.ResourceIdentity]bool, len(candidates))
	result := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if seen[c.Identity] {
			continue
		}
		seen[c.Identity] = true
		result = append(result, c)
	}
	return result
}

// detectConflicts groups applicable candidates by ResourceKey and reports
// every group with more than one distinct ContentHash where at least one
// member is HARD_CONSTRAINT — the same rule engineeringpack.CheckResourceConflicts
// already applies to one pack's resolved manifest, extended here to the
// full candidate set (multiple packs/components/messages together).
// Conflicts (and each Conflict's own Candidates) are always sorted
// deterministically, independent of map iteration order.
func detectConflicts(applicable []Candidate) []ConstraintConflict {
	byKey := make(map[string][]Candidate)
	for _, c := range applicable {
		byKey[c.Identity.ResourceKey] = append(byKey[c.Identity.ResourceKey], c)
	}

	var conflicts []ConstraintConflict
	for key, group := range byKey {
		hashes := make(map[string]bool, len(group))
		hasHardConstraint := false
		for _, c := range group {
			hashes[c.Identity.ContentHash] = true
			if c.Priority == definition.PriorityHardConstraint {
				hasHardConstraint = true
			}
		}
		if len(hashes) <= 1 || !hasHardConstraint {
			continue
		}
		conflicting := make([]ConflictingCandidate, 0, len(group))
		for _, c := range group {
			conflicting = append(conflicting, ConflictingCandidate{Identity: c.Identity, Provenance: c.Provenance})
		}
		sort.Slice(conflicting, func(i, j int) bool {
			a, b := conflicting[i].Identity, conflicting[j].Identity
			if a.OwnerVersionID != b.OwnerVersionID {
				return a.OwnerVersionID < b.OwnerVersionID
			}
			return a.ContentHash < b.ContentHash
		})
		conflicts = append(conflicts, ConstraintConflict{ResourceKey: key, Candidates: conflicting})
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].ResourceKey < conflicts[j].ResourceKey })
	return conflicts
}
