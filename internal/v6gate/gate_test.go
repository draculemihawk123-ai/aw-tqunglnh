package v6gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture builds a complete, PASSING evidence tree, which each test then
// damages in exactly one way. Starting from a green baseline is what makes
// each assertion below about ONE thing: if a test goes red, the single
// mutation it made is the cause.
type fixture struct {
	t         *testing.T
	root      string
	repoRoot  string
	commit    string
	contract  string
	scenarios map[string]map[string]string // platform -> test -> action
}

const testContractPrefix = "1"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:         t,
		root:      t.TempDir(),
		repoRoot:  t.TempDir(),
		commit:    "0123456789abcdef0123456789abcdef01234567",
		scenarios: map[string]map[string]string{},
	}

	goldenDir := filepath.Join(f.repoRoot, "internal", "delivery", "httpapi", "apicontract", "testdata", "golden")
	if err := os.MkdirAll(goldenDir, 0o755); err != nil {
		t.Fatalf("mkdir golden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goldenDir, "contract.json"), []byte(`{"routes":[]}`), 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
	identity, err := contractIdentity(f.repoRoot, testContractPrefix)
	if err != nil {
		t.Fatalf("contractIdentity: %v", err)
	}
	f.contract = identity

	for _, platform := range Platforms {
		f.scenarios[platform] = map[string]string{}
		for _, name := range requiredScenarios {
			f.scenarios[platform][name] = "pass"
		}
		for _, name := range conditionalScenarios {
			f.scenarios[platform][name] = "pass"
		}
	}
	f.writeStability(true, true, true)
	return f
}

func (f *fixture) platformDir(platform string) string {
	dir := filepath.Join(f.root, "v6-acceptance-report-"+platform)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func (f *fixture) writeJSON(path string, value any) {
	f.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		f.t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		f.t.Fatalf("write %s: %v", path, err)
	}
}

func (f *fixture) writeStability(race, stable, spk08 bool) {
	dir := filepath.Join(f.root, "v0-12-stability-report")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatalf("mkdir stability: %v", err)
	}
	f.writeJSON(filepath.Join(dir, "v0-12-stability-report.json"), map[string]any{
		"raceDetector":        map[string]any{"passed": race},
		"spk08WriteLeaseRace": map[string]any{"meetsMinimum100": spk08},
		"allTenRunsStable":    stable,
	})
}

// materialize writes every platform's three files from the fixture's current
// state.
func (f *fixture) materialize() {
	f.t.Helper()
	for _, platform := range Platforms {
		dir := f.platformDir(platform)
		f.writeJSON(filepath.Join(dir, "report.json"), map[string]any{
			"goos":            platform,
			"contractVersion": f.contract,
			"allPassed":       true,
			"stages":          []map[string]any{{"name": "01_health_doctor_settings", "passed": true}},
		})
		f.writeJSON(filepath.Join(dir, "meta.json"), map[string]any{"commit": f.commit, "goos": platform})

		var lines []string
		for name, action := range f.scenarios[platform] {
			if action == "skip" {
				lines = append(lines, jsonLine(map[string]any{"Action": "output", "Test": name, "Output": "    x_test.go:12: could not create the condition"}))
			}
			lines = append(lines, jsonLine(map[string]any{"Action": action, "Test": name}))
		}
		if err := os.WriteFile(filepath.Join(dir, "acceptance.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			f.t.Fatalf("write jsonl: %v", err)
		}
	}
}

func jsonLine(value map[string]any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func (f *fixture) run(docsDebt int) Report {
	f.t.Helper()
	report, err := Run(Inputs{
		EvidenceDir:           f.root,
		RepoRoot:              f.repoRoot,
		ExpectedCommit:        f.commit,
		ContractVersionPrefix: testContractPrefix,
		DocsDebt:              docsDebt,
	})
	if err != nil {
		f.t.Fatalf("Run: %v", err)
	}
	return report
}

func requireVerdict(t *testing.T, report Report, want Verdict) {
	t.Helper()
	if report.Verdict != want {
		t.Fatalf("verdict = %q, want %q; findings=%+v", report.Verdict, want, report.Findings)
	}
}

// requireFinding asserts some finding names the task and mentions the phrase,
// so a test pins the ACTIONABLE part of the message, not its wording.
func requireFinding(t *testing.T, report Report, taskID, phrase string) {
	t.Helper()
	for _, f := range report.Findings {
		if f.TaskID == taskID && strings.Contains(f.Detail, phrase) {
			return
		}
	}
	t.Fatalf("no finding for task %s containing %q; got %+v", taskID, phrase, report.Findings)
}

func TestGate_CompleteGreenEvidencePasses(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	report := f.run(0)
	requireVerdict(t, report, VerdictPass)
	if len(report.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", report.Findings)
	}
	if report.ContractVersion != f.contract {
		t.Errorf("contract version = %q, want %q", report.ContractVersion, f.contract)
	}
	for _, name := range append(append([]string{}, requiredScenarios...), conditionalScenarios...) {
		if len(report.Scenarios[name]) != len(Platforms) {
			t.Errorf("scenario %s indexed for %d platforms, want %d", name, len(report.Scenarios[name]), len(Platforms))
		}
	}
}

// A whole platform going missing must never be a pass — the case V6-14B's own
// "unavailable platform is not silently marked PASS" names.
func TestGate_MissingPlatformIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	if err := os.RemoveAll(filepath.Join(f.root, "v6-acceptance-report-windows-latest")); err != nil {
		t.Fatalf("remove platform: %v", err)
	}
	report := f.run(0)
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V6-14B", "windows-latest")
}

func TestGate_RequiredScenarioFailureIsRework(t *testing.T) {
	f := newFixture(t)
	f.scenarios["ubuntu-latest"]["TestV6HTTPAcceptance_Fault_PoisonProjection"] = "fail"
	f.materialize()
	report := f.run(0)
	requireVerdict(t, report, VerdictRework)
	requireFinding(t, report, "V6-14A", "TestV6HTTPAcceptance_Fault_PoisonProjection failed")
}

// The waiver V6-14C's "Không làm" forbids: a required scenario that skipped
// has not been demonstrated, so it cannot count as a pass.
func TestGate_RequiredScenarioSkipIsNotAPass(t *testing.T) {
	f := newFixture(t)
	f.scenarios["windows-latest"]["TestV6HTTPAcceptance_Fault_CrashAfterGitCommit"] = "skip"
	f.materialize()
	report := f.run(0)
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V6-14A", "was skipped, and a skip is not a pass")
}

func TestGate_RequiredScenarioAbsentIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	delete(f.scenarios["ubuntu-latest"], "TestV6HTTPAcceptance_Fault_RoleDowngradeMidFlight")
	f.materialize()
	report := f.run(0)
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V6-14A", "did not run")
}

// A conditional scenario may lose its race on ONE machine: that is a recorded
// limitation, not a defect, and the run still passes.
func TestGate_ConditionalScenarioMaySkipOnOnePlatform(t *testing.T) {
	f := newFixture(t)
	f.scenarios["windows-latest"]["TestV6HTTPAcceptance_Fault_SlowSSEClientDoesNotBlockServer"] = "skip"
	f.materialize()
	report := f.run(0)
	requireVerdict(t, report, VerdictPass)
	if len(report.Notes) == 0 {
		t.Fatal("a tolerated skip must still be recorded as a note, never silently dropped")
	}
	if !strings.Contains(strings.Join(report.Notes, "\n"), "SlowSSEClient") {
		t.Errorf("note should name the skipped scenario, got %v", report.Notes)
	}
}

// ...but skipping it EVERYWHERE means the property was never demonstrated at
// all, which is missing evidence rather than a pass.
func TestGate_ConditionalScenarioSkippedEverywhereIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	for _, platform := range Platforms {
		f.scenarios[platform]["TestV6HTTPAcceptance_Fault_CrashDuringRebuildBeforeCutover"] = "skip"
	}
	f.materialize()
	report := f.run(0)
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V6-14A", "never demonstrated on ANY platform")
}

func TestGate_ConditionalScenarioFailureIsStillRework(t *testing.T) {
	f := newFixture(t)
	f.scenarios["ubuntu-latest"]["TestV6HTTPAcceptance_Fault_SlowSSEClientDoesNotBlockServer"] = "fail"
	f.materialize()
	report := f.run(0)
	requireVerdict(t, report, VerdictRework)
	requireFinding(t, report, "V6-14A", "a skip is tolerated, a failure is not")
}

// Evidence from another revision describes different code, so it proves
// nothing about the revision being gated.
func TestGate_EvidenceFromAnotherCommitIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	f.writeJSON(filepath.Join(f.platformDir("ubuntu-latest"), "meta.json"),
		map[string]any{"commit": "ffffffffffffffffffffffffffffffffffffffff", "goos": "ubuntu-latest"})
	report := f.run(0)
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V6-14B", "describes different code")
}

// The gate recomputes the contract identity from the checkout, so evidence
// recorded against a different API surface cannot be passed off as current.
func TestGate_ContractVersionMismatchIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	f.writeJSON(filepath.Join(f.platformDir("windows-latest"), "report.json"), map[string]any{
		"goos": "windows-latest", "contractVersion": "1+deadbeefdeadbeef", "allPassed": true,
	})
	report := f.run(0)
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V6-14B", "did not exercise this API surface")
}

func TestGate_JourneyRegressionIsRework(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	f.writeJSON(filepath.Join(f.platformDir("ubuntu-latest"), "report.json"), map[string]any{
		"goos": "ubuntu-latest", "contractVersion": f.contract, "allPassed": false,
		"stages": []map[string]any{{"name": "05_work_item_and_run", "passed": false}},
	})
	report := f.run(0)
	requireVerdict(t, report, VerdictRework)
	requireFinding(t, report, "V6-14", "journey stage 05_work_item_and_run failed")
}

func TestGate_StabilityFailuresAreRework(t *testing.T) {
	for _, tc := range []struct {
		name                string
		race, stable, spk08 bool
		phrase              string
	}{
		{"race", false, true, true, "race detector did not pass"},
		{"stability", true, false, true, "not stable across all ten runs"},
		{"spk08", true, true, false, "100-iteration minimum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.materialize()
			f.writeStability(tc.race, tc.stable, tc.spk08)
			report := f.run(0)
			requireVerdict(t, report, VerdictRework)
			requireFinding(t, report, "V0-12", tc.phrase)
		})
	}
}

func TestGate_MissingStabilityReportIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	if err := os.RemoveAll(filepath.Join(f.root, "v0-12-stability-report")); err != nil {
		t.Fatalf("remove stability: %v", err)
	}
	report := f.run(0)
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V0-12", "race and 10x-stability results are unknown")
}

func TestGate_DocumentationDebtIsRework(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	report := f.run(3)
	requireVerdict(t, report, VerdictRework)
	requireFinding(t, report, "V1-00C", "debt = 3")
}

// Missing evidence outranks a failure: when part of the picture is absent,
// the honest headline is that the picture is incomplete, not that the visible
// part is bad.
func TestGate_MissingEvidenceOutranksRework(t *testing.T) {
	f := newFixture(t)
	f.scenarios["ubuntu-latest"]["TestV6HTTPAcceptance_Fault_PoisonProjection"] = "fail"
	f.materialize()
	if err := os.RemoveAll(filepath.Join(f.root, "v6-acceptance-report-windows-latest")); err != nil {
		t.Fatalf("remove platform: %v", err)
	}
	report := f.run(0)
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V6-14A", "TestV6HTTPAcceptance_Fault_PoisonProjection failed")
}

// The per-scenario stream is the only evidence that covers scenarios; the
// journey's own report describes one test's stages and cannot stand in for it.
func TestGate_MissingScenarioStreamIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	if err := os.Remove(filepath.Join(f.platformDir("windows-latest"), "acceptance.jsonl")); err != nil {
		t.Fatalf("remove jsonl: %v", err)
	}
	report := f.run(0)
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V6-14A", "no readable per-scenario results")
}

// Subtests must not be mistaken for scenarios: the journey's stages arrive as
// "Parent/01_stage" records in the same stream.
func TestParseTestEvents_IgnoresSubtestsAndCapturesSkipReasons(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acceptance.jsonl")
	lines := []string{
		jsonLine(map[string]any{"Action": "pass", "Test": "TestParent/01_stage"}),
		jsonLine(map[string]any{"Action": "pass", "Test": "TestParent"}),
		jsonLine(map[string]any{"Action": "output", "Test": "TestSkipped", "Output": "    a_test.go:9: burst too slow to overflow"}),
		jsonLine(map[string]any{"Action": "skip", "Test": "TestSkipped"}),
		"not json at all",
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	outcomes, err := parseTestEvents(path)
	if err != nil {
		t.Fatalf("parseTestEvents: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2 (subtests must not count): %+v", len(outcomes), outcomes)
	}
	if outcomes["TestParent"].Action != "pass" {
		t.Errorf("TestParent = %+v, want pass", outcomes["TestParent"])
	}
	if got := outcomes["TestSkipped"]; got.Action != "skip" || !strings.Contains(got.Reason, "burst too slow") {
		t.Errorf("TestSkipped = %+v, want a skip carrying its real reason", got)
	}
}

// Every required and conditional scenario must name a real test in the
// acceptance package, or the gate is guarding a list that drifted away from
// the suite it describes.
func TestScenarioListsMatchTheRealSuite(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "integration", "v6accept", "journey_test.go"))
	if err != nil {
		t.Skipf("acceptance package not readable: %v", err)
	}
	if !strings.Contains(string(source), "func "+requiredScenarios[0]) {
		t.Errorf("%s is required by the gate but not defined in journey_test.go", requiredScenarios[0])
	}

	entries, err := os.ReadDir(filepath.Join("..", "integration", "v6accept"))
	if err != nil {
		t.Skipf("acceptance package not readable: %v", err)
	}
	var all strings.Builder
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join("..", "integration", "v6accept", entry.Name()))
		if err != nil {
			continue
		}
		all.Write(data)
	}
	body := all.String()
	for _, name := range append(append([]string{}, requiredScenarios...), conditionalScenarios...) {
		if !strings.Contains(body, "func "+name+"(") {
			t.Errorf("gate lists scenario %s, but no such test exists in internal/integration/v6accept", name)
		}
	}
}
