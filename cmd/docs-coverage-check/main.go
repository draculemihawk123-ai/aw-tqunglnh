// Command docs-coverage-check runs the V1-00C pre-V1 aggregate gate
// (docs/design/03-v1-alpha-foundation.md V1-00C): it parses the repository's
// own doc/design files and fails with a non-zero exit code the moment any
// SourceRef/coverage debt exists, printing every violation it found so a
// human never has to recount by hand. It never modifies any file.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/taQuangLing/agent-workflow/internal/docscoverage"
)

func main() {
	repoRoot := flag.String("repo-root", ".", "path to the repository root")
	flag.Parse()

	report, err := docscoverage.Run(*repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docs-coverage-check: %v\n", err)
		os.Exit(2)
	}

	if report.Debt() == 0 {
		fmt.Println("docs-coverage-check: debt = 0 (all criteria labeled, all ALPHA_MUST owned, all Nguồn fields present and resolvable, all SPKs mapped)")
		return
	}

	fmt.Printf("docs-coverage-check: debt = %d\n\n", report.Debt())
	byRule := map[string][]docscoverage.Violation{}
	for _, v := range report.Violations {
		byRule[v.Rule] = append(byRule[v.Rule], v)
	}
	ruleNames := map[string]string{
		"a": "(a) criterion missing a phase label",
		"b": "(b) ALPHA_MUST criterion has no owner",
		"c": "(c) V1..V8 task missing Nguồn",
		"d": "(d) SourceRef token fails grammar or does not resolve",
		"e": "(e) criterion carries more than one phase label",
		"f": "(f) NOT_APPLICABLE missing authority reason",
		"g": "(g) SPK not mapped to any criterion",
	}
	for _, rule := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		vs := byRule[rule]
		if len(vs) == 0 {
			continue
		}
		fmt.Printf("%s — %d finding(s)\n", ruleNames[rule], len(vs))
		for _, v := range vs {
			fmt.Printf("  - %s\n", v.Detail)
		}
		fmt.Println()
	}
	os.Exit(1)
}
