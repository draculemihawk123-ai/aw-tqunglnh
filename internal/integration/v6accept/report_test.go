// report_test.go is V6-14B's own structured, OS-normalized result summary
// (docs/design/08-v6-api-projections.md V6-14B: "same semantic acceptance
// suite passes on Windows AND Linux ... normalized result diff between the
// two platforms"). It does not add a new journey or change any of the
// EXISTING 14 stages' own logic (journey_test.go and the stage_*.go files):
// it only observes and records around them, by instrumenting journey_test.go's
// own stage() helper (pass/fail + duration per stage) and by capturing a
// handful of real, end-of-journey counts once the journey has proven the
// installation healthy.
//
// The report this file writes is what makes ".github/workflows/spike-gate.yml"'s
// own v6-acceptance-diff job's cross-platform comparison possible: comparing
// raw logs would fail on absolute paths, ports, PIDs and timestamps that
// legitimately differ between two real machines even when the two runs are
// semantically identical. This report carries only OS-independent content
// (stage names/pass state, real counts, the real contract version) so the CI
// comparison step can diff it field-by-field instead.
package v6accept

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/apicontract"
)

// reportPathEnvVar names the file the structured result summary is written
// to. The CI job (.github/workflows/spike-gate.yml, v6-acceptance job) sets
// it to an OS-distinguishing path before running `go test`; a standalone
// `AW_HTTP_ACCEPTANCE=1 go test ./internal/integration/v6accept/...` run
// (no env var set) still produces a real file, at the sane local default
// below, so the mechanism is never silently inert and always exercisable
// without CI.
const reportPathEnvVar = "AW_V6_REPORT_PATH"

// defaultReportPath is used when reportPathEnvVar is unset. It deliberately
// lives outside the repository tree (os.TempDir(), never the working
// directory `go test` runs from) so an ordinary local run can never leave a
// stray report file for `git status` to notice.
func defaultReportPath() string {
	return filepath.Join(os.TempDir(), "aw-v6-acceptance-report.json")
}

// stageResult is one named stage's real, observed outcome.
type stageResult struct {
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	DurationMs int64  `json:"durationMs"`
}

// v6Report is the whole structured, OS-normalized result summary a
// top-level acceptance test in this package writes when it finishes (or is
// cut short by a real failure — see writeReport's own doc comment).
type v6Report struct {
	GOOS            string         `json:"goos"`
	ContractVersion string         `json:"contractVersion"`
	Stages          []stageResult  `json:"stages"`
	FinalCounts     map[string]int `json:"finalCounts"`
	AllPassed       bool           `json:"allPassed"`
}

// reportsMu guards reports, a package-level accumulator keyed by the
// TOP-LEVEL test name (the *testing.T stage() itself is called with is
// always the root test's own T — e.g. "TestV6HTTPAcceptance_CleanDatabaseJourney",
// never a subtest's dotted name, since journey_test.go's stage() receives
// the outer `t` parameter of TestV6HTTPAcceptance_CleanDatabaseJourney
// itself). Keying by root test name means a second, independent top-level
// test in this package that also calls stage() (for example V6-14A's own
// fault-injection matrix, landing separately on its own branch) accumulates
// into its own report rather than colliding with this one — no coordination
// between the two tasks is required.
var (
	reportsMu sync.Mutex
	reports   = map[string]*v6Report{}
)

// reportFor returns (creating if needed) the named root test's accumulator.
// Callers must hold reportsMu.
func reportFor(rootName string) *v6Report {
	r, ok := reports[rootName]
	if !ok {
		r = &v6Report{GOOS: runtime.GOOS, FinalCounts: map[string]int{}}
		reports[rootName] = r
	}
	return r
}

// recordStage appends one stage's outcome to rootName's report. Called from
// journey_test.go's own stage() helper — the one place every one of the 14
// stages already passes through, so no individual stage function needed to
// change.
func recordStage(rootName, stageName string, passed bool, duration time.Duration) {
	reportsMu.Lock()
	defer reportsMu.Unlock()
	r := reportFor(rootName)
	r.Stages = append(r.Stages, stageResult{Name: stageName, Passed: passed, DurationMs: duration.Milliseconds()})
}

// setFinalCount records one real, end-of-journey count.
func setFinalCount(rootName, key string, value int) {
	reportsMu.Lock()
	defer reportsMu.Unlock()
	reportFor(rootName).FinalCounts[key] = value
}

// writeReport finalizes and writes rootName's report. AllPassed reflects
// both every recorded stage's own pass state AND whether the top-level test
// itself ever failed outside a stage (t.Failed() covers both), so a real
// failure never silently reports allPassed:true. The report is written even
// when incomplete — fewer than the full stage count recorded, because a
// stage() call itself never returned — which is exactly how the CI
// comparison step later tells a REAL recorded failure (a report file with
// allPassed:false, or a short stage list) apart from missing evidence
// altogether (no report file at all, e.g. the binaries never even built).
func writeReport(t *testing.T, rootName string) {
	t.Helper()
	reportsMu.Lock()
	r := reportFor(rootName)
	cp := *r
	cp.Stages = append([]stageResult(nil), r.Stages...)
	cp.FinalCounts = make(map[string]int, len(r.FinalCounts))
	for k, v := range r.FinalCounts {
		cp.FinalCounts[k] = v
	}
	reportsMu.Unlock()

	cp.ContractVersion = realContractVersion(t)
	allStagesPassed := true
	for _, s := range cp.Stages {
		if !s.Passed {
			allStagesPassed = false
			break
		}
	}
	cp.AllPassed = allStagesPassed && !t.Failed()

	path := resolveReportPath(t, os.Getenv(reportPathEnvVar))
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		t.Errorf("marshal v6 acceptance report: %v", err)
		return
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Errorf("mkdir report dir %s: %v", dir, err)
			return
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Errorf("write v6 acceptance report %s: %v", path, err)
		return
	}
	t.Logf("wrote v6 acceptance report (allPassed=%v) to %s:\n%s", cp.AllPassed, path, string(data))
}

// resolveReportPath turns the raw reportPathEnvVar value into the absolute
// file the report is actually written to.
//
// An empty value means the local default (a file outside the repository
// tree). A RELATIVE value is resolved against the MODULE ROOT, not against
// the process' working directory: `go test` runs a test binary with the
// package's own directory as the working directory, so a caller that sets
// AW_V6_REPORT_PATH=v6-report/report.json (the natural thing for a CI step
// whose own shell commands run at the repository root) would otherwise
// silently write the report to internal/integration/v6accept/v6-report/ —
// a real file, in the wrong place, which a repo-root `upload-artifact`
// step then cannot see. That exact mismatch shipped in V6-14B's first CI
// run: both platforms' suites passed and uploaded an artifact containing
// only acceptance.log, and the cross-platform diff job two jobs later
// reported it as "CHƯA ĐỦ EVIDENCE". Resolving here makes a relative path
// mean what the person writing the CI step meant; an absolute value (what
// the workflow now passes) is used exactly as given.
func resolveReportPath(t *testing.T, raw string) string {
	t.Helper()
	if raw == "" {
		return defaultReportPath()
	}
	if filepath.IsAbs(raw) {
		return raw
	}
	root, err := moduleRoot()
	if err != nil {
		t.Logf("report path: moduleRoot: %v (using %s relative to the test's own working directory)", err, raw)
		return raw
	}
	return filepath.Join(root, raw)
}

// TestResolveReportPath_RelativeIsAnchoredAtModuleRoot is the cheap,
// always-run guard on the mismatch described above: it needs no real
// processes and no AW_HTTP_ACCEPTANCE, so it runs in the ordinary offline
// suite rather than only in the acceptance job it protects.
func TestResolveReportPath_RelativeIsAnchoredAtModuleRoot(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Skipf("moduleRoot: %v", err)
	}

	got := resolveReportPath(t, filepath.Join("v6-report", "report.json"))
	want := filepath.Join(root, "v6-report", "report.json")
	if got != want {
		t.Errorf("relative report path = %q, want %q (anchored at the module root, not the package directory `go test` runs in)", got, want)
	}

	absolute := filepath.Join(t.TempDir(), "report.json")
	if got := resolveReportPath(t, absolute); got != absolute {
		t.Errorf("absolute report path = %q, want it used verbatim (%q)", got, absolute)
	}

	if got := resolveReportPath(t, ""); got != defaultReportPath() {
		t.Errorf("empty report path = %q, want the local default %q", got, defaultReportPath())
	}
}

// realContractVersion returns the SAME contract version identity
// TestContract_MatchesGoldenFixture (internal/delivery/httpapi/apicontract/
// golden_test.go) asserts a freshly-built, real production Contract against:
// apicontract.ContractVersion (that package's own exported artifact-format
// version constant, contract.go) plus a SHA-256 of the committed golden
// fixture's own bytes (testdata/golden/contract.json) — the exact byte
// sequence that test proves equals apicontract.Build's output for the
// CURRENT real route composition (internal/delivery/httpcompose.ComposeRoutes).
// Reading this checked-in fixture, rather than re-running the full
// production route-composition wiring a second time inside this black-box
// suite (which would need a real temporary SQLite database, artifact store
// and git worktree provider just to compute a version string), keeps this
// package's own "never call a handler in-process" discipline intact while
// still reporting a value that is real — byte-for-byte the committed
// fixture, never invented — and that changes exactly when the route set the
// golden test itself guards changes (a stale, un-regenerated fixture would
// already fail "Offline contract suite" in the contract CI job, so by the
// time this suite runs in the SAME CI pipeline the fixture is known-current).
func realContractVersion(t *testing.T) string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Logf("contract version: moduleRoot: %v (falling back to apicontract.ContractVersion alone)", err)
		return apicontract.ContractVersion
	}
	goldenPath := filepath.Join(root, "internal", "delivery", "httpapi", "apicontract", "testdata", "golden", "contract.json")
	data, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Logf("contract version: read golden fixture %s: %v (falling back to apicontract.ContractVersion alone)", goldenPath, err)
		return apicontract.ContractVersion
	}
	sum := sha256.Sum256(data)
	return apicontract.ContractVersion + "+" + hex.EncodeToString(sum[:])[:16]
}

// captureFinalCounts records a handful of real, end-of-journey counts —
// docs/design/08-v6-api-projections.md V6-14B's own "finalCounts should
// come from real end-of-journey queries the journey already makes
// reachable" guidance. It is called once, directly from
// TestV6HTTPAcceptance_CleanDatabaseJourney between stage 13
// (restart_equality) and stage 14 (graceful_shutdown) — while both real
// processes are still up — and is deliberately NOT itself wrapped in
// stage(): it is a reporting-only observation, not a 15th assertion stage,
// so it never renumbers or restructures the existing 14.
func (j *journey) captureFinalCounts(t *testing.T) {
	t.Helper()
	rootName := t.Name()
	api := j.s.api

	var workItems struct {
		Items []json.RawMessage `json:"items"`
	}
	api.get(t, "/projects/"+j.projectID+"/work-items").requireStatus(t, http.StatusOK).decode(t, &workItems)
	setFinalCount(rootName, "workItems", len(workItems.Items))

	runs := 0
	for _, id := range []string{j.runID, j.releaseRunID} {
		if id != "" {
			runs++
		}
	}
	setFinalCount(rootName, "runs", runs)

	var evidenceList struct {
		Items []json.RawMessage `json:"items"`
	}
	api.get(t, "/projects/"+j.projectID+"/work-items/"+j.childWorkItemID+"/evidence").requireStatus(t, http.StatusOK).decode(t, &evidenceList)
	setFinalCount(rootName, "evidence", len(evidenceList.Items))

	events := j.eventTrace(t)
	setFinalCount(rootName, "domainEvents", len(events))
}
