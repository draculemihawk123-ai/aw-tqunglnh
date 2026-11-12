package ports

import (
	"context"
	"time"
)

// QueueState is the state of a WorkItem's place in its family's queue (V12-19, ADR-054).
type QueueState string

const (
	// QueueQueued waits for its turn.
	QueueQueued QueueState = "QUEUED"
	// QueueStarted: the queue started a run for the WorkItem (RunID).
	QueueStarted QueueState = "STARTED"
	// QueueRemoved: an operator took the WorkItem out of the queue, or it was cancelled or done without the queue.
	QueueRemoved QueueState = "REMOVED"
)

// QueueEntry is one WorkItem's place in its family's queue. A family shares one worktree, so its queue runs one task at a time:
// the QUEUED entry with the lowest Position starts when the task the queue started before it is DONE and no run of the family is
// active, and when WaitsForWorkItemID (if set) is DONE.
type QueueEntry struct {
	WorkItemID         string
	ProjectID          string
	FamilyID           string
	Position           int
	WaitsForWorkItemID string
	State              QueueState
	RunID              string
	// Problem is why the entry did not start on the last try (empty once it starts).
	Problem   string
	Version   uint64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// QueueRepository is the Tx accessor of the family queues.
type QueueRepository interface {
	// PutQueueEntry inserts entry (Version 1), or, when the WorkItem already has an entry, replaces it when its Version is
	// expectedVersion (ErrOptimisticConflict otherwise). It returns the stored entry.
	PutQueueEntry(ctx context.Context, entry QueueEntry, expectedVersion uint64) (QueueEntry, error)
	// GetQueueEntry returns the WorkItem's entry, or ErrPersistenceNotFound.
	GetQueueEntry(ctx context.Context, workItemID string) (QueueEntry, error)
	// ListQueueEntriesForFamily returns the family's entries in every state, by Position then CreatedAt.
	ListQueueEntriesForFamily(ctx context.Context, familyID string) ([]QueueEntry, error)
	// ListFamiliesWithQueuedEntries returns the ids of the families that have a QUEUED entry.
	ListFamiliesWithQueuedEntries(ctx context.Context) ([]string, error)
}
