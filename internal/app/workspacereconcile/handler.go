package workspacereconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Decision is the closed, three-way outcome ExecuteWorkspaceReconciliation
// reaches for one RepositoryWorkspace.
type Decision string

const (
	// DecisionAccept: the workspace is confirmed usable as-is. Only
	// reachable from READY — there is no store primitive that moves a
	// QUARANTINED row back to READY in place (ports.ErrWorkspaceQuarantined's
	// own doc comment: "Nothing un-quarantines a generation in place").
	DecisionAccept Decision = "ACCEPT"
	// DecisionBlock: no state change. From READY this means "quarantine
	// it first" (the transition itself is the block); from QUARANTINED it
	// means "stays exactly as it is" — either way, no writer is granted
	// and evidence is preserved untouched.
	DecisionBlock Decision = "BLOCK"
	// DecisionRecreate: provision a fresh generation via
	// ports.WorkspaceLifecycle.RecreateRepositoryWorkspace. The previous
	// generation's row is never mutated by this — it stays exactly as it
	// was, permanently, as evidence (RecreateRepositoryWorkspace's own doc
	// comment).
	DecisionRecreate Decision = "RECREATE"
)

// ClassifyReconciliation is the pure decision core, deliberately
// independent of any real Inspect call so it is unit-testable without a
// real Git subprocess — mirroring worker.ReconcileMutatingAttempt's own
// pure-function style for the sibling automatic crash-recovery flow.
//
// currentState is the RepositoryWorkspace's own state at the moment of
// inspection: READY or QUARANTINED (RequestWorkspaceReconciliation's own
// eligibility gate never lets any other state reach here — any other
// value is a caller bug, not a real decision path). inspectErr is whatever
// ports.WorkspaceProvider.Inspect itself returned (nil on success).
// released reports whether a successful Inspect found the workspace
// already ports.WorkspaceReleased — a real inconsistency here, since
// RequestWorkspaceReconciliation's own eligibility gate never admits a
// RELEASED row to begin with, so the on-disk state and the DB row have
// diverged. dirty is Inspect's own working-tree-dirty signal, only
// meaningful when inspectErr is nil and released is false.
//
// Decision table — grounded solely in Inspect's own State/Dirty signals,
// never an invented one (docs/design/05-v3-project-workspace.md V3-10's
// own "không destructive reset mù" bar):
//
//	currentState  Inspect outcome            Decision   Why
//	READY         error, or released         BLOCK      unsalvageable/inconsistent — quarantine before
//	                                                      anything else touches it
//	READY         succeeds, dirty            BLOCK      dirty with no attempt to attribute it to —
//	                                                      never silently accepted or reset
//	READY         succeeds, clean            ACCEPT     confirmed usable, no action needed
//	QUARANTINED   error, or released         RECREATE   nothing left to protect by staying inert;
//	                                                      recreate is the only path back to service
//	QUARANTINED   succeeds, dirty            BLOCK      still unexplainable — stays quarantined,
//	                                                      evidence preserved exactly as-is
//	QUARANTINED   succeeds, clean            RECREATE   the only "back into service" path once
//	                                                      already quarantined (no un-quarantine-in-place
//	                                                      primitive exists)
func ClassifyReconciliation(currentState workspace.RepositoryWorkspaceState, inspectErr error, released bool, dirty bool) Decision {
	unsalvageable := inspectErr != nil || released
	switch currentState {
	case workspace.RepositoryWorkspaceReady:
		if unsalvageable || dirty {
			return DecisionBlock
		}
		return DecisionAccept
	case workspace.RepositoryWorkspaceQuarantined:
		if dirty && !unsalvageable {
			return DecisionBlock
		}
		return DecisionRecreate
	default:
		return DecisionBlock
	}
}

// blockReason renders a short, typed reason code for the
// REPOSITORY_WORKSPACE_QUARANTINED domain event's own Reason field
// (ports.QuarantineRepositoryWorkspaceUpdate.Reason) — never a raw
// provider/SQL error string, the same "typed Code, not a parsed message"
// discipline every other error-classification field in this codebase
// already follows (see workspace.RepositoryWorkspace.LastProvisionErrorCode's
// own doc comment).
func blockReason(inspectErr error, released, dirty bool) string {
	switch {
	case inspectErr != nil:
		return "RECONCILE_INSPECT_FAILED"
	case released:
		return "RECONCILE_WORKSPACE_RELEASED"
	case dirty:
		return "RECONCILE_DIRTY_UNEXPLAINED"
	default:
		return "RECONCILE_BLOCKED"
	}
}

// ExecuteWorkspaceReconciliationDeps bundles ExecuteWorkspaceReconciliation's
// collaborators. Provider and Lifecycle are deliberately two separate
// ports — ports.WorkspaceProvider owns the physical worktree,
// ports.WorkspaceLifecycle owns which generation is authoritative for
// writers and evidence (ports.WorkspaceLifecycle's own doc comment) — the
// same separation internal/app/worker's WorkspaceReconciler interface
// already draws for the automatic crash-recovery flow.
type ExecuteWorkspaceReconciliationDeps struct {
	UnitOfWork ports.UnitOfWork
	IDs        idsource.Source
	Provider   ports.WorkspaceProvider
	Lifecycle  ports.WorkspaceLifecycle
}

// ExecuteWorkspaceReconciliationRequest is everything
// ExecuteWorkspaceReconciliation needs to fully reconcile one
// RepositoryWorkspace — the parsed form of reconciliationJobPayload plus
// CorrelationID (job.ID, so every domain event ExecuteWorkspaceReconciliation
// causes ties back to the exact WORKSPACE_RECONCILIATION job that produced
// it).
type ExecuteWorkspaceReconciliationRequest struct {
	RepositoryWorkspaceID string
	ProjectID             string
	FamilyID              string
	WorkspaceSetID        string
	RepositoryID          string
	Generation            uint64
	ExpectedVersion       uint64
	CorrelationID         string
}

// ExecuteWorkspaceReconciliation is V3-10's internal command: the
// WORKSPACE_RECONCILIATION job handler's own decision-and-mutate core —
// inspect the real workspace (ports.WorkspaceProvider.Inspect/Diff), reach
// one of ClassifyReconciliation's three closed decisions, and act on it
// via the already-existing, already-tested ports.WorkspaceLifecycle
// primitives. It is never called by any API/CLI-shaped caller directly —
// see this package's own doc comment for the public/internal split
// RequestWorkspaceReconciliation establishes. Handler.Handle (below) is
// its only caller in this codebase; it is exported here, taking an
// already-parsed request rather than a raw ports.DurableJob, so it is
// directly unit-testable, mirroring worker.ReconcileInterruptedAttempt's
// own identical shape for the sibling automatic crash-recovery flow.
func ExecuteWorkspaceReconciliation(ctx context.Context, deps ExecuteWorkspaceReconciliationDeps, request ExecuteWorkspaceReconciliationRequest) error {
	if deps.UnitOfWork == nil || deps.IDs == nil || deps.Provider == nil || deps.Lifecycle == nil {
		return errors.New("workspacereconcile: ExecuteWorkspaceReconciliation requires UnitOfWork/IDs/Provider/Lifecycle")
	}
	if request.RepositoryWorkspaceID == "" || request.WorkspaceSetID == "" || request.RepositoryID == "" ||
		request.Generation == 0 || request.ExpectedVersion == 0 {
		return errors.New("workspacereconcile: ExecuteWorkspaceReconciliation request is incomplete")
	}

	current, resolved, err := loadCurrentRepositoryWorkspace(ctx, deps.UnitOfWork, request)
	if err != nil {
		return fmt.Errorf("load repository workspace %s: %w", request.RepositoryWorkspaceID, err)
	}
	if resolved {
		// Idempotent early-return: an earlier, crashed claim of this exact
		// job already committed its decision (a Quarantine transition
		// already moved the row's version past what this job's own payload
		// captured, or a next-generation row from an earlier Recreate
		// already exists) — nothing more to do. See this package's own doc
		// comment's "Idempotency" section, layer 2.
		return nil
	}
	if current.State != workspace.RepositoryWorkspaceReady && current.State != workspace.RepositoryWorkspaceQuarantined {
		return fmt.Errorf("repository workspace %s is %s, want READY or QUARANTINED", current.ID, current.State)
	}

	handle, err := ports.NewWorkspaceHandle(current.Locator)
	if err != nil {
		return fmt.Errorf("repository workspace %s has an invalid locator: %w", current.ID, err)
	}

	// Real I/O: entirely outside any open transaction, mirroring every
	// other job handler in this codebase (repositoryprobe.Handler/
	// workspaceprovision.Handler's own identical discipline).
	inspection, inspectErr := deps.Provider.Inspect(ctx, handle)
	released := inspectErr == nil && inspection.State == ports.WorkspaceReleased
	dirty := inspectErr == nil && inspection.Dirty

	decision := ClassifyReconciliation(current.State, inspectErr, released, dirty)
	switch decision {
	case DecisionAccept:
		return nil
	case DecisionBlock:
		return executeBlock(ctx, deps, current, handle, request.CorrelationID, inspectErr, released, dirty)
	case DecisionRecreate:
		return executeRecreate(ctx, deps, current, request)
	default:
		return fmt.Errorf("unreachable reconciliation decision %q for repository workspace %s", decision, current.ID)
	}
}

// executeBlock performs DecisionBlock's own action: a no-op if current is
// already QUARANTINED (the row itself, untouched, is the evidence); a
// fenced READY->QUARANTINED transition otherwise, whose Reason captures
// why (blockReason), enriched with a real Diff file count when one is
// available — evidence enrichment only, never a second decision axis (see
// ClassifyReconciliation's own doc comment: Diff.Files is always non-empty
// whenever Inspect already reported Dirty, so it can never disagree with
// the decision already made from Dirty alone).
func executeBlock(
	ctx context.Context, deps ExecuteWorkspaceReconciliationDeps,
	current workspace.RepositoryWorkspace, handle ports.WorkspaceHandle, correlationID string,
	inspectErr error, released, dirty bool,
) error {
	if current.State == workspace.RepositoryWorkspaceQuarantined {
		return nil
	}
	reason := blockReason(inspectErr, released, dirty)
	if dirty && inspectErr == nil {
		diff, diffErr := deps.Provider.Diff(ctx, handle, workspace.Revision{
			RepositoryID: current.RepositoryID, VCSObjectID: current.BaseRevision, WorkspaceGeneration: current.Generation,
		})
		if diffErr == nil {
			reason = fmt.Sprintf("%s: %d file(s) changed", reason, len(diff.Files))
		}
	}
	return deps.Lifecycle.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: current.ID, ExpectedVersion: current.Version, Reason: reason,
		EventID: deps.IDs.NewID(), CorrelationID: correlationID, OccurredAt: time.Now().UTC(),
	})
}

// executeRecreate performs DecisionRecreate's own action: provision a
// fresh generation (real I/O, entirely outside any open transaction, per
// this file's own doc comment) then persist it via
// ports.WorkspaceLifecycle.RecreateRepositoryWorkspace — mirroring
// workspaceprovision.Handler's own identical Provision -> CaptureRevision
// -> persist sequence for first-time provisioning.
func executeRecreate(
	ctx context.Context, deps ExecuteWorkspaceReconciliationDeps,
	current workspace.RepositoryWorkspace, request ExecuteWorkspaceReconciliationRequest,
) error {
	var repo project.Repository
	if err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		result, err := tx.Catalog().GetRepository(ctx, request.RepositoryID)
		repo = result
		return err
	}); err != nil {
		return fmt.Errorf("load repository %s: %w", request.RepositoryID, err)
	}

	nextGeneration := current.Generation + 1
	handle, err := deps.Provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID: project.RepositoryID(request.RepositoryID), LocalRepository: repo.RemoteLocator,
		BaseRef: repo.DefaultRef, FamilyID: work.TaskFamilyID(request.FamilyID),
		WorkspaceSetID: workspace.WorkspaceSetID(request.WorkspaceSetID), Generation: nextGeneration,
	})
	if err != nil {
		return fmt.Errorf("provision recreated workspace for repository %s generation %d: %w", request.RepositoryID, nextGeneration, err)
	}
	if handle.IsZero() {
		return fmt.Errorf("ports.WorkspaceProvider.Provision for repository %s returned a zero handle with a nil error", request.RepositoryID)
	}
	revision, err := deps.Provider.CaptureRevision(ctx, handle)
	if err != nil {
		return fmt.Errorf("capture revision for recreated workspace %s: %w", request.RepositoryID, err)
	}

	_, err = deps.Lifecycle.RecreateRepositoryWorkspace(ctx, ports.RecreateRepositoryWorkspaceRequest{
		PreviousRepositoryWorkspaceID: current.ID, PreviousExpectedVersion: current.Version,
		NewRepositoryWorkspaceID: workspace.RepositoryWorkspaceID(deps.IDs.NewID()),
		Locator:                  handle.String(), BranchRef: "", BaseRevision: revision.VCSObjectID,
		EventID: deps.IDs.NewID(), CorrelationID: request.CorrelationID, OccurredAt: time.Now().UTC(),
	})
	return err
}

// loadCurrentRepositoryWorkspace re-reads request's own RepositoryWorkspace
// by its (WorkspaceSetID, RepositoryID, Generation) composite key —
// request.ExpectedVersion was already the exact version observed when
// RequestWorkspaceReconciliation enqueued this job — and reports whether
// this exact reconciliation attempt is already resolved: either the row's
// version has moved past ExpectedVersion (a Quarantine already committed)
// or a next-generation row already exists (a Recreate already committed).
func loadCurrentRepositoryWorkspace(
	ctx context.Context, uow ports.UnitOfWork, request ExecuteWorkspaceReconciliationRequest,
) (workspace.RepositoryWorkspace, bool, error) {
	var current workspace.RepositoryWorkspace
	var resolved bool
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		rw, err := tx.Work().GetRepositoryWorkspace(ctx, request.WorkspaceSetID, request.RepositoryID, request.Generation)
		if err != nil {
			return err
		}
		current = rw
		if rw.Version != request.ExpectedVersion {
			resolved = true
			return nil
		}
		_, err = tx.Work().GetRepositoryWorkspace(ctx, request.WorkspaceSetID, request.RepositoryID, request.Generation+1)
		if err == nil {
			resolved = true
			return nil
		}
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			return err
		}
		return nil
	})
	return current, resolved, err
}

// Handler implements workerpool.Handler for WorkspaceReconciliationJobKind.
// It depends only on ports.UnitOfWork/ports.WorkspaceProvider/
// ports.WorkspaceLifecycle/idsource.Source (never a concrete adapter
// package directly) — internal/adapters/gitworktree.Provider and
// internal/adapters/sqlite.Store are the production implementations a
// composition root wires in, exactly as repositoryprobe.Handler depends on
// ports.RepositoryProber, never a concrete adapter directly.
type Handler struct {
	deps ExecuteWorkspaceReconciliationDeps
}

// New returns a ready-to-register Handler.
func New(uow ports.UnitOfWork, ids idsource.Source, provider ports.WorkspaceProvider, lifecycle ports.WorkspaceLifecycle) *Handler {
	return &Handler{deps: ExecuteWorkspaceReconciliationDeps{UnitOfWork: uow, IDs: ids, Provider: provider, Lifecycle: lifecycle}}
}

var _ workerpool.Handler = (*Handler)(nil)

// Handle implements workerpool.Handler: unmarshal the job payload and
// delegate to ExecuteWorkspaceReconciliation. Every error
// ExecuteWorkspaceReconciliation itself can return is a genuine,
// unexpected condition (a stale/missing RepositoryWorkspace row, a
// provider contract violation) — unlike repositoryprobe/workspaceprovision's
// own "environment failure is a business outcome, job still succeeds"
// distinction, there is no environment-failure business outcome here for
// Handle to swallow: Inspect/Provision failing IS itself one of
// ClassifyReconciliation's own real decision inputs (RECREATE/BLOCK), not
// a condition ExecuteWorkspaceReconciliation reports back up as an error.
// So any non-nil error here always leaves the job un-completed for retry,
// exactly like every other unexpected-error path in this codebase.
func (h *Handler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload reconciliationJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("workspacereconcile: unmarshal job %s payload: %w", job.ID, err)
	}
	if payload.RepositoryWorkspaceID == "" || payload.ProjectID == "" || payload.FamilyID == "" ||
		payload.WorkspaceSetID == "" || payload.RepositoryID == "" || payload.Generation == 0 || payload.ExpectedVersion == 0 {
		return fmt.Errorf("workspacereconcile: job %s payload is incomplete", job.ID)
	}

	if err := ExecuteWorkspaceReconciliation(ctx, h.deps, ExecuteWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: payload.RepositoryWorkspaceID, ProjectID: payload.ProjectID, FamilyID: payload.FamilyID,
		WorkspaceSetID: payload.WorkspaceSetID, RepositoryID: payload.RepositoryID,
		Generation: payload.Generation, ExpectedVersion: payload.ExpectedVersion, CorrelationID: string(job.ID),
	}); err != nil {
		return fmt.Errorf("workspacereconcile: execute reconciliation for job %s: %w", job.ID, err)
	}
	return nil
}
