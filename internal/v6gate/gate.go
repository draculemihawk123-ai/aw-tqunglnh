// Package v6gate started as V6-14C — "Gate cuối API/projection"
// (docs/design/08-v6-api-projections.md V6-14C: "phát một verdict từ evidence
// happy/fault/platform đầy đủ trước gate terminal", verified by "one
// reproducible gate command over all artifacts") — and is now also V6-15P's
// own final terminal gate: V6-15P depends on V6-14C by name, and rather than
// building a second, near-duplicate gate command, its "Hoàn thành khi: final
// verdict PASS" bar is satisfied by requiredScenarios below also naming
// V6-15P's own terminal-acceptance journey
// (TestV6TerminalAcceptance_CLIJourneyThenHTTPReplay,
// internal/integration/v6accept/stage_terminal_test.go — the operator's core
// journey/recovery driven entirely through `aw <resource> <action>` one-shot
// processes, plus a fresh HTTP-driven cycle proving the same installation
// still serves HTTP afterward). No new evidence artifact was needed for
// this: that test runs inside the SAME package the existing `v6-acceptance`
// CI job already executes wholesale, so it is already present in
// acceptance.jsonl on both platforms.
//
// It is a pure reader. It never runs a test, never starts a process and never
// writes to the repository: it consumes the artifacts CI already produces —
// V6-14B's per-platform acceptance evidence and V0-12's stability report —
// checks them against the repository they claim to describe, and emits one
// verdict.
//
// The three verdicts are not interchangeable, and keeping them apart is the
// point of this package:
//
//   - INSUFFICIENT_EVIDENCE ("CHƯA ĐỦ EVIDENCE") — a required input is absent,
//     unreadable, or describes a different revision. Nothing is known, so
//     nothing may be claimed. This is never downgraded to a pass.
//   - REWORK — the evidence is present and says something failed.
//   - PASS — every required input is present, belongs to this revision, and
//     reports success.
//
// Every failing finding names the Task ID that owns the evidence, so a reader
// is sent to the task that must fix it rather than to this gate.
package v6gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Verdict is the gate's single overall answer.
type Verdict string

const (
	VerdictPass                 Verdict = "PASS"
	VerdictRework               Verdict = "REWORK"
	VerdictInsufficientEvidence Verdict = "CHƯA ĐỦ EVIDENCE"
)

// Platforms are the two GOOS targets V6-14B requires evidence from. A gate
// that accepted whichever platforms happened to show up would silently pass a
// run where one platform never produced anything, which is exactly the
// "unavailable platform is not silently marked PASS" case V6-14B names.
var Platforms = []string{"ubuntu-latest", "windows-latest"}

// requiredScenarios must PASS on every platform. These are the properties
// that hold on any machine: nothing here depends on winning a race or on the
// host being fast enough to create a particular load.
var requiredScenarios = []string{
	"TestV6HTTPAcceptance_CleanDatabaseJourney",
	"TestV6HTTPAcceptance_Fault_CrashAfterAttachmentPut",
	"TestV6HTTPAcceptance_Fault_CancelQuiesceSurvivesWorkerCrash",
	"TestV6HTTPAcceptance_Fault_ConcurrentSameIdempotencyKey",
	"TestV6HTTPAcceptance_Fault_ConcurrentDifferentIdempotencyKeysSameTarget",
	"TestV6HTTPAcceptance_Fault_CrashAfterGitCommit",
	"TestV6HTTPAcceptance_Fault_PoisonProjection",
	"TestV6HTTPAcceptance_Fault_CrashAfterRebuildCutover",
	"TestV6HTTPAcceptance_Fault_CrashAfterReceiptCommit",
	"TestV6HTTPAcceptance_Fault_RoleDowngradeMidFlight",
	// V6-15P's own terminal acceptance journey: the operator's core
	// journey/recovery driven entirely through `aw <resource> <action>`
	// one-shot processes, plus a fresh HTTP-driven cycle proving the same
	// installation still serves HTTP afterward. This is what turns this
	// gate into V6's own final terminal gate (V6-15P's own "Hoàn thành
	// khi": this verdict, PASS) rather than stopping at V6-14C's
	// happy/fault/platform evidence alone. Deterministic (3 consecutive
	// local runs, both before and after being folded into the full
	// package's own run) — required, not conditional.
	"TestV6TerminalAcceptance_CLIJourneyThenHTTPReplay",
}

// conditionalScenarios genuinely cannot be forced on every machine: each one
// needs to win a real race (a rebuild observed mid-flight) or to create a real
// overload (messages pushed faster than the server drains them), and a slow
// runner can lose either honestly. They are allowed to SKIP on a platform —
// but NOT on every platform at once.
//
// That "at least one platform" rule is the whole reason this category exists
// rather than being dropped from the gate. A property that skipped everywhere
// was never demonstrated anywhere, and reporting that as PASS would be the
// waiver V6-14C's own "Không làm: no waiver for failed/missing required
// scenario" forbids. A skip on one platform is a real, recorded limitation of
// that machine; a skip on all of them is missing evidence.
var conditionalScenarios = []string{
	"TestV6HTTPAcceptance_Fault_CrashDuringRebuildBeforeCutover",
	"TestV6HTTPAcceptance_Fault_SlowSSEClientDoesNotBlockServer",
}

// Finding is one thing the gate objects to.
type Finding struct {
	// TaskID owns the evidence this finding is about, so the reader is sent
	// to the task that must fix it.
	TaskID string `json:"taskId"`
	// Verdict is this finding's own severity: INSUFFICIENT_EVIDENCE when
	// something is missing or unreadable, REWORK when it is present and bad.
	Verdict Verdict `json:"verdict"`
	Detail  string  `json:"detail"`
}

// ScenarioOutcome is one scenario's result on one platform.
type ScenarioOutcome struct {
	Action string `json:"action"` // pass | fail | skip
	Reason string `json:"reason,omitempty"`
}

// Report is the evidence index plus the verdict, written as the gate's own
// durable artifact.
type Report struct {
	Verdict         Verdict                               `json:"verdict"`
	Commit          string                                `json:"commit"`
	ContractVersion string                                `json:"contractVersion"`
	DocsDebt        int                                   `json:"docsDebt"`
	Platforms       []string                              `json:"platforms"`
	Scenarios       map[string]map[string]ScenarioOutcome `json:"scenarios"`
	Findings        []Finding                             `json:"findings"`
	Notes           []string                              `json:"notes,omitempty"`
}

// Inputs is everything Run reads.
type Inputs struct {
	// EvidenceDir holds the downloaded CI artifacts, one directory per
	// artifact name (v6-acceptance-report-<os>/, v0-12-stability-report/).
	EvidenceDir string
	// RepoRoot is the checkout the evidence claims to describe. The gate
	// recomputes the contract identity from it rather than trusting the
	// number the evidence carries.
	RepoRoot string
	// ExpectedCommit, when set, is the revision the evidence must belong to.
	ExpectedCommit string
	// ContractVersionPrefix is apicontract.ContractVersion, injected so this
	// package stays free of delivery-layer imports.
	ContractVersionPrefix string
	// DocsDebt is the V1-00C debt count the caller computed.
	DocsDebt int
}

// platformEvidence is one platform's own uploaded artifact, already parsed.
type platformEvidence struct {
	report    acceptanceReport
	meta      evidenceMeta
	scenarios map[string]ScenarioOutcome
}

type acceptanceReport struct {
	GOOS            string `json:"goos"`
	ContractVersion string `json:"contractVersion"`
	AllPassed       bool   `json:"allPassed"`
	Stages          []struct {
		Name   string `json:"name"`
		Passed bool   `json:"passed"`
	} `json:"stages"`
}

type evidenceMeta struct {
	Commit string `json:"commit"`
	GOOS   string `json:"goos"`
}

type stabilityReport struct {
	RaceDetector struct {
		Passed bool `json:"passed"`
	} `json:"raceDetector"`
	SPK08WriteLeaseRace struct {
		MeetsMinimum100 bool `json:"meetsMinimum100"`
	} `json:"spk08WriteLeaseRace"`
	AllTenRunsStable bool `json:"allTenRunsStable"`
}

// Run reads every artifact and returns the verdict. The error return is for
// the gate itself being unable to operate (an unreadable repository); a
// missing or bad ARTIFACT is a Finding, not an error, because that is a real
// result the caller must publish rather than a crash.
func Run(in Inputs) (Report, error) {
	report := Report{
		Commit:    in.ExpectedCommit,
		DocsDebt:  in.DocsDebt,
		Platforms: append([]string(nil), Platforms...),
		Scenarios: map[string]map[string]ScenarioOutcome{},
	}

	wantContract, err := contractIdentity(in.RepoRoot, in.ContractVersionPrefix)
	if err != nil {
		return Report{}, fmt.Errorf("recompute contract identity from %s: %w", in.RepoRoot, err)
	}
	report.ContractVersion = wantContract

	// V1-00C zero-debt count. Checked here rather than trusted from an
	// artifact because the gate already has the repository in hand.
	if in.DocsDebt != 0 {
		report.Findings = append(report.Findings, Finding{
			TaskID:  "V1-00C",
			Verdict: VerdictRework,
			Detail:  fmt.Sprintf("documentation coverage debt = %d, want 0", in.DocsDebt),
		})
	}

	evidence := map[string]platformEvidence{}
	for _, platform := range Platforms {
		found, findings := readPlatform(in.EvidenceDir, platform)
		report.Findings = append(report.Findings, findings...)
		if len(findings) == 0 {
			evidence[platform] = found
		}
	}

	report.Findings = append(report.Findings, checkCommits(evidence, in.ExpectedCommit)...)
	report.Findings = append(report.Findings, checkContract(evidence, wantContract)...)
	report.Findings = append(report.Findings, checkJourneyReports(evidence)...)

	scenarioFindings, notes := checkScenarios(evidence, report.Scenarios)
	report.Findings = append(report.Findings, scenarioFindings...)
	report.Notes = append(report.Notes, notes...)

	report.Findings = append(report.Findings, checkStability(in.EvidenceDir)...)

	report.Verdict = verdictFor(report.Findings)
	return report, nil
}

// verdictFor collapses the findings into one answer. Missing evidence
// dominates: when something is both missing and something else merely failed,
// the honest headline is that the picture is incomplete.
func verdictFor(findings []Finding) Verdict {
	verdict := VerdictPass
	for _, f := range findings {
		if f.Verdict == VerdictInsufficientEvidence {
			return VerdictInsufficientEvidence
		}
		verdict = VerdictRework
	}
	return verdict
}

// contractIdentity recomputes the same value the acceptance suite records:
// the contract-format version plus a SHA-256 prefix of the committed golden
// fixture. Recomputing from the checkout is what ties the evidence to THIS
// code; accepting the number the evidence carries would prove only that the
// evidence agrees with itself.
func contractIdentity(repoRoot, prefix string) (string, error) {
	goldenPath := filepath.Join(repoRoot, "internal", "delivery", "httpapi", "apicontract", "testdata", "golden", "contract.json")
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return prefix + "+" + hex.EncodeToString(sum[:])[:16], nil
}

// readPlatform loads one platform's artifact directory.
func readPlatform(evidenceDir, platform string) (platformEvidence, []Finding) {
	dir := filepath.Join(evidenceDir, "v6-acceptance-report-"+platform)
	var out platformEvidence

	if err := readJSON(filepath.Join(dir, "report.json"), &out.report); err != nil {
		return out, []Finding{{
			TaskID:  "V6-14B",
			Verdict: VerdictInsufficientEvidence,
			Detail:  fmt.Sprintf("%s: no readable acceptance report (%v) — the v6-acceptance job for this platform produced no usable evidence", platform, err),
		}}
	}
	if err := readJSON(filepath.Join(dir, "meta.json"), &out.meta); err != nil {
		return out, []Finding{{
			TaskID:  "V6-14B",
			Verdict: VerdictInsufficientEvidence,
			Detail:  fmt.Sprintf("%s: no readable evidence metadata (%v) — without it the revision this evidence describes is unknown", platform, err),
		}}
	}

	scenarios, err := parseTestEvents(filepath.Join(dir, "acceptance.jsonl"))
	if err != nil {
		return out, []Finding{{
			TaskID:  "V6-14A",
			Verdict: VerdictInsufficientEvidence,
			Detail:  fmt.Sprintf("%s: no readable per-scenario results (%v) — the journey's own report cannot stand in for them, since it describes only that one test's stages", platform, err),
		}}
	}
	out.scenarios = scenarios
	return out, nil
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

// testEvent is one `go test -json` record. Only the fields the gate reads are
// declared.
type testEvent struct {
	Action string `json:"Action"`
	Test   string `json:"Test"`
	Output string `json:"Output"`
}

// parseTestEvents reduces a `go test -json` stream to one outcome per
// TOP-LEVEL test. Subtests (names containing "/") are deliberately ignored:
// the journey's own stages are subtests, and they are already covered by its
// structured report; what this gate needs is the scenario set.
func parseTestEvents(path string) (map[string]ScenarioOutcome, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	outcomes := map[string]ScenarioOutcome{}
	skipReasons := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event testEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			continue // a non-JSON line (build noise) is not evidence either way
		}
		if event.Test == "" || strings.Contains(event.Test, "/") {
			continue
		}
		switch event.Action {
		case "output":
			if trimmed := strings.TrimSpace(event.Output); strings.Contains(trimmed, "SKIP:") || strings.Contains(trimmed, "_test.go:") {
				skipReasons[event.Test] = trimmed
			}
		case "pass", "fail", "skip":
			outcomes[event.Test] = ScenarioOutcome{Action: event.Action}
		}
	}
	for name, outcome := range outcomes {
		if outcome.Action == "skip" {
			outcome.Reason = skipReasons[name]
			outcomes[name] = outcome
		}
	}
	if len(outcomes) == 0 {
		return nil, fmt.Errorf("no top-level test results in %s", path)
	}
	return outcomes, nil
}

// checkCommits proves every platform's evidence describes the SAME revision,
// and the one the caller expected. Two platforms that each passed, but on
// different code, are not evidence that any single revision passed anywhere.
func checkCommits(evidence map[string]platformEvidence, expected string) []Finding {
	var findings []Finding
	for _, platform := range Platforms {
		found, ok := evidence[platform]
		if !ok {
			continue // already reported as missing
		}
		if found.meta.Commit == "" {
			findings = append(findings, Finding{
				TaskID:  "V6-14B",
				Verdict: VerdictInsufficientEvidence,
				Detail:  fmt.Sprintf("%s: evidence carries no commit", platform),
			})
			continue
		}
		if expected != "" && found.meta.Commit != expected {
			findings = append(findings, Finding{
				TaskID:  "V6-14B",
				Verdict: VerdictInsufficientEvidence,
				Detail: fmt.Sprintf("%s: evidence was produced from commit %s, not the %s being gated — it describes different code",
					platform, short(found.meta.Commit), short(expected)),
			})
		}
	}
	return findings
}

// checkContract ties the evidence to this checkout's own route set.
func checkContract(evidence map[string]platformEvidence, want string) []Finding {
	var findings []Finding
	for _, platform := range Platforms {
		found, ok := evidence[platform]
		if !ok {
			continue
		}
		if found.report.ContractVersion != want {
			findings = append(findings, Finding{
				TaskID:  "V6-14B",
				Verdict: VerdictInsufficientEvidence,
				Detail: fmt.Sprintf("%s: evidence records contract version %q but this checkout's own golden fixture yields %q — the run did not exercise this API surface",
					platform, found.report.ContractVersion, want),
			})
		}
	}
	return findings
}

// checkJourneyReports covers the happy path's own structured result.
func checkJourneyReports(evidence map[string]platformEvidence) []Finding {
	var findings []Finding
	for _, platform := range Platforms {
		found, ok := evidence[platform]
		if !ok {
			continue
		}
		if !found.report.AllPassed {
			findings = append(findings, Finding{
				TaskID:  "V6-14",
				Verdict: VerdictRework,
				Detail:  fmt.Sprintf("%s: the happy-path journey reports allPassed=false", platform),
			})
		}
		for _, stage := range found.report.Stages {
			if !stage.Passed {
				findings = append(findings, Finding{
					TaskID:  "V6-14",
					Verdict: VerdictRework,
					Detail:  fmt.Sprintf("%s: journey stage %s failed", platform, stage.Name),
				})
			}
		}
	}
	return findings
}

// checkScenarios enforces the closed scenario list. An unknown extra scenario
// is not an error — new coverage is welcome — but a required one that is
// absent, failed or skipped is, and so is a conditional one that skipped
// everywhere.
func checkScenarios(evidence map[string]platformEvidence, index map[string]map[string]ScenarioOutcome) ([]Finding, []string) {
	var findings []Finding
	var notes []string

	record := func(name, platform string, outcome ScenarioOutcome) {
		if index[name] == nil {
			index[name] = map[string]ScenarioOutcome{}
		}
		index[name][platform] = outcome
	}

	for _, name := range requiredScenarios {
		for _, platform := range Platforms {
			found, ok := evidence[platform]
			if !ok {
				continue
			}
			outcome, ran := found.scenarios[name]
			if !ran {
				findings = append(findings, Finding{
					TaskID:  taskFor(name),
					Verdict: VerdictInsufficientEvidence,
					Detail:  fmt.Sprintf("%s: required scenario %s did not run", platform, name),
				})
				continue
			}
			record(name, platform, outcome)
			switch outcome.Action {
			case "pass":
			case "skip":
				findings = append(findings, Finding{
					TaskID:  taskFor(name),
					Verdict: VerdictInsufficientEvidence,
					Detail:  fmt.Sprintf("%s: required scenario %s was skipped, and a skip is not a pass: %s", platform, name, outcome.Reason),
				})
			default:
				findings = append(findings, Finding{
					TaskID:  taskFor(name),
					Verdict: VerdictRework,
					Detail:  fmt.Sprintf("%s: required scenario %s failed", platform, name),
				})
			}
		}
	}

	for _, name := range conditionalScenarios {
		passedSomewhere := false
		observed := 0
		for _, platform := range Platforms {
			found, ok := evidence[platform]
			if !ok {
				continue
			}
			outcome, ran := found.scenarios[name]
			if !ran {
				findings = append(findings, Finding{
					TaskID:  taskFor(name),
					Verdict: VerdictInsufficientEvidence,
					Detail:  fmt.Sprintf("%s: conditional scenario %s did not run", platform, name),
				})
				continue
			}
			observed++
			record(name, platform, outcome)
			switch outcome.Action {
			case "pass":
				passedSomewhere = true
			case "skip":
				notes = append(notes, fmt.Sprintf("%s skipped on %s (allowed on a machine that cannot create the condition): %s", name, platform, outcome.Reason))
			default:
				findings = append(findings, Finding{
					TaskID:  taskFor(name),
					Verdict: VerdictRework,
					Detail:  fmt.Sprintf("%s: conditional scenario %s failed — a skip is tolerated, a failure is not", platform, name),
				})
			}
		}
		if observed > 0 && !passedSomewhere {
			findings = append(findings, Finding{
				TaskID:  taskFor(name),
				Verdict: VerdictInsufficientEvidence,
				Detail: fmt.Sprintf("conditional scenario %s was never demonstrated on ANY platform (skipped on all of them) — the property it exists to prove is unproven, which is missing evidence rather than a pass",
					name),
			})
		}
	}

	sort.Slice(findings, func(i, j int) bool { return findings[i].Detail < findings[j].Detail })
	sort.Strings(notes)
	return findings, notes
}

// taskFor maps a scenario to the task that owns it.
func taskFor(scenario string) string {
	switch {
	case strings.Contains(scenario, "_Fault_"):
		return "V6-14A"
	case strings.HasPrefix(scenario, "TestV6TerminalAcceptance_"):
		return "V6-15P"
	default:
		return "V6-14"
	}
}

// checkStability covers V0-12's own race/stability evidence.
func checkStability(evidenceDir string) []Finding {
	path := filepath.Join(evidenceDir, "v0-12-stability-report", "v0-12-stability-report.json")
	var stability stabilityReport
	if err := readJSON(path, &stability); err != nil {
		return []Finding{{
			TaskID:  "V0-12",
			Verdict: VerdictInsufficientEvidence,
			Detail:  fmt.Sprintf("no readable stability report (%v) — race and 10x-stability results are unknown", err),
		}}
	}
	var findings []Finding
	if !stability.RaceDetector.Passed {
		findings = append(findings, Finding{TaskID: "V0-12", Verdict: VerdictRework, Detail: "race detector did not pass"})
	}
	if !stability.AllTenRunsStable {
		findings = append(findings, Finding{TaskID: "V0-12", Verdict: VerdictRework, Detail: "the 10x offline suite was not stable across all ten runs"})
	}
	if !stability.SPK08WriteLeaseRace.MeetsMinimum100 {
		findings = append(findings, Finding{TaskID: "V0-12", Verdict: VerdictRework, Detail: "SPK-08 write-lease race did not meet its 100-iteration minimum"})
	}
	return findings
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
