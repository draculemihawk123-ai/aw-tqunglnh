package spikeacceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// runSPK12Scenario closes SPK-12 against both real providers: each fake CLI
// is configured to fail any Resume invocation, then a real
// Checkpoint+ContextSnapshot chain in a real SQLite database drives
// worker.StartFreshFromLatestCheckpoint through the SAME hostile-to-resume
// adapter. Recovery must still succeed for both providers — proving Resume
// was never called, because the fake CLI would have failed the whole run if
// it had been, not because a mock recorded zero calls.
func runSPK12Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	if sc.Binaries.FakeClaude == "" || sc.Binaries.FakeCodex == "" {
		return SPKResult{}, fmt.Errorf("spk12: ScenarioBinaries.FakeClaude and FakeCodex are required")
	}
	var assertions []Assertion
	passed := true
	record := func(name string, ok bool, detail string) {
		assertions = append(assertions, Assertion{Name: name, Passed: ok, Detail: detail})
		if !ok {
			passed = false
		}
	}

	supervisor := processadapter.NewSupervisor()
	adapters := map[string]ports.AgentExecutor{}
	if codexAdapter, err := codex.New(supervisor, codex.Config{Executable: sc.Binaries.FakeCodex}); err != nil {
		return SPKResult{}, fmt.Errorf("spk12: build codex adapter: %w", err)
	} else {
		adapters["codex"] = codexAdapter
	}
	if claudeAdapter, err := claude.New(supervisor, claude.Config{Executable: sc.Binaries.FakeClaude, PermissionMode: "dontAsk"}); err != nil {
		return SPKResult{}, fmt.Errorf("spk12: build claude adapter: %w", err)
	} else {
		adapters["claude"] = claudeAdapter
	}

	tempDir, err := os.MkdirTemp("", "spk12-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk12: create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// The real fake-claude/fake-codex binaries these requests spawn are
	// passed to this scenario as relative paths (cmd/agentkit-spike's own
	// --fake-claude/--fake-codex flags, e.g. "bin/fake-codex") — Go's
	// os/exec resolves a relative Executable against the CHILD's own new
	// working directory (the OS changes directory before resolving/exec'ing
	// a relative image path), not the calling process's cwd. tempDir broke
	// this for the two AgentExecutionRequest.WorkingDirectory values below
	// (V5-05 CI: "fork/exec bin/fake-codex: no such file or directory" on
	// both platforms) — the calling process's OWN cwd is the only directory
	// relative binary paths still resolve from. tempDir itself stays in use
	// for the sqlite database path below, which has no such constraint.
	workingDirectory, err := os.Getwd()
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk12: get working directory: %w", err)
	}

	var recoveredHashes []string
	var lastCorrelation CorrelationIDs
	for _, name := range []string{"codex", "claude"} {
		adapter := adapters[name]

		invalidSessionResult, err := adapter.Resume(ctx, ports.AgentExecutionRequest{
			AttemptID:         ports.ExecutionAttemptID("spk12-invalid-" + name),
			ContextSnapshotID: domainruntime.ContextSnapshotID("spk12-invalid-context-" + name),
			Prompt:            "spk-12 fixture prompt", WorkingDirectory: workingDirectory, Timeout: 5 * time.Second,
			Environment: map[string]string{"AGENTKIT_HELPER_MODE": "invalid-session"},
		}, ports.ProviderSessionRef{Provider: ports.ProviderKey(name), SessionID: name + "-session-0001"}, &recordingEventSink{})
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s resume with invalid session returned a Go error: %w", name, err)
		}
		record(fmt.Sprintf("%s Resume genuinely rejects an invalid session", name),
			invalidSessionResult.Status == ports.AgentExecutionFailed, string(invalidSessionResult.Status))

		databasePath := filepath.Join(tempDir, "agentkit-"+name+".db")
		store, err := sqlite.Open(ctx, databasePath)
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s open store: %w", name, err)
		}

		projectID := "spk12-project-" + name
		familyID := "spk12-family-" + name
		workItemID := "spk12-work-item-" + name
		runID := domainruntime.WorkflowRunID("spk12-run-" + name)
		nodeRunID := domainruntime.NodeRunID("spk12-node-run-" + name)
		interruptedAttempt := domainruntime.ExecutionAttemptID("spk12-attempt-before-" + name)
		replacementAttempt := domainruntime.ExecutionAttemptID("spk12-attempt-after-" + name)

		if err := sqlite.SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s seed owners: %w", name, err)
		}
		definitionProjectID := project.ProjectID(projectID)
		definition := workflow.WorkflowDefinition{
			ID: workflow.WorkflowDefinitionID("spk12-definition-" + name), ProjectID: &definitionProjectID,
			Name: "SPK-12 fixture workflow", Status: workflow.DefinitionActive, Version: 1,
		}
		version, err := workflow.Compile(definition, workflow.PublishRequest{
			VersionID: workflow.WorkflowVersionID("spk12-version-" + name), VersionNumber: 1, Document: spk01WorkflowDocumentV1(),
			Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
				{Kind: "skill", Key: "implement", Version: "v1", Hash: "sha256:spk12-" + name},
			}},
			PublishedBy: "spk12-scenario", PublishedAt: started,
		})
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s compile workflow version: %w", name, err)
		}
		if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s publish workflow version: %w", name, err)
		}
		run, err := domainruntime.NewWorkflowRun(
			runID, project.ProjectID(projectID), work.WorkItemID(workItemID), version, work.TaskFamilyID(familyID), 1,
			json.RawMessage(`{}`),
		)
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s new workflow run: %w", name, err)
		}
		if err := store.StartWorkflowRun(ctx, run); err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s start workflow run: %w", name, err)
		}
		if err := sqlite.SeedFixtureNodeRunAndAttempt(ctx, store, runID, nodeRunID, interruptedAttempt); err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s seed node run/attempt: %w", name, err)
		}

		revisions, err := workspace.NewRevisionSet([]workspace.Revision{{
			RepositoryID: project.RepositoryID("spk12-repo-" + name), VCSObjectID: "spk12-base-" + name, WorkspaceGeneration: 1,
		}})
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s build revision set: %w", name, err)
		}
		snapshot, err := domainruntime.NewContextSnapshot(domainruntime.ContextSnapshotInput{
			ID: domainruntime.ContextSnapshotID("spk12-context-" + name), AttemptID: interruptedAttempt,
			Messages:  []domainruntime.ContextMessage{{Role: domainruntime.ContextRoleUser, Content: "Continue after invalid session"}},
			Revisions: revisions, CreatedAt: started,
		})
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s build context snapshot: %w", name, err)
		}
		if _, err := store.StoreContextSnapshot(ctx, project.ProjectID(projectID), snapshot); err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s persist context snapshot: %w", name, err)
		}
		checkpoint, err := domainruntime.NewCheckpoint(
			domainruntime.CheckpointID("spk12-checkpoint-"+name), runID, nodeRunID, interruptedAttempt,
			1, 1, snapshot.ID(), revisions, "sha256:spk12-shared-state-"+name, nil, started,
		)
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s build checkpoint: %w", name, err)
		}
		if _, err := store.StoreCheckpoint(ctx, checkpoint); err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s persist checkpoint: %w", name, err)
		}

		recoveryEvents := &recordingEventSink{}
		result, err := worker.StartFreshFromLatestCheckpoint(ctx, store, adapter, interruptedAttempt, ports.AgentExecutionRequest{
			AttemptID: replacementAttempt, WorkingDirectory: workingDirectory, Timeout: 5 * time.Second,
			Environment: map[string]string{"AGENTKIT_HELPER_MODE": "invalid-session"},
		}, recoveryEvents)
		if err != nil {
			if closeErr := store.Close(); closeErr != nil {
				return SPKResult{}, fmt.Errorf("spk12: %s recovery through hostile-to-resume adapter: %w (also failed to close store: %v)", name, err, closeErr)
			}
			return SPKResult{}, fmt.Errorf("spk12: %s recovery through hostile-to-resume adapter: %w", name, err)
		}
		record(fmt.Sprintf("%s recovery succeeds through Start, proving Resume was never used", name),
			result.Status == ports.AgentExecutionSucceeded && result.AttemptID == replacementAttempt,
			fmt.Sprintf("status=%s attempt=%s", result.Status, result.AttemptID))

		reloaded, err := store.LoadContextSnapshot(ctx, snapshot.ID())
		if err != nil {
			_ = store.Close()
			return SPKResult{}, fmt.Errorf("spk12: %s reload context snapshot: %w", name, err)
		}
		record(fmt.Sprintf("%s pinned context hash is unchanged across recovery", name),
			reloaded.ContentHash() == snapshot.ContentHash(), "")
		recoveredHashes = append(recoveredHashes, reloaded.ContentHash())
		lastCorrelation = CorrelationIDs{
			ProjectID: projectID, FamilyID: familyID, RunID: string(runID),
			NodeRunID: string(nodeRunID), AttemptID: string(replacementAttempt),
		}

		if err := store.Close(); err != nil {
			return SPKResult{}, fmt.Errorf("spk12: %s close store: %w", name, err)
		}
	}

	sessionsArtifact, err := sc.Bundle.PutJSON("providers/sessions.json", map[string]any{
		"recoveredContextHashes": recoveredHashes,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk12: write sessions evidence: %w", err)
	}

	return SPKResult{
		Passed:      passed,
		Assertions:  assertions,
		Correlation: lastCorrelation,
		Platform:    Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:      Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts:   []ArtifactRef{{Kind: ArtifactKindProviders, Artifact: sessionsArtifact}},
	}, nil
}
