package spikeacceptance

import (
	"context"
	"fmt"
	"runtime"
	"time"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// spk11RequiredEventKinds is the canonical event schema every AgentExecutor
// must emit for a successful run, regardless of provider — the same list
// internal/adapters/providers' own contract_test.go checks via
// assertCanonicalEvents. A provider may emit additional protocol-specific
// events around these (Codex's fake CLI, for instance, also emits a
// STATUS_CHANGED event Claude's protocol has no equivalent for); the
// contract is that these seven are present, not that the two streams are
// identical event-for-event.
var spk11RequiredEventKinds = []ports.AgentEventKind{
	ports.AgentEventExecutionStarted,
	ports.AgentEventAssistantMessage,
	ports.AgentEventToolCallStarted,
	ports.AgentEventToolCallFinished,
	ports.AgentEventUsageReported,
	ports.AgentEventCheckpointProposed,
	ports.AgentEventExecutionFinished,
}

// runSPK11Scenario closes SPK-11: the same fixture request (same prompt,
// same attempt shape) runs once through a real claude.Adapter and once
// through a real codex.Adapter, each driving its own standalone fake-claude
// / fake-codex binary as a genuine child OS process — not an in-process
// fake. Both must produce the same canonical event-kind schema
// (spk11RequiredEventKinds) and the same successful outcome; only provider
// identity/session metadata (and provider-specific extra events) may
// differ, proving the orchestrator-facing contract really is
// provider-neutral.
func runSPK11Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	if sc.Binaries.FakeClaude == "" || sc.Binaries.FakeCodex == "" {
		return SPKResult{}, fmt.Errorf("spk11: ScenarioBinaries.FakeClaude and FakeCodex are required")
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
	codexAdapter, err := codex.New(supervisor, codex.Config{Executable: sc.Binaries.FakeCodex})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk11: build codex adapter: %w", err)
	}
	claudeAdapter, err := claude.New(supervisor, claude.Config{Executable: sc.Binaries.FakeClaude, PermissionMode: "dontAsk"})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk11: build claude adapter: %w", err)
	}

	codexEvents := &recordingEventSink{}
	codexResult, err := codexAdapter.Start(ctx, spk11Request("spk11-codex-attempt"), codexEvents)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk11: codex start: %w", err)
	}
	claudeEvents := &recordingEventSink{}
	claudeResult, err := claudeAdapter.Start(ctx, spk11Request("spk11-claude-attempt"), claudeEvents)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk11: claude start: %w", err)
	}

	record("both providers report a successful outcome",
		codexResult.Status == ports.AgentExecutionSucceeded && claudeResult.Status == ports.AgentExecutionSucceeded,
		fmt.Sprintf("codex=%s claude=%s", codexResult.Status, claudeResult.Status))
	record("both providers stamp their own, distinct provider identity",
		codexResult.Provider == ports.ProviderCodex && claudeResult.Provider == ports.ProviderClaude, "")

	codexKinds := eventKindSequence(codexEvents.snapshot())
	claudeKinds := eventKindSequence(claudeEvents.snapshot())
	codexMissing := missingEventKinds(codexKinds, spk11RequiredEventKinds)
	claudeMissing := missingEventKinds(claudeKinds, spk11RequiredEventKinds)
	record("both providers emit the full canonical event-kind schema",
		len(codexMissing) == 0 && len(claudeMissing) == 0,
		fmt.Sprintf("codexMissing=%v claudeMissing=%v codex=%v claude=%v", codexMissing, claudeMissing, codexKinds, claudeKinds))

	normalizedArtifact, err := sc.Bundle.PutJSON("providers/normalized.jsonl", map[string]any{
		"codexEventKinds": codexKinds, "claudeEventKinds": claudeKinds,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk11: write normalized evidence: %w", err)
	}
	sessionsArtifact, err := sc.Bundle.PutJSON("providers/sessions.json", map[string]any{
		"codex":  codexResult.Session,
		"claude": claudeResult.Session,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk11: write sessions evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Correlation: CorrelationIDs{
			ProjectID: "spk11-project", FamilyID: "spk11-family",
			RunID: "spk11-run", NodeRunID: "spk11-node-run", AttemptID: "spk11-codex-attempt",
		},
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:   Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindProviders, Artifact: normalizedArtifact},
			{Kind: ArtifactKindProviders, Artifact: sessionsArtifact},
		},
	}, nil
}

func spk11Request(attemptID string) ports.AgentExecutionRequest {
	return ports.AgentExecutionRequest{
		AttemptID:         ports.ExecutionAttemptID(attemptID),
		ContextSnapshotID: domainruntime.ContextSnapshotID("context-" + attemptID),
		Prompt:            "spk-11 fixture prompt",
		WorkingDirectory:  ".",
		Environment: map[string]string{
			"AGENTKIT_HELPER_MODE": "success",
		},
		Timeout: 5 * time.Second,
	}
}

func eventKindSequence(events []ports.AgentEvent) []ports.AgentEventKind {
	kinds := make([]ports.AgentEventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

// missingEventKinds reports which of required are absent from present.
func missingEventKinds(present, required []ports.AgentEventKind) []ports.AgentEventKind {
	seen := make(map[ports.AgentEventKind]struct{}, len(present))
	for _, kind := range present {
		seen[kind] = struct{}{}
	}
	var missing []ports.AgentEventKind
	for _, kind := range required {
		if _, ok := seen[kind]; !ok {
			missing = append(missing, kind)
		}
	}
	return missing
}

// recordingEventSink is a minimal ports.AgentEventSink: it exists only to
// capture the normalized event stream for cross-provider comparison, not to
// decide anything about it.
type recordingEventSink struct {
	events []ports.AgentEvent
}

func (s *recordingEventSink) Accept(_ context.Context, event ports.AgentEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *recordingEventSink) snapshot() []ports.AgentEvent {
	return append([]ports.AgentEvent(nil), s.events...)
}

var _ ports.AgentEventSink = (*recordingEventSink)(nil)
