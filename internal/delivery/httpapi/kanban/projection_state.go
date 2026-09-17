package kanban

import (
	"context"
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// resolveProjectionState returns the currently active generation for
// (projectID, projection.ProjectionName) plus the shared httpapi.Freshness
// envelope every response in this package reports. ports.ProjectionStatus's
// own three values (LIVE/DEGRADED/STALE, ports/projection.go) are the
// IDENTICAL wire vocabulary httpapi.FreshnessStatus already freezes
// (freshness.go) — a direct string cast, never a translation table that
// could silently drift.
//
// Two honestly-reported-as-STALE edge cases, both real and both expected
// during this task's own early-lifecycle testing (never treated as an
// error the caller must special-case):
//
//   - GetActiveGeneration ok=false: no generation has EVER been created for
//     this project's own "workitem" projection (V6-08A has never run against
//     it, or the project has no rows of this projection at all yet).
//   - GetProjectionCheckpoint returns ports.ErrPersistenceNotFound: a
//     generation was created (EnsureGeneration) but the live consumer has
//     never yet written its own first checkpoint for it
//     (AcquireOrRenewConsumerLease's own "no checkpoint row exists yet"
//     first-call path, V6-08A, not yet exercised for this project).
//
// Both report Generation as whatever is actually active (0 for the first
// case), AsOfJournalPosition 0, Status STALE — never fabricated as LIVE,
// since claiming a healthy projection when no consumer has ever confirmed
// one would be exactly the kind of false authority this task's own "Hoàn
// thành khi" line forbids.
func resolveProjectionState(ctx context.Context, tx ports.Tx, projectID string) (uint64, httpapi.Freshness, error) {
	generation, ok, err := tx.Projections().GetActiveGeneration(ctx, projectID, projection.ProjectionName)
	if err != nil {
		return 0, httpapi.Freshness{}, err
	}
	if !ok {
		return 0, httpapi.Freshness{Generation: 0, AsOfJournalPosition: 0, Status: httpapi.FreshnessStale}, nil
	}
	checkpoint, err := tx.Projections().GetProjectionCheckpoint(ctx, projectID, projection.ProjectionName, generation)
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return generation, httpapi.Freshness{Generation: int(generation), AsOfJournalPosition: 0, Status: httpapi.FreshnessStale}, nil
		}
		return 0, httpapi.Freshness{}, err
	}
	return generation, httpapi.Freshness{
		Generation: int(generation), AsOfJournalPosition: int64(checkpoint.Cursor), Status: httpapi.FreshnessStatus(checkpoint.Status),
	}, nil
}
