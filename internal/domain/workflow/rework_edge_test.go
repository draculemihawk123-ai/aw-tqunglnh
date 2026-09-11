package workflow

// This file is V5-10B's own test suite (2026-09-10 combined-gaps follow-up):
// COMPLETION_REWORK edges are the schema piece ADR-009/ADR-021/GC-INV-29
// need — CompletionPolicy's REWORK outcome routes through "a rework edge
// published in the WorkflowVersion," but until this task nothing let an
// author declare one (END could not have ANY outgoing edge). See
// EdgeKind's own doc comment (workflow.go) for the full design.

import (
	"strings"
	"testing"
	"time"
)

// linearDocumentWithEdges returns validLinearDocument() (compiler_test.go)
// with extra edges appended — the base fixture is otherwise untouched
// (start->agent->command->end, all FLOW), so every case below is testing
// exactly one new rework-edge rule in isolation.
func linearDocumentWithEdges(extra ...Edge) WorkflowDocument {
	doc := validLinearDocument()
	doc.Edges = append(doc.Edges, extra...)
	return doc
}

// linearDocumentMutated returns validLinearDocument() after mutate runs
// against a pointer to it — for cases that need to change an existing
// node/edge rather than just add one.
func linearDocumentMutated(mutate func(*WorkflowDocument)) WorkflowDocument {
	doc := validLinearDocument()
	mutate(&doc)
	return doc
}

func TestValidateDocumentRejectsInvalidReworkEdges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		document    WorkflowDocument
		wantProblem string
	}{
		{
			name: "flow edge from end",
			document: linearDocumentMutated(func(doc *WorkflowDocument) {
				for i := range doc.Nodes {
					if doc.Nodes[i].Key == "end" {
						doc.Nodes[i].Outcomes = []string{"loop"}
					}
				}
				doc.Edges = append(doc.Edges, Edge{Key: "end-to-command", From: "end", Outcome: "loop", To: "command"})
			}),
			wantProblem: "cannot have outgoing FLOW edges",
		},
		{
			name: "completion rework edge not sourced from end",
			document: linearDocumentWithEdges(Edge{
				Key: "rework-from-agent", From: "agent", To: "command",
				Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 1},
			}),
			wantProblem: "is not an END node",
		},
		{
			name: "completion rework edge targets end",
			document: linearDocumentWithEdges(Edge{
				Key: "rework-to-end", From: "end", To: "end",
				Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 1},
			}),
			wantProblem: "targets END node",
		},
		{
			name: "completion rework edge declares an outcome",
			document: linearDocumentWithEdges(Edge{
				Key: "rework-with-outcome", From: "end", Outcome: "retry", To: "agent",
				Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 1},
			}),
			wantProblem: "must not declare an outcome",
		},
		{
			name: "completion rework edge missing reworkPolicy",
			document: linearDocumentWithEdges(Edge{
				Key: "rework-no-policy", From: "end", To: "agent", Kind: EdgeCompletionRework,
			}),
			wantProblem: "requires a reworkPolicy",
		},
		{
			name: "completion rework edge zero max iterations",
			document: linearDocumentWithEdges(Edge{
				Key: "rework-zero-budget", From: "end", To: "agent",
				Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 0},
			}),
			wantProblem: "max iterations must be greater than zero",
		},
		{
			name: "flow edge declares reworkPolicy",
			document: linearDocumentMutated(func(doc *WorkflowDocument) {
				for i := range doc.Edges {
					if doc.Edges[i].Key == "edge-agent-command" {
						doc.Edges[i].ReworkPolicy = &ReworkPolicy{MaxIterations: 1}
					}
				}
			}),
			wantProblem: "must not declare a reworkPolicy",
		},
		{
			name: "duplicate completion rework edges from same end",
			document: linearDocumentWithEdges(
				Edge{Key: "rework-1", From: "end", To: "agent", Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 1}},
				Edge{Key: "rework-2", From: "end", To: "command", Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 1}},
			),
			wantProblem: "duplicate routes",
		},
		{
			name: "edge has unsupported kind",
			document: linearDocumentMutated(func(doc *WorkflowDocument) {
				for i := range doc.Edges {
					if doc.Edges[i].Key == "edge-agent-command" {
						doc.Edges[i].Kind = "BOGUS"
					}
				}
			}),
			wantProblem: "unsupported kind",
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

// TestValidateDocumentAcceptsCompletionReworkEdge proves the golden path:
// a single COMPLETION_REWORK edge from END back to an already-normally-
// reachable node (here "agent", already part of the START->agent->command
// ->end FLOW path) validates cleanly.
func TestValidateDocumentAcceptsCompletionReworkEdge(t *testing.T) {
	t.Parallel()
	doc := linearDocumentWithEdges(Edge{
		Key: "rework-to-agent", From: "end", To: "agent",
		Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 3},
	})
	if err := ValidateDocument(doc); err != nil {
		t.Fatalf("a single well-formed rework edge should validate: %v", err)
	}
}

// TestValidateDocumentAcceptsExplicitFlowKind proves "" and the explicit
// "FLOW" string are treated identically everywhere — an author (or a
// document written before Kind existed) never needs to know this field
// exists for an ordinary edge.
func TestValidateDocumentAcceptsExplicitFlowKind(t *testing.T) {
	t.Parallel()
	doc := linearDocumentMutated(func(doc *WorkflowDocument) {
		for i := range doc.Edges {
			doc.Edges[i].Kind = EdgeFlow
		}
	})
	if err := ValidateDocument(doc); err != nil {
		t.Fatalf("explicit FLOW kind should validate identically to omitted kind: %v", err)
	}
}

// TestValidateDocumentRejectsReworkEdgeTargetUnreachableFromNormalFlow is a
// deliberate scope-boundary test: a rework edge does NOT, by itself, prove
// its target is reachable — COMPLETION_REWORK edges are excluded from the
// START-reachability graph on purpose (see EdgeKind's own doc comment), so
// a target that ISN'T otherwise reachable via ordinary FLOW edges still
// fails as "unreachable from START". A rework target must independently be
// a legitimate, already-connected node in the normal graph.
func TestValidateDocumentRejectsReworkEdgeTargetUnreachableFromNormalFlow(t *testing.T) {
	t.Parallel()
	doc := validLinearDocument()
	doc.Nodes = append(doc.Nodes, Node{Key: "rework-only", Type: NodeCommand, Outcomes: []string{"pass"}, Command: testCommandNodeConfig()})
	doc.Edges = append(doc.Edges,
		Edge{Key: "rework-only-to-end", From: "rework-only", Outcome: "pass", To: "end"},
		Edge{Key: "rework-to-rework-only", From: "end", To: "rework-only", Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 1}},
	)
	err := ValidateDocument(doc)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "unreachable from START") {
		t.Fatalf("validation error %q does not contain %q", err, "unreachable from START")
	}
}

// TestCycleMembership_ExcludesCompletionReworkEdges proves a
// COMPLETION_REWORK edge never merges END's own strongly-connected
// component with its target's — CycleMembership is V4-07's own escalation-
// edge-leaves-its-own-cycle check (advance.go), which must keep reasoning
// about the scheduler's real (FLOW-only) cycle structure, never one a
// rework route would otherwise fabricate.
func TestCycleMembership_ExcludesCompletionReworkEdges(t *testing.T) {
	t.Parallel()
	doc := linearDocumentWithEdges(Edge{
		Key: "rework-to-agent", From: "end", To: "agent",
		Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 3},
	})
	membership := CycleMembership(doc)
	if membership["end"] == membership["agent"] {
		t.Fatalf("end and agent must not share a cycle-membership component via a COMPLETION_REWORK edge: %+v", membership)
	}
}

// TestCloneDocument_DeepCopiesEdgeReworkPolicy proves WorkflowVersion's own
// documented "immutable from outside this package, accessors return
// copies" contract holds for the new Edge.ReworkPolicy pointer field —
// mutating a value returned by WorkflowVersion.Document() must never reach
// back into the version's own internal state.
func TestCloneDocument_DeepCopiesEdgeReworkPolicy(t *testing.T) {
	t.Parallel()
	def := WorkflowDefinition{ID: WorkflowDefinitionID("wf-clone"), Name: "Clone", Status: DefinitionDraft, Version: 1}
	doc := linearDocumentWithEdges(Edge{
		Key: "rework-to-agent", From: "end", To: "agent",
		Kind: EdgeCompletionRework, ReworkPolicy: &ReworkPolicy{MaxIterations: 3},
	})
	version, err := Compile(def, PublishRequest{
		VersionID: WorkflowVersionID("v1"), VersionNumber: 1, Document: doc,
		PublishedBy: "alice", PublishedAt: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	first := version.Document()
	for i := range first.Edges {
		if first.Edges[i].Key == "rework-to-agent" {
			first.Edges[i].ReworkPolicy.MaxIterations = 999
		}
	}

	second := version.Document()
	for _, edge := range second.Edges {
		if edge.Key == "rework-to-agent" && edge.ReworkPolicy.MaxIterations != 3 {
			t.Fatalf("mutating one Document() call's ReworkPolicy leaked into another: got %d, want 3", edge.ReworkPolicy.MaxIterations)
		}
	}
}
