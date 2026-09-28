// V8-03 — Concurrency/race/stability soak (docs/design/10-v8-alpha-hardening.md
// V8-03, AK-ARCH-009/GC-ACC-09): multiple real root WorkItem families started
// CONCURRENTLY against one real `aw serve`+`aw worker` pair — two sharing the
// SAME repository (forcing real serialization at that repository's own write
// lease) and two each on their OWN distinct repository (free to run in
// parallel) — with a genuinely duplicated command (the identical
// Idempotency-Key fired from several goroutines at once, not sequentially
// the way stage_fault_concurrency_test.go's own two scenarios already prove)
// racing to start each family's run. AK-ARCH-009's own invariant — a stale
// lease/fencing token can never commit after expiry or reassignment — is
// checked the only way that is actually decisive: after every run settles,
// each one's own real timeline must show EXACTLY one NODE_RUN and one
// EXECUTION_ATTEMPT reaching a terminal SUCCEEDED state, never two (which
// would mean a stale/duplicate attempt was allowed through).
//
// This package's own real-process harness (newStack/apiClient) and V8-01's
// golden-workload helpers (registerAdapterBuild/registerGoldenRepository/
// publishGolden) are reused verbatim — this file adds only the concurrency
// orchestration itself.
package v6accept

import (
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// soakDuplicateSubmissions is how many concurrent goroutines fire the
// IDENTICAL start-run command (same Idempotency-Key) per family — the
// "duplicate commands/events" half of V8-03's own Thực hiện line.
const soakDuplicateSubmissions = 5

// soakFamilySpec names one family this soak drives: which repository it is
// scoped to (soakSharedRepo appears twice, on purpose — two families
// genuinely contending for the SAME repository's own write lease).
type soakFamilySpec struct {
	name         string
	repositoryID string
}

// soakFamilyResult is what driveSoakFamily reports back over its channel.
type soakFamilyResult struct {
	name           string
	runID          string
	finalState     string
	distinctRunIDs map[string]bool
	err            error
}

// TestV8ConcurrencySoak_MultiFamilyRaceInvariants is V8-03's own primary
// scenario. A single run of the full soak; the v8-concurrency-soak CI job
// repeats this test 10 times with -race, matching V0-12's own established
// "10 runs, no retry until green" convention (docs/design/10-v8-alpha-
// hardening.md V8-03's own "Hoàn thành khi: 10/10 pass và stale fence
// success bằng 0").
func TestV8ConcurrencySoak_MultiFamilyRaceInvariants(t *testing.T) {
	requireAcceptance(t)
	s := newStack(t)
	s.start(t)
	t.Cleanup(func() {
		s.serve.dumpOnFailure(t)
		s.worker.dumpOnFailure(t)
	})
	api := s.api

	var created struct {
		ProjectID string `json:"projectId"`
	}
	api.post(t, "/projects", map[string]string{"name": "concurrency-soak"}).requireStatus(t, http.StatusCreated).decode(t, &created)
	projectID := created.ProjectID
	if projectID == "" {
		t.Fatal("project create returned no id")
	}
	projectPrefix := "/projects/" + projectID

	repoSharedPath := createGitRepository(t, filepath.Join(s.root, "soak-repo-shared-origin"))
	repoCPath := createGitRepository(t, filepath.Join(s.root, "soak-repo-c-origin"))
	repoDPath := createGitRepository(t, filepath.Join(s.root, "soak-repo-d-origin"))
	repoSharedID := registerGoldenRepository(t, api, projectID, "soak-repo-shared", repoSharedPath)
	repoCID := registerGoldenRepository(t, api, projectID, "soak-repo-c", repoCPath)
	repoDID := registerGoldenRepository(t, api, projectID, "soak-repo-d", repoDPath)

	wf := publishSoakWorkflow(t, api, projectPrefix)

	families := []soakFamilySpec{
		{name: "family-a", repositoryID: repoSharedID}, // shares soak-repo-shared with family-b
		{name: "family-b", repositoryID: repoSharedID}, // real same-repository contention
		{name: "family-c", repositoryID: repoCID},       // independent repository
		{name: "family-d", repositoryID: repoDID},       // independent repository
	}

	var wg sync.WaitGroup
	resultsCh := make(chan soakFamilyResult, len(families))
	for _, family := range families {
		wg.Add(1)
		go func(family soakFamilySpec) {
			defer wg.Done()
			resultsCh <- driveSoakFamily(t, s, api, projectID, projectPrefix, family, wf)
		}(family)
	}
	wg.Wait()
	close(resultsCh)

	results := make(map[string]soakFamilyResult, len(families))
	for result := range resultsCh {
		results[result.name] = result
	}

	for _, family := range families {
		result, ok := results[family.name]
		if !ok {
			t.Errorf("%s: no result reported", family.name)
			continue
		}
		if result.err != nil {
			t.Errorf("%s: %v", family.name, result.err)
			continue
		}
		if result.finalState != "SUCCEEDED" {
			t.Errorf("%s: run settled in %s, want SUCCEEDED", family.name, result.finalState)
		}
		// The duplicate-command half of AK-ARCH-009: soakDuplicateSubmissions
		// concurrent, identically-keyed start-run calls must all resolve to
		// the SAME run — real idempotent dedup under genuine concurrent load,
		// not merely sequential replay.
		if len(result.distinctRunIDs) != 1 {
			t.Errorf("%s: %d concurrent identically-keyed start-run calls resolved to %d distinct run id(s), want exactly 1: %v",
				family.name, soakDuplicateSubmissions, len(result.distinctRunIDs), keysOf(result.distinctRunIDs))
		}
	}
	if t.Failed() {
		return
	}

	// The other half of AK-ARCH-009: a stale/fenced token must never commit.
	// Checked the only decisive way — each family's own real timeline shows
	// EXACTLY one terminal SUCCEEDED NodeRun/ExecutionAttempt for its single
	// "maker" node, never two (which would mean a duplicate/stale attempt
	// slipped through as a second authoritative outcome).
	for _, family := range families {
		result := results[family.name]
		verifySoakNoStaleFenceSuccess(t, api, result.runID, family.name)
	}
}

// driveSoakFamily creates one family end to end (root -> child -> READY ->
// N concurrent identically-keyed start-run calls -> wait SUCCEEDED) and
// reports the outcome. Never calls t.Fatalf: a family failing must not abort
// the other three goroutines' own work, so every failure is carried back in
// the result for the caller to report after all four have finished.
func driveSoakFamily(t *testing.T, s *stack, api *apiClient, projectID, projectPrefix string, family soakFamilySpec, wf publishedDefinition) soakFamilyResult {
	result := soakFamilyResult{name: family.name, distinctRunIDs: map[string]bool{}}
	grant := []map[string]any{{"repositoryId": family.repositoryID, "access": "WRITE", "pathScopes": []string{"src/"}, "reason": "v8 concurrency soak"}}

	root := api.post(t, projectPrefix+"/work-items", map[string]any{
		"title": family.name + "-root", "initialScope": grant,
	})
	if root.status != http.StatusCreated {
		result.err = fmt.Errorf("create root work item: status=%d body=%s", root.status, root.body)
		return result
	}
	var rootResult struct {
		WorkItemID string `json:"workItemId"`
		FamilyID   string `json:"familyId"`
	}
	root.decode(t, &rootResult)
	if rootResult.WorkItemID == "" || rootResult.FamilyID == "" {
		result.err = fmt.Errorf("root work item result missing ids: %s", root.body)
		return result
	}

	child := api.post(t, projectPrefix+"/work-items/"+rootResult.WorkItemID+"/children", map[string]any{
		"title": family.name + "-child", "parentJoinPolicy": "v8-soak-" + family.name, "effectiveScope": grant,
		"contract": map[string]any{
			"schemaVersion":      1,
			"behavior":           "the concurrency soak's own single-agent graph completes",
			"acceptanceCriteria": []map[string]any{{"description": "the workflow's own verification passes", "verificationRef": wf.definitionID}},
			"verificationSpec":   "see the referenced workflow",
			"riskLevel":          "LOW",
			"exclusions":         []string{"no network access"},
			"workflowVersionId":  wf.versionID,
		},
	})
	if child.status != http.StatusCreated {
		result.err = fmt.Errorf("create child work item: status=%d body=%s", child.status, child.body)
		return result
	}
	var childResult struct {
		WorkItemID string `json:"workItemId"`
	}
	child.decode(t, &childResult)
	if childResult.WorkItemID == "" {
		result.err = fmt.Errorf("child work item result has no id: %s", child.body)
		return result
	}
	workItemID := childResult.WorkItemID

	deadline := time.Now().Add(90 * time.Second)
	for {
		state := api.get(t, projectPrefix+"/workspace-sets/"+rootResult.FamilyID)
		var view struct {
			State string `json:"state"`
		}
		state.decode(t, &view)
		if view.State == "READY" {
			break
		}
		if time.Now().After(deadline) {
			result.err = fmt.Errorf("workspace set for family %s never reached READY", family.name)
			return result
		}
		time.Sleep(300 * time.Millisecond)
	}

	var readiness struct {
		Ready    bool     `json:"ready"`
		Problems []string `json:"problems"`
	}
	api.get(t, projectPrefix+"/work-items/"+workItemID+"/readiness").decode(t, &readiness)
	if !readiness.Ready {
		result.err = fmt.Errorf("work item is not ready: %v", readiness.Problems)
		return result
	}
	view := api.get(t, projectPrefix+"/work-items/"+workItemID)
	if mr := api.post(t, "/work-items/"+workItemID+"/mark-ready", map[string]any{}, withIfMatch(view.etag())); mr.status != http.StatusOK {
		result.err = fmt.Errorf("mark-ready: status=%d body=%s", mr.status, mr.body)
		return result
	}

	// The duplicate-command race: soakDuplicateSubmissions goroutines, the
	// IDENTICAL Idempotency-Key, fired as close to simultaneously as this
	// process can manage.
	idempotencyKey := "v8-soak-start-run-" + family.name
	var raceWG sync.WaitGroup
	var raceMu sync.Mutex
	var raceErrs []string
	raceWG.Add(soakDuplicateSubmissions)
	for i := 0; i < soakDuplicateSubmissions; i++ {
		go func() {
			defer raceWG.Done()
			response := api.post(t, "/work-items/"+workItemID+"/runs", map[string]any{"workflowVersionId": wf.versionID}, withIdempotencyKey(idempotencyKey))
			raceMu.Lock()
			defer raceMu.Unlock()
			switch response.status {
			case http.StatusOK, http.StatusCreated, http.StatusAccepted:
				var run struct {
					RunID string `json:"runId"`
				}
				response.decode(t, &run)
				if run.RunID == "" {
					raceErrs = append(raceErrs, fmt.Sprintf("start-run returned no run id: %s", response.body))
					return
				}
				result.distinctRunIDs[run.RunID] = true
			default:
				raceErrs = append(raceErrs, fmt.Sprintf("start-run status=%d body=%s", response.status, response.body))
			}
		}()
	}
	raceWG.Wait()
	if len(raceErrs) > 0 {
		result.err = fmt.Errorf("%d/%d concurrent start-run calls failed: %v", len(raceErrs), soakDuplicateSubmissions, raceErrs)
		return result
	}
	for runID := range result.distinctRunIDs {
		result.runID = runID
	}
	if result.runID == "" {
		result.err = fmt.Errorf("no run id recorded from any concurrent start-run call")
		return result
	}

	settleDeadline := time.Now().Add(2 * time.Minute)
	for {
		runView := api.get(t, "/runs/"+result.runID)
		var view struct {
			State string `json:"state"`
		}
		runView.decode(t, &view)
		result.finalState = view.State
		switch view.State {
		case "SUCCEEDED", "FAILED", "CANCELLED", "BLOCKED":
			return result
		}
		if time.Now().After(settleDeadline) {
			result.err = fmt.Errorf("run %s never reached a terminal state (last=%s)", result.runID, view.State)
			return result
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// verifySoakNoStaleFenceSuccess reads runID's own real timeline and asserts
// exactly one NODE_RUN and one EXECUTION_ATTEMPT reached a terminal
// SUCCEEDED state for the "maker" node — the decisive check that no
// stale/fenced token was ever also allowed to commit a second authoritative
// outcome (AK-ARCH-009).
func verifySoakNoStaleFenceSuccess(t *testing.T, api *apiClient, runID, familyName string) {
	t.Helper()
	var timeline struct {
		Entries []struct {
			Kind         string `json:"kind"`
			NodeKey      string `json:"nodeKey"`
			NodeState    string `json:"nodeState,omitempty"`
			AttemptState string `json:"attemptState,omitempty"`
		} `json:"entries"`
	}
	api.get(t, "/runs/"+runID+"/timeline").requireStatus(t, http.StatusOK).decode(t, &timeline)

	succeededNodeRuns, succeededAttempts := 0, 0
	for _, entry := range timeline.Entries {
		if entry.NodeKey != "maker" {
			continue
		}
		switch entry.Kind {
		case "NODE_RUN":
			if entry.NodeState == "SUCCEEDED" {
				succeededNodeRuns++
			}
		case "EXECUTION_ATTEMPT":
			if entry.AttemptState == "SUCCEEDED" {
				succeededAttempts++
			}
		}
	}
	if succeededNodeRuns != 1 {
		t.Errorf("%s: run %s has %d SUCCEEDED NodeRun(s) for node \"maker\", want exactly 1 (a stale/duplicate activation would show more)", familyName, runID, succeededNodeRuns)
	}
	if succeededAttempts != 1 {
		t.Errorf("%s: run %s has %d SUCCEEDED ExecutionAttempt(s) for node \"maker\", want exactly 1 (a stale-fenced token committing would show more)", familyName, runID, succeededAttempts)
	}
}

// soakGateEvidenceKey is the one evidence key the soak's own always-PASS
// gate script reports and its completion policy requires.
const soakGateEvidenceKey = "SOAK_OUTPUT_VERIFIED"

// publishSoakWorkflow authors a minimal, fast START -> MACHINE_GATE -> END
// graph — a single MACHINE_GATE, not an AGENT or COMMAND node, deliberately:
// GateNodeExecutor always runs its own command against a FRESH SCRATCH
// DIRECTORY, never a real repository workspace mount (unlike
// resolveCommandInvocation's own CwdRepositoryTarget lookup, which requires
// the target repository to be in THIS node run's own effective scope — see
// V8-01's own golden_workload_test.go finding). That makes this ONE
// published workflow version usable, unmodified, across all four soak
// families even though they are scoped to three DIFFERENT repositories —
// exactly what this soak needs, since its own concern is concurrency/
// fencing correctness across many simultaneously-running families, never
// graph or provider complexity.
func publishSoakWorkflow(t *testing.T, api *apiClient, projectPrefix string) publishedDefinition {
	t.Helper()

	attemptPolicy := publishGolden(t, api, "", definition.KindPolicy, "soak-attempt-policy", "soak attempt policy", policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt:  &policy.AttemptRules{MaxAttempts: 3, BackoffSeconds: 1, TimeoutSeconds: 60},
	})
	permissionPolicy := publishGolden(t, api, "", definition.KindPolicy, "soak-permission-policy", "soak permission policy", policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierOperatorTrustedLocal, GrantedCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		},
	})
	attemptRefs := []definition.DependencyPin{attemptPolicy.pin(definition.KindPolicy), permissionPolicy.pin(definition.KindPolicy)}

	gateKey, gateScript := soakGateScript()
	provenance := skill.Provenance{Owner: "v8soak", Source: "fixture", Revision: "v1"}
	skillDocument := skill.SkillDocument{Resources: []skill.Resource{
		{Key: gateKey, Instruction: gateScript, Priority: definition.PriorityGuidance, Global: true, Provenance: provenance},
	}}
	scripts := publishGolden(t, api, "", definition.KindSkill, "soak-scripts", "soak scripts", skillDocument)
	identities, err := skill.ResourceIdentities(skill.SkillVersionID(scripts.versionID), skillDocument)
	if err != nil {
		t.Fatalf("skill.ResourceIdentities: %v", err)
	}
	var contentHash string
	for _, identity := range identities {
		if identity.Identity.ResourceKey == gateKey {
			contentHash = identity.Identity.ContentHash
		}
	}
	if contentHash == "" {
		t.Fatalf("no content hash for skill resource %q", gateKey)
	}

	gateCommand := publishGolden(t, api, "", definition.KindCommand, "soak-gate-command", "soak gate command", command.CommandDocument{
		Executable: command.ExecutableRef{OwnerVersionID: scripts.versionID, ResourceKey: gateKey, ContentHash: contentHash},
		Argv:       []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		// CwdRepositoryTarget is never resolved for a MACHINE_GATE's own
		// command (GateNodeExecutor always uses a fresh scratch directory
		// instead) — this value is structurally required by
		// command.ValidateDocument but genuinely never looked up at
		// execution time, so a fixed, arbitrary placeholder is correct here,
		// not a real repository id.
		CwdRepositoryTarget: "unused-by-machine-gate",
		Compatibility:       command.Compatibility{OS: []string{runtime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})
	machineGate := publishGolden(t, api, "", definition.KindGate, "soak-machine-gate", "soak machine gate", gate.GateDocument{
		CommandRef: gateCommand.pin(definition.KindCommand),
		Criteria:   []gate.Criterion{{Name: "soak-output-verified", EvidenceKey: soakGateEvidenceKey}},
	})
	completionPolicy := publishGolden(t, api, "", definition.KindPolicy, "soak-completion-policy", "soak completion policy", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{soakGateEvidenceKey}},
	})
	completionRef := completionPolicy.pin(definition.KindPolicy)

	graph := workflow.WorkflowDocument{
		SchemaVersion:       "1",
		CompletionPolicyRef: &completionRef,
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "maker", Type: workflow.NodeMachineGate, Outcomes: []string{"passed"}, MachineGate: &workflow.MachineGateNodeConfig{
				GateRef: machineGate.pin(definition.KindGate), PolicyRefs: attemptRefs,
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-maker", From: "start", Outcome: "next", To: "maker"},
			{Key: "maker-end", From: "maker", Outcome: "passed", To: "end"},
		},
	}
	return publishGolden(t, api, projectPrefix, definition.KindWorkflow, "soak-workflow", "soak workflow", graph)
}

// soakGateScript is an always-PASS gate script — this soak's own concern is
// concurrency/fencing correctness, never a real maker/gate content
// dependency, so the gate needs no marker file to check for.
func soakGateScript() (gateKey, gateScript string) {
	if runtime.GOOS == "windows" {
		return "soak-gate.bat", "@echo off\r\necho {\"" + soakGateEvidenceKey + "\":{\"verdict\":\"PASS\"}}\r\nexit /b 0\r\n"
	}
	return "soak-gate.sh", "#!/bin/sh\necho '{\"" + soakGateEvidenceKey + "\":{\"verdict\":\"PASS\"}}'\nexit 0\n"
}
