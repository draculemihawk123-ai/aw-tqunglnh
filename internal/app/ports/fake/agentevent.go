package fake

import (
	"context"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// AgentEventsRepository is an in-memory ports.AgentEventsRepository — V5-08A
// gives this concern real behavior from the start (the same treatment
// AdapterBuildRepository/Readiness/Wait/Approvals/Artifacts above already
// received), so an application-layer test of agentevents.Sink never needs
// sqlite. AppendBatch rejects a duplicate (attempt_id, sequence) the same
// way the sqlite adapter's UNIQUE constraint does — see
// sqlite.agentEventsRepository's own doc comment for why this fake mirrors
// "reject", not "silently accept", on conflict.
type AgentEventsRepository struct {
	records map[string][]ports.AgentEventRecord // by AttemptID, oldest-Sequence-first
}

var _ ports.AgentEventsRepository = (*AgentEventsRepository)(nil)

func (a *AgentEventsRepository) clone() *AgentEventsRepository {
	records := make(map[string][]ports.AgentEventRecord, len(a.records))
	for k, v := range a.records {
		records[k] = append([]ports.AgentEventRecord(nil), v...)
	}
	return &AgentEventsRepository{records: records}
}

func (a *AgentEventsRepository) AppendBatch(_ context.Context, batch []ports.AgentEventRecord) error {
	for _, record := range batch {
		for _, existing := range a.records[record.AttemptID] {
			if existing.Sequence == record.Sequence {
				return fmt.Errorf("fake: duplicate agent event (attempt_id=%q sequence=%d)", record.AttemptID, record.Sequence)
			}
		}
		if a.records == nil {
			a.records = map[string][]ports.AgentEventRecord{}
		}
		a.records[record.AttemptID] = append(a.records[record.AttemptID], record)
	}
	return nil
}

func (a *AgentEventsRepository) ListByAttempt(_ context.Context, attemptID string) ([]ports.AgentEventRecord, error) {
	records := append([]ports.AgentEventRecord(nil), a.records[attemptID]...)
	sort.Slice(records, func(i, j int) bool { return records[i].Sequence < records[j].Sequence })
	return records, nil
}
