package spikeacceptance

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// TestNormalizeEventKinds_StripsOnlyAllowlistedProviderExtras proves
// normalizeEventKinds removes exactly the allow-listed kinds for a
// provider (Codex's own STATUS_CHANGED) and leaves everything else,
// including kinds a provider was never expected to emit at all.
func TestNormalizeEventKinds_StripsOnlyAllowlistedProviderExtras(t *testing.T) {
	sequence := []ports.AgentEventKind{
		ports.AgentEventExecutionStarted, ports.AgentEventStatusChanged, ports.AgentEventAssistantMessage,
		ports.AgentEventToolCallStarted, ports.AgentEventStatusChanged, ports.AgentEventToolCallFinished,
		ports.AgentEventUsageReported, ports.AgentEventCheckpointProposed, ports.AgentEventExecutionFinished,
	}
	got := normalizeEventKinds(ports.ProviderCodex, sequence)
	want := []ports.AgentEventKind{
		ports.AgentEventExecutionStarted, ports.AgentEventAssistantMessage, ports.AgentEventToolCallStarted,
		ports.AgentEventToolCallFinished, ports.AgentEventUsageReported, ports.AgentEventCheckpointProposed,
		ports.AgentEventExecutionFinished,
	}
	if !equalEventKindMultisets(got, want) {
		t.Fatalf("normalizeEventKinds(codex) = %v, want %v", got, want)
	}

	// Claude has no allow-listed extras — normalizing a sequence with no
	// STATUS_CHANGED events must be a no-op.
	claudeSequence := want
	gotClaude := normalizeEventKinds(ports.ProviderClaude, claudeSequence)
	if !equalEventKindMultisets(gotClaude, claudeSequence) {
		t.Fatalf("normalizeEventKinds(claude) = %v, want unchanged %v", gotClaude, claudeSequence)
	}
}

// TestEqualEventKindMultisets_DetectsRealDivergence is the audit's own
// required negative-fixture proof: an extra event outside the allow-list,
// a dropped event, and a duplicate must all be detected as real
// differences. A pure reordering of otherwise-identical kinds must NOT be
// flagged — see this file's own real-world case: Codex reports its tool
// call operationally before a separate summary message, Claude bundles
// them into one message first, and that is a legitimate provider
// difference this comparison deliberately tolerates (spk11_scenario.go's
// own call-site comment explains why a strict total order was tried and
// rejected).
func TestEqualEventKindMultisets_DetectsRealDivergence(t *testing.T) {
	base := []ports.AgentEventKind{
		ports.AgentEventExecutionStarted, ports.AgentEventAssistantMessage, ports.AgentEventToolCallStarted,
		ports.AgentEventToolCallFinished, ports.AgentEventExecutionFinished,
	}

	t.Run("identical sequences match", func(t *testing.T) {
		if !equalEventKindMultisets(base, append([]ports.AgentEventKind(nil), base...)) {
			t.Fatal("identical sequences reported as different")
		}
	})

	t.Run("a pure reordering of the same kinds still matches", func(t *testing.T) {
		reordered := []ports.AgentEventKind{
			ports.AgentEventExecutionStarted, ports.AgentEventToolCallStarted, ports.AgentEventToolCallFinished,
			ports.AgentEventAssistantMessage, ports.AgentEventExecutionFinished,
		}
		if !equalEventKindMultisets(base, reordered) {
			t.Fatal("a legitimate reordering (same kinds, same counts) was flagged as a divergence")
		}
	})

	t.Run("extra event outside allowlist is detected", func(t *testing.T) {
		withExtra := append(append([]ports.AgentEventKind(nil), base...), ports.AgentEventDiagnostic)
		if equalEventKindMultisets(base, withExtra) {
			t.Fatal("an unexplained extra event was not detected as a divergence")
		}
	})

	t.Run("dropped event is detected", func(t *testing.T) {
		dropped := base[:len(base)-1]
		if equalEventKindMultisets(base, dropped) {
			t.Fatal("a dropped event was not detected as a divergence")
		}
	})

	t.Run("same length but different kind counts is detected", func(t *testing.T) {
		// Same total length as base, but ASSISTANT_MESSAGE appears twice and
		// TOOL_CALL_FINISHED not at all — exactly the case a naive
		// len(a)==len(b) check alone would miss.
		wrongCounts := []ports.AgentEventKind{
			ports.AgentEventExecutionStarted, ports.AgentEventAssistantMessage, ports.AgentEventToolCallStarted,
			ports.AgentEventAssistantMessage, ports.AgentEventExecutionFinished,
		}
		if equalEventKindMultisets(base, wrongCounts) {
			t.Fatal("a same-length sequence with different per-kind counts was not detected as a divergence")
		}
	})
}

func TestFirstLastEventKindIndex(t *testing.T) {
	sequence := []ports.AgentEventKind{
		ports.AgentEventExecutionStarted, ports.AgentEventToolCallStarted, ports.AgentEventToolCallFinished, ports.AgentEventExecutionFinished,
	}
	if got := firstEventKind(sequence); got != ports.AgentEventExecutionStarted {
		t.Fatalf("firstEventKind = %s, want EXECUTION_STARTED", got)
	}
	if got := lastEventKind(sequence); got != ports.AgentEventExecutionFinished {
		t.Fatalf("lastEventKind = %s, want EXECUTION_FINISHED", got)
	}
	if got := eventKindIndex(sequence, ports.AgentEventToolCallStarted); got != 1 {
		t.Fatalf("eventKindIndex(TOOL_CALL_STARTED) = %d, want 1", got)
	}
	if got := eventKindIndex(sequence, ports.AgentEventToolCallFinished); got != 2 {
		t.Fatalf("eventKindIndex(TOOL_CALL_FINISHED) = %d, want 2", got)
	}
	if got := eventKindIndex(sequence, ports.AgentEventDiagnostic); got != -1 {
		t.Fatalf("eventKindIndex(absent kind) = %d, want -1", got)
	}
	if got := firstEventKind(nil); got != "" {
		t.Fatalf("firstEventKind(nil) = %q, want empty", got)
	}
	if got := lastEventKind(nil); got != "" {
		t.Fatalf("lastEventKind(nil) = %q, want empty", got)
	}
}
