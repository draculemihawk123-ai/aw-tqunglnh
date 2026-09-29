package v8gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture builds a complete, PASSING evidence tree across BOTH artifact
// families (v6-acceptance-report-<platform> and v8-04e-evidence-<platform>),
// which each test then damages in exactly one way — mirrors internal/v6gate's
// own fixture pattern (gate_test.go) deliberately, so a reader familiar with
// that gate's own tests needs no new mental model here.
type fixture struct {
	t      *testing.T
	root   string
	commit string
	// outcomes["v6-acceptance-report"]["ubuntu-latest"]["TestFoo"] = "pass"
	outcomes map[string]map[string]map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:        t,
		root:     t.TempDir(),
		commit:   "0123456789abcdef0123456789abcdef01234567",
		outcomes: map[string]map[string]map[string]string{},
	}
	for _, source := range []string{string(sourceV6Acceptance), string(sourceV8SecuritySuite)} {
		f.outcomes[source] = map[string]map[string]string{}
		for _, platform := range Platforms {
			f.outcomes[source][platform] = map[string]string{}
		}
	}
	for _, s := range scenarios {
		for _, platform := range Platforms {
			f.outcomes[string(s.Source)][platform][s.Name] = "pass"
		}
	}
	return f
}

func (f *fixture) platformDir(source, platform string) string {
	dir := filepath.Join(f.root, source+"-"+platform)
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

// materialize writes every (source, platform) pair's meta.json and jsonl
// stream from the fixture's current state.
func (f *fixture) materialize() {
	f.t.Helper()
	for source, byPlatform := range f.outcomes {
		for platform, tests := range byPlatform {
			dir := f.platformDir(source, platform)
			f.writeJSON(filepath.Join(dir, "meta.json"), map[string]any{"commit": f.commit, "goos": platform})

			var lines []string
			for name, action := range tests {
				if action == "skip" {
					lines = append(lines, jsonLine(map[string]any{"Action": "output", "Test": name, "Output": "    x_test.go:12: could not create the condition"}))
				}
				lines = append(lines, jsonLine(map[string]any{"Action": action, "Test": name}))
			}
			if err := os.WriteFile(filepath.Join(dir, jsonlFileFor(source)), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
				f.t.Fatalf("write jsonl: %v", err)
			}
		}
	}
}

func jsonLine(value map[string]any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func (f *fixture) run() Report {
	f.t.Helper()
	report, err := Run(Inputs{EvidenceDir: f.root, ExpectedCommit: f.commit})
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
	report := f.run()
	requireVerdict(t, report, VerdictPass)
	if len(report.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", report.Findings)
	}
	for _, s := range scenarios {
		if len(report.Scenarios[s.Name]) != len(Platforms) {
			t.Errorf("scenario %s indexed for %d platforms, want %d", s.Name, len(report.Scenarios[s.Name]), len(Platforms))
		}
	}
	for _, suite := range []string{"V8-04A", "V8-04B", "V8-04C", "V8-04D"} {
		found := false
		for _, s := range report.Suites {
			if s == suite {
				found = true
			}
		}
		if !found {
			t.Errorf("suites = %v, missing %s", report.Suites, suite)
		}
	}
}

// A whole platform's own artifact going missing must never be silently
// treated as a pass.
func TestGate_MissingPlatformArtifactIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	if err := os.RemoveAll(f.platformDir(string(sourceV8SecuritySuite), "windows-latest")); err != nil {
		t.Fatalf("remove platform dir: %v", err)
	}
	report := f.run()
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V8-04E", "windows-latest")
}

func TestGate_RequiredScenarioFailureIsRework(t *testing.T) {
	f := newFixture(t)
	f.outcomes[string(sourceV8SecuritySuite)]["ubuntu-latest"]["TestLoopbackOnlyBind"] = "fail"
	f.materialize()
	report := f.run()
	requireVerdict(t, report, VerdictRework)
	requireFinding(t, report, "V8-04C", "TestLoopbackOnlyBind failed")
}

// The completion bar's own "không suite nào bị bỏ qua" (no suite skipped):
// a required scenario that skipped has not been demonstrated, so it cannot
// count as a pass.
func TestGate_RequiredScenarioSkipIsNotAPass(t *testing.T) {
	f := newFixture(t)
	f.outcomes[string(sourceV8SecuritySuite)]["ubuntu-latest"]["TestV5AcceptIsolationUnavailable_RealAdmissionRejectsBeforeSpawn"] = "skip"
	f.materialize()
	report := f.run()
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V8-04B", "was skipped, and a skip is not a pass")
}

func TestGate_RequiredScenarioAbsentIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	delete(f.outcomes[string(sourceV6Acceptance)]["ubuntu-latest"], "TestV8PathAbuse_RepositoryNestedUnderWorkspaceRoot_Rejected")
	f.materialize()
	report := f.run()
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V8-04A", "did not run")
}

// The one scenario with a per-platform SkipAllowedOn entry: a real,
// deterministic Windows Developer-Mode privilege gap, tolerated on that one
// named platform without dragging the verdict down.
func TestGate_SymlinkScenarioMaySkipOnWindowsOnly(t *testing.T) {
	f := newFixture(t)
	f.outcomes[string(sourceV6Acceptance)]["windows-latest"]["TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected"] = "skip"
	f.materialize()
	report := f.run()
	requireVerdict(t, report, VerdictPass)
	if len(report.Notes) == 0 {
		t.Fatal("a tolerated skip must still be recorded as a note, never silently dropped")
	}
	if !strings.Contains(strings.Join(report.Notes, "\n"), "SymlinkDisguisedNestedRepository") {
		t.Errorf("note should name the skipped scenario, got %v", report.Notes)
	}
}

// ...but the SAME scenario skipping on ubuntu-latest (not in its own
// SkipAllowedOn set) is still missing evidence — the allowance is
// per-platform, not blanket.
func TestGate_SymlinkScenarioSkipOnUbuntuIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	f.outcomes[string(sourceV6Acceptance)]["ubuntu-latest"]["TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected"] = "skip"
	f.materialize()
	report := f.run()
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V8-04A", "was skipped, and a skip is not a pass")
}

// Evidence from another revision describes different code, so it proves
// nothing about the revision being gated.
func TestGate_EvidenceFromAnotherCommitIsInsufficientEvidence(t *testing.T) {
	f := newFixture(t)
	f.materialize()
	f.writeJSON(filepath.Join(f.platformDir(string(sourceV8SecuritySuite), "ubuntu-latest"), "meta.json"),
		map[string]any{"commit": "ffffffffffffffffffffffffffffffffffffffff", "goos": "ubuntu-latest"})
	report := f.run()
	requireVerdict(t, report, VerdictInsufficientEvidence)
	requireFinding(t, report, "V8-04B", "describes different code")
}

// A single suite failing anywhere fails the WHOLE gate — the design doc's
// own "một suite fail làm gate fail" bar, made concrete: this mutates only
// V8-04D's own scenario, and the verdict still comes back non-PASS overall.
func TestGate_OneSuiteFailingFailsTheWholeGate(t *testing.T) {
	f := newFixture(t)
	f.outcomes[string(sourceV8SecuritySuite)]["windows-latest"]["TestV5AcceptRetainedDataSecretScan_RealSecretNeverPersistedUnredacted"] = "fail"
	f.materialize()
	report := f.run()
	requireVerdict(t, report, VerdictRework)
	requireFinding(t, report, "V8-04D", "failed")
}
