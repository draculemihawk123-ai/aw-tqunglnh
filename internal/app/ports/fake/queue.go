package fake

import (
	"context"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// QueueRepository is the in-memory ports.QueueRepository (V12-19).
type QueueRepository struct {
	entries map[string]ports.QueueEntry
}

func (r *QueueRepository) clone() *QueueRepository {
	entries := make(map[string]ports.QueueEntry, len(r.entries))
	for k, v := range r.entries {
		entries[k] = v
	}
	return &QueueRepository{entries: entries}
}

// PutQueueEntry mirrors the SQLite repository.
func (r *QueueRepository) PutQueueEntry(_ context.Context, entry ports.QueueEntry, expectedVersion uint64) (ports.QueueEntry, error) {
	if r.entries == nil {
		r.entries = map[string]ports.QueueEntry{}
	}
	existing, ok := r.entries[entry.WorkItemID]
	switch {
	case !ok && expectedVersion != 0:
		return ports.QueueEntry{}, fmt.Errorf("fake: %w: queue entry %s", ports.ErrPersistenceNotFound, entry.WorkItemID)
	case ok && existing.Version != expectedVersion:
		return ports.QueueEntry{}, fmt.Errorf("fake: %w: queue entry %s", ports.ErrOptimisticConflict, entry.WorkItemID)
	}
	if ok {
		entry.CreatedAt = existing.CreatedAt
		entry.Version = existing.Version + 1
	} else {
		entry.Version = 1
	}
	r.entries[entry.WorkItemID] = entry
	return entry, nil
}

// GetQueueEntry mirrors the SQLite repository.
func (r *QueueRepository) GetQueueEntry(_ context.Context, workItemID string) (ports.QueueEntry, error) {
	e, ok := r.entries[workItemID]
	if !ok {
		return ports.QueueEntry{}, fmt.Errorf("fake: %w: queue entry %s", ports.ErrPersistenceNotFound, workItemID)
	}
	return e, nil
}

// ListQueueEntriesForFamily mirrors the SQLite repository.
func (r *QueueRepository) ListQueueEntriesForFamily(_ context.Context, familyID string) ([]ports.QueueEntry, error) {
	var out []ports.QueueEntry
	for _, e := range r.entries {
		if e.FamilyID == familyID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// ListFamiliesWithQueuedEntries mirrors the SQLite repository.
func (r *QueueRepository) ListFamiliesWithQueuedEntries(_ context.Context) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, e := range r.entries {
		if e.State == ports.QueueQueued && !seen[e.FamilyID] {
			seen[e.FamilyID] = true
			out = append(out, e.FamilyID)
		}
	}
	sort.Strings(out)
	return out, nil
}
