// Package clicompose is V6-15O's own CLI composition: the one place that
// (a) wires every V6-15C..V6-15N leaf package's own Run* function into the
// real `aw <resource> <action>` routing table and (b) enforces the two
// cross-cutting CLI rules ADR-028 assigns to the composition root — the
// high-impact confirmation gate and the typed one-document failure
// envelope — so no leaf re-implements either.
//
// It is the CLI-side twin of internal/delivery/httpcompose: V6-12 composed
// every HTTP route fragment into one importable function so the contract
// generator and the production `aw serve` see the identical route set; this
// package does the same for the CLI so `cmd/aw` (production routing) and
// V6-15O's own parity checker/equivalence tests (internal/delivery/parity)
// see the identical leaf set. docs/design/08-v6-api-projections.md §1 rule
// 8 reserves this ("chỉ V6-15O compose CLI/parity registry"); every leaf
// package still owns its own cli.Descriptor registration (its own init(),
// unchanged) and its own Run* functions — this package adds no command of
// its own.
//
// # What this package deliberately does not do
//
//   - It imports no concrete adapter (SQLite, Git, provider, process): every
//     dependency a leaf needs arrives through Deps, built by the composition
//     root (cmd/aw) from the real adapters. internal/archtest's
//     TestDeliveryCLIComposeNeverImportsAdaptersOrWorkers proves it.
//   - It never opens a database for `aw help`, `aw version`, `aw health
//     live`, an unknown command or a usage error: the DepsFactory is only
//     called after the route is resolved and (for a high-impact command)
//     after the confirmation gate passed, so a refused command touches
//     nothing.
//   - It never adds an --actor/--role flag. Principal selection stays each
//     leaf's own --principal-config (cli.BindPrincipalFlag), the one
//     "global composition option chọn một trusted config file" ADR-028
//     allows.
//
// # Global composition options
//
// --db, --artifact-root, --workspace-root, --claude-executable and
// --codex-executable (env fallbacks AW_DB, AW_ARTIFACT_ROOT,
// AW_WORKSPACE_ROOT, AW_CLAUDE_EXECUTABLE, AW_CODEX_EXECUTABLE) select the
// installation a one-shot invocation operates on, exactly like the same
// flags of `aw serve`. They may appear anywhere in the argument list and are
// stripped before the leaf parses its own flags; no leaf defines a flag of
// the same name (TestGlobalOptionNamesNeverCollideWithLeafFlags).
package clicompose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Needs is the set of concrete runtime dependencies one route requires. The
// DepsFactory builds only what the resolved route asks for, so `aw doctor`
// without --artifact-root still runs (and reports the missing root as
// degraded, which is its job) instead of failing before it can diagnose.
type Needs uint

const (
	// NeedUoW: an open ports.UnitOfWork (requires --db).
	NeedUoW Needs = 1 << iota
	// NeedStore: the ports.QueryStore GET /doctor's database check pings.
	NeedStore
	// NeedArtifactStore: a ports.ArtifactStore rooted at --artifact-root
	// (requires --artifact-root).
	NeedArtifactStore
	// NeedArtifactRoot: only the artifact root PATH (health ready checks it
	// itself); does not require the directory to exist.
	NeedArtifactRoot
	// NeedWorkspace: a ports.WorkspaceInspectionReader rooted at
	// --workspace-root (requires --workspace-root).
	NeedWorkspace
	// NeedIsolation: the real ports.IsolationEnforcementChecker.
	NeedIsolation
	// NeedAgents: the agentregistry.Registry (zero executors unless
	// --claude-executable/--codex-executable are given).
	NeedAgents
	// NeedConfig: a fully-built config.Config for `aw doctor`.
	NeedConfig
)

// Deps is the union of every dependency any leaf's own Dependencies struct
// asks for, already built from real adapters by the composition root
// (cmd/aw). A route reads only the fields its own Needs declares.
type Deps struct {
	UoW             ports.UnitOfWork
	Store           ports.QueryStore
	ArtifactStore   ports.ArtifactStore
	ArtifactRoot    string
	WorkspaceReader ports.WorkspaceInspectionReader
	Isolation       ports.IsolationEnforcementChecker
	Agents          *agentregistry.Registry
	Config          config.Config
	// Matcher is the process-lifetime known-secrets redactor every
	// redacting leaf reuses. A one-shot process mints no per-process
	// session token (cmd/aw/serve.go's own sessionToken has no CLI
	// equivalent), so the composition root passes redact.NewMatcher() —
	// the same constructor, with whatever known secrets it has (possibly
	// none) — exactly what run/doc.go and events/events.go document.
	Matcher redact.Matcher
	// IDs defaults to idsource.Random{}; Clock to clock.System{}; Now and
	// Sleep stay nil in production (each leaf documents nil as "real time /
	// real sleeper") and are set only by deterministic tests.
	IDs   idsource.Source
	Clock clock.Clock
	Now   func() time.Time
	Sleep cli.Sleeper
}

func (d *Deps) ids() idsource.Source {
	if d.IDs != nil {
		return d.IDs
	}
	return idsource.Random{}
}

func (d *Deps) clock() clock.Clock {
	if d.Clock != nil {
		return d.Clock
	}
	return clock.System{}
}

// IO is the process-level streams one invocation reads and writes.
// Interactive is true only when both stdin and stdout are real terminals
// (the composition root resolves it with cli.IsTerminal); it gates whether
// a high-impact command may prompt at all.
type IO struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Interactive bool
}

// Route is one `aw <resource> <action>` path bound to its leaf's own Run*
// function. A single Route may back several cli.Descriptors that share the
// path at different scopes (definition/version commands, ADR-028's own
// "--scope global vs --project-id" example): the leaf itself resolves the
// scope from its flags, exactly as it always did.
type Route struct {
	Path  []string
	Needs Needs
	Run   func(ctx context.Context, d *Deps, args []string, io IO) error
}

// Name is the route's space-joined path, e.g. "run cancel".
func (r Route) Name() string { return strings.Join(r.Path, " ") }

// DepsFactory builds the Deps one invocation needs. It is called only after
// routing, help handling and the confirmation gate, and returns a cleanup
// func the caller always runs (closing the database, ...). A missing
// required option is a cli.UsageError.
type DepsFactory func(ctx context.Context, opts Options, needs Needs) (Deps, func(), error)

// Options are the parsed global composition options.
type Options struct {
	DB               string
	ArtifactRoot     string
	WorkspaceRoot    string
	ClaudeExecutable string
	CodexExecutable  string
}

// globalOptionNames is the closed list of composition options, each with
// its environment fallback. It is exported for the collision test only.
var globalOptionNames = []struct{ Flag, Env string }{
	{"db", "AW_DB"},
	{"artifact-root", "AW_ARTIFACT_ROOT"},
	{"workspace-root", "AW_WORKSPACE_ROOT"},
	{"claude-executable", "AW_CLAUDE_EXECUTABLE"},
	{"codex-executable", "AW_CODEX_EXECUTABLE"},
}

// GlobalOptionFlags returns the global composition option flag names (no
// leading dashes), for the collision guard.
func GlobalOptionFlags() []string {
	names := make([]string, 0, len(globalOptionNames))
	for _, o := range globalOptionNames {
		names = append(names, o.Flag)
	}
	return names
}

// ParseGlobalOptions strips every global composition option out of args
// (accepting --name value, --name=value and the single-dash spellings the
// stdlib flag package accepts, anywhere before a bare "--") and returns the
// remaining arguments in their original order. getenv supplies the
// AW_* fallbacks; nil means "no environment".
func ParseGlobalOptions(args []string, getenv func(string) string) (Options, []string, error) {
	values := make(map[string]string, len(globalOptionNames))
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name, value, hasValue, ok := matchGlobalOption(arg)
		if !ok {
			rest = append(rest, arg)
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return Options{}, nil, cli.UsageError{Err: fmt.Errorf("--%s requires a value", name)}
			}
			i++
			value = args[i]
		}
		values[name] = value
	}
	opts := Options{
		DB:               pick(values, "db", getenv, "AW_DB"),
		ArtifactRoot:     pick(values, "artifact-root", getenv, "AW_ARTIFACT_ROOT"),
		WorkspaceRoot:    pick(values, "workspace-root", getenv, "AW_WORKSPACE_ROOT"),
		ClaudeExecutable: pick(values, "claude-executable", getenv, "AW_CLAUDE_EXECUTABLE"),
		CodexExecutable:  pick(values, "codex-executable", getenv, "AW_CODEX_EXECUTABLE"),
	}
	return opts, rest, nil
}

func pick(values map[string]string, flagName string, getenv func(string) string, env string) string {
	if v, ok := values[flagName]; ok {
		return v
	}
	if getenv != nil {
		return getenv(env)
	}
	return ""
}

func matchGlobalOption(arg string) (name, value string, hasValue, ok bool) {
	trimmed := strings.TrimLeft(arg, "-")
	if len(trimmed) == len(arg) || len(arg)-len(trimmed) > 2 {
		return "", "", false, false
	}
	base, val, eq := strings.Cut(trimmed, "=")
	for _, o := range globalOptionNames {
		if base == o.Flag {
			return o.Flag, val, eq, true
		}
	}
	return "", "", false, false
}

// ErrConfirmationDeclined is returned when an interactive operator answers
// anything but yes to a high-impact prompt: the command is not dispatched,
// and the invocation exits non-zero (a declined destructive command is not
// a success).
var ErrConfirmationDeclined = errors.New("clicompose: high-impact command declined at the confirmation prompt")

// Lookup returns the route for path, if any.
func Lookup(routes []Route, path []string) (Route, bool) {
	want := strings.Join(path, " ")
	for _, r := range routes {
		if r.Name() == want {
			return r, true
		}
	}
	return Route{}, false
}

// resolveRoute finds the route for the leading non-flag tokens of args and
// returns the arguments that follow the route's own path. A known resource
// with a missing or unknown action, and an unknown command, are usage
// errors that name what would have been valid.
func resolveRoute(routes []Route, args []string) (Route, []string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return Route{}, nil, cli.UsageError{Err: errors.New("expected a command (run 'aw help')")}
	}
	if len(args) >= 2 && !strings.HasPrefix(args[1], "-") {
		if r, ok := Lookup(routes, args[:2]); ok {
			return r, args[2:], nil
		}
	}
	if r, ok := Lookup(routes, args[:1]); ok {
		return r, args[1:], nil
	}
	var actions []string
	for _, r := range routes {
		if len(r.Path) == 2 && r.Path[0] == args[0] {
			actions = append(actions, r.Path[1])
		}
	}
	if len(actions) > 0 {
		sort.Strings(actions)
		if len(args) >= 2 && !strings.HasPrefix(args[1], "-") {
			return Route{}, nil, cli.UsageError{Err: fmt.Errorf("unknown action %q for %q (want %s)", args[1], args[0], strings.Join(actions, "|"))}
		}
		return Route{}, nil, cli.UsageError{Err: fmt.Errorf("expected 'aw %s %s'", args[0], strings.Join(actions, "|"))}
	}
	return Route{}, nil, cli.UsageError{Err: fmt.Errorf("unknown command %q (run 'aw help')", args[0])}
}

// HasRoute reports whether name is the first path segment of any route — the
// composition root's cheap "does the resource router own this command?"
// test, so a name the router does not know still reaches the process-level
// utilities (serve, worker, help, version) untouched.
func HasRoute(name string) bool {
	for _, r := range Routes() {
		if r.Path[0] == name {
			return true
		}
	}
	return false
}

// Usage renders the routed commands, grouped by resource, for `aw help`.
func Usage() string {
	byResource := map[string][]string{}
	var resources []string
	for _, r := range Routes() {
		res := r.Path[0]
		if _, seen := byResource[res]; !seen {
			resources = append(resources, res)
		}
		if len(r.Path) == 2 {
			byResource[res] = append(byResource[res], r.Path[1])
		} else {
			byResource[res] = append(byResource[res], "")
		}
	}
	sort.Strings(resources)
	var b strings.Builder
	for _, res := range resources {
		actions := byResource[res]
		sort.Strings(actions)
		var named []string
		for _, a := range actions {
			if a != "" {
				named = append(named, a)
			}
		}
		if len(named) == 0 {
			fmt.Fprintf(&b, "  %s\n", res)
			continue
		}
		fmt.Fprintf(&b, "  %-22s %s\n", res, strings.Join(named, "|"))
	}
	return b.String()
}

// isHelp reports whether args ask for the leaf's own flag help.
func isHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		base, val, eq := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if len(a)-len(strings.TrimLeft(a, "-")) == 0 || len(a)-len(strings.TrimLeft(a, "-")) > 2 {
			continue
		}
		if base != name {
			continue
		}
		if !eq {
			return true
		}
		return val != "false" && val != "0"
	}
	return false
}

// stripFlag removes every spelling of the boolean flag name from args.
func stripFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		base, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		dashes := len(a) - len(strings.TrimLeft(a, "-"))
		if dashes >= 1 && dashes <= 2 && base == name {
			continue
		}
		out = append(out, a)
	}
	return out
}
