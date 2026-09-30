// Package alphagate is V8-11's Alpha release acceptance assessment
// (docs/design/10-v8-alpha-hardening.md V8-11): it aggregates the evidence
// the repository and CI already produce onto the coverage map V1-00A…C built,
// and computes one machine-readable `gatePass`.
//
// It is a pure reader, in the same family as internal/v6gate and
// internal/v8gate: it never runs a product test, never starts a process and
// never mutates the repository. Its inputs are (1) the coverage inventory
// (internal/docscoverage — every criterion's ADR-024 phase label and owner
// tasks, exactly as the existing checker resolves them), (2) what the
// repository's own test sources say (internal/alphagate/scan.go), (3) CI's
// per-suite results, and (4) the `go test -json` output of a small, targeted
// run of the final-gate tests.
//
// Three rules from the design shape every line here:
//
//   - V8 never reclassifies a criterion (ADR-024). The label is read, not
//     decided: ALPHA_MUST and CROSS_PHASE_GUARD rows count toward the gate,
//     BETA_* rows are reported as outside Alpha scope (never as failures),
//     NOT_APPLICABLE rows carry their authority reason. There is no status
//     called "deferred".
//   - Missing evidence is never a pass. A suite that did not run, a test that
//     was skipped, an artifact that is absent: CHƯA ĐỦ EVIDENCE.
//   - A known failure is reported as a failure, and it dominates missing
//     evidence on the same row: something that is known to be broken must
//     not be hidden behind "we could not tell".
//
// What the matrix can and cannot claim is recorded per row as
// EvidenceLevel. TEST means at least one test in the repository names the
// criterion; SUITE means the criterion is covered only at the granularity of
// the suites its owning versions' gates require (ADR-024 and the coverage
// map fix ownership, not test-per-criterion traceability, and only a minority
// of criteria are cited by a test). The summary counts both so the verdict
// can disclose the limitation instead of implying a finer proof.
package alphagate

import (
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/docscoverage"
)

// Status is one row's (or gate's) outcome.
type Status string

const (
	StatusPass            Status = "PASS"
	StatusFail            Status = "FAIL"
	StatusInsufficient    Status = "CHƯA ĐỦ EVIDENCE"
	StatusOutOfAlphaScope Status = "OUT_OF_ALPHA_SCOPE" // BETA_ADAPTER_GATE / BETA_PARITY_GATE
	StatusNotApplicable   Status = "NOT_APPLICABLE"     // carries its authority reason
	StatusNoOwner         Status = "NO_OWNER_TASK"      // an ADR no task cites (informational)
)

// Evidence levels, see the package doc.
const (
	LevelTest  = "TEST"
	LevelSuite = "SUITE"
)

// Verdict hints feed V8-12; V8-11 itself only computes gatePass.
const (
	HintPass         = "PASS"
	HintRework       = "REWORK"
	HintInsufficient = "CHƯA ĐỦ EVIDENCE"
)

// Input is everything Assess reads.
type Input struct {
	Commit    string
	Inventory docscoverage.Inventory
	Scan      Scan
	// Suites maps a suite id (AllSuites) to GitHub's `needs.<id>.result`.
	// An absent key is treated exactly like "skipped": no evidence.
	Suites map[string]string
	// TestRun is the parsed `go test -json` output of the final-gate run.
	TestRun TestRun
	// GitDiffCheck is the result of the `git diff --check` step: "success",
	// "failure", or anything else for "did not run".
	GitDiffCheck string
}

// CriterionRow is one phase-labeled criterion's assessment.
type CriterionRow struct {
	ID             string     `json:"id"`
	Family         string     `json:"family"`
	Phase          string     `json:"phase"`
	Status         Status     `json:"status"`
	EvidenceLevel  string     `json:"evidenceLevel,omitempty"`
	Reason         string     `json:"reason,omitempty"`
	OwnerTasks     []string   `json:"ownerTasks,omitempty"`
	OwnerSPKs      []string   `json:"ownerSpikes,omitempty"`
	Versions       []string   `json:"versions,omitempty"`
	RequiredSuites []string   `json:"requiredSuites,omitempty"`
	Tests          []Citation `json:"tests,omitempty"`
	FailingSuites  []string   `json:"failingSuites,omitempty"`
	MissingSuites  []string   `json:"missingSuites,omitempty"`
	Note           string     `json:"note,omitempty"`
}

// DecisionRow is one ADR's assessment. ADRs are decision sources, not
// criteria, so they are reported but never gate the release.
type DecisionRow struct {
	ID            string   `json:"id"`
	Status        Status   `json:"status"`
	OwnerTasks    []string `json:"ownerTasks,omitempty"`
	Versions      []string `json:"versions,omitempty"`
	FailingSuites []string `json:"failingSuites,omitempty"`
	MissingSuites []string `json:"missingSuites,omitempty"`
}

// JourneyRow is one system acceptance journey's assessment.
type JourneyRow struct {
	ID            string   `json:"id"`
	Summary       string   `json:"summary"`
	Status        Status   `json:"status"`
	Tests         []string `json:"tests,omitempty"`
	Suites        []string `json:"suites,omitempty"`
	FailingSuites []string `json:"failingSuites,omitempty"`
	MissingSuites []string `json:"missingSuites,omitempty"`
	MissingTests  []string `json:"missingTests,omitempty"`
}

// VersionGateRow is one version's gate (V0…V8): its required suites.
type VersionGateRow struct {
	Version       string   `json:"version"`
	Status        Status   `json:"status"`
	Suites        []string `json:"suites"`
	FailingSuites []string `json:"failingSuites,omitempty"`
	MissingSuites []string `json:"missingSuites,omitempty"`
}

// FinalGateRow is one mandatory last gate's outcome.
type FinalGateRow struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
	Status  Status `json:"status"`
	Detail  string `json:"detail,omitempty"`
}

// SuiteRow records one CI suite as read.
type SuiteRow struct {
	ID     string `json:"id"`
	Result string `json:"result"`
	Status Status `json:"status"`
}

// Blocker is one gate-relevant item that is not PASS.
type Blocker struct {
	Kind   string `json:"kind"` // CRITERION, JOURNEY, FINAL_GATE, VERSION_GATE
	ID     string `json:"id"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Counts tallies rows by status.
type Counts struct {
	Total        int `json:"total"`
	Pass         int `json:"pass"`
	Fail         int `json:"fail"`
	Insufficient int `json:"insufficient"`
}

// Summary is the matrix at a glance.
type Summary struct {
	// AlphaGated counts the ALPHA_MUST and CROSS_PHASE_GUARD criteria — the
	// ones that decide gatePass.
	AlphaGated Counts `json:"alphaGatedCriteria"`
	// AlphaMust is the ALPHA_MUST subset of AlphaGated.
	AlphaMust Counts `json:"alphaMustCriteria"`
	// TestLevelEvidence / SuiteLevelOnly split AlphaGated by EvidenceLevel.
	TestLevelEvidence int    `json:"criteriaWithTestEvidence"`
	SuiteLevelOnly    int    `json:"criteriaWithSuiteEvidenceOnly"`
	OutOfAlphaScope   int    `json:"criteriaOutOfAlphaScope"`
	NotApplicable     int    `json:"criteriaNotApplicable"`
	Journeys          Counts `json:"journeys"`
	FinalGates        Counts `json:"finalGates"`
	VersionGates      Counts `json:"versionGates"`
	DecisionsNoOwner  int    `json:"decisionsWithoutOwnerTask"`
}

// Assessment is the whole machine-readable result.
type Assessment struct {
	SchemaVersion int    `json:"schemaVersion"`
	Commit        string `json:"commit"`
	// GatePass is true only when every gate-relevant row is PASS.
	GatePass    bool      `json:"gatePass"`
	VerdictHint string    `json:"verdictHint"`
	Summary     Summary   `json:"summary"`
	Blockers    []Blocker `json:"blockers"`

	Suites       []SuiteRow       `json:"suites"`
	VersionGates []VersionGateRow `json:"versionGates"`
	FinalGates   []FinalGateRow   `json:"finalGates"`
	Journeys     []JourneyRow     `json:"journeys"`
	Criteria     []CriterionRow   `json:"criteria"`
	Decisions    []DecisionRow    `json:"decisions"`
}

// suiteStatus maps a GitHub job result to a Status.
func suiteStatus(result string) Status {
	switch result {
	case "success":
		return StatusPass
	case "failure":
		return StatusFail
	default: // "skipped", "cancelled", "" (absent) — no evidence
		return StatusInsufficient
	}
}

// combine evaluates a list of suites: any failure is FAIL, else any missing
// evidence is CHƯA ĐỦ EVIDENCE, else PASS.
func combine(in Input, suites []string) (status Status, failing, missing []string) {
	for _, id := range suites {
		switch suiteStatus(in.Suites[id]) {
		case StatusFail:
			failing = append(failing, id)
		case StatusInsufficient:
			missing = append(missing, id)
		}
	}
	sort.Strings(failing)
	sort.Strings(missing)
	switch {
	case len(failing) > 0:
		return StatusFail, failing, missing
	case len(missing) > 0:
		return StatusInsufficient, failing, missing
	}
	return StatusPass, nil, nil
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func versionsOf(tasks, spikes []string) []string {
	var versions []string
	for _, t := range tasks {
		versions = append(versions, VersionOfTask(t))
	}
	if len(spikes) > 0 {
		versions = append(versions, "V0")
	}
	return sortedUnique(versions)
}

func suitesForVersions(versions []string) []string {
	var suites []string
	for _, v := range versions {
		suites = appendUnique(suites, SuitesForVersion(v)...)
	}
	sort.Strings(suites)
	return suites
}

// Assess computes the assessment matrix and gatePass.
func Assess(in Input) Assessment {
	a := Assessment{SchemaVersion: 1, Commit: in.Commit}

	for _, id := range AllSuites {
		a.Suites = append(a.Suites, SuiteRow{ID: id, Result: in.Suites[id], Status: suiteStatus(in.Suites[id])})
	}

	for _, v := range Versions {
		suites := SuitesForVersion(v)
		sort.Strings(suites)
		status, failing, missing := combine(in, suites)
		a.VersionGates = append(a.VersionGates, VersionGateRow{Version: v, Status: status, Suites: suites, FailingSuites: failing, MissingSuites: missing})
	}

	for _, c := range in.Inventory.Criteria {
		a.Criteria = append(a.Criteria, assessCriterion(in, c))
	}
	sort.Slice(a.Criteria, func(i, j int) bool { return a.Criteria[i].ID < a.Criteria[j].ID })

	for _, d := range in.Inventory.Decisions {
		a.Decisions = append(a.Decisions, assessDecision(in, d))
	}
	sort.Slice(a.Decisions, func(i, j int) bool { return a.Decisions[i].ID < a.Decisions[j].ID })

	for _, j := range Journeys {
		a.Journeys = append(a.Journeys, assessJourney(in, j))
	}
	for _, g := range FinalGates {
		a.FinalGates = append(a.FinalGates, assessFinalGate(in, g))
	}

	a.summarize()
	return a
}

func assessCriterion(in Input, c docscoverage.Criterion) CriterionRow {
	row := CriterionRow{
		ID: c.ID, Family: string(c.Family), Phase: string(c.Label), Reason: c.Reason,
		OwnerTasks: c.OwnerTasks, OwnerSPKs: c.OwnerSPKs,
		Tests: in.Scan.Citations[c.ID],
	}
	switch c.Label {
	case docscoverage.BetaAdapterGate, docscoverage.BetaParityGate:
		row.Status = StatusOutOfAlphaScope
		row.Note = "Beta-labeled by ADR-024; reported as outside Alpha scope, not as a failure"
		return row
	case docscoverage.NotApplicable:
		row.Status = StatusNotApplicable
		return row
	}

	row.Versions = versionsOf(c.OwnerTasks, c.OwnerSPKs)
	suites := suitesForVersions(row.Versions)
	if c.Label == docscoverage.CrossPhaseGuard {
		// ADR-024: Alpha checks only the boundary/port half, through the
		// architecture tests, which run inside `contract`.
		suites = appendUnique(suites, SuiteContract)
		sort.Strings(suites)
		if !hasArchitectureTests(in.Scan) {
			row.Status = StatusInsufficient
			row.RequiredSuites = suites
			row.Note = "no architecture test found under internal/archtest — the Alpha half of this criterion has no evidence"
			return row
		}
		row.Note = "Alpha half only (port/import boundary, internal/archtest); full adapter parity is Beta"
	}
	for _, citation := range row.Tests {
		suites = appendUnique(suites, SuiteForPackage(citation.Pkg))
	}
	sort.Strings(suites)
	row.RequiredSuites = suites

	if len(row.Versions) == 0 && c.Label == docscoverage.AlphaMust {
		row.Status = StatusInsufficient
		row.Note = "no owner task or spike — the coverage checker should have rejected this"
		return row
	}
	row.EvidenceLevel = LevelSuite
	if len(row.Tests) > 0 {
		row.EvidenceLevel = LevelTest
	}
	row.Status, row.FailingSuites, row.MissingSuites = combine(in, suites)
	return row
}

func hasArchitectureTests(scan Scan) bool {
	for key := range scan.Tests {
		if strings.HasPrefix(key, "internal/archtest|") {
			return true
		}
	}
	return false
}

func assessDecision(in Input, d docscoverage.Decision) DecisionRow {
	row := DecisionRow{ID: d.ID, OwnerTasks: d.OwnerTasks}
	if len(d.OwnerTasks) == 0 {
		row.Status = StatusNoOwner
		return row
	}
	row.Versions = versionsOf(d.OwnerTasks, nil)
	row.Status, row.FailingSuites, row.MissingSuites = combine(in, suitesForVersions(row.Versions))
	return row
}

func assessJourney(in Input, j Journey) JourneyRow {
	row := JourneyRow{ID: j.ID, Summary: j.Summary}
	suites := append([]string(nil), j.Suites...)
	for _, ref := range j.Tests {
		row.Tests = append(row.Tests, ref.Key())
		if !in.Scan.HasTest(ref) {
			row.MissingTests = append(row.MissingTests, ref.Key())
			continue
		}
		suites = appendUnique(suites, SuiteForPackage(ref.Pkg))
	}
	sort.Strings(suites)
	row.Suites = suites
	row.Status, row.FailingSuites, row.MissingSuites = combine(in, suites)
	if len(row.MissingTests) > 0 && row.Status == StatusPass {
		row.Status = StatusInsufficient
	}
	return row
}

func assessFinalGate(in Input, g FinalGate) FinalGateRow {
	row := FinalGateRow{ID: g.ID, Summary: g.Summary}
	switch g.Computed {
	case ComputedSourceRefDebt:
		if debt := in.Inventory.Debt(); debt == 0 {
			row.Status = StatusPass
		} else {
			row.Status = StatusFail
			row.Detail = debtDetail(in.Inventory)
		}
		return row
	case ComputedGitDiffCheck:
		switch in.GitDiffCheck {
		case "success":
			row.Status = StatusPass
		case "failure":
			row.Status = StatusFail
			row.Detail = "`git diff --check` reported problems"
		default:
			row.Status = StatusInsufficient
			row.Detail = "`git diff --check` did not run"
		}
		return row
	}

	var failing, missing []string
	for _, ref := range g.Tests {
		switch in.TestRun.Outcome(ref) {
		case OutcomePass:
		case OutcomeFail:
			failing = append(failing, ref.Key()+failureOutput(in.TestRun, ref))
		default:
			missing = append(missing, ref.Key())
		}
	}
	for _, id := range g.Suites {
		switch suiteStatus(in.Suites[id]) {
		case StatusFail:
			failing = append(failing, "suite:"+id)
		case StatusInsufficient:
			missing = append(missing, "suite:"+id)
		}
	}
	switch {
	case len(failing) > 0:
		row.Status = StatusFail
		row.Detail = "failing: " + strings.Join(failing, ", ")
		if len(missing) > 0 {
			row.Detail += "; no evidence: " + strings.Join(missing, ", ")
		}
	case len(missing) > 0:
		row.Status = StatusInsufficient
		row.Detail = "no evidence (absent or skipped): " + strings.Join(missing, ", ")
	default:
		row.Status = StatusPass
	}
	return row
}

// failureOutput renders the tail of a failed test's own output, collapsed to
// one line, so the blocker says what failed and not only which test.
func failureOutput(run TestRun, ref TestRef) string {
	out := strings.TrimSpace(run.Output[ref.Key()])
	if out == "" {
		return ""
	}
	return " (" + strings.Join(strings.Fields(out), " ") + ")"
}

func debtDetail(inv docscoverage.Inventory) string {
	const show = 3
	var parts []string
	for i, v := range inv.Violations {
		if i == show {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, "["+v.Rule+"] "+v.Detail)
	}
	return strings.Join(parts, "; ")
}

func (a *Assessment) summarize() {
	tally := func(c *Counts, s Status) {
		c.Total++
		switch s {
		case StatusPass:
			c.Pass++
		case StatusFail:
			c.Fail++
		default:
			c.Insufficient++
		}
	}
	addBlocker := func(kind, id string, status Status, detail string) {
		if status != StatusPass {
			a.Blockers = append(a.Blockers, Blocker{Kind: kind, ID: id, Status: status, Detail: detail})
		}
	}

	for _, c := range a.Criteria {
		switch Status(c.Status) {
		case StatusOutOfAlphaScope:
			a.Summary.OutOfAlphaScope++
			continue
		case StatusNotApplicable:
			a.Summary.NotApplicable++
			continue
		}
		tally(&a.Summary.AlphaGated, c.Status)
		if c.Phase == string(docscoverage.AlphaMust) {
			tally(&a.Summary.AlphaMust, c.Status)
		}
		switch c.EvidenceLevel {
		case LevelTest:
			a.Summary.TestLevelEvidence++
		case LevelSuite:
			a.Summary.SuiteLevelOnly++
		}
		addBlocker("CRITERION", c.ID, c.Status, criterionDetail(c))
	}
	for _, d := range a.Decisions {
		if d.Status == StatusNoOwner {
			a.Summary.DecisionsNoOwner++
		}
	}
	for _, j := range a.Journeys {
		tally(&a.Summary.Journeys, j.Status)
		addBlocker("JOURNEY", j.ID, j.Status, journeyDetail(j))
	}
	for _, g := range a.FinalGates {
		tally(&a.Summary.FinalGates, g.Status)
		addBlocker("FINAL_GATE", g.ID, g.Status, g.Detail)
	}
	for _, v := range a.VersionGates {
		tally(&a.Summary.VersionGates, v.Status)
		addBlocker("VERSION_GATE", v.Version, v.Status, suitesDetail(v.FailingSuites, v.MissingSuites))
	}

	a.GatePass = len(a.Blockers) == 0
	switch {
	case a.GatePass:
		a.VerdictHint = HintPass
	case hasStatus(a.Blockers, StatusFail):
		a.VerdictHint = HintRework
	default:
		a.VerdictHint = HintInsufficient
	}
	if a.Blockers == nil {
		a.Blockers = []Blocker{}
	}
}

func hasStatus(blockers []Blocker, status Status) bool {
	for _, b := range blockers {
		if b.Status == status {
			return true
		}
	}
	return false
}

func criterionDetail(c CriterionRow) string {
	if c.Note != "" && len(c.FailingSuites) == 0 && len(c.MissingSuites) == 0 {
		return c.Note
	}
	return suitesDetail(c.FailingSuites, c.MissingSuites)
}

func journeyDetail(j JourneyRow) string {
	detail := suitesDetail(j.FailingSuites, j.MissingSuites)
	if len(j.MissingTests) > 0 {
		if detail != "" {
			detail += "; "
		}
		detail += "test not found in repository: " + strings.Join(j.MissingTests, ", ")
	}
	return detail
}

func suitesDetail(failing, missing []string) string {
	var parts []string
	if len(failing) > 0 {
		parts = append(parts, "failing suites: "+strings.Join(failing, ", "))
	}
	if len(missing) > 0 {
		parts = append(parts, "no evidence from suites: "+strings.Join(missing, ", "))
	}
	return strings.Join(parts, "; ")
}
