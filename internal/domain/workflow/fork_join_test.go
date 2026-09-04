package workflow

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// TestValidateForkJoinTopology_ValidConvergence confirms
// comprehensiveDocument()'s own fork/join pair (both branches converge
// at "join") is accepted — the baseline every negative test below
// mutates away from.
func TestValidateForkJoinTopology_ValidConvergence(t *testing.T) {
	t.Parallel()
	if err := ValidateDocument(comprehensiveDocument()); err != nil {
		t.Fatalf("valid fork/join convergence should be accepted: %v", err)
	}
}

// TestValidateForkJoinTopology_RejectsDivergentJoins makes branch_a route
// to a second, independent JOIN instead of the shared one — the fork's
// two branches now converge at two different joins.
func TestValidateForkJoinTopology_RejectsDivergentJoins(t *testing.T) {
	t.Parallel()
	doc := comprehensiveDocument()
	doc.Nodes = append(doc.Nodes, Node{Key: "join2", Type: NodeJoin, Outcomes: []string{"joined2"}, Join: &JoinNodeConfig{Mode: JoinModeAll}})
	doc.Edges = append(doc.Edges, Edge{Key: "e-join2-end", From: "join2", Outcome: "joined2", To: "end"})
	for i, edge := range doc.Edges {
		if edge.Key == "e-branch-a-join" {
			doc.Edges[i].To = "join2"
		}
	}

	err := ValidateDocument(doc)
	if err == nil {
		t.Fatal("branches converging at two different joins should be rejected")
	}
	if !strings.Contains(err.Error(), "converge at more than one join") {
		t.Fatalf("err = %v, want it to mention converging at more than one join", err)
	}
}

// TestValidateForkJoinTopology_RejectsDeadEndBranch routes branch_b
// straight to an END node instead of the shared join.
func TestValidateForkJoinTopology_RejectsDeadEndBranch(t *testing.T) {
	t.Parallel()
	doc := comprehensiveDocument()
	for i, edge := range doc.Edges {
		if edge.Key == "e-branch-b-join" {
			doc.Edges[i].To = "end2"
		}
	}
	// branch_b's own outcome "done" must still route somewhere valid, and
	// end2 has no outgoing edges — this also means "join" only receives
	// branch_a now, which is fine structurally (join still has an
	// incoming edge), the dead-end is the actual property under test.

	err := ValidateDocument(doc)
	if err == nil {
		t.Fatal("a fork branch reaching END without reconverging should be rejected")
	}
	if !strings.Contains(err.Error(), "reaches END without reconverging") {
		t.Fatalf("err = %v, want it to mention reaching END without reconverging", err)
	}
}

// TestValidateForkJoinTopology_RejectsNestedForkJoin puts a second,
// nested FORK/JOIN pair inside branch_a before it ever reaches the outer
// join — an unsupported topology per this package's own deliberate
// scope-narrowing (see validateForkJoinTopology's doc comment).
func TestValidateForkJoinTopology_RejectsNestedForkJoin(t *testing.T) {
	t.Parallel()
	doc := comprehensiveDocument()
	doc.Nodes = append(doc.Nodes,
		Node{Key: "inner_fork", Type: NodeFork, Outcomes: []string{"ia", "ib"}},
		Node{Key: "inner_a", Type: NodeCommand, Outcomes: []string{"done"}, Command: &CommandNodeConfig{
			CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "inner-a-cmd", VersionID: "v1"},
		}},
		Node{Key: "inner_b", Type: NodeCommand, Outcomes: []string{"done"}, Command: &CommandNodeConfig{
			CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "inner-b-cmd", VersionID: "v1"},
		}},
		Node{Key: "inner_join", Type: NodeJoin, Outcomes: []string{"joined"}, Join: &JoinNodeConfig{Mode: JoinModeAll}},
	)
	doc.Edges = append(doc.Edges,
		Edge{Key: "e-branch-a-innerfork", From: "branch_a", Outcome: "done", To: "inner_fork"},
		Edge{Key: "e-innerfork-a", From: "inner_fork", Outcome: "ia", To: "inner_a"},
		Edge{Key: "e-innerfork-b", From: "inner_fork", Outcome: "ib", To: "inner_b"},
		Edge{Key: "e-innera-innerjoin", From: "inner_a", Outcome: "done", To: "inner_join"},
		Edge{Key: "e-innerb-innerjoin", From: "inner_b", Outcome: "done", To: "inner_join"},
		Edge{Key: "e-innerjoin-outerjoin", From: "inner_join", Outcome: "joined", To: "join"},
	)
	for i, edge := range doc.Edges {
		if edge.Key == "e-branch-a-join" {
			doc.Edges = append(doc.Edges[:i], doc.Edges[i+1:]...)
			break
		}
	}

	err := ValidateDocument(doc)
	if err == nil {
		t.Fatal("nested fork/join should be rejected as unsupported")
	}
	if !strings.Contains(err.Error(), "nested fork/join is not supported") {
		t.Fatalf("err = %v, want it to mention nested fork/join is not supported", err)
	}
}

// TestValidateForkJoinTopology_RejectsAmbiguousSharedJoin adds a second
// FORK whose branches also route into the very same "join" node — two
// different forks both claiming ownership of one join. fork2 is reached
// via a third outcome declared on "approval" itself (a real decision
// producer, so a third outcome is legitimate) rather than router — router
// may declare only one outcome (Alpha, correction found during V4-03
// review), so it can no longer serve as a second branch point. Hanging
// fork2 off "approval" (a sibling entry point, not downstream of "fork")
// keeps it outside fork's own branch region — attaching it inside that
// region instead trips the unrelated "nested fork/join" rule first, not
// the ambiguous-shared-join rule this test targets.
func TestValidateForkJoinTopology_RejectsAmbiguousSharedJoin(t *testing.T) {
	t.Parallel()
	doc := comprehensiveDocument()
	doc.Nodes = append(doc.Nodes,
		Node{Key: "fork2", Type: NodeFork, Outcomes: []string{"c", "d"}},
		Node{Key: "branch_c", Type: NodeCommand, Outcomes: []string{"done"}, Command: &CommandNodeConfig{
			CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "branch-c-cmd", VersionID: "v1"},
		}},
		Node{Key: "branch_d", Type: NodeCommand, Outcomes: []string{"done"}, Command: &CommandNodeConfig{
			CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "branch-d-cmd", VersionID: "v1"},
		}},
	)
	for i, node := range doc.Nodes {
		if node.Key == "approval" {
			doc.Nodes[i].Outcomes = append(doc.Nodes[i].Outcomes, "escalated")
		}
	}
	doc.Edges = append(doc.Edges,
		Edge{Key: "e-approval-fork2", From: "approval", Outcome: "escalated", To: "fork2"},
		Edge{Key: "e-fork2-c", From: "fork2", Outcome: "c", To: "branch_c"},
		Edge{Key: "e-fork2-d", From: "fork2", Outcome: "d", To: "branch_d"},
		Edge{Key: "e-branch-c-join", From: "branch_c", Outcome: "done", To: "join"},
		Edge{Key: "e-branch-d-join", From: "branch_d", Outcome: "done", To: "join"},
	)

	err := ValidateDocument(doc)
	if err == nil {
		t.Fatal("a join shared by two different forks should be rejected as ambiguous")
	}
	if !strings.Contains(err.Error(), "branch identity is ambiguous") {
		t.Fatalf("err = %v, want it to mention ambiguous branch identity", err)
	}
}

// TestValidateForkJoinTopology_RejectsQuorumExceedingBranchCount sets the
// join's QuorumCount above the fork's actual 2-branch count.
func TestValidateForkJoinTopology_RejectsQuorumExceedingBranchCount(t *testing.T) {
	t.Parallel()
	doc := comprehensiveDocument()
	for i, node := range doc.Nodes {
		if node.Key == "join" {
			doc.Nodes[i].Join = &JoinNodeConfig{Mode: JoinModeQuorum, QuorumCount: 3}
		}
	}

	err := ValidateDocument(doc)
	if err == nil {
		t.Fatal("a quorum count exceeding the owning fork's branch count should be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds its owning fork") {
		t.Fatalf("err = %v, want it to mention exceeding its owning fork", err)
	}
}

// TestValidateForkJoinTopology_AcceptsQuorumWithinBranchCount is the
// positive counterpart: QuorumCount at or below the branch count is
// valid.
func TestValidateForkJoinTopology_AcceptsQuorumWithinBranchCount(t *testing.T) {
	t.Parallel()
	doc := comprehensiveDocument()
	for i, node := range doc.Nodes {
		if node.Key == "join" {
			doc.Nodes[i].Join = &JoinNodeConfig{Mode: JoinModeQuorum, QuorumCount: 2}
		}
	}

	if err := ValidateDocument(doc); err != nil {
		t.Fatalf("a quorum count within the owning fork's branch count should be accepted: %v", err)
	}
}

// TestAgentNodeConfig_AdapterBuildID_OptionalAndClonedDeeply confirms an
// AGENT node's optional AdapterBuildID both validates when present and
// non-blank, and survives a clone as an independent pointer (not shared
// with the original) — the same deep-copy discipline CyclePolicy's own
// pointer field already follows.
func TestAgentNodeConfig_AdapterBuildID_OptionalAndClonedDeeply(t *testing.T) {
	t.Parallel()
	doc := comprehensiveDocument()
	buildID := "sha256:deadbeef"
	findNode(&doc, "agent").Agent.AdapterBuildID = &buildID

	if err := ValidateDocument(doc); err != nil {
		t.Fatalf("a non-blank adapter build id should be accepted: %v", err)
	}

	cloned := cloneDocument(doc)
	clonedAgent := findNode(&cloned, "agent").Agent
	if clonedAgent.AdapterBuildID == nil || *clonedAgent.AdapterBuildID != buildID {
		t.Fatalf("clone should preserve AdapterBuildID's value, got %v", clonedAgent.AdapterBuildID)
	}
	if clonedAgent.AdapterBuildID == findNode(&doc, "agent").Agent.AdapterBuildID {
		t.Fatal("clone should not share the same AdapterBuildID pointer as the original")
	}
	*clonedAgent.AdapterBuildID = "sha256:mutated"
	if *findNode(&doc, "agent").Agent.AdapterBuildID != buildID {
		t.Fatal("mutating the clone's AdapterBuildID must not affect the original")
	}
}

// TestValidateForkJoinTopology_RejectsForkWithNoBranches is a defensive
// structural case: a FORK node with zero outgoing edges.
func TestValidateForkJoinTopology_RejectsForkWithNoBranches(t *testing.T) {
	t.Parallel()
	doc := WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "start", Type: NodeStart, Outcomes: []string{"go"}},
			{Key: "fork", Type: NodeFork},
			{Key: "end", Type: NodeEnd},
		},
		Edges: []Edge{
			{Key: "e1", From: "start", Outcome: "go", To: "fork"},
		},
	}
	err := ValidateDocument(doc)
	if err == nil {
		t.Fatal("a fork with no outgoing branches should be rejected")
	}
	if !strings.Contains(err.Error(), "no outgoing branches") {
		t.Fatalf("err = %v, want it to mention no outgoing branches", err)
	}
}
