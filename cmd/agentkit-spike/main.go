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
	evidenceDir := flags.String("evidence-dir", "docs/spikes/evidence", "evidence root")
	goExecutable := flags.String("go", "go", "Go executable")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if !*offline {
		return fmt.Errorf("only --offline is supported by the current baseline harness")
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	result, err := spikeacceptance.RunOfflineBaseline(context.Background(), spikeacceptance.OSCommandRunner{}, spikeacceptance.RunRequest{
		EvidenceRoot: *evidenceDir,
		WorkingDir:   workingDir,
		GoExecutable: *goExecutable,
		Now:          time.Now().UTC(),
	})
	fmt.Printf("suite=%s evidence=%s passed=%t\n", result.SuiteID, result.EvidenceDir, result.Passed)
	return err
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
