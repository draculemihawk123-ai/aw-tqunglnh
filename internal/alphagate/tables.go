package alphagate

import "strings"

// This file holds the three CLOSED tables V8-11 is built on. They are data,
// not logic, and each is guarded by a test that reads the real repository:
// an entry that names a suite CI does not have, or a test that does not
// exist, fails `go test` rather than silently weakening the gate.

// ModulePath is this repository's Go module path, stripped from `go test
// -json` package names so evidence keys are repo-relative directories.
const ModulePath = "github.com/taQuangLing/agent-workflow"

// Suite identifiers are the CI job ids of .github/workflows/spike-gate.yml.
// Their result is GitHub's own `needs.<id>.result` ("success", "failure",
// "cancelled", "skipped"); a matrix job's result already aggregates every
// platform (one failing OS fails the job).
const (
	SuiteContract         = "contract"                    // vet, V1-00C docs coverage, full offline `go test ./...` on both OSes
	SuiteRace             = "linux-race-and-stability"    // V0-12: race detector, suite repeated
	SuiteSpike            = "spike-acceptance"            // SPK-01..14 offline scenarios on both OSes
	SuiteSemanticDiff     = "semantic-diff"               // SPK-13 cross-platform semantic diff
	SuiteV6Acceptance     = "v6-acceptance"               // black-box HTTP/CLI acceptance against real aw processes
	SuiteV6AcceptanceDiff = "v6-acceptance-diff"          // the same evidence compared across platforms
	SuiteV6Gate           = "v6-gate"                     // V6-14C/V6-15P verdict
	SuiteWeb              = "web"                         // typecheck + component tests + build
	SuiteE2E              = "e2e"                         // Playwright full journey from a real browser
	SuiteV8Fault          = "v8-fault-matrix"             // V8-02 crash-boundary matrix
	SuiteV8SoakRace       = "v8-concurrency-soak-race"    // V8-03 soak under -race
	SuiteV8SoakWindows    = "v8-concurrency-soak-windows" // V8-03 soak on Windows
	SuiteV8Security       = "v8-04e-gate"                 // V8-04E security aggregate verdict
	SuiteReleaseBuild     = "release-build"               // V8-08 reproducible build + smoke
)

// AllSuites is every suite the assessment reads, in a stable order.
var AllSuites = []string{
	SuiteContract, SuiteRace, SuiteSpike, SuiteSemanticDiff,
	SuiteV6Acceptance, SuiteV6AcceptanceDiff, SuiteV6Gate,
	SuiteWeb, SuiteE2E,
	SuiteV8Fault, SuiteV8SoakRace, SuiteV8SoakWindows, SuiteV8Security, SuiteReleaseBuild,
}

// baseSuites are required by every version from V0 on: the offline contract
// suite, the race/stability run, and the SPK scenarios (00-roadmap.md §6
// items 2 and 10: `go test`/`go vet` pass and "full offline SPK-01…14 vẫn
// pass, từ V1 trở đi").
var baseSuites = []string{SuiteContract, SuiteRace, SuiteSpike, SuiteSemanticDiff}

// versionSuites maps a version to the suites that are ITS gate (00-roadmap.md
// §4 "Gate sang version sau"), on top of baseSuites. V0–V5 add nothing of
// their own: their gates are the offline suites every version already needs.
var versionSuites = map[string][]string{
	"V6": {SuiteV6Acceptance, SuiteV6AcceptanceDiff, SuiteV6Gate},
	"V7": {SuiteWeb, SuiteE2E},
	"V8": {SuiteV8Fault, SuiteV8SoakRace, SuiteV8SoakWindows, SuiteV8Security, SuiteReleaseBuild, SuiteV6Acceptance},
}

// Versions is the closed list of version gates, in order.
var Versions = []string{"V0", "V1", "V2", "V3", "V4", "V5", "V6", "V7", "V8"}

// SuitesForVersion returns the suites version's gate requires: baseSuites
// plus the version's own.
func SuitesForVersion(version string) []string {
	out := append([]string(nil), baseSuites...)
	return appendUnique(out, versionSuites[version]...)
}

// VersionOfTask returns "V6" for "V6-07A". A malformed id returns "".
func VersionOfTask(taskID string) string {
	dash := strings.IndexByte(taskID, '-')
	if dash < 2 || taskID[0] != 'V' {
		return ""
	}
	return taskID[:dash]
}

// SuiteForPackage names the CI suite a Go/TS test's package runs in. Every
// package not listed runs in `contract` (`go test ./...`).
func SuiteForPackage(dir string) string {
	switch {
	case strings.HasPrefix(dir, "internal/integration/v6accept"):
		return SuiteV6Acceptance
	case strings.HasPrefix(dir, "internal/integration/v8fault"):
		return SuiteV8Fault
	case strings.HasPrefix(dir, "internal/spikeacceptance"):
		return SuiteSpike
	case strings.HasPrefix(dir, "web/e2e"):
		return SuiteE2E
	case strings.HasPrefix(dir, "web/"):
		return SuiteWeb
	default:
		return SuiteContract
	}
}

func appendUnique(list []string, values ...string) []string {
	for _, v := range values {
		found := false
		for _, existing := range list {
			if existing == v {
				found = true
				break
			}
		}
		if !found {
			list = append(list, v)
		}
	}
	return list
}

// TestRef names one Go test by its repo-relative package directory and its
// (possibly slash-qualified subtest) name.
type TestRef struct {
	Pkg  string
	Name string
}

// Key is the evidence-index key "pkg|Name".
func (r TestRef) Key() string { return r.Pkg + "|" + r.Name }

// Journey is one of the system acceptance journeys of
// docs/design/01-system-design.md §13, with the real evidence that backs it.
// Tests must exist in the repository; Suites are whole-suite evidence for a
// journey no single Go test expresses (a browser run, a cross-platform diff).
type Journey struct {
	ID      string
	Summary string
	Tests   []TestRef
	Suites  []string
}

// FinalGate is one of V8-11's mandatory last gates. Exactly one of Tests,
// Suites, Computed is the evidence source (see EvaluateFinalGates).
type FinalGate struct {
	ID      string
	Summary string
	// Tests must each report `pass` (never skip) in the final-gate test run.
	Tests []TestRef
	// Suites must each be `success`.
	Suites []string
	// Computed names a check the gate tool evaluates itself: "sourceref-debt"
	// (the V1-00C checker's violation count must be 0) or "git-diff-check"
	// (the `git diff --check` step's own result).
	Computed string
}

// Computed-check names.
const (
	ComputedSourceRefDebt = "sourceref-debt"
	ComputedGitDiffCheck  = "git-diff-check"
)

// FinalGates is V8-11's closed list (docs/design/10-v8-alpha-hardening.md
// V8-11 "Verify"): cancel-vs-claim both commit orders, route inventory equals
// OpenAPI both ways, every recovery command has core/API/UI/CLI owners, the
// parity inventory has no debt, SourceRef debt is 0, `git diff --check`,
// `go test ./...` and `go vet ./...`.
var FinalGates = []FinalGate{
	{
		ID:      "cancel-vs-claim-both-commit-orders",
		Summary: "CancelRun vs claim: cancel committing first AND running committing first",
		Tests: []TestRef{
			{"internal/app/runtime", "TestCancelRun_ClaimVsCancel_CommitOrder/cancel_commits_first"},
			{"internal/app/runtime", "TestCancelRun_ClaimVsCancel_CommitOrder/running_commits_first"},
		},
	},
	{
		ID:      "route-inventory-equals-openapi-both-directions",
		Summary: "every served route is declared in OpenAPI and every declared operation is served",
		Tests:   []TestRef{{"internal/delivery/httpapi/apicontract", "TestRouteInventory_ServedEqualsDeclaredBothDirections"}},
	},
	{
		ID:      "recovery-commands-have-core-api-ui-cli-owners",
		Summary: "RetryBlockedActivation, CancelWorkItem and ResolveWorkItemBlocker each have a core operation, an HTTP operation, an `aw` leaf and a UI caller",
		Tests:   []TestRef{{"internal/delivery/parity", "TestRecoveryCommandsHaveCoreAPIUICLIOwners"}},
	},
	{
		ID:      "parity-inventory-has-zero-debt",
		Summary: "UI <-> operationId <-> `aw` <-> application operation inventory has no debt (empty ledger)",
		Tests: []TestRef{
			{"internal/delivery/parity", "TestRealInventoryParityGate"},
			{"internal/delivery/parity", "TestParityLedgerIsEmpty"},
		},
	},
	{
		ID:       "sourceref-debt-is-zero",
		Summary:  "the V1-00C coverage checker reports zero SourceRef/coverage violations",
		Computed: ComputedSourceRefDebt,
	},
	{
		ID:       "git-diff-check-clean",
		Summary:  "`git diff --check` reports no whitespace error or conflict marker",
		Computed: ComputedGitDiffCheck,
	},
	{
		ID:      "go-test-and-go-vet-all-packages",
		Summary: "`go vet ./...` and the full offline `go test ./...` pass on both platforms (contract job)",
		Suites:  []string{SuiteContract},
	},
}

// FinalGateTestPackages is every package directory the final-gate test run
// must cover, derived from FinalGates.
func FinalGateTestPackages() []string {
	var pkgs []string
	for _, g := range FinalGates {
		for _, t := range g.Tests {
			pkgs = appendUnique(pkgs, t.Pkg)
		}
	}
	return pkgs
}
