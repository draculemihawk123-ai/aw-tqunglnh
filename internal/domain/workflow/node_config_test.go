package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// comprehensiveDocument builds one WorkflowDocument that exercises all
// nine of V2-08's Alpha node types (START, AGENT, COMMAND, MACHINE_GATE,
// APPROVAL, WAIT, ROUTER, FORK, JOIN, END — ROUTER is a trivial single-
// outcome pass-through into the FORK/JOIN pair (Alpha's own ROUTER may
// declare exactly one outcome, a correction found during V4-03 review; see
// that node's own doc comment below), approval's own "timeout" outcome
// reaches the second END instead, and FORK's two branches are plain
// COMMAND nodes) plus a declared shared-state schema touching several of
// them. It is deliberately valid end-to-end: every test in this file
// either asserts it stays valid, or copies it and mutates exactly one
// thing to prove one specific rule fires.
func comprehensiveDocument() WorkflowDocument {
	return WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
			{
				Key: "agent", Type: NodeAgent, Outcomes: []string{"done"},
				Agent: &AgentNodeConfig{
					ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-default", VersionID: "v1"},
					PolicyRefs: []definition.DependencyPin{{Kind: definition.KindPolicy, DefinitionID: "attempt-policy", VersionID: "v1"}},
				},
			},
			{
				Key: "command", Type: NodeCommand, Outcomes: []string{"pass"},
				Command: &CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "command-test", VersionID: "v1"},
				},
			},
			{
				Key: "gate", Type: NodeMachineGate, Outcomes: []string{"verified"},
				MachineGate: &MachineGateNodeConfig{
					GateRef:    definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "gate-verify", VersionID: "v1"},
					PolicyRefs: []definition.DependencyPin{{Kind: definition.KindPolicy, DefinitionID: "gate-policy", VersionID: "v1"}},
				},
			},
			{
				Key: "approval", Type: NodeApproval, Outcomes: []string{"approved", "timeout"},
				Approval: &ApprovalNodeConfig{
					AuthorizedRoles:        []string{"release-manager", "tech-lead"},
					TimeoutSeconds:         3600,
					EscalationOutcome:      "timeout",
					RequestedEvidenceKinds: []string{"diff-summary"},
				},
			},
			{
				Key: "wait", Type: NodeWait, Outcomes: []string{"resumed"},
				Wait: &WaitNodeConfig{Mode: WaitModeSignal, SignalName: "resume-signal", TimeoutSeconds: 600},
			},
			// ROUTER may declare exactly one outcome for Alpha (correction
			// found during V4-03 review) — this fixture used to route
			// "fanout"/"skip" from a two-outcome router; "end2" now stays
			// reachable via approval's own "timeout" outcome instead (see
			// e-approval-end below), and router is a trivial single-outcome
			// pass-through to fork.
			{Key: "router", Type: NodeRouter, Outcomes: []string{"fanout"}},
			{Key: "fork", Type: NodeFork, Outcomes: []string{"branch-a", "branch-b"}},
			{
				Key: "branch_a", Type: NodeCommand, Outcomes: []string{"done"},
				Command: &CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "branch-a-cmd", VersionID: "v1"},
				},
			},
			{
				Key: "branch_b", Type: NodeCommand, Outcomes: []string{"done"},
				Command: &CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "branch-b-cmd", VersionID: "v1"},
				},
			},
			{
				Key: "join", Type: NodeJoin, Outcomes: []string{"joined"},
				Join: &JoinNodeConfig{Mode: JoinModeAll},
			},
			{Key: "end", Type: NodeEnd},
			{Key: "end2", Type: NodeEnd},
		},
		Edges: []Edge{
			{Key: "e-start-agent", From: "start", Outcome: "next", To: "agent"},
			{Key: "e-agent-command", From: "agent", Outcome: "done", To: "command"},
			{Key: "e-command-gate", From: "command", Outcome: "pass", To: "gate"},
			{Key: "e-gate-approval", From: "gate", Outcome: "verified", To: "approval"},
			{Key: "e-approval-wait", From: "approval", Outcome: "approved", To: "wait"},
			{Key: "e-approval-end", From: "approval", Outcome: "timeout", To: "end2"},
			{Key: "e-wait-router", From: "wait", Outcome: "resumed", To: "router"},
			{Key: "e-router-fork", From: "router", Outcome: "fanout", To: "fork"},
			{Key: "e-fork-branch-a", From: "fork", Outcome: "branch-a", To: "branch_a"},
			{Key: "e-fork-branch-b", From: "fork", Outcome: "branch-b", To: "branch_b"},
			{Key: "e-branch-a-join", From: "branch_a", Outcome: "done", To: "join"},
			{Key: "e-branch-b-join", From: "branch_b", Outcome: "done", To: "join"},
			{Key: "e-join-end", From: "join", Outcome: "joined", To: "end"},
		},
		SharedState: []SharedStateField{
			{
				Name: "implementationNotes", Type: SharedStateTypeString, Owner: "agent",
				Writers: []string{"agent"}, Readers: []string{"command", "gate"},
				MergeRule: MergeRuleLastWriteWins,
			},
			{
				Name: "gateEvidenceRef", Type: SharedStateTypeArtifactRef, Owner: "gate",
				Writers: []string{"gate"}, Readers: []string{"approval"},
				MergeRule: MergeRuleLastWriteWins,
			},
			{
				Name: "branchLogs", Type: SharedStateTypeArray, Owner: "branch_a",
				Writers: []string{"branch_a", "branch_b"}, Readers: []string{"join"},
				MergeRule: MergeRuleAppend,
			},
		},
	}
}

func TestValidateDocumentAcceptsAllNineNodeTypes(t *testing.T) {
	t.Parallel()
	if err := ValidateDocument(comprehensiveDocument()); err != nil {
		t.Fatalf("comprehensive document exercising all nine node types should be valid: %v", err)
	}
}

// TestCompileRoundTripsNodeConfigWithoutLoss is V2-08's own "Hoàn thành
// khi" bar made concrete: a compiled WorkflowVersion's Document() and
// CanonicalContent() must carry every field of every node's typed
// config and the shared-state schema, so a runtime never needs to
// re-parse the original authoring file to understand a node.
func TestCompileRoundTripsNodeConfigWithoutLoss(t *testing.T) {
	t.Parallel()

	def := WorkflowDefinition{ID: "workflow-roundtrip", Name: "Roundtrip", Status: DefinitionDraft, Version: 1}
	document := comprehensiveDocument()
	want := normalizeDocument(document)

	version, err := Compile(def, PublishRequest{
		VersionID:     "version-roundtrip-1",
		VersionNumber: 1,
		Document:      document,
		PublishedBy:   "publisher",
		PublishedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("compile comprehensive document: %v", err)
	}

	if got := version.Document(); !reflect.DeepEqual(got, want) {
		t.Fatalf("compiled Document() lost or changed node config:\n got:  %+v\n want: %+v", got, want)
	}

	var decoded canonicalWorkflow
	if err := json.Unmarshal(version.CanonicalContent(), &decoded); err != nil {
		t.Fatalf("unmarshal canonical content: %v", err)
	}
	if !reflect.DeepEqual(decoded.Document, want) {
		t.Fatalf("canonical content lost or changed node config:\n got:  %+v\n want: %+v", decoded.Document, want)
	}
}

func TestValidateDocumentRejectsInvalidNodeTypeConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mutate      func(*WorkflowDocument)
		wantProblem string
	}{
		{
			name: "AGENT missing agent config",
			mutate: func(doc *WorkflowDocument) {
				node := findNode(doc, "agent")
				node.Agent = nil
			},
			wantProblem: `node "agent" of type "AGENT" must declare exactly a "agent" config`,
		},
		{
			name: "AGENT profile ref wrong kind",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "agent").Agent.ProfileRef.Kind = definition.KindCommand
			},
			wantProblem: `agent.profileRef.kind must be "AGENT_PROFILE"`,
		},
		{
			name: "AGENT profile ref missing version",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "agent").Agent.ProfileRef.VersionID = ""
			},
			wantProblem: `agent.profileRef.versionId is required`,
		},
		{
			name: "AGENT duplicate policy ref",
			mutate: func(doc *WorkflowDocument) {
				agent := findNode(doc, "agent").Agent
				agent.PolicyRefs = append(agent.PolicyRefs, agent.PolicyRefs[0])
			},
			wantProblem: "duplicate policy ref",
		},
		{
			name: "AGENT blank adapter build id",
			mutate: func(doc *WorkflowDocument) {
				blank := "   "
				findNode(doc, "agent").Agent.AdapterBuildID = &blank
			},
			wantProblem: `agent.adapterBuildId must not be blank when present`,
		},
		{
			name: "COMMAND missing command config",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "command").Command = nil
			},
			wantProblem: `node "command" of type "COMMAND" must declare exactly a "command" config`,
		},
		{
			name: "COMMAND ref wrong kind",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "command").Command.CommandRef.Kind = definition.KindGate
			},
			wantProblem: `command.commandRef.kind must be "COMMAND"`,
		},
		{
			name: "MACHINE_GATE missing gate config",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "gate").MachineGate = nil
			},
			wantProblem: `node "gate" of type "MACHINE_GATE" must declare exactly a "machineGate" config`,
		},
		{
			name: "MACHINE_GATE ref wrong kind",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "gate").MachineGate.GateRef.Kind = definition.KindBlock
			},
			wantProblem: `machineGate.gateRef.kind must be "GATE"`,
		},
		{
			name: "APPROVAL no authorized roles",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "approval").Approval.AuthorizedRoles = nil
			},
			wantProblem: "approval must declare at least one authorized role",
		},
		{
			name: "APPROVAL zero timeout",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "approval").Approval.TimeoutSeconds = 0
			},
			wantProblem: "approval timeout must be greater than zero",
		},
		{
			name: "APPROVAL undeclared escalation outcome",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "approval").Approval.EscalationOutcome = "missing"
			},
			wantProblem: `approval escalation outcome "missing" is not declared`,
		},
		{
			name: "WAIT duration mode with zero duration",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "wait").Wait = &WaitNodeConfig{Mode: WaitModeDuration}
			},
			wantProblem: "wait duration must be greater than zero for mode DURATION",
		},
		{
			name: "WAIT signal mode with empty signal name",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "wait").Wait.SignalName = ""
			},
			wantProblem: "wait signal name is required for mode SIGNAL",
		},
		{
			name: "WAIT signal mode with duration also set",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "wait").Wait.DurationSeconds = 30
			},
			wantProblem: "wait duration must be zero for mode SIGNAL",
		},
		{
			name: "ROUTER must not declare a config",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "router").Agent = &AgentNodeConfig{
					ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "x", VersionID: "v1"},
				}
			},
			wantProblem: `node "router" of type "ROUTER" must not declare a node config`,
		},
		{
			name: "FORK must not declare a config",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "fork").Join = &JoinNodeConfig{Mode: JoinModeAll}
			},
			wantProblem: `node "fork" of type "FORK" must not declare a node config`,
		},
		{
			name: "JOIN missing join config",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "join").Join = nil
			},
			wantProblem: `node "join" of type "JOIN" must declare exactly a "join" config`,
		},
		{
			name: "JOIN quorum mode with zero count",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "join").Join = &JoinNodeConfig{Mode: JoinModeQuorum}
			},
			wantProblem: "join quorum count must be greater than zero for mode QUORUM",
		},
		{
			name: "JOIN all mode with nonzero count",
			mutate: func(doc *WorkflowDocument) {
				findNode(doc, "join").Join = &JoinNodeConfig{Mode: JoinModeAll, QuorumCount: 2}
			},
			wantProblem: `join quorum count must be zero for mode "ALL"`,
		},
		{
			name: "shared state duplicate field name",
			mutate: func(doc *WorkflowDocument) {
				doc.SharedState = append(doc.SharedState, doc.SharedState[0])
			},
			wantProblem: `duplicate shared state field "implementationNotes"`,
		},
		{
			name: "shared state owner not a declared node",
			mutate: func(doc *WorkflowDocument) {
				doc.SharedState[0].Owner = "no-such-node"
				doc.SharedState[0].Writers = []string{"no-such-node"}
			},
			wantProblem: `shared state field "implementationNotes" owner "no-such-node" does not reference a declared node`,
		},
		{
			name: "shared state owner not in writers",
			mutate: func(doc *WorkflowDocument) {
				doc.SharedState[0].Writers = []string{"command"}
			},
			wantProblem: `owner "agent" must also be listed in writers`,
		},
		{
			name: "shared state empty readers",
			mutate: func(doc *WorkflowDocument) {
				doc.SharedState[0].Readers = nil
			},
			wantProblem: `shared state field "implementationNotes" must declare at least one reader`,
		},
		{
			name: "shared state invalid merge rule",
			mutate: func(doc *WorkflowDocument) {
				doc.SharedState[0].MergeRule = "SUM"
			},
			wantProblem: `shared state field "implementationNotes" has unsupported merge rule "SUM"`,
		},
		{
			name: "shared state invalid type",
			mutate: func(doc *WorkflowDocument) {
				doc.SharedState[0].Type = "BLOB"
			},
			wantProblem: `shared state field "implementationNotes" has unsupported type "BLOB"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := comprehensiveDocument()
			test.mutate(&document)
			err := ValidateDocument(document)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), test.wantProblem) {
				t.Fatalf("validation error %q does not contain %q", err, test.wantProblem)
			}
		})
	}
}

func findNode(doc *WorkflowDocument, key string) *Node {
	for i := range doc.Nodes {
		if doc.Nodes[i].Key == key {
			return &doc.Nodes[i]
		}
	}
	panic("no such node: " + key)
}
