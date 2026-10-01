package workflow

// V9-02 (ADR-031): the optional failureOutcome of a COMMAND or MACHINE_GATE
// node — canonical encoding and hash stability when absent, and the publish
// rules when present (decision 2: exactly two selectable outcomes; the edge
// leaving failureOutcome follows the existing bounded-cycle rule).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// failureLoopDocument is the workflow ADR-031 exists for:
//
//	start -> build (AGENT) -> check (COMMAND) --passed--> end
//	                ^              |  \--failed--> build        (the loop)
//	                |              \--escalated--> escalated_end (past maxIterations)
//
// check carries the cyclePolicy, so its escalation outcome "escalated" is not
// one of the outcomes the check itself selects.
func failureLoopDocument() WorkflowDocument {
	return WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
			{
				Key: "build", Type: NodeAgent, Outcomes: []string{"done"},
				Agent: &AgentNodeConfig{
					ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-default", VersionID: "v1"},
					Role:       AgentRoleMaker,
				},
			},
			{
				Key: "check", Type: NodeCommand, Outcomes: []string{"passed", "failed", "escalated"},
				CyclePolicy: &CyclePolicy{MaxIterations: 2, EscalationOutcome: "escalated"},
				Command: &CommandNodeConfig{
					CommandRef:     definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "command-test", VersionID: "v1"},
					FailureOutcome: "failed",
				},
			},
			{Key: "end", Type: NodeEnd},
			{Key: "escalated_end", Type: NodeEnd},
		},
		Edges: []Edge{
			{Key: "e-start-build", From: "start", Outcome: "next", To: "build"},
			{Key: "e-build-check", From: "build", Outcome: "done", To: "check"},
			{Key: "e-check-end", From: "check", Outcome: "passed", To: "end"},
			{Key: "e-check-build", From: "check", Outcome: "failed", To: "build"},
			{Key: "e-check-escalated", From: "check", Outcome: "escalated", To: "escalated_end"},
		},
	}
}

// gateFailureLoopDocument is failureLoopDocument with a MACHINE_GATE as the
// check.
func gateFailureLoopDocument() WorkflowDocument {
	document := failureLoopDocument()
	check := findNode(&document, "check")
	check.Type = NodeMachineGate
	check.Command = nil
	check.MachineGate = &MachineGateNodeConfig{
		GateRef:        definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "gate-verify", VersionID: "v1"},
		FailureOutcome: "failed",
	}
	return document
}

func TestFailureOutcome_ValidLoopIsAccepted(t *testing.T) {
	t.Parallel()
	for name, document := range map[string]WorkflowDocument{"COMMAND": failureLoopDocument(), "MACHINE_GATE": gateFailureLoopDocument()} {
		if err := ValidateDocument(document); err != nil {
			t.Fatalf("%s: the build -> check --failed--> build loop with a cyclePolicy should publish: %v", name, err)
		}
	}
}

// TestFailureOutcome_AbsentFieldKeepsCanonicalContentAndHash is the "published
// workflows keep their hashes" bar: a document that never sets failureOutcome
// encodes without the key, so its canonical content — and so its compiled hash
// — is exactly what it was before the field existed. The literal below was
// computed by the build that predates V9-02 for this very document.
func TestFailureOutcome_AbsentFieldKeepsCanonicalContentAndHash(t *testing.T) {
	t.Parallel()
	document := WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
			{
				Key: "test", Type: NodeCommand, Outcomes: []string{"passed"},
				Command: &CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "command-test", VersionID: "v1"},
					PolicyRefs: []definition.DependencyPin{{Kind: definition.KindPolicy, DefinitionID: "attempt-policy", VersionID: "v1"}},
				},
			},
			{
				Key: "gate", Type: NodeMachineGate, Outcomes: []string{"passed"},
				MachineGate: &MachineGateNodeConfig{
					GateRef: definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "gate-verify", VersionID: "v1"},
				},
			},
			{Key: "end", Type: NodeEnd},
		},
		Edges: []Edge{
			{Key: "e-start-test", From: "start", Outcome: "next", To: "test"},
			{Key: "e-test-gate", From: "test", Outcome: "passed", To: "gate"},
			{Key: "e-gate-end", From: "gate", Outcome: "passed", To: "end"},
		},
	}
	version, err := Compile(WorkflowDefinition{ID: "workflow-hash", Name: "Hash", Status: DefinitionDraft, Version: 1}, PublishRequest{
		VersionID: "version-hash-1", VersionNumber: 1, Document: document, PublishedBy: "publisher", PublishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if strings.Contains(string(version.CanonicalContent()), "failureOutcome") {
		t.Fatalf("canonical content mentions failureOutcome for a document that never set it: %s", version.CanonicalContent())
	}
	const wantHash = "sha256:c7e7fc1be36eeffc049883a1da5e8b4bd57fc0e64475b7c9d0f29f57d0e05f6e"
	if version.ContentHash() != wantHash {
		t.Fatalf("ContentHash() = %s, want %s — a workflow without failureOutcome must keep its exact compiled hash", version.ContentHash(), wantHash)
	}
}

// TestFailureOutcome_RoundTripsThroughJSON proves the field is part of the
// document's canonical form when set, and survives CompileJSON's strict decode
// (DisallowUnknownFields) — the path `aw definition publish` takes.
func TestFailureOutcome_RoundTripsThroughJSON(t *testing.T) {
	t.Parallel()
	for name, document := range map[string]WorkflowDocument{"COMMAND": failureLoopDocument(), "MACHINE_GATE": gateFailureLoopDocument()} {
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if !strings.Contains(string(raw), `"failureOutcome":"failed"`) {
			t.Fatalf("%s: encoded document = %s, want failureOutcome to be encoded when set", name, raw)
		}
		version, err := CompileJSON(WorkflowDefinition{ID: "workflow-json", Name: "JSON", Status: DefinitionDraft, Version: 1}, raw, PublishRequest{
			VersionID: "version-json-1", VersionNumber: 1, PublishedBy: "publisher", PublishedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("%s: CompileJSON: %v", name, err)
		}
		var got string
		for _, node := range version.Document().Nodes {
			if node.Key == "check" {
				got = node.CheckFailureOutcome()
			}
		}
		if got != "failed" {
			t.Fatalf("%s: compiled check node failureOutcome = %q, want failed", name, got)
		}
		if !strings.Contains(string(version.CanonicalContent()), `"failureOutcome":"failed"`) {
			t.Fatalf("%s: canonical content = %s, want failureOutcome part of it", name, version.CanonicalContent())
		}
	}
}

func TestFailureOutcome_RejectsWrongShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		mutate      func(*Node)
		wantProblem string
	}{
		{
			name:        "failureOutcome is not a declared outcome",
			mutate:      func(n *Node) { setFailureOutcome(n, "broken") },
			wantProblem: `failureOutcome "broken" is not one of its declared outcomes`,
		},
		{
			name:        "failureOutcome is the node's own escalation outcome",
			mutate:      func(n *Node) { setFailureOutcome(n, "escalated") },
			wantProblem: `is the node's own cyclePolicy escalation outcome`,
		},
		{
			name:        "three selectable outcomes",
			mutate:      func(n *Node) { n.Outcomes = []string{"passed", "failed", "flaky", "escalated"} },
			wantProblem: "exactly two outcomes must be selectable by the check",
		},
		{
			name:        "only the failure outcome is selectable",
			mutate:      func(n *Node) { n.Outcomes = []string{"failed", "escalated"} },
			wantProblem: "exactly two outcomes must be selectable by the check",
		},
		{
			name: "without a cyclePolicy every declared outcome counts, so a third one is rejected too",
			mutate: func(n *Node) {
				n.CyclePolicy = nil
				n.Outcomes = []string{"passed", "failed", "escalated"}
			},
			wantProblem: "exactly two outcomes must be selectable by the check",
		},
		{
			name:        "surrounding whitespace",
			mutate:      func(n *Node) { setFailureOutcome(n, " failed") },
			wantProblem: `failureOutcome " failed" is invalid`,
		},
	}
	for _, test := range tests {
		for kind, base := range map[string]func() WorkflowDocument{"COMMAND": failureLoopDocument, "MACHINE_GATE": gateFailureLoopDocument} {
			t.Run(kind+"/"+test.name, func(t *testing.T) {
				document := base()
				test.mutate(findNode(&document, "check"))
				// The mutations can orphan an edge (a removed or renamed
				// outcome); only the failureOutcome problem is under test, so
				// other problems may be present alongside it.
				err := ValidateDocument(document)
				if err == nil {
					t.Fatalf("ValidateDocument accepted a document with %s", test.name)
				}
				if !strings.Contains(err.Error(), test.wantProblem) {
					t.Fatalf("error = %v, want it to contain %q", err, test.wantProblem)
				}
			})
		}
	}
}

func TestFailureOutcome_NodeWithoutFailureOutcomeKeepsItsOutcomeRules(t *testing.T) {
	t.Parallel()
	// Two selectable outcomes and no failureOutcome is exactly what published
	// before V9-02, and still publishes: the new rule only applies when the
	// field is set.
	document := failureLoopDocument()
	check := findNode(&document, "check")
	check.Command.FailureOutcome = ""
	if err := ValidateDocument(document); err != nil {
		t.Fatalf("a check without failureOutcome must validate as before: %v", err)
	}
	if got := check.CheckFailureOutcome(); got != "" {
		t.Fatalf("CheckFailureOutcome() = %q, want empty", got)
	}
}

// TestFailureOutcome_LoopFollowsTheExistingBoundedCycleRule: the edge leaving
// failureOutcome is an ordinary FLOW edge, so a loop through it needs a
// cyclePolicy on some node of the loop whose escalation edge leaves it.
func TestFailureOutcome_LoopFollowsTheExistingBoundedCycleRule(t *testing.T) {
	t.Parallel()

	t.Run("a loop with no cyclePolicy anywhere is rejected", func(t *testing.T) {
		t.Parallel()
		document := failureLoopDocument()
		check := findNode(&document, "check")
		check.CyclePolicy = nil
		check.Outcomes = []string{"passed", "failed"}
		document.Edges = []Edge{
			{Key: "e-start-build", From: "start", Outcome: "next", To: "build"},
			{Key: "e-build-check", From: "build", Outcome: "done", To: "check"},
			{Key: "e-check-end", From: "check", Outcome: "passed", To: "end"},
			{Key: "e-check-build", From: "check", Outcome: "failed", To: "build"},
		}
		document.Nodes = document.Nodes[:len(document.Nodes)-1] // drop the now unreachable escalated_end
		err := ValidateDocument(document)
		if err == nil || !strings.Contains(err.Error(), "requires a positive iteration budget and escalation route outside the cycle") {
			t.Fatalf("error = %v, want the unbounded-cycle rejection", err)
		}
	})

	t.Run("the cyclePolicy may sit on the maker instead of the check", func(t *testing.T) {
		t.Parallel()
		document := failureLoopDocument()
		check := findNode(&document, "check")
		check.CyclePolicy = nil
		check.Outcomes = []string{"passed", "failed"}
		build := findNode(&document, "build")
		build.Outcomes = []string{"done", "escalated"}
		build.CyclePolicy = &CyclePolicy{MaxIterations: 2, EscalationOutcome: "escalated"}
		document.Edges = []Edge{
			{Key: "e-start-build", From: "start", Outcome: "next", To: "build"},
			{Key: "e-build-check", From: "build", Outcome: "done", To: "check"},
			{Key: "e-build-escalated", From: "build", Outcome: "escalated", To: "escalated_end"},
			{Key: "e-check-end", From: "check", Outcome: "passed", To: "end"},
			{Key: "e-check-build", From: "check", Outcome: "failed", To: "build"},
		}
		if err := ValidateDocument(document); err != nil {
			t.Fatalf("a loop bounded by the maker's cyclePolicy should publish: %v", err)
		}
	})

	t.Run("a cyclePolicy whose escalation edge stays inside the loop does not bound it", func(t *testing.T) {
		t.Parallel()
		document := failureLoopDocument()
		// "escalated" routes back to build, i.e. into the very cycle it is
		// supposed to leave.
		for i := range document.Edges {
			if document.Edges[i].Key == "e-check-escalated" {
				document.Edges[i].To = "build"
			}
		}
		document.Nodes = document.Nodes[:len(document.Nodes)-1]
		err := ValidateDocument(document)
		if err == nil || !strings.Contains(err.Error(), "requires a positive iteration budget and escalation route outside the cycle") {
			t.Fatalf("error = %v, want the unbounded-cycle rejection", err)
		}
	})
}

func TestCheckSuccessOutcome(t *testing.T) {
	t.Parallel()
	document := failureLoopDocument()
	check := *findNode(&document, "check")
	got, ok := check.CheckSuccessOutcome()
	if !ok || got != "passed" {
		t.Fatalf("CheckSuccessOutcome() = %q, %v, want passed, true (the escalation outcome is not a candidate)", got, ok)
	}
	cloned := check.Command.clone()
	cloned.FailureOutcome = ""
	check.Command = cloned
	if got, ok := check.CheckSuccessOutcome(); ok {
		t.Fatalf("CheckSuccessOutcome() = %q, true for a node with no failureOutcome", got)
	}
}

func setFailureOutcome(n *Node, outcome string) {
	switch n.Type {
	case NodeCommand:
		n.Command.FailureOutcome = outcome
	case NodeMachineGate:
		n.MachineGate.FailureOutcome = outcome
	}
}
