package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/embeddedui"
	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/clicompose"
)

// exitCode is the typed replacement for a bare os.Exit(N) magic number: each
// value names exactly what class of outcome it reports, so a caller (or a
// test asserting on run's return) never has to remember what a raw integer
// meant. Values follow the common CLI convention Go's own flag package uses
// (2 for a usage error) so external tooling that already expects that
// convention keeps working.
type exitCode int

const (
	exitSuccess exitCode = 0
	exitFailure exitCode = 1
	exitUsage   exitCode = 2
)

// usage is the `aw help` text: the process-level commands this file owns
// plus every `aw <resource> <action>` command internal/delivery/clicompose
// routes (generated from the routing table, so the help can never drift from
// what actually dispatches).
var usage = `Usage: aw <command> [flags]

Process commands:
  serve       start the local API/control-plane server
  worker      start an embedded worker process
  help        show this help
  version     print the aw build version

Resource commands (aw <resource> <action> [flags]):
` + clicompose.Usage() + `
Global options (any resource command; env AW_DB, AW_ARTIFACT_ROOT, ...):
  --db <path>                 sqlite database of the installation
  --artifact-root <dir>       artifact storage root
  --workspace-root <dir>      Git worktree storage root (workspace/source commands)
  --claude-executable <path>  register the Claude CLI as a live provider
  --codex-executable <path>   register the Codex CLI as a live provider
  --env-allowlist <names>     comma-separated variable names a provider's version probe may inherit
                              (give it the same list as 'aw worker --env-allowlist'; 'aw doctor' reports
                              whether the provider can run in that environment)

Machine-readable output: add --json (one JSON document on stdout; a failure is
one typed error document). High-impact commands require --yes when stdin is not
a terminal or --json is set. Actor and roles are never flags: --principal-config
selects the trusted principal file.

Run 'aw <command> -h' for command-specific flags.
`

// run is the composition root's single dispatch point (V1-01's "wiring chỉ
// ở composition root"): it owns argument parsing down to the command name
// and nothing else — each command's own flags are parsed inside its own
// handler, not here. stdout/stderr are injected so tests can assert on
// output without spawning the built binary.
func run(arguments []string, stdout, stderr io.Writer) exitCode {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runStreams(ctx, arguments, os.Stdin, isInteractive(os.Stdin, stdout), stdout, stderr)
}

// isInteractive is true only when stdin and stdout are both real terminals:
// the condition under which a high-impact command may prompt (ADR-028).
// stdout must be the process' own *os.File — an injected buffer (tests, a
// pipe wrapper) is never interactive.
func isInteractive(stdin *os.File, stdout io.Writer) bool {
	out, ok := stdout.(*os.File)
	return ok && cli.IsTerminal(stdin) && cli.IsTerminal(out)
}

// runStreams is run with an injectable stdin and interactivity, so tests
// can drive body-reading commands and the confirmation prompt.
func runStreams(ctx context.Context, arguments []string, stdin io.Reader, interactive bool, stdout, stderr io.Writer) exitCode {
	if len(arguments) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}

	name := arguments[0]
	rest := arguments[1:]

	if name == "-h" || name == "--help" || name == "help" {
		fmt.Fprint(stdout, usage)
		return exitSuccess
	}

	// Process-level commands own their own flags and lifecycle — unless the
	// arguments are a routed resource command that merely shares the word
	// (`aw version show|diff`, V9-09): those fall through to the router below.
	if handler, ok := subcommands[name]; ok && !isRoutedResourceCommand(name, rest) {
		return finish(handler(rest, stdout), stderr)
	}

	// `aw evidence verify --evidence-dir/--suite` is the pre-V6 offline
	// bundle verifier (V1-01); `aw evidence verify <evidenceId> ...` is the
	// V6-15K runtime-evidence leaf. Both are the one CLI_LOCAL `evidence
	// verify` ADR-028 names; the bundle flags select the local-bundle mode.
	if name == "evidence" && isLegacyBundleVerify(rest) {
		return finish(runEvidenceBundleVerify(rest[1:], stdout), stderr)
	}

	// Resource commands take the global composition options anywhere,
	// including before the resource name (`aw --db x.db project list`), so
	// the routed command name is the first argument that is not one of them.
	if clicompose.HasRoute(routedCommandName(arguments)) {
		code := clicompose.Execute(ctx, oneShotFactory, os.Getenv, arguments, clicompose.IO{
			Stdin: stdin, Stdout: stdout, Stderr: stderr, Interactive: interactive,
		})
		return exitCode(code)
	}

	fmt.Fprintf(stderr, "aw: unknown command %q\n\n%s", name, usage)
	return exitUsage
}

// isRoutedResourceCommand reports whether `aw <name> <rest...>` is a resource
// command the clicompose router owns even though <name> is also a process
// command (V9-09). The one such word today is `version`: `aw version` prints
// this binary's build identity (CLI_LOCAL, ADR-028), while `aw version show`
// and `aw version diff` are the definition-version resource commands (they
// have HTTP twins and sit in the parity ledger as ordinary resource routes).
// The process-command table used to win unconditionally, so the resource
// commands were unreachable by name.
//
// The decision is made from the router's own table, not a second hardcoded
// list: the first argument after <name> that is not a global composition
// option must complete a registered two-word path. Anything else — no
// argument, `--json`, a typo, an extra word — stays with the process
// command, whose output and usage errors are therefore byte-for-byte what
// they were before.
func isRoutedResourceCommand(name string, rest []string) bool {
	_, residual, err := clicompose.ParseGlobalOptions(rest, nil)
	if err != nil || len(residual) == 0 {
		return false
	}
	_, routed := clicompose.Lookup(clicompose.Routes(), []string{name, residual[0]})
	return routed
}

// routedCommandName is the first argument that is not a global composition
// option (or that option's value), or "" when there is none.
func routedCommandName(arguments []string) string {
	_, rest, err := clicompose.ParseGlobalOptions(arguments, nil)
	if err != nil || len(rest) == 0 {
		return ""
	}
	return rest[0]
}

// finish maps a process-level handler's error to the exit code and the one
// `aw: ...` stderr line every such command has always produced.
func finish(err error, stderr io.Writer) exitCode {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return exitSuccess
	}
	fmt.Fprintln(stderr, "aw:", err)
	if isUsageError(err) {
		return exitUsage
	}
	return exitFailure
}

// usageError marks an error caused by how the command was invoked (bad
// flags, missing required arguments) rather than a failure while doing the
// work, so run can map it to exitUsage instead of exitFailure.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }

func isUsageError(err error) bool {
	var u usageError
	return errors.As(err, &u)
}

// subcommands are the process-level commands the composition root itself
// owns; every `aw <resource> <action>` command is routed by
// internal/delivery/clicompose instead (V6-15O).
var subcommands = map[string]func(arguments []string, stdout io.Writer) error{
	"serve":   runServe,
	"worker":  runWorker,
	"version": runVersion,
}

// runVersion prints `aw version`: the module/VCS identity of this binary,
// plain text (a process-level command is never forced into a fake JSON
// document, ADR-028). It is one of the closed CLI_LOCAL set
// {serve, worker, help, version, evidence verify}.
func runVersion(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "print the full release manifest (schema/adapter/UI-embedded provenance) as JSON instead of the one-line human summary")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if flags.NArg() != 0 {
		return usageError{errors.New("version takes no arguments")}
	}
	if !*jsonOutput {
		fmt.Fprintln(stdout, versionLine())
		return nil
	}
	manifest, err := buildReleaseManifest()
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode release manifest: %w", err)
	}
	fmt.Fprintln(stdout, string(encoded))
	return nil
}

func versionLine() string {
	version, revision := "devel", "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				revision = setting.Value
			}
		}
	}
	return fmt.Sprintf("aw %s (commit %s)", version, revision)
}

// releaseManifest is V8-08's own "version/schema/adapter manifest"
// (docs/design/10-v8-alpha-hardening.md V8-08, HE-02-M04's "toolchain
// version, dependency lock, repo revision ... MUST have provenance"): every
// field a release artifact must be able to answer about itself without an
// operator needing to separately inspect the binary or the source tree it
// came from. GeneratedAt is deliberately the ONE field V8-08's own
// reproducible-build verify step allowlists as expected to differ between
// two otherwise-identical builds — see cmd/aw-release-build's own doc
// comment for that comparison.
type releaseManifest struct {
	Version            string   `json:"version"`
	Commit             string   `json:"commit"`
	GoVersion          string   `json:"goVersion"`
	OS                 string   `json:"os"`
	Arch               string   `json:"arch"`
	SchemaVersion      int      `json:"schemaVersion"`
	UIEmbedded         bool     `json:"uiEmbedded"`
	SupportedProviders []string `json:"supportedProviders"`
	GeneratedAt        string   `json:"generatedAt"`
}

// supportedProviderKinds is the closed, compile-time list of agent provider
// kinds this build's own --claude-executable/--codex-executable flags
// (cmd/aw/serve.go, cmd/aw/worker.go) ever register — never the runtime
// adapterbuild.Build inventory (a per-installation, per-repository-tuple
// concept V6 already owns), just which PROVIDER FAMILIES this compiled
// binary itself knows how to talk to at all.
var supportedProviderKinds = []string{"claude", "codex"}

func buildReleaseManifest() (releaseManifest, error) {
	version, revision := "devel", "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				revision = setting.Value
			}
		}
	}
	schemaVersion, err := sqlite.CurrentSchemaVersion()
	if err != nil {
		return releaseManifest{}, fmt.Errorf("determine schema version: %w", err)
	}
	uiEmbedded, err := embeddedui.IsEmbedded()
	if err != nil {
		return releaseManifest{}, fmt.Errorf("determine UI-embedded status: %w", err)
	}
	return releaseManifest{
		Version: version, Commit: revision, GoVersion: runtime.Version(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, SchemaVersion: schemaVersion,
		UIEmbedded: uiEmbedded, SupportedProviders: supportedProviderKinds,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// isLegacyBundleVerify reports whether the arguments after `evidence` select
// the pre-V6 offline bundle verifier: `verify` with one of its own
// --evidence-dir/--suite flags.
func isLegacyBundleVerify(args []string) bool {
	if len(args) == 0 || args[0] != "verify" {
		return false
	}
	for _, a := range args[1:] {
		trimmed := strings.TrimLeft(a, "-")
		if len(trimmed) == len(a) {
			continue
		}
		flagName, _, _ := strings.Cut(trimmed, "=")
		if flagName == "evidence-dir" || flagName == "suite" {
			return true
		}
	}
	return false
}

// runEvidenceBundleVerify verifies a sealed evidence bundle's checksums the
// same way `agentkit-spike evidence verify` does — the offline, local-only
// mode of the CLI_LOCAL `evidence verify` command. It depends only on
// internal/adapters/evidence (a production adapter, not a test fixture), the
// reason cmd/aw (the one place allowed to import adapters) owns it.
func runEvidenceBundleVerify(arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("evidence verify", flag.ContinueOnError)
	evidenceDir := flags.String("evidence-dir", "", "evidence root")
	suiteID := flags.String("suite", "", "suite id")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if *evidenceDir == "" || *suiteID == "" {
		return usageError{errors.New("--evidence-dir and --suite are required")}
	}
	directory := filepath.Join(*evidenceDir, *suiteID)
	if _, err := evidence.Verify(directory); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "verified=%s\n", directory)
	return nil
}
