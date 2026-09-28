// V8-01 — Golden workload and trace completeness
// (docs/design/10-v8-alpha-hardening.md V8-01, AK-ARCH-021, HE-11-S03):
// a fixed, deterministic multi-repo/graph/provider/gate workload the rest
// of V8 (V8-02's fault-injection matrix, V8-03's concurrency soak, V8-07's
// performance budgets — all of which name V8-01 as their own dependency)
// can point at as a stable regression baseline, plus a real completeness
// check over the evidence chain and domain-event journal it produces.
//
// Lives in package v6accept (not a new package) deliberately: this
// package's own stack/apiClient/publishedDefinition machinery already
// builds and runs real `aw serve`/`aw worker` processes against a real
// HTTP surface — duplicating that ~500 lines of proven harness for one
// more fixed workload would be pure risk with no benefit. This file adds
// only what V6-14's own journey never needed: a second real provider
// (codex, alongside claude) and a workload spanning two repositories,
// then a NEW verification layer (trace_completeness_test.go) neither V6-14
// nor any other V6/V7 acceptance test required.
package v6accept

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"
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

// goldenReportPathEnvVar names an optional file this test writes its
// traceCompletenessReport to as JSON (in addition to always t.Logf-ing it) —
// mirrors journey's own AW_V6_REPORT_PATH convention (report_test.go) so a
// future CI job can upload it as an artifact without this test caring
// whether one exists. Unset is a normal, complete run: the test's own
// pass/fail (via t.Errorf on any real violation) is what "Hoàn thành khi"
// actually requires, not the file's existence.
const goldenReportPathEnvVar = "AW_V8_GOLDEN_REPORT_PATH"

// goldenGateEvidenceKey mirrors definitions_test.go's own gateEvidenceKey —
// a separate constant, not a reuse of that one, because this workload's own
// gate command/criterion are independent definitions from V6-14's (this
// test publishes its own project, never shares definitions across
// projects).
const goldenGateEvidenceKey = "GOLDEN_OUTPUT_VERIFIED"

// registerAdapterBuild probes then registers one real provider executable
// through the public HTTP flow — generalizes journey.adapterProbeRegister
// (journey_test.go) to any (providerKey, executablePath, capabilities)
// triple, since this workload needs TWO real, distinct providers
// registered (claude and codex), not just one.
func registerAdapterBuild(t *testing.T, api *apiClient, providerKey ports.ProviderKey, caps ports.AgentCapabilities, executablePath string) string {
	t.Helper()
	kinds := make([]string, len(caps.CanonicalEventKinds))
	for i, kind := range caps.CanonicalEventKinds {
		kinds[i] = string(kind)
	}
	manifest := map[string]any{
		"supportsStart": caps.SupportsStart, "supportsResume": caps.SupportsResume,
		"supportsCancel": caps.SupportsCancel, "canonicalEventKinds": kinds,
	}
	probeBody := map[string]any{
		"providerKey": string(providerKey), "executablePath": executablePath,
		"protocolVersion": caps.ProtocolVersion, "capabilityManifest": manifest,
		"os": runtime.GOOS, "toolchain": runtime.Version(), "configIdentity": "v8-golden-workload-" + string(providerKey),
	}
	probe := api.post(t, "/adapter-builds/probe", probeBody, withIdempotencyKey("golden-probe-"+string(providerKey))).requireStatus(t, http.StatusOK, http.StatusCreated)
	candidate := json.RawMessage(probe.body)
	registered := api.post(t, "/adapter-builds", map[string]any{
		"token": candidate, "capabilityManifest": manifest,
	}).requireStatus(t, http.StatusOK, http.StatusCreated)
	var result struct {
		Build struct {
			ID string `json:"id"`
		} `json:"build"`
	}
	registered.decode(t, &result)
	if result.Build.ID == "" {
		t.Fatalf("register %s adapter build returned no id: %s", providerKey, registered.body)
	}
	return result.Build.ID
}

// registerGoldenRepository registers one real local git repository and
// waits for the worker to probe it ACTIVE — mirrors journey.
// projectAndRepository's own repository half exactly, generalized to a
// caller-chosen (repositoryID, path) pair since this workload registers
// two.
func registerGoldenRepository(t *testing.T, api *apiClient, projectID, repositoryID, path string) string {
	t.Helper()
	registered := api.post(t, "/projects/"+projectID+"/repositories", map[string]string{
		"repositoryId": repositoryID, "name": repositoryID, "remoteLocator": path, "defaultRef": "main",
	}).requireStatus(t, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	var repository struct {
		RepositoryID string `json:"repositoryId"`
	}
	registered.decode(t, &repository)
	if repository.RepositoryID == "" {
		t.Fatalf("register repository %s returned no id: %s", repositoryID, registered.body)
	}
	waitFor(t, "repository "+repositoryID+" to be probed ACTIVE by the worker", 60*time.Second, 200*time.Millisecond, func() bool {
		var view struct {
			Status string `json:"status"`
		}
		api.get(t, "/repositories/"+repository.RepositoryID).requireStatus(t, http.StatusOK).decode(t, &view)
		return view.Status == "ACTIVE"
	})
	return repository.RepositoryID
}

// publishGolden authors one definition through the same public
// create->validate->publish HTTP flow journey.publishDefinition uses
// (definitions_test.go) — a free function rather than a *journey method
// since this workload builds its own graph outside any *journey value.
func publishGolden(t *testing.T, api *apiClient, scopePrefix string, kind definition.Kind, definitionID, name string, document any) publishedDefinition {
	t.Helper()
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

// goldenScopeGrant grants WRITE access to every repository this workload
// spans (both, unlike journey.scopeGrant's own single-repository grant) —
// the multi-repository dimension V8-01's own Mục tiêu line names.
func goldenScopeGrant(repositoryIDs []string) []map[string]any {
	grants := make([]map[string]any, len(repositoryIDs))
	for i, id := range repositoryIDs {
		grants[i] = map[string]any{"repositoryId": id, "access": "WRITE", "pathScopes": []string{"src/"}, "reason": "v8 golden workload"}
	}
	return grants
}

// TestV8GoldenWorkload_TraceCompleteness runs the fixed golden workload
// end to end — two repositories, a five-node graph (START -> AGENT(maker,
// claude) -> COMMAND -> MACHINE_GATE -> AGENT(checker, codex) -> END) —
// against real `aw serve`/`aw worker` processes, then verifies the run's
// own evidence chain and domain-event journal are genuinely complete
// (AK-ARCH-021, HE-11-S03). A missing link fails the test; see
// trace_completeness_test.go for what "complete" means here.
func TestV8GoldenWorkload_TraceCompleteness(t *testing.T) {
	requireAcceptance(t)

	s := newStack(t)
	s.codexExecutable = s.bin.fakeCodex
	s.start(t)
	t.Cleanup(func() {
		s.serve.dumpOnFailure(t)
		s.worker.dumpOnFailure(t)
	})
	api := s.api

	// -- two real, distinct providers --
	claudeAdapter, err := claude.New(processadapter.NewSupervisor(), claude.Config{Executable: s.bin.fakeClaude})
	if err != nil {
		t.Fatalf("claude.New: %v", err)
	}
	claudeCaps, err := claudeAdapter.Capabilities(t.Context())
	if err != nil {
		t.Fatalf("measure claude capabilities: %v", err)
	}
	claudeBuildID := registerAdapterBuild(t, api, ports.ProviderClaude, claudeCaps, s.bin.fakeClaude)

	codexAdapter, err := codex.New(processadapter.NewSupervisor(), codex.Config{Executable: s.bin.fakeCodex})
	if err != nil {
		t.Fatalf("codex.New: %v", err)
	}
	codexCaps, err := codexAdapter.Capabilities(t.Context())
	if err != nil {
		t.Fatalf("measure codex capabilities: %v", err)
	}
	codexBuildID := registerAdapterBuild(t, api, ports.ProviderCodex, codexCaps, s.bin.fakeCodex)

	// -- project + two real local git repositories --
	var created struct {
		ProjectID string `json:"projectId"`
	}
	api.post(t, "/projects", map[string]string{"name": "golden-workload"}).requireStatus(t, http.StatusCreated).decode(t, &created)
	projectID := created.ProjectID
	if projectID == "" {
		t.Fatal("project create returned no id")
	}

	repoAlphaPath := createGitRepository(t, filepath.Join(s.root, "golden-repo-alpha-origin"))
	repoBetaPath := createGitRepository(t, filepath.Join(s.root, "golden-repo-beta-origin"))
	repoAlphaID := registerGoldenRepository(t, api, projectID, "golden-repo-alpha", repoAlphaPath)
	repoBetaID := registerGoldenRepository(t, api, projectID, "golden-repo-beta", repoBetaPath)
	repositoryIDs := []string{repoAlphaID, repoBetaID}

	// -- definitions: policies, two agent profiles (one per provider), the
	// maker/gate scripts, the MACHINE_GATE, the workflow graph --
	projectPrefix := "/projects/" + projectID
	osCompat := command.Compatibility{OS: []string{runtime.GOOS}}

	contextPolicy := publishGolden(t, api, "", definition.KindPolicy, "golden-ctx-policy", "golden context policy", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context:  &policy.ContextRules{Selector: []string{"v8-golden-agent-context"}, Budget: policy.ContextBudget{MaxTokens: 4096}},
	})
	makerProfile := publishGolden(t, api, "", definition.KindAgentProfile, "golden-maker-profile", "golden maker profile", agentprofile.AgentProfileDocument{
		ProviderKey: string(ports.ProviderClaude), Model: "fake-model", ToolRefs: []string{"read_file"},
		ContextPolicyRef: contextPolicy.pin(definition.KindPolicy),
		Compatibility:    agentprofile.Compatibility{OS: []string{runtime.GOOS}},
		Budget:           agentprofile.Budget{MaxTokens: 4096},
	})
	checkerProfile := publishGolden(t, api, "", definition.KindAgentProfile, "golden-checker-profile", "golden checker profile", agentprofile.AgentProfileDocument{
		ProviderKey: string(ports.ProviderCodex), Model: "fake-model", ToolRefs: []string{"read_file"},
		ContextPolicyRef: contextPolicy.pin(definition.KindPolicy),
		Compatibility:    agentprofile.Compatibility{OS: []string{runtime.GOOS}},
		Budget:           agentprofile.Budget{MaxTokens: 4096},
	})
	attemptPolicy := publishGolden(t, api, "", definition.KindPolicy, "golden-attempt-policy", "golden attempt policy", policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt:  &policy.AttemptRules{MaxAttempts: 3, BackoffSeconds: 1, TimeoutSeconds: 60},
	})
	permissionPolicy := publishGolden(t, api, "", definition.KindPolicy, "golden-permission-policy", "golden permission policy", policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierOperatorTrustedLocal, GrantedCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		},
	})
	attemptRefs := []definition.DependencyPin{attemptPolicy.pin(definition.KindPolicy), permissionPolicy.pin(definition.KindPolicy)}

	markerPath := filepath.Join(s.root, "golden-maker-marker.txt")
	makerKey, makerScript, gateKey, gateScript := goldenJourneyScripts(markerPath)
	provenance := skill.Provenance{Owner: "v8golden", Source: "fixture", Revision: "v1"}
	skillDocument := skill.SkillDocument{Resources: []skill.Resource{
		{Key: makerKey, Instruction: makerScript, Priority: definition.PriorityGuidance, Global: true, Provenance: provenance},
		{Key: gateKey, Instruction: gateScript, Priority: definition.PriorityGuidance, Global: true, Provenance: provenance},
	}}
	scripts := publishGolden(t, api, "", definition.KindSkill, "golden-scripts", "golden acceptance scripts", skillDocument)
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
			CwdRepositoryTarget: repoAlphaID,
			Compatibility:       osCompat,
			NetworkAccess:       command.NetworkAccessNone,
			TimeoutSeconds:      60,
			Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
		}
	}
	makerCommand := publishGolden(t, api, "", definition.KindCommand, "golden-maker-command", "golden maker command", commandDocument(makerKey))
	gateCommand := publishGolden(t, api, "", definition.KindCommand, "golden-gate-command", "golden gate command", commandDocument(gateKey))
	machineGate := publishGolden(t, api, "", definition.KindGate, "golden-machine-gate", "golden machine gate", gate.GateDocument{
		CommandRef: gateCommand.pin(definition.KindCommand),
		Criteria:   []gate.Criterion{{Name: "golden-output-verified", EvidenceKey: goldenGateEvidenceKey}},
	})
	completionPolicy := publishGolden(t, api, "", definition.KindPolicy, "golden-completion-policy", "golden completion policy", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindCommandExecution, goldenGateEvidenceKey}},
	})
	completionRef := completionPolicy.pin(definition.KindPolicy)
	claudeBuildIDCopy, codexBuildIDCopy := claudeBuildID, codexBuildID
	graph := workflow.WorkflowDocument{
		SchemaVersion:       "1",
		CompletionPolicyRef: &completionRef,
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "maker", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: makerProfile.pin(definition.KindAgentProfile), PolicyRefs: attemptRefs,
				AdapterBuildID: &claudeBuildIDCopy, Role: workflow.AgentRoleMaker,
			}},
			{Key: "test_a", Type: workflow.NodeCommand, Outcomes: []string{"passed"}, Command: &workflow.CommandNodeConfig{
				CommandRef: makerCommand.pin(definition.KindCommand), PolicyRefs: attemptRefs,
			}},
			{Key: "gate_b", Type: workflow.NodeMachineGate, Outcomes: []string{"passed"}, MachineGate: &workflow.MachineGateNodeConfig{
				GateRef: machineGate.pin(definition.KindGate), PolicyRefs: attemptRefs,
			}},
			{Key: "checker", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: checkerProfile.pin(definition.KindAgentProfile), PolicyRefs: attemptRefs,
				AdapterBuildID: &codexBuildIDCopy, Role: workflow.AgentRoleChecker,
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
	wf := publishGolden(t, api, projectPrefix, definition.KindWorkflow, "golden-workflow", "golden workflow", graph)

	// -- root WorkItem (family container, scoped to BOTH repositories) plus
	// the CHILD WorkItem that actually pins and runs the workflow — mirrors
	// journey.workItemAndRun/createChild exactly: a workflow only ever runs
	// on a child, never directly on the root (the root's own
	// work_item_effective_scopes row is not what a NodeRun's execution
	// resolves EffectiveScope against — found live the first time this
	// workload tried to run directly on the root and every COMMAND/AGENT
	// node failed VALIDATION_FAILED with "cwd repository target ... is not
	// in this node run's effective scope" despite the root's own detail
	// view already showing both repositories provisioned).
	root := api.post(t, projectPrefix+"/work-items", map[string]any{
		"title": "golden-workload-root", "initialScope": goldenScopeGrant(repositoryIDs),
	}).requireStatus(t, http.StatusCreated)
	var rootResult struct {
		WorkItemID string `json:"workItemId"`
		FamilyID   string `json:"familyId"`
	}
	root.decode(t, &rootResult)
	if rootResult.WorkItemID == "" || rootResult.FamilyID == "" {
		t.Fatalf("root work item result is missing ids: %s", root.body)
	}
	rootWorkItemID, familyID := rootResult.WorkItemID, rootResult.FamilyID

	child := api.post(t, projectPrefix+"/work-items/"+rootWorkItemID+"/children", map[string]any{
		"title": "golden-workload-child", "parentJoinPolicy": "v8-golden-workload-child", "effectiveScope": goldenScopeGrant(repositoryIDs),
		"contract": map[string]any{
			"schemaVersion":      1,
			"behavior":           "the golden workload's own multi-repo/multi-provider/gate graph completes",
			"acceptanceCriteria": []map[string]any{{"description": "the workflow's own verification passes", "verificationRef": wf.definitionID}},
			"verificationSpec":   "see the referenced workflow",
			"riskLevel":          "LOW",
			"exclusions":         []string{"no network access"},
			"workflowVersionId":  wf.versionID,
		},
	}).requireStatus(t, http.StatusCreated)
	var childResult struct {
		WorkItemID string `json:"workItemId"`
	}
	child.decode(t, &childResult)
	if childResult.WorkItemID == "" {
		t.Fatalf("child work item result has no id: %s", child.body)
	}
	workItemID := childResult.WorkItemID

	waitFor(t, "workspace set to be provisioned READY by the worker", 90*time.Second, 300*time.Millisecond, func() bool {
		var state struct {
			State string `json:"state"`
		}
		api.get(t, projectPrefix+"/workspace-sets/"+familyID).requireStatus(t, http.StatusOK).decode(t, &state)
		return state.State == "READY"
	})

	var readiness struct {
		Ready    bool     `json:"ready"`
		Problems []string `json:"problems"`
	}
	api.get(t, projectPrefix+"/work-items/"+workItemID+"/readiness").requireStatus(t, http.StatusOK).decode(t, &readiness)
	if !readiness.Ready {
		t.Fatalf("golden work item is not ready: %v", readiness.Problems)
	}
	view := api.get(t, projectPrefix+"/work-items/"+workItemID).requireStatus(t, http.StatusOK)
	api.post(t, "/work-items/"+workItemID+"/mark-ready", map[string]any{}, withIfMatch(view.etag())).requireStatus(t, http.StatusOK)

	started := api.post(t, "/work-items/"+workItemID+"/runs", map[string]any{"workflowVersionId": wf.versionID}).requireStatus(t, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	var runResult struct {
		RunID string `json:"runId"`
	}
	started.decode(t, &runResult)
	if runResult.RunID == "" {
		t.Fatalf("start run returned no run id: %s", started.body)
	}
	runID := runResult.RunID

	var finalState string
	waitFor(t, "golden run "+runID+" to reach a terminal state", 4*time.Minute, 500*time.Millisecond, func() bool {
		var runView struct {
			State string `json:"state"`
		}
		api.get(t, "/runs/"+runID).requireStatus(t, http.StatusOK).decode(t, &runView)
		finalState = runView.State
		switch runView.State {
		case "SUCCEEDED", "FAILED", "CANCELLED", "BLOCKED":
			return true
		}
		return false
	})
	if finalState != "SUCCEEDED" {
		diag := api.get(t, projectPrefix+"/runs/"+runID+"/diagnostics")
		timeline := api.get(t, "/runs/"+runID+"/timeline")
		t.Fatalf("golden run settled in %s, want SUCCEEDED\ndiagnostics: %s\ntimeline: %s", finalState, diag.body, timeline.body)
	}

	// -- AK-ARCH-021's own evidence chain, through the real public evidence
	// routes, while serve is still up --
	report := traceCompletenessReport{ProjectID: projectID, WorkItemID: workItemID, RunID: runID}
	verifyEvidenceChain(t, api, projectID, workItemID, runID, repositoryIDs, &report)

	// -- graceful shutdown BEFORE direct db introspection: the domain-event
	// journal half opens a second, read-only connection to the same sqlite
	// file (matching internal/integration's own V4-14 precedent,
	// runtimeengine_test.go), simplest to reason about once neither real
	// process still holds it open for writes.
	serveGraceful, workerGraceful := s.stop(t)
	if !serveGraceful || !workerGraceful {
		t.Fatalf("shutdown was not graceful: serve=%v worker=%v", serveGraceful, workerGraceful)
	}
	verifyDomainEventJournal(t, s, projectID, &report)
	sort.Strings(report.Violations)

	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal trace completeness report: %v", err)
	}
	t.Logf("trace completeness report:\n%s", reportJSON)
	if path := os.Getenv(goldenReportPathEnvVar); path != "" {
		if err := os.WriteFile(path, reportJSON, 0o644); err != nil {
			t.Fatalf("write trace completeness report to %s: %v", path, err)
		}
	}
	if len(report.Violations) > 0 {
		t.Fatalf("trace completeness FAILED with %d violation(s):\n%s", len(report.Violations), reportJSON)
	}
}

// goldenJourneyScripts mirrors definitions_test.go's own journeyScripts —
// a separate copy (not a shared call) because the two use different marker
// paths and this one's gate reports goldenGateEvidenceKey rather than
// gateEvidenceKey; sharing one function across both meanings would either
// force a parameter neither caller's own file otherwise needs or blur two
// genuinely independent fixtures into one.
func goldenJourneyScripts(markerPath string) (makerKey, makerScript, gateKey, gateScript string) {
	if runtime.GOOS == "windows" {
		return "golden-maker.bat", "@echo off\r\necho marker> \"" + markerPath + "\"\r\nexit /b 0\r\n",
			"golden-gate.bat", "@echo off\r\nif exist \"" + markerPath + "\" (\r\n  echo {\"" + goldenGateEvidenceKey + "\":{\"verdict\":\"PASS\"}}\r\n) else (\r\n  echo {\"" + goldenGateEvidenceKey + "\":{\"verdict\":\"FAIL\"}}\r\n)\r\nexit /b 0\r\n"
	}
	return "golden-maker.sh", "#!/bin/sh\necho marker > \"" + markerPath + "\"\nexit 0\n",
		"golden-gate.sh", "#!/bin/sh\nif [ -f \"" + markerPath + "\" ]; then\n  echo '{\"" + goldenGateEvidenceKey + "\":{\"verdict\":\"PASS\"}}'\nelse\n  echo '{\"" + goldenGateEvidenceKey + "\":{\"verdict\":\"FAIL\"}}'\nfi\nexit 0\n"
}
