// stage_terminal_test.go is V6-15P — "Gate acceptance cuối V6 bằng
// terminal" (docs/design/08-v6-api-projections.md V6-15P): the operator's
// core journey/recovery driven ENTIRELY through `aw <resource> <action>`
// one-shot processes (cli_test.go's runCLI — a real, separate OS process
// per call, never an in-process function call), against the SAME
// --db/--artifact-root/--workspace-root a real `aw serve`+`aw worker` pair
// (this package's own proven stack) already serves — then, with that same
// installation still running, a fresh HTTP-driven cycle proves the product
// is not "CLI-only, HTTP now broken" (the design doc's own "re-run V6-14
// HTTP suite via same root" line).
//
// "Không làm" bar this file holds itself to: no SQLite/Git surgery (every
// mutation goes through a real `aw` invocation, nothing pokes the database
// or repository directly), no inline worker (recovery is proven by
// stack.hardKillWorker + stack.startWorker on the real spawned process,
// exactly like V6-14A's own crash scenarios), no UI dependency, and no
// missing-evidence PASS (requireAcceptance below; a real subprocess
// failure is a real t.Fatalf, never swallowed).
package v6accept

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// TestV6TerminalAcceptance_CLIJourneyThenHTTPReplay is V6-15P's own top-level
// test. It builds one fresh installation, drives the categories the design
// doc names — Doctor/settings/catalog/definition/adapter/WorkItem/Run/human
// decisions/conversation/evidence/cancel/recovery/workspace/source/
// ReleaseSet/local commit/projection/events — through real `aw` subprocess
// calls, then proves HTTP still works on that same root, then shuts both
// processes down gracefully.
func TestV6TerminalAcceptance_CLIJourneyThenHTTPReplay(t *testing.T) {
	requireAcceptance(t)
	tj := &terminalJourney{journey: &journey{s: newStack(t)}}
	tj.repoPath = createGitRepository(t, filepath.Join(tj.s.root, "origin-repo"))
	tj.originHead = runGit(t, tj.repoPath, "rev-parse", "HEAD")
	tj.s.start(t)
	t.Cleanup(func() {
		tj.s.serve.dumpOnFailure(t)
		tj.s.worker.dumpOnFailure(t)
	})
	// Every reusable definition-graph builder (publishVerificationWorkflow,
	// publishCompletionPolicy, publishReleaseWorkflow) now authors through
	// `aw definition create/validate/publish` instead of HTTP — see
	// definitions_test.go's own publish/publishOverride doc comment.
	tj.publishOverride = tj.publishDefinitionCLI

	stage(t, "01_terminal_doctor_settings", tj.terminalDoctorSettings)
	stage(t, "02_terminal_adapter", tj.terminalAdapter)
	stage(t, "03_terminal_project_repository", tj.terminalProjectRepository)
	stage(t, "04_terminal_catalog", tj.terminalCatalog)
	stage(t, "05_terminal_definitions_workflow", func(t *testing.T) { tj.verification = tj.publishVerificationWorkflow(t) })
	stage(t, "06_terminal_workitem_run", tj.terminalWorkItemAndRun)
	stage(t, "07_terminal_human_decision", tj.terminalHumanDecision)
	stage(t, "08_terminal_conversation", tj.terminalConversation)
	stage(t, "09_terminal_scope_expansion", tj.terminalScopeExpansion)
	stage(t, "10_terminal_evidence", tj.terminalEvidence)
	stage(t, "11_terminal_workspace_source", tj.terminalWorkspaceSource)
	stage(t, "12_terminal_release_set", tj.terminalReleaseSet)
	stage(t, "13_terminal_projection_events", tj.terminalProjectionAndEvents)
	stage(t, "14_terminal_cancel", tj.terminalCancel)
	stage(t, "15_terminal_recovery", tj.terminalRecovery)
	stage(t, "16_terminal_http_replay_same_root", tj.terminalHTTPReplay)
	stage(t, "17_terminal_graceful_shutdown", func(t *testing.T) {
		serveGraceful, workerGraceful := tj.s.stop(t)
		if !serveGraceful || !workerGraceful {
			t.Fatalf("shutdown was not graceful: serve=%v worker=%v", serveGraceful, workerGraceful)
		}
	})
}

// terminalJourney embeds the same journey struct every HTTP stage already
// uses (same IDs, same scopeGrant/createChild/markReady/startRun/
// waitRunSettled/waitWorkspaceReady helpers — those are pure state/HTTP
// helpers this file reuses unchanged where a step is legitimately still
// HTTP, e.g. workItemAndRun's own waitRunSettled polls GetRunDetail the same
// way whether the RUN was started by CLI or HTTP), plus the extra state this
// terminal journey alone needs.
type terminalJourney struct {
	*journey
	verification verificationWorkflow

	humanDecisionRunID string
	cancelRunID        string

	// commitWorkspace/commitReleaseSetVersion are the EXACT flag values
	// stage 12's own `aw release-set local-commit` call used — terminalRecovery
	// (stage 15) replays with these, never with freshly re-read current
	// state, because BuildEnvelope's own semantic-hash contract requires a
	// replay's flags to be byte-identical to the original call's; re-reading
	// --expected-release-set-version after stage 12's own seal would read a
	// LATER version than the original commit request used.
	commitWorkspace         repositoryWorkspace
	commitReleaseSetVersion uint64
}

// terminalDoctorSettings mirrors journey.healthDoctorSettings's own
// coverage, but through `aw doctor`/`aw settings show`/`aw settings
// update` instead of HTTP.
func (tj *terminalJourney) terminalDoctorSettings(t *testing.T) {
	var doctor struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	runCLI(t, tj.s, nil, "doctor", "--json").requireOK(t).decodeQuery(t, &doctor)
	if len(doctor.Checks) == 0 {
		t.Fatal("aw doctor returned no checks")
	}
	for _, check := range doctor.Checks {
		if check.Status != "HEALTHY" {
			t.Errorf("doctor check %q = %s, want HEALTHY on a clean installation", check.Name, check.Status)
		}
	}

	var current struct {
		Desired map[string]any `json:"desired"`
		Version uint64         `json:"version"`
	}
	runCLI(t, tj.s, nil, "settings", "show", "--json").requireOK(t).decodeQuery(t, &current)

	update, err := json.Marshal(map[string]any{
		"managedWorkspaceRoot":   tj.s.workspaceRoot,
		"managedArtifactRoot":    tj.s.artifactRoot,
		"evidenceRetention":      "72h0m0s",
		"processOutputLimit":     2097152,
		"providerExecutablePath": tj.s.bin.fakeClaude,
		"providerDefaultModel":   "fake-model",
		"providerCredentialRef":  "env:AW_ACCEPTANCE_KEY",
	})
	if err != nil {
		t.Fatalf("encode settings update body: %v", err)
	}

	// An update without --expected-version is refused before anything
	// changes — the CLI equivalent of the HTTP journey's own "PUT without
	// If-Match" check. Exit 2 (exitUsage, cmd/aw/cli.go): a missing
	// required flag is a usage error, not a runtime failure.
	runCLI(t, tj.s, update, "settings", "update", "--json").requireExit(t, 2)

	updated := runCLI(t, tj.s, update, "settings", "update", "--json",
		"--expected-version", strconv.FormatUint(current.Version, 10)).requireOK(t)
	var afterUpdate struct {
		Version uint64         `json:"version"`
		Desired map[string]any `json:"desired"`
	}
	updated.decodeEnvelope(t, &afterUpdate)
	if afterUpdate.Version != current.Version+1 {
		t.Fatalf("settings version after aw settings update = %d, want %d", afterUpdate.Version, current.Version+1)
	}
	if afterUpdate.Desired["evidenceRetention"] != "72h0m0s" {
		t.Fatalf("desired evidenceRetention = %v, want 72h0m0s", afterUpdate.Desired["evidenceRetention"])
	}
}

// terminalAdapter mirrors journey.adapterProbeRegister via `aw adapter
// probe`/`aw adapter register` — the same fake-claude executable, the same
// measured capabilities, driven as two real subprocess calls instead of two
// HTTP POSTs.
func (tj *terminalJourney) terminalAdapter(t *testing.T) {
	adapter, err := claude.New(processadapter.NewSupervisor(), claude.Config{Executable: tj.s.bin.fakeClaude})
	if err != nil {
		t.Fatalf("claude.New: %v", err)
	}
	caps, err := adapter.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("measure capabilities: %v", err)
	}
	kinds := make([]string, len(caps.CanonicalEventKinds))
	for i, kind := range caps.CanonicalEventKinds {
		kinds[i] = string(kind)
	}

	probeArgs := []string{
		"adapter", "probe", "--json",
		"--provider-key", string(caps.Provider), "--executable-path", tj.s.bin.fakeClaude,
		"--protocol-version", caps.ProtocolVersion, "--config-identity", "v6-terminal",
		"--os", runtime.GOOS, "--toolchain", runtime.Version(),
		"--canonical-event-kinds", strings.Join(kinds, ","),
		"--supports-start=" + strconv.FormatBool(caps.SupportsStart),
		"--supports-resume=" + strconv.FormatBool(caps.SupportsResume),
		"--supports-cancel=" + strconv.FormatBool(caps.SupportsCancel),
	}
	probe := runCLI(t, tj.s, nil, probeArgs...).requireOK(t)
	// Unlike the HTTP journey's own probe response (a bare token, no
	// wrapper — journey_test.go's own adapterProbeRegister), `aw adapter
	// probe` DOES wrap its result in the standard ResultEnvelope (it takes
	// --idempotency-key too), so the signed candidate token itself is
	// envelope.Result — that is what `adapter register --file` expects,
	// never the whole envelope.
	var candidate json.RawMessage
	probe.decodeEnvelope(t, &candidate)
	candidateFile := writeTempFile(t, tj.s.root, "cli-adapter-candidate.json", candidate)

	registerArgs := []string{"adapter", "register", "--json", "--file", candidateFile,
		"--supports-start=" + strconv.FormatBool(caps.SupportsStart),
		"--supports-resume=" + strconv.FormatBool(caps.SupportsResume),
		"--supports-cancel=" + strconv.FormatBool(caps.SupportsCancel),
		"--canonical-event-kinds", strings.Join(kinds, ","),
		"--yes", // HighImpact (UX Screen 1 row 4), non-interactive.
	}
	registered := runCLI(t, tj.s, nil, registerArgs...).requireOK(t)
	var result struct {
		Build struct {
			ID string `json:"id"`
		} `json:"build"`
		AlreadyExisted bool `json:"alreadyExisted"`
	}
	registered.decodeEnvelope(t, &result)
	if result.Build.ID == "" || result.AlreadyExisted {
		t.Fatalf("aw adapter register result = %+v, want a fresh build id", result)
	}
	tj.adapterBuildID = result.Build.ID
}

// terminalProjectRepository mirrors journey.projectAndRepository via `aw
// project create`/`aw repository register`, polling `aw repository list`
// (there is no `repository show`) for the WORKER's own ACTIVE probe — same
// "two processes cooperate through the shared database alone" proof, now
// with both sides of that cooperation reached over the CLI's own one-shot
// path instead of HTTP.
func (tj *terminalJourney) terminalProjectRepository(t *testing.T) {
	createBody, err := json.Marshal(map[string]string{"name": "acceptance-terminal"})
	if err != nil {
		t.Fatalf("encode project create body: %v", err)
	}
	created := runCLI(t, tj.s, createBody, "project", "create").requireOK(t)
	var project struct {
		ProjectID string `json:"projectId"`
	}
	created.decodeEnvelope(t, &project)
	if project.ProjectID == "" {
		t.Fatal("aw project create returned no projectId")
	}
	tj.projectID = project.ProjectID

	registerBody, err := json.Marshal(map[string]string{
		"repositoryId": "repo-a", "name": "repo-a", "remoteLocator": tj.repoPath, "defaultRef": "main",
	})
	if err != nil {
		t.Fatalf("encode repository register body: %v", err)
	}
	registered := runCLI(t, tj.s, registerBody, "repository", "register", "--project-id", tj.projectID).requireOK(t)
	var repository struct {
		RepositoryID string `json:"repositoryId"`
	}
	registered.decodeEnvelope(t, &repository)
	if repository.RepositoryID == "" {
		t.Fatalf("aw repository register returned no repositoryId: %s", registered.stdout)
	}
	tj.repositoryID = repository.RepositoryID

	waitFor(t, "repository to be probed ACTIVE by the worker (observed via `aw repository list`)", 60*time.Second, 200*time.Millisecond, func() bool {
		var list struct {
			Repositories []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"repositories"`
		}
		runCLI(t, tj.s, nil, "repository", "list", tj.projectID).requireOK(t).decodeQuery(t, &list)
		for _, r := range list.Repositories {
			if r.ID == tj.repositoryID {
				return r.Status == "ACTIVE"
			}
		}
		return false
	})
}

// terminalCatalog proves `aw component list` — the read-only catalog
// surface component.go's own doc comment describes: every row it can ever
// return was inserted by the repository onboarding/probe worker (this
// leaf creates none itself, matching that file's "Không làm: no generic
// probe/component creation" line). By this stage the WORKER has already
// probed repo-a to ACTIVE (stage 03), so the real assertion is that the
// worker's own discovery already populated this project's catalog — not
// that the catalog is empty.
func (tj *terminalJourney) terminalCatalog(t *testing.T) {
	var components struct {
		Components []json.RawMessage `json:"components"`
	}
	runCLI(t, tj.s, nil, "component", "list", tj.projectID).requireOK(t).decodeQuery(t, &components)
	if len(components.Components) == 0 {
		t.Fatal("aw component list returned 0 components after the worker probed a real repository, want at least the ones onboarding discovered")
	}
}

// terminalWorkItemAndRun mirrors journey.workItemAndRun: create the root+
// child WorkItem, wait for the worker to provision the workspace, mark the
// child ready and start+wait its Run — all through `aw work-item
// create/create-child/mark-ready/readiness/show` and `aw run start --wait`.
func (tj *terminalJourney) terminalWorkItemAndRun(t *testing.T) {
	rootBody, err := json.Marshal(map[string]any{"title": "terminal-root", "initialScope": tj.scopeGrant()})
	if err != nil {
		t.Fatalf("encode work-item create body: %v", err)
	}
	created := runCLI(t, tj.s, rootBody, "work-item", "create", "--project-id", tj.projectID).requireOK(t)
	var root struct {
		WorkItemID     string `json:"workItemId"`
		FamilyID       string `json:"familyId"`
		WorkspaceSetID string `json:"workspaceSetId"`
	}
	created.decodeEnvelope(t, &root)
	if root.WorkItemID == "" || root.FamilyID == "" || root.WorkspaceSetID == "" {
		t.Fatalf("aw work-item create result is missing ids: %s", created.stdout)
	}
	tj.rootWorkItemID, tj.familyID, tj.workspaceSetID = root.WorkItemID, root.FamilyID, root.WorkspaceSetID

	tj.childWorkItemID = tj.createChildCLI(t, tj.rootWorkItemID, "terminal-child",
		"the acceptance repository is verified by the machine gate", tj.verification.workflow)
	tj.waitWorkspaceReady(t) // pure HTTP read helper, reused unchanged (see this file's own doc comment)

	tj.markReadyCLI(t, tj.childWorkItemID)

	tj.runID = tj.startRunCLI(t, tj.childWorkItemID, tj.verification.workflow.versionID, true)
	if state := tj.runStateCLI(t, tj.runID); state != "SUCCEEDED" {
		t.Fatalf("aw run start --wait: verification run settled in %s, want SUCCEEDED", state)
	}
}

// createChildCLI is createChild's CLI twin: identical body shape (the exact
// map this package's own HTTP journey already proves the server accepts),
// `aw work-item create-child` instead of an HTTP POST.
func (tj *terminalJourney) createChildCLI(t *testing.T, parentWorkItemID, title, behavior string, wf publishedDefinition) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"title": title, "parentJoinPolicy": "v6-terminal-child", "effectiveScope": tj.scopeGrant(),
		"contract": map[string]any{
			"schemaVersion":      1,
			"behavior":           behavior,
			"acceptanceCriteria": []map[string]any{{"description": "the workflow's own verification passes", "verificationRef": wf.definitionID}},
			"verificationSpec":   "see the referenced workflow",
			"riskLevel":          "LOW",
			"exclusions":         []string{"no network access"},
			"workflowVersionId":  wf.versionID,
		},
	})
	if err != nil {
		t.Fatalf("encode create-child body: %v", err)
	}
	created := runCLI(t, tj.s, body, "work-item", "create-child", parentWorkItemID).requireOK(t)
	var result struct {
		WorkItemID string `json:"workItemId"`
	}
	created.decodeEnvelope(t, &result)
	if result.WorkItemID == "" {
		t.Fatalf("aw work-item create-child result has no id: %s", created.stdout)
	}
	return result.WorkItemID
}

// markReadyCLI checks readiness (freshly computed, `aw work-item
// readiness`) then applies the versioned mark-ready command (`aw work-item
// mark-ready`), reading the WorkItem's own current version first via `aw
// work-item show` — the CLI's --expected-version is HTTP's If-Match.
func (tj *terminalJourney) markReadyCLI(t *testing.T, workItemID string) {
	t.Helper()
	var readiness struct {
		Ready    bool     `json:"ready"`
		Problems []string `json:"problems"`
	}
	runCLI(t, tj.s, nil, "work-item", "readiness", "--project-id", tj.projectID, workItemID).requireOK(t).decodeQuery(t, &readiness)
	if !readiness.Ready {
		t.Fatalf("aw work-item readiness %s: not ready: %v", workItemID, readiness.Problems)
	}

	var view struct {
		Version uint64 `json:"version"`
	}
	runCLI(t, tj.s, nil, "work-item", "show", "--project-id", tj.projectID, workItemID).requireOK(t).decodeQuery(t, &view)

	runCLI(t, tj.s, nil, "work-item", "mark-ready",
		"--expected-version", strconv.FormatUint(view.Version, 10), workItemID).requireOK(t)
}

// startRunCLI starts `aw run start <workItemId> --workflow-version-id
// <id> [--wait]`.
func (tj *terminalJourney) startRunCLI(t *testing.T, workItemID, workflowVersionID string, wait bool) string {
	t.Helper()
	args := []string{"run", "start", "--json", "--workflow-version-id", workflowVersionID}
	if wait {
		args = append(args, "--wait", "--wait-timeout", "4m")
	}
	args = append(args, workItemID)
	started := runCLI(t, tj.s, nil, args...).requireOK(t)
	var result struct {
		RunID string `json:"runId"`
	}
	started.decodeEnvelope(t, &result)
	if result.RunID == "" {
		t.Fatalf("aw run start returned no runId: %s", started.stdout)
	}
	return result.RunID
}

// runStateCLI reads a Run's current state via `aw run show`.
func (tj *terminalJourney) runStateCLI(t *testing.T, runID string) string {
	t.Helper()
	var detail struct {
		State string `json:"state"`
	}
	runCLI(t, tj.s, nil, "run", "show", runID).requireOK(t).decodeQuery(t, &detail)
	return detail.State
}

// waitRunTerminalCLI polls `aw run show` until the Run reaches a terminal
// state — the CLI-only equivalent of journey.waitRunSettled, for the
// scenarios below that do NOT use `run start --wait` (cancel/recovery,
// where this test itself controls the timing of the observation).
func (tj *terminalJourney) waitRunTerminalCLI(t *testing.T, runID string, within time.Duration) string {
	t.Helper()
	var last string
	waitFor(t, "run "+runID+" to reach a terminal state (observed via `aw run show`)", within, 500*time.Millisecond, func() bool {
		last = tj.runStateCLI(t, runID)
		switch last {
		case "SUCCEEDED", "FAILED", "CANCELLED", "BLOCKED":
			return true
		}
		return false
	})
	return last
}

// terminalHumanDecision is V6-15P's own "human decisions" coverage: publish
// a START -> APPROVAL(gate) -> END workflow (approvalOnlyWorkflowDocument,
// stage_fault_security_test.go — reused verbatim through publishOverride,
// same as every other definition here), start a Run of it, observe the
// real pending ApprovalRequest via `aw run show` (V6-06E's own "expose
// approval requests ... in run detail"), and resolve it via `aw approval
// resolve` — the one write no earlier stage in this package (HTTP or CLI)
// has ever exercised end to end.
func (tj *terminalJourney) terminalHumanDecision(t *testing.T) {
	const authorizedRole = "operator" // the default local-operator principal's own role.
	wf := tj.publish(t, "/projects/"+tj.projectID, definition.KindWorkflow, "terminal-approval-workflow", "terminal approval workflow",
		approvalOnlyWorkflowDocument(authorizedRole, 3600))
	childID := tj.createChildCLI(t, tj.rootWorkItemID, "terminal-human-decision-child", "v6-15p human decision fixture", wf)
	tj.markReadyCLI(t, childID)
	runID := tj.startRunCLI(t, childID, wf.versionID, false)
	tj.humanDecisionRunID = runID

	type approvalView struct {
		ApprovalRequestID string `json:"approvalRequestId"`
		State             string `json:"state"`
		Version           uint64 `json:"version"`
	}
	var approval approvalView
	waitFor(t, "the run to reach a real, pending APPROVAL node (observed via `aw run show`)", 30*time.Second, 200*time.Millisecond, func() bool {
		var detail struct {
			ApprovalRequests []approvalView `json:"approvalRequests"`
		}
		runCLI(t, tj.s, nil, "run", "show", runID).requireOK(t).decodeQuery(t, &detail)
		for _, a := range detail.ApprovalRequests {
			if a.State == "PENDING" || a.State == "ESCALATED" {
				approval = a
				return true
			}
		}
		return false
	})

	resolved := runCLI(t, tj.s, nil, "approval", "resolve",
		"--outcome", "approved", "--reason", "v6-15p terminal acceptance: resolved via aw approval resolve",
		"--expected-version", strconv.FormatUint(approval.Version, 10),
		runID, approval.ApprovalRequestID).requireOK(t)
	var decision struct {
		ApprovalRequestID string `json:"approvalRequestId"`
	}
	resolved.decodeEnvelope(t, &decision)
	if decision.ApprovalRequestID != approval.ApprovalRequestID {
		t.Fatalf("aw approval resolve result carries approvalRequestId %q, want %q", decision.ApprovalRequestID, approval.ApprovalRequestID)
	}

	// This fixture (approvalOnlyWorkflowDocument, reused verbatim from
	// V6-14A's own role-downgrade scenario) carries no CompletionPolicyRef,
	// so it deliberately does NOT assert the run's own aggregate State
	// here — that state depends on completion-orchestrator semantics this
	// bare fixture was never built to satisfy, and asserting SUCCEEDED
	// would be pinning behavior this stage has no real claim on. The real
	// proof a human decision took effect is direct: the SAME
	// ApprovalRequest this stage observed PENDING is now DECIDED, and the
	// run is no longer stuck waiting on it (any terminal state proves the
	// graph advanced past the APPROVAL node).
	tj.waitRunTerminalCLI(t, runID, 30*time.Second)
	var afterResolve struct {
		ApprovalRequests []approvalView `json:"approvalRequests"`
	}
	runCLI(t, tj.s, nil, "run", "show", runID).requireOK(t).decodeQuery(t, &afterResolve)
	found := false
	for _, a := range afterResolve.ApprovalRequests {
		if a.ApprovalRequestID == approval.ApprovalRequestID {
			found = true
			if a.State != "DECIDED" {
				t.Fatalf("approval request %s state after aw approval resolve = %s, want DECIDED", a.ApprovalRequestID, a.State)
			}
		}
	}
	if !found {
		t.Fatalf("run detail after resolve no longer lists approval request %s", approval.ApprovalRequestID)
	}
}

// terminalConversation covers "conversation": append a message on the root
// WorkItem via `aw message append` (its content piped over stdin — the CLI's
// own bounded-input body, appmessage.AppendMessageResult's own doc comment:
// unlike a create leaf's result, this carries only the new MessageID, never
// the content itself, which is stored content-addressed via an Artifact, not
// inlined on the message row), then reads it back with `aw message list` —
// the real proof the append reached the store, not just that the command
// exited 0.
func (tj *terminalJourney) terminalConversation(t *testing.T) {
	const content = "v6-15p terminal acceptance: a real operator note"
	appended := runCLI(t, tj.s, []byte(content), "message", "append",
		"--project-id", tj.projectID, "--role", "USER", "--content-type", "text/plain", tj.rootWorkItemID).requireOK(t)
	var message struct {
		MessageID string `json:"messageId"`
	}
	appended.decodeEnvelope(t, &message)
	if message.MessageID == "" {
		t.Fatalf("aw message append returned no messageId: %s", appended.stdout)
	}

	var list struct {
		Items []struct {
			MessageID string `json:"messageId"`
			Role      string `json:"role"`
		} `json:"items"`
	}
	runCLI(t, tj.s, nil, "message", "list", "--project-id", tj.projectID, tj.rootWorkItemID).requireOK(t).decodeQuery(t, &list)
	found := false
	for _, m := range list.Items {
		if m.MessageID == message.MessageID {
			found = true
			if m.Role != "USER" {
				t.Errorf("aw message list: message %s role = %q, want USER", m.MessageID, m.Role)
			}
		}
	}
	if !found {
		t.Fatalf("aw message list does not list the just-appended message %s", message.MessageID)
	}
}

// terminalScopeExpansion covers "scope-expansion": request an expansion
// beyond the root WorkItem's src/-only grant, then approve it — `aw
// scope-expansion request`/`aw scope-expansion approve`.
func (tj *terminalJourney) terminalScopeExpansion(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"requestedGrants": []map[string]any{{"repositoryId": tj.repositoryID, "access": "READ", "pathScopes": []string{"docs/"}, "reason": "v6-15p terminal acceptance"}},
		"reason":          "the operator wants read access to docs/ as well",
	})
	if err != nil {
		t.Fatalf("encode scope-expansion request body: %v", err)
	}
	requested := runCLI(t, tj.s, body, "scope-expansion", "request", "--project-id", tj.projectID, tj.familyID).requireOK(t)
	var request struct {
		RequestID string `json:"requestId"`
		Status    string `json:"status"`
	}
	requested.decodeEnvelope(t, &request)
	if request.RequestID == "" {
		t.Fatalf("aw scope-expansion request returned no requestId: %s", requested.stdout)
	}
	if request.Status != "PENDING" {
		t.Fatalf("aw scope-expansion request: status = %q, want PENDING", request.Status)
	}

	// No `scope-expansion show`/`list` leaf exists to read the request's
	// current version back (this package's own file listing has only
	// request/approve/reject/withdraw) — 1 is not a guess: every
	// ScopeExpansionRequest is created at Version 1
	// (internal/domain/work/scope_expansion.go's own NewScopeExpansionRequest),
	// and this is the only approve ever issued against this particular
	// fresh request.
	approved := runCLI(t, tj.s, nil, "scope-expansion", "approve",
		"--project-id", tj.projectID, "--expected-version", "1",
		request.RequestID).requireOK(t)
	var decision struct {
		RequestID       string `json:"requestId"`
		NewScopeVersion uint64 `json:"newScopeVersion"`
		ApprovedGrants  []struct {
			RepositoryID string `json:"repositoryId"`
		} `json:"approvedGrants"`
	}
	approved.decodeEnvelope(t, &decision)
	if decision.RequestID != request.RequestID {
		t.Fatalf("aw scope-expansion approve result requestId = %q, want %q", decision.RequestID, request.RequestID)
	}
	if len(decision.ApprovedGrants) != 1 || decision.ApprovedGrants[0].RepositoryID != tj.repositoryID {
		t.Fatalf("aw scope-expansion approve: approvedGrants = %+v, want exactly one grant for %s", decision.ApprovedGrants, tj.repositoryID)
	}
}

// terminalEvidence covers "evidence": the verification Run's own COMMAND
// and MACHINE_GATE nodes each produce a real Evidence row — `aw evidence
// list` on the child WorkItem must report both.
func (tj *terminalJourney) terminalEvidence(t *testing.T) {
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	runCLI(t, tj.s, nil, "evidence", "list", "--project-id", tj.projectID, tj.childWorkItemID).requireOK(t).decodeQuery(t, &list)
	if len(list.Items) == 0 {
		t.Fatal("aw evidence list on the verified child WorkItem returned 0 items, want at least the COMMAND/MACHINE_GATE evidence")
	}
}

// terminalWorkspaceSource covers "workspace/source": `aw workspace-set
// show` for the family's own provisioned repository workspace, then `aw
// repository-workspace source` to read a real file back out of the managed
// Git worktree at its current revision.
func (tj *terminalJourney) terminalWorkspaceSource(t *testing.T) {
	var set struct {
		Workspaces []repositoryWorkspace `json:"repositoryWorkspaces"`
	}
	runCLI(t, tj.s, nil, "workspace-set", "show", "--project-id", tj.projectID, tj.familyID).requireOK(t).decodeQuery(t, &set)
	if len(set.Workspaces) != 1 {
		t.Fatalf("aw workspace-set show: %d repository workspaces, want 1", len(set.Workspaces))
	}
	ws := set.Workspaces[0]

	outputPath := filepath.Join(tj.s.root, "cli-source-read.txt")
	runCLI(t, tj.s, nil, "repository-workspace", "source",
		// --workspace-set-id is the workspace SET's own id (tj.workspaceSetID,
		// captured back in terminalWorkItemAndRun's `work-item create`
		// result) — NOT tj.familyID, the id `workspace-set show`'s own
		// POSITIONAL argument happens to be keyed by. The two are
		// different ids for the same family, confirmed by
		// workspaceSetStateResult carrying both a `workspaceSetId` and a
		// `familyId` field of its own.
		"--project-id", tj.projectID, "--repository-id", tj.repositoryID, "--workspace-set-id", tj.workspaceSetID,
		"--revision", ws.CurrentRevision, "--revision-generation", strconv.FormatUint(ws.Generation, 10),
		"--path", "README.md", "--output", outputPath, ws.ID).requireOK(t)
	content := readFile(t, outputPath)
	if !strings.Contains(content, "acceptance fixture") {
		t.Fatalf("aw repository-workspace source README.md = %q, want it to contain the fixture repository's own marker text", content)
	}
}

// terminalReleaseSet covers "ReleaseSet/local commit": publish a real
// release workflow (publishReleaseWorkflow, stage_release_test.go — reused
// verbatim through publishOverride), run it via `aw run start --wait` so
// the release note is really written into the managed worktree, then `aw
// release-set create` / `aw release-set local-commit --wait` / `aw
// release-set seal`.
//
// The local commit's own idempotency key is fixed (not generated) and
// remembered on tj — terminalRecovery below REPLAYS this exact call after
// a worker restart. There is no separate "get one local commit" CLI query
// (unlike a Run, whose `run show` is always available); `--wait`'s own
// Result.Wait is the only place a caller ever observes a local commit's
// terminal state, whether this is the original request or a later replay.
const terminalLocalCommitKey = "v6-15p-terminal-local-commit-1"

func (tj *terminalJourney) terminalReleaseSet(t *testing.T) {
	release := tj.publishReleaseWorkflow(t)
	releaseChildID := tj.createChildCLI(t, tj.rootWorkItemID, "terminal-release-child", "the release note is written into src/", release.workflow)
	tj.markReadyCLI(t, releaseChildID)
	releaseRunID := tj.startRunCLI(t, releaseChildID, release.workflow.versionID, true)
	if state := tj.runStateCLI(t, releaseRunID); state != "SUCCEEDED" {
		t.Fatalf("aw run start --wait: release run settled in %s, want SUCCEEDED", state)
	}

	ws := tj.workspaceCLI(t)

	createBody, err := json.Marshal(map[string]any{
		"repositories": []map[string]any{{
			"repositoryId": tj.repositoryID, "baseVcsObjectId": ws.CurrentRevision, "resultVcsObjectId": ws.CurrentRevision, "verdict": "PASS",
		}},
	})
	if err != nil {
		t.Fatalf("encode release-set create body: %v", err)
	}
	created := runCLI(t, tj.s, createBody, "release-set", "create", "--project-id", tj.projectID, "--family-id", tj.familyID).requireOK(t)
	var releaseSet struct {
		ReleaseSetID string `json:"releaseSetId"`
		Version      uint64 `json:"version"`
	}
	created.decodeEnvelope(t, &releaseSet)
	if releaseSet.ReleaseSetID == "" {
		t.Fatalf("aw release-set create returned no id: %s", created.stdout)
	}
	tj.releaseSetID = releaseSet.ReleaseSetID

	tj.commitWorkspace, tj.commitReleaseSetVersion = ws, releaseSet.Version
	commit := tj.requestLocalCommitCLI(t, ws, releaseSet.Version)
	if commit.State != "COMMITTED" {
		t.Fatalf("aw release-set local-commit --wait: state = %q, want COMMITTED", commit.State)
	}
	if commit.ResultVCSObjectID == "" || commit.ResultVCSObjectID == ws.CurrentRevision {
		t.Fatalf("local commit result revision = %q, want a new commit distinct from the pre-commit revision %q", commit.ResultVCSObjectID, ws.CurrentRevision)
	}
	tj.localCommitID = commit.ReleaseSetLocalCommitID
	tj.resultRevision = commit.ResultVCSObjectID

	var beforeSeal struct{ Version uint64 }
	runCLI(t, tj.s, nil, "release-set", "show", tj.releaseSetID).requireOK(t).decodeQuery(t, &beforeSeal)
	sealed := runCLI(t, tj.s, nil, "release-set", "seal",
		"--expected-version", strconv.FormatUint(beforeSeal.Version, 10), "--yes", tj.releaseSetID).requireOK(t)
	var afterSeal struct {
		State string `json:"state"`
	}
	sealed.decodeEnvelope(t, &afterSeal)
	if afterSeal.State != "SEALED" {
		t.Fatalf("aw release-set seal: state = %q, want SEALED", afterSeal.State)
	}
}

// workspaceCLI reads the family's one repository workspace via `aw
// workspace-set show`.
func (tj *terminalJourney) workspaceCLI(t *testing.T) repositoryWorkspace {
	t.Helper()
	var set struct {
		Workspaces []repositoryWorkspace `json:"repositoryWorkspaces"`
	}
	runCLI(t, tj.s, nil, "workspace-set", "show", "--project-id", tj.projectID, tj.familyID).requireOK(t).decodeQuery(t, &set)
	if len(set.Workspaces) != 1 {
		t.Fatalf("aw workspace-set show: %d repository workspaces, want 1", len(set.Workspaces))
	}
	return set.Workspaces[0]
}

// requestLocalCommitCLI runs `aw release-set local-commit --wait` with
// terminalLocalCommitKey as its idempotency key and returns the terminal
// ReleaseSetLocalCommitStatus --wait observed. Called a second time (same
// key, same flags) by terminalRecovery below, this is a genuine receipt
// replay, not a fresh request — BuildEnvelope's own semantic-hash contract
// (definitions_test.go's own doc comments on it) requires every flag that
// feeds the hash to be byte-identical across both calls, which is why this
// helper exists rather than inlining the call twice with a chance to drift.
func (tj *terminalJourney) requestLocalCommitCLI(t *testing.T, ws repositoryWorkspace, releaseSetVersion uint64) releaseSetLocalCommitStatus {
	t.Helper()
	requested := runCLI(t, tj.s, nil, "release-set", "local-commit",
		"--project-id", tj.projectID, "--release-set-id", tj.releaseSetID,
		"--repository-workspace-id", ws.ID, "--expected-release-set-version", strconv.FormatUint(releaseSetVersion, 10),
		"--expected-workspace-version", strconv.FormatUint(ws.Version, 10),
		"--message", "v6-15p terminal acceptance: add the release note",
		"--author-name", "Terminal Acceptance", "--author-email", "terminal@example.invalid",
		"--idempotency-key", terminalLocalCommitKey,
		"--wait", "--wait-timeout", "60s", "--yes").requireOK(t) // HighImpact (UX Screen 10).
	var result struct {
		ReleaseSetLocalCommitID string                       `json:"releaseSetLocalCommitId"`
		Wait                    *releaseSetLocalCommitStatus `json:"wait"`
	}
	requested.decodeEnvelope(t, &result)
	if result.ReleaseSetLocalCommitID == "" {
		t.Fatalf("aw release-set local-commit returned no id: %s", requested.stdout)
	}
	if result.Wait == nil {
		t.Fatalf("aw release-set local-commit --wait returned no terminal observation within its own timeout: %s", requested.stdout)
	}
	return *result.Wait
}

// releaseSetLocalCommitStatus mirrors workapp.ReleaseSetLocalCommitStatus's
// own wire fields (release_set_queries.go) — this package never imports
// internal/app/work directly (client_test.go's "public surface only"
// discipline), so it declares its own copy here, the same convention every
// other *View/*Status type in this package already follows.
type releaseSetLocalCommitStatus struct {
	ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
	State                   string `json:"state"`
	ParentVCSObjectID       string `json:"parentVcsObjectId"`
	ResultVCSObjectID       string `json:"resultVcsObjectId"`
}

// terminalProjectionAndEvents covers "projection/events": `aw projection
// rebuild`/`aw projection status` on the WorkItem read model, then `aw
// events watch` — a bounded read of the real NDJSON stream, proving it
// carries real domain events from everything the stages above just did.
func (tj *terminalJourney) terminalProjectionAndEvents(t *testing.T) {
	rebuilt := runCLI(t, tj.s, nil, "projection", "rebuild", "--project-id", tj.projectID, "--projection-name", "workitem").requireOK(t)
	var operation struct {
		OperationID string `json:"operationId"`
	}
	rebuilt.decodeEnvelope(t, &operation)
	if operation.OperationID == "" {
		t.Fatalf("aw projection rebuild returned no operationId: %s", rebuilt.stdout)
	}

	waitFor(t, "projection rebuild operation to reach SUCCEEDED (observed via `aw projection rebuild-status`)", 30*time.Second, 300*time.Millisecond, func() bool {
		var status struct {
			Phase string `json:"phase"`
		}
		runCLI(t, tj.s, nil, "projection", "rebuild-status", operation.OperationID).requireOK(t).decodeQuery(t, &status)
		if status.Phase == "FAILED" {
			t.Fatalf("projection rebuild operation %s reached FAILED", operation.OperationID)
		}
		return status.Phase == "SUCCEEDED"
	})

	// `aw projection status` is the OTHER read: the projection's current
	// freshness, independent of any one operation's own id.
	var freshness struct {
		Status string `json:"status"`
	}
	runCLI(t, tj.s, nil, "projection", "status", "--project-id", tj.projectID, "--projection-name", "workitem").requireOK(t).decodeQuery(t, &freshness)
	if freshness.Status != "LIVE" {
		t.Fatalf("aw projection status after a successful rebuild: status = %q, want LIVE", freshness.Status)
	}

	watch := runCLIStreaming(t, tj.s, 5*time.Second, "events", "watch", "--project-id", tj.projectID, "--from-cursor", "0")
	lines := strings.Split(strings.TrimSpace(watch), "\n")
	found := map[string]bool{}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			EventType string `json:"eventType"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("aw events watch: undecodable NDJSON line: %v\nline: %s", err, line)
		}
		found[event.EventType] = true
	}
	for _, want := range []string{"ProjectCreated", "RepositoryRegistered", "RootWorkItemCreated"} {
		if !found[want] {
			t.Errorf("aw events watch never reported %s among %d lines", want, len(lines))
		}
	}
}

// terminalCancel covers "cancel": start a fresh Run of a WAIT-only workflow
// (waitOnlyWorkflowDocument, stage_fault_cancel_test.go — a Run that would
// otherwise sit WAITING forever, so cancelling it is a real state change,
// not a race against the Run finishing on its own) and cancel it via `aw
// run cancel`.
func (tj *terminalJourney) terminalCancel(t *testing.T) {
	wf := tj.publish(t, "/projects/"+tj.projectID, definition.KindWorkflow, "terminal-cancel-workflow", "terminal cancel workflow",
		waitSignalOnlyWorkflowDocument(3600))
	childID := tj.createChildCLI(t, tj.rootWorkItemID, "terminal-cancel-child", "v6-15p cancel fixture", wf)
	tj.markReadyCLI(t, childID)
	runID := tj.startRunCLI(t, childID, wf.versionID, false)
	tj.cancelRunID = runID

	// The Run's own top-level State stays RUNNING while its WAIT node
	// blocks on a signal nothing ever sends — only the WAIT node's own
	// WaitRegistration (also exposed on run detail, V6-06E) goes ACTIVE.
	waitFor(t, "the run's WAIT node to reach a real, active WaitRegistration (observed via `aw run show`)", 15*time.Second, 200*time.Millisecond, func() bool {
		var detail struct {
			WaitRegistrations []struct {
				State string `json:"state"`
			} `json:"waitRegistrations"`
		}
		runCLI(t, tj.s, nil, "run", "show", runID).requireOK(t).decodeQuery(t, &detail)
		for _, w := range detail.WaitRegistrations {
			if w.State == "ACTIVE" {
				return true
			}
		}
		return false
	})

	runCLI(t, tj.s, nil, "run", "cancel", "--reason", "v6-15p terminal acceptance: operator-requested cancel", "--yes", runID).requireOK(t) // HighImpact.

	if state := tj.waitRunTerminalCLI(t, runID, 30*time.Second); state != "CANCELLED" {
		t.Fatalf("aw run cancel: run settled in %s, want CANCELLED", state)
	}
}

// terminalRecovery covers "recovery": hard-kill the worker, restart it,
// then REPLAY the exact same local-commit request stage 12 already made
// (terminalLocalCommitKey, requestLocalCommitCLI) — the same "crash a real
// process, restart it, observe convergence" discipline V6-14A's own
// scenarios use, now triggered by CLI-originated state instead of
// HTTP-originated state. A replay is the right proof here specifically
// because there is no separate "get one local commit" CLI query to poll
// instead (requestLocalCommitCLI's own doc comment): if the crash had left
// anything corrupted, this replay would either fail outright, return a
// DIFFERENT resultVcsObjectId (a real bug: two commits for one logical
// request), or return replayed:false (a fresh request improperly
// re-dispatched instead of served from the stored receipt).
func (tj *terminalJourney) terminalRecovery(t *testing.T) {
	tj.s.hardKillWorker(t)
	tj.s.startWorker(t)

	waitFor(t, "the worker to be healthy again after restart (observed via `aw doctor`)", 30*time.Second, 300*time.Millisecond, func() bool {
		var doctor struct {
			Status string `json:"status"`
		}
		runCLI(t, tj.s, nil, "doctor", "--json").requireOK(t).decodeQuery(t, &doctor)
		return doctor.Status == "HEALTHY"
	})

	// Every flag below is byte-identical to stage 12's own original call
	// (tj.commitWorkspace/tj.commitReleaseSetVersion, captured there) —
	// re-deriving --expected-release-set-version from CURRENT state here
	// would read a LATER version (stage 12's own seal already advanced
	// it), which BuildEnvelope's semantic-hash check correctly rejects as
	// a different request reusing the same key, not a replay.
	ws := tj.commitWorkspace
	replay := runCLI(t, tj.s, nil, "release-set", "local-commit",
		"--project-id", tj.projectID, "--release-set-id", tj.releaseSetID,
		"--repository-workspace-id", ws.ID, "--expected-release-set-version", strconv.FormatUint(tj.commitReleaseSetVersion, 10),
		"--expected-workspace-version", strconv.FormatUint(ws.Version, 10),
		"--message", "v6-15p terminal acceptance: add the release note",
		"--author-name", "Terminal Acceptance", "--author-email", "terminal@example.invalid",
		"--idempotency-key", terminalLocalCommitKey,
		"--wait", "--wait-timeout", "30s", "--yes").requireOK(t)
	envelope := replay.decodeEnvelope(t, nil)
	if !envelope.Replayed {
		t.Fatalf("aw release-set local-commit after worker restart: replayed = false, want true (the SAME idempotency key as stage 12's own request)")
	}
	var result struct {
		Wait *releaseSetLocalCommitStatus `json:"wait"`
	}
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatalf("decode replayed local-commit result: %v", err)
	}
	if result.Wait == nil || result.Wait.State != "COMMITTED" {
		t.Fatalf("replayed local commit --wait observation = %+v, want state COMMITTED", result.Wait)
	}
	if result.Wait.ResultVCSObjectID != tj.resultRevision {
		t.Fatalf("replayed local commit resultVcsObjectId = %q, want the SAME revision stage 12 already committed (%q) — a worker restart must never produce a second commit for one logical request",
			result.Wait.ResultVCSObjectID, tj.resultRevision)
	}

	// The ReleaseSet itself (a separate aggregate from the local commit)
	// must also still report SEALED — the crash must not have reverted or
	// otherwise disturbed stage 12's own seal.
	if after := tj.releaseSetDetailCLI(t); after.State != "SEALED" {
		t.Fatalf("aw release-set show after worker restart: state = %q, want SEALED", after.State)
	}
}

type releaseSetDetail struct {
	State   string `json:"state"`
	Version uint64 `json:"version"`
}

func (tj *terminalJourney) releaseSetDetailCLI(t *testing.T) releaseSetDetail {
	t.Helper()
	var detail releaseSetDetail
	runCLI(t, tj.s, nil, "release-set", "show", tj.releaseSetID).requireOK(t).decodeQuery(t, &detail)
	return detail
}

// terminalHTTPReplay is the design doc's own "re-run V6-14 HTTP suite via
// same root": with serve+worker STILL the same two processes, on the SAME
// --db/--artifact-root/--workspace-root the whole CLI journey above just
// used, a fresh project/repository/WorkItem/Run cycle is driven entirely
// over HTTP (journey's own already-proven stage methods, completely
// unchanged) — proving the CLI-created state above does not corrupt or
// interfere with the HTTP surface on a shared installation, and that HTTP
// is not "now broken" just because CLI drove everything before it.
func (tj *terminalJourney) terminalHTTPReplay(t *testing.T) {
	hj := &journey{s: tj.s, repoPath: tj.repoPath, originHead: tj.originHead}
	hj.httpProjectAndRepository(t) // NOT journey.projectAndRepository — see that method's own doc comment.

	root := hj.s.api.post(t, "/projects/"+hj.projectID+"/work-items", map[string]any{
		"title": "http-replay-root", "initialScope": hj.scopeGrant(),
	}).requireStatus(t, http.StatusCreated)
	var rootResult struct {
		WorkItemID     string `json:"workItemId"`
		FamilyID       string `json:"familyId"`
		WorkspaceSetID string `json:"workspaceSetId"`
	}
	root.decode(t, &rootResult)
	hj.rootWorkItemID, hj.familyID, hj.workspaceSetID = rootResult.WorkItemID, rootResult.FamilyID, rootResult.WorkspaceSetID
	if hj.rootWorkItemID == "" || hj.familyID == "" || hj.workspaceSetID == "" {
		t.Fatalf("HTTP replay root work item result is missing ids: %s", root.body)
	}

	// approvalOnlyWorkflowDocument (stage_fault_security_test.go), NOT
	// publishVerificationWorkflow: that builder's own dependencies
	// (ctx-policy, agent-profile, attempt-policy, ...) are INSTALLATION-
	// scoped — real primary keys with no project_id, per
	// 0004_shared_definitions.sql — and stage 05 already published every
	// one of them under the SAME hardcoded ids while proving the CLI path.
	// This is the SAME root on purpose, so calling that builder a second
	// time collides on those ids (confirmed: the first version of this
	// stage did exactly that and got a raw 500 "sqlite: unexpected
	// error"). approvalOnlyWorkflowDocument needs no installation-scoped
	// dependency at all, so a project-scoped "http-replay-workflow" here
	// cannot collide with anything the CLI journey already created.
	const authorizedRole = "operator"
	wf := hj.publish(t, "/projects/"+hj.projectID, definition.KindWorkflow, "http-replay-workflow", "http replay workflow",
		approvalOnlyWorkflowDocument(authorizedRole, 3600))
	hj.waitWorkspaceReady(t)
	childID := hj.createChild(t, "http-replay-child", "v6-15p HTTP replay fixture", wf)
	hj.markReady(t, childID)
	runID := hj.startRun(t, childID, wf.versionID)

	type approvalView struct {
		ApprovalRequestID string `json:"approvalRequestId"`
		State             string `json:"state"`
		Version           uint64 `json:"version"`
	}
	var approval approvalView
	waitFor(t, "HTTP replay run to reach a real, pending APPROVAL node", 30*time.Second, 200*time.Millisecond, func() bool {
		var detail struct {
			ApprovalRequests []approvalView `json:"approvalRequests"`
		}
		hj.s.api.get(t, "/runs/"+runID).requireStatus(t, http.StatusOK).decode(t, &detail)
		for _, a := range detail.ApprovalRequests {
			if a.State == "PENDING" || a.State == "ESCALATED" {
				approval = a
				return true
			}
		}
		return false
	})

	resolvePath := "/runs/" + runID + "/approval-requests/" + approval.ApprovalRequestID + "/resolve"
	body := map[string]string{"outcome": "approved", "reason": "v6-15p terminal acceptance: HTTP replay resolve"}
	resolved := hj.s.api.post(t, resolvePath, body, withIdempotencyKey("v6-15p-http-replay-resolve-1"),
		withIfMatch(httpapi.ETagFromVersion(approval.Version))).requireStatus(t, http.StatusOK)
	if !strings.Contains(string(resolved.body), approval.ApprovalRequestID) {
		t.Fatalf("HTTP replay: resolve response did not return the decision: %s", resolved.body)
	}

	// Same "no aggregate-state claim" reasoning as terminalHumanDecision's
	// own doc comment: this fixture carries no CompletionPolicyRef, so the
	// real proof is direct — the approval request itself is DECIDED, the
	// run is no longer stuck waiting on it, and (the whole point of this
	// stage) every one of these real, fresh, HTTP-driven writes succeeded
	// on the SAME installation the CLI journey just finished using.
	hj.waitRunSettled(t, runID)
	var afterResolve struct {
		ApprovalRequests []approvalView `json:"approvalRequests"`
	}
	hj.s.api.get(t, "/runs/"+runID).requireStatus(t, http.StatusOK).decode(t, &afterResolve)
	for _, a := range afterResolve.ApprovalRequests {
		if a.ApprovalRequestID == approval.ApprovalRequestID && a.State != "DECIDED" {
			t.Fatalf("HTTP replay: approval request %s state after resolve = %s, want DECIDED", a.ApprovalRequestID, a.State)
		}
	}
}

// httpProjectAndRepository is journey.projectAndRepository's own logic,
// reproduced here rather than called directly, for exactly one reason:
// that method hardcodes repositoryId "repo-a", and `repositories.id` is a
// GLOBAL primary key (0001_initial_schema.sql), not scoped per project —
// reusing "repo-a" for this SECOND project on the SAME database (the
// entire point of this stage: same root as the CLI journey's own
// project, which already registered "repo-a") collides with it. The
// first attempt at this stage did exactly that and surfaced as a raw 500
// "sqlite: unexpected error" rather than a clean 409 — a real rough edge
// in that error path, but not this task's own scope to fix; the correct
// fix on THIS side is simply not colliding.
func (j *journey) httpProjectAndRepository(t *testing.T) {
	t.Helper()
	api := j.s.api

	var created struct {
		ProjectID string `json:"projectId"`
	}
	api.post(t, "/projects", map[string]string{"name": "acceptance-http-replay"}).requireStatus(t, http.StatusCreated).decode(t, &created)
	j.projectID = created.ProjectID
	if j.projectID == "" {
		t.Fatal("project create returned no id")
	}

	registered := api.post(t, "/projects/"+j.projectID+"/repositories", map[string]string{
		"repositoryId": "repo-http-replay", "name": "repo-http-replay", "remoteLocator": j.repoPath, "defaultRef": "main",
	}).requireStatus(t, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	var repository struct {
		RepositoryID string `json:"repositoryId"`
	}
	registered.decode(t, &repository)
	j.repositoryID = repository.RepositoryID
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
