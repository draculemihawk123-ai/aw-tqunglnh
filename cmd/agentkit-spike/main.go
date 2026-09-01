package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/spikeacceptance"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agentkit-spike:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("expected 'acceptance' or 'evidence verify'")
	}
	switch arguments[0] {
	case "acceptance":
		return runAcceptance(arguments[1:])
	case "evidence":
		return runEvidence(arguments[1:])
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func runAcceptance(arguments []string) error {
	flags := flag.NewFlagSet("acceptance", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	offline := flags.Bool("offline", false, "run offline baseline only")
	full := flags.Bool("full", false, "dispatch the SPK-01..SPK-14 registry as a dry run (prints per-SPK status; does not write an evidence bundle)")
	evidenceDir := flags.String("evidence-dir", "docs/spikes/evidence", "evidence root")
	goExecutable := flags.String("go", "go", "Go executable")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	switch {
	case *offline && *full:
		return fmt.Errorf("--offline and --full are mutually exclusive")
	case *full:
		return runFullSuite()
	case *offline:
		return runOfflineBaseline(*evidenceDir, *goExecutable)
	default:
		return fmt.Errorf("one of --offline or --full is required")
	}
}

func runOfflineBaseline(evidenceDir, goExecutable string) error {
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	result, err := spikeacceptance.RunOfflineBaseline(context.Background(), spikeacceptance.OSCommandRunner{}, spikeacceptance.RunRequest{
		EvidenceRoot: evidenceDir,
		WorkingDir:   workingDir,
		GoExecutable: goExecutable,
		Now:          time.Now().UTC(),
	})
	fmt.Printf("suite=%s evidence=%s passed=%t\n", result.SuiteID, result.EvidenceDir, result.Passed)
	return err
}

// runFullSuite dispatches every registered SPK-01..SPK-14 scenario and
// prints its real, current status. It is a dry run: dispatch succeeding
// (err == nil) means the registry/dispatcher mechanism itself ran cleanly,
// not that every SPK passed — that distinction is what V0-01A locks down.
// Evidence-bundle persistence for a full run is out of scope here (V0-08).
func runFullSuite() error {
	registry, err := spikeacceptance.NewRegistry(spikeacceptance.DefaultScenarios())
	if err != nil {
		return fmt.Errorf("build SPK registry: %w", err)
	}
	now := time.Now().UTC()
	manifest, err := registry.RunAll(context.Background(), "full-"+now.Format("20060102t150405.000000000z"), now)
	if err != nil {
		return fmt.Errorf("run SPK registry: %w", err)
	}
	passed := 0
	for _, result := range manifest.Results {
		status := "FAIL"
		if result.Passed {
			status = "PASS"
			passed++
		}
		fmt.Printf("%s %s\n", result.SPKID, status)
	}
	fmt.Printf("suite=%s spk=%d/%d passed=%t (dry run; not an evidence-bundled gate verdict)\n",
		manifest.SuiteID, passed, len(manifest.Results), passed == len(manifest.Results))
	return nil
}

func runEvidence(arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "verify" {
		return fmt.Errorf("expected 'evidence verify'")
	}
	flags := flag.NewFlagSet("evidence verify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	suiteID := flags.String("suite", "", "suite id")
	evidenceDir := flags.String("evidence-dir", "docs/spikes/evidence", "evidence root")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if err := spikeacceptance.VerifySuite(*evidenceDir, *suiteID); err != nil {
		return err
	}
	abs, err := filepath.Abs(filepath.Join(*evidenceDir, *suiteID))
	if err != nil {
		return err
	}
	fmt.Printf("verified=%s\n", abs)
	return nil
}
