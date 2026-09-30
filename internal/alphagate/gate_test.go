package alphagate

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/docscoverage"
)

// greenInput builds a minimal, fully passing Input: a handful of criteria
// (one per relevant label), every journey test present in the scan, every
// final-gate test passed, every suite successful.
func greenInput() Input {
	in := Input{
		Commit: "abc123",
		Inventory: docscoverage.Inventory{
			Criteria: []docscoverage.Criterion{
				{ID: "AK-ARCH-001", Family: docscoverage.FamilyAKArch, Label: docscoverage.AlphaMust, OwnerTasks: []string{"V1-04"}},
				{ID: "GC-INV-01", Family: docscoverage.FamilyGC, Label: docscoverage.AlphaMust, OwnerTasks: []string{"V6-07A"}},
				{ID: "HE-01-M01", Family: docscoverage.FamilyHE, Label: docscoverage.AlphaMust, OwnerSPKs: []string{"SPK-02"}},
				{ID: "AK-ARCH-019", Family: docscoverage.FamilyAKArch, Label: docscoverage.BetaAdapterGate},
				{ID: "AK-ARCH-026", Family: docscoverage.FamilyAKArch, Label: docscoverage.BetaParityGate},
				{ID: "AK-ARCH-028", Family: docscoverage.FamilyAKArch, Label: docscoverage.CrossPhaseGuard},
				{ID: "HE-06-M08", Family: docscoverage.FamilyHE, Label: docscoverage.NotApplicable, Reason: "beta worker only"},
			},
			Decisions: []docscoverage.Decision{
				{ID: "ADR-005", OwnerTasks: []string{"V1-04"}},
				{ID: "ADR-001"},
			},
		},
		Scan: Scan{
			Tests: map[string]bool{"internal/archtest|TestBoundary": true},
			Citations: map[string][]Citation{
				"GC-INV-01": {{Pkg: "internal/integration/v6accept", File: "internal/integration/v6accept/x_test.go", Test: "TestX"}},
			},
		},
		Suites:       map[string]string{},
		TestRun:      TestRun{Outcomes: map[string]Outcome{}, FailedPackages: map[string]bool{}},
		GitDiffCheck: "success",
	}
	for _, id := range AllSuites {
		in.Suites[id] = "success"
	}
	for _, j := range Journeys {
		for _, ref := range j.Tests {
			in.Scan.Tests[ref.Pkg+"|"+strings.SplitN(ref.Name, "/", 2)[0]] = true
		}
	}
	for _, g := range FinalGates {
		for _, ref := range g.Tests {
			in.TestRun.Outcomes[ref.Key()] = OutcomePass
		}
	}
	return in
}

func rowByID(t *testing.T, a Assessment, id string) CriterionRow {
	t.Helper()
	for _, c := range a.Criteria {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no criterion row %s", id)
	return CriterionRow{}
}

func hasBlocker(a Assessment, kind, id string) bool {
	for _, b := range a.Blockers {
		if b.Kind == kind && b.ID == id {
			return true
		}
	}
	return false
}

func TestAssess_EverythingGreen_GatePasses(t *testing.T) {
	a := Assess(greenInput())
	if !a.GatePass || a.VerdictHint != HintPass || len(a.Blockers) != 0 {
		t.Fatalf("gatePass=%v hint=%s blockers=%+v, want a clean pass", a.GatePass, a.VerdictHint, a.Blockers)
	}
	if got := rowByID(t, a, "GC-INV-01"); got.EvidenceLevel != LevelTest || got.Status != StatusPass {
		t.Fatalf("GC-INV-01 = %+v, want PASS at TEST level (a test cites it)", got)
	}
	if got := rowByID(t, a, "AK-ARCH-001"); got.EvidenceLevel != LevelSuite || got.Status != StatusPass {
		t.Fatalf("AK-ARCH-001 = %+v, want PASS at SUITE level (no test cites it)", got)
	}
	if a.Summary.TestLevelEvidence != 1 || a.Summary.SuiteLevelOnly != 3 {
		t.Fatalf("evidence split = %d test / %d suite, want 1 / 3 (GC-INV-01 vs AK-ARCH-001, HE-01-M01, AK-ARCH-028)", a.Summary.TestLevelEvidence, a.Summary.SuiteLevelOnly)
	}
}

func TestAssess_BetaAndNotApplicableNeverBlock_AndAreNotReportedAsFailures(t *testing.T) {
	a := Assess(greenInput())
	for id, want := range map[string]Status{
		"AK-ARCH-019": StatusOutOfAlphaScope,
		"AK-ARCH-026": StatusOutOfAlphaScope,
		"HE-06-M08":   StatusNotApplicable,
	} {
		if got := rowByID(t, a, id); got.Status != want {
			t.Errorf("%s status = %s, want %s", id, got.Status, want)
		}
		if hasBlocker(a, "CRITERION", id) {
			t.Errorf("%s must never be a blocker", id)
		}
	}
	if a.Summary.OutOfAlphaScope != 2 || a.Summary.NotApplicable != 1 {
		t.Fatalf("out-of-scope/not-applicable = %d/%d, want 2/1", a.Summary.OutOfAlphaScope, a.Summary.NotApplicable)
	}
	if got := rowByID(t, a, "HE-06-M08"); got.Reason == "" {
		t.Fatal("a NOT_APPLICABLE row must carry its authority reason")
	}
}

func TestAssess_NoRowEverCarriesADeferredStatus(t *testing.T) {
	allowed := map[Status]bool{StatusPass: true, StatusFail: true, StatusInsufficient: true, StatusOutOfAlphaScope: true, StatusNotApplicable: true, StatusNoOwner: true}
	in := greenInput()
	in.Suites[SuiteV6Gate] = "failure"
	in.Suites[SuiteE2E] = "skipped"
	a := Assess(in)
	for _, c := range a.Criteria {
		if !allowed[c.Status] {
			t.Errorf("criterion %s has status %q", c.ID, c.Status)
		}
	}
}

func TestAssess_AFailingSuiteFailsEveryCriterionWhoseVersionNeedsIt(t *testing.T) {
	in := greenInput()
	in.Suites[SuiteV6Gate] = "failure"
	a := Assess(in)

	if got := rowByID(t, a, "GC-INV-01"); got.Status != StatusFail || len(got.FailingSuites) != 1 || got.FailingSuites[0] != SuiteV6Gate {
		t.Fatalf("GC-INV-01 (owned by a V6 task) = %+v, want FAIL naming %s", got, SuiteV6Gate)
	}
	if got := rowByID(t, a, "AK-ARCH-001"); got.Status != StatusPass {
		t.Fatalf("AK-ARCH-001 (owned by a V1 task, which never needs %s) = %s, want PASS", SuiteV6Gate, got.Status)
	}
	if a.GatePass || a.VerdictHint != HintRework {
		t.Fatalf("gatePass=%v hint=%s, want false/REWORK", a.GatePass, a.VerdictHint)
	}
}

func TestAssess_MissingEvidenceIsNeverAPass_AndAFailureDominatesIt(t *testing.T) {
	in := greenInput()
	delete(in.Suites, SuiteWeb) // absent
	in.Suites[SuiteE2E] = "skipped"
	a := Assess(in)
	if a.GatePass || a.VerdictHint != HintInsufficient {
		t.Fatalf("gatePass=%v hint=%s, want false / %s", a.GatePass, a.VerdictHint, HintInsufficient)
	}
	if !hasBlocker(a, "VERSION_GATE", "V7") {
		t.Fatal("V7's gate needs web and e2e; with neither it must be a blocker")
	}

	in.Suites[SuiteV6Gate] = "failure"
	if a := Assess(in); a.VerdictHint != HintRework {
		t.Fatalf("hint = %s, want REWORK — a known failure must not be hidden behind missing evidence elsewhere", a.VerdictHint)
	}
}

func TestAssess_CancelledSuiteIsInsufficientNotFailure(t *testing.T) {
	in := greenInput()
	in.Suites[SuiteRace] = "cancelled"
	a := Assess(in)
	if got := rowByID(t, a, "AK-ARCH-001"); got.Status != StatusInsufficient {
		t.Fatalf("status = %s, want %s (a cancelled job proves nothing)", got.Status, StatusInsufficient)
	}
}

func TestAssess_CriterionWithoutAnOwnerIsInsufficient(t *testing.T) {
	in := greenInput()
	in.Inventory.Criteria = append(in.Inventory.Criteria, docscoverage.Criterion{ID: "GC-INV-99", Family: docscoverage.FamilyGC, Label: docscoverage.AlphaMust})
	a := Assess(in)
	if got := rowByID(t, a, "GC-INV-99"); got.Status != StatusInsufficient {
		t.Fatalf("status = %s, want %s", got.Status, StatusInsufficient)
	}
	if a.GatePass {
		t.Fatal("an ALPHA_MUST with no owner must block the gate")
	}
}

func TestAssess_CrossPhaseGuardNeedsTheArchitectureTests(t *testing.T) {
	in := greenInput()
	delete(in.Scan.Tests, "internal/archtest|TestBoundary")
	a := Assess(in)
	if got := rowByID(t, a, "AK-ARCH-028"); got.Status != StatusInsufficient {
		t.Fatalf("status = %s, want %s without any internal/archtest test", got.Status, StatusInsufficient)
	}
}

func TestAssess_DecisionWithoutAnOwnerIsInformationalOnly(t *testing.T) {
	a := Assess(greenInput())
	var orphan DecisionRow
	for _, d := range a.Decisions {
		if d.ID == "ADR-001" {
			orphan = d
		}
	}
	if orphan.Status != StatusNoOwner {
		t.Fatalf("ADR-001 status = %s, want %s", orphan.Status, StatusNoOwner)
	}
	if !a.GatePass || a.Summary.DecisionsNoOwner != 1 {
		t.Fatalf("an ADR no task cites must be reported but never gate the release (gatePass=%v, count=%d)", a.GatePass, a.Summary.DecisionsNoOwner)
	}
}

func TestAssess_CitedTestsPullTheirOwnSuitesIntoTheRequirement(t *testing.T) {
	in := greenInput()
	in.Suites[SuiteV6Acceptance] = "failure"
	// AK-ARCH-001 is a V1 criterion (no v6-acceptance in its version gate), but
	// a v6accept test cites it, so that test's suite is now evidence for it.
	in.Scan.Citations["AK-ARCH-001"] = []Citation{{Pkg: "internal/integration/v6accept", File: "f_test.go", Test: "TestY"}}
	a := Assess(in)
	if got := rowByID(t, a, "AK-ARCH-001"); got.Status != StatusFail {
		t.Fatalf("status = %s, want FAIL via the cited test's suite", got.Status)
	}
}

func TestAssess_FinalGates(t *testing.T) {
	gate := func(a Assessment, id string) FinalGateRow {
		t.Helper()
		for _, g := range a.FinalGates {
			if g.ID == id {
				return g
			}
		}
		t.Fatalf("no final gate %s", id)
		return FinalGateRow{}
	}

	t.Run("skipped test is missing evidence", func(t *testing.T) {
		in := greenInput()
		ref := FinalGates[1].Tests[0]
		in.TestRun.Outcomes[ref.Key()] = OutcomeSkip
		if g := gate(Assess(in), FinalGates[1].ID); g.Status != StatusInsufficient {
			t.Fatalf("status = %s, want %s", g.Status, StatusInsufficient)
		}
	})
	t.Run("failed test", func(t *testing.T) {
		in := greenInput()
		in.TestRun.Outcomes[FinalGates[0].Tests[1].Key()] = OutcomeFail
		a := Assess(in)
		if g := gate(a, FinalGates[0].ID); g.Status != StatusFail || !strings.Contains(g.Detail, "running_commits_first") {
			t.Fatalf("gate = %+v, want FAIL naming the failing subtest", g)
		}
		if a.GatePass {
			t.Fatal("a failed final gate must fail the release")
		}
	})
	t.Run("package that failed to run reports no test lines but is a failure", func(t *testing.T) {
		in := greenInput()
		ref := FinalGates[1].Tests[0]
		delete(in.TestRun.Outcomes, ref.Key())
		in.TestRun.FailedPackages[ref.Pkg] = true
		if g := gate(Assess(in), FinalGates[1].ID); g.Status != StatusFail {
			t.Fatalf("status = %s, want FAIL (build failure must not read as absent)", g.Status)
		}
	})
	t.Run("test absent from a package that ran", func(t *testing.T) {
		in := greenInput()
		delete(in.TestRun.Outcomes, FinalGates[1].Tests[0].Key())
		if g := gate(Assess(in), FinalGates[1].ID); g.Status != StatusInsufficient {
			t.Fatalf("status = %s, want %s", g.Status, StatusInsufficient)
		}
	})
	t.Run("SourceRef debt", func(t *testing.T) {
		in := greenInput()
		in.Inventory.Violations = []docscoverage.Violation{{Rule: "b", Detail: "GC-INV-09 has no owner"}}
		g := gate(Assess(in), "sourceref-debt-is-zero")
		if g.Status != StatusFail || !strings.Contains(g.Detail, "GC-INV-09") {
			t.Fatalf("gate = %+v, want FAIL showing the violation", g)
		}
	})
	t.Run("git diff --check", func(t *testing.T) {
		for result, want := range map[string]Status{"success": StatusPass, "failure": StatusFail, "": StatusInsufficient, "skipped": StatusInsufficient} {
			in := greenInput()
			in.GitDiffCheck = result
			if g := gate(Assess(in), "git-diff-check-clean"); g.Status != want {
				t.Errorf("git diff --check %q -> %s, want %s", result, g.Status, want)
			}
		}
	})
	t.Run("go test and vet follow the contract job", func(t *testing.T) {
		in := greenInput()
		in.Suites[SuiteContract] = "failure"
		if g := gate(Assess(in), "go-test-and-go-vet-all-packages"); g.Status != StatusFail {
			t.Fatalf("status = %s, want FAIL", g.Status)
		}
	})
}

func TestAssess_JourneyWhoseTestIsGoneIsInsufficient(t *testing.T) {
	in := greenInput()
	delete(in.Scan.Tests, "internal/integration/v5accept|TestV5AcceptFalseCompletionOracle")
	a := Assess(in)
	var j04 JourneyRow
	for _, j := range a.Journeys {
		if j.ID == "J04" {
			j04 = j
		}
	}
	if j04.Status != StatusInsufficient || len(j04.MissingTests) != 1 {
		t.Fatalf("J04 = %+v, want CHƯA ĐỦ EVIDENCE naming the vanished test", j04)
	}
}

func TestAssess_SuiteOnlyJourneyFollowsItsSuite(t *testing.T) {
	in := greenInput()
	in.Suites[SuiteE2E] = "failure"
	a := Assess(in)
	for _, j := range a.Journeys {
		if j.ID == "J11" && j.Status != StatusFail {
			t.Fatalf("J11 = %s, want FAIL when the browser suite fails", j.Status)
		}
	}
}

func TestAssess_MarkdownNamesTheVerdictAndBlockers(t *testing.T) {
	in := greenInput()
	in.Suites[SuiteV6Gate] = "failure"
	md := Assess(in).Markdown()
	for _, want := range []string{"gatePass = **false**", "REWORK", "### Blockers", SuiteV6Gate} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown does not contain %q:\n%s", want, md)
		}
	}
	if md := Assess(greenInput()).Markdown(); !strings.Contains(md, "gatePass = **true**") || !strings.Contains(md, "No blockers.") {
		t.Errorf("green markdown = %s", md)
	}
}

func TestParseGoTestJSON(t *testing.T) {
	stream := strings.Join([]string{
		`# some build noise that is not json`,
		`{"Action":"pass","Package":"github.com/taQuangLing/agent-workflow/internal/a","Test":"TestOK"}`,
		`{"Action":"fail","Package":"github.com/taQuangLing/agent-workflow/internal/a","Test":"TestBad"}`,
		`{"Action":"skip","Package":"github.com/taQuangLing/agent-workflow/internal/a","Test":"TestSkipped"}`,
		`{"Action":"pass","Package":"github.com/taQuangLing/agent-workflow/internal/a","Test":"TestParent/sub"}`,
		`{"Action":"fail","Package":"github.com/taQuangLing/agent-workflow/internal/a","Test":"TestFlaky"}`,
		`{"Action":"pass","Package":"github.com/taQuangLing/agent-workflow/internal/a","Test":"TestFlaky"}`,
		`{"Action":"fail","Package":"github.com/taQuangLing/agent-workflow/internal/broken"}`,
		`{"Action":"output","Package":"github.com/taQuangLing/agent-workflow/internal/a","Test":"TestOK","Output":"x"}`,
	}, "\n")
	run, err := ParseGoTestJSON(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("ParseGoTestJSON: %v", err)
	}
	for ref, want := range map[TestRef]Outcome{
		{"internal/a", "TestOK"}:           OutcomePass,
		{"internal/a", "TestBad"}:          OutcomeFail,
		{"internal/a", "TestSkipped"}:      OutcomeSkip,
		{"internal/a", "TestParent/sub"}:   OutcomePass,
		{"internal/a", "TestFlaky"}:        OutcomeFail, // a later pass never hides an earlier failure
		{"internal/a", "TestNeverRan"}:     OutcomeMissing,
		{"internal/broken", "TestAnyName"}: OutcomeFail, // package failed without per-test lines
	} {
		if got := run.Outcome(ref); got != want {
			t.Errorf("%v = %q, want %q", ref, got, want)
		}
	}
	if _, err := ParseGoTestJSON(strings.NewReader("not json\n")); err == nil {
		t.Error("a stream with no test event must be an error, not an empty success")
	}
}

// --- tables checked against the real repository -----------------------------

const repoRoot = "../.."

func TestJourneyTableMatchesTheDesignDocument(t *testing.T) {
	content, err := os.ReadFile(repoRoot + "/docs/design/01-system-design.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(content)
	start := strings.Index(doc, "## 13. System acceptance journeys")
	if start < 0 {
		t.Fatal("section 13 not found")
	}
	section := doc[start+len("## 13."):]
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}
	item := regexp.MustCompile(`(?m)^(\d+[A-Z]?)\. `)
	want := map[string]bool{}
	for _, m := range item.FindAllStringSubmatch(section, -1) {
		n := m[1]
		digits := strings.TrimRight(n, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
		if len(digits) == 1 {
			digits = "0" + digits
		}
		want["J"+digits+strings.TrimPrefix(n, strings.TrimRight(n, "ABCDEFGHIJKLMNOPQRSTUVWXYZ"))] = true
	}
	got := map[string]bool{}
	for _, j := range Journeys {
		if got[j.ID] {
			t.Errorf("duplicate journey %s", j.ID)
		}
		got[j.ID] = true
		if len(j.Tests) == 0 && len(j.Suites) == 0 {
			t.Errorf("journey %s has no evidence at all", j.ID)
		}
	}
	for id := range want {
		if !got[id] {
			t.Errorf("design journey %s has no row in Journeys", id)
		}
	}
	for id := range got {
		if !want[id] {
			t.Errorf("Journeys row %s is not a journey in docs/design/01-system-design.md §13", id)
		}
	}
	if len(want) != 23 {
		t.Errorf("the design lists %d journeys, expected 23 (1-22 and 15A) — update this guard if the design changed on purpose", len(want))
	}
}

func TestJourneyAndFinalGateTestsExistInTheRepository(t *testing.T) {
	scan, err := ScanRepository(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range Journeys {
		for _, ref := range j.Tests {
			if !scan.HasTest(ref) {
				t.Errorf("journey %s names %s, which does not exist", j.ID, ref.Key())
			}
		}
	}
	for _, g := range FinalGates {
		for _, ref := range g.Tests {
			if !scan.HasTest(ref) {
				t.Errorf("final gate %s names %s, which does not exist", g.ID, ref.Key())
			}
		}
		sources := 0
		for _, present := range []bool{len(g.Tests) > 0, len(g.Suites) > 0, g.Computed != ""} {
			if present {
				sources++
			}
		}
		if sources != 1 {
			t.Errorf("final gate %s must have exactly one evidence source, has %d", g.ID, sources)
		}
	}
}

func workflowText(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(repoRoot + "/.github/workflows/spike-gate.yml")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}

// TestSuiteTableMatchesTheWorkflow keeps AllSuites, the CI job ids and the
// alpha gate job's own `needs` list identical: a suite the assessment reads
// must exist, and a job the gate does not depend on would never be read.
func TestSuiteTableMatchesTheWorkflow(t *testing.T) {
	workflow := workflowText(t)
	jobID := regexp.MustCompile(`(?m)^  ([a-z0-9-]+):\s*$`)
	jobs := map[string]bool{}
	for _, m := range jobID.FindAllStringSubmatch(workflow, -1) {
		jobs[m[1]] = true
	}
	for _, id := range AllSuites {
		if !jobs[id] {
			t.Errorf("suite %q is not a job id in .github/workflows/spike-gate.yml", id)
		}
	}

	const header = "  v8-alpha-gate:\n"
	start := strings.Index(workflow, "\n"+header)
	if start < 0 {
		t.Fatal("the workflow has no v8-alpha-gate job")
	}
	job := workflow[start+1:]
	if next := regexp.MustCompile(`(?m)^  [a-z0-9-]+:\s*$`).FindStringIndex(job[len(header):]); next != nil {
		job = job[:len(header)+next[0]]
	}
	needs := regexp.MustCompile(`(?m)^    needs: \[([^\]]+)\]`).FindStringSubmatch(job)
	if needs == nil {
		t.Fatal("v8-alpha-gate has no inline `needs: [...]` list")
	}
	listed := map[string]bool{}
	for _, id := range strings.Split(needs[1], ",") {
		listed[strings.TrimSpace(id)] = true
	}
	for _, id := range AllSuites {
		if !listed[id] {
			t.Errorf("v8-alpha-gate does not depend on suite %q", id)
		}
	}
	for id := range listed {
		known := false
		for _, s := range AllSuites {
			if s == id {
				known = true
			}
		}
		if !known {
			t.Errorf("v8-alpha-gate depends on %q, which is not in AllSuites", id)
		}
	}
	if !strings.Contains(job, "if: always()") {
		t.Error("v8-alpha-gate must run with `if: always()` so a failing suite still produces an assessment")
	}
}

// TestWorkflowFinalGateRunCoversTheFinalGateTable keeps the CI step that
// produces the final-gate `go test -json` stream in step with FinalGates.
func TestWorkflowFinalGateRunCoversTheFinalGateTable(t *testing.T) {
	workflow := workflowText(t)
	for _, pkg := range FinalGateTestPackages() {
		if !strings.Contains(workflow, "./"+pkg) {
			t.Errorf("the workflow's final-gate run does not list package ./%s", pkg)
		}
	}
	for _, g := range FinalGates {
		for _, ref := range g.Tests {
			top := strings.SplitN(ref.Name, "/", 2)[0]
			if !strings.Contains(workflow, top) {
				t.Errorf("the workflow's final-gate -run pattern does not name %s", top)
			}
		}
	}
	if !strings.Contains(workflow, "AW_ALPHA_GATE=1") {
		t.Error("the final-gate run must set AW_ALPHA_GATE=1 or the opt-in parity-debt test skips")
	}
}

// TestRealRepositoryMatrixIsCompleteUnderFullyGreenEvidence assesses the REAL
// repository with every suite green and every final-gate test passing. Under
// that assumption the matrix must have no blocker at all: any blocker left is
// a defect in the tables or the coverage inventory (an unmapped criterion, a
// journey test that vanished, SourceRef debt), not in the product — and the
// matrix must contain exactly one row per criterion, journey and ADR.
func TestRealRepositoryMatrixIsCompleteUnderFullyGreenEvidence(t *testing.T) {
	in, err := Load(Options{RepoRoot: repoRoot, Commit: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range AllSuites {
		in.Suites[id] = "success"
	}
	for _, g := range FinalGates {
		for _, ref := range g.Tests {
			in.TestRun.Outcomes[ref.Key()] = OutcomePass
		}
	}
	in.GitDiffCheck = "success"
	a := Assess(in)

	if len(a.Criteria) != len(in.Inventory.Criteria) || len(a.Criteria) == 0 {
		t.Fatalf("matrix has %d criterion rows for %d inventory criteria", len(a.Criteria), len(in.Inventory.Criteria))
	}
	if len(a.Decisions) != len(in.Inventory.Decisions) {
		t.Fatalf("matrix has %d ADR rows for %d ADRs", len(a.Decisions), len(in.Inventory.Decisions))
	}
	if len(a.Journeys) != len(Journeys) || len(a.FinalGates) != len(FinalGates) || len(a.VersionGates) != len(Versions) {
		t.Fatalf("journeys/finalGates/versionGates = %d/%d/%d", len(a.Journeys), len(a.FinalGates), len(a.VersionGates))
	}
	seen := map[string]bool{}
	for _, c := range a.Criteria {
		if seen[c.ID] {
			t.Errorf("duplicate criterion row %s", c.ID)
		}
		seen[c.ID] = true
		if c.Status == "" {
			t.Errorf("criterion %s has no status", c.ID)
		}
		gated := c.Phase == string(docscoverage.AlphaMust) || c.Phase == string(docscoverage.CrossPhaseGuard)
		if gated && (len(c.RequiredSuites) == 0 || c.EvidenceLevel == "") {
			t.Errorf("gated criterion %s has no required suites or evidence level: %+v", c.ID, c)
		}
	}
	if a.Summary.AlphaMust.Total == 0 || a.Summary.AlphaMust.Total != a.Summary.AlphaMust.Pass {
		t.Fatalf("ALPHA_MUST = %+v, want every criterion PASS under green evidence", a.Summary.AlphaMust)
	}
	if !a.GatePass {
		t.Fatalf("under fully green evidence the gate must pass; blockers: %+v", a.Blockers)
	}
	var ids []string
	for id := range seen {
		ids = append(ids, id)
	}
	t.Logf("assessed %d criteria, %d journeys, %d ADRs; %d criteria test-cited, %d suite-level only",
		len(ids), len(a.Journeys), len(a.Decisions), a.Summary.TestLevelEvidence, a.Summary.SuiteLevelOnly)
}

// TestScanNeverCountsTheCoverageReadersAsEvidence: internal/alphagate and
// internal/docscoverage tests name criterion IDs as fixtures.
func TestScanNeverCountsTheCoverageReadersAsEvidence(t *testing.T) {
	scan, err := ScanRepository(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for id, citations := range scan.Citations {
		for _, c := range citations {
			if strings.HasPrefix(c.Pkg, "internal/alphagate") || strings.HasPrefix(c.Pkg, "internal/docscoverage") {
				t.Errorf("%s is 'evidenced' by %s, a meta package", id, c.File)
			}
		}
	}
	if len(scan.Citations) == 0 || len(scan.Tests) == 0 {
		t.Fatalf("the scan found %d cited criteria and %d tests — it is broken", len(scan.Citations), len(scan.Tests))
	}
}

func TestFinalGateFailureDetailCarriesTheTestsOwnOutput(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"output","Package":"github.com/taQuangLing/agent-workflow/internal/delivery/parity","Test":"TestParityLedgerIsEmpty","Output":"    alpha_gate_test.go:143: the parity ledger still pins 15 debt entr(ies)\n"}`,
		`{"Action":"fail","Package":"github.com/taQuangLing/agent-workflow/internal/delivery/parity","Test":"TestParityLedgerIsEmpty"}`,
	}, "\n")
	run, err := ParseGoTestJSON(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	in := greenInput()
	in.TestRun = run
	var detail string
	for _, g := range Assess(in).FinalGates {
		if g.ID == "parity-inventory-has-zero-debt" {
			detail = g.Detail
		}
	}
	if !strings.Contains(detail, "still pins 15 debt") {
		t.Fatalf("detail = %q, want the failing test's own output", detail)
	}
}
