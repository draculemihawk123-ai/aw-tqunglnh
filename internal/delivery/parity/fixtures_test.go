package parity

import (
	"sort"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/apicontract"
)

// The injected-debt fixtures are V6-15O's proof that the checker fails closed
// and is not vacuously green: for EVERY violation class, a copy of the REAL
// inputs is mutated to plant exactly that violation, and the test demands that
//
//  1. the pristine baseline did not already report it (so the detection is
//     caused by the plant, not by pre-existing debt);
//  2. Check reports the planted finding (the exact Class and Subject); and
//  3. the gate as a whole turns red — Evaluate against the real Ledger lists
//     it as a NEW finding.
//
// The unmodified inputs are re-checked after every fixture ("restore"), and
// TestEveryViolationClassHasAFixture closes the loop: a new Class cannot be
// added without a fixture that detects it.

func findCLI(t *testing.T, in *Inputs, path string, scope cli.ScopeKind) int {
	t.Helper()
	for i, d := range in.CLI {
		if strings.Join(d.Path, " ") == path && d.Scope == scope {
			return i
		}
	}
	t.Fatalf("fixture setup: no CLI descriptor %q@%s", path, scope)
	return -1
}

func findHTTP(t *testing.T, in *Inputs, operationID string) int {
	t.Helper()
	for i, op := range in.HTTP.Operations {
		if op.OperationID == operationID {
			return i
		}
	}
	t.Fatalf("fixture setup: no HTTP operation %q", operationID)
	return -1
}

func findRegistry(t *testing.T, in *Inputs, name string) int {
	t.Helper()
	for i, op := range in.Registry {
		if op.Name == name {
			return i
		}
	}
	t.Fatalf("fixture setup: no registry entry %q", name)
	return -1
}

func findUX(t *testing.T, in *Inputs, proposed string) int {
	t.Helper()
	for i, row := range in.UX {
		for _, id := range row.OperationIDs {
			if id == proposed {
				return i
			}
		}
	}
	t.Fatalf("fixture setup: no UX row proposing %q", proposed)
	return -1
}

func removeCLI(in *Inputs, i int) { in.CLI = append(in.CLI[:i:i], in.CLI[i+1:]...) }

func keySet(findings []Finding) map[string]Finding {
	set := make(map[string]Finding, len(findings))
	for _, f := range findings {
		set[f.Key()] = f
	}
	return set
}

type plant struct {
	name    string
	class   Class
	subject string
	mutate  func(t *testing.T, in *Inputs, rules *Rules)
}

func plants() []plant {
	return []plant{
		// ---- missing ------------------------------------------------------
		{"an HTTP operation loses its CLI mirror", ClassMissingCLI, "http:getRunDetail",
			func(t *testing.T, in *Inputs, _ *Rules) { removeCLI(in, findCLI(t, in, "run show", cli.ScopeProject)) }},
		{"a CLI descriptor names a route that is not registered", ClassMissingHTTP, "cli:run graph@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				i := findHTTP(t, in, "getRunGraph")
				in.HTTP.Operations = append(in.HTTP.Operations[:i:i], in.HTTP.Operations[i+1:]...)
			}},
		{"a UX proposal resolves to no route", ClassMissingHTTP, "ux:proposeSomethingNobodyRegistered",
			func(t *testing.T, in *Inputs, _ *Rules) {
				i := findUX(t, in, "getRunGraph")
				in.UX[i].OperationIDs = []string{"proposeSomethingNobodyRegistered"}
			}},
		{"a public registry operation is served by no route", ClassMissingHTTP, "app:GetRunTimeline",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.Registry[findRegistry(t, in, "GetRunTimeline")].HTTP = nil
			}},
		{"a CLI descriptor names an application operation the registry does not know", ClassMissingApp, "cli:run timeline@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				i := findRegistry(t, in, "GetRunTimeline")
				in.Registry = append(in.Registry[:i:i], in.Registry[i+1:]...)
			}},
		{"an HTTP operation is served by no registry entry", ClassMissingApp, "http:getRunTimeline",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.Registry[findRegistry(t, in, "GetRunTimeline")].HTTP = nil
			}},

		// ---- duplicate ----------------------------------------------------
		{"two CLI descriptors share a path and scope", ClassDuplicate, "cli:run show@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				d := in.CLI[findCLI(t, in, "run show", cli.ScopeProject)]
				d.HTTPOperationID = "getRunGraph"
				in.CLI = append(in.CLI, d)
			}},
		{"two CLI descriptors mirror one route", ClassDuplicate, "http:getRunDetail",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI = append(in.CLI, cli.Descriptor{Path: []string{"run", "inspect"}, Scope: cli.ScopeProject, AppOperation: "GetRunDetail", HTTPOperationID: "getRunDetail"})
			}},
		{"two registry entries share a name", ClassDuplicate, "app:GetRunDetail",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.Registry = append(in.Registry, in.Registry[findRegistry(t, in, "GetRunDetail")])
			}},
		{"two registry entries claim one route", ClassDuplicate, "http:getRunDetail",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.Registry = append(in.Registry, PublicOperation{Name: "GetRunDetailAlias", Kind: KindQuery, Exposure: ExposurePublic,
					HTTP: []HTTPBinding{{OperationID: "getRunDetail", Scope: cli.ScopeProject}}, Symbol: "internal/app/runtime.GetRunDetail"})
			}},
		{"the UX inventory gives one operationId two owners", ClassDuplicate, "ux:getRunGraph",
			func(t *testing.T, in *Inputs, _ *Rules) {
				row := in.UX[findUX(t, in, "getRunGraph")]
				row.OwnerTaskID = "V6-99"
				in.UX = append(in.UX, row)
			}},

		// ---- scope --------------------------------------------------------
		{"a descriptor declares the wrong scope", ClassScopeMismatch, "cli:run show@INSTALLATION",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI[findCLI(t, in, "run show", cli.ScopeProject)].Scope = cli.ScopeInstallation
			}},
		{"a route declares the wrong scope", ClassScopeMismatch, "http:getRunDetail",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.HTTP.Operations[findHTTP(t, in, "getRunDetail")].ScopeKind = "INSTALLATION"
			}},
		// The next two isolate the independent detectors: the descriptor-vs-
		// route rule and the descriptor-vs-registry rule must each catch a
		// disagreement the other cannot see (mutation testing showed a
		// single shared plant let either rule be deleted unnoticed).
		{"descriptor and registry agree but the route does not", ClassScopeMismatch, "cli:run show@INSTALLATION",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI[findCLI(t, in, "run show", cli.ScopeProject)].Scope = cli.ScopeInstallation
				in.Registry[findRegistry(t, in, "GetRunDetail")].HTTP[0].Scope = cli.ScopeInstallation
			}},
		{"descriptor and route agree but the registry does not", ClassScopeMismatch, "cli:run show@INSTALLATION",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI[findCLI(t, in, "run show", cli.ScopeProject)].Scope = cli.ScopeInstallation
				in.HTTP.Operations[findHTTP(t, in, "getRunDetail")].ScopeKind = "INSTALLATION"
			}},
		{"the registry binds the wrong scope", ClassScopeMismatch, "cli:run show@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.Registry[findRegistry(t, in, "GetRunDetail")].HTTP[0].Scope = cli.ScopeInstallation
			}},

		// ---- kind ---------------------------------------------------------
		{"a query is registered as a command", ClassKindMismatch, "cli:run show@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.Registry[findRegistry(t, in, "GetRunDetail")].Kind = KindCommand
			}},
		{"a command is served by a GET route", ClassKindMismatch, "http:cancelRun",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.HTTP.Operations[findHTTP(t, in, "cancelRun")].Method = "GET"
			}},
		{"the UX inventory calls a query a command", ClassKindMismatch, "ux:getRunGraph",
			func(t *testing.T, in *Inputs, _ *Rules) { in.UX[findUX(t, in, "getRunGraph")].Kind = "command" }},

		// ---- registry / descriptor disagreement ---------------------------
		{"the registry binds a different route than the descriptor mirrors", ClassAppMismatch, "cli:run show@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.Registry[findRegistry(t, in, "GetRunDetail")].HTTP[0].OperationID = "getRunDetailElsewhere"
			}},

		// ---- internal (worker-only) exposure ------------------------------
		{"a CLI descriptor exposes AdvanceRun", ClassInternalExposed, "cli:run advance@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI = append(in.CLI, cli.Descriptor{Path: []string{"run", "advance"}, Scope: cli.ScopeProject, AppOperation: "AdvanceRun", HTTPOperationID: "getRunDetail"})
			}},
		{"an internal registry operation is bound to a route", ClassInternalExposed, "app:AdvanceRun",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.Registry[findRegistry(t, in, "AdvanceRun")].HTTP = []HTTPBinding{{OperationID: "getRunDetail", Scope: cli.ScopeProject}}
			}},
		{"an HTTP route serves ExecuteWorkspaceReconciliation", ClassInternalExposed, "http:executeReconcile",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.HTTP.Operations = append(in.HTTP.Operations, apicontract.Operation{OperationID: "executeReconcile", Method: "POST", Path: "/projects/{projectId}/reconcile-now", ScopeKind: "PROJECT"})
				in.Registry[findRegistry(t, in, "ExecuteWorkspaceReconciliation")].HTTP = []HTTPBinding{{OperationID: "executeReconcile", Scope: cli.ScopeProject}}
			}},
		// Neither the path nor the AppOperation carries worker vocabulary, so
		// only the registry's own INTERNAL classification can catch this one.
		{"a CLI descriptor exposes an internal operation by its registry entry alone", ClassInternalExposed, "cli:run settle@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI = append(in.CLI, cli.Descriptor{Path: []string{"run", "settle"}, Scope: cli.ScopeProject, AppOperation: "ReconcileMutatingAttempt", HTTPOperationID: "getRunDetail"})
			}},
		{"a CLI path uses scheduler vocabulary", ClassInternalExposed, "cli:run heartbeat@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI = append(in.CLI, cli.Descriptor{Path: []string{"run", "heartbeat"}, Scope: cli.ScopeProject, AppOperation: "GetRunDetail", HTTPOperationID: "getRunGraph"})
			}},

		// ---- remote Git ---------------------------------------------------
		{"a CLI descriptor exposes a remote push", ClassRemoteGitExposed, "cli:release-set push@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI = append(in.CLI, cli.Descriptor{Path: []string{"release-set", "push"}, Scope: cli.ScopeProject, AppOperation: "PushReleaseSet", HTTPOperationID: "sealReleaseSet"})
			}},
		{"an HTTP route exposes a remote pull request", ClassRemoteGitExposed, "http:openPullRequest",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.HTTP.Operations = append(in.HTTP.Operations, apicontract.Operation{OperationID: "openPullRequest", Method: "POST", Path: "/projects/{projectId}/release-sets/{releaseSetId}/pull-request", ScopeKind: "PROJECT"})
			}},
		{"an application operation names a force push", ClassRemoteGitExposed, "app:ForcePushBranch",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.Registry = append(in.Registry, PublicOperation{Name: "ForcePushBranch", Kind: KindCommand, Exposure: ExposurePublic, Symbol: "internal/app/work.CreateReleaseSet"})
			}},

		// ---- CLI_LOCAL closed set -----------------------------------------
		{"a leaf that has an HTTP twin is declared CLI_LOCAL", ClassCLILocalNotAllowed, "cli:settings show@INSTALLATION",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI[findCLI(t, in, "settings show", cli.ScopeInstallation)].HTTPOperationID = cli.CLILocalOperation
			}},
		{"the closed set loses evidence verify", ClassCLILocalNotAllowed, "cli:evidence verify@PROJECT",
			func(t *testing.T, _ *Inputs, rules *Rules) { delete(rules.CLILocalAllowed, "evidence verify") }},

		// ---- confirmation -------------------------------------------------
		{"a high-impact command loses its confirmation", ClassConfirmationMismatch, "cli:run cancel@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI[findCLI(t, in, "run cancel", cli.ScopeProject)].HighImpact = false
			}},
		{"a read command demands confirmation", ClassConfirmationMismatch, "cli:run show@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI[findCLI(t, in, "run show", cli.ScopeProject)].HighImpact = true
			}},
		{"the two scopes of one command disagree on confirmation", ClassConfirmationMismatch, "cli:definition publish",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.CLI[findCLI(t, in, "definition publish", cli.ScopeProject)].HighImpact = false
			}},
		{"the registry stops gating a command the descriptor gates", ClassConfirmationMismatch, "cli:release-set seal@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				i := findRegistry(t, in, "SealReleaseSet")
				in.Registry[i].HighImpact, in.Registry[i].Cite = false, ""
			}},

		// ---- UX reserved invocation shape ---------------------------------
		{"the UX reserves a shape the CLI does not register", ClassUXLeafMismatch, "ux:getRunGraph",
			func(t *testing.T, in *Inputs, _ *Rules) {
				in.UX[findUX(t, in, "getRunGraph")].AwLeaves = [][]string{{"run", "mermaid"}}
			}},
		{"a reviewed rename is withdrawn", ClassUXLeafMismatch, "ux:resolveApproval",
			func(t *testing.T, _ *Inputs, rules *Rules) {
				delete(rules.UXLeafRenames, "approval approve")
				delete(rules.UXLeafRenames, "approval reject")
			}},

		// ---- router -------------------------------------------------------
		{"a descriptor has no route in the router", ClassRouteMissing, "cli:run cancel@PROJECT",
			func(t *testing.T, in *Inputs, _ *Rules) {
				kept := in.Routes[:0:0]
				for _, r := range in.Routes {
					if r != "run cancel" {
						kept = append(kept, r)
					}
				}
				in.Routes = kept
			}},
	}
}

func TestInjectedParityDebtIsDetectedFailClosed(t *testing.T) {
	base := realInputs(t)
	rules := DefaultRules()
	baseline := keySet(Check(base, rules))
	baselineReport := Evaluate(Check(base, rules), Ledger())
	if len(baselineReport.New) != 0 || len(baselineReport.Stale) != 0 {
		t.Fatalf("the pristine inputs must be gate-green before any plant: new=%v stale=%v", baselineReport.New, baselineReport.Stale)
	}

	detected := map[Class]int{}
	for _, p := range plants() {
		t.Run(string(p.class)+"/"+p.name, func(t *testing.T) {
			want := string(p.class) + " " + p.subject
			if _, already := baseline[want]; already {
				t.Fatalf("%s is already reported by the pristine inputs — the fixture would prove nothing", want)
			}

			in := clone(base)
			r := rules
			r.CLILocalAllowed = copyBoolMap(rules.CLILocalAllowed)
			r.UXLeafRenames = copyStringMap(rules.UXLeafRenames)
			p.mutate(t, &in, &r)

			after := Check(in, r)
			if _, ok := keySet(after)[want]; !ok {
				t.Fatalf("planted violation NOT detected: want %q, got:\n%s", want, describe(after))
			}
			// The gate as a whole is red for it.
			report := Evaluate(after, Ledger())
			found := false
			for _, f := range report.New {
				if f.Key() == want {
					found = true
				}
			}
			if !found {
				t.Fatalf("%q was detected but the gate does not list it as NEW debt", want)
			}
			detected[p.class]++
		})
	}

	// Restore: none of the plants leaked into the shared inputs.
	if after := Evaluate(Check(realInputs(t), DefaultRules()), Ledger()); len(after.New) != 0 || len(after.Stale) != 0 {
		t.Fatalf("the restored inputs are no longer gate-green: new=%v stale=%v", after.New, after.Stale)
	}
	for _, c := range AllClasses() {
		if detected[c] == 0 {
			t.Errorf("class %s has no fixture that detects it", c)
		}
	}
}

// TestPlantsAreIndependentOfTheLedger proves a fixture would fail the gate
// even if the ledger were empty or already pinned the finding's class: the
// detection comes from Check, never from the ledger.
func TestEmptyLedgerTurnsEveryPinnedDebtIntoANewFinding(t *testing.T) {
	findings := Check(realInputs(t), DefaultRules())
	report := Evaluate(findings, nil)
	if len(report.New) != len(Ledger()) || report.Debt != len(Ledger()) {
		t.Fatalf("with an empty ledger every pinned debt must be NEW: new=%d debt=%d ledger=%d", len(report.New), report.Debt, len(Ledger()))
	}
	// And a ledger entry with no matching finding is stale.
	stale := Evaluate(findings, append(Ledger(), LedgerEntry{Class: ClassMissingCLI, Subject: "http:nothingHasThisName", Owner: "V6-15O", Reason: "fixture"}))
	if len(stale.Stale) != 1 || stale.Stale[0].Subject != "http:nothingHasThisName" {
		t.Fatalf("a resolved (unmatched) ledger entry must be stale, got %+v", stale.Stale)
	}
}

func TestEveryViolationClassHasAFixture(t *testing.T) {
	covered := map[Class]bool{}
	for _, p := range plants() {
		covered[p.class] = true
	}
	for _, c := range AllClasses() {
		if !covered[c] {
			t.Errorf("violation class %s has no injected-debt fixture", c)
		}
	}
}

func copyBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func describe(findings []Finding) string {
	lines := make([]string, len(findings))
	for i, f := range findings {
		lines[i] = "  " + f.Key() + " — " + f.Detail
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
