// ExecuteReleaseSetLocalCommit (this file) is this package's own "consumer"
// half — see commands.go's own package doc comment for the full producer/
// consumer split. It claims no job itself (Handler.Handle, handler.go, is
// the only caller, holding the job's own JobLease workerpool.Pool already
// claimed and is heartbeating); every step below runs either as a plain,
// read-only DB read, a real (never mocked) Git call entirely outside any
// transaction, or inside its own short, fenced ports.UnitOfWork transaction
// — never a Git/filesystem call composed inside a transaction
// (docs/architecture/04-go-core-spec.md §11.1).
//
// # Two leases, both revalidated, both required
//
// The job's own JobLease ("worker lease" — V6-10E's own Thực hiện line)
// proves this worker instance still owns processing this job; a SEPARATE
// LocalCommitWriteLeaseGrant ("write lease") proves this worker instance
// still has exclusive real-Git-mutation rights over the target
// RepositoryWorkspace's own current generation. Both are re-validated
// immediately before the real, mutating Git call (AcquireLocalCommitWriteLease
// itself re-checks the JobLease is still LEASED) and both are re-validated
// again, fresh, inside the finalize transaction below (ValidateActiveJob +
// ValidateLocalCommitWriteLeaseFencing) — never trusted merely because they
// were valid earlier in this same function call.
//
// # Crash-recovery reconciliation
//
// Before ever calling LocalCommitCreator, this function asks
// LocalCommitMarkerReader whether the target workspace's own CURRENT HEAD
// commit already carries this exact operation's own deterministic marker —
// true exactly when an earlier, crashed attempt of THIS SAME operation
// already ran the real Git commit but crashed before the finalize
// transaction below ever committed. When true, the earlier commit is
// reused outright (parent cross-checked against whatever this operation
// itself durably pinned before that earlier attempt's own real Git call —
// a mismatch is "drift", handled by quarantining the workspace rather than
// ever silently proceeding); LocalCommitCreator is never called a second
// time for the same marker. When false, this function durably pins the
// exact parent it is about to build on top of (PinReleaseSetLocalCommitParent)
// BEFORE the real Git call — so a LATER crash between that call succeeding
// and finalize committing leaves a future retry something durable to
// reconcile the reused commit's own parent against.
package releasesetcommit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ExecuteReleaseSetLocalCommitDeps bundles ExecuteReleaseSetLocalCommit's
// collaborators — the same UnitOfWork/IDs/port-not-adapter shape
// ExecuteWorkspaceSetReleaseDeps (internal/app/workspacerelease) already
// establishes for the sibling "outside-tx real I/O, fenced finalize" job.
type ExecuteReleaseSetLocalCommitDeps struct {
	UnitOfWork   ports.UnitOfWork
	IDs          idsource.Source
	WriteLeases  ports.LocalCommitWriteLeaseManager
	MarkerReader ports.LocalCommitMarkerReader
	Creator      ports.LocalCommitCreator
	Lifecycle    ports.WorkspaceLifecycle
	// WriteLeaseTTL bounds how long a local commit write lease is held
	// without this function itself renewing it — this operation's own real
	// Git call is bounded/synchronous within one Handle() invocation (never
	// spanning multiple heartbeat intervals the way a long AGENT attempt
	// does), so no separate heartbeat loop exists for it; TTL alone must
	// comfortably exceed how long the real Git call can ever take.
	WriteLeaseTTL time.Duration
}

func (d ExecuteReleaseSetLocalCommitDeps) validate() error {
	if d.UnitOfWork == nil || d.IDs == nil || d.WriteLeases == nil || d.MarkerReader == nil || d.Creator == nil || d.Lifecycle == nil {
		return errors.New("releasesetcommit: ExecuteReleaseSetLocalCommit requires UnitOfWork/IDs/WriteLeases/MarkerReader/Creator/Lifecycle")
	}
	if d.WriteLeaseTTL <= 0 {
		return errors.New("releasesetcommit: ExecuteReleaseSetLocalCommit requires a positive WriteLeaseTTL")
	}
	return nil
}

// ExecuteReleaseSetLocalCommit is this package's own internal worker
// entry point — see this file's own doc comment for the full contract.
// job must be a currently-LEASED DurableJob of ReleaseSetLocalCommitJobKind
// (Handler.Handle, this package's only caller, guarantees this).
func ExecuteReleaseSetLocalCommit(ctx context.Context, deps ExecuteReleaseSetLocalCommitDeps, job ports.DurableJob) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if job.LeaseUntil == nil {
		return errors.New("releasesetcommit: job has no active lease")
	}
	jobLease := ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken, LeaseUntil: *job.LeaseUntil}

	var payload localCommitJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("releasesetcommit: unmarshal job %s payload: %w", job.ID, err)
	}
	if payload.ReleaseSetLocalCommitID == "" {
		return fmt.Errorf("releasesetcommit: job %s payload is incomplete", job.ID)
	}

	intent, err := loadIntent(ctx, deps.UnitOfWork, payload.ReleaseSetLocalCommitID)
	if err != nil {
		return fmt.Errorf("releasesetcommit: load release set local commit %s: %w", payload.ReleaseSetLocalCommitID, err)
	}
	if intent.State != workdomain.ReleaseSetLocalCommitRequested {
		// Already terminal — a redelivered/duplicate job (replay). Nothing
		// more to do, and no second Git operation is ever attempted.
		return nil
	}

	grant, err := deps.WriteLeases.AcquireLocalCommitWriteLease(ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: jobLease,
		Target: ports.LocalCommitWriteLeaseTarget{
			RepositoryID: intent.RepositoryID, RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(intent.RepositoryWorkspaceID),
			Generation: intent.ExpectedGeneration,
		},
		TTL: deps.WriteLeaseTTL,
	})
	if err != nil {
		return fmt.Errorf("releasesetcommit: acquire local commit write lease for %s: %w", intent.RepositoryWorkspaceID, err)
	}
	releaseGrant := func() {
		_ = deps.WriteLeases.ReleaseLocalCommitWriteLease(context.WithoutCancel(ctx), grant)
	}

	current, err := loadRepositoryWorkspace(ctx, deps.UnitOfWork, string(intent.RepositoryWorkspaceID))
	if err != nil {
		releaseGrant()
		return fmt.Errorf("releasesetcommit: load repository workspace %s: %w", intent.RepositoryWorkspaceID, err)
	}

	// current.Workspace.Generation is never compared against
	// intent.ExpectedGeneration here: GetRepositoryWorkspaceByID looks up
	// the SAME row by its own fixed ID, and a RepositoryWorkspace row's own
	// Generation column is immutable for its lifetime (a RECREATE always
	// supersedes it with an entirely new row/ID, never edits this one) — so
	// the only real way this exact row can have become unusable since
	// request time is QUARANTINED, checked below (see
	// work.ReleaseSetLocalCommitFailureReason's own doc comment for the
	// full reasoning, including why "stale" is instead enforced at request
	// time).
	now := time.Now().UTC()
	switch current.Workspace.State {
	case workspace.RepositoryWorkspaceQuarantined:
		releaseGrant()
		return failTerminal(ctx, deps.UnitOfWork, jobLease, intent, workdomain.FailureWorkspaceQuarantined, now)
	case workspace.RepositoryWorkspaceReady:
		// proceed
	default:
		releaseGrant()
		return fmt.Errorf("releasesetcommit: repository workspace %s is %s, not eligible for local commit execution",
			intent.RepositoryWorkspaceID, current.Workspace.State)
	}

	handle, err := ports.NewWorkspaceHandle(current.Workspace.Locator)
	if err != nil {
		releaseGrant()
		return fmt.Errorf("releasesetcommit: repository workspace %s has an invalid locator: %w", intent.RepositoryWorkspaceID, err)
	}

	found, headRevision, headParentVCSObjectID, err := deps.MarkerReader.FindLocalCommitByMarker(ctx, handle, intent.Marker)
	if err != nil {
		releaseGrant()
		return fmt.Errorf("releasesetcommit: find local commit by marker for %s: %w", intent.RepositoryWorkspaceID, err)
	}

	var parentVCSObjectID, resultVCSObjectID string
	if found {
		if intent.ParentVCSObjectID != "" && intent.ParentVCSObjectID != headParentVCSObjectID {
			releaseGrant()
			reason := fmt.Sprintf(
				"release set local commit %s: marker %s found at workspace HEAD %s but its own parent %s does not match the pinned parent %s",
				intent.ID, intent.Marker, headRevision.VCSObjectID, headParentVCSObjectID, intent.ParentVCSObjectID)
			if quarantineErr := quarantineWorkspace(ctx, deps, current.Workspace, jobLease, reason, now); quarantineErr != nil {
				return quarantineErr
			}
			return failTerminal(ctx, deps.UnitOfWork, jobLease, intent, workdomain.FailureMarkerDrift, now)
		}
		parentVCSObjectID = headParentVCSObjectID
		if parentVCSObjectID == "" {
			parentVCSObjectID = intent.ParentVCSObjectID
		}
		resultVCSObjectID = headRevision.VCSObjectID
	} else {
		parentVCSObjectID = headRevision.VCSObjectID
		pinned, pinErr := pinParent(ctx, deps.UnitOfWork, intent, parentVCSObjectID)
		if pinErr != nil {
			releaseGrant()
			return fmt.Errorf("releasesetcommit: pin release set local commit %s parent: %w", intent.ID, pinErr)
		}
		intent = pinned

		message := commitMessageWithMarker(intent.Message, intent.Marker)
		revision, createErr := deps.Creator.CreateLocalCommit(ctx, ports.CreateLocalCommitRequest{
			Handle: handle, Message: message, AuthorName: intent.AuthorName, AuthorEmail: intent.AuthorEmail,
		})
		if createErr != nil {
			releaseGrant()
			return fmt.Errorf("releasesetcommit: create local commit for %s: %w", intent.RepositoryWorkspaceID, createErr)
		}
		resultVCSObjectID = revision.VCSObjectID
	}

	finalizeErr := deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if err := tx.Jobs().ValidateActiveJob(ctx, jobLease, aggregateType, string(intent.ID)); err != nil {
			return err
		}
		if err := tx.Work().ValidateLocalCommitWriteLeaseFencing(ctx, jobLease, grant); err != nil {
			return err
		}
		if _, err := tx.Work().TransitionReleaseSetLocalCommitToCommitted(ctx, ports.TransitionReleaseSetLocalCommitToCommittedRequest{
			ReleaseSetLocalCommitID: string(intent.ID), ExpectedVersion: intent.Version,
			ParentVCSObjectID: parentVCSObjectID, ResultVCSObjectID: resultVCSObjectID, OccurredAt: now,
		}); err != nil {
			return err
		}
		eventPayload, err := json.Marshal(releaseSetLocalCommitCommittedEventPayload{
			ReleaseSetLocalCommitID: string(intent.ID), ParentVCSObjectID: parentVCSObjectID,
			ResultVCSObjectID: resultVCSObjectID, JobID: string(jobLease.JobID),
		})
		if err != nil {
			return fmt.Errorf("marshal %s event payload: %w", ReleaseSetLocalCommitCommittedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: string(intent.ID) + "-committed", ProjectID: string(intent.ProjectID),
			AggregateType: aggregateType, AggregateID: string(intent.ID), Sequence: 2,
			EventType: ReleaseSetLocalCommitCommittedEventType, SchemaVersion: ReleaseSetLocalCommitCommittedSchemaVersion,
			PayloadJSON: string(eventPayload), CreatedAt: now,
		}); err != nil {
			return err
		}
		return tx.Jobs().CompleteJob(ctx, jobLease)
	})
	if finalizeErr != nil {
		// Fence lost (job lease or local-commit write lease) or some other
		// failure — this operation must NOT finalize (V6-10E's own "lease
		// loss" Verify case). The write lease is deliberately left held,
		// never released here: releasing it now, after the real commit
		// already exists but before any durable record says so, would let
		// a second worker believe it is free to acquire and could then
		// reconcile-or-create against a workspace whose own HEAD this
		// worker has already (successfully or not) mutated without ever
		// recording the outcome. A future retry (this worker's own job
		// lease expiring and being reclaimed, or the write lease's own TTL
		// lapsing) starts over from the top of this function and correctly
		// reconciles whatever real Git state already exists via
		// FindLocalCommitByMarker.
		return fmt.Errorf("releasesetcommit: finalize release set local commit %s: %w", intent.ID, finalizeErr)
	}

	// Release AFTER the terminal commit, never before or inside the
	// transaction above — V6-10E's own "write lease releases only after
	// terminal commit, idempotent/fence-aware", mirroring
	// FinalizeExecutionAttempt's identical release-after-commit ordering
	// (internal/app/runtime/finalize.go). ErrLocalCommitWriteLeaseLost here
	// means the desired end state (this operation no longer holds the
	// lease) already holds true some other way — never a reason to fail a
	// finalize that itself already committed successfully.
	if releaseErr := deps.WriteLeases.ReleaseLocalCommitWriteLease(ctx, grant); releaseErr != nil && !errors.Is(releaseErr, ports.ErrLocalCommitWriteLeaseLost) {
		return fmt.Errorf("releasesetcommit: release local commit write lease for %s: %w", intent.RepositoryWorkspaceID, releaseErr)
	}
	return nil
}

func loadIntent(ctx context.Context, uow ports.UnitOfWork, id string) (workdomain.ReleaseSetLocalCommit, error) {
	var intent workdomain.ReleaseSetLocalCommit
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.Work().GetReleaseSetLocalCommit(ctx, id)
		intent = loaded
		return err
	})
	return intent, err
}

func loadRepositoryWorkspace(ctx context.Context, uow ports.UnitOfWork, id string) (ports.RepositoryWorkspaceRecord, error) {
	var record ports.RepositoryWorkspaceRecord
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.Work().GetRepositoryWorkspaceByID(ctx, id)
		record = loaded
		return err
	})
	return record, err
}

// pinParent durably records parentVCSObjectID on intent (still REQUESTED)
// in its own short transaction — see ports.WorkRepository.
// PinReleaseSetLocalCommitParent's own doc comment for why this exists.
func pinParent(ctx context.Context, uow ports.UnitOfWork, intent workdomain.ReleaseSetLocalCommit, parentVCSObjectID string) (workdomain.ReleaseSetLocalCommit, error) {
	var updated workdomain.ReleaseSetLocalCommit
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		result, err := tx.Work().PinReleaseSetLocalCommitParent(ctx, ports.PinReleaseSetLocalCommitParentRequest{
			ReleaseSetLocalCommitID: string(intent.ID), ExpectedVersion: intent.Version, ParentVCSObjectID: parentVCSObjectID,
		})
		updated = result
		return err
	})
	return updated, err
}

func failTerminal(
	ctx context.Context, uow ports.UnitOfWork, jobLease ports.JobLease, intent workdomain.ReleaseSetLocalCommit,
	reason workdomain.ReleaseSetLocalCommitFailureReason, now time.Time,
) error {
	return uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if err := tx.Jobs().ValidateActiveJob(ctx, jobLease, aggregateType, string(intent.ID)); err != nil {
			return err
		}
		if _, err := tx.Work().TransitionReleaseSetLocalCommitToFailed(ctx, ports.TransitionReleaseSetLocalCommitToFailedRequest{
			ReleaseSetLocalCommitID: string(intent.ID), ExpectedVersion: intent.Version, FailureReason: reason, OccurredAt: now,
		}); err != nil {
			return err
		}
		payload, err := json.Marshal(releaseSetLocalCommitFailedEventPayload{
			ReleaseSetLocalCommitID: string(intent.ID), FailureReason: string(reason), JobID: string(jobLease.JobID),
		})
		if err != nil {
			return fmt.Errorf("marshal %s event payload: %w", ReleaseSetLocalCommitFailedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: string(intent.ID) + "-failed", ProjectID: string(intent.ProjectID),
			AggregateType: aggregateType, AggregateID: string(intent.ID), Sequence: 2,
			EventType: ReleaseSetLocalCommitFailedEventType, SchemaVersion: ReleaseSetLocalCommitFailedSchemaVersion,
			PayloadJSON: string(payload), CreatedAt: now,
		}); err != nil {
			return err
		}
		return tx.Jobs().CompleteJob(ctx, jobLease)
	})
}

// quarantineWorkspace calls WorkspaceLifecycle.QuarantineRepositoryWorkspace
// for target — V6-10E's own "mismatch blocks/quarantines" Verify case.
// ports.ErrWorkspaceQuarantined (the real adapter's own "already
// QUARANTINED" outcome) is tolerated as an idempotent no-op: whatever
// concurrently quarantined it first already achieved the safety property
// this call itself wants.
func quarantineWorkspace(
	ctx context.Context, deps ExecuteReleaseSetLocalCommitDeps, target workspace.RepositoryWorkspace,
	jobLease ports.JobLease, reason string, now time.Time,
) error {
	err := deps.Lifecycle.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: target.ID, ExpectedVersion: target.Version, Reason: reason,
		EventID: deps.IDs.NewID(), CorrelationID: string(jobLease.JobID), OccurredAt: now,
	})
	if err != nil && !errors.Is(err, ports.ErrWorkspaceQuarantined) {
		return fmt.Errorf("releasesetcommit: quarantine repository workspace %s: %w", target.ID, err)
	}
	return nil
}
