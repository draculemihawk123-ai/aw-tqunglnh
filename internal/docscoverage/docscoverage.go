// Package docscoverage implements the V1-00C coverage checker
// (docs/design/03-v1-alpha-foundation.md V1-00C): it proves, by parsing the
// actual doc/design files rather than trusting a written claim, that every
// phase-labeled criterion (AK-ARCH, HE-M, GC-INV/ACC/DS) has exactly one
// phase label, every ALPHA_MUST criterion has an owner (a V1..V8 Task ID or
// a V0 SPK), every V1..V8 task declares a Nguồn field whose SourceRef tokens
// all resolve to a real item, and every SPK-01..14 maps to at least one
// criterion. This package only reads repository markdown; it never mutates
// product code or docs (00-roadmap.md §3's "Phạm vi" for V1-00C).
package docscoverage

// Label is one of the five ADR-024 phase classifications, or the empty
// string when a criterion has none (a defect Rule violates).
type Label string

const (
	AlphaMust        Label = "ALPHA_MUST"
	BetaAdapterGate  Label = "BETA_ADAPTER_GATE"
	BetaParityGate   Label = "BETA_PARITY_GATE"
	CrossPhaseGuard  Label = "CROSS_PHASE_GUARD"
	NotApplicable    Label = "NOT_APPLICABLE"
)

func validLabel(l Label) bool {
	switch l {
	case AlphaMust, BetaAdapterGate, BetaParityGate, CrossPhaseGuard, NotApplicable:
		return true
	default:
		return false
	}
}

// Family groups criterion IDs that share one phase-classification subsection
// (default label + closed exceptions table) in the source docs.
type Family string

const (
	FamilyAKArch Family = "AK-ARCH"
	FamilyHE     Family = "HE"
	FamilyGC     Family = "GC"
)

// criterionSet is everything known about one criterion family: every ID the
// docs actually define (criteria requiring a label, e.g. HE-*-M* but not
// HE-*-S*), every ID that is a merely-citable-but-unlabeled sibling (HE-*-S*
// specifically — valid SourceRef targets, never required to carry a phase
// label), whether the family's default-ALPHA_MUST statement was found, and
// the exceptions parsed from its classification table.
type criterionSet struct {
	family         Family
	labeled        map[string]bool          // IDs that must carry a phase label
	citableOnly    map[string]bool          // IDs valid as SourceRef targets but never phase-labeled (HE-*-S*)
	defaultFound   bool                     // the "Mặc định ... ALPHA_MUST" statement was present
	exceptionRows  []exceptionRow           // raw rows parsed from the classification table, in file order
}

type exceptionRow struct {
	id     string
	label  Label
	reason string // the free-text "Phần Alpha vẫn phải làm" / reason column
	line   int
}

// Task is one V1..V8 Task ID and the raw SourceRef tokens its Nguồn field
// lists. HasNguon distinguishes "field present but empty" (still a defect)
// from "field entirely absent".
type Task struct {
	ID         string
	File       string
	Line       int
	HasNguon   bool
	RawSources []string
}

// spkEntry is one SPK-01..14 row from the V0 coverage map
// (docs/design/02-v0-spike-verdict.md).
type spkEntry struct {
	id       string
	criteria []string
	line     int
}

// Violation is one concrete, evidence-backed defect the checker found.
// Rule identifies which of V1-00C's seven Verify clauses (a..g) it
// violates.
type Violation struct {
	Rule   string // "a".."g"
	Detail string
}

// Report is the checker's full result: Debt is len(Violations), computed —
// never hard-coded — so the "Hoàn thành khi: debt bằng 0" gate in V1-00C
// always reflects the docs as they currently stand.
type Report struct {
	Violations []Violation
}

func (r Report) Debt() int { return len(r.Violations) }

func (r *Report) add(rule, detail string) {
	r.Violations = append(r.Violations, Violation{Rule: rule, Detail: detail})
}
