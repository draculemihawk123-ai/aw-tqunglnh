// Package workspacereconcile is V3-10's operator/API-triggered recovery
// flow for a RepositoryWorkspace (docs/design/05-v3-project-workspace.md
// V3-10, GC-ACC-05): "vận hành recovery workspace mà không destructive
// reset mù" — inspect the real workspace, reach an accept/block/recreate
// decision grounded in that real evidence, and never blindly reset
// anything.
//
// This is a DIFFERENT flow from internal/app/worker.ReconcileInterruptedAttempt
// (interruption.go): that one is the AUTOMATIC crash-recovery path a
// replacement worker runs the moment it finds an attempt stranded at
// RUNNING after a hard crash — it quarantines proactively, from inside the
// crash-recovery sweep itself, and this package never modifies it. This
// package is the OPERATOR-triggered follow-up: an explicit request to
// inspect and decide on a RepositoryWorkspace an operator has flagged —
// whether because it was already quarantined by that automatic path, or
// because it is merely suspect and has not been quarantined at all yet.
//
// Like every other V3 concern in this codebase (catalog.RegisterRepository
// enqueues, repositoryprobe.Handler consumes; work.CreateRootWorkItem
// enqueues, workspaceprovision.Handler consumes), this package splits its
// work into a public command and an internal job handler:
//
//   - RequestWorkspaceReconciliation (below) is the ONLY entry point any
//     API/CLI-shaped caller may ever call. It touches nothing but
//     ports.UnitOfWork/ports.Command — no ports.WorkspaceProvider, no
//     ports.WorkspaceLifecycle, no filesystem or Git call anywhere in its
//     own call graph (see internal/archtest's
//     TestRequestWorkspaceReconciliationNeverImportsWorkspaceIO for the
//     enforced boundary, mirroring
//     TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess's
//     own identical "public path never touches I/O directly" precedent).
//     It checks eligibility (the RepositoryWorkspace must currently be
//     READY or QUARANTINED — nothing else is reconcilable), fences a stale
//     caller out via cmd.ExpectedVersion, writes a durable
//     WorkspaceReconciliationRequested event, and enqueues exactly one
//     WORKSPACE_RECONCILIATION durable job.
//
//   - ExecuteWorkspaceReconciliation (handler.go) is the internal job
//     handler that actually inspects the real process/revision/diff state
//     (ports.WorkspaceProvider.Inspect/Diff) and calls this codebase's own
//     already-built, already-tested QuarantineRepositoryWorkspace/
//     RecreateRepositoryWorkspace (ports.WorkspaceLifecycle,
//     internal/adapters/sqlite/workspace_lifecycle.go) to act on its
//     decision. No API/CLI surface calls it directly — only
//     Handler.Handle, wired as this durable job kind's own registered
//     workerpool.Handler.
//
// # Idempotency
//
// "Idempotent reconcile" (this task's own Verify line) is enforced at two
// layers:
//
//  1. Request layer: the WORKSPACE_RECONCILIATION job's own IdempotencyKey
//     is derived deterministically from (RepositoryWorkspaceID, Version) —
//     "workspace-reconciliation:<id>@<version>". durable_jobs.idempotency_key
//     is a real UNIQUE column, so at most one reconciliation job can ever
//     exist for one exact (workspace, version) pair. A BLOCK decision never
//     changes the workspace's version, so a second RequestWorkspaceReconciliation
//     call while that exact reconciliation is still open (or already
//     resolved to BLOCK) always collides on the same key and is rejected —
//     "already has an open reconciliation intent" grounded in the version
//     fence itself, with no separate intent table needed (the version
//     column already is that ledger). A genuinely NEW reconciliation can
//     only ever be requested once the workspace's version has actually
//     moved (a fresh Quarantine, or a fresh generation from Recreate).
//  2. Job layer: Handler.Handle re-reads the current row before doing any
//     real I/O and returns early (no-op) if either the row's version has
//     already moved past what this job's own payload captured, or a
//     next-generation row already exists — the same "an earlier, crashed
//     claim of this exact job already resolved it" idempotent early-return
//     repositoryprobe.Handler/workspaceprovision.Handler both already
//     establish for their own aggregates.
package workspacereconcile

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

// WorkspaceReconciliationJobKind is durable_jobs.kind's value for the job
// RequestWorkspaceReconciliation enqueues. durable_jobs.kind carries no
// CHECK constraint (internal/adapters/sqlite/migrations/0001_initial_schema.sql),
// so this new kind string needs no migration. The design doc's own phrase
// "enqueue job CONTROL" reads as a descriptive category (control-plane
// work, as opposed to a data-plane node execution), not a literal required
// string — no existing job kind in this codebase is literally "CONTROL" —
// so this follows REPOSITORY_PROBE/WORKSPACE_PROVISION's own established
// naming style instead.
const WorkspaceReconciliationJobKind = "WORKSPACE_RECONCILIATION"

// defaultReconciliationJobMaxClaims mirrors catalog.defaultProbeJobMaxClaims
// and workspaceprovision's own identical choice for a single logical unit
// of recovery work.
const defaultReconciliationJobMaxClaims = 3

// ErrWorkspaceNotReconcilable is returned when the RepositoryWorkspace
// req.RepositoryWorkspaceID names is not currently in a state
// RequestWorkspaceReconciliation can act on — neither READY (an operator
// wants to double-check a currently in-service workspace) nor QUARANTINED
// (a follow-up after an earlier quarantine, automatic or otherwise).
// PROVISIONING/RELEASING/RELEASED/FAILED are never reconciled: a
// PROVISIONING or RELEASING workspace already has its own in-flight
// transition to wait out, and RELEASED/FAILED have no writer to protect or
// service to restore.
var ErrWorkspaceNotReconcilable = errors.New("workspacereconcile: repository workspace is not in a reconcilable state")

// RequestWorkspaceReconciliationRequest is what a caller supplies to
// RequestWorkspaceReconciliation.
type RequestWorkspaceReconciliationRequest struct {
	RepositoryWorkspaceID string
	ProjectID             string
}

// RequestWorkspaceReconciliationResult is what RequestWorkspaceReconciliation
// returns (and what a replayed command-receipt reconstructs).
type RequestWorkspaceReconciliationResult struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	ProjectID             string `json:"projectId"`
	State                 string `json:"state"`
	ReconciliationJobID   string `json:"reconciliationJobId"`
}

// reconciliationJobPayload mirrors workspaceprovision's own jobPayload
// shape: everything ExecuteWorkspaceReconciliation needs to re-resolve the
// exact RepositoryWorkspace this job targets, captured once at request
// time so the job handler never has to re-derive FamilyID from a
// WorkspaceSetID with no "get by ID" lookup of its own (see
// ports.WorkRepository.GetRepositoryWorkspaceByID's own doc comment).
type reconciliationJobPayload struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	ProjectID             string `json:"projectId"`
	FamilyID              string `json:"familyId"`
	WorkspaceSetID        string `json:"workspaceSetId"`
	RepositoryID          string `json:"repositoryId"`
	Generation            uint64 `json:"generation"`
	ExpectedVersion       uint64 `json:"expectedVersion"`
}

// RequestWorkspaceReconciliation is the public command: see this package's
// own doc comment for the full public/internal contract. cmd.ExpectedVersion
// is required and is checked against the RepositoryWorkspace's own current
// version — a caller presenting a stale token (one that no longer matches
// the workspace's current generation/version) is rejected with
// ports.ErrOptimisticConflict rather than silently reconciling against a
// generation it no longer has an accurate picture of (Judgment call 3(b):
// "stale token"). Follows V1-06's idempotent-command shape like
// catalog.RegisterRepository: a retry with the same IdempotencyKey and
// RequestHash replays the first call's result; the same key with a
// different RequestHash is rejected as ports.ErrReceiptConflict.
func RequestWorkspaceReconciliation(
	ctx context.Context,
	uow ports.UnitOfWork,
	ids idsource.Source,
	cmd ports.Command,
	req RequestWorkspaceReconciliationRequest,
) (RequestWorkspaceReconciliationResult, error) {
	if strings.TrimSpace(req.RepositoryWorkspaceID) == "" {
		return RequestWorkspaceReconciliationResult{}, errors.New("workspacereconcile: RepositoryWorkspaceID is required")
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return RequestWorkspaceReconciliationResult{}, errors.New("workspacereconcile: ProjectID is required")
	}
	if cmd.ExpectedVersion == 0 {
		return RequestWorkspaceReconciliationResult{}, errors.New(
			"workspacereconcile: RequestWorkspaceReconciliation requires cmd.ExpectedVersion (the RepositoryWorkspace version the caller observed) — this is the fence that rejects a stale reconciliation request")
	}

	var result RequestWorkspaceReconciliationResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
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

		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, req.RepositoryWorkspaceID)
		if err != nil {
			return err
		}
		rw := record.Workspace

		repo, err := tx.Catalog().GetRepository(ctx, string(rw.RepositoryID))
		if err != nil {
			return err
		}
		if string(repo.ProjectID) != req.ProjectID {
			return fmt.Errorf("%w: repository workspace %s belongs to project %s, not %s",
				ports.ErrCrossProjectReference, rw.ID, repo.ProjectID, req.ProjectID)
		}

		if cmd.ExpectedVersion != rw.Version {
			return fmt.Errorf("%w: repository workspace %s expected version %d, currently %d",
				ports.ErrOptimisticConflict, rw.ID, cmd.ExpectedVersion, rw.Version)
		}
		if rw.State != workspace.RepositoryWorkspaceReady && rw.State != workspace.RepositoryWorkspaceQuarantined {
			return fmt.Errorf("%w: repository workspace %s is %s, want READY or QUARANTINED",
				ErrWorkspaceNotReconcilable, rw.ID, rw.State)
		}

		payload, err := json.Marshal(reconciliationJobPayload{
			RepositoryWorkspaceID: string(rw.ID), ProjectID: req.ProjectID, FamilyID: record.FamilyID,
			WorkspaceSetID: string(rw.WorkspaceSetID), RepositoryID: string(rw.RepositoryID),
			Generation: rw.Generation, ExpectedVersion: rw.Version,
		})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", WorkspaceReconciliationJobKind, err)
		}

		// Deterministic, version-scoped idempotency key — see this
		// package's own doc comment's "Idempotency" section for the full
		// reasoning: at most one WORKSPACE_RECONCILIATION job can ever
		// exist for this exact (RepositoryWorkspace, Version) pair.
		jobIdempotencyKey := fmt.Sprintf("workspace-reconciliation:%s@%d", rw.ID, rw.Version)
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(ids.NewID()), ProjectID: project.ProjectID(req.ProjectID), Kind: WorkspaceReconciliationJobKind,
			AggregateType: "RepositoryWorkspace", AggregateID: string(rw.ID), Payload: payload,
			AvailableAt: cmd.RequestedAt, MaxClaims: defaultReconciliationJobMaxClaims,
			IdempotencyKey: jobIdempotencyKey,
		})
		if err != nil {
			if errors.Is(err, ports.ErrPersistenceAlreadyExists) {
				return fmt.Errorf("repository workspace %s already has an open reconciliation at version %d: %w",
					rw.ID, rw.Version, err)
			}
			return err
		}

		eventPayload, err := json.Marshal(struct {
			RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
			ProjectID             string `json:"projectId"`
			State                 string `json:"state"`
			ReconciliationJobID   string `json:"reconciliationJobId"`
		}{
			RepositoryWorkspaceID: string(rw.ID), ProjectID: req.ProjectID,
			State: string(rw.State), ReconciliationJobID: string(job.ID),
		})
		if err != nil {
			return fmt.Errorf("marshal WorkspaceReconciliationRequested payload: %w", err)
		}
		// AggregateID is the freshly minted job's own ID, not the
		// RepositoryWorkspace's — the same technique
		// catalog.RetryRepositoryProbe's own RepositoryProbeRetried event
		// uses (see that function's doc comment): this package has no
		// per-aggregate event-sequence allocator for "RepositoryWorkspace"
		// (whose own event stream already has REPOSITORY_WORKSPACE_QUARANTINED/
		// RECREATED/RELEASED events with sequences this package does not
		// own), and a freshly minted job ID is only ever assigned once,
		// ever, so Sequence=1 on its own aggregate identity can never
		// collide.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-requested", ProjectID: req.ProjectID,
			AggregateType: "RepositoryWorkspaceReconciliation", AggregateID: string(job.ID), Sequence: 1,
			EventType: "WorkspaceReconciliationRequested", SchemaVersion: 1, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = RequestWorkspaceReconciliationResult{
			RepositoryWorkspaceID: string(rw.ID), ProjectID: req.ProjectID,
			State: string(rw.State), ReconciliationJobID: string(job.ID),
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
