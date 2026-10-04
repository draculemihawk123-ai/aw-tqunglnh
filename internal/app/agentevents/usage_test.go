package agentevents

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func usageRecord(t *testing.T, sequence uint64, kind ports.AgentEventKind, usage *ports.AgentUsage) ports.AgentEventRecord {
	t.Helper()
	payload, err := json.Marshal(Payload{ObservedAt: time.Now().UTC(), Usage: usage})
	if err != nil {
		t.Fatal(err)
	}
	return ports.AgentEventRecord{AttemptID: "a-1", Sequence: sequence, Kind: string(kind), SchemaVersion: 1, PayloadJSON: string(payload)}
}

func TestUsageFromRecords_SumsEveryUsageReport(t *testing.T) {
	records := []ports.AgentEventRecord{
		usageRecord(t, 1, ports.AgentEventAssistantMessage, nil),
		usageRecord(t, 2, ports.AgentEventUsageReported, &ports.AgentUsage{InputTokens: 100, CachedInputTokens: 40, OutputTokens: 10, CostUSD: 0.25}),
		usageRecord(t, 3, ports.AgentEventUsageReported, &ports.AgentUsage{InputTokens: 5, OutputTokens: 1, CostUSD: 0.5}),
	}
	got, ok := UsageFromRecords(records)
	if !ok {
		t.Fatal("no usage found in a stream with two reports")
	}
	want := ports.AgentUsage{InputTokens: 105, CachedInputTokens: 40, OutputTokens: 11, CostUSD: 0.75}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}

func TestUsageFromRecords_NoReportIsNotZeroSpend(t *testing.T) {
	if _, ok := UsageFromRecords(nil); ok {
		t.Fatal("an empty stream reported usage")
	}
	records := []ports.AgentEventRecord{usageRecord(t, 1, ports.AgentEventAssistantMessage, nil)}
	if _, ok := UsageFromRecords(records); ok {
		t.Fatal("a stream without a USAGE_REPORTED event reported usage")
	}
	// A report of zero is a report: the CLI said it used nothing.
	zero := []ports.AgentEventRecord{usageRecord(t, 1, ports.AgentEventUsageReported, &ports.AgentUsage{})}
	if got, ok := UsageFromRecords(zero); !ok || got != (ports.AgentUsage{}) {
		t.Fatalf("a zero report = %+v, %v; want zero usage that is known", got, ok)
	}
	// A damaged payload is skipped, not fatal.
	damaged := []ports.AgentEventRecord{{Kind: string(ports.AgentEventUsageReported), PayloadJSON: "{not json"}}
	if _, ok := UsageFromRecords(damaged); ok {
		t.Fatal("a damaged payload counted as a report")
	}
}
