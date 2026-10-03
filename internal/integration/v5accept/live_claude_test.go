// V9-11 — "Kiểm chứng với Claude CLI thật"
// (docs/design/12-v9-harness-alignment.md V9-11; HE-01-M06, HE-02-M02).
//
// Every other scenario in this package drives the V9 changes with cmd/fake-claude,
// a wire-protocol stand-in that never reads its prompt. This one drives the same
// stack — a real git worktree, a real sqlite store, a real worker pool, the real
// claude.Adapter — against the REAL Claude CLI, so it is the one place that shows
// the V9 changes work with a model that actually reads what aw tells it.
//
// It is skipped unless AW_LIVE_CLAUDE=1: a run spends real money (a few tenths of
// a dollar per attempt) and needs a logged-in `claude`. CI never sets it.
//
//	AW_LIVE_CLAUDE=1 go test ./internal/integration/v5accept/ -run TestV9LiveClaude -v -timeout 30m
//
// Optional settings: AW_LIVE_CLAUDE_EXECUTABLE (default: `claude` on PATH),
// AW_LIVE_CLAUDE_MODEL (default claude-sonnet-4-6), AW_LIVE_CLAUDE_EFFORT (default
// medium), AW_LIVE_CLAUDE_MAX_USD (a ceiling for ONE attempt, default 0.75) and
// AW_LIVE_CLAUDE_OUT (a directory to write the evidence bundle to: summary, prompts,
// transcripts, the final notes.txt).
//
// The workflow is the smallest one that passes through every V9 claim:
//
//		build (MAKER) -> check (COMMAND, failureOutcome) -> review (CHECKER) -> end
//		build <-failed- check          build <-rework- review
//
//	  - V9-01: review runs after build in the same run and reads what build left
//	    uncommitted in the worktree; the run does not die on SCOPE_VIOLATION.
//	  - V9-02: the check fails the first time (build is not told what it demands),
//	    the run goes back to build, and build's second prompt carries the failure.
//	  - V9-03: review has two allowed outcomes, so its prompt lists them and
//	    describes the marker protocol; the real model must pick one with it.
//	  - V9-04: the layer resources that tell build and review what they are
//	    (tagged blockKinds MAKER / CHECKER) reach only the node they are for.
//	  - V9-05: the Claude process is given its environment by the AgentProfile's
//	    envAllowlist and the worker's --env-allowlist — no wrapper script.
//
// What the real model does is not scripted: the assertions below are on the
// shape of the run, on what aw handed the model and on the files it left.
package v5accept

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const (
	liveRequiredLine = "VERIFIED-BY-CHECK"
	liveNotesFile    = "notes.txt"
)

// liveSettings are the knobs of one live run, read from the environment.
type liveSettings struct {
	executable string
	model      string
	effort     string
	maxUSD     string
	outDir     string
}

func liveSettingsOrSkip(t *testing.T) liveSettings {
	t.Helper()
	if os.Getenv("AW_LIVE_CLAUDE") != "1" {
		t.Skip("live Claude CLI run: set AW_LIVE_CLAUDE=1 (it spends real money and needs a logged-in `claude`)")
	}
	settings := liveSettings{
		executable: os.Getenv("AW_LIVE_CLAUDE_EXECUTABLE"), model: os.Getenv("AW_LIVE_CLAUDE_MODEL"),
		effort: os.Getenv("AW_LIVE_CLAUDE_EFFORT"), maxUSD: os.Getenv("AW_LIVE_CLAUDE_MAX_USD"), outDir: os.Getenv("AW_LIVE_CLAUDE_OUT"),
	}
	if settings.executable == "" {
		path, err := exec.LookPath("claude")
		if err != nil {
			t.Fatalf("no `claude` on PATH and AW_LIVE_CLAUDE_EXECUTABLE is unset: %v", err)
		}
		settings.executable = path
	}
	if settings.model == "" {
		settings.model = "claude-sonnet-4-6"
	}
	if settings.effort == "" {
		settings.effort = "medium"
	}
	if settings.maxUSD == "" {
		settings.maxUSD = "0.75"
	}
	return settings
}

// liveEnvironmentNames are the parent-environment variables the Claude process
// is allowed to inherit — names only; the profile and the worker both list them.
// Only the ones this process actually has are listed.
func liveEnvironmentNames() []string {
	candidates := []string{"PATH", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SystemRoot", "TEMP", "TMP", "TMPDIR", "XDG_CONFIG_HOME"}
	var names []string
	for _, name := range candidates {
		if _, ok := os.LookupEnv(name); ok {
			names = append(names, name)
		}
	}
	return names
}

// liveCheckScript is the COMMAND check: notes.txt must contain a line that is
// exactly liveRequiredLine. Nothing in the task text mentions this line, so the
// first build cannot satisfy it — the failure, and what it says, is what sends
// the maker back with something to act on.
func liveCheckScript() (key, script string) {
	message := "check failed: " + liveNotesFile + " must contain a line that is exactly " + liveRequiredLine
	if stdruntime.GOOS == "windows" {
		// Batch builtins only: a COMMAND runs with the environment its Command
		// definition declares — none here — so there is no PATH and `findstr` is not
		// found (the first live runs' check failed that way, and said so in the text
		// the maker was given). `for /f` also reads a file whose lines end in a bare
		// LF, which is how the model writes them.
		return "live-check.bat", "@echo off\r\n" +
			"if not exist " + liveNotesFile + " goto fail\r\n" +
			"set FOUND=\r\n" +
			"for /f \"usebackq delims=\" %%L in (\"" + liveNotesFile + "\") do if \"%%L\"==\"" + liveRequiredLine + "\" set FOUND=1\r\n" +
			"if not defined FOUND goto fail\r\n" +
			"exit /b 0\r\n" +
			":fail\r\n" +
			"echo " + message + " 1>&2\r\n" +
			"exit /b 1\r\n"
	}
	return "live-check.sh", "#!/bin/sh\n" +
		"if [ -f " + liveNotesFile + " ] && grep -qx '" + liveRequiredLine + "' " + liveNotesFile + "; then exit 0; fi\n" +
		"echo '" + message + "' >&2\nexit 1\n"
}

func liveLayerDocument() layer.LayerDocument {
	provenance := layer.Provenance{Owner: "platform-team", Source: "v9-11 live run", Revision: "v1"}
	return layer.LayerDocument{Resources: []layer.Resource{
		{
			Key: "live-maker-role", Priority: definition.PriorityRequiredProcedure, Provenance: provenance,
			Selector:   layer.Selector{BlockKinds: []string{"MAKER"}},
			Convention: "You are the maker. Do exactly what the task asks, in the repository working directory, with the Write or Edit tool. Create no other file. Do not run commands.",
		},
		{
			Key: "live-checker-role", Priority: definition.PriorityRequiredProcedure, Provenance: provenance,
			Selector: layer.Selector{BlockKinds: []string{"CHECKER"}},
			Convention: "You are the reviewer. Do NOT create, edit or delete any file. Read " + liveNotesFile + ". " +
				"If its first line is exactly Paris, the work is correct: report the outcome approved. Otherwise report rework. " +
				"Keep your answer to one or two sentences and end it with the outcome marker the task contract describes.",
		},
	}}
}

// registerLiveAgentBuild is registerAgentBuild for an arbitrary provider
// executable: the AdapterBuild's content hash is that file's real hash and its
// capability manifest comes from a live probe of it.
func (f *v5AcceptFixture) registerLiveAgentBuild(t *testing.T, adapter *claude.Adapter, executable string) string {
	t.Helper()
	ctx := context.Background()
	capabilities, err := adapter.Capabilities(ctx)
	if err != nil {
		t.Fatalf("probe the real Claude CLI: %v", err)
	}
	contentHash, err := adapterbuild.HashExecutableFile(executable)
	if err != nil {
		t.Fatalf("HashExecutableFile: %v", err)
	}
	eventKinds := make([]string, len(capabilities.CanonicalEventKinds))
	for i, kind := range capabilities.CanonicalEventKinds {
		eventKinds[i] = string(kind)
	}
	manifest := domainadapterbuild.CapabilityManifest{
		SupportsStart: capabilities.SupportsStart, SupportsResume: capabilities.SupportsResume, SupportsCancel: capabilities.SupportsCancel,
		CanonicalEventKinds: eventKinds,
	}
	_, manifestHash, err := domainadapterbuild.HashCapabilityManifest(manifest)
	if err != nil {
		t.Fatalf("HashCapabilityManifest: %v", err)
	}
	build, err := domainadapterbuild.NewBuild(domainadapterbuild.NewBuildRequest{
		Tuple: domainadapterbuild.CandidateTuple{
			ProviderKey: string(capabilities.Provider), ExecutablePath: executable, ExecutableContentHash: contentHash,
			ProtocolVersion: capabilities.ProtocolVersion, CapabilityManifestHash: manifestHash,
			OS: stdruntime.GOOS, Toolchain: stdruntime.Version(), ConfigIdentity: "v9-11-live",
		},
		CapabilityManifest: manifest, RegisteredBy: "operator-1", RegisteredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("NewBuild: %v", err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, _, err := tx.AdapterBuilds().InsertIfAbsent(ctx, build)
		return err
	}); err != nil {
		t.Fatalf("InsertIfAbsent(AdapterBuild): %v", err)
	}
	return build.ID()
}

func liveWorkflowDocument(buildID string) workflow.WorkflowDocument {
	build := v9AgentNode("build", buildID, workflow.AgentRoleMaker, "done")
	build.Outcomes = []string{"done", "escalated"}
	build.CyclePolicy = &workflow.CyclePolicy{MaxIterations: 3, EscalationOutcome: "escalated"}
	review := v9AgentNode("review", buildID, workflow.AgentRoleChecker, "approved")
	review.Outcomes = []string{"approved", "rework"}
	check := workflow.Node{
		Key: "check", Type: workflow.NodeCommand, Outcomes: []string{"passed", "failed"},
		Command: &workflow.CommandNodeConfig{
			CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "live-cmd-def", VersionID: "live-cmd-v1"},
			PolicyRefs: v9PolicyRefs(), FailureOutcome: "failed",
		},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			build, check, review,
			{Key: "end", Type: workflow.NodeEnd},
			{Key: "escalated_end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-build", From: "start", Outcome: "next", To: "build"},
			{Key: "build-check", From: "build", Outcome: "done", To: "check"},
			{Key: "build-escalated", From: "build", Outcome: "escalated", To: "escalated_end"},
			{Key: "check-review", From: "check", Outcome: "passed", To: "review"},
			{Key: "check-build", From: "check", Outcome: "failed", To: "build"},
			{Key: "review-end", From: "review", Outcome: "approved", To: "end"},
			{Key: "review-build", From: "review", Outcome: "rework", To: "build"},
		},
	}
}

// liveAttemptReport is one attempt as the evidence bundle records it.
type liveAttemptReport struct {
	NodeKey            string   `json:"nodeKey"`
	Activation         uint64   `json:"activation"`
	NodeRunID          string   `json:"nodeRunId"`
	NodeRunState       string   `json:"nodeRunState"`
	SelectedOutcome    string   `json:"selectedOutcome,omitempty"`
	AttemptID          string   `json:"attemptId"`
	AttemptState       string   `json:"attemptState"`
	TerminationReason  string   `json:"terminationReason,omitempty"`
	FailureCode        string   `json:"failureCode,omitempty"`
	SnapshotEvidence   []string `json:"snapshotEvidenceIds,omitempty"`
	EvidenceIDs        []string `json:"evidenceIds,omitempty"`
	PromptFile         string   `json:"promptFile,omitempty"`
	TranscriptFile     string   `json:"transcriptFile,omitempty"`
	TranscriptEvents   int      `json:"transcriptEvents,omitempty"`
	AllowedOutcomes    []string `json:"allowedOutcomes,omitempty"`
	HardConstraintKeys []string `json:"resourceKeys,omitempty"`
}

type liveSummary struct {
	RunID            string   `json:"runId"`
	WorkItemID       string   `json:"workItemId"`
	FinalRunState    string   `json:"finalRunState"`
	Provider         string   `json:"provider"`
	ProviderVersion  string   `json:"providerVersion"`
	Model            string   `json:"model"`
	Effort           string   `json:"effort"`
	MaxUSDPerAttempt string   `json:"maxUsdPerAttempt"`
	EnvironmentNames []string `json:"environmentNames"`
	DurationSeconds  float64  `json:"durationSeconds"`
	ReworkRounds     int      `json:"reworkRounds"`
	// Findings are things the run showed that the assertions do not fail on —
	// known gaps between aw and the real CLI, each described in
	// docs/release/live-provider/README.md.
	Findings          []string            `json:"findings,omitempty"`
	Attempts          []liveAttemptReport `json:"attempts"`
	FinalNotes        string              `json:"finalNotes"`
	ReportedCostUSD   float64             `json:"reportedCostUsd,omitempty"`
	ReportedCostNotes string              `json:"reportedCostNotes,omitempty"`
}

func TestV9LiveClaude_MakerCheckFailReworkCheckerApprovesWithoutAWrapper(t *testing.T) {
	settings := liveSettingsOrSkip(t)
	ctx := context.Background()
	started := time.Now()

	f := newV5AcceptFixture(t)

	names := liveEnvironmentNames()
	adapter, err := claude.New(f.supervisor, claude.Config{
		// acceptEdits, not the default: a headless Claude CLI refuses every file
		// write in a worktree it has not been told to trust, and a worktree aw
		// creates is new for every WorkItem, so the project's own
		// .claude/settings.json allow rules are ignored there ("this workspace has
		// not been trusted") and no one can answer the trust dialog. `aw worker`
		// has no flag for this: see docs/release/alpha-release-report.md, V9-11.
		Executable: settings.executable, PermissionMode: "acceptEdits",
		// The CLI flags aw has no setting for: the effort level and a ceiling for
		// ONE attempt, so a misbehaving model cannot spend without limit.
		StartArgs:                   []string{"--effort", settings.effort, "--max-budget-usd", settings.maxUSD},
		VersionInheritedEnvironment: names,
	})
	if err != nil {
		t.Fatalf("claude.New: %v", err)
	}
	buildID := f.registerLiveAgentBuild(t, adapter, settings.executable)
	registry := f.newAgentRegistry(t, adapter)

	capabilities, err := adapter.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}

	// Knowledge: one layer, two resources, each for one role.
	layerDoc := liveLayerDocument()
	publishAreaLayer(t, f, layerDoc)
	publishPolicyVersion(t, f.uow, "v9-context-policy-def", "v9-context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"live"}, Budget: policy.ContextBudget{MaxTokens: 65536}, ResourceRefs: areaLayerRefs(t, layerDoc),
		},
	})
	publishAgentProfileDocument(t, f.uow, "v9-agent-profile-def", "v9-agent-profile-v1", agentprofile.AgentProfileDocument{
		ProviderKey: string(ports.ProviderClaude), Model: settings.model, ToolRefs: []string{"read_file"},
		ContextPolicyRef: definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v9-context-policy-def", VersionID: "v9-context-policy-v1"},
		Compatibility:    agentprofile.Compatibility{OS: []string{stdruntime.GOOS}},
		Budget:           agentprofile.Budget{MaxTokens: 4096},
		EnvAllowlist:     names,
	})
	publishPolicyVersion(t, f.uow, "v9-attempt-policy-def", "v9-attempt-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt:  &policy.AttemptRules{MaxAttempts: 1, BackoffSeconds: 1, TimeoutSeconds: 900},
	})
	publishPolicyVersion(t, f.uow, "v9-permission-policy-def", "v9-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())

	// The check.
	scriptKey, script := liveCheckScript()
	skillDoc := v5AcceptTwoResourceSkillDocument(scriptKey, script, "unused.txt", "unused")
	publishSkillVersion(t, f.uow, "live-skill-def", "live-skill-v1", skillDoc)
	publishCommandVersion(t, f.uow, "live-cmd-def", "live-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "live-skill-v1", ResourceKey: scriptKey, ContentHash: resourceContentHash(t, "live-skill-v1", scriptKey, skillDoc)},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       command.Compatibility{OS: []string{stdruntime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      120,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})

	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "live-workflow-def", "live-workflow-v1", liveWorkflowDocument(buildID), workflow.DependencyManifest{})

	// The worker's own allowlist: the same names the profile asks for.
	workerConfig := fake.NewRuntimeExecutionConfigProvider()
	workerConfig.Snapshot.EnvAllowlist = append([]string{}, names...)
	f.runtimeConfig = workerConfig
	executor := f.newAgentExecutor(registry, runtime.WithAgentEnvironmentCeiling(workerConfig))
	router := &runtime.NodeExecutorRouter{Agent: executor, Command: f.newCommandExecutor(), Gate: f.newGateExecutor()}

	handlers := f.registerHandlersWithAgents(router, "live", registry)
	_, stopPool := f.startPoolWithConfig(t, handlers, workerpool.Config{
		Concurrency: 1, Owner: "v9-live", LeaseTTL: 2 * time.Minute,
		HeartbeatEvery: 2 * time.Second, PollInterval: 20 * time.Millisecond,
		ShutdownGrace: 10 * time.Second, RecoveryInterval: 2 * time.Second,
	})
	defer stopPool()

	root := f.createRootWorkItem(t, "live-root", workdomain.RepositoryWrite)
	child := createLiveChildWorkItem(t, f, root.WorkItemID)
	baseHandle := f.repositoryWorkspaceHandle(t, root.WorkspaceSetID, v5AcceptRepositoryID, 1)
	workDir, err := f.provider.WorkingDirectory(ctx, baseHandle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}

	startCmd := testCmd("live-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	startedRun, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	t.Logf("run %s started (work item %s); model %s, effort %s", startedRun.RunID, child.WorkItemID, settings.model, settings.effort)

	finalState := waitForLiveRunEnd(t, f, startedRun.RunID, 25*time.Minute)
	stopPool()

	history := readV9fHistory(t, f, startedRun.RunID)
	summary := liveSummary{
		ReworkRounds: len(history.byKey("build")) - 1,
		RunID:        startedRun.RunID, WorkItemID: child.WorkItemID, FinalRunState: string(finalState),
		Provider: string(capabilities.Provider), ProviderVersion: capabilities.TestedCLIVersion,
		Model: settings.model, Effort: settings.effort, MaxUSDPerAttempt: settings.maxUSD, EnvironmentNames: names,
		DurationSeconds: time.Since(started).Seconds(),
	}
	notes, _ := os.ReadFile(filepath.Join(workDir, liveNotesFile))
	summary.FinalNotes = string(notes)

	bundle := newLiveBundle(t, settings.outDir, f)
	for _, nodeRun := range history.nodeRuns {
		for _, attempt := range history.attempts[nodeRun.ID] {
			report := liveAttemptReport{
				NodeKey: nodeRun.NodeKey, Activation: nodeRun.ActivationSequence, NodeRunID: string(nodeRun.ID),
				NodeRunState: string(nodeRun.State), SelectedOutcome: nodeRun.SelectedOutcome,
				AttemptID: string(attempt.ID), AttemptState: string(attempt.State),
				TerminationReason: string(attempt.TerminationReason), FailureCode: string(attempt.FailureCode),
				SnapshotEvidence: f.snapshotEvidenceIDs(t, string(attempt.ID)),
			}
			for _, row := range f.evidenceOfAttempt(t, string(attempt.ID)) {
				report.EvidenceIDs = append(report.EvidenceIDs, string(row.ID))
			}
			if agentNode(nodeRun.NodeKey) {
				prompt := f.v9fPromptRaw(t, startedRun.RunID, nodeRun, attempt)
				report.PromptFile = bundle.writePrompt(nodeRun, prompt)
				var decoded struct {
					TaskContract struct {
						AllowedOutcomes []string `json:"allowedOutcomes"`
					} `json:"taskContract"`
					Resources []struct {
						ResourceKey string `json:"resourceKey"`
					} `json:"resources"`
				}
				_ = json.Unmarshal([]byte(prompt), &decoded)
				report.AllowedOutcomes = decoded.TaskContract.AllowedOutcomes
				for _, r := range decoded.Resources {
					report.HardConstraintKeys = append(report.HardConstraintKeys, r.ResourceKey)
				}
				report.TranscriptFile, report.TranscriptEvents = bundle.writeTranscript(t, nodeRun, attempt)
			}
			summary.Attempts = append(summary.Attempts, report)
		}
	}
	if reviewRuns := history.byKey("review"); len(reviewRuns) == 1 && !reviewerReadMakersFile(t, f, history.onlyAttempt(t, reviewRuns[0])) {
		summary.Findings = append(summary.Findings,
			"F2: the reviewer never read the maker's "+liveNotesFile+". The CLI was started in an empty scratch directory (the checker's working directory) and neither its prompt nor its command line names where the repository is, so it wrote a "+liveNotesFile+" of its own there and approved that.")
	}
	for _, finding := range summary.Findings {
		t.Logf("FINDING %s", finding)
	}
	bundle.writeSummary(t, summary, notes)

	// ---- what the V9 changes must have done ----

	if finalState != runtimedomain.WorkflowRunVerifying {
		t.Fatalf("run %s ended %s, want VERIFYING (the workflow reached its end node); see the evidence bundle", startedRun.RunID, finalState)
	}
	builds, checks, reviews := history.byKey("build"), history.byKey("check"), history.byKey("review")
	// How many rounds the real model needs is its own business (it may act on the
	// check's message at once or argue with it first): the loop must have gone
	// round at least once, every build must have been followed by a check, only
	// the last check may have passed, and there must be exactly one review.
	if len(builds) < 2 || len(checks) != len(builds) || len(reviews) != 1 || len(history.byKey("end")) != 1 || len(history.byKey("escalated_end")) != 0 {
		t.Fatalf("activations: build=%d check=%d review=%d end=%d escalated_end=%d, want builds>=2, checks==builds, 1 review, 1 end, 0 escalated_end",
			len(builds), len(checks), len(reviews), len(history.byKey("end")), len(history.byKey("escalated_end")))
	}

	// V9-02: the first check failed on its evidence and sent the run back; the
	// last one passed.
	for i, check := range checks {
		want := "failed"
		if i == len(checks)-1 {
			want = "passed"
		}
		if check.SelectedOutcome != want {
			t.Fatalf("check #%d outcome = %q, want %q (outcomes in order must be failed... then passed)", i+1, check.SelectedOutcome, want)
		}
	}
	firstCheck := history.onlyAttempt(t, checks[0])
	failureEvidence := f.evidenceOfAttempt(t, string(firstCheck.ID))
	if len(failureEvidence) == 0 {
		t.Fatal("the failing check left no evidence")
	}
	secondBuild := history.onlyAttempt(t, builds[1])
	if refs := f.snapshotEvidenceIDs(t, string(secondBuild.ID)); len(refs) == 0 {
		t.Fatal("the second build's context snapshot pins no evidence of the failing check")
	}
	secondPrompt := f.v9fPrompt(t, startedRun.RunID, builds[1], secondBuild)
	failures := v9fDecodeCheckFailures(t, secondPrompt)
	if len(failures) != 1 || !strings.Contains(failures[0].Why, liveRequiredLine) {
		t.Fatalf("the second build's checkFailures = %+v, want one that quotes the check's message (%s)", failures, liveRequiredLine)
	}
	if _, present := f.v9fPrompt(t, startedRun.RunID, builds[0], history.onlyAttempt(t, builds[0]))["checkFailures"]; present {
		t.Fatal("the first build's prompt already has a checkFailures section")
	}

	// V9-03 + the real model: the reviewer has a choice, its prompt lists it, and it
	// answered with an outcome that is on the list.
	reviewAttempt := history.onlyAttempt(t, reviews[0])
	if reviewAttempt.State != runtimedomain.ExecutionAttemptSucceeded {
		t.Fatalf("review attempt = %s (reason %s, code %s), want SUCCEEDED", reviewAttempt.State, reviewAttempt.TerminationReason, reviewAttempt.FailureCode)
	}
	reviewPrompt := f.v9fPromptRaw(t, startedRun.RunID, reviews[0], reviewAttempt)
	if !strings.Contains(reviewPrompt, `"allowedOutcomes":["approved","rework"]`) || !strings.Contains(reviewPrompt, "agentkit-outcome") {
		t.Fatalf("the review prompt does not list the outcomes and the marker protocol: %.600s", reviewPrompt)
	}
	if reviews[0].SelectedOutcome != "approved" {
		t.Fatalf("the reviewer's outcome = %q, want approved (it must pick one of approved/rework with the marker)", reviews[0].SelectedOutcome)
	}

	// V9-04: each node got the role resource tagged for it, and not the other's.
	if prompt := f.v9fPromptRaw(t, startedRun.RunID, builds[0], history.onlyAttempt(t, builds[0])); !strings.Contains(prompt, "You are the maker") || strings.Contains(prompt, "You are the reviewer") {
		t.Fatal("the build prompt does not carry exactly the maker role resource")
	}
	if !strings.Contains(reviewPrompt, "You are the reviewer") || strings.Contains(reviewPrompt, "You are the maker") {
		t.Fatal("the review prompt does not carry exactly the checker role resource")
	}

	// V9-01: the reviewer ran after the maker in the same run and changed nothing.
	// V9-05: the real process got an environment without a wrapper: the pinned
	// profile records the names, never a value.
	pinned := pinnedExecutionProfile(t, f, string(builds[0].ID))
	if strings.Join(pinned.AgentInheritedEnvironment, ",") != strings.Join(sortedCopy(names), ",") {
		t.Fatalf("pinned environment names = %v, want %v", pinned.AgentInheritedEnvironment, sortedCopy(names))
	}

	// The files the real model left.
	lines := strings.Split(strings.ReplaceAll(string(notes), "\r\n", "\n"), "\n")
	if len(lines) == 0 || !strings.EqualFold(strings.TrimSpace(lines[0]), "Paris") {
		t.Fatalf("%s = %q, want the first line to be Paris", liveNotesFile, notes)
	}
	foundRequired := false
	for _, line := range lines {
		if strings.TrimSpace(line) == liveRequiredLine {
			foundRequired = true
		}
	}
	if !foundRequired {
		t.Fatalf("%s = %q, want a line %s (what the second build was told)", liveNotesFile, notes, liveRequiredLine)
	}
	t.Logf("live run %s passed: %d builds (%d rework rounds), %d attempts, %.0fs; evidence bundle: %s",
		summary.RunID, len(builds), len(builds)-1, len(summary.Attempts), summary.DurationSeconds, bundle.dir)
}

// reviewerReadMakersFile reports whether the review attempt read the file the
// maker left in the worktree: a Read of a notes.txt whose content has the line
// the check demanded. A reviewer that only saw a file of its own (the scratch
// directory its CLI was started in) did not review anything.
func reviewerReadMakersFile(t *testing.T, f *v5AcceptFixture, attempt runtimedomain.ExecutionAttempt) bool {
	t.Helper()
	ctx := context.Background()
	var records []ports.AgentEventRecord
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		records, err = tx.AgentEvents().ListByAttempt(ctx, string(attempt.ID))
		return err
	}); err != nil {
		t.Fatalf("list agent events: %v", err)
	}
	for _, record := range records {
		if record.Kind != "TOOL_CALL_FINISHED" {
			continue
		}
		var payload struct {
			Tool struct {
				Output string `json:"Output"`
			} `json:"tool"`
		}
		if json.Unmarshal([]byte(record.PayloadJSON), &payload) == nil && strings.Contains(payload.Tool.Output, liveRequiredLine) {
			return true
		}
	}
	return false
}

func agentNode(key string) bool { return key == "build" || key == "review" }

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// createLiveChildWorkItem is createChildWorkItem with the task as a real
// readiness contract, which is how an operator tells an agent what to do.
func createLiveChildWorkItem(t *testing.T, f *v5AcceptFixture, parentWorkItemID string) appwork.CreateChildWorkItemResult {
	t.Helper()
	ctx := context.Background()
	result, err := appwork.CreateChildWorkItem(ctx, f.uow, f.ids, testCmd("live-child", ports.ProjectScope(v5AcceptProjectID), "CreateChildWorkItem"), appwork.CreateChildWorkItemRequest{
		ParentWorkItemID: parentWorkItemID, Title: "Write the capital of France to " + liveNotesFile, ParentJoinPolicy: "v9-live",
		EffectiveScope: []appwork.ScopeGrantRequest{{RepositoryID: v5AcceptRepositoryID, Access: string(workdomain.RepositoryWrite), Reason: "v9-11 live run"}},
		Contract: &appwork.WorkItemContractRequest{
			SchemaVersion: 1,
			Behavior:      "Create a file named " + liveNotesFile + " in the repository root. Its first line is the name of the capital city of France.",
			AcceptanceCriteria: []appwork.AcceptanceCriterionRequest{
				{Description: liveNotesFile + " exists and its first line is the capital of France", VerificationRef: "review"},
			},
			VerificationSpec: "The check command and then a reviewer read " + liveNotesFile + ".",
			RiskLevel:        "LOW",
		},
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem: %v", err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: result.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: 1, NextStatus: workdomain.WorkItemReady,
		})
		return err
	}); err != nil {
		t.Fatalf("force work item READY: %v", err)
	}
	return result
}

// waitForLiveRunEnd polls until the run is VERIFYING (the workflow reached an end
// node), FAILED or CANCELLED, with a deadline sized for real model calls.
func waitForLiveRunEnd(t *testing.T, f *v5AcceptFixture, runID string, limit time.Duration) runtimedomain.WorkflowRunState {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(limit)
	var last runtimedomain.WorkflowRunState
	for time.Now().Before(deadline) {
		var run runtimedomain.WorkflowRun
		if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			run, err = tx.Runtime().GetWorkflowRun(ctx, runID)
			return err
		}); err == nil {
			last = run.State
			switch run.State {
			case runtimedomain.WorkflowRunVerifying, runtimedomain.WorkflowRunFailed, runtimedomain.WorkflowRunCancelled:
				return run.State
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("run %s did not end within %s; last state %s", runID, limit, last)
	return last
}

func pinnedExecutionProfile(t *testing.T, f *v5AcceptFixture, nodeRunID string) runtimedomain.ResolvedExecutionProfileV1 {
	t.Helper()
	ctx := context.Background()
	var pinned runtimedomain.ResolvedExecutionProfileV1
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		decision, err := tx.Runtime().GetDecisionArtifact(ctx, nodeRunID+"-execution-profile-v1")
		if err != nil {
			return err
		}
		return json.Unmarshal(decision.Result, &pinned)
	}); err != nil {
		t.Fatalf("read the pinned execution profile: %v", err)
	}
	return pinned
}

// v9fPromptRaw is v9fPrompt with the prompt text as the worker handed it to the CLI.
func (f *v5AcceptFixture) v9fPromptRaw(t *testing.T, runID string, nodeRun runtimedomain.NodeRun, attempt runtimedomain.ExecutionAttempt) string {
	t.Helper()
	req, err := runtime.AssembleAgentExecutionRequest(context.Background(), f.uow, f.artifacts, runtime.AssembleAgentExecutionRequestRequest{
		RunID: runID, NodeRunID: string(nodeRun.ID), AttemptID: string(attempt.ID),
	})
	if err != nil {
		t.Fatalf("AssembleAgentExecutionRequest: %v", err)
	}
	return req.Prompt
}

// liveBundle writes the evidence of a run to a directory (when one is given).
type liveBundle struct {
	dir string
	f   *v5AcceptFixture
	// redactions replace local paths in everything written out.
	redactions []string
}

func newLiveBundle(t *testing.T, dir string, f *v5AcceptFixture) *liveBundle {
	t.Helper()
	b := &liveBundle{dir: dir, f: f}
	if dir == "" {
		return b
	}
	for _, sub := range []string{"prompts", "transcripts"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("create %s: %v", sub, err)
		}
	}
	home, _ := os.UserHomeDir()
	for _, path := range []string{f.fixtureRoot, home} {
		if path != "" {
			b.redactions = append(b.redactions, path, strings.ReplaceAll(path, `\`, `/`), strings.ReplaceAll(path, `\`, `\\`))
		}
	}
	return b
}

func (b *liveBundle) scrub(text string) string {
	for _, path := range b.redactions {
		text = strings.ReplaceAll(text, path, "<local-path>")
	}
	return text
}

func (b *liveBundle) writePrompt(nodeRun runtimedomain.NodeRun, prompt string) string {
	if b.dir == "" {
		return ""
	}
	name := fmt.Sprintf("%s-%d.json", nodeRun.NodeKey, nodeRun.ActivationSequence)
	var pretty bytes.Buffer
	if json.Indent(&pretty, []byte(prompt), "", "  ") != nil {
		pretty.Reset()
		pretty.WriteString(prompt)
	}
	_ = os.WriteFile(filepath.Join(b.dir, "prompts", name), []byte(b.scrub(pretty.String())), 0o644)
	return "prompts/" + name
}

func (b *liveBundle) writeTranscript(t *testing.T, nodeRun runtimedomain.NodeRun, attempt runtimedomain.ExecutionAttempt) (string, int) {
	t.Helper()
	ctx := context.Background()
	var records []ports.AgentEventRecord
	if err := b.f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		records, err = tx.AgentEvents().ListByAttempt(ctx, string(attempt.ID))
		return err
	}); err != nil {
		t.Fatalf("list agent events: %v", err)
	}
	if b.dir == "" {
		return "", len(records)
	}
	name := fmt.Sprintf("%s-%d.jsonl", nodeRun.NodeKey, nodeRun.ActivationSequence)
	var out strings.Builder
	for _, record := range records {
		line, _ := json.Marshal(map[string]any{
			"sequence": record.Sequence, "kind": record.Kind, "at": record.CreatedAt.UTC().Format(time.RFC3339Nano),
			"payload": json.RawMessage(record.PayloadJSON),
		})
		out.Write(line)
		out.WriteByte('\n')
	}
	_ = os.WriteFile(filepath.Join(b.dir, "transcripts", name), []byte(b.scrub(out.String())), 0o644)
	return "transcripts/" + name, len(records)
}

func (b *liveBundle) writeSummary(t *testing.T, summary liveSummary, notes []byte) {
	t.Helper()
	if b.dir == "" {
		return
	}
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	_ = os.WriteFile(filepath.Join(b.dir, "summary.json"), []byte(b.scrub(string(encoded))+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(b.dir, liveNotesFile), notes, 0o644)
}
