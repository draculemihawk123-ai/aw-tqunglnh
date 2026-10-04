package v5accept

// V9-14a: the model each AGENT node's own AgentProfile pins is what its CLI is
// started with.
//
// Model has always been per profile, hence per node: a workflow can run its
// reviewer on a cheaper model than its maker by pinning two profiles. What was
// missing was a test that looks at the command line a real run produced; the
// path profile -> ResolvedExecutionProfile -> AgentExecutionRequest.Model ->
// adapter was read and believed, never locked. The fake CLI records the argv it
// was started with (the LAST invocation of a run wins the capture file), so two
// runs prove both ends: a run whose last node is the reviewer shows the
// reviewer's model, a run whose last node is the maker shows the maker's.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	stdruntime "runtime"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const (
	v9MakerModel    = "fake-model" // what publishAgentProfileVersion pins
	v9ReviewerModel = "claude-haiku-4-5-20251001"
	v9ReviewerDef   = "v9-reviewer-profile-def"
	v9ReviewerVer   = "v9-reviewer-profile-v1"
)

// v9AgentNodeWithProfile is v9AgentNode pinning the given profile.
func v9AgentNodeWithProfile(key, agentBuildID string, role workflow.AgentRole, outcome, profileDef, profileVersion string) workflow.Node {
	node := v9AgentNode(key, agentBuildID, role, outcome)
	node.Agent.ProfileRef = definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: profileDef, VersionID: profileVersion}
	return node
}

func lastArgvModel(t *testing.T, capturePath string) string {
	t.Helper()
	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read fake CLI capture: %v", err)
	}
	var invocation providers.FakeCLIInvocation
	if err := json.Unmarshal(raw, &invocation); err != nil {
		t.Fatalf("decode fake CLI capture: %v", err)
	}
	var models []string
	for i, argument := range invocation.Argv {
		if argument == "--model" && i+1 < len(invocation.Argv) {
			models = append(models, invocation.Argv[i+1])
		}
	}
	if len(models) != 1 {
		t.Fatalf("--model appears %d times in the CLI's argv %v, want exactly once", len(models), invocation.Argv)
	}
	return models[0]
}

func TestV9AcceptAgentModel_EachNodeGetsTheModelOfItsOwnProfile(t *testing.T) {
	type scenario struct {
		name      string
		document  func(buildID string) workflow.WorkflowDocument
		wantModel string // the model of the node that ran LAST, whose argv the fake recorded
	}
	scenarios := []scenario{
		{
			// maker (profile A) -> reviewer (profile B): the reviewer ran last.
			name: "the reviewer's profile model reaches the reviewer's CLI",
			document: func(buildID string) workflow.WorkflowDocument {
				return workflow.WorkflowDocument{
					SchemaVersion: "1",
					Nodes: []workflow.Node{
						{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
						v9AgentNode("implement", buildID, workflow.AgentRoleMaker, "done"),
						v9AgentNodeWithProfile("review", buildID, workflow.AgentRoleChecker, "done", v9ReviewerDef, v9ReviewerVer),
						{Key: "end", Type: workflow.NodeEnd},
					},
					Edges: []workflow.Edge{
						{Key: "start-implement", From: "start", Outcome: "next", To: "implement"},
						{Key: "implement-review", From: "implement", Outcome: "done", To: "review"},
						{Key: "review-end", From: "review", Outcome: "done", To: "end"},
					},
				}
			},
			wantModel: v9ReviewerModel,
		},
		{
			// the same maker alone: it ran last, with profile A's model, so the two
			// models above cannot both come from one worker-wide default.
			name: "the maker's profile model reaches the maker's CLI",
			document: func(buildID string) workflow.WorkflowDocument {
				return workflow.WorkflowDocument{
					SchemaVersion: "1",
					Nodes: []workflow.Node{
						{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
						v9AgentNode("implement", buildID, workflow.AgentRoleMaker, "done"),
						{Key: "end", Type: workflow.NodeEnd},
					},
					Edges: []workflow.Edge{
						{Key: "start-implement", From: "start", Outcome: "next", To: "implement"},
						{Key: "implement-end", From: "implement", Outcome: "done", To: "end"},
					},
				}
			},
			wantModel: v9MakerModel,
		},
	}

	for i, sc := range scenarios {
		sc := sc
		idPrefix := []string{"v9mdla", "v9mdlb"}[i]
		t.Run(sc.name, func(t *testing.T) {
			f := newV5AcceptFixture(t)
			capturePath := filepath.Join(f.fixtureRoot, "fake-cli-capture.json")
			t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
			t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")
			t.Setenv("AGENTKIT_HELPER_WRITE_IN_CWD", v9MakerWrittenFile)
			t.Setenv("AGENTKIT_CAPTURE_PATH", capturePath)

			agents := newV9AgentSetup(t, f)
			publishAgentProfileDocument(t, f.uow, v9ReviewerDef, v9ReviewerVer, agentprofile.AgentProfileDocument{
				ProviderKey: string(ports.ProviderClaude), Model: v9ReviewerModel, ToolRefs: []string{"read_file"},
				ContextPolicyRef: definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v9-context-policy-def", VersionID: "v9-context-policy-v1"},
				Compatibility:    agentprofile.Compatibility{OS: []string{stdruntime.GOOS}},
				Budget:           agentprofile.Budget{MaxTokens: 4096},
			})
			version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, idPrefix+"-workflow-def", idPrefix+"-workflow-v1", sc.document(agents.buildID), workflow.DependencyManifest{})
			router := &runtime.NodeExecutorRouter{Agent: agents.executor}
			startV9Run(t, f, router, agents.registry, version, idPrefix, runtimedomain.WorkflowRunVerifying, nil)

			if got := lastArgvModel(t, capturePath); got != sc.wantModel {
				t.Fatalf("the last CLI was started with --model %q, want %q (its own node's profile)", got, sc.wantModel)
			}
		})
	}
}
