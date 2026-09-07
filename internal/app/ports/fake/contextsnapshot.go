package fake

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
)

// ContextSnapshotRepository is an in-memory ports.ContextSnapshotRepository
// — V5-04 gives this concern real behavior from the start (the same
// treatment ArtifactRepository/MessageRepository above already received),
// so an application-layer test of the scheduling/dispatch flow never
// needs sqlite. runtime is populated now, mirroring MessageRepository's
// own runtime field: CreateSnapshot's own "AttemptID must name a row that
// exists" check needs to read RuntimeRepository's own attempts map.
type ContextSnapshotRepository struct {
	runtime *RuntimeRepository

	snapshots         map[string]contextsnapshot.Snapshot // by ID
	snapshotByAttempt map[string]string                   // AttemptID -> ID
}

var _ ports.ContextSnapshotRepository = (*ContextSnapshotRepository)(nil)

func (c *ContextSnapshotRepository) cloneWith(runtime *RuntimeRepository) *ContextSnapshotRepository {
	snapshots := make(map[string]contextsnapshot.Snapshot, len(c.snapshots))
	for k, v := range c.snapshots {
		snapshots[k] = v
	}
	byAttempt := make(map[string]string, len(c.snapshotByAttempt))
	for k, v := range c.snapshotByAttempt {
		byAttempt[k] = v
	}
	return &ContextSnapshotRepository{runtime: runtime, snapshots: snapshots, snapshotByAttempt: byAttempt}
}

// CreateSnapshot mirrors sqlite's createSnapshotTx: snapshot.AttemptID
// must name an ExecutionAttempt this fake's own RuntimeRepository already
// has, and a duplicate ID returns the already-stored row.
func (c *ContextSnapshotRepository) CreateSnapshot(_ context.Context, snapshot contextsnapshot.Snapshot) (contextsnapshot.Snapshot, error) {
	if existing, ok := c.snapshots[string(snapshot.ID)]; ok {
		return existing, nil
	}
	if _, ok := c.runtime.attempts[string(snapshot.AttemptID)]; !ok {
		return contextsnapshot.Snapshot{}, fmt.Errorf("fake: %w: execution attempt %s", ports.ErrPersistenceNotFound, snapshot.AttemptID)
	}
	if c.snapshots == nil {
		c.snapshots = map[string]contextsnapshot.Snapshot{}
	}
	if c.snapshotByAttempt == nil {
		c.snapshotByAttempt = map[string]string{}
	}
	c.snapshots[string(snapshot.ID)] = snapshot
	c.snapshotByAttempt[string(snapshot.AttemptID)] = string(snapshot.ID)
	return snapshot, nil
}

func (c *ContextSnapshotRepository) GetSnapshot(_ context.Context, id string) (contextsnapshot.Snapshot, error) {
	snapshot, ok := c.snapshots[id]
	if !ok {
		return contextsnapshot.Snapshot{}, fmt.Errorf("fake: %w: context snapshot %s", ports.ErrPersistenceNotFound, id)
	}
	return snapshot, nil
}

func (c *ContextSnapshotRepository) GetSnapshotByAttemptID(_ context.Context, attemptID string) (contextsnapshot.Snapshot, error) {
	id, ok := c.snapshotByAttempt[attemptID]
	if !ok {
		return contextsnapshot.Snapshot{}, fmt.Errorf("fake: %w: context snapshot for attempt %s", ports.ErrPersistenceNotFound, attemptID)
	}
	return c.snapshots[id], nil
}

// DeleteSnapshot removes id (and its own attempt binding, if any) —
// test-only, standing in for a "row went missing" scenario a real
// database can otherwise only reach via direct, out-of-band tampering
// (mirroring JobsRepository.SetActiveLease's own "no equivalent public
// mutation exists" precedent). Production code must never call this.
func (c *ContextSnapshotRepository) DeleteSnapshot(id string) {
	snapshot, ok := c.snapshots[id]
	if !ok {
		return
	}
	delete(c.snapshots, id)
	if c.snapshotByAttempt[string(snapshot.AttemptID)] == id {
		delete(c.snapshotByAttempt, string(snapshot.AttemptID))
	}
}

// Overwrite replaces the stored row at snapshot.ID with snapshot exactly
// as given, bypassing every check CreateSnapshot normally applies —
// test-only, for simulating a corrupted/mismatched row directly (e.g. one
// bound to a different AttemptID than the caller expects) without
// reaching into this struct's own unexported maps from another package.
func (c *ContextSnapshotRepository) Overwrite(snapshot contextsnapshot.Snapshot) {
	if c.snapshots == nil {
		c.snapshots = map[string]contextsnapshot.Snapshot{}
	}
	if c.snapshotByAttempt == nil {
		c.snapshotByAttempt = map[string]string{}
	}
	c.snapshots[string(snapshot.ID)] = snapshot
	c.snapshotByAttempt[string(snapshot.AttemptID)] = string(snapshot.ID)
}
