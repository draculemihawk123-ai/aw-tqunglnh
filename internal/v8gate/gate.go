// Package v8gate is V8-04E — "Security aggregate gate"
// (docs/design/10-v8-alpha-hardening.md V8-04E: "một verdict duy nhất trên
// bốn suite" — one verdict over the four V8-04A..D security suites),
// verified by "aggregate report; một suite fail làm gate fail" and complete
// when "deny-by-default failures có safe diagnostic/evidence và không suite
// nào bị bỏ qua" (every deny-by-default rejection has safe diagnostic/
// evidence, and no suite is silently skipped).
//
// It is a pure reader, mirroring internal/v6gate's own established shape
// (docs/design/08-v6-api-projections.md V6-14C) deliberately rather than
// inventing a new one: it never runs a test, never starts a process, and
// never writes to the repository. It consumes the `go test -json` evidence
// CI already produces for each of the four suites and emits one verdict.
//
// V8-04A/B/C/D's own real research (this repo's own baocaov8checklist.md)
// found that most of their design-doc scope was already closed by
// pre-existing, already-tested production code — each task's own real,
// new contribution is a SMALL, named set of tests, not a whole dedicated
// package. That is why this gate checks an explicit, closed list of named
// scenarios (scenarios below) rather than "did package X pass" — a shared
// package (v6accept, v5accept, httpapi) passing trivially would not prove
// the specific new security test ever ran; v6gate's own doc comment already
// makes this same "did ran" vs "didn't fail" distinction for its own
// requiredScenarios.
package v8gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Verdict is the gate's single overall answer — the same three-value
// vocabulary v6gate already established, reused verbatim rather than
// inventing a fourth spelling for the same three ideas.
type Verdict string

const (
	VerdictPass                 Verdict = "PASS"
	VerdictRework               Verdict = "REWORK"
	VerdictInsufficientEvidence Verdict = "CHƯA ĐỦ EVIDENCE"
)

// Platforms are the two GOOS targets every V8-04 suite ran on.
var Platforms = []string{"ubuntu-latest", "windows-latest"}

// evidenceSource names which downloaded artifact directory a scenario's own
// `go test -json` stream lives in.
type evidenceSource string

const (
	// sourceV6Acceptance is the EXISTING v6-acceptance-report-<platform>
	// artifact (V6-14B) — internal/integration/v6accept's own full package
	// run already produces acceptance.jsonl on both platforms; V8-04A's own
	// two v6accept-hosted tests ride this for free, no new CI needed.
	sourceV6Acceptance evidenceSource = "v6-acceptance-report"
	// sourceV8SecuritySuite is the NEW v8-04e-evidence-<platform> artifact
	// this task adds (contract job's own new "Security suite evidence"
	// step) — the one genuinely new piece of CI V8-04E requires, since
	// gitworktree/v5accept/httpapi have no existing `-json` evidence
	// artifact of their own.
	sourceV8SecuritySuite evidenceSource = "v8-04e-evidence"
)

// scenario is one named test this gate requires real evidence for.
type scenario struct {
	Name   string
	TaskID string
	Source evidenceSource
	// SkipAllowedOn names platforms where a SKIP is tolerated rather than
	// missing evidence — the one case in this suite,
	// TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected, follows
	// this repo's own established convention
	// (internal/adapters/repoprobe/prober_test.go) of treating "os.Symlink
	// unavailable outside Developer Mode/admin" as a Windows environment
	// limitation, never tolerated as a blanket "skip anywhere" the way
	// v6gate's own conditionalScenarios allows for a genuinely racy
	// property — this one is a deterministic, OS-specific privilege gap,
	// so it is named per-platform, not left open-ended.
	SkipAllowedOn map[string]bool
}

// scenarios is the closed list V8-04E gates on — one entry per real,
// genuinely new (or, for V8-04C, representative pre-existing) test each of
// V8-04A..D's own checklist entry names as its own load-bearing evidence.
var scenarios = []scenario{
	// V8-04A — internal/integration/v6accept/filesystem_path_abuse_test.go
	{Name: "TestV8PathAbuse_RepositoryNestedUnderWorkspaceRoot_Rejected", TaskID: "V8-04A", Source: sourceV6Acceptance},
	{Name: "TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected", TaskID: "V8-04A", Source: sourceV6Acceptance, SkipAllowedOn: map[string]bool{"windows-latest": true}},
	// V8-04A — internal/adapters/gitworktree/provider_test.go
	{Name: "TestProviderRelease_BystanderSiblingUnderManagedRootSurvives", TaskID: "V8-04A", Source: sourceV8SecuritySuite},

	// V8-04B — internal/integration/v5accept (isolation-profile-lie and
	// adapter-build-drift already existed from V5-15C; multi-repository
	// write is V8-04B's own real addition; all three are this task's own
	// load-bearing evidence per its checklist entry).
	{Name: "TestV5AcceptIsolationUnavailable_RealAdmissionRejectsBeforeSpawn", TaskID: "V8-04B", Source: sourceV8SecuritySuite},
	{Name: "TestV5AcceptAdapterDrift_RealAdmissionRejectsMismatchedPin", TaskID: "V8-04B", Source: sourceV8SecuritySuite},
	{Name: "TestV5AcceptMultiRepositoryWriteWithoutGrant_RealAdmissionRejectsBeforeSpawn", TaskID: "V8-04B", Source: sourceV8SecuritySuite},

	// V8-04C — no new test was written (verification-only closure); these
	// are the representative pre-existing V6-13 tests its own checklist
	// entry cites as the real, load-bearing coverage for each of its 6
	// design-doc scenarios plus its own "token never in URL/log" bar.
	{Name: "TestLoopbackOnlyBind", TaskID: "V8-04C", Source: sourceV8SecuritySuite},
	{Name: "TestTransportGuardMatrix_EveryRoute", TaskID: "V8-04C", Source: sourceV8SecuritySuite},
	{Name: "TestCORSPreflightIsNeverAnswered", TaskID: "V8-04C", Source: sourceV8SecuritySuite},
	{Name: "TestArtifactContentMediaHandling", TaskID: "V8-04C", Source: sourceV8SecuritySuite},
	{Name: "TestOversizedBodyIsRefusedBeforeAnyHandlerRuns", TaskID: "V8-04C", Source: sourceV8SecuritySuite},
	{Name: "TestSecretScan_TokenNeverAppearsInLogOutput", TaskID: "V8-04C", Source: sourceV8SecuritySuite},
	{Name: "TestSecretScan_BootstrapHTMLNeverPutsTokenInAURLOrQueryString", TaskID: "V8-04C", Source: sourceV8SecuritySuite},

	// V8-04D — internal/integration/v5accept/retained_data_secret_scan_test.go
	{Name: "TestV5AcceptRetainedDataSecretScan_RealSecretNeverPersistedUnredacted", TaskID: "V8-04D", Source: sourceV8SecuritySuite},
}

// Finding is one thing the gate objects to — the same shape as v6gate's own
// Finding, so a reader familiar with the V6-14C verdict needs no new mental
// model to read this one.
type Finding struct {
	TaskID  string  `json:"taskId"`
	Verdict Verdict `json:"verdict"`
	Detail  string  `json:"detail"`
}

// ScenarioOutcome is one scenario's result on one platform.
type ScenarioOutcome struct {
	Action string `json:"action"` // pass | fail | skip
	Reason string `json:"reason,omitempty"`
}

// Report is the evidence index plus the verdict, written as this gate's own
// durable artifact.
type Report struct {
	Verdict   Verdict                               `json:"verdict"`
	Commit    string                                `json:"commit"`
	Platforms []string                              `json:"platforms"`
	Suites    []string                              `json:"suites"`
	Scenarios map[string]map[string]ScenarioOutcome `json:"scenarios"`
	Findings  []Finding                             `json:"findings"`
	Notes     []string                              `json:"notes,omitempty"`
}

// Inputs is everything Run reads.
type Inputs struct {
	// EvidenceDir holds the downloaded CI artifacts, one directory per
	// artifact name (v6-acceptance-report-<os>/, v8-04e-evidence-<os>/).
	EvidenceDir string
	// ExpectedCommit, when set, is the revision the evidence must belong to.
	ExpectedCommit string
}

type platformEvidence struct {
	commit    string
	scenarios map[string]ScenarioOutcome
}

type evidenceMeta struct {
	Commit string `json:"commit"`
	GOOS   string `json:"goos"`
}

// Run reads every artifact and returns the verdict. The error return is for
// the gate itself being unable to operate; a missing or bad ARTIFACT is a
// Finding, not an error, because that is a real result the caller must
// publish rather than a crash.
func Run(in Inputs) (Report, error) {
	suiteSet := map[string]struct{}{}
	for _, s := range scenarios {
		suiteSet[s.TaskID] = struct{}{}
	}
	suites := make([]string, 0, len(suiteSet))
	for suite := range suiteSet {
		suites = append(suites, suite)
	}
	sort.Strings(suites)

	report := Report{
		Commit:    in.ExpectedCommit,
		Platforms: append([]string(nil), Platforms...),
		Suites:    suites,
		Scenarios: map[string]map[string]ScenarioOutcome{},
	}

	// Load each (source, platform) pair's evidence at most once, even
	// though several scenarios share one source.
	loaded := map[evidenceSource]map[string]platformEvidence{}
	loadSource := func(source evidenceSource) map[string]platformEvidence {
		if cached, ok := loaded[source]; ok {
			return cached
		}
		byPlatform := map[string]platformEvidence{}
		for _, platform := range Platforms {
			found, findings := readPlatform(in.EvidenceDir, string(source), platform)
			report.Findings = append(report.Findings, findings...)
			if len(findings) == 0 {
				byPlatform[platform] = found
			}
		}
		loaded[source] = byPlatform
		return byPlatform
	}

	var notes []string
	for _, s := range scenarios {
		byPlatform := loadSource(s.Source)
		for _, platform := range Platforms {
			found, ok := byPlatform[platform]
			if !ok {
				continue // already reported as missing by readPlatform
			}
			if in.ExpectedCommit != "" && found.commit != "" && found.commit != in.ExpectedCommit {
				report.Findings = append(report.Findings, Finding{
					TaskID:  s.TaskID,
					Verdict: VerdictInsufficientEvidence,
					Detail: fmt.Sprintf("%s/%s: evidence was produced from commit %s, not the %s being gated — it describes different code",
						s.Source, platform, short(found.commit), short(in.ExpectedCommit)),
				})
				continue
			}
			outcome, ran := found.scenarios[s.Name]
			if !ran {
				report.Findings = append(report.Findings, Finding{
					TaskID:  s.TaskID,
					Verdict: VerdictInsufficientEvidence,
					Detail:  fmt.Sprintf("%s: required scenario %s did not run", platform, s.Name),
				})
				continue
			}
			if report.Scenarios[s.Name] == nil {
				report.Scenarios[s.Name] = map[string]ScenarioOutcome{}
			}
			report.Scenarios[s.Name][platform] = outcome
			switch outcome.Action {
			case "pass":
			case "skip":
				if s.SkipAllowedOn[platform] {
					notes = append(notes, fmt.Sprintf("%s: %s skipped on %s (allowed environment limitation): %s", s.TaskID, s.Name, platform, outcome.Reason))
					continue
				}
				report.Findings = append(report.Findings, Finding{
					TaskID:  s.TaskID,
					Verdict: VerdictInsufficientEvidence,
					Detail:  fmt.Sprintf("%s: required scenario %s was skipped, and a skip is not a pass: %s", platform, s.Name, outcome.Reason),
				})
			default:
				report.Findings = append(report.Findings, Finding{
					TaskID:  s.TaskID,
					Verdict: VerdictRework,
					Detail:  fmt.Sprintf("%s: required scenario %s failed", platform, s.Name),
				})
			}
		}
	}

	sort.Slice(report.Findings, func(i, j int) bool {
		if report.Findings[i].TaskID != report.Findings[j].TaskID {
			return report.Findings[i].TaskID < report.Findings[j].TaskID
		}
		return report.Findings[i].Detail < report.Findings[j].Detail
	})
	sort.Strings(notes)
	report.Notes = notes

	report.Verdict = verdictFor(report.Findings)
	return report, nil
}

// verdictFor collapses the findings into one answer. Missing evidence
// dominates: when something is both missing and something else merely
// failed, the honest headline is that the picture is incomplete — the same
// rule v6gate's own verdictFor already establishes.
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

// readPlatform loads one (source, platform) artifact directory — mirrors
// v6gate's own readPlatform, generalized to a caller-chosen artifact-name
// prefix since this gate reads from two DIFFERENT artifact families, not
// just one.
func readPlatform(evidenceDir, sourcePrefix, platform string) (platformEvidence, []Finding) {
	dir := filepath.Join(evidenceDir, sourcePrefix+"-"+platform)
	var out platformEvidence

	var meta evidenceMeta
	if err := readJSON(filepath.Join(dir, "meta.json"), &meta); err != nil {
		return out, []Finding{{
			TaskID:  "V8-04E",
			Verdict: VerdictInsufficientEvidence,
			Detail:  fmt.Sprintf("%s/%s: no readable evidence metadata (%v) — without it the revision this evidence describes is unknown", sourcePrefix, platform, err),
		}}
	}
	out.commit = meta.Commit

	scenarios, err := parseTestEvents(filepath.Join(dir, jsonlFileFor(sourcePrefix)))
	if err != nil {
		return out, []Finding{{
			TaskID:  "V8-04E",
			Verdict: VerdictInsufficientEvidence,
			Detail:  fmt.Sprintf("%s/%s: no readable per-scenario results (%v)", sourcePrefix, platform, err),
		}}
	}
	out.scenarios = scenarios
	return out, nil
}

// jsonlFileFor names the `go test -json` stream file each artifact family
// carries — the two CI steps that produce these chose their own file names
// independently (acceptance.jsonl predates this gate by a whole phase),
// so this mapping is the one place that difference is absorbed.
func jsonlFileFor(sourcePrefix string) string {
	if sourcePrefix == string(sourceV6Acceptance) {
		return "acceptance.jsonl"
	}
	return "security-evidence.jsonl"
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

// testEvent is one `go test -json` record. Only the fields the gate reads
// are declared.
type testEvent struct {
	Action string `json:"Action"`
	Test   string `json:"Test"`
	Output string `json:"Output"`
}

// parseTestEvents reduces a `go test -json` stream to one outcome per
// TOP-LEVEL test — the identical reduction v6gate's own parseTestEvents
// already performs (subtests, names containing "/", are ignored: none of
// this gate's own named scenarios are ever addressed as a subtest).
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

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
