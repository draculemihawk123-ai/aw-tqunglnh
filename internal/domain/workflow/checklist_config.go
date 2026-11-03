package workflow

import (
	"fmt"
	"strings"
)

// MaxChecklistItems bounds an APPROVAL checklist (manual items plus acceptance criteria).
const MaxChecklistItems = 50

// ApprovalChecklistConfig (V12-31, ADR-063) turns an APPROVAL node into a QA checklist: the approver marks each item
// passed or failed (a note, images), and the decision's outcome follows the items — PassedOutcome when every item passed,
// FailedOutcome otherwise. The role, timeout, escalation and release of the node apply unchanged. A node without it (nil) keeps
// its hash and behavior.
type ApprovalChecklistConfig struct {
	// FromAcceptanceCriteria adds one item per acceptance criterion of the run's work item (the task under QA).
	FromAcceptanceCriteria bool `json:"fromAcceptanceCriteria,omitempty"`
	// Items are extra items checked by hand.
	Items []string `json:"items,omitempty"`
	// PassedOutcome and FailedOutcome are declared outcomes of the node.
	PassedOutcome string `json:"passedOutcome"`
	FailedOutcome string `json:"failedOutcome"`
}

func (c *ApprovalChecklistConfig) clone() *ApprovalChecklistConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.Items = append([]string(nil), c.Items...)
	return &cloned
}

// OutcomeFor returns the outcome a checklist with these results leads to.
func (c *ApprovalChecklistConfig) OutcomeFor(allPassed bool) string {
	if allPassed {
		return c.PassedOutcome
	}
	return c.FailedOutcome
}

func validateApprovalChecklistFields(node Node) []string {
	cfg := node.Approval.Checklist
	if cfg == nil {
		return nil
	}
	var problems []string
	for _, outcome := range []struct{ name, value string }{{"passedOutcome", cfg.PassedOutcome}, {"failedOutcome", cfg.FailedOutcome}} {
		if outcome.value == "" {
			problems = append(problems, fmt.Sprintf("node %q approval checklist must declare %s", node.Key, outcome.name))
		} else if !contains(node.Outcomes, outcome.value) {
			problems = append(problems, fmt.Sprintf("node %q approval checklist %s %q is not declared by the node", node.Key, outcome.name, outcome.value))
		}
	}
	if cfg.PassedOutcome != "" && cfg.PassedOutcome == cfg.FailedOutcome {
		problems = append(problems, fmt.Sprintf("node %q approval checklist passedOutcome and failedOutcome must differ", node.Key))
	}
	if !cfg.FromAcceptanceCriteria && len(cfg.Items) == 0 {
		problems = append(problems, fmt.Sprintf("node %q approval checklist needs items or fromAcceptanceCriteria", node.Key))
	}
	if len(cfg.Items) > MaxChecklistItems {
		problems = append(problems, fmt.Sprintf("node %q approval checklist has more than %d items", node.Key, MaxChecklistItems))
	}
	seen := map[string]struct{}{}
	for _, item := range cfg.Items {
		if item == "" || item != strings.TrimSpace(item) || len(item) > 500 {
			problems = append(problems, fmt.Sprintf("node %q approval checklist has an invalid item %q", node.Key, item))
			continue
		}
		if _, duplicate := seen[item]; duplicate {
			problems = append(problems, fmt.Sprintf("node %q approval checklist has duplicate item %q", node.Key, item))
		}
		seen[item] = struct{}{}
	}
	return problems
}
