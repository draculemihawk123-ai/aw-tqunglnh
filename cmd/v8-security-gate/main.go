// Command v8-security-gate is V8-04E's own "one reproducible gate command
// over all artifacts" — the same role cmd/v6-gate already plays for V6-14C,
// scoped to the four V8-04A..D security suites instead. It reads the
// artifacts CI produces, prints the evidence index, and exits non-zero
// unless the verdict is PASS. It modifies nothing.
//
//	go run ./cmd/v8-security-gate --evidence-dir ./evidence --commit "$GITHUB_SHA"
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/taQuangLing/agent-workflow/internal/v8gate"
)

func main() {
	evidenceDir := flag.String("evidence-dir", "evidence", "directory holding the downloaded CI artifacts, one directory per artifact name")
	commit := flag.String("commit", "", "the revision being gated; evidence from any other revision is rejected")
	out := flag.String("out", "", "optional path to write the verdict document to")
	flag.Parse()

	report, err := v8gate.Run(v8gate.Inputs{
		EvidenceDir:    *evidenceDir,
		ExpectedCommit: *commit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "v8-security-gate: %v\n", err)
		os.Exit(2)
	}

	document, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "v8-security-gate: marshal verdict: %v\n", err)
		os.Exit(2)
	}
	document = append(document, '\n')
	if *out != "" {
		if err := os.WriteFile(*out, document, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "v8-security-gate: write %s: %v\n", *out, err)
			os.Exit(2)
		}
	}
	fmt.Printf("%s", document)

	for _, note := range report.Notes {
		fmt.Printf("note: %s\n", note)
	}
	for _, finding := range report.Findings {
		fmt.Printf("%s [%s] %s\n", finding.Verdict, finding.TaskID, finding.Detail)
	}

	fmt.Printf("v8-security-gate verdict: %s\n", report.Verdict)
	if report.Verdict != v8gate.VerdictPass {
		os.Exit(1)
	}
}
