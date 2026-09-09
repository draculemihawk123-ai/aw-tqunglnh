package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func testAgentNodeConfig() *AgentNodeConfig {
	return &AgentNodeConfig{
		ProfileRef: definition.DependencyPin{
			Kind:         definition.KindAgentProfile,
			DefinitionID: "agent-default",
			VersionID:    "agent-default-v1",
		},
	}
}

func testCommandNodeConfig() *CommandNodeConfig {
	return &CommandNodeConfig{
		CommandRef: definition.DependencyPin{
			Kind:         definition.KindCommand,
			DefinitionID: "command-test",
			VersionID:    "command-test-v1",
		},
	}
}

func TestCompileCanonicalHashIgnoresSetOrderAndPublishMetadata(t *testing.T) {
	t.Parallel()

	definition := WorkflowDefinition{
		ID:      WorkflowDefinitionID("workflow-build"),
		Name:    "Build",
		Status:  DefinitionDraft,
		Version: 1,
	}
	firstDocument := validLinearDocument()
	secondDocument := WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "end", Type: NodeEnd},
			{Key: "command", Type: NodeCommand, Outcomes: []string{"pass"}, Command: testCommandNodeConfig()},
			{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
			{Key: "agent", Type: NodeAgent, Outcomes: []string{"ok"}, Agent: testAgentNodeConfig()},
		},
		Edges: []Edge{
			{Key: "edge-command-end", From: "command", Outcome: "pass", To: "end"},
			{Key: "edge-start-agent", From: "start", Outcome: "next", To: "agent"},
			{Key: "edge-agent-command", From: "agent", Outcome: "ok", To: "command"},
		},
	}

	first, err := Compile(definition, PublishRequest{
		VersionID:     WorkflowVersionID("version-1"),
		VersionNumber: 1,
		Document:      firstDocument,
		Dependencies: DependencyManifest{Pins: []DependencyPin{
			{Kind: "COMMAND", Key: "test", Version: "1", Hash: "sha256:command"},
			{Kind: "AGENT_PROFILE", Key: "default", Version: "2", Hash: "sha256:agent"},
		}},
		PublishedBy: "alice",
		PublishedAt: time.Date(2026, 8, 28, 10, 0, 0, 0, time.FixedZone("test", 7*60*60)),
	})
	if err != nil {
		t.Fatalf("compile first version: %v", err)
	}
	second, err := Compile(definition, PublishRequest{
		VersionID:     WorkflowVersionID("version-99"),
		VersionNumber: 99,
		Document:      secondDocument,
		Dependencies: DependencyManifest{Pins: []DependencyPin{
			{Kind: "AGENT_PROFILE", Key: "default", Version: "2", Hash: "sha256:agent"},
			{Kind: "COMMAND", Key: "test", Version: "1", Hash: "sha256:command"},
		}},
		PublishedBy: "bob",
		PublishedAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile second version: %v", err)
	}

	if first.ContentHash() != second.ContentHash() {
		t.Fatalf("semantic equivalents have different hashes: %q != %q", first.ContentHash(), second.ContentHash())
	}
	if string(first.CanonicalContent()) != string(second.CanonicalContent()) {
		t.Fatal("semantic equivalents have different canonical JSON")
	}
	if !strings.HasPrefix(first.ContentHash(), "sha256:") || len(first.ContentHash()) != len("sha256:")+64 {
		t.Fatalf("unexpected canonical hash format %q", first.ContentHash())
	}

	// Mutating author input or accessor copies must not mutate the published snapshot.
	firstDocument.Nodes[0].Key = "mutated-input"
	contentCopy := first.CanonicalContent()
	contentCopy[0] = '!'
	documentCopy := first.Document()
	documentCopy.Nodes[0].Key = "mutated-copy"
	if first.ContentHash() != second.ContentHash() || string(first.CanonicalContent()) != string(second.CanonicalContent()) {
		t.Fatal("published WorkflowVersion changed through a mutable input or accessor")
	}
}

func TestCompileJSONCanonicalizesWhitespaceAndObjectKeyOrder(t *testing.T) {
	t.Parallel()

	definition := WorkflowDefinition{ID: "workflow-json", Name: "JSON", Status: DefinitionDraft, Version: 1}
	compact := []byte(`{"schemaVersion":"1","nodes":[{"key":"start","type":"START","outcomes":["next"]},{"key":"end","type":"END"}],"edges":[{"key":"edge","from":"start","outcome":"next","to":"end"}]}`)
	reordered := []byte(`{
		"edges": [{"to":"end", "outcome":"next", "from":"start", "key":"edge"}],
		"nodes": [{"type":"END", "key":"end"}, {"outcomes":["next"], "type":"START", "key":"start"}],
		"schemaVersion": "1"
	}`)
	baseRequest := PublishRequest{
		VersionID:     "version-json-1",
		VersionNumber: 1,
		PublishedBy:   "publisher",
		PublishedAt:   time.Now(),
	}
	first, err := CompileJSON(definition, compact, baseRequest)
	if err != nil {
		t.Fatalf("compile compact JSON: %v", err)
	}
	baseRequest.VersionID = "version-json-2"
	baseRequest.VersionNumber = 2
	second, err := CompileJSON(definition, reordered, baseRequest)
	if err != nil {
		t.Fatalf("compile reordered JSON: %v", err)
	}
	if first.ContentHash() != second.ContentHash() {
		t.Fatalf("JSON formatting changed canonical hash: %q != %q", first.ContentHash(), second.ContentHash())
	}
}

func TestValidateDocumentRejectsInvalidGraphs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		document    WorkflowDocument
		wantProblem string
	}{
		{
			name: "duplicate node",
			document: WorkflowDocument{
				SchemaVersion: "1",
				Nodes: []Node{
					{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
					{Key: "start", Type: NodeAgent, Outcomes: []string{"ok"}},
					{Key: "end", Type: NodeEnd},
				},
				Edges: []Edge{{Key: "edge", From: "start", Outcome: "next", To: "end"}},
			},
			wantProblem: "duplicate node key",
		},
		{
			name: "missing reference",
			document: WorkflowDocument{
				SchemaVersion: "1",
				Nodes: []Node{
					{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
					{Key: "end", Type: NodeEnd},
				},
				Edges: []Edge{{Key: "edge", From: "start", Outcome: "next", To: "missing"}},
			},
			wantProblem: "missing target node",
		},
		{
			name: "unreachable node",
			document: WorkflowDocument{
				SchemaVersion: "1",
				Nodes: []Node{
					{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
					{Key: "orphan", Type: NodeCommand, Outcomes: []string{"pass"}},
					{Key: "end", Type: NodeEnd},
				},
				Edges: []Edge{
					{Key: "edge-start", From: "start", Outcome: "next", To: "end"},
					{Key: "edge-orphan", From: "orphan", Outcome: "pass", To: "end"},
				},
			},
			wantProblem: "unreachable from START",
		},
		{
			name: "outcome missing route",
			document: WorkflowDocument{
				SchemaVersion: "1",
				Nodes: []Node{
					{Key: "start", Type: NodeStart, Outcomes: []string{"next", "missing"}},
					{Key: "end", Type: NodeEnd},
				},
				Edges: []Edge{{Key: "edge", From: "start", Outcome: "next", To: "end"}},
			},
			wantProblem: "outcome \"missing\" has no route",
		},
		{
			name:        "unbounded cycle",
			document:    unboundedCycleDocument(),
			wantProblem: "requires a positive iteration budget",
		},
		{
			// Correction found during V4-03 review
			// (docs/design/06-v4-runtime-engine.md): a ROUTER with two or
			// more declared outcomes used to compile successfully but could
			// never route at runtime — nothing in this codebase can ever
			// produce that outcome (internal/app/runtime.AdvanceRun only
			// auto-resolves a node with exactly one declared outcome), so
			// the run would permanently deadlock at that node with no
			// error ever surfaced. This must fail at publish time instead.
			name: "router with more than one outcome",
			document: WorkflowDocument{
				SchemaVersion: "1",
				Nodes: []Node{
					{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
					{Key: "router", Type: NodeRouter, Outcomes: []string{"a", "b"}},
					{Key: "end-a", Type: NodeEnd},
					{Key: "end-b", Type: NodeEnd},
				},
				Edges: []Edge{
					{Key: "start-to-router", From: "start", Outcome: "next", To: "router"},
					{Key: "router-to-end-a", From: "router", Outcome: "a", To: "end-a"},
					{Key: "router-to-end-b", From: "router", Outcome: "b", To: "end-b"},
				},
			},
			wantProblem: "ROUTER may declare exactly one outcome",
		},
		{
			// V5-08B (confirmed with the user 2026-09-09): an AGENT node
			// whose ONLY declared outcome is its own CyclePolicy escalation
			// outcome would leave the agents own terminal <agentkit-outcome>
			// marker protocol nothing it could ever legitimately propose --
			// the runtime always assigns the escalation outcome itself, on a
			// SKIPPED NodeRun, without ever invoking the agent for that round.
			name: "agent with only its own escalation outcome",
			document: WorkflowDocument{
				SchemaVersion: "1",
				Nodes: []Node{
					{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
					{
						Key: "agent", Type: NodeAgent, Outcomes: []string{"escalate"}, Agent: testAgentNodeConfig(),
						CyclePolicy: &CyclePolicy{MaxIterations: 2, EscalationOutcome: "escalate"},
					},
					{Key: "end", Type: NodeEnd},
				},
				Edges: []Edge{
					{Key: "start-to-agent", From: "start", Outcome: "next", To: "agent"},
					{Key: "agent-to-agent", From: "agent", Outcome: "escalate", To: "agent"},
				},
			},
			wantProblem: "at least one agent-selectable outcome is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateDocument(test.document)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), test.wantProblem) {
				t.Fatalf("validation error %q does not contain %q", err, test.wantProblem)
			}
		})
	}
}

func TestValidateDocumentAcceptsBoundedCycleWithEscalationExit(t *testing.T) {
	t.Parallel()

	document := unboundedCycleDocument()
	for index := range document.Nodes {
		if document.Nodes[index].Key == "agent" {
			document.Nodes[index].CyclePolicy = &CyclePolicy{
				MaxIterations:     3,
				EscalationOutcome: "done",
			}
		}
	}
	if err := ValidateDocument(document); err != nil {
		t.Fatalf("bounded cycle should be valid: %v", err)
	}
}

func validLinearDocument() WorkflowDocument {
	return WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
			{Key: "agent", Type: NodeAgent, Outcomes: []string{"ok"}, Agent: testAgentNodeConfig()},
			{Key: "command", Type: NodeCommand, Outcomes: []string{"pass"}, Command: testCommandNodeConfig()},
			{Key: "end", Type: NodeEnd},
		},
		Edges: []Edge{
			{Key: "edge-start-agent", From: "start", Outcome: "next", To: "agent"},
			{Key: "edge-agent-command", From: "agent", Outcome: "ok", To: "command"},
			{Key: "edge-command-end", From: "command", Outcome: "pass", To: "end"},
		},
	}
}

func unboundedCycleDocument() WorkflowDocument {
	return WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
			{Key: "agent", Type: NodeAgent, Outcomes: []string{"retry", "done"}, Agent: testAgentNodeConfig()},
			{Key: "end", Type: NodeEnd},
		},
		Edges: []Edge{
			{Key: "edge-start", From: "start", Outcome: "next", To: "agent"},
			{Key: "edge-retry", From: "agent", Outcome: "retry", To: "agent"},
			{Key: "edge-done", From: "agent", Outcome: "done", To: "end"},
		},
	}
}
