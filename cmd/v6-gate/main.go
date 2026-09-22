// Command v6-gate is V6-14C's own "one reproducible gate command over all
// artifacts" (docs/design/08-v6-api-projections.md V6-14C). It reads the
// artifacts CI produced, checks them against the checkout they claim to
// describe, prints the evidence index, and exits non-zero unless the verdict
// is PASS. It modifies nothing.
//
//	go run ./cmd/v6-gate --evidence-dir ./evidence --commit "$GITHUB_SHA"
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/apicontract"
	"github.com/taQuangLing/agent-workflow/internal/docscoverage"
	"github.com/taQuangLing/agent-workflow/internal/v6gate"
)

func main() {
	evidenceDir := flag.String("evidence-dir", "evidence", "directory holding the downloaded CI artifacts, one directory per artifact name")
	repoRoot := flag.String("repo-root", ".", "the checkout the evidence claims to describe")
	commit := flag.String("commit", "", "the revision being gated; evidence from any other revision is rejected")
	out := flag.String("out", "", "optional path to write the verdict document to")
	flag.Parse()

	// The V1-00C debt count is computed here, from the repository itself,
	// rather than read from an artifact: the gate already has the checkout,
	// and a number it derives cannot drift from the code it describes.
	docs, err := docscoverage.Run(*repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "v6-gate: documentation coverage check: %v\n", err)
		os.Exit(2)
	}

	report, err := v6gate.Run(v6gate.Inputs{
		EvidenceDir:           *evidenceDir,
		RepoRoot:              *repoRoot,
		ExpectedCommit:        *commit,
		ContractVersionPrefix: apicontract.ContractVersion,
		DocsDebt:              docs.Debt(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "v6-gate: %v\n", err)
		os.Exit(2)
	}

	document, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "v6-gate: marshal verdict: %v\n", err)
		os.Exit(2)
	}
	document = append(document, '\n')
	if *out != "" {
		if err := os.WriteFile(*out, document, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "v6-gate: write %s: %v\n", *out, err)
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

	fmt.Printf("v6-gate verdict: %s\n", report.Verdict)
	if report.Verdict != v6gate.VerdictPass {
		os.Exit(1)
	}
}
