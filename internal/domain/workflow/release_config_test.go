package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// V11-07: an APPROVAL node's release policy.

func releaseDocument(release *ApprovalReleaseConfig, approvedTo string, extra ...Node) WorkflowDocument {
	nodes := []Node{
		{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
		{Key: "review", Type: NodeApproval, Outcomes: []string{"approved", "revise"}, Approval: &ApprovalNodeConfig{
			AuthorizedRoles: []string{"reviewer"}, TimeoutSeconds: 3600, EscalationOutcome: "revise", Release: release,
		}},
		{Key: "work", Type: NodeAgent, Outcomes: []string{"done", "escalated"}, Agent: &AgentNodeConfig{
			ProfileRef: testAgentNodeConfig().ProfileRef, Role: AgentRoleMaker,
		}, CyclePolicy: &CyclePolicy{MaxIterations: 3, EscalationOutcome: "escalated"}},
		{Key: "end", Type: NodeEnd},
	}
	nodes = append(nodes, extra...)
	return WorkflowDocument{
		SchemaVersion: "1",
		Nodes:         nodes,
		Edges: []Edge{
			{Key: "start-work", From: "start", Outcome: "next", To: "work"},
			{Key: "work-review", From: "work", Outcome: "done", To: "review"},
			{Key: "work-escalated", From: "work", Outcome: "escalated", To: "end"},
			{Key: "review-approved", From: "review", Outcome: "approved", To: approvedTo},
			{Key: "review-revise", From: "review", Outcome: "revise", To: "work"},
		},
	}
}

func TestApprovalRelease_OfferedOnAnOutcomeThatEndsTheRunIsValid(t *testing.T) {
	t.Parallel()
	for _, mode := range []ReleaseCommitMode{ReleaseCommitOffered, ReleaseCommitRequired} {
		document := releaseDocument(&ApprovalReleaseConfig{Commit: mode, Outcomes: []string{"approved"}}, "end")
		if err := ValidateDocument(document); err != nil {
			t.Errorf("%s on approved -> END must publish: %v", mode, err)
		}
	}
}

func TestApprovalRelease_AnAbsentPolicyIsDisabledAndChangesNoHash(t *testing.T) {
	t.Parallel()
	plain := releaseDocument(nil, "end")
	disabledNil := releaseDocument(nil, "end")
	if err := ValidateDocument(plain); err != nil {
		t.Fatal(err)
	}
	a, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(disabledNil)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) || strings.Contains(string(a), "release") {
		t.Fatalf("an absent policy must not appear in the serialized document: %s", a)
	}
	if got := (*ApprovalReleaseConfig)(nil).Mode(); got != ReleaseCommitDisabled {
		t.Fatalf("nil policy mode = %q, want DISABLED", got)
	}
	if (*ApprovalReleaseConfig)(nil).AllowsCommitOn("approved") {
		t.Fatal("nil policy must not allow a commit")
	}
}

func TestApprovalRelease_RejectsAnOutcomeWhoseRouteCanReachAWriter(t *testing.T) {
	t.Parallel()
	// approved -> work (a MAKER): a background commit would race with it.
	document := releaseDocument(&ApprovalReleaseConfig{Commit: ReleaseCommitOffered, Outcomes: []string{"approved"}}, "work")
	err := ValidateDocument(document)
	if err == nil {
		t.Fatal("a release outcome that reaches a MAKER must not publish")
	}
	for _, want := range []string{"review", "approved", "work", "race"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestApprovalRelease_ReadOnlyNodesAfterTheDecisionAreFineButACommandIsNot(t *testing.T) {
	t.Parallel()
	checker := Node{Key: "check", Type: NodeAgent, Outcomes: []string{"ok"}, Agent: &AgentNodeConfig{
		ProfileRef: testAgentNodeConfig().ProfileRef, Role: AgentRoleChecker,
	}}
	document := releaseDocument(&ApprovalReleaseConfig{Commit: ReleaseCommitOffered, Outcomes: []string{"approved"}}, "check", checker)
	document.Edges = append(document.Edges, Edge{Key: "check-end", From: "check", Outcome: "ok", To: "end"})
	if err := ValidateDocument(document); err != nil {
		t.Fatalf("approved -> CHECKER -> END writes nothing, must publish: %v", err)
	}

	command := Node{Key: "fmt", Type: NodeCommand, Outcomes: []string{"ok"}, Command: testCommandNodeConfig()}
	document = releaseDocument(&ApprovalReleaseConfig{Commit: ReleaseCommitOffered, Outcomes: []string{"approved"}}, "fmt", command)
	document.Edges = append(document.Edges, Edge{Key: "fmt-end", From: "fmt", Outcome: "ok", To: "end"})
	if err := ValidateDocument(document); err == nil {
		t.Fatal("a COMMAND after the decision may write to the worktree, must not publish")
	}
}

func TestApprovalRelease_FieldValidation(t *testing.T) {
	t.Parallel()
	cases := map[string]*ApprovalReleaseConfig{
		"unknown mode":           {Commit: "MAYBE", Outcomes: []string{"approved"}},
		"offered without list":   {Commit: ReleaseCommitOffered},
		"required without list":  {Commit: ReleaseCommitRequired},
		"disabled with a list":   {Commit: ReleaseCommitDisabled, Outcomes: []string{"approved"}},
		"undeclared outcome":     {Commit: ReleaseCommitOffered, Outcomes: []string{"merged"}},
		"duplicate outcome":      {Commit: ReleaseCommitOffered, Outcomes: []string{"approved", "approved"}},
		"outcome with a padding": {Commit: ReleaseCommitOffered, Outcomes: []string{" approved"}},
	}
	for name, release := range cases {
		if err := ValidateDocument(releaseDocument(release, "end")); err == nil {
			t.Errorf("%s: must not publish", name)
		}
	}
	if err := ValidateDocument(releaseDocument(&ApprovalReleaseConfig{Commit: ReleaseCommitDisabled}, "end")); err != nil {
		t.Errorf("an explicit DISABLED with no outcomes is valid: %v", err)
	}
}

func TestApprovalRelease_CloneAndOutcomeOrderDoNotAffectTheHash(t *testing.T) {
	t.Parallel()
	node := releaseDocument(&ApprovalReleaseConfig{Commit: ReleaseCommitOffered, Outcomes: []string{"revise", "approved"}}, "end").Nodes[1]
	cloned := node.Approval.clone()
	cloned.Release.Outcomes[0] = "mutated"
	if node.Approval.Release.Outcomes[0] != "revise" {
		t.Fatal("clone must deep-copy the release outcomes")
	}
}

func TestAllowsCommitOn(t *testing.T) {
	t.Parallel()
	offered := &ApprovalReleaseConfig{Commit: ReleaseCommitOffered, Outcomes: []string{"approved"}}
	if !offered.AllowsCommitOn("approved") || offered.AllowsCommitOn("revise") {
		t.Fatal("OFFERED allows exactly the listed outcomes")
	}
	if (&ApprovalReleaseConfig{Commit: ReleaseCommitDisabled}).AllowsCommitOn("approved") {
		t.Fatal("DISABLED allows nothing")
	}
}
