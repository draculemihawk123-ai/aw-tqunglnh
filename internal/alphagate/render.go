package alphagate

import (
	"fmt"
	"strings"
)

// Markdown renders the assessment for a human (the CI step summary). The JSON
// is the authoritative artifact; this is its readable projection.
func (a Assessment) Markdown() string {
	var b strings.Builder
	verdict := "gatePass = **false**"
	if a.GatePass {
		verdict = "gatePass = **true**"
	}
	fmt.Fprintf(&b, "## V8-11 Alpha release acceptance — %s (verdict hint: %s)\n\n", verdict, a.VerdictHint)
	fmt.Fprintf(&b, "Commit `%s`\n\n", a.Commit)

	s := a.Summary
	b.WriteString("| Area | Total | PASS | FAIL | CHƯA ĐỦ EVIDENCE |\n|---|---:|---:|---:|---:|\n")
	row := func(name string, c Counts) {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", name, c.Total, c.Pass, c.Fail, c.Insufficient)
	}
	row("ALPHA_MUST criteria", s.AlphaMust)
	row("Alpha-gated criteria (ALPHA_MUST + CROSS_PHASE_GUARD)", s.AlphaGated)
	row("System journeys", s.Journeys)
	row("Final gates", s.FinalGates)
	row("Version gates (V0–V8)", s.VersionGates)
	fmt.Fprintf(&b, "\nEvidence level of the Alpha-gated criteria: %d cited by at least one test, %d covered at suite level only. Outside Alpha scope (Beta-labeled): %d. Not applicable: %d. ADRs with no owner task (informational): %d.\n\n",
		s.TestLevelEvidence, s.SuiteLevelOnly, s.OutOfAlphaScope, s.NotApplicable, s.DecisionsNoOwner)

	if len(a.Blockers) == 0 {
		b.WriteString("No blockers.\n")
		return b.String()
	}
	b.WriteString("### Blockers\n\n| Kind | ID | Status | Detail |\n|---|---|---|---|\n")
	const maxRows = 60
	for i, blocker := range a.Blockers {
		if i == maxRows {
			fmt.Fprintf(&b, "| … | … | … | %d more in alpha-assessment.json |\n", len(a.Blockers)-maxRows)
			break
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", blocker.Kind, blocker.ID, blocker.Status, strings.ReplaceAll(blocker.Detail, "|", `\|`))
	}
	return b.String()
}
