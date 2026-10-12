package workflow

import (
	"fmt"
	"strings"
)

// ReleaseCommitMode says whether an APPROVAL node lets the approver commit the worktree's
// changes in the same action as the decision (V11-07, ADR-040, completing ADR-014).
type ReleaseCommitMode string

const (
	// ReleaseCommitDisabled: the decision never commits (the behavior before V11-07).
	ReleaseCommitDisabled ReleaseCommitMode = "DISABLED"
	// ReleaseCommitOffered: for an outcome listed in ApprovalReleaseConfig.Outcomes the
	// approver may tick "Commit" and choose the files.
	ReleaseCommitOffered ReleaseCommitMode = "OFFERED"
	// ReleaseCommitRequired: for an outcome listed there the decision must carry a commit
	// whenever the worktree has changes to commit; with none, the commit is skipped and
	// recorded as NOTHING_TO_COMMIT instead of failing.
	ReleaseCommitRequired ReleaseCommitMode = "REQUIRED"
)

// ApprovalReleaseConfig is an APPROVAL node's V11-07 release policy. A node without it
// (nil) is DISABLED, so every definition authored before V11-07 keeps its hash and behavior.
type ApprovalReleaseConfig struct {
	Commit ReleaseCommitMode `json:"commit"`
	// Outcomes are the node outcomes that may carry a commit — each must be declared by
	// the node and lead only to END or to nodes that never write to the worktree.
	Outcomes []string `json:"outcomes,omitempty"`
}

func (c *ApprovalReleaseConfig) clone() *ApprovalReleaseConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.Outcomes = append([]string(nil), c.Outcomes...)
	return &cloned
}

// Mode returns the effective commit mode: DISABLED for a nil policy.
func (c *ApprovalReleaseConfig) Mode() ReleaseCommitMode {
	if c == nil || c.Commit == "" {
		return ReleaseCommitDisabled
	}
	return c.Commit
}

// AllowsCommitOn reports whether a decision with outcome may carry a commit under this policy.
func (c *ApprovalReleaseConfig) AllowsCommitOn(outcome string) bool {
	if c.Mode() == ReleaseCommitDisabled {
		return false
	}
	return contains(c.Outcomes, outcome)
}

// validateApprovalReleaseFields checks the policy's own shape (the part that needs no graph).
func validateApprovalReleaseFields(node Node) []string {
	cfg := node.Approval.Release
	if cfg == nil {
		return nil
	}
	var problems []string
	switch cfg.Commit {
	case ReleaseCommitDisabled:
		if len(cfg.Outcomes) > 0 {
			problems = append(problems, fmt.Sprintf("node %q approval release is DISABLED and must not list outcomes", node.Key))
		}
	case ReleaseCommitOffered, ReleaseCommitRequired:
		if len(cfg.Outcomes) == 0 {
			problems = append(problems, fmt.Sprintf("node %q approval release %s must list at least one outcome", node.Key, cfg.Commit))
		}
	default:
		problems = append(problems, fmt.Sprintf("node %q approval release has unsupported commit mode %q", node.Key, cfg.Commit))
	}
	seen := map[string]struct{}{}
	for _, outcome := range cfg.Outcomes {
		if outcome == "" || outcome != strings.TrimSpace(outcome) {
			problems = append(problems, fmt.Sprintf("node %q approval release has invalid outcome %q", node.Key, outcome))
			continue
		}
		if !contains(node.Outcomes, outcome) {
			problems = append(problems, fmt.Sprintf("node %q approval release outcome %q is not declared by the node", node.Key, outcome))
		}
		if _, duplicate := seen[outcome]; duplicate {
			problems = append(problems, fmt.Sprintf("node %q approval release has duplicate outcome %q", node.Key, outcome))
		}
		seen[outcome] = struct{}{}
	}
	return problems
}

// nodeWritesWorktree reports whether a node may change files in the worktree: an AGENT that
// is not a CHECKER, and a COMMAND (which the engine does not hold to read-only). A commit made
// in the background while such a node runs would race with it.
func nodeWritesWorktree(node Node) bool {
	switch node.Type {
	case NodeAgent:
		return node.Agent == nil || node.Agent.EffectiveRole() == AgentRoleMaker
	case NodeCommand:
		return true
	default:
		return false
	}
}

// validateApprovalReleaseRouting rejects a release-carrying outcome whose route can reach a
// node that writes to the worktree: the commit runs in the background after the decision, so
// it must only be offered where nothing else can be writing — the outcome leads to END or
// through read-only nodes only.
func validateApprovalReleaseRouting(nodes map[string]Node, outgoing map[string][]Edge) []string {
	var problems []string
	for _, node := range nodes {
		if node.Type != NodeApproval || node.Approval == nil || node.Approval.Release.Mode() == ReleaseCommitDisabled {
			continue
		}
		for _, outcome := range node.Approval.Release.Outcomes {
			for _, edge := range outgoing[node.Key] {
				if edge.Outcome != outcome {
					continue
				}
				if writer, found := reachableWriter(edge.To, nodes, outgoing); found {
					problems = append(problems, fmt.Sprintf(
						"node %q approval release outcome %q can reach node %q, which may write to the worktree — a commit made at the decision would race with it",
						node.Key, outcome, writer))
				}
			}
		}
	}
	return problems
}

// reachableWriter walks the flow graph from start (inclusive) and returns the first node that
// may write to the worktree.
func reachableWriter(start string, nodes map[string]Node, outgoing map[string][]Edge) (string, bool) {
	visited := map[string]bool{}
	stack := []string{start}
	for len(stack) > 0 {
		key := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[key] {
			continue
		}
		visited[key] = true
		if nodeWritesWorktree(nodes[key]) {
			return key, true
		}
		for _, edge := range outgoing[key] {
			stack = append(stack, edge.To)
		}
	}
	return "", false
}
