// Package workspaceprovision is V3-06's workerpool.Handler for the
// WORKSPACE_PROVISION durable job internal/app/work.CreateRootWorkItem
// enqueues, one per repository in a root task's initial scope
// (internal/app/work.WorkspaceProvisionJobKind,
// docs/design/05-v3-project-workspace.md V3-06) — the same "an earlier task
// enqueues, a later task is the sole consumer" relationship
// internal/app/repositoryprobe already established for
// REPOSITORY_PROBE/RepositoryProbeJobKind (V3-01 enqueues, V3-02 consumes).
//
// Handle mirrors repositoryprobe.Handler.Handle's own structure exactly:
// claim a job, an idempotent early-return for a job whose target
// RepositoryWorkspace already reached a terminal state (crash-recovery
// reclaim), a begin step that best-effort CAS-transitions the owning
// WorkspaceSet REQUESTED->PROVISIONING inside its own transaction, real I/O
// (here: the real Git worktree provisioning via ports.WorkspaceProvider —
// never git/filesystem calls of this package's own) entirely OUTSIDE any
// open transaction, then a finish step that persists the resulting
// RepositoryWorkspace (READY or FAILED) and re-aggregates the owning
// WorkspaceSet's own state, all inside one final transaction.
//
// Job success vs. business outcome mirrors V3-02's own identical
// distinction ("job success vs. business outcome" in
// repositoryprobe.Handler's own doc comment): a provisioning attempt that
// fails for a real, expected reason (the repository's own local path
// became unreachable, a Git command failed against real repository state,
// ...) is the job doing its job correctly — Handle returns nil, workerpool
// marks the durable job itself SUCCEEDED, and the failure is recorded as
// data (RepositoryWorkspace.State = FAILED,
// workspace.RepositoryWorkspace.LastProvisionErrorCode set). Only a genuine
// unexpected condition (a malformed job payload, a persistence failure, a
// ports.WorkspaceProvider implementation violating its own contract)
// returns a real error, leaving the job un-completed for workerpool's own
// lease-expiry/retry mechanism.
//
// Unlike ports.RepositoryProber — whose own contract, per
// repositoryprobe.Handler's doc comment, promises every failure is a
// classified *apperror.Error — ports.WorkspaceProvider documents no such
// error-typing contract, and internal/adapters/gitworktree (its only
// existing implementation) returns plain sentinel errors
// (internal/adapters/gitworktree/errors.go), never *apperror.Error. This
// package deliberately never imports that concrete adapter package to
// pattern-match against its sentinels — the same "depend on the port,
// never a concrete adapter" discipline repositoryprobe.Handler already
// follows for ports.RepositoryProber (internal/adapters/repoprobe is that
// package's own production implementation, also never imported here). So
// the environment-vs-unexpected split here is drawn differently: ANY error
// Provision/CaptureRevision themselves return is treated as the business/
// environment outcome (real, external Git and filesystem state is exactly
// what those two calls run against) — FAILED, job succeeds; only a
// provider call succeeding (err == nil) while returning a structurally
// invalid result (a zero WorkspaceHandle, an empty/zero Revision) is
// treated as the provider violating its own contract — a genuine handler
// failure, left for retry.
package workspaceprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// generation is always 1: this package's whole scope is first-time
// provisioning. Regeneration after QUARANTINED (a new, higher generation)
// is V3-09/V3-10's job entirely — see
// internal/app/ports/workspacelifecycle.go's own
// RecreateRepositoryWorkspace, explicitly out of this task's scope.
const generation = 1

// Handler implements workerpool.Handler for
// internal/app/work.WorkspaceProvisionJobKind. It depends only on
// ports.UnitOfWork/ports.WorkspaceProvider/idsource.Source (never a
// concrete adapter package directly) — internal/adapters/gitworktree.Provider
// is the production ports.WorkspaceProvider implementation a composition
// root wires in, exactly as internal/app/repositoryprobe.Handler depends on
// ports.RepositoryProber, never internal/adapters/repoprobe directly.
type Handler struct {
	uow      ports.UnitOfWork
	ids      idsource.Source
	provider ports.WorkspaceProvider
}

// New returns a ready-to-register Handler.
func New(uow ports.UnitOfWork, ids idsource.Source, provider ports.WorkspaceProvider) *Handler {
	return &Handler{uow: uow, ids: ids, provider: provider}
}

var _ workerpool.Handler = (*Handler)(nil)

// jobPayload mirrors the exact JSON shape internal/app/work.CreateRootWorkItem
// marshals for each WORKSPACE_PROVISION job: {"workItemId", "projectId",
// "familyId", "workspaceSetId", "repositoryId"}. WorkItemID is carried only
// for shape-parity with that payload and diagnostics — this handler's own
// logic never keys off it (a RepositoryWorkspace belongs to a WorkspaceSet/
// Repository pair, not to the WorkItem that happened to cause its job to
// be enqueued).
type jobPayload struct {
	WorkItemID     string `json:"workItemId"`
	ProjectID      string `json:"projectId"`
	FamilyID       string `json:"familyId"`
	WorkspaceSetID string `json:"workspaceSetId"`
	RepositoryID   string `json:"repositoryId"`
}

// Handle implements workerpool.Handler. See this package's own doc comment
// for the full contract.
func (h *Handler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload jobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("workspaceprovision: unmarshal job %s payload: %w", job.ID, err)
	}
	if payload.ProjectID == "" || payload.FamilyID == "" || payload.WorkspaceSetID == "" || payload.RepositoryID == "" {
		return fmt.Errorf("workspaceprovision: job %s payload missing projectId/familyId/workspaceSetId/repositoryId", job.ID)
	}

	// Idempotent early-return, mirroring repositoryprobe.Handler's own
	// identical "active probe idempotent" discipline
	// (docs/design/01-system-design.md §6.1) applied to this aggregate: a
	// previous claim of this exact job already drove this repository's own
	// RepositoryWorkspace to a resolved terminal state, then crashed before
	// this job was itself marked SUCCEEDED (the window between the finish
	// transaction's commit below and workerpool.Pool's own CompleteJob
	// call). That earlier attempt already did everything there is to do —
	// re-running the real Git worktree provisioning again would be
	// redundant at best and unsafe at worst.
	existing, err := h.loadExistingRepositoryWorkspace(ctx, payload.WorkspaceSetID, payload.RepositoryID)
	if err != nil {
		return fmt.Errorf("workspaceprovision: load repository workspace for job %s: %w", job.ID, err)
	}
	if existing != nil {
		switch existing.State {
		case workspace.RepositoryWorkspaceReady, workspace.RepositoryWorkspaceFailed:
			return nil
		default:
			// This package's finish step (below) only ever inserts a
			// RepositoryWorkspace already at a terminal state (READY or
			// FAILED) — it never persists a transient PROVISIONING row of
			// its own — so finding one at any other state here is a
			// genuine data-integrity condition, not a business outcome this
			// handler can classify.
			return fmt.Errorf("workspaceprovision: repository workspace %s is %s, want READY or FAILED",
				existing.ID, existing.State)
		}
	}

	repo, err := h.loadRepository(ctx, payload.RepositoryID)
	if err != nil {
		return fmt.Errorf("workspaceprovision: load repository %s: %w", payload.RepositoryID, err)
	}
	if string(repo.ProjectID) != payload.ProjectID {
		return fmt.Errorf("workspaceprovision: repository %s belongs to project %s, not job %s's own project %s",
			payload.RepositoryID, repo.ProjectID, job.ID, payload.ProjectID)
	}

	// Best-effort begin step: flip the owning WorkspaceSet REQUESTED->
	// PROVISIONING once, from whichever of its scoped repositories' own
	// jobs gets there first. A lost CAS here always means another
	// repository's own job already made the identical transition —
	// "someone else already finished it, nothing more to do", never an
	// error (see beginProvisioning's own doc comment for the full
	// reasoning, and this package's own doc comment for the general
	// lost-CAS discipline it and aggregateWorkspaceSet below both share).
	if err := h.beginProvisioning(ctx, payload.FamilyID); err != nil {
		return fmt.Errorf("workspaceprovision: begin provisioning for family %s: %w", payload.FamilyID, err)
	}

	// Real I/O: entirely outside any open transaction, mirroring
	// repositoryprobe.Handler's own identical discipline
	// (internal/app/workflowcompiler.CompileAndResolve's own doc comment;
	// internal/archtest's enforced precedent elsewhere document the same
	// "real I/O never runs inside an open write transaction" rule this
	// package follows too).
	//
	// Defensive re-check: CreateRootWorkItem already required every
	// initially-scoped repository to be ACTIVE before granting it scope,
	// but a repository can be disabled/blocked in the window between that
	// grant and this job actually running. A no-longer-ACTIVE repository
	// is exactly this task's own example of "a real, expected condition"
	// (this package's own doc comment) — no real Git I/O is even attempted
	// against it.
	if repo.Status != project.RepositoryActive {
		reason := "REPOSITORY_NOT_ACTIVE"
		return h.finishFailed(ctx, payload, &reason)
	}

	handle, provisionErr := h.provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID(payload.RepositoryID),
		LocalRepository: repo.RemoteLocator,
		BaseRef:         repo.DefaultRef,
		FamilyID:        work.TaskFamilyID(payload.FamilyID),
		WorkspaceSetID:  workspace.WorkspaceSetID(payload.WorkspaceSetID),
		Generation:      generation,
	})
	if provisionErr != nil {
		reason := "PROVISION_FAILED"
		return h.finishFailed(ctx, payload, &reason)
	}
	if handle.IsZero() {
		// Contract violation: Provision succeeded (nil error) but returned
		// no usable handle. Treated as a genuine handler failure rather
		// than silently recording a FAILED RepositoryWorkspace for a
		// provider bug — the job is left un-completed for retry, matching
		// every other unexpected-error path here.
		return fmt.Errorf("workspaceprovision: ports.WorkspaceProvider.Provision for repository %s returned a zero handle with a nil error", payload.RepositoryID)
	}

	revision, captureErr := h.provider.CaptureRevision(ctx, handle)
	if captureErr != nil {
		// Unlike Provision itself, a CaptureRevision failure immediately
		// after a successful Provision inspects a workspace this same call
		// just created — a real, unexpected condition, not a business
		// outcome. Left for retry: the next attempt's own idempotency
		// check above finds no terminal row yet, re-runs Provision
		// (idempotent by construction against the identical FamilyID/
		// RepositoryID/Generation spec, per
		// internal/adapters/gitworktree.Provider's own doc comment), and
		// tries CaptureRevision again.
		return fmt.Errorf("workspaceprovision: capture revision for repository %s: %w", payload.RepositoryID, captureErr)
	}
	if revision.RepositoryID != project.RepositoryID(payload.RepositoryID) ||
		revision.VCSObjectID == "" || revision.WorkspaceGeneration != generation {
		return fmt.Errorf("workspaceprovision: ports.WorkspaceProvider.CaptureRevision for repository %s returned an invalid revision %+v",
			payload.RepositoryID, revision)
	}

	return h.finishReady(ctx, payload, repo, handle, revision)
}

func (h *Handler) loadExistingRepositoryWorkspace(ctx context.Context, workspaceSetID, repositoryID string) (*workspace.RepositoryWorkspace, error) {
	var result *workspace.RepositoryWorkspace
	err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		rw, err := tx.Work().GetRepositoryWorkspace(ctx, workspaceSetID, repositoryID, generation)
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &rw
		return nil
	})
	return result, err
}

func (h *Handler) loadRepository(ctx context.Context, repositoryID string) (project.Repository, error) {
	var repo project.Repository
	err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		result, err := tx.Catalog().GetRepository(ctx, repositoryID)
		repo = result
		return err
	})
	return repo, err
}

// beginProvisioning CAS-transitions the WorkspaceSet owning familyID from
// REQUESTED to PROVISIONING in its own transaction, once — whichever of
// this WorkspaceSet's scoped repositories' own WORKSPACE_PROVISION job
// happens to run first. Because several such jobs (one per repository) can
// reach this step concurrently, a lost CAS (the set has already left
// REQUESTED by the time this call runs) is always benign here: it can only
// mean another repository's own job already made the identical REQUESTED->
// PROVISIONING transition (or, on a rarer race, an even later aggregation
// already moved the set past PROVISIONING entirely) — never a genuine
// conflict this call needs to surface as an error.
func (h *Handler) beginProvisioning(ctx context.Context, familyID string) error {
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, familyID)
		if err != nil {
			return err
		}
		if set.State != workspace.WorkspaceSetRequested {
			return nil
		}
		_, err = tx.Work().TransitionWorkspaceSetState(ctx, ports.TransitionWorkspaceSetStateRequest{
			WorkspaceSetID: string(set.ID), ExpectedState: workspace.WorkspaceSetRequested, ExpectedVersion: set.Version,
			NextState: workspace.WorkspaceSetProvisioning,
		})
		if errors.Is(err, ports.ErrOptimisticConflict) {
			return nil
		}
		return err
	})
}

// finishFailed persists a FAILED RepositoryWorkspace for payload's own
// repository and re-aggregates the owning WorkspaceSet's own state — see
// this package's own doc comment for the full contract. reasonCode is a
// short, coarse code (never a raw provider/SQL error string, the same
// "typed Code, not a parsed message" discipline every other
// error-classification field in this codebase already follows) recorded as
// workspace.RepositoryWorkspace.LastProvisionErrorCode.
func (h *Handler) finishFailed(ctx context.Context, payload jobPayload, reasonCode *string) error {
	rw := workspace.RepositoryWorkspace{
		ID:                     workspace.RepositoryWorkspaceID(h.ids.NewID()),
		WorkspaceSetID:         workspace.WorkspaceSetID(payload.WorkspaceSetID),
		RepositoryID:           project.RepositoryID(payload.RepositoryID),
		Generation:             generation,
		State:                  workspace.RepositoryWorkspaceFailed,
		Version:                1,
		LastProvisionErrorCode: reasonCode,
	}
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Work().CreateRepositoryWorkspace(ctx, rw); err != nil {
			return err
		}
		return h.aggregateWorkspaceSet(ctx, tx, payload.WorkspaceSetID, payload.FamilyID)
	})
}

// finishReady persists a READY RepositoryWorkspace for payload's own
// repository (built via workspace.NewRepositoryWorkspace, the shared,
// already-tested domain constructor — see this package's own doc comment
// for why this task reuses it rather than duplicating its invariants) and
// re-aggregates the owning WorkspaceSet's own state.
func (h *Handler) finishReady(
	ctx context.Context, payload jobPayload,
	repo project.Repository, handle ports.WorkspaceHandle, revision workspace.Revision,
) error {
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, payload.FamilyID)
		if err != nil {
			return err
		}
		rw, err := workspace.NewRepositoryWorkspace(
			workspace.RepositoryWorkspaceID(h.ids.NewID()), set, repo, generation,
			handle.String(), "", revision.VCSObjectID,
		)
		if err != nil {
			return fmt.Errorf("build repository workspace: %w", err)
		}
		rw.State = workspace.RepositoryWorkspaceReady
		rw.CurrentRevision = revision.VCSObjectID
		if _, err := tx.Work().CreateRepositoryWorkspace(ctx, rw); err != nil {
			return err
		}
		return h.aggregateWorkspaceSet(ctx, tx, payload.WorkspaceSetID, payload.FamilyID)
	})
}

// aggregateWorkspaceSet re-derives the owning WorkspaceSet's own state from
// every RepositoryWorkspace row now persisted for it, against the family's
// own required-repository set — every DISTINCT RepositoryID its scope
// history names, via ListFamilyRepositoryScopes, the exact same method
// internal/app/work.CreateChildWorkItem's own subset check already reuses,
// not a parallel query of this package's own invention. BLOCKED the moment
// any required repository's own row is FAILED; READY only once every
// required repository's own row is READY (this task's own explicit "Hoàn
// thành khi: family chỉ ready khi mọi required repository ready" bar) —
// computing and persisting the set's own base RevisionSet in the same CAS
// transaction, never partially (workspace.NewRevisionSet, already built,
// reused rather than duplicated).
//
// A lost CAS here — like beginProvisioning's own identical race above —
// always means another repository's own job already completed the
// identical aggregation outcome first (a benign, expected race under
// concurrent WORKSPACE_PROVISION jobs for the same WorkspaceSet, never a
// genuine conflict): "someone else already finished it, nothing more to
// do", not an error.
func (h *Handler) aggregateWorkspaceSet(ctx context.Context, tx ports.Tx, workspaceSetID, familyID string) error {
	set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, familyID)
	if err != nil {
		return err
	}
	// Only ever advance a set that is still PROVISIONING. REQUESTED cannot
	// reach this point (this job's own beginProvisioning call, above,
	// already ran first — it either performed the REQUESTED->PROVISIONING
	// transition itself or found the set already past REQUESTED). READY/
	// BLOCKED/any other state is a terminal outcome this task's own scope
	// never revisits: once a family is BLOCKED by one failed repository, it
	// can never silently become READY just because every other sibling's
	// own job happens to finish afterward (V3-06 has no path back out of
	// either terminal state — that reconciliation, if any, is entirely
	// later scope).
	if set.State != workspace.WorkspaceSetProvisioning {
		return nil
	}

	workspaces, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, workspaceSetID)
	if err != nil {
		return err
	}
	familyScopes, err := tx.Work().ListFamilyRepositoryScopes(ctx, familyID)
	if err != nil {
		return err
	}
	family, err := tx.Work().GetTaskFamily(ctx, familyID)
	if err != nil {
		return err
	}
	// Cheap, pure (no I/O) static safety net — mirrors
	// internal/app/work.CreateRootWorkItem's own identical "redundant with
	// this handler's own inline checks, kept anyway" reasoning for calling
	// work.ValidateFamilyScopes there.
	if err := workspace.ValidateRepositoryWorkspaces(set, family, familyScopes, workspaces); err != nil {
		return fmt.Errorf("workspaceprovision: workspace set %s invariant violation: %w", workspaceSetID, err)
	}

	required := make(map[project.RepositoryID]struct{}, len(familyScopes))
	for _, scope := range familyScopes {
		required[scope.RepositoryID()] = struct{}{}
	}

	ready := make(map[project.RepositoryID]workspace.RepositoryWorkspace, len(workspaces))
	anyFailed := false
	for _, rw := range workspaces {
		if _, wanted := required[rw.RepositoryID]; !wanted {
			continue
		}
		switch rw.State {
		case workspace.RepositoryWorkspaceReady:
			ready[rw.RepositoryID] = rw
		case workspace.RepositoryWorkspaceFailed:
			anyFailed = true
		}
	}

	if anyFailed {
		_, err := tx.Work().TransitionWorkspaceSetState(ctx, ports.TransitionWorkspaceSetStateRequest{
			WorkspaceSetID: string(set.ID), ExpectedState: workspace.WorkspaceSetProvisioning, ExpectedVersion: set.Version,
			NextState: workspace.WorkspaceSetBlocked,
		})
		if errors.Is(err, ports.ErrOptimisticConflict) {
			return nil
		}
		return err
	}

	if len(ready) < len(required) {
		// Still provisioning: at least one required repository has no
		// READY row yet (either no row at all, or its own job has not run
		// yet). Nothing more to do until that repository's own job
		// finishes and re-aggregates.
		return nil
	}

	entries := make([]workspace.Revision, 0, len(ready))
	for repositoryID, rw := range ready {
		entries = append(entries, workspace.Revision{
			RepositoryID: repositoryID, VCSObjectID: rw.BaseRevision, WorkspaceGeneration: rw.Generation,
		})
	}
	revisionSet, err := workspace.NewRevisionSet(entries)
	if err != nil {
		return fmt.Errorf("workspaceprovision: compute base revision set for workspace set %s: %w", workspaceSetID, err)
	}

	_, err = tx.Work().TransitionWorkspaceSetState(ctx, ports.TransitionWorkspaceSetStateRequest{
		WorkspaceSetID: string(set.ID), ExpectedState: workspace.WorkspaceSetProvisioning, ExpectedVersion: set.Version,
		NextState: workspace.WorkspaceSetReady, BaseRevisionSet: &revisionSet,
	})
	if errors.Is(err, ports.ErrOptimisticConflict) {
		return nil
	}
	return err
}
