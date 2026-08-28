package workflow

import (
	"fmt"
	"sort"
	"strings"
)

type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid workflow graph: " + strings.Join(e.Problems, "; ")
}

func validateNormalizedDocument(document WorkflowDocument) error {
	problems := make([]string, 0)
	if strings.TrimSpace(document.SchemaVersion) == "" {
		problems = append(problems, "schema version is required")
	}

	nodes := make(map[string]Node, len(document.Nodes))
	startKeys := make([]string, 0, 1)
	endKeys := make([]string, 0)
	for _, node := range document.Nodes {
		if node.Key == "" || node.Key != strings.TrimSpace(node.Key) {
			problems = append(problems, fmt.Sprintf("node key %q is invalid", node.Key))
			continue
		}
		if _, duplicate := nodes[node.Key]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate node key %q", node.Key))
			continue
		}
		nodes[node.Key] = node

		switch node.Type {
		case NodeStart:
			startKeys = append(startKeys, node.Key)
		case NodeEnd:
			endKeys = append(endKeys, node.Key)
		case NodeAgent, NodeCommand:
		default:
			problems = append(problems, fmt.Sprintf("node %q has unsupported type %q", node.Key, node.Type))
		}

		seenOutcomes := make(map[string]struct{}, len(node.Outcomes))
		for _, outcome := range node.Outcomes {
			if outcome == "" || outcome != strings.TrimSpace(outcome) {
				problems = append(problems, fmt.Sprintf("node %q has invalid outcome %q", node.Key, outcome))
				continue
			}
			if _, duplicate := seenOutcomes[outcome]; duplicate {
				problems = append(problems, fmt.Sprintf("node %q has duplicate outcome %q", node.Key, outcome))
			}
			seenOutcomes[outcome] = struct{}{}
		}
		if node.Type != NodeEnd && len(node.Outcomes) == 0 {
			problems = append(problems, fmt.Sprintf("node %q must declare at least one outcome", node.Key))
		}
		if node.CyclePolicy != nil {
			if node.CyclePolicy.MaxIterations == 0 {
				problems = append(problems, fmt.Sprintf("node %q cycle max iterations must be greater than zero", node.Key))
			}
			if _, declared := seenOutcomes[node.CyclePolicy.EscalationOutcome]; !declared {
				problems = append(problems, fmt.Sprintf("node %q cycle escalation outcome is not declared", node.Key))
			}
		}
	}
	if len(startKeys) != 1 {
		problems = append(problems, fmt.Sprintf("workflow must contain exactly one START node, found %d", len(startKeys)))
	}
	if len(endKeys) == 0 {
		problems = append(problems, "workflow must contain at least one END node")
	}

	outgoing := make(map[string][]Edge, len(nodes))
	incoming := make(map[string][]Edge, len(nodes))
	edgeKeys := make(map[string]struct{}, len(document.Edges))
	routes := make(map[string]string, len(document.Edges))
	for _, edge := range document.Edges {
		if edge.Key == "" || edge.Key != strings.TrimSpace(edge.Key) {
			problems = append(problems, fmt.Sprintf("edge key %q is invalid", edge.Key))
		} else if _, duplicate := edgeKeys[edge.Key]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate edge key %q", edge.Key))
		}
		edgeKeys[edge.Key] = struct{}{}

		from, fromExists := nodes[edge.From]
		_, toExists := nodes[edge.To]
		if !fromExists {
			problems = append(problems, fmt.Sprintf("edge %q references missing source node %q", edge.Key, edge.From))
		}
		if !toExists {
			problems = append(problems, fmt.Sprintf("edge %q references missing target node %q", edge.Key, edge.To))
		}
		if !fromExists || !toExists {
			continue
		}
		outgoing[edge.From] = append(outgoing[edge.From], edge)
		incoming[edge.To] = append(incoming[edge.To], edge)

		if edge.Outcome == "" {
			problems = append(problems, fmt.Sprintf("edge %q outcome is required", edge.Key))
		} else if !contains(from.Outcomes, edge.Outcome) {
			problems = append(problems, fmt.Sprintf("edge %q uses undeclared outcome %q on node %q", edge.Key, edge.Outcome, edge.From))
		}
		routeKey := edge.From + "\x00" + edge.Outcome
		if previous, duplicate := routes[routeKey]; duplicate {
			problems = append(problems, fmt.Sprintf(
				"node %q outcome %q has duplicate routes %q and %q",
				edge.From,
				edge.Outcome,
				previous,
				edge.Key,
			))
		} else {
			routes[routeKey] = edge.Key
		}
	}

	for key, node := range nodes {
		if node.Type == NodeStart && len(incoming[key]) != 0 {
			problems = append(problems, fmt.Sprintf("START node %q cannot have incoming edges", key))
		}
		if node.Type == NodeEnd && len(outgoing[key]) != 0 {
			problems = append(problems, fmt.Sprintf("END node %q cannot have outgoing edges", key))
		}
		if node.Type == NodeEnd {
			continue
		}
		for _, outcome := range node.Outcomes {
			if _, routed := routes[key+"\x00"+outcome]; !routed {
				problems = append(problems, fmt.Sprintf("node %q outcome %q has no route", key, outcome))
			}
		}
	}

	if len(startKeys) == 1 {
		reachable := walkForward(startKeys[0], outgoing)
		for key := range nodes {
			if !reachable[key] {
				problems = append(problems, fmt.Sprintf("node %q is unreachable from START", key))
			}
		}
	}
	if len(endKeys) > 0 {
		canReachEnd := walkBackward(endKeys, incoming)
		for key := range nodes {
			if !canReachEnd[key] {
				problems = append(problems, fmt.Sprintf("node %q has no path to END", key))
			}
		}
	}

	problems = append(problems, validateBoundedCycles(nodes, outgoing)...)
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return &ValidationError{Problems: problems}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func walkForward(start string, outgoing map[string][]Edge) map[string]bool {
	visited := make(map[string]bool)
	stack := []string{start}
	for len(stack) > 0 {
		last := len(stack) - 1
		key := stack[last]
		stack = stack[:last]
		if visited[key] {
			continue
		}
		visited[key] = true
		for _, edge := range outgoing[key] {
			stack = append(stack, edge.To)
		}
	}
	return visited
}

func walkBackward(ends []string, incoming map[string][]Edge) map[string]bool {
	visited := make(map[string]bool)
	stack := append([]string(nil), ends...)
	for len(stack) > 0 {
		last := len(stack) - 1
		key := stack[last]
		stack = stack[:last]
		if visited[key] {
			continue
		}
		visited[key] = true
		for _, edge := range incoming[key] {
			stack = append(stack, edge.From)
		}
	}
	return visited
}

func validateBoundedCycles(nodes map[string]Node, outgoing map[string][]Edge) []string {
	components := stronglyConnectedComponents(nodes, outgoing)
	problems := make([]string, 0)
	for _, component := range components {
		cyclic := len(component) > 1
		if len(component) == 1 {
			key := component[0]
			for _, edge := range outgoing[key] {
				if edge.To == key {
					cyclic = true
					break
				}
			}
		}
		if !cyclic {
			continue
		}

		members := make(map[string]struct{}, len(component))
		for _, key := range component {
			members[key] = struct{}{}
		}
		bounded := false
		for _, key := range component {
			node := nodes[key]
			if node.CyclePolicy == nil || node.CyclePolicy.MaxIterations == 0 {
				continue
			}
			for _, edge := range outgoing[key] {
				_, targetInCycle := members[edge.To]
				if edge.Outcome == node.CyclePolicy.EscalationOutcome && !targetInCycle {
					bounded = true
					break
				}
			}
			if bounded {
				break
			}
		}
		if !bounded {
			sort.Strings(component)
			problems = append(problems, fmt.Sprintf(
				"cycle [%s] requires a positive iteration budget and escalation route outside the cycle",
				strings.Join(component, ","),
			))
		}
	}
	return problems
}

func stronglyConnectedComponents(nodes map[string]Node, outgoing map[string][]Edge) [][]string {
	index := 0
	indices := make(map[string]int, len(nodes))
	lowLinks := make(map[string]int, len(nodes))
	onStack := make(map[string]bool, len(nodes))
	stack := make([]string, 0, len(nodes))
	components := make([][]string, 0)

	var visit func(string)
	visit = func(key string) {
		indices[key] = index
		lowLinks[key] = index
		index++
		stack = append(stack, key)
		onStack[key] = true

		for _, edge := range outgoing[key] {
			target := edge.To
			if _, seen := indices[target]; !seen {
				visit(target)
				if lowLinks[target] < lowLinks[key] {
					lowLinks[key] = lowLinks[target]
				}
			} else if onStack[target] && indices[target] < lowLinks[key] {
				lowLinks[key] = indices[target]
			}
		}

		if lowLinks[key] != indices[key] {
			return
		}
		component := make([]string, 0)
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == key {
				break
			}
		}
		components = append(components, component)
	}

	keys := make([]string, 0, len(nodes))
	for key := range nodes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, seen := indices[key]; !seen {
			visit(key)
		}
	}
	return components
}
