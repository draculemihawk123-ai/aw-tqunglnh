package agentevents

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// UsageFromRecords (V9-13a, finding F4) totals the USAGE_REPORTED events of one
// attempt's stream. The adapter normalizes what the provider CLI reported (for
// Claude, the final result's token counts and total_cost_usd) into one such
// event per report, so an attempt that reported twice is the sum of both. The
// second result is false when the stream has no usage at all — an attempt that
// never reached the provider, or one recorded before the CLI reported — so a
// caller can tell "spent nothing" from "not known".
//
// The figures are the provider's own, not aw's accounting: they are as accurate
// as the CLI, and a CLI that reports no cost reports a zero one.
func UsageFromRecords(records []ports.AgentEventRecord) (ports.AgentUsage, bool) {
	var total ports.AgentUsage
	found := false
	for _, record := range records {
		if record.Kind != string(ports.AgentEventUsageReported) {
			continue
		}
		var payload Payload
		if err := json.Unmarshal([]byte(record.PayloadJSON), &payload); err != nil || payload.Usage == nil {
			continue
		}
		found = true
		total.InputTokens += payload.Usage.InputTokens
		total.CachedInputTokens += payload.Usage.CachedInputTokens
		total.OutputTokens += payload.Usage.OutputTokens
		total.CostUSD += payload.Usage.CostUSD
	}
	return total, found
}
