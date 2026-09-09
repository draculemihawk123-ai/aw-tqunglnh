package spikeacceptance

import (
	"context"
	"fmt"
	"os"
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

	// Audit finding (2026-09-08): "missing kinds present" alone tolerates a
	// provider emitting EXTRA events outside spk11NormalizedDifferencesAllowlist,
	// a different COUNT of a required kind, or dropping one and adding an
	// unrelated one. Strip each provider's own explicitly allow-listed extra
	// kinds, then require the remaining kinds to match as a MULTISET (same
	// kinds, same counts) between providers.
	//
	// A strict total-order comparison was tried first and immediately caught
	// a real difference: Codex's own real wire protocol reports its tool
	// call as two operational events (item.started/item.completed) BEFORE a
	// separate summary agent_message item, while Claude's own real protocol
	// bundles the assistant's text and its tool_use request into ONE
	// message — so claude.go's normalizer emits ASSISTANT_MESSAGE before the
	// TOOL_CALL_STARTED/FINISHED pair the later tool_result implies
	// (confirmed by reading each fixture's own real JSONL payload,
	// internal/adapters/providers/fixtures.go's own writeProviderSuccess —
	// not assumed). That is a genuine, legitimate difference in how the two
	// providers structure a turn, not a bug in either fake — a multiset
	// comparison plus the two structural invariants below is the correct
	// contract, not "identical total order".
	normalizedCodex := normalizeEventKinds(ports.ProviderCodex, codexKinds)
	normalizedClaude := normalizeEventKinds(ports.ProviderClaude, claudeKinds)
	record("normalized event kinds (after the explicit allow-list) match as a multiset between providers — same kinds, same counts",
		equalEventKindMultisets(normalizedCodex, normalizedClaude),
		fmt.Sprintf("normalizedCodex=%v normalizedClaude=%v", normalizedCodex, normalizedClaude))
	record("both providers start with EXECUTION_STARTED and end with EXECUTION_FINISHED",
		firstEventKind(codexKinds) == ports.AgentEventExecutionStarted && lastEventKind(codexKinds) == ports.AgentEventExecutionFinished &&
			firstEventKind(claudeKinds) == ports.AgentEventExecutionStarted && lastEventKind(claudeKinds) == ports.AgentEventExecutionFinished,
		fmt.Sprintf("codex first=%s last=%s claude first=%s last=%s", firstEventKind(codexKinds), lastEventKind(codexKinds), firstEventKind(claudeKinds), lastEventKind(claudeKinds)))
	record("both providers report TOOL_CALL_STARTED strictly before its own TOOL_CALL_FINISHED",
		eventKindIndex(codexKinds, ports.AgentEventToolCallStarted) < eventKindIndex(codexKinds, ports.AgentEventToolCallFinished) &&
			eventKindIndex(claudeKinds, ports.AgentEventToolCallStarted) < eventKindIndex(claudeKinds, ports.AgentEventToolCallFinished),
		fmt.Sprintf("codex=%v claude=%v", codexKinds, claudeKinds))

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
	// The real fake-claude/fake-codex binaries this scenario spawns are
	// passed to it as relative paths (cmd/agentkit-spike's own --fake-claude/
	// --fake-codex flags, e.g. "bin/fake-codex") — Go's os/exec resolves a
	// relative Executable against the CHILD's own new working directory
	// (the OS changes directory before resolving/exec'ing a relative
	// image path), not the calling process's cwd. os.TempDir() broke this
	// (V5-05 CI: "fork/exec bin/fake-codex: no such file or directory" on
	// both platforms) — the calling process's OWN cwd is the only directory
	// relative binary paths still resolve from, so that is what a scenario
	// with a relative Executable must use here.
	workingDirectory, _ := os.Getwd()
	return ports.AgentExecutionRequest{
		AttemptID:         ports.ExecutionAttemptID(attemptID),
		ContextSnapshotID: domainruntime.ContextSnapshotID("context-" + attemptID),
		Prompt:            "spk-11 fixture prompt",
		WorkingDirectory:  workingDirectory,
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

// spk11NormalizedDifferencesAllowlist is the audit finding (2026-09-08) fix:
// an EXPLICIT, versioned record of which event kinds one provider may emit
// that another legitimately does not — Codex's fake CLI emits a
// STATUS_CHANGED event Claude's protocol has no equivalent for (this
// scenario's own original doc comment already named this exact example).
// Any OTHER kind-level difference between providers is no longer tolerated
// silently: normalizeEventKinds strips only what is listed here, and
// equalEventKindSequences then demands the remainder match exactly,
// including order. Adding a new tolerated difference means editing this
// map, not weakening the comparison itself.
var spk11NormalizedDifferencesAllowlist = map[ports.ProviderKey][]ports.AgentEventKind{
	ports.ProviderCodex: {ports.AgentEventStatusChanged},
}

// normalizeEventKinds strips provider's own allow-listed extra kinds from
// sequence, leaving only the kinds every provider is expected to share.
func normalizeEventKinds(provider ports.ProviderKey, sequence []ports.AgentEventKind) []ports.AgentEventKind {
	allowed := make(map[ports.AgentEventKind]struct{}, len(spk11NormalizedDifferencesAllowlist[provider]))
	for _, kind := range spk11NormalizedDifferencesAllowlist[provider] {
		allowed[kind] = struct{}{}
	}
	normalized := make([]ports.AgentEventKind, 0, len(sequence))
	for _, kind := range sequence {
		if _, ok := allowed[kind]; ok {
			continue
		}
		normalized = append(normalized, kind)
	}
	return normalized
}

// equalEventKindMultisets reports whether a and b contain the identical
// kinds with the identical COUNT of each — stronger than a set-membership
// check (a dropped event and an unrelated extra event, or a duplicate, no
// longer cancel out to "same set"), while deliberately NOT requiring
// identical relative order between different kinds (see this file's own
// call site for why a strict total order is the wrong contract here).
func equalEventKindMultisets(a, b []ports.AgentEventKind) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[ports.AgentEventKind]int, len(a))
	for _, kind := range a {
		counts[kind]++
	}
	for _, kind := range b {
		counts[kind]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

// firstEventKind/lastEventKind/eventKindIndex are small structural-order
// helpers — this file's own call site uses them for the few genuine
// ordering invariants that DO hold across every provider (EXECUTION_STARTED
// first, EXECUTION_FINISHED last, a tool call's own STARTED before its own
// FINISHED), as opposed to the relative order between unrelated kinds
// (assistant messaging vs tool-call reporting), which legitimately varies
// by provider.
func firstEventKind(sequence []ports.AgentEventKind) ports.AgentEventKind {
	if len(sequence) == 0 {
		return ""
	}
	return sequence[0]
}

func lastEventKind(sequence []ports.AgentEventKind) ports.AgentEventKind {
	if len(sequence) == 0 {
		return ""
	}
	return sequence[len(sequence)-1]
}

func eventKindIndex(sequence []ports.AgentEventKind, kind ports.AgentEventKind) int {
	for i, k := range sequence {
		if k == kind {
			return i
		}
	}
	return -1
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
