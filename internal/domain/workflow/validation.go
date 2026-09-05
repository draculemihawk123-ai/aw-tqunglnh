package workflow

import (
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
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
		case NodeAgent, NodeCommand, NodeMachineGate, NodeApproval, NodeWait, NodeRouter, NodeFork, NodeJoin:
		default:
			problems = append(problems, fmt.Sprintf("node %q has unsupported type %q", node.Key, node.Type))
		}
		problems = append(problems, validateNodeConfig(node)...)

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
		// ROUTER may declare exactly one outcome for Alpha (correction
		// found during V4-03 review, docs/design/06-v4-runtime-engine.md):
		// go-core-spec §6 describes ROUTER as picking an outcome "từ typed
		// state bằng rule deterministic", but no ADR or authoring schema
		// (this package's own Node/AgentNodeConfig-sibling types) defines
		// what that rule actually is or where it would be declared — V2-08
		// never gave ROUTER any typed config of its own precisely because
		// that decision was left open. Publishing a ROUTER with two or
		// more declared outcomes today would compile successfully but can
		// never route at runtime: nothing in this codebase can ever
		// produce that outcome (internal/app/runtime.AdvanceRun only
		// auto-resolves a node with exactly one declared outcome, by
		// design — see that file's own doc comment), so the run
		// permanently deadlocks at that node with no error surfaced at
		// publish time. Rejecting it here, at compile time, turns that
		// silent runtime deadlock into an immediate, actionable publish
		// error. A future ADR introducing a real, typed RouterRule can
		// relax this the same way it would have to define the rule format
		// in the first place — this is not a permanent ceiling, just the
		// honest Alpha boundary given no rule exists to author yet.
		if node.Type == NodeRouter && len(node.Outcomes) > 1 {
			problems = append(problems, fmt.Sprintf("node %q is a ROUTER with %d declared outcomes, but Alpha has no typed rule to choose between them — ROUTER may declare exactly one outcome", node.Key, len(node.Outcomes)))
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
	problems = append(problems, validateForkJoinTopology(nodes, outgoing)...)
	problems = append(problems, validateSharedState(document.SharedState, nodes)...)
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

// CycleMembership groups every node key in document by which strongly-
// connected component (Tarjan) it belongs to — two node keys mapping to
// the same component index are mutually reachable from each other.
// Exported for internal/app/runtime's own V4-07 defensive runtime check: a
// CyclePolicy's own EscalationOutcome edge is proven, at PUBLISH time
// (validateBoundedCycles, this same file), to lead somewhere outside its
// own cycle — but the runtime enforcement path re-verifies that exact fact
// again before ever trusting an escalation edge as a forced-escape route,
// the same "corrupted/foreign WorkflowVersion fails closed rather than
// panicking" discipline advance.go's own ErrRouteNotFound already
// documents. Re-deriving this from the compiled document, rather than
// caching the compiler's own componentization somewhere, means this can
// never drift from what validateBoundedCycles itself already computed for
// the exact same document.
func CycleMembership(document WorkflowDocument) map[string]int {
	nodes := make(map[string]Node, len(document.Nodes))
	for _, node := range document.Nodes {
		nodes[node.Key] = node
	}
	outgoing := make(map[string][]Edge, len(document.Nodes))
	for _, edge := range document.Edges {
		if _, ok := nodes[edge.From]; !ok {
			continue
		}
		if _, ok := nodes[edge.To]; !ok {
			continue
		}
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}
	components := stronglyConnectedComponents(nodes, outgoing)
	membership := make(map[string]int, len(nodes))
	for idx, component := range components {
		for _, key := range component {
			membership[key] = idx
		}
	}
	return membership
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

// validateForkJoinTopology is V2-09's own "fork-join" graph-topology
// check (docs/design/04-v2-definition-plane.md V2-09, HE-14-M06),
// explicitly deferred here by both JoinNodeConfig's and Node's own doc
// comments in workflow.go/node_config.go: every branch a FORK fans out
// must reconverge at exactly one JOIN, and that JOIN's QuorumCount (for
// Mode QUORUM) must be between 1 and the FORK's actual branch count —
// the cross-check V2-08's own validateJoinConfig explicitly declined to
// make since it has no access to the FORK that owns a given JOIN.
//
// Scope, deliberately narrowed: nested FORK/JOIN pairs (a branch itself
// containing another FORK before reconverging at the outer JOIN) are
// rejected as unsupported rather than resolved — correctly matching
// nested fork/join topology (like balanced parentheses) is materially
// more complex than Alpha's own graph authoring needs justify building
// right now, and rejecting with a clear message is safer than a
// half-correct nested implementation that silently mishandles some
// topologies. A JOIN is also required to be the reconvergence point of
// at most one FORK — two different forks both routing branches into the
// same JOIN is exactly the "ambiguous branch identity" HE-14-M06 names
// as a case to reject.
func validateForkJoinTopology(nodes map[string]Node, outgoing map[string][]Edge) []string {
	problems := make([]string, 0)
	joinOwners := make(map[string][]string, 8)

	forkKeys := make([]string, 0)
	for key, node := range nodes {
		if node.Type == NodeFork {
			forkKeys = append(forkKeys, key)
		}
	}
	sort.Strings(forkKeys)

	for _, forkKey := range forkKeys {
		branches := outgoing[forkKey]
		if len(branches) == 0 {
			problems = append(problems, fmt.Sprintf("fork %q has no outgoing branches", forkKey))
			continue
		}

		reachedJoins := make(map[string]bool, 1)
		nestedFork := false
		deadEnd := false
		for _, branch := range branches {
			joins, hitNestedFork, hitDeadEnd := firstJoinsReachableFromBranch(branch.To, nodes, outgoing)
			if hitNestedFork {
				nestedFork = true
			}
			if hitDeadEnd {
				deadEnd = true
			}
			for join := range joins {
				reachedJoins[join] = true
			}
		}

		switch {
		case nestedFork:
			problems = append(problems, fmt.Sprintf("fork %q has a branch containing another fork before reconverging — nested fork/join is not supported", forkKey))
		case deadEnd:
			problems = append(problems, fmt.Sprintf("fork %q has a branch that reaches END without reconverging at a join", forkKey))
		case len(reachedJoins) == 0:
			problems = append(problems, fmt.Sprintf("fork %q branches never reach a join", forkKey))
		case len(reachedJoins) > 1:
			joinList := make([]string, 0, len(reachedJoins))
			for join := range reachedJoins {
				joinList = append(joinList, join)
			}
			sort.Strings(joinList)
			problems = append(problems, fmt.Sprintf("fork %q branches converge at more than one join: %s", forkKey, strings.Join(joinList, ", ")))
		default:
			var joinKey string
			for join := range reachedJoins {
				joinKey = join
			}
			joinOwners[joinKey] = append(joinOwners[joinKey], forkKey)
			if joinNode, ok := nodes[joinKey]; ok && joinNode.Join != nil && joinNode.Join.Mode == JoinModeQuorum {
				if int(joinNode.Join.QuorumCount) > len(branches) {
					problems = append(problems, fmt.Sprintf(
						"join %q quorum count %d exceeds its owning fork %q's branch count %d",
						joinKey, joinNode.Join.QuorumCount, forkKey, len(branches),
					))
				}
			}
		}
	}

	joinKeys := make([]string, 0, len(joinOwners))
	for join := range joinOwners {
		joinKeys = append(joinKeys, join)
	}
	sort.Strings(joinKeys)
	for _, join := range joinKeys {
		owners := joinOwners[join]
		if len(owners) > 1 {
			sort.Strings(owners)
			problems = append(problems, fmt.Sprintf(
				"join %q receives branches from more than one fork (%s) — branch identity is ambiguous",
				join, strings.Join(owners, ", "),
			))
		}
	}

	return problems
}

// firstJoinsReachableFromBranch walks forward from start, stopping at
// (not past) the first JOIN node reached along each path — the set of
// stop-points for one FORK branch. It reports hitNestedFork if the walk
// reaches another FORK before any JOIN (validateForkJoinTopology treats
// this as an unsupported nested topology), and hitDeadEnd if it reaches
// an END node without ever reaching a JOIN.
func firstJoinsReachableFromBranch(start string, nodes map[string]Node, outgoing map[string][]Edge) (joins map[string]bool, hitNestedFork, hitDeadEnd bool) {
	visited := make(map[string]bool)
	joins = make(map[string]bool)
	var walk func(key string)
	walk = func(key string) {
		if visited[key] {
			return
		}
		visited[key] = true
		node, ok := nodes[key]
		if !ok {
			return
		}
		switch node.Type {
		case NodeJoin:
			joins[key] = true
			return
		case NodeFork:
			hitNestedFork = true
			return
		case NodeEnd:
			hitDeadEnd = true
			return
		}
		for _, edge := range outgoing[key] {
			walk(edge.To)
		}
	}
	walk(start)
	return joins, hitNestedFork, hitDeadEnd
}

// expectedNodeConfigField is which typed config field name (used in
// error messages) validateNodeConfig requires populated for each
// NodeType that carries one. A NodeType absent from this map — START,
// END, ROUTER, FORK — is purely structural: it must have none of Node's
// six typed config pointers populated (see Node's own doc comment for
// why ROUTER and FORK in particular carry no config of their own).
var expectedNodeConfigField = map[NodeType]string{
	NodeAgent:       "agent",
	NodeCommand:     "command",
	NodeMachineGate: "machineGate",
	NodeApproval:    "approval",
	NodeWait:        "wait",
	NodeJoin:        "join",
}

// presentNodeConfigFields reports which of node's six typed config
// pointers are non-nil, by their JSON field name — used both to check a
// node declares exactly the one config its Type expects, and in error
// messages when it does not.
func presentNodeConfigFields(node Node) []string {
	present := make([]string, 0, 1)
	if node.Agent != nil {
		present = append(present, "agent")
	}
	if node.Command != nil {
		present = append(present, "command")
	}
	if node.MachineGate != nil {
		present = append(present, "machineGate")
	}
	if node.Approval != nil {
		present = append(present, "approval")
	}
	if node.Wait != nil {
		present = append(present, "wait")
	}
	if node.Join != nil {
		present = append(present, "join")
	}
	return present
}

// validateNodeConfig enforces HE-14-M02's typed node contract at the
// Go-struct level: a node must declare exactly the one typed config
// field its Type expects (or none, for the purely structural types),
// and whichever config is populated must itself be internally valid.
func validateNodeConfig(node Node) []string {
	problems := make([]string, 0)
	present := presentNodeConfigFields(node)
	expected, needsConfig := expectedNodeConfigField[node.Type]

	switch {
	case needsConfig && (len(present) != 1 || present[0] != expected):
		problems = append(problems, fmt.Sprintf(
			"node %q of type %q must declare exactly a %q config, found %v",
			node.Key, node.Type, expected, present,
		))
	case !needsConfig && len(present) != 0:
		problems = append(problems, fmt.Sprintf(
			"node %q of type %q must not declare a node config, found %v",
			node.Key, node.Type, present,
		))
	}

	switch {
	case node.Agent != nil:
		problems = append(problems, validateExecutorPin(node.Agent.ProfileRef, definition.KindAgentProfile, "agent.profileRef", node.Key)...)
		problems = append(problems, validatePolicyRefs(node.Agent.PolicyRefs, "agent.policyRefs", node.Key)...)
		if node.Agent.AdapterBuildID != nil && strings.TrimSpace(*node.Agent.AdapterBuildID) == "" {
			problems = append(problems, fmt.Sprintf("node %q agent.adapterBuildId must not be blank when present", node.Key))
		}
	case node.Command != nil:
		problems = append(problems, validateExecutorPin(node.Command.CommandRef, definition.KindCommand, "command.commandRef", node.Key)...)
		problems = append(problems, validatePolicyRefs(node.Command.PolicyRefs, "command.policyRefs", node.Key)...)
	case node.MachineGate != nil:
		problems = append(problems, validateExecutorPin(node.MachineGate.GateRef, definition.KindGate, "machineGate.gateRef", node.Key)...)
		problems = append(problems, validatePolicyRefs(node.MachineGate.PolicyRefs, "machineGate.policyRefs", node.Key)...)
	case node.Approval != nil:
		problems = append(problems, validateApprovalConfig(node)...)
	case node.Wait != nil:
		problems = append(problems, validateWaitConfig(node)...)
	case node.Join != nil:
		problems = append(problems, validateJoinConfig(node)...)
	}

	return problems
}

// validateExecutorPin checks one executable node config's
// definition.DependencyPin: DefinitionID/VersionID must be present (a
// pin naming no exact version could never be resolved), and Kind must
// be exactly expectedKind — the same "pins exactly one Kind" rule this
// task's own design guidance names per node type (AGENT pins
// AGENT_PROFILE, COMMAND pins COMMAND, MACHINE_GATE pins GATE).
func validateExecutorPin(pin definition.DependencyPin, expectedKind definition.Kind, path, nodeKey string) []string {
	problems := make([]string, 0)
	if strings.TrimSpace(pin.DefinitionID) == "" {
		problems = append(problems, fmt.Sprintf("node %q %s.definitionId is required", nodeKey, path))
	}
	if strings.TrimSpace(pin.VersionID) == "" {
		problems = append(problems, fmt.Sprintf("node %q %s.versionId is required", nodeKey, path))
	}
	if pin.Kind != expectedKind {
		problems = append(problems, fmt.Sprintf("node %q %s.kind must be %q, got %q", nodeKey, path, expectedKind, pin.Kind))
	}
	return problems
}

// validatePolicyRefs checks a node config's PolicyRefs the same way
// internal/domain/block.validatePolicyRefs already checks BlockDocument
// PolicyRefs: every pin must name an exact DefinitionID+VersionID, every
// pin's Kind must be definition.KindPolicy, and no DefinitionID may
// repeat.
func validatePolicyRefs(refs []definition.DependencyPin, path, nodeKey string) []string {
	problems := make([]string, 0)
	seen := make(map[string]struct{}, len(refs))
	for i, ref := range refs {
		fieldPath := fmt.Sprintf("%s[%d]", path, i)
		if strings.TrimSpace(ref.DefinitionID) == "" || strings.TrimSpace(ref.VersionID) == "" {
			problems = append(problems, fmt.Sprintf("node %q %s is missing definitionId or versionId", nodeKey, fieldPath))
			continue
		}
		if ref.Kind != definition.KindPolicy {
			problems = append(problems, fmt.Sprintf("node %q %s.kind must be POLICY, got %q", nodeKey, fieldPath, ref.Kind))
		}
		if _, duplicate := seen[ref.DefinitionID]; duplicate {
			problems = append(problems, fmt.Sprintf("node %q duplicate policy ref %q at %s", nodeKey, ref.DefinitionID, fieldPath))
		}
		seen[ref.DefinitionID] = struct{}{}
	}
	return problems
}

// validateApprovalConfig checks an APPROVAL node's HE-14-S03-derived
// config: at least one authorized role, a positive timeout, an escalation
// outcome that names one of this node's own declared Outcomes, and no
// duplicate/empty entries anywhere.
//
// EscalationOutcome is required now (V4-09, correction found while
// building this task's own real consumer — the field's own doc comment
// used to call it merely optional, "not every approval needs an
// escalation path"): TimeoutSeconds is itself already required and
// positive precisely because "an approval with no timeout could wait
// forever" is the failure mode this schema exists to prevent, so a
// declared timeout with nowhere to route once it actually fires would
// silently reintroduce that exact same unbounded wait the moment
// TimeoutSeconds elapses — V4-09's own timer has to route somewhere,
// deterministically, and there is no "standardized timeout outcome"
// convention anywhere in this codebase for it to fall back to (confirmed
// by search before making this change) for it to lean on instead.
func validateApprovalConfig(node Node) []string {
	problems := make([]string, 0)
	cfg := node.Approval

	if len(cfg.AuthorizedRoles) == 0 {
		problems = append(problems, fmt.Sprintf("node %q approval must declare at least one authorized role", node.Key))
	}
	seenRoles := make(map[string]struct{}, len(cfg.AuthorizedRoles))
	for _, role := range cfg.AuthorizedRoles {
		if role == "" || role != strings.TrimSpace(role) {
			problems = append(problems, fmt.Sprintf("node %q approval has invalid authorized role %q", node.Key, role))
			continue
		}
		if _, duplicate := seenRoles[role]; duplicate {
			problems = append(problems, fmt.Sprintf("node %q approval has duplicate authorized role %q", node.Key, role))
		}
		seenRoles[role] = struct{}{}
	}

	if cfg.TimeoutSeconds == 0 {
		problems = append(problems, fmt.Sprintf("node %q approval timeout must be greater than zero", node.Key))
	}

	if cfg.EscalationOutcome == "" {
		problems = append(problems, fmt.Sprintf("node %q approval must declare an escalation outcome", node.Key))
	} else if !contains(node.Outcomes, cfg.EscalationOutcome) {
		problems = append(problems, fmt.Sprintf("node %q approval escalation outcome %q is not declared", node.Key, cfg.EscalationOutcome))
	}

	seenEvidence := make(map[string]struct{}, len(cfg.RequestedEvidenceKinds))
	for _, kind := range cfg.RequestedEvidenceKinds {
		if kind == "" || kind != strings.TrimSpace(kind) {
			problems = append(problems, fmt.Sprintf("node %q approval has invalid requested evidence kind %q", node.Key, kind))
			continue
		}
		if _, duplicate := seenEvidence[kind]; duplicate {
			problems = append(problems, fmt.Sprintf("node %q approval has duplicate requested evidence kind %q", node.Key, kind))
		}
		seenEvidence[kind] = struct{}{}
	}

	return problems
}

// validateWaitConfig checks a WAIT node's config: exactly one of
// DurationSeconds (Mode DURATION) or SignalName (Mode SIGNAL) is set,
// consistent with the Mode declared, per WaitNodeConfig's own doc
// comment — plus (V4-08) CompletionOutcome/TimeoutOutcome, which must
// each name a declared Outcome when set, must stay empty for a completion
// path that can never fire (TimeoutOutcome under DURATION, or under
// SIGNAL with no TimeoutSeconds ceiling), and must be explicitly pinned
// the moment more than one Outcome makes the "which one fires" question
// genuinely ambiguous.
func validateWaitConfig(node Node) []string {
	problems := make([]string, 0)
	cfg := node.Wait

	switch cfg.Mode {
	case WaitModeDuration:
		if cfg.DurationSeconds == 0 {
			problems = append(problems, fmt.Sprintf("node %q wait duration must be greater than zero for mode DURATION", node.Key))
		}
		if cfg.SignalName != "" {
			problems = append(problems, fmt.Sprintf("node %q wait signal name must be empty for mode DURATION", node.Key))
		}
		if cfg.TimeoutSeconds != 0 {
			problems = append(problems, fmt.Sprintf("node %q wait timeout must be zero for mode DURATION; duration is already its own ceiling", node.Key))
		}
		if cfg.TimeoutOutcome != "" {
			problems = append(problems, fmt.Sprintf("node %q wait timeout outcome must be empty for mode DURATION; reaching duration is normal completion, not a timeout", node.Key))
		}
	case WaitModeSignal:
		if strings.TrimSpace(cfg.SignalName) == "" {
			problems = append(problems, fmt.Sprintf("node %q wait signal name is required for mode SIGNAL", node.Key))
		}
		if cfg.DurationSeconds != 0 {
			problems = append(problems, fmt.Sprintf("node %q wait duration must be zero for mode SIGNAL", node.Key))
		}
		if cfg.TimeoutSeconds == 0 && cfg.TimeoutOutcome != "" {
			problems = append(problems, fmt.Sprintf("node %q wait timeout outcome must be empty when timeout seconds is zero (no timeout ceiling)", node.Key))
		}
	default:
		problems = append(problems, fmt.Sprintf("node %q has unsupported wait mode %q", node.Key, cfg.Mode))
	}

	if cfg.CompletionOutcome != "" && !contains(node.Outcomes, cfg.CompletionOutcome) {
		problems = append(problems, fmt.Sprintf("node %q wait completion outcome %q is not declared", node.Key, cfg.CompletionOutcome))
	}
	if cfg.TimeoutOutcome != "" && !contains(node.Outcomes, cfg.TimeoutOutcome) {
		problems = append(problems, fmt.Sprintf("node %q wait timeout outcome %q is not declared", node.Key, cfg.TimeoutOutcome))
	}
	if len(node.Outcomes) > 1 {
		if cfg.CompletionOutcome == "" {
			problems = append(problems, fmt.Sprintf("node %q has %d declared outcomes but no wait completion outcome pinned", node.Key, len(node.Outcomes)))
		}
		if cfg.Mode == WaitModeSignal && cfg.TimeoutSeconds > 0 && cfg.TimeoutOutcome == "" {
			problems = append(problems, fmt.Sprintf("node %q has %d declared outcomes and a wait timeout ceiling but no timeout outcome pinned", node.Key, len(node.Outcomes)))
		}
	}

	return problems
}

// validateJoinConfig checks a JOIN node's config: QuorumCount is
// populated if and only if Mode is QUORUM, per JoinNodeConfig's own doc
// comment. It deliberately does not cross-check QuorumCount against this
// JOIN's actual incoming-edge count — see JoinNodeConfig's own doc
// comment for why that is V2-09's job, not V2-08's.
//
// V4-11 (docs/design/06-v4-runtime-engine.md, confirmed with the user
// before writing that task's runtime code): a JOIN must declare EXACTLY
// ONE outcome, never zero and never more than one. The runtime has no
// authority to pick among several — unlike AGENT/COMMAND/MACHINE_GATE
// (a real Attempt reports its own outcome) or WAIT/APPROVAL (a signal/
// operator decision carries one) or ROUTER's own single-outcome-only
// carve-out, a JOIN's own "outcome" is entirely a function of its
// declared policy (ALL/ANY/QUORUM) having been satisfied — there is
// exactly one meaningful event to report, so exactly one outcome is
// declarable. Rejecting this at compile/publish time, rather than
// leaving it for the runtime to discover it cannot resolve an outcome,
// matches this package's own "fail closed at the earliest possible
// point" discipline.
func validateJoinConfig(node Node) []string {
	problems := make([]string, 0)
	cfg := node.Join

	if len(node.Outcomes) != 1 {
		problems = append(problems, fmt.Sprintf("node %q join must declare exactly one outcome, got %d", node.Key, len(node.Outcomes)))
	}

	switch cfg.Mode {
	case JoinModeAll, JoinModeAny:
		if cfg.QuorumCount != 0 {
			problems = append(problems, fmt.Sprintf("node %q join quorum count must be zero for mode %q", node.Key, cfg.Mode))
		}
	case JoinModeQuorum:
		if cfg.QuorumCount == 0 {
			problems = append(problems, fmt.Sprintf("node %q join quorum count must be greater than zero for mode QUORUM", node.Key))
		}
	default:
		problems = append(problems, fmt.Sprintf("node %q has unsupported join mode %q", node.Key, cfg.Mode))
	}

	return problems
}

// validateSharedState checks HE-14-M04's own field-level requirements
// against document's declared shared-state schema: every field has a
// unique name, a supported Type and MergeRule, an Owner that names a
// node this document actually declares (and that also appears in
// Writers), and closed, non-empty, node-key-valid Writers/Readers
// allowlists.
func validateSharedState(fields []SharedStateField, nodes map[string]Node) []string {
	problems := make([]string, 0)
	seenNames := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field.Name == "" || field.Name != strings.TrimSpace(field.Name) {
			problems = append(problems, fmt.Sprintf("shared state field %q has an invalid name", field.Name))
		} else if _, duplicate := seenNames[field.Name]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate shared state field %q", field.Name))
		}
		seenNames[field.Name] = struct{}{}

		if !validSharedStateFieldTypes[field.Type] {
			problems = append(problems, fmt.Sprintf("shared state field %q has unsupported type %q", field.Name, field.Type))
		}
		if !validMergeRules[field.MergeRule] {
			problems = append(problems, fmt.Sprintf("shared state field %q has unsupported merge rule %q", field.Name, field.MergeRule))
		}

		if _, exists := nodes[field.Owner]; field.Owner == "" || !exists {
			problems = append(problems, fmt.Sprintf("shared state field %q owner %q does not reference a declared node", field.Name, field.Owner))
		}

		if len(field.Writers) == 0 {
			problems = append(problems, fmt.Sprintf("shared state field %q must declare at least one writer", field.Name))
		}
		problems = append(problems, validateSharedStateNodeRefs(field.Name, "writer", field.Writers, nodes)...)
		ownerIsWriter := false
		for _, writer := range field.Writers {
			if writer == field.Owner {
				ownerIsWriter = true
				break
			}
		}
		if field.Owner != "" && !ownerIsWriter {
			problems = append(problems, fmt.Sprintf("shared state field %q owner %q must also be listed in writers", field.Name, field.Owner))
		}

		if len(field.Readers) == 0 {
			problems = append(problems, fmt.Sprintf("shared state field %q must declare at least one reader", field.Name))
		}
		problems = append(problems, validateSharedStateNodeRefs(field.Name, "reader", field.Readers, nodes)...)
	}
	return problems
}

// validateSharedStateNodeRefs checks one SharedStateField allowlist
// (Writers or Readers): every entry must name a node this document
// actually declares, and no entry may repeat.
func validateSharedStateNodeRefs(fieldName, role string, keys []string, nodes map[string]Node) []string {
	problems := make([]string, 0)
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			problems = append(problems, fmt.Sprintf("shared state field %q has an empty %s entry", fieldName, role))
			continue
		}
		if _, exists := nodes[key]; !exists {
			problems = append(problems, fmt.Sprintf("shared state field %q %s %q does not reference a declared node", fieldName, role, key))
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			problems = append(problems, fmt.Sprintf("shared state field %q has duplicate %s %q", fieldName, role, key))
		}
		seen[key] = struct{}{}
	}
	return problems
}
