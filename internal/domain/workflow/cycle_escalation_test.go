package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// V11-03: every node that carries a CyclePolicy and lies on a cycle must have an
// escalation edge that leaves that cycle — not just one node of the cycle.
//
// Observed with wf-task-delivery-plus: `simplify` (CyclePolicy, escalation
// outcome "escalated") had its escalation edge pointing at `quality`, a node of
// the same loop. Another node of the loop (`build`) escaped properly, so the
// compiler accepted the document ("some node of the cycle escapes"); at run time
// the first time `simplify` ran out of iterations advance.go refused the edge
// (ErrCycleEscalationNotBounded) after the node before it had finished.
func escalationDocument(simplifyEscalatesTo string) WorkflowDocument {
	return WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
			{
				Key: "build", Type: NodeAgent, Outcomes: []string{"done", "escalated"}, Agent: testAgentNodeConfig(),
				CyclePolicy: &CyclePolicy{MaxIterations: 3, EscalationOutcome: "escalated"},
			},
			{Key: "gate", Type: NodeCommand, Outcomes: []string{"passed", "large", "failed"}, Command: testCommandNodeConfig()},
			{
				Key: "simplify", Type: NodeAgent, Outcomes: []string{"done", "escalated"}, Agent: testAgentNodeConfig(),
				CyclePolicy: &CyclePolicy{MaxIterations: 2, EscalationOutcome: "escalated"},
			},
			{Key: "end", Type: NodeEnd},
		},
		Edges: []Edge{
			{Key: "start-build", From: "start", Outcome: "next", To: "build"},
			{Key: "build-gate", From: "build", Outcome: "done", To: "gate"},
			{Key: "build-escape", From: "build", Outcome: "escalated", To: "end"},
			{Key: "gate-end", From: "gate", Outcome: "passed", To: "end"},
			{Key: "gate-simplify", From: "gate", Outcome: "large", To: "simplify"},
			{Key: "gate-build", From: "gate", Outcome: "failed", To: "build"},
			{Key: "simplify-gate", From: "simplify", Outcome: "done", To: "gate"},
			{Key: "simplify-escalated", From: "simplify", Outcome: "escalated", To: simplifyEscalatesTo},
		},
	}
}

func TestValidateDocument_RejectsACyclePolicyWhoseEscalationEdgeStaysInsideItsCycle(t *testing.T) {
	t.Parallel()

	err := ValidateDocument(escalationDocument("gate"))
	if err == nil {
		t.Fatal("a document whose simplify escalation edge points back into its own loop must not publish")
	}
	for _, want := range []string{"simplify", "simplify-escalated", "gate", "outside"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q (the node, the edge, the target and the way out)", err, want)
		}
	}
}

func TestValidateDocument_AcceptsEveryCyclePolicyEscapingItsCycle(t *testing.T) {
	t.Parallel()

	if err := ValidateDocument(escalationDocument("end")); err != nil {
		t.Fatalf("every CyclePolicy node escapes its loop, the document must publish: %v", err)
	}
}

// A CyclePolicy on a node that is on no cycle is harmless and stays valid, whatever
// its escalation edge points at.
func TestValidateDocument_CyclePolicyOutsideAnyCycleIsNotChecked(t *testing.T) {
	t.Parallel()

	document := WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
			{
				Key: "agent", Type: NodeAgent, Outcomes: []string{"done", "escalated"}, Agent: testAgentNodeConfig(),
				CyclePolicy: &CyclePolicy{MaxIterations: 2, EscalationOutcome: "escalated"},
			},
			{Key: "end", Type: NodeEnd},
		},
		Edges: []Edge{
			{Key: "start-agent", From: "start", Outcome: "next", To: "agent"},
			{Key: "agent-end", From: "agent", Outcome: "done", To: "end"},
			{Key: "agent-escalated", From: "agent", Outcome: "escalated", To: "end"},
		},
	}
	if err := ValidateDocument(document); err != nil {
		t.Fatalf("a CyclePolicy on a node that is on no cycle must stay valid: %v", err)
	}
}

// The workflows the kit ships keep every escalation edge outside its loop. Only
// the graph is read from the template (nodes, outcomes, cyclePolicy, edges); its
// $ref placeholders are not resolvable here, so node configuration problems are
// ignored and only the cycle rule is asserted.
func TestKitWorkflowsKeepEveryEscalationEdgeOutsideItsCycle(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "kit", "workflows", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("kit workflows not found (err=%v, matches=%d)", err, len(paths))
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var template struct {
			Nodes []struct {
				Key         string       `json:"key"`
				Type        NodeType     `json:"type"`
				Outcomes    []string     `json:"outcomes"`
				CyclePolicy *CyclePolicy `json:"cyclePolicy"`
			} `json:"nodes"`
			Edges []Edge `json:"edges"`
		}
		if err := json.Unmarshal(raw, &template); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		document := WorkflowDocument{SchemaVersion: "1", Edges: template.Edges}
		for _, node := range template.Nodes {
			document.Nodes = append(document.Nodes, Node{Key: node.Key, Type: node.Type, Outcomes: node.Outcomes, CyclePolicy: node.CyclePolicy})
		}
		if err := ValidateDocument(document); err != nil && strings.Contains(err.Error(), "cyclePolicy escalation edge") {
			t.Errorf("%s: %v", filepath.Base(path), err)
		}
	}
}
