package providers_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const (
	spk12ProjectID          = "project-spk12"
	spk12FamilyID           = "family-spk12"
	spk12WorkItemID         = "work-item-spk12"
	spk12RunID              = "workflow-run-spk12"
	spk12NodeRunID          = "node-run-spk12"
	spk12InterruptedAttempt = "attempt-spk12-before"
	spk12ReplacementAttempt = "attempt-spk12-after"
	spk12ContextSnapshotID  = "context-spk12"
	spk12CheckpointID       = "checkpoint-spk12"
)

// TestSPK12InvalidProviderSessionNeverBlocksRecovery closes SPK-12 with
// run-level evidence: a real claude/codex Adapter, wired to a fake CLI that
// fails any resume attempt (see TestProviderHelperProcess's invalid-session
// mode), drives worker.StartFreshFromLatestCheckpoint against a real
// SQLite checkpoint/context chain. Recovery must still succeed — proving
// Resume was never called, because the fake CLI would have made the whole
// run fail if it had been, not because a mock was told to record zero
// calls.
func TestSPK12InvalidProviderSessionNeverBlocksRecovery(t *testing.T) {
	for _, testCase := range providerCases() {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			workingDirectory := t.TempDir()
			executor := testCase.newExecutor(t, processadapter.NewSupervisor())

			// 1. The fake adapter genuinely rejects Resume when configured
			// hostile — the backstop SPK-12 requires, not an assumption.
			resumeCapture := filepath.Join(t.TempDir(), "resume-invalid.json")
			resumeRequest := helperRequest("resume-invalid-"+testCase.name, workingDirectory, resumeCapture, "invalid-session")
			resumeResult, err := executor.Resume(context.Background(), resumeRequest, ports.ProviderSessionRef{
				Provider:  testCase.provider,
				SessionID: testCase.sessionID,
			}, &eventCollector{})
			if err != nil {
				t.Fatalf("Resume() with an invalid session returned a Go error, want a well-formed failed result: %v", err)
			}
			if resumeResult.Status != ports.AgentExecutionFailed || resumeResult.TerminationReason != "provider_failure" {
				t.Fatalf("Resume() with an invalid session = %+v, want Failed/provider_failure", resumeResult)
			}

			// 2. A real crash/recovery chain: durable Checkpoint+ContextSnapshot
			// in a real SQLite store, then real recovery through the SAME
			// hostile-to-resume adapter.
			ctx := context.Background()
			databasePath := filepath.Join(t.TempDir(), "agentkit-spk12.db")
			store, err := sqlite.Open(ctx, databasePath)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			defer func() {
				if err := store.Close(); err != nil {
					t.Errorf("close store: %v", err)
				}
			}()

			if err := sqlite.SeedFixtureOwners(ctx, store, spk12ProjectID, spk12FamilyID, spk12WorkItemID); err != nil {
				t.Fatalf("seed fixture owners: %v", err)
			}
			definitionProjectID := project.ProjectID(spk12ProjectID)
			definition := workflow.WorkflowDefinition{
				ID:        "definition-spk12",
				ProjectID: &definitionProjectID,
				Name:      "SPK-12 fixture workflow",
				Status:    workflow.DefinitionActive,
				Version:   1,
			}
			version, err := workflow.Compile(definition, workflow.PublishRequest{
				VersionID:     "workflow-version-spk12",
				VersionNumber: 1,
				Document:      spk12WorkflowDocument(),
				Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
					{Kind: "skill", Key: "implement", Version: "skill-v1", Hash: "sha256:skill-v1"},
				}},
				PublishedBy: "spk12-fixture",
				PublishedAt: time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatalf("compile fixture workflow version: %v", err)
			}
			if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
				t.Fatalf("publish fixture workflow version: %v", err)
			}
			run, err := runtime.NewWorkflowRun(
				spk12RunID, spk12ProjectID, spk12WorkItemID, version, spk12FamilyID, 1,
				json.RawMessage(`{}`),
			)
			if err != nil {
				t.Fatalf("create fixture workflow run: %v", err)
			}
			if err := store.StartWorkflowRun(ctx, run); err != nil {
				t.Fatalf("start fixture workflow run: %v", err)
			}
			if err := sqlite.SeedFixtureNodeRunAndAttempt(ctx, store, spk12RunID, spk12NodeRunID, spk12InterruptedAttempt); err != nil {
				t.Fatalf("seed fixture node run and attempt: %v", err)
			}

			revisions, err := workspace.NewRevisionSet([]workspace.Revision{{
				RepositoryID: project.RepositoryID("repo-spk12"), VCSObjectID: "rev-spk12-base", WorkspaceGeneration: 1,
			}})
			if err != nil {
				t.Fatalf("build fixture revision set: %v", err)
			}
			snapshot, err := runtime.NewContextSnapshot(runtime.ContextSnapshotInput{
				ID:        spk12ContextSnapshotID,
				AttemptID: spk12InterruptedAttempt,
				Messages:  []runtime.ContextMessage{{Role: runtime.ContextRoleUser, Content: "Continue task after invalid session"}},
				Revisions: revisions,
				CreatedAt: time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatalf("build fixture context snapshot: %v", err)
			}
			if _, err := store.StoreContextSnapshot(ctx, spk12ProjectID, snapshot); err != nil {
				t.Fatalf("persist fixture context snapshot: %v", err)
			}
			checkpoint, err := runtime.NewCheckpoint(
				spk12CheckpointID, spk12RunID, spk12NodeRunID, spk12InterruptedAttempt,
				1, 1, snapshot.ID(), revisions, "sha256:spk12-shared-state", nil,
				time.Date(2026, 8, 28, 16, 0, 1, 0, time.UTC),
			)
			if err != nil {
				t.Fatalf("build fixture checkpoint: %v", err)
			}
			if _, err := store.StoreCheckpoint(ctx, checkpoint); err != nil {
				t.Fatalf("persist fixture checkpoint: %v", err)
			}

			startCapture := filepath.Join(t.TempDir(), "start-recovery.json")
			base := ports.AgentExecutionRequest{
				AttemptID:        spk12ReplacementAttempt,
				WorkingDirectory: workingDirectory,
				Sandbox:          testCase.startSandbox,
				Timeout:          5 * time.Second,
				Environment: map[string]string{
					"AGENTKIT_PROVIDER_HELPER": "1",
					"AGENTKIT_CAPTURE_PATH":    startCapture,
					"AGENTKIT_HELPER_MODE":     "invalid-session",
				},
			}
			result, err := worker.StartFreshFromLatestCheckpoint(
				ctx, store, executor, spk12InterruptedAttempt, base, &eventCollector{},
			)
			if err != nil {
				t.Fatalf("StartFreshFromLatestCheckpoint() with a hostile-to-resume adapter error = %v", err)
			}
			if result.Status != ports.AgentExecutionSucceeded {
				t.Fatalf("recovery result = %+v, want Succeeded (proves Start, not Resume, was used)", result)
			}
			if result.AttemptID != spk12ReplacementAttempt {
				t.Fatalf("recovery result attempt id = %s, want %s", result.AttemptID, spk12ReplacementAttempt)
			}

			reloaded, err := store.LoadContextSnapshot(ctx, snapshot.ID())
			if err != nil {
				t.Fatalf("reload fixture context snapshot: %v", err)
			}
			if reloaded.ContentHash() != snapshot.ContentHash() {
				t.Fatalf("pinned context hash changed across recovery: %s -> %s", snapshot.ContentHash(), reloaded.ContentHash())
			}
		})
	}
}

func spk12WorkflowDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"execute"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{
					Kind:         definition.KindAgentProfile,
					DefinitionID: "agent-default",
					VersionID:    "agent-default-v1",
				},
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "execute", To: "implement"},
			{Key: "implement-end", From: "implement", Outcome: "done", To: "end"},
		},
	}
}
