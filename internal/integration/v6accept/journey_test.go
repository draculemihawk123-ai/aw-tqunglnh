package v6accept

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
)

// journey carries the identifiers each stage hands to the next. Every value
// here was returned by the product over HTTP — nothing is pre-seeded.
type journey struct {
	s *stack

	repoPath string // the fixture Git repository the operator brings

	adapterBuildID string
	projectID      string
	repositoryID   string

	verification    verificationWorkflow
	rootWorkItemID  string
	familyID        string
	workspaceSetID  string
	childWorkItemID string
	runID           string

	// extraSnapshotPaths are read-only queries later stages add so the
	// restart comparison covers what they created.
	extraSnapshotPaths []string
}

// stage runs one named step of the journey and stops the whole journey at
// the first failure: a later stage that depends on an earlier one's output
// must never run against missing state and report a misleading second error.
func stage(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	if !t.Run(name, fn) {
		t.FailNow()
	}
}

// TestV6HTTPAcceptance_CleanDatabaseJourney is V6-14: the core product
// journey from an empty installation to a local commit and a rebuilt
// projection, driven only through the public HTTP surface of the real
// `aw serve` and `aw worker` processes.
func TestV6HTTPAcceptance_CleanDatabaseJourney(t *testing.T) {
	requireAcceptance(t)
	j := &journey{s: newStack(t)}
	j.repoPath = createGitRepository(t, filepath.Join(j.s.root, "origin-repo"))
	j.s.start(t)
	t.Cleanup(func() {
		j.s.serve.dumpOnFailure(t)
		j.s.worker.dumpOnFailure(t)
	})

	stage(t, "01_health_doctor_settings", j.healthDoctorSettings)
	stage(t, "02_adapter_probe_register", j.adapterProbeRegister)
	stage(t, "03_project_and_repository", j.projectAndRepository)
	stage(t, "04_definitions_and_workflow", func(t *testing.T) { j.verification = j.publishVerificationWorkflow(t) })
	stage(t, "05_work_item_and_run", j.workItemAndRun)
}

// healthDoctorSettings covers the installation-scoped operator surface that
// needs no project: liveness/readiness, the Doctor report, and the safe
// settings overlay's optimistic-concurrency update.
func (j *journey) healthDoctorSettings(t *testing.T) {
	api := j.s.api
	api.get(t, "/health/live").requireStatus(t, http.StatusOK)
	api.get(t, "/health/ready").requireStatus(t, http.StatusOK)

	var doctor struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	api.get(t, "/doctor").requireStatus(t, http.StatusOK).decode(t, &doctor)
	if len(doctor.Checks) == 0 {
		t.Fatal("doctor returned no checks")
	}
	for _, check := range doctor.Checks {
		if check.Status != "HEALTHY" {
			t.Errorf("doctor check %q = %s, want HEALTHY on a clean installation", check.Name, check.Status)
		}
	}

	before := api.get(t, "/settings/safe").requireStatus(t, http.StatusOK)
	var current struct {
		Desired map[string]any `json:"desired"`
		Version uint64         `json:"version"`
	}
	before.decode(t, &current)

	// The desired document is validated as a whole, so an update supplies
	// every field. The roots equal the ones this installation was started
	// with, so applying it cannot change where anything is stored; only the
	// retention and output-limit values actually differ.
	update := map[string]any{
		"managedWorkspaceRoot":   j.s.workspaceRoot,
		"managedArtifactRoot":    j.s.artifactRoot,
		"evidenceRetention":      "72h0m0s",
		"processOutputLimit":     2097152,
		"providerExecutablePath": j.s.bin.fakeClaude,
		"providerDefaultModel":   "fake-model",
		"providerCredentialRef":  "env:AW_ACCEPTANCE_KEY",
	}
	// A PUT without the current version is refused before anything changes.
	unversioned := api.put(t, "/settings/safe", update)
	if unversioned.status < 400 {
		t.Fatalf("PUT /settings/safe without If-Match succeeded (%d): %s", unversioned.status, unversioned.body)
	}
	t.Logf("PUT without If-Match -> %d %s", unversioned.status, tail(string(unversioned.body), 300))
	after := api.put(t, "/settings/safe", update, withIfMatch(before.etag())).requireStatus(t, http.StatusOK)
	var updated struct {
		Version uint64         `json:"version"`
		Desired map[string]any `json:"desired"`
	}
	after.decode(t, &updated)
	if updated.Version != current.Version+1 {
		t.Fatalf("settings version = %d, want %d", updated.Version, current.Version+1)
	}
	if updated.Desired["evidenceRetention"] != "72h0m0s" {
		t.Fatalf("desired evidenceRetention = %v, want 72h0m0s", updated.Desired["evidenceRetention"])
	}
}

// adapterProbeRegister runs the provider-upgrade flow: probe the executable
// (a replayable candidate token, no registry change), then register exactly
// what was probed.
func (j *journey) adapterProbeRegister(t *testing.T) {
	api := j.s.api
	ctx := context.Background()

	// The declared capability manifest is what the operator's own client
	// measured from the executable — the same measurement `aw adapter probe`
	// makes locally before calling the command.
	adapter, err := claude.New(processadapter.NewSupervisor(), claude.Config{Executable: j.s.bin.fakeClaude})
	if err != nil {
		t.Fatalf("claude.New: %v", err)
	}
	caps, err := adapter.Capabilities(ctx)
	if err != nil {
		t.Fatalf("measure capabilities: %v", err)
	}
	kinds := make([]string, len(caps.CanonicalEventKinds))
	for i, kind := range caps.CanonicalEventKinds {
		kinds[i] = string(kind)
	}
	manifest := map[string]any{
		"supportsStart": caps.SupportsStart, "supportsResume": caps.SupportsResume,
		"supportsCancel": caps.SupportsCancel, "canonicalEventKinds": kinds,
	}
	probeBody := map[string]any{
		"providerKey": string(caps.Provider), "executablePath": j.s.bin.fakeClaude,
		"protocolVersion": caps.ProtocolVersion, "capabilityManifest": manifest,
		"os": runtime.GOOS, "toolchain": runtime.Version(), "configIdentity": "v6-acceptance",
	}

	var registry struct {
		Builds []json.RawMessage `json:"builds"`
	}
	api.get(t, "/adapter-builds").requireStatus(t, http.StatusOK).decode(t, &registry)
	if len(registry.Builds) != 0 {
		t.Fatalf("a clean installation lists %d adapter builds, want 0", len(registry.Builds))
	}

	probeKey := "acc-probe-1"
	probe := api.post(t, "/adapter-builds/probe", probeBody, withIdempotencyKey(probeKey)).requireStatus(t, http.StatusOK, http.StatusCreated)
	// The probe response IS the signed candidate token (no wrapper).
	candidate := json.RawMessage(probe.body)

	// Probe is replayable: the same key and body returns the identical
	// candidate, and probing changed no registry state.
	replay := api.post(t, "/adapter-builds/probe", probeBody, withIdempotencyKey(probeKey)).requireStatus(t, http.StatusOK, http.StatusCreated)
	if string(replay.body) != string(probe.body) {
		t.Fatalf("probe replay differs:\nfirst:  %s\nreplay: %s", probe.body, replay.body)
	}
	api.get(t, "/adapter-builds").requireStatus(t, http.StatusOK).decode(t, &registry)
	if len(registry.Builds) != 0 {
		t.Fatalf("probe changed the registry: %d builds", len(registry.Builds))
	}

	registered := api.post(t, "/adapter-builds", map[string]any{
		"token": candidate, "capabilityManifest": manifest,
	}).requireStatus(t, http.StatusOK, http.StatusCreated)
	var result struct {
		Build struct {
			ID          string `json:"id"`
			ProviderKey string `json:"providerKey"`
		} `json:"build"`
		AlreadyExisted bool `json:"alreadyExisted"`
	}
	registered.decode(t, &result)
	if result.Build.ID == "" || result.AlreadyExisted {
		t.Fatalf("register result = %+v, want a fresh build id", result)
	}
	j.adapterBuildID = result.Build.ID

	api.get(t, "/adapter-builds").requireStatus(t, http.StatusOK).decode(t, &registry)
	if len(registry.Builds) != 1 {
		t.Fatalf("registry lists %d builds after register, want 1", len(registry.Builds))
	}
	api.get(t, "/adapter-builds/"+j.adapterBuildID).requireStatus(t, http.StatusOK)
}

// projectAndRepository creates a project and registers the operator's Git
// repository; the WORKER (not the API process) probes it and flips it to
// ACTIVE, which is the first proof the two processes cooperate through the
// shared database alone.
func (j *journey) projectAndRepository(t *testing.T) {
	api := j.s.api

	var created struct {
		ProjectID string `json:"projectId"`
		ID        string `json:"id"`
	}
	api.post(t, "/projects", map[string]string{"name": "acceptance"}).requireStatus(t, http.StatusCreated).decode(t, &created)
	j.projectID = created.ProjectID
	if j.projectID == "" {
		j.projectID = created.ID
	}
	if j.projectID == "" {
		t.Fatal("project create returned no id")
	}

	registered := api.post(t, "/projects/"+j.projectID+"/repositories", map[string]string{
		"repositoryId": "repo-a", "name": "repo-a", "remoteLocator": j.repoPath, "defaultRef": "main",
	}).requireStatus(t, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	var repository struct {
		RepositoryID string `json:"repositoryId"`
		ID           string `json:"id"`
		Status       string `json:"status"`
	}
	registered.decode(t, &repository)
	j.repositoryID = repository.RepositoryID
	if j.repositoryID == "" {
		j.repositoryID = repository.ID
	}
	if j.repositoryID == "" {
		t.Fatalf("repository register returned no id: %s", registered.body)
	}

	waitFor(t, "repository to be probed ACTIVE by the worker", 60*time.Second, 200*time.Millisecond, func() bool {
		var view struct {
			Status string `json:"status"`
		}
		api.get(t, "/repositories/"+j.repositoryID).requireStatus(t, http.StatusOK).decode(t, &view)
		return view.Status == "ACTIVE"
	})
}

// workItemAndRun creates the WorkItem family, waits for the worker to
// provision its workspace, marks the executable child ready and starts a Run
// of the published workflow.
func (j *journey) workItemAndRun(t *testing.T) {
	api := j.s.api
	// The grant is narrowed to src/ on purpose: the later scope-expansion stage
	// then has something real to expand (docs/), decided by a human.
	grant := []map[string]any{{"repositoryId": j.repositoryID, "access": "WRITE", "pathScopes": []string{"src/"}, "reason": "v6 acceptance"}}

	root := api.post(t, "/projects/"+j.projectID+"/work-items", map[string]any{
		"title": "acceptance-root", "initialScope": grant,
	}).requireStatus(t, http.StatusCreated)
	t.Logf("root work item: %s", tail(string(root.body), 800))
	var rootResult struct {
		WorkItemID     string `json:"workItemId"`
		FamilyID       string `json:"familyId"`
		WorkspaceSetID string `json:"workspaceSetId"`
	}
	root.decode(t, &rootResult)
	j.rootWorkItemID, j.familyID, j.workspaceSetID = rootResult.WorkItemID, rootResult.FamilyID, rootResult.WorkspaceSetID
	if j.rootWorkItemID == "" || j.familyID == "" || j.workspaceSetID == "" {
		t.Fatalf("root work item result is missing ids: %s", root.body)
	}

	child := api.post(t, "/projects/"+j.projectID+"/work-items/"+j.rootWorkItemID+"/children", map[string]any{
		"title": "acceptance-child", "parentJoinPolicy": "v6-acceptance-child", "effectiveScope": grant,
		// The readiness contract travels with the create command (V6-04B):
		// without it no WorkItem could ever be marked READY over HTTP.
		"contract": map[string]any{
			"schemaVersion":      1,
			"behavior":           "the acceptance repository is verified by the machine gate",
			"acceptanceCriteria": []map[string]any{{"description": "the gate reports PASS for the maker's output", "verificationRef": j.verification.workflow.definitionID}},
			"verificationSpec":   "machine gate over the maker command's output",
			"riskLevel":          "LOW",
			"exclusions":         []string{"no network access"},
			"workflowVersionId":  j.verification.workflow.versionID,
		},
	}).requireStatus(t, http.StatusCreated)
	t.Logf("child work item: %s", tail(string(child.body), 800))
	var childResult struct {
		WorkItemID string `json:"workItemId"`
	}
	child.decode(t, &childResult)
	j.childWorkItemID = childResult.WorkItemID
	if j.childWorkItemID == "" {
		t.Fatalf("child work item result has no id: %s", child.body)
	}

	j.waitWorkspaceReady(t)

	// Readiness is computed fresh by the server; mark-ready is versioned.
	readiness := api.get(t, "/projects/"+j.projectID+"/work-items/"+j.childWorkItemID+"/readiness").requireStatus(t, http.StatusOK)
	t.Logf("child readiness: %s", tail(string(readiness.body), 700))
	childView := api.get(t, "/projects/"+j.projectID+"/work-items/"+j.childWorkItemID).requireStatus(t, http.StatusOK)
	api.post(t, "/work-items/"+j.childWorkItemID+"/mark-ready", map[string]any{}, withIfMatch(childView.etag())).requireStatus(t, http.StatusOK)

	started := api.post(t, "/work-items/"+j.childWorkItemID+"/runs", map[string]any{
		"workflowVersionId": j.verification.workflow.versionID,
	}).requireStatus(t, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	t.Logf("start run: %s", tail(string(started.body), 500))
	var run struct {
		RunID string `json:"runId"`
	}
	started.decode(t, &run)
	j.runID = run.RunID
	if j.runID == "" {
		t.Fatalf("start run returned no run id: %s", started.body)
	}

	// The WORKER drives the run through all four executor roles; the test
	// only observes the durable state over HTTP.
	var last string
	waitFor(t, "run to leave RUNNING (VERIFYING/SUCCEEDED/failed)", 4*time.Minute, 500*time.Millisecond, func() bool {
		var view struct {
			State string `json:"state"`
		}
		api.get(t, "/runs/"+j.runID).requireStatus(t, http.StatusOK).decode(t, &view)
		last = view.State
		return view.State != "" && view.State != "RUNNING" && view.State != "PENDING" && view.State != "STARTING"
	})
	t.Logf("run settled in state %s", last)
}

// waitWorkspaceReady waits for the WORKER to provision the family's workspace
// set (a real `git worktree` per repository) and report it READY.
func (j *journey) waitWorkspaceReady(t *testing.T) {
	t.Helper()
	waitFor(t, "workspace set to be provisioned READY by the worker", 90*time.Second, 300*time.Millisecond, func() bool {
		var state struct {
			State string `json:"state"`
		}
		j.s.api.get(t, "/projects/"+j.projectID+"/workspace-sets/"+j.familyID).requireStatus(t, http.StatusOK).decode(t, &state)
		return state.State == "READY"
	})
}
