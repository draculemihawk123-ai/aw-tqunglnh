package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
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

// errNotYetImplemented marks a subcommand skeleton that exists (it parses
// its own flags and can be invoked) but has no real behavior yet — the
// production logic lands in the later V1..V8 task that owns it. It is a
// distinct sentinel, not a generic error, so run can map it to a stable
// message instead of forcing every stub to hand-write one.
var errNotYetImplemented = errors.New("not yet implemented")

const usage = `Usage: agentkit <command> [flags]

Commands:
  serve       start the local API/control-plane server
  worker      start an embedded worker process
  doctor      run readiness/capability diagnostics
  definition  validate/publish/inspect definitions
  evidence    inspect and verify evidence bundles

Run 'agentkit <command> -h' for command-specific flags.
`

// run is the composition root's single dispatch point (V1-01's "wiring chỉ
// ở composition root"): it owns argument parsing down to the subcommand
// name and nothing else — each subcommand's own flags are parsed inside its
// own handler, not here. stdout/stderr are injected so tests can assert on
// output without spawning the built binary.
func run(arguments []string, stdout, stderr io.Writer) exitCode {
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

	handler, ok := subcommands[name]
	if !ok {
		fmt.Fprintf(stderr, "agentkit: unknown command %q\n\n%s", name, usage)
		return exitUsage
	}

	if err := handler(rest, stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		fmt.Fprintln(stderr, "agentkit:", err)
		if isUsageError(err) {
			return exitUsage
		}
		return exitFailure
	}
	return exitSuccess
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

var subcommands = map[string]func(arguments []string, stdout io.Writer) error{
	"serve":      stub("serve"),
	"worker":     stub("worker"),
	"doctor":     stub("doctor"),
	"definition": stub("definition"),
	"evidence":   runEvidence,
}

// stub returns a handler for a subcommand skeleton that has not been
// implemented yet: it still owns its own (currently empty) flag set, so
// adding real flags in the task that implements it is a local change, not a
// composition-root change.
func stub(name string) func([]string, io.Writer) error {
	return func(arguments []string, _ io.Writer) error {
		flags := flag.NewFlagSet(name, flag.ContinueOnError)
		if err := flags.Parse(arguments); err != nil {
			return usageError{err}
		}
		return fmt.Errorf("%s: %w", name, errNotYetImplemented)
	}
}

// runEvidence is the one subcommand V1-01 allows to depend on
// spikeacceptance-adjacent packages; it currently only needs
// internal/adapters/evidence (a production adapter, not a test fixture) to
// verify a sealed bundle's checksums the same way `agentkit-spike evidence
// verify` does.
func runEvidence(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 || arguments[0] != "verify" {
		return usageError{errors.New("expected 'evidence verify'")}
	}
	flags := flag.NewFlagSet("evidence verify", flag.ContinueOnError)
	evidenceDir := flags.String("evidence-dir", "", "evidence root")
	suiteID := flags.String("suite", "", "suite id")
	if err := flags.Parse(arguments[1:]); err != nil {
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
