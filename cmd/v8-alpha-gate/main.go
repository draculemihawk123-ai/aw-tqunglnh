// Command v8-alpha-gate is V8-11's Alpha release acceptance gate
// (docs/design/10-v8-alpha-hardening.md V8-11): it aggregates the repository's
// coverage map, its test sources, CI's per-suite results and the final-gate
// test run into one assessment matrix, writes it as JSON (and optionally
// Markdown), and exits non-zero unless `gatePass` is true. It modifies
// nothing in the repository.
//
// The assessment is written even when the gate does not pass — V8-12's
// verdict consumes it, and a verdict task must be able to run when the gate
// fails (00-roadmap.md §3).
//
//	go run ./cmd/v8-alpha-gate --repo-root . --commit "$GITHUB_SHA" \
//	    --needs-json needs.json --final-gate-tests final-gates.jsonl \
//	    --git-diff-check success --out alpha-assessment.json --markdown-out alpha-assessment.md
//
// Exit codes: 0 gatePass=true (or gatePass=false with --enforce=false), 1
// gatePass=false, 2 the tool itself failed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/taQuangLing/agent-workflow/internal/alphagate"
)

func main() {
	repoRoot := flag.String("repo-root", ".", "path to the repository root")
	commit := flag.String("commit", "", "the revision being assessed (recorded in the output)")
	needsJSON := flag.String("needs-json", "", "file holding GitHub's `needs` context as JSON ({\"<job id>\": {\"result\": \"success\"}, ...})")
	finalGateTests := flag.String("final-gate-tests", "", "file holding the `go test -json` output of the final-gate run")
	gitDiffCheck := flag.String("git-diff-check", "", "result of the `git diff --check` step: success or failure")
	out := flag.String("out", "", "path to write the assessment JSON to (required)")
	markdownOut := flag.String("markdown-out", "", "optional path to write a Markdown summary to")
	enforce := flag.Bool("enforce", true, "exit 1 when gatePass is false; --enforce=false still writes the full assessment and reports gatePass but exits 0 (CI uses this until the release verdict flips the gate to enforcing)")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "v8-alpha-gate: --out is required")
		os.Exit(2)
	}

	input, err := alphagate.Load(alphagate.Options{
		RepoRoot: *repoRoot, Commit: *commit, NeedsJSON: *needsJSON,
		FinalGateTests: *finalGateTests, GitDiffCheck: *gitDiffCheck,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "v8-alpha-gate: %v\n", err)
		os.Exit(2)
	}
	assessment := alphagate.Assess(input)

	document, err := json.MarshalIndent(assessment, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "v8-alpha-gate: marshal assessment: %v\n", err)
		os.Exit(2)
	}
	document = append(document, '\n')
	if err := os.WriteFile(*out, document, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "v8-alpha-gate: write %s: %v\n", *out, err)
		os.Exit(2)
	}
	if *markdownOut != "" {
		if err := os.WriteFile(*markdownOut, []byte(assessment.Markdown()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "v8-alpha-gate: write %s: %v\n", *markdownOut, err)
			os.Exit(2)
		}
	}

	fmt.Print(assessment.Markdown())
	fmt.Printf("\nv8-alpha-gate: gatePass=%v verdictHint=%s\n", assessment.GatePass, assessment.VerdictHint)
	if !assessment.GatePass && *enforce {
		os.Exit(1)
	}
}
