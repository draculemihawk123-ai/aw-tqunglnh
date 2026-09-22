package clicompose

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// TestRoutesCoverEveryDescriptorBothDirections is V6-15O's own "every CLI
// leaf is really reachable from os.Args" proof, in both directions: every
// path any leaf registered a cli.Descriptor for has a route (else the
// command exists in the registry but `aw` can never dispatch it), and every
// route path has at least one descriptor (else `aw` dispatches a command the
// parity inventory has never heard of). Route paths are also unique and every
// route carries a Run func.
func TestRoutesCoverEveryDescriptorBothDirections(t *testing.T) {
	routes := Routes()
	routePaths := map[string]bool{}
	for _, r := range routes {
		name := r.Name()
		if routePaths[name] {
			t.Errorf("duplicate route path %q", name)
		}
		routePaths[name] = true
		if r.Run == nil {
			t.Errorf("route %q has no Run func", name)
		}
		if len(r.Path) == 0 || len(r.Path) > 2 {
			t.Errorf("route %q has %d path segments, want 1 or 2 (aw <resource> <action>)", name, len(r.Path))
		}
	}

	descriptorPaths := map[string]bool{}
	for _, d := range cli.All() {
		descriptorPaths[strings.Join(d.Path, " ")] = true
	}
	if len(descriptorPaths) == 0 {
		t.Fatal("cli.All() is empty — the leaf packages did not register (clicompose must import every leaf)")
	}
	for path := range descriptorPaths {
		if !routePaths[path] {
			t.Errorf("descriptor path %q has no route: the command is registered but unreachable from `aw`", path)
		}
	}
	for path := range routePaths {
		if !descriptorPaths[path] {
			t.Errorf("route %q has no cli.Descriptor: `aw` would dispatch a command the parity inventory does not know", path)
		}
	}
}

func TestParseGlobalOptions(t *testing.T) {
	env := map[string]string{"AW_DB": "env.db", "AW_ARTIFACT_ROOT": "env-artifacts"}
	getenv := func(k string) string { return env[k] }

	cases := []struct {
		name     string
		args     []string
		want     Options
		wantRest []string
		wantErr  bool
	}{
		{"none, env fallback", []string{"project", "list"}, Options{DB: "env.db", ArtifactRoot: "env-artifacts"}, []string{"project", "list"}, false},
		{"flag beats env, space form", []string{"--db", "x.db", "project", "list"}, Options{DB: "x.db", ArtifactRoot: "env-artifacts"}, []string{"project", "list"}, false},
		{"equals form after the command", []string{"project", "list", "--db=y.db", "--json"}, Options{DB: "y.db", ArtifactRoot: "env-artifacts"}, []string{"project", "list", "--json"}, false},
		{"single dash", []string{"-workspace-root", "ws", "doctor"}, Options{DB: "env.db", ArtifactRoot: "env-artifacts", WorkspaceRoot: "ws"}, []string{"doctor"}, false},
		{"all five", []string{"--db", "a", "--artifact-root", "b", "--workspace-root", "c", "--claude-executable", "d", "--codex-executable", "e", "x"},
			Options{DB: "a", ArtifactRoot: "b", WorkspaceRoot: "c", ClaudeExecutable: "d", CodexExecutable: "e"}, []string{"x"}, false},
		{"terminator keeps later tokens verbatim", []string{"run", "--", "--db", "not-an-option"}, Options{DB: "env.db", ArtifactRoot: "env-artifacts"}, []string{"run", "--", "--db", "not-an-option"}, false},
		{"missing value", []string{"project", "list", "--db"}, Options{}, nil, true},
		{"three dashes is not an option", []string{"---db", "x"}, Options{DB: "env.db", ArtifactRoot: "env-artifacts"}, []string{"---db", "x"}, false},
		{"a leaf flag with a similar prefix is untouched", []string{"run", "--db-note", "x"}, Options{DB: "env.db", ArtifactRoot: "env-artifacts"}, []string{"run", "--db-note", "x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rest, err := ParseGlobalOptions(tc.args, getenv)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				if !cli.IsUsageError(err) {
					t.Errorf("error %v is not a cli.UsageError", err)
				}
				return
			}
			if got != tc.want {
				t.Errorf("options = %+v, want %+v", got, tc.want)
			}
			if strings.Join(rest, "\x00") != strings.Join(tc.wantRest, "\x00") {
				t.Errorf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

// TestGlobalOptionNamesNeverCollideWithLeafFlags proves the global option
// stripping can never steal a leaf's own flag: no non-test source file under
// internal/delivery/cli defines a string literal equal to a global option
// name (every leaf defines its flags through fs.String/fs.Bool/... string
// literals, or cli.Bind* helpers in the framework files scanned here too).
func TestGlobalOptionNamesNeverCollideWithLeafFlags(t *testing.T) {
	global := map[string]bool{}
	for _, n := range GlobalOptionFlags() {
		global[n] = true
	}
	root := filepath.Join("..", "cli")
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		checked++
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if value, unquoteErr := strconv.Unquote(lit.Value); unquoteErr == nil && global[value] {
				t.Errorf("%s defines the string literal %q — a leaf must never define a global composition option name", path, value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("only %d leaf source files scanned — the walk root is wrong", checked)
	}
}

// ---- Execute: routing, help, usage --------------------------------------

type factoryProbe struct {
	calls  int
	needs  Needs
	err    error
	called bool
}

func (p *factoryProbe) factory(_ context.Context, _ Options, needs Needs) (Deps, func(), error) {
	p.calls++
	p.called = true
	p.needs = needs
	if p.err != nil {
		return Deps{}, nil, p.err
	}
	return Deps{}, func() {}, nil
}

func execute1(t *testing.T, probe *factoryProbe, args []string, stdin string, interactive bool) (cli.ExitCode, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), probe.factory, nil, args, IO{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr, Interactive: interactive,
	})
	return code, stdout.String(), stderr.String()
}

func decodeErrorDoc(t *testing.T, stdout string) (code string, details []map[string]string, message string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var doc struct {
		Error struct {
			Code    string              `json:"code"`
			Message string              `json:"message"`
			Details []map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a JSON error document: %v\n%s", err, stdout)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err == nil {
		t.Fatalf("stdout carries more than one JSON document:\n%s", stdout)
	}
	return doc.Error.Code, doc.Error.Details, doc.Error.Message
}

func TestExecuteResolveErrorsAreTypedUsageErrorsAndNeverBuildDependencies(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no arguments", nil, "expected a command"},
		{"flag first", []string{"-x"}, "expected a command"},
		{"unknown resource", []string{"warp-drive", "engage"}, `unknown command "warp-drive"`},
		{"resource without action", []string{"project"}, "expected 'aw project"},
		{"unknown action", []string{"project", "explode"}, `unknown action "explode" for "project"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := &factoryProbe{}
			code, _, stderr := execute1(t, probe, tc.args, "", false)
			if code != cli.ExitUsage {
				t.Errorf("exit = %d, want %d", code, cli.ExitUsage)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr %q does not contain %q", stderr, tc.want)
			}
			if probe.called {
				t.Error("a usage error must never build dependencies (open a database)")
			}
		})
	}
}

func TestExecuteJSONUsageErrorIsExactlyOneTypedDocument(t *testing.T) {
	probe := &factoryProbe{}
	code, stdout, stderr := execute1(t, probe, []string{"project", "explode", "--json"}, "", false)
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d", code, cli.ExitUsage)
	}
	errCode, _, message := decodeErrorDoc(t, stdout)
	if errCode != "INVALID_REQUEST" {
		t.Errorf("error.code = %q, want INVALID_REQUEST", errCode)
	}
	if !strings.Contains(message, "explode") {
		t.Errorf("error.message %q should name the bad action", message)
	}
	if stderr != "" {
		t.Errorf("--json failure must keep stderr for diagnostics only; got %q", stderr)
	}
}

func TestExecuteHelpNeverBuildsDependencies(t *testing.T) {
	for _, flagSpelling := range []string{"-h", "--help"} {
		probe := &factoryProbe{}
		code, stdout, stderr := execute1(t, probe, []string{"project", "list", flagSpelling}, "", false)
		if code != cli.ExitSuccess {
			t.Errorf("%s: exit = %d, want 0; stderr=%q", flagSpelling, code, stderr)
		}
		if probe.called {
			t.Errorf("%s: help must not build dependencies", flagSpelling)
		}
		if stdout != "" {
			t.Errorf("%s: help output belongs on stderr, stdout = %q", flagSpelling, stdout)
		}
		if !strings.Contains(stderr, "project list") && !strings.Contains(stderr, "Usage") && !strings.Contains(stderr, "usage") {
			t.Errorf("%s: stderr %q does not look like usage", flagSpelling, stderr)
		}
	}
}

func TestExecuteFactoryErrorIsReportedAndCleanupNotLeaked(t *testing.T) {
	probe := &factoryProbe{err: cli.UsageError{Err: errors.New("--db is required (or set AW_DB)")}}
	code, _, stderr := execute1(t, probe, []string{"project", "list"}, "", false)
	if code != cli.ExitUsage || !strings.Contains(stderr, "--db is required") {
		t.Fatalf("exit=%d stderr=%q, want a usage error naming --db", code, stderr)
	}
	if probe.needs&NeedUoW == 0 {
		t.Errorf("project list must declare NeedUoW, got %b", probe.needs)
	}
}

func TestHealthLiveNeedsNoDependencies(t *testing.T) {
	routes := Routes()
	route, ok := Lookup(routes, []string{"health", "live"})
	if !ok {
		t.Fatal("health live route missing")
	}
	if route.Needs != 0 {
		t.Errorf("health live Needs = %b, want 0 (it proves only that the process serves)", route.Needs)
	}
	var stdout bytes.Buffer
	code := Execute(context.Background(), (&factoryProbe{}).factory, nil, []string{"health", "live", "--json"}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if code != cli.ExitSuccess || !strings.Contains(stdout.String(), `"live"`) {
		t.Fatalf("health live exit=%d stdout=%q", code, stdout.String())
	}
}

// ---- Execute: the high-impact confirmation gate -------------------------

func highImpactPaths(t *testing.T) [][]string {
	t.Helper()
	seen := map[string]bool{}
	var paths [][]string
	for _, d := range cli.All() {
		if d.HighImpact && !seen[strings.Join(d.Path, " ")] {
			seen[strings.Join(d.Path, " ")] = true
			paths = append(paths, d.Path)
		}
	}
	sort.Slice(paths, func(i, j int) bool { return strings.Join(paths[i], " ") < strings.Join(paths[j], " ") })
	if len(paths) == 0 {
		t.Fatal("no HighImpact descriptor is registered — the confirmation gate has nothing to protect")
	}
	return paths
}

// TestEveryHighImpactRouteRefusesWithoutYesBeforeAnyDependencyIsBuilt is the
// ADR-028 rule made executable for every real high-impact command: --json (or
// any non-interactive session) without --yes is the typed PRECONDITION_FAILED
// failure with detail confirmation=required, raised BEFORE dispatch — proven
// by the dependency factory (which would open the database) never being
// called.
func TestEveryHighImpactRouteRefusesWithoutYesBeforeAnyDependencyIsBuilt(t *testing.T) {
	for _, path := range highImpactPaths(t) {
		name := strings.Join(path, " ")
		t.Run(name+"/json", func(t *testing.T) {
			probe := &factoryProbe{}
			args := append(append([]string{}, path...), "--json")
			code, stdout, _ := execute1(t, probe, args, "", true /* even a TTY: --json never prompts */)
			if code != cli.ExitUsage {
				t.Errorf("exit = %d, want %d", code, cli.ExitUsage)
			}
			errCode, details, _ := decodeErrorDoc(t, stdout)
			if errCode != "PRECONDITION_FAILED" {
				t.Errorf("error.code = %q, want PRECONDITION_FAILED", errCode)
			}
			if len(details) != 1 || details[0]["field"] != "confirmation" || details[0]["message"] != "required" {
				t.Errorf("error.details = %v, want [{confirmation required}]", details)
			}
			if probe.called {
				t.Error("the confirmation gate must fire before any dependency is built or the leaf dispatched")
			}
		})
		t.Run(name+"/noninteractive-human", func(t *testing.T) {
			probe := &factoryProbe{}
			code, stdout, stderr := execute1(t, probe, append([]string{}, path...), "", false)
			if code != cli.ExitUsage || probe.called {
				t.Errorf("exit=%d factory-called=%v, want %d and false", code, probe.called, cli.ExitUsage)
			}
			if stdout != "" || !strings.Contains(stderr, "--yes") {
				t.Errorf("stdout=%q stderr=%q, want an empty stdout and a stderr line naming --yes", stdout, stderr)
			}
		})
	}
}

func TestEveryHighImpactRouteProceedsPastTheGateWithYes(t *testing.T) {
	for _, path := range highImpactPaths(t) {
		name := strings.Join(path, " ")
		for _, spelling := range []string{"--yes", "-yes", "--yes=true"} {
			t.Run(name+"/"+spelling, func(t *testing.T) {
				sentinel := errors.New("factory reached")
				probe := &factoryProbe{err: sentinel}
				args := append(append([]string{}, path...), spelling, "--json")
				code, stdout, _ := execute1(t, probe, args, "", false)
				if !probe.called {
					t.Fatalf("with %s the gate must pass and reach the dependency factory (exit=%d stdout=%q)", spelling, code, stdout)
				}
				errCode, _, message := decodeErrorDoc(t, stdout)
				if errCode == "PRECONDITION_FAILED" || !strings.Contains(message, "factory reached") {
					t.Errorf("error = %s %q, want the factory's own failure", errCode, message)
				}
			})
		}
	}
}

func TestHighImpactInteractivePrompt(t *testing.T) {
	path := []string{"run", "cancel"}
	cases := []struct {
		name        string
		stdin       string
		wantFactory bool
		wantExit    cli.ExitCode
	}{
		{"y", "y\n", true, cli.ExitFailure /* factory sentinel */},
		{"YES padded", "  YES \n", true, cli.ExitFailure},
		{"n", "n\n", false, cli.ExitFailure},
		{"bare enter", "\n", false, cli.ExitFailure},
		{"eof", "", false, cli.ExitFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := &factoryProbe{err: errors.New("factory reached")}
			code, stdout, stderr := execute1(t, probe, append([]string{}, path...), tc.stdin, true)
			if probe.called != tc.wantFactory {
				t.Errorf("factory called = %v, want %v (stderr=%q)", probe.called, tc.wantFactory, stderr)
			}
			if code != tc.wantExit {
				t.Errorf("exit = %d, want %d", code, tc.wantExit)
			}
			if !strings.Contains(stderr, "run cancel") || !strings.Contains(stderr, "[y/N]") {
				t.Errorf("the prompt must name the command on stderr, got %q", stderr)
			}
			if stdout != "" {
				t.Errorf("the prompt must never touch stdout, got %q", stdout)
			}
			if !tc.wantFactory && !strings.Contains(stderr, "declined") {
				t.Errorf("a declined command must say so, stderr=%q", stderr)
			}
		})
	}
}

func TestNonHighImpactRoutesDoNotAcceptYes(t *testing.T) {
	// --yes on a command the inventory does not gate is a plain unknown flag:
	// silently accepting it would hide a descriptor/registry confirmation
	// mismatch from the operator.
	probe := &factoryProbe{}
	code, _, stderr := execute1(t, probe, []string{"settings", "show", "--yes"}, "", false)
	if code != cli.ExitUsage {
		t.Errorf("exit = %d, want %d; stderr=%q", code, cli.ExitUsage, stderr)
	}
}

func TestSplitHighImpactVerdictFailsClosed(t *testing.T) {
	descriptors := []cli.Descriptor{
		{Path: []string{"definition", "publish"}, Scope: cli.ScopeInstallation, AppOperation: "X", HTTPOperationID: "a", HighImpact: true},
		{Path: []string{"definition", "publish"}, Scope: cli.ScopeProject, AppOperation: "X", HTTPOperationID: "b"},
	}
	routes := []Route{{Path: []string{"definition", "publish"}, Run: func(context.Context, *Deps, []string, IO) error { return nil }}}
	probe := &factoryProbe{}
	var stdout, stderr bytes.Buffer
	code := ExecuteWith(context.Background(), routes, descriptors, probe.factory, nil, []string{"definition", "publish", "--yes"}, IO{Stdout: &stdout, Stderr: &stderr})
	if code == cli.ExitSuccess || probe.called {
		t.Fatalf("descriptors that disagree on HighImpact must fail closed; exit=%d factory-called=%v", code, probe.called)
	}
	if !strings.Contains(stderr.String(), "disagree on HighImpact") {
		t.Errorf("stderr %q should explain the split verdict", stderr.String())
	}
}

// ---- Execute: outcome and failure envelope ------------------------------

func TestExecuteFailureNeverAddsASecondDocumentAfterAResult(t *testing.T) {
	// `aw health ready` writes its report document and THEN fails (503-style)
	// when not ready; --json must not append an error document to it.
	routes := []Route{{
		Path: []string{"probe", "thing"},
		Run: func(_ context.Context, _ *Deps, _ []string, s IO) error {
			s.Stdout.Write([]byte("{\"status\":\"not_ready\"}\n"))
			return errors.New("not ready")
		},
	}}
	descriptors := []cli.Descriptor{{Path: []string{"probe", "thing"}, Scope: cli.ScopeInstallation, AppOperation: "P", HTTPOperationID: "p"}}
	var stdout, stderr bytes.Buffer
	code := ExecuteWith(context.Background(), routes, descriptors, (&factoryProbe{}).factory, nil, []string{"probe", "thing", "--json"}, IO{Stdout: &stdout, Stderr: &stderr})
	if code != cli.ExitFailure {
		t.Errorf("exit = %d, want %d", code, cli.ExitFailure)
	}
	dec := json.NewDecoder(&stdout)
	var first map[string]any
	if err := dec.Decode(&first); err != nil || first["status"] != "not_ready" {
		t.Fatalf("stdout must start with the leaf's own document, got %v err=%v", first, err)
	}
	var second map[string]any
	if err := dec.Decode(&second); err == nil {
		t.Errorf("stdout carries a second document %v after the result", second)
	}
	if !strings.Contains(stderr.String(), "not ready") {
		t.Errorf("the failure must still be reported on stderr, got %q", stderr.String())
	}
}

func TestExecuteFailureBodyMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
		wantExit cli.ExitCode
	}{
		{"usage", cli.UsageError{Err: errors.New("bad")}, "INVALID_REQUEST", cli.ExitUsage},
		{"confirmation", cli.ErrConfirmationRequired, "PRECONDITION_FAILED", cli.ExitUsage},
		{"declined", ErrConfirmationDeclined, "CONFLICT", cli.ExitFailure},
		{"other", errors.New("boom"), "INTERNAL", cli.ExitFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(failureBody(tc.err).Code); got != tc.wantCode {
				t.Errorf("code = %q, want %q", got, tc.wantCode)
			}
			if got := exitCodeFor(tc.err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d", got, tc.wantExit)
			}
		})
	}
}
