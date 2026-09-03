// Package workspacerelease is V3-11's public/internal split for releasing
// an entire WorkspaceSet (docs/design/05-v3-project-workspace.md V3-11,
// AK-ARCH-015C, GC-INV-26): "cung cấp primitive release idempotent chỉ khi
// không còn active attempt/job/lease, quarantine và caller đưa
// authorization ReleaseSet sealed|abandoned hợp lệ" — check eligibility
// against real, already-persisted state, write a durable intent, and
// enqueue exactly one control job. It never touches a repository's real
// filesystem or Git state itself.
//
// # Scope (V3-11's own Phạm vi line, read literally)
//
// V3-11 owns exactly one thing: the public RequestWorkspaceSetRelease
// command below — eligibility check, intent write, job enqueue. Nothing
// else in the release lifecycle is this package's job:
//
//   - ExecuteWorkspaceSetRelease — the internal handler that actually
//     inspects/releases each repository's real physical workspace via
//     ports.WorkspaceProvider/ports.WorkspaceLifecycle, records per-repo
//     partial results, retries after a crash, and retains metadata/
//     artifacts — belongs to V5-14. It does not exist in this codebase and
//     this package never imports anything that could reach it (see this
//     package's own TestRequestWorkspaceSetReleaseNeverImportsWorkspaceIO
//     in internal/archtest/boundary_test.go, mirroring
//     internal/app/workspacereconcile's identical
//     TestRequestWorkspaceReconciliationNeverImportsWorkspaceIO).
//   - The API route that exposes this command belongs to V6-10B.
//   - Surfacing "release" as a valid, UI-visible action belongs to V7-13A.
//
// The WORKSPACE_SET_RELEASE job this package enqueues is consumed by
// nobody in this codebase yet — exactly the same "enqueue only, a
// documented future task is the sole consumer" relationship
// catalog.RegisterRepository (REPOSITORY_PROBE, consumed by V3-02),
// work.CreateRootWorkItem (WORKSPACE_PROVISION, consumed by
// internal/app/workspaceprovision) and
// workspacereconcile.RequestWorkspaceReconciliation
// (WORKSPACE_RECONCILIATION, consumed by this same package's own
// handler.go) already established for their own aggregates before their
// own consumer existed. The job's own payload (releaseJobPayload below)
// enumerates every RepositoryWorkspace in the set at request time — not
// because this package does anything with that list itself, but so
// V5-14's own executor has what it needs to produce a real per-repo
// partial result later, per this task's own Thực hiện line.
//
// # Eligibility (V3-11's own Mục tiêu line: three checks, all real today)
//
//  1. No active lease: ports.WorkRepository.HasActiveWriteLease reads
//     write_leases directly (V3-09's own real, persisted table) — a live,
//     un-expired write lease on any RepositoryWorkspace in the set blocks
//     the request.
//  2. No active job: ports.JobsRepository.HasActiveJobForAggregateIDs
//     reads durable_jobs directly — any non-terminal job whose own
//     AggregateID names the WorkspaceSet itself or one of its
//     RepositoryWorkspaces blocks the request. This structurally also
//     rejects a WorkspaceSet still mid-PROVISIONING (its own still-open
//     WORKSPACE_PROVISION job, internal/app/work.WorkspaceProvisionJobKind,
//     names the WorkspaceSet as AggregateID) or one with an open
//     reconciliation (WORKSPACE_RECONCILIATION, AggregateID = the
//     RepositoryWorkspace) — no separate WorkspaceSetState allow-list is
//     needed for either case.
//  3. No quarantine: any RepositoryWorkspace in the set currently
//     workspace.RepositoryWorkspaceQuarantined (V3-10, already real and
//     persisted) blocks the request outright — this task's own Mục tiêu
//     line names this unambiguously.
//
// A fourth condition gates the request just as hard, but is not itself an
// "eligibility check against this codebase's own state": the caller must
// present a ports.ReleaseEligibilityAuthority that reports familyID's own
// ReleaseSet as sealed or abandoned (GC-INV-26). See
// ports.ReleaseEligibilityAuthority's own doc comment for why this is an
// abstract port with no real implementation anywhere in this task's own
// scope — V3's own test coverage supplies a fake.
//
// # "Active attempt" — deliberately not checked, and why
//
// The Mục tiêu line's own eligibility list also names "active attempt"
// (runtime.ExecutionAttemptID). Unlike lease/job/quarantine, this is not
// merely unpopulated in V3 — there is no schema path to even ask the
// question yet: execution_attempts (0001_initial_schema.sql) foreign-keys
// only to node_runs, which foreign-keys to workflow_runs — nothing in that
// chain references a RepositoryWorkspace or WorkspaceSet at all, because no
// runtime engine exists yet to create that linkage (V4's own job, per this
// codebase's already-established boundary — see e.g.
// internal/domain/work.WorkItem's own SourceNodeRunID field and its "no
// runtime engine exists yet to spawn a real NodeRun" doc comment for the
// identical pattern). Wiring a query against a join that cannot exist
// without inventing schema this task does not own would be exactly the
// premature scope 00-roadmap.md §3 warns against, so this check is omitted
// outright rather than replaced with an always-passing stand-in query —
// there is no vacuous version of a query with no join path to write.
package workspacerelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// WorkspaceSetReleaseJobKind is durable_jobs.kind's value for the job
// RequestWorkspaceSetRelease enqueues, following
// REPOSITORY_PROBE/WORKSPACE_PROVISION/WORKSPACE_RECONCILIATION's own
// established naming (see this package's own doc comment on why "CONTROL"
// in the design doc's "enqueue job CONTROL" reads as a descriptive
// category, not a literal string — no existing job kind in this codebase is
// literally "CONTROL").
const WorkspaceSetReleaseJobKind = "WORKSPACE_SET_RELEASE"

// defaultReleaseJobMaxClaims mirrors defaultReconciliationJobMaxClaims and
// catalog.defaultProbeJobMaxClaims's own identical choice for a single
// logical unit of (eventually V5-14's own) recovery/cleanup work.
const defaultReleaseJobMaxClaims = 3

var (
	// ErrWorkspaceSetAlreadyReleased is returned when the WorkspaceSet
	// req.FamilyID names is already workspace.WorkspaceSetReleased —
	// nothing remains to release, and a fresh request against a set with
	// zero live repository workspaces left would be meaningless.
	ErrWorkspaceSetAlreadyReleased = errors.New("workspacerelease: workspace set is already released")
	// ErrWorkspaceSetHasQuarantinedRepository is returned when any
	// RepositoryWorkspace in the set is currently QUARANTINED (V3-10) —
	// this task's own Mục tiêu line's "quarantine" eligibility check.
	ErrWorkspaceSetHasQuarantinedRepository = errors.New("workspacerelease: workspace set has a quarantined repository workspace")
	// ErrWorkspaceSetHasActiveWriteLease is returned when any
	// RepositoryWorkspace in the set currently holds a live write lease
	// (V3-09) — this task's own Mục tiêu line's "active lease" eligibility
	// check.
	ErrWorkspaceSetHasActiveWriteLease = errors.New("workspacerelease: workspace set has a repository workspace with an active write lease")
	// ErrWorkspaceSetHasActiveJob is returned when a non-terminal durable
	// job already references the WorkspaceSet or one of its repository
	// workspaces — this task's own Mục tiêu line's "active job"
	// eligibility check.
	ErrWorkspaceSetHasActiveJob = errors.New("workspacerelease: workspace set has an active durable job")
	// ErrReleaseNotAuthorized is returned when
	// ports.ReleaseEligibilityAuthority.IsReleaseAuthorized reports the
	// family's own ReleaseSet is not currently sealed or abandoned
	// (GC-INV-26).
	ErrReleaseNotAuthorized = errors.New("workspacerelease: release is not authorized")
)

// RequestWorkspaceSetReleaseRequest is what a caller supplies to
// RequestWorkspaceSetRelease.
type RequestWorkspaceSetReleaseRequest struct {
	FamilyID  string
	ProjectID string
}

// RequestWorkspaceSetReleaseResult is what RequestWorkspaceSetRelease
// returns (and what a replayed command-receipt reconstructs).
type RequestWorkspaceSetReleaseResult struct {
	WorkspaceSetID string `json:"workspaceSetId"`
	FamilyID       string `json:"familyId"`
	ProjectID      string `json:"projectId"`
	State          string `json:"state"`
	ReleaseJobID   string `json:"releaseJobId"`
}

// releaseRepositoryWorkspaceEntry is one row of releaseJobPayload's own
// RepositoryWorkspaces list — just enough for V5-14's own future executor
// to re-resolve each real RepositoryWorkspace without a second
// ListWorkspaceSetRepositoryWorkspaces call racing against whatever has
// changed since this request was accepted.
type releaseRepositoryWorkspaceEntry struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	RepositoryID          string `json:"repositoryId"`
	Generation            uint64 `json:"generation"`
}

// releaseJobPayload mirrors reconciliationJobPayload's own shape: captured
// once at request time so a later job handler never has to re-derive it.
// RepositoryWorkspaces enumerates every repository workspace this
// WorkspaceSet had at request time — this task's own "per-repo partial
// result" phrase (Thực hiện line) describes what V5-14's own executor will
// eventually do with this list, not anything this package does with it
// itself; this package never reads RepositoryWorkspaces back out again.
type releaseJobPayload struct {
	WorkspaceSetID       string                            `json:"workspaceSetId"`
	FamilyID             string                            `json:"familyId"`
	ProjectID            string                            `json:"projectId"`
	ExpectedVersion      uint64                            `json:"expectedVersion"`
	RepositoryWorkspaces []releaseRepositoryWorkspaceEntry `json:"repositoryWorkspaces"`
}

// RequestWorkspaceSetRelease is the public command: see this package's own
// doc comment for the full public/internal contract. cmd.ExpectedVersion is
// required and is checked against the WorkspaceSet's own current version —
// a stale caller is rejected with ports.ErrOptimisticConflict rather than
// silently requesting release against a generation it no longer has an
// accurate picture of, the same fencing
// workspacereconcile.RequestWorkspaceReconciliation already establishes for
// RepositoryWorkspace.Version. Follows the identical idempotent-command
// shape: a retry with the same IdempotencyKey and RequestHash replays the
// first call's result; the same key with a different RequestHash is
// rejected as ports.ErrReceiptConflict.
//
// authority is consulted before this function ever opens a write
// transaction: it is an external dependency (ports.ReleaseEligibilityAuthority),
// and ports.UnitOfWork's own doc comment forbids a WithSerializedWrite
// closure from making any call that is not through the ports.Tx it
// receives. A pure replay (an already-recorded receipt for this exact
// IdempotencyKey/RequestHash) is detected first, via a read-only pass, so a
// retried call never re-consults authority at all — only a genuinely new
// request does.
func RequestWorkspaceSetRelease(
	ctx context.Context,
	uow ports.UnitOfWork,
	ids idsource.Source,
	authority ports.ReleaseEligibilityAuthority,
	cmd ports.Command,
	req RequestWorkspaceSetReleaseRequest,
) (RequestWorkspaceSetReleaseResult, error) {
	if strings.TrimSpace(req.FamilyID) == "" {
		return RequestWorkspaceSetReleaseResult{}, errors.New("workspacerelease: FamilyID is required")
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return RequestWorkspaceSetReleaseResult{}, errors.New("workspacerelease: ProjectID is required")
	}
	if cmd.ExpectedVersion == 0 {
		return RequestWorkspaceSetReleaseResult{}, errors.New(
			"workspacerelease: RequestWorkspaceSetRelease requires cmd.ExpectedVersion (the WorkspaceSet version the caller observed) — this is the fence that rejects a stale release request")
	}
	if authority == nil {
		return RequestWorkspaceSetReleaseResult{}, errors.New("workspacerelease: ReleaseEligibilityAuthority is required")
	}

	// Fast path: a genuine replay never needs to re-consult authority (see
	// this function's own doc comment).
	var replay RequestWorkspaceSetReleaseResult
	var replayed bool
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		existingReceipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if existingReceipt.RequestHash != cmd.RequestHash {
			return ports.ErrReceiptConflict
		}
		replayed = true
		return json.Unmarshal([]byte(existingReceipt.ResultJSON), &replay)
	}); err != nil {
		return RequestWorkspaceSetReleaseResult{}, err
	}
	if replayed {
		return replay, nil
	}

	authorized, reason, err := authority.IsReleaseAuthorized(ctx, req.FamilyID)
	if err != nil {
		return RequestWorkspaceSetReleaseResult{}, fmt.Errorf("workspacerelease: resolve release authorization for family %s: %w", req.FamilyID, err)
	}
	if !authorized {
		return RequestWorkspaceSetReleaseResult{}, fmt.Errorf("%w: family %s: %s", ErrReleaseNotAuthorized, req.FamilyID, reason)
	}

	var result RequestWorkspaceSetReleaseResult
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		existingReceipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existingReceipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(existingReceipt.ResultJSON), &result)
		}

		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, req.FamilyID)
		if err != nil {
			return err
		}
		if string(set.ProjectID) != req.ProjectID {
			return fmt.Errorf("%w: workspace set for family %s belongs to project %s, not %s",
				ports.ErrCrossProjectReference, req.FamilyID, set.ProjectID, req.ProjectID)
		}
		if cmd.ExpectedVersion != set.Version {
			return fmt.Errorf("%w: workspace set %s expected version %d, currently %d",
				ports.ErrOptimisticConflict, set.ID, cmd.ExpectedVersion, set.Version)
		}
		if set.State == workspace.WorkspaceSetReleased {
			return fmt.Errorf("%w: workspace set %s", ErrWorkspaceSetAlreadyReleased, set.ID)
		}

		repoWorkspaces, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, string(set.ID))
		if err != nil {
			return err
		}

		repoWorkspaceIDs := make([]string, 0, len(repoWorkspaces))
		payloadEntries := make([]releaseRepositoryWorkspaceEntry, 0, len(repoWorkspaces))
		for _, rw := range repoWorkspaces {
			if rw.State == workspace.RepositoryWorkspaceQuarantined {
				return fmt.Errorf("%w: repository workspace %s", ErrWorkspaceSetHasQuarantinedRepository, rw.ID)
			}
			repoWorkspaceIDs = append(repoWorkspaceIDs, string(rw.ID))
			payloadEntries = append(payloadEntries, releaseRepositoryWorkspaceEntry{
				RepositoryWorkspaceID: string(rw.ID), RepositoryID: string(rw.RepositoryID), Generation: rw.Generation,
			})
		}

		hasActiveLease, err := tx.Work().HasActiveWriteLease(ctx, repoWorkspaceIDs)
		if err != nil {
			return err
		}
		if hasActiveLease {
			return fmt.Errorf("%w: workspace set %s", ErrWorkspaceSetHasActiveWriteLease, set.ID)
		}

		aggregateIDs := append([]string{string(set.ID)}, repoWorkspaceIDs...)
		hasActiveJob, err := tx.Jobs().HasActiveJobForAggregateIDs(ctx, aggregateIDs)
		if err != nil {
			return err
		}
		if hasActiveJob {
			return fmt.Errorf("%w: workspace set %s", ErrWorkspaceSetHasActiveJob, set.ID)
		}

		payload, err := json.Marshal(releaseJobPayload{
			WorkspaceSetID: string(set.ID), FamilyID: req.FamilyID, ProjectID: req.ProjectID,
			ExpectedVersion: set.Version, RepositoryWorkspaces: payloadEntries,
		})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", WorkspaceSetReleaseJobKind, err)
		}

		// Deterministic, version-scoped idempotency key — mirrors
		// workspacereconcile's own identical reasoning: at most one
		// WORKSPACE_SET_RELEASE job can ever exist for this exact
		// (WorkspaceSet, Version) pair, and since this command never
		// itself moves WorkspaceSet.Version, a second distinct request
		// against the same still-open release always collides here too
		// (on top of the "active job" eligibility check above already
		// catching it).
		jobIdempotencyKey := fmt.Sprintf("workspace-set-release:%s@%d", set.ID, set.Version)
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(ids.NewID()), ProjectID: project.ProjectID(req.ProjectID), Kind: WorkspaceSetReleaseJobKind,
			AggregateType: "WorkspaceSet", AggregateID: string(set.ID), Payload: payload,
			AvailableAt: cmd.RequestedAt, MaxClaims: defaultReleaseJobMaxClaims,
			IdempotencyKey: jobIdempotencyKey,
		})
		if err != nil {
			if errors.Is(err, ports.ErrPersistenceAlreadyExists) {
				return fmt.Errorf("workspace set %s already has an open release at version %d: %w",
					set.ID, set.Version, err)
			}
			return err
		}

		eventPayload, err := json.Marshal(struct {
			WorkspaceSetID string `json:"workspaceSetId"`
			FamilyID       string `json:"familyId"`
			ProjectID      string `json:"projectId"`
			State          string `json:"state"`
			ReleaseJobID   string `json:"releaseJobId"`
		}{
			WorkspaceSetID: string(set.ID), FamilyID: req.FamilyID, ProjectID: req.ProjectID,
			State: string(set.State), ReleaseJobID: string(job.ID),
		})
		if err != nil {
			return fmt.Errorf("marshal WorkspaceSetReleaseRequested payload: %w", err)
		}
		// AggregateID is the freshly minted job's own ID, not the
		// WorkspaceSet's — the same technique
		// workspacereconcile.RequestWorkspaceReconciliation's own
		// WorkspaceReconciliationRequested event uses (see that function's
		// doc comment): this package has no per-aggregate event-sequence
		// allocator for "WorkspaceSet" (no existing event stream in this
		// codebase uses that AggregateType at all yet), and a freshly
		// minted job ID is only ever assigned once, ever, so Sequence=1 on
		// its own aggregate identity can never collide.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-requested", ProjectID: req.ProjectID,
			AggregateType: "WorkspaceSetRelease", AggregateID: string(job.ID), Sequence: 1,
			EventType: "WorkspaceSetReleaseRequested", SchemaVersion: 1, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = RequestWorkspaceSetReleaseResult{
			WorkspaceSetID: string(set.ID), FamilyID: req.FamilyID, ProjectID: req.ProjectID,
			State: string(set.State), ReleaseJobID: string(job.ID),
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}
