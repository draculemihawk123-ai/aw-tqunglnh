package parity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// TestRealInventoryParityGate is V6-15O's own verdict over the REAL tree: the
// real UX document, the real composed HTTP routes, the public operation
// registry and every CLI descriptor the composed leaves registered, checked
// in both directions.
//
// It fails when Check reports any finding the Ledger does not pin (a NEW
// violation — debt can never grow silently) and when a Ledger entry matches
// no finding (a resolved debt must have its entry deleted, so the ledger can
// only shrink). What it deliberately does NOT assert is Report.Debt == 0:
// the ledger records debt the design forbids this task to close (see
// Ledger's own doc comment); V6-15P's terminal gate is where zero is
// required, and this test logs the exact number it inherits.
func TestRealInventoryParityGate(t *testing.T) {
	findings := Check(realInputs(t), DefaultRules())
	report := Evaluate(findings, Ledger())

	for _, f := range report.New {
		t.Errorf("NEW parity debt: %s — %s", f.Key(), f.Detail)
	}
	for _, e := range report.Stale {
		t.Errorf("stale ledger entry (its debt is closed — delete it): %s (owner %s)", e.Key(), e.Owner)
	}
	t.Logf("parity debt = %d (all acknowledged in Ledger(); V6-15P requires 0): %d MISSING_CLI/HTTP/APP + CLI_LOCAL entries pinned",
		report.Debt, len(report.Acknowledged))
	if report.Debt != len(Ledger()) {
		t.Errorf("Debt = %d but the ledger pins %d entries", report.Debt, len(Ledger()))
	}
}

func TestLedgerEntriesAreOwnedAndExplained(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Ledger() {
		if e.Owner == "" || strings.TrimSpace(e.Reason) == "" {
			t.Errorf("ledger entry %s needs an owner task and a reason (V6-00: every gap has a responsible Task ID)", e.Key())
		}
		if seen[e.Key()] {
			t.Errorf("duplicate ledger entry %s", e.Key())
		}
		seen[e.Key()] = true
		known := false
		for _, c := range AllClasses() {
			if c == e.Class {
				known = true
			}
		}
		if !known {
			t.Errorf("ledger entry %s uses an unknown class", e.Key())
		}
	}
}

// TestRealInventoryHasNoDebtOfTheSafetyClasses pins the classes whose
// presence would be a safety problem rather than a completeness one: no
// scope/kind/confirmation disagreement, no duplicate authority, no internal or
// remote-Git exposure, no unreachable descriptor and no registry/descriptor
// disagreement. These are asserted independent of the ledger, so a ledger
// edit can never hide one.
func TestRealInventoryHasNoDebtOfTheSafetyClasses(t *testing.T) {
	forbiddenInLedger := map[Class]bool{
		ClassScopeMismatch: true, ClassKindMismatch: true, ClassConfirmationMismatch: true, ClassDuplicate: true,
		ClassInternalExposed: true, ClassRemoteGitExposed: true, ClassRouteMissing: true, ClassAppMismatch: true,
		ClassUXLeafMismatch: true,
	}
	for _, f := range Check(realInputs(t), DefaultRules()) {
		if forbiddenInLedger[f.Class] {
			t.Errorf("%s — %s", f.Key(), f.Detail)
		}
	}
	for _, e := range Ledger() {
		if forbiddenInLedger[e.Class] {
			t.Errorf("the ledger may never pin a %s finding: %s", e.Class, e.Key())
		}
	}
}

// ---- closed sets ----------------------------------------------------------

func TestCLILocalClosedSetIsExactlyTheDesignedOne(t *testing.T) {
	want := []string{"evidence verify", "help", "serve", "version", "worker"}
	var got []string
	for path := range DefaultRules().CLILocalAllowed {
		got = append(got, path)
	}
	sort.Strings(got)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("CLI_LOCAL closed set = %v, want %v (docs/design/08-v6-api-projections.md V6-15O)", got, want)
	}
}

// TestEveryRealCLILocalDescriptorIsInTheClosedSetOrLedgered: the only
// CLI_LOCAL descriptors that exist are `evidence verify` (in the set) and the
// two `definition list` ones (ledgered until V6-05 adds the route).
func TestEveryRealCLILocalDescriptorIsInTheClosedSetOrLedgered(t *testing.T) {
	rules := DefaultRules()
	ledgered := map[string]bool{}
	for _, e := range Ledger() {
		ledgered[e.Key()] = true
	}
	for _, d := range cli.All() {
		if d.HTTPOperationID != cli.CLILocalOperation {
			continue
		}
		path := strings.Join(d.Path, " ")
		if rules.CLILocalAllowed[path] {
			continue
		}
		if !ledgered[string(ClassCLILocalNotAllowed)+" "+cliSubject(d)] {
			t.Errorf("CLI_LOCAL descriptor %q is neither in the closed set nor ledgered", path)
		}
	}
}

// ---- the registry ---------------------------------------------------------

func TestRegistryIsInternallyConsistent(t *testing.T) {
	names := map[string]bool{}
	routes := map[string]string{}
	for _, op := range PublicOperations() {
		if op.Name == "" {
			t.Error("registry entry with an empty name")
		}
		if names[op.Name] {
			t.Errorf("duplicate registry name %q", op.Name)
		}
		names[op.Name] = true
		if op.Kind != KindCommand && op.Kind != KindQuery {
			t.Errorf("%s: invalid kind %q", op.Name, op.Kind)
		}
		for _, b := range op.HTTP {
			if other, dup := routes[b.OperationID]; dup {
				t.Errorf("%s and %s both bind %q", op.Name, other, b.OperationID)
			}
			routes[b.OperationID] = op.Name
			if b.Scope != cli.ScopeInstallation && b.Scope != cli.ScopeProject {
				t.Errorf("%s binds %q with an invalid scope %q", op.Name, b.OperationID, b.Scope)
			}
		}
		switch op.Exposure {
		case ExposurePublic, ExposureInternal:
			if op.Symbol == "" {
				t.Errorf("%s (%s) must name the real symbol that implements it", op.Name, op.Exposure)
			}
		case ExposureLocal, ExposureHealth, ExposureStream, ExposureProjectionRead:
			if op.Symbol != "" {
				t.Errorf("%s (%s) names no application function, so it must not claim a Symbol", op.Name, op.Exposure)
			}
		default:
			t.Errorf("%s: unknown exposure %q", op.Name, op.Exposure)
		}
		if op.Exposure == ExposureInternal && len(op.HTTP) != 0 {
			t.Errorf("internal operation %s must not be bound to any route", op.Name)
		}
		if op.HighImpact != (op.Cite != "") {
			t.Errorf("%s: HighImpact and Cite must be set together (the marker is only as good as its UX citation)", op.Name)
		}
	}
	for _, internal := range []string{"AdvanceRun", "ExecuteWorkspaceReconciliation", "ExecuteWorkspaceSetRelease"} {
		if !names[internal] {
			t.Errorf("ADR-028 names %s as an internal command that must never be exposed; the registry must list it", internal)
		}
	}
}

// TestRegistrySymbolsExist proves the registry cannot invent an operation:
// every Symbol ("internal/app/<pkg>.<Func>" or ".<Type>.<Method>") is parsed
// out of the real source tree.
func TestRegistrySymbolsExist(t *testing.T) {
	symbols := exportedAppSymbols(t)
	if len(symbols) < 500 {
		t.Fatalf("only %d exported application symbols parsed — the walk root is wrong", len(symbols))
	}
	for _, op := range PublicOperations() {
		if op.Symbol == "" {
			continue
		}
		if !symbols[op.Symbol] {
			t.Errorf("%s: symbol %q does not exist in internal/app", op.Name, op.Symbol)
		}
	}
}

// exportedAppSymbols parses every non-test Go file under internal/app and
// returns the set of exported "path.Func" and "path.Type.Method" identifiers.
func exportedAppSymbols(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Join("..", "..", "app")
	symbols := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		pkg := "internal/app/" + filepath.ToSlash(strings.TrimPrefix(filepath.Dir(path), root+string(filepath.Separator)))
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			if fn.Recv == nil || len(fn.Recv.List) == 0 {
				symbols[pkg+"."+fn.Name.Name] = true
				continue
			}
			recv := fn.Recv.List[0].Type
			if star, isStar := recv.(*ast.StarExpr); isStar {
				recv = star.X
			}
			if ident, isIdent := recv.(*ast.Ident); isIdent {
				symbols[pkg+"."+ident.Name+"."+fn.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return symbols
}

// TestHighImpactCitationsExistInTheUXDoc: the confirmation marker has ONE
// authoritative source, the UX document (HE-04-M07). Every HighImpact
// registry entry cites the phrase there that puts a confirmation dialog in
// front of it; if the document stops saying so, this fails and forces a
// review of the marker.
func TestHighImpactCitationsExistInTheUXDoc(t *testing.T) {
	doc := readUXDoc(t)
	highImpact := 0
	for _, op := range PublicOperations() {
		if !op.HighImpact {
			continue
		}
		highImpact++
		if !strings.Contains(doc, op.Cite) {
			t.Errorf("%s cites %q, which docs/design/11-v6-00-ux-artifact.md no longer contains", op.Name, op.Cite)
		}
	}
	if highImpact != 8 {
		t.Errorf("%d high-impact operations, want the 8 the UX inventory gates (publish, register adapter, cancel run, cancel task, release workspace set, seal, abandon, local commit)", highImpact)
	}
}

// TestRegistryHighImpactEqualsDescriptorHighImpact is the confirmation
// dimension of the four-way check on the real tree, phrased as a set
// equality so a failure names both sides.
func TestRegistryHighImpactEqualsDescriptorHighImpact(t *testing.T) {
	fromRegistry := map[string]bool{}
	for _, op := range PublicOperations() {
		if op.HighImpact {
			fromRegistry[op.Name] = true
		}
	}
	fromCLI := map[string]bool{}
	for _, d := range cli.All() {
		if d.HighImpact {
			fromCLI[d.AppOperation] = true
		}
	}
	for name := range fromRegistry {
		if !fromCLI[name] {
			t.Errorf("registry marks %s high-impact but no CLI descriptor requires confirmation for it", name)
		}
	}
	for name := range fromCLI {
		if !fromRegistry[name] {
			t.Errorf("a CLI descriptor requires confirmation for %s but the UX-cited registry does not gate it", name)
		}
	}
}

// TestUXProposalRenamesAreLive: every reviewed rename target is a real route,
// so the table cannot rot into pointing at something that no longer exists.
func TestUXProposalRenamesAreLive(t *testing.T) {
	in := realInputs(t)
	registered := map[string]bool{}
	for _, op := range in.HTTP.Operations {
		registered[op.OperationID] = true
	}
	for proposed, targets := range DefaultRules().UXProposalRenames {
		for _, id := range targets {
			if !registered[id] {
				t.Errorf("UXProposalRenames[%q] -> %q is not a registered route", proposed, id)
			}
		}
	}
	paths := map[string]bool{}
	for _, d := range in.CLI {
		paths[strings.Join(d.Path, " ")] = true
	}
	for reserved, actual := range DefaultRules().UXLeafRenames {
		if !paths[actual] {
			t.Errorf("UXLeafRenames[%q] -> %q is not a registered CLI path", reserved, actual)
		}
	}
}

func TestTokenizeSplitsCamelCaseAndPaths(t *testing.T) {
	cases := map[string]string{
		"requestReleaseSetLocalCommit":           "request release set local commit",
		"AdvanceRun":                             "advance run",
		"/projects/{projectId}/pull-request":     "projects pull request",
		"/projects/{id}/repository-workspaces/x": "projects repository workspaces x",
		"HTTPServer":                             "http server",
	}
	for in, want := range cases {
		if got := strings.Join(tokenize(in), " "); got != want {
			t.Errorf("tokenize(%q) = %q, want %q", in, got, want)
		}
	}
}
