package v6accept

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// gateEvidenceKey is the one evidence key the gate script reports and the
// completion policy requires — a single constant so the script, the authored
// Criterion and the policy cannot drift apart.
const gateEvidenceKey = "OUTPUT_VERIFIED"

// publishedDefinition is what publishing one definition version returns.
type publishedDefinition struct {
	definitionID string
	versionID    string
	compiledHash string
}

// pin is the dependency reference other definitions use to name this version.
func (p publishedDefinition) pin(kind definition.Kind) definition.DependencyPin {
	return definition.DependencyPin{Kind: kind, DefinitionID: p.definitionID, VersionID: p.versionID}
}

// publishDefinition authors one definition through the public HTTP flow the
// product documents: create the Definition, validate the draft document, then
// publish an immutable version. scopePrefix is "" for an installation
// definition or "/projects/{id}" for a project one.
func (j *journey) publishDefinition(t *testing.T, scopePrefix string, kind definition.Kind, definitionID, name string, document any) publishedDefinition {
	t.Helper()
	api := j.s.api
	content, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode %s document: %v", kind, err)
	}
	base := scopePrefix + "/definitions/" + string(kind)

	api.post(t, base, map[string]string{"definitionId": definitionID, "name": name}).requireStatus(t, http.StatusCreated)

	body := map[string]any{"content": string(content), "format": "json", "schemaVersion": 1}
	validated := api.post(t, base+"/"+definitionID+"/validate", body)
	if validated.status != http.StatusOK {
		t.Fatalf("validate %s %s = %d: %s", kind, definitionID, validated.status, validated.body)
	}

	published := api.post(t, base+"/"+definitionID+"/publish", body).requireStatus(t, http.StatusCreated)
	var version struct {
		ID           string `json:"id"`
		CompiledHash string `json:"compiledHash"`
	}
	published.decode(t, &version)
	if version.ID == "" || version.CompiledHash == "" {
		t.Fatalf("publish %s %s returned no version id/compiled hash: %s", kind, definitionID, published.body)
	}
	return publishedDefinition{definitionID: definitionID, versionID: version.ID, compiledHash: version.CompiledHash}
}

// journeyScripts returns the two cross-platform scripts the COMMAND and
// MACHINE_GATE nodes run. The marker they share lives OUTSIDE the repository
// working tree on purpose: a MACHINE_GATE or CHECKER attempt requires the
// repository diff to be empty relative to the Run's pinned base revision, so
// a maker that wrote inside the tree would make every later read-only node
// fail by design (V5-15's own recorded finding). The gate still reports PASS
// only by finding the maker's real file on disk.
func journeyScripts(markerPath string) (makerKey, makerScript, gateKey, gateScript string) {
	if runtime.GOOS == "windows" {
		return "maker.bat", "@echo off\r\necho marker> \"" + markerPath + "\"\r\nexit /b 0\r\n",
			"gate.bat", "@echo off\r\nif exist \"" + markerPath + "\" (\r\n  echo {\"" + gateEvidenceKey + "\":{\"verdict\":\"PASS\"}}\r\n) else (\r\n  echo {\"" + gateEvidenceKey + "\":{\"verdict\":\"FAIL\"}}\r\n)\r\nexit /b 0\r\n"
	}
	return "maker.sh", "#!/bin/sh\necho marker > \"" + markerPath + "\"\nexit 0\n",
		"gate.sh", "#!/bin/sh\nif [ -f \"" + markerPath + "\" ]; then\n  echo '{\"" + gateEvidenceKey + "\":{\"verdict\":\"PASS\"}}'\nelse\n  echo '{\"" + gateEvidenceKey + "\":{\"verdict\":\"FAIL\"}}'\nfi\nexit 0\n"
}

// verificationWorkflow holds the definitions the four-role graph needs.
type verificationWorkflow struct {
	workflow publishedDefinition
}

// publishVerificationWorkflow authors, over HTTP, every definition the
// START → AGENT(maker) → COMMAND → MACHINE_GATE → AGENT(checker) → END graph
// needs and publishes the project-scoped workflow that ties them together.
// Version ids are the ones the server returned; nothing is chosen by the
// test.
func (j *journey) publishVerificationWorkflow(t *testing.T) verificationWorkflow {
	t.Helper()
	osCompat := command.Compatibility{OS: []string{runtime.GOOS}}
	projectPrefix := "/projects/" + j.projectID

	contextPolicy := j.publishDefinition(t, "", definition.KindPolicy, "ctx-policy", "context policy", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context:  &policy.ContextRules{Selector: []string{"v6accept-agent-context"}, Budget: policy.ContextBudget{MaxTokens: 4096}},
	})
	agentProfile := j.publishDefinition(t, "", definition.KindAgentProfile, "agent-profile", "agent profile", agentprofile.AgentProfileDocument{
		ProviderKey: string(ports.ProviderClaude), Model: "fake-model", ToolRefs: []string{"read_file"},
		ContextPolicyRef: contextPolicy.pin(definition.KindPolicy),
		Compatibility:    agentprofile.Compatibility{OS: []string{runtime.GOOS}},
		Budget:           agentprofile.Budget{MaxTokens: 4096},
	})
	attemptPolicy := j.publishDefinition(t, "", definition.KindPolicy, "attempt-policy", "attempt policy", policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt:  &policy.AttemptRules{MaxAttempts: 3, BackoffSeconds: 1, TimeoutSeconds: 60},
	})
	permissionPolicy := j.publishDefinition(t, "", definition.KindPolicy, "permission-policy", "permission policy", policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierOperatorTrustedLocal, GrantedCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		},
	})

	markerPath := filepath.Join(j.s.root, "maker-marker.txt")
	makerKey, makerScript, gateKey, gateScript := journeyScripts(markerPath)
	provenance := skill.Provenance{Owner: "v6accept", Source: "fixture", Revision: "v1"}
	skillDocument := skill.SkillDocument{Resources: []skill.Resource{
		{Key: makerKey, Instruction: makerScript, Priority: definition.PriorityGuidance, Global: true, Provenance: provenance},
		{Key: gateKey, Instruction: gateScript, Priority: definition.PriorityGuidance, Global: true, Provenance: provenance},
	}}
	scripts := j.publishDefinition(t, "", definition.KindSkill, "scripts", "acceptance scripts", skillDocument)
	identities, err := skill.ResourceIdentities(skill.SkillVersionID(scripts.versionID), skillDocument)
	if err != nil {
		t.Fatalf("skill.ResourceIdentities: %v", err)
	}
	hashOf := func(key string) string {
		for _, identity := range identities {
			if identity.Identity.ResourceKey == key {
				return identity.Identity.ContentHash
			}
		}
		t.Fatalf("no content hash for skill resource %q", key)
		return ""
	}

	commandDocument := func(resourceKey string) command.CommandDocument {
		return command.CommandDocument{
			Executable:          command.ExecutableRef{OwnerVersionID: scripts.versionID, ResourceKey: resourceKey, ContentHash: hashOf(resourceKey)},
			Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
			CwdRepositoryTarget: j.repositoryID,
			Compatibility:       osCompat,
			NetworkAccess:       command.NetworkAccessNone,
			TimeoutSeconds:      60,
			Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
		}
	}
	makerCommand := j.publishDefinition(t, "", definition.KindCommand, "maker-command", "maker command", commandDocument(makerKey))
	gateCommand := j.publishDefinition(t, "", definition.KindCommand, "gate-command", "gate command", commandDocument(gateKey))
	machineGate := j.publishDefinition(t, "", definition.KindGate, "machine-gate", "machine gate", gate.GateDocument{
		CommandRef: gateCommand.pin(definition.KindCommand),
		Criteria:   []gate.Criterion{{Name: "output-verified", EvidenceKey: gateEvidenceKey}},
	})
	completionPolicy := j.publishDefinition(t, "", definition.KindPolicy, "completion-policy", "completion policy", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindCommandExecution, gateEvidenceKey}},
	})

	attemptRefs := []definition.DependencyPin{attemptPolicy.pin(definition.KindPolicy), permissionPolicy.pin(definition.KindPolicy)}
	buildID := j.adapterBuildID
	completionRef := completionPolicy.pin(definition.KindPolicy)
	graph := workflow.WorkflowDocument{
		SchemaVersion:       "1",
		CompletionPolicyRef: &completionRef,
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "maker", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: agentProfile.pin(definition.KindAgentProfile), PolicyRefs: attemptRefs,
				AdapterBuildID: &buildID, Role: workflow.AgentRoleMaker,
			}},
			{Key: "test_a", Type: workflow.NodeCommand, Outcomes: []string{"passed"}, Command: &workflow.CommandNodeConfig{
				CommandRef: makerCommand.pin(definition.KindCommand), PolicyRefs: attemptRefs,
			}},
			{Key: "gate_b", Type: workflow.NodeMachineGate, Outcomes: []string{"passed"}, MachineGate: &workflow.MachineGateNodeConfig{
				GateRef: machineGate.pin(definition.KindGate), PolicyRefs: attemptRefs,
			}},
			{Key: "checker", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: agentProfile.pin(definition.KindAgentProfile), PolicyRefs: attemptRefs,
				AdapterBuildID: &buildID, Role: workflow.AgentRoleChecker,
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-maker", From: "start", Outcome: "next", To: "maker"},
			{Key: "maker-test_a", From: "maker", Outcome: "done", To: "test_a"},
			{Key: "test_a-gate_b", From: "test_a", Outcome: "passed", To: "gate_b"},
			{Key: "gate_b-checker", From: "gate_b", Outcome: "passed", To: "checker"},
			{Key: "checker-end", From: "checker", Outcome: "done", To: "end"},
		},
	}
	wf := j.publishDefinition(t, projectPrefix, definition.KindWorkflow, "verify-workflow", "verification workflow", graph)
	_ = os.Stdout
	return verificationWorkflow{workflow: wf}
}
