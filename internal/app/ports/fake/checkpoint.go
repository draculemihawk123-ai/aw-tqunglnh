package fake

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// CheckpointsRepository is an in-memory ports.CheckpointsRepository (V5-08B)
// — the same "gets a real interface from the start" treatment
// AgentEventsRepository above already received, so an application-layer
// test of FinalizeExecutionAttempt's own completion checkpoint never needs
// sqlite. InsertCheckpoint rejects a duplicate (attempt_id, sequence) the
// same way the sqlite adapter's UNIQUE constraint does — see
// sqlite.checkpointsRepository's own doc comment for why a conflict here
// is a real bug, never a legitimate race to dedup.
type CheckpointsRepository struct {
	records map[string][]runtime.Checkpoint // by AttemptID
}

var _ ports.CheckpointsRepository = (*CheckpointsRepository)(nil)

func (c *CheckpointsRepository) clone() *CheckpointsRepository {
	records := make(map[string][]runtime.Checkpoint, len(c.records))
	for k, v := range c.records {
		records[k] = append([]runtime.Checkpoint(nil), v...)
	}
	return &CheckpointsRepository{records: records}
}

func (c *CheckpointsRepository) InsertCheckpoint(_ context.Context, checkpoint runtime.Checkpoint) error {
	attemptID := string(checkpoint.AttemptID)
	for _, existing := range c.records[attemptID] {
		if existing.Sequence == checkpoint.Sequence {
			return fmt.Errorf("fake: duplicate checkpoint (attempt_id=%q sequence=%d)", attemptID, checkpoint.Sequence)
		}
	}
	if c.records == nil {
		c.records = map[string][]runtime.Checkpoint{}
	}
	c.records[attemptID] = append(c.records[attemptID], checkpoint)
	return nil
}
