package workspacerelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ErrRepositoryWorkspaceQuarantinedDuringRelease is returned when a
// RepositoryWorkspace named by the release job's own payload has become
// QUARANTINED (V3-10) since RequestWorkspaceSetRelease accepted the
// request — this task's own "không release... quarantined workspace" bar
// (design doc V5-14 Thực hiện line), rechecked here against the row's own
// current state rather than trusted from request time, exactly like
// workspacereconcile.ExecuteWorkspaceReconciliation re-reads current state
// rather than trusting anything captured earlier. The whole
// ExecuteWorkspaceSetRelease call fails when this happens: any repository
// already released by this same call stays released (that state change is
// never undone — see this file's own idempotency discussion), but the
// WorkspaceSet itself is left short of RELEASED for an operator to
// investigate and resolve (e.g. via workspacereconcile's own RECREATE path
// for the offending repository) before retrying.
var ErrRepositoryWorkspaceQuarantinedDuringRelease = errors.New(
	"workspacerelease: repository workspace is quarantined, cannot be released")

// repositoryWorkspaceReleaseAction is ExecuteWorkspaceSetRelease's own
// pure, per-RepositoryWorkspace decision — deliberately independent of any
// real Provider/Lifecycle call so it is unit-testable without I/O,
// mirroring workspacereconcile.ClassifyReconciliation's identical style.
type repositoryWorkspaceReleaseAction string

const (
	// repositoryWorkspaceReleaseActionSkip: already RELEASED — an earlier,
	// crashed attempt of this exact job already committed this repository's
	// own release. Idempotent no-op.
	repositoryWorkspaceReleaseActionSkip repositoryWorkspaceReleaseAction = "SKIP"
	// repositoryWorkspaceReleaseActionRelease: READY — release it now via
	// ports.WorkspaceProvider.Release followed by
	// ports.WorkspaceLifecycle.ReleaseRepositoryWorkspace.
	repositoryWorkspaceReleaseActionRelease repositoryWorkspaceReleaseAction = "RELEASE"
	// repositoryWorkspaceReleaseActionBlocked: QUARANTINED — refuse the
	// whole release (ErrRepositoryWorkspaceQuarantinedDuringRelease).
	repositoryWorkspaceReleaseActionBlocked repositoryWorkspaceReleaseAction = "BLOCKED"
)

// classifyRepositoryWorkspaceRelease maps a RepositoryWorkspace's own
// current state to one of the three actions above. Any state other than
// READY/QUARANTINED/RELEASED (REQUESTED/PROVISIONING/RELEASING/FAILED) is
// a genuine inconsistency this job's own request-time eligibility checks
// (RequestWorkspaceSetRelease's "no active job" gate) should never let
// reach here — reported as an error rather than silently folded into one
// of the three real actions above.
func classifyRepositoryWorkspaceRelease(state workspace.RepositoryWorkspaceState) (repositoryWorkspaceReleaseAction, error) {
	switch state {
	case workspace.RepositoryWorkspaceReleased:
		return repositoryWorkspaceReleaseActionSkip, nil
	case workspace.RepositoryWorkspaceReady:
		return repositoryWorkspaceReleaseActionRelease, nil
	case workspace.RepositoryWorkspaceQuarantined:
		return repositoryWorkspaceReleaseActionBlocked, nil
	default:
		return "", fmt.Errorf("repository workspace state %s is not eligible for release execution", state)
	}
}

// ExecuteWorkspaceSetReleaseDeps bundles ExecuteWorkspaceSetRelease's
// collaborators — the same UnitOfWork/IDs/Provider/Lifecycle shape
// workspacereconcile.ExecuteWorkspaceReconciliationDeps already
// establishes for the sibling per-repository job.
type ExecuteWorkspaceSetReleaseDeps struct {
	UnitOfWork ports.UnitOfWork
	IDs        idsource.Source
	Provider   ports.WorkspaceProvider
	Lifecycle  ports.WorkspaceLifecycle
}

// ExecuteWorkspaceSetReleaseRequest is the parsed form of releaseJobPayload
// plus CorrelationID (job.ID), mirroring
// workspacereconcile.ExecuteWorkspaceReconciliationRequest's identical
// relationship to its own job payload.
type ExecuteWorkspaceSetReleaseRequest struct {
	WorkspaceSetID       string
	FamilyID             string
	ProjectID            string
	ExpectedVersion      uint64
	RepositoryWorkspaces []releaseRepositoryWorkspaceEntry
	CorrelationID        string
}

// ExecuteWorkspaceSetRelease is V5-14's own internal command — this
// package's own doc comment names it explicitly: "the internal handler
// that actually inspects/releases each repository's real physical
// workspace via ports.WorkspaceProvider/ports.WorkspaceLifecycle, records
// per-repo partial results, retries after a crash". It is never called by
// any API/CLI-shaped caller directly — RequestWorkspaceSetRelease (this
// package's own public command) is the only entry point, and
// Handler.Handle (below) is this function's only caller, mirroring
// workspacereconcile's identical public/internal split.
//
// # Per-repository partial result / retry schema
//
// The design doc's own "Dữ kiện phải khóa trước khi code" line names
// "per-repository release result/retry schema" as a contract this task
// must lock before coding. No new schema is introduced: each
// RepositoryWorkspace's own, already-persisted State column (READY ->
// RELEASED, V3-11's own ports.WorkspaceLifecycle.ReleaseRepositoryWorkspace)
// already IS that result/retry schema — classifyRepositoryWorkspaceRelease
// re-reads it fresh on every call (including a crash-recovery reclaim of
// this exact job) and skips whatever is already RELEASED, so a partial
// failure partway through RepositoryWorkspaces simply leaves the
// already-released entries released and retries only what is left. This
// mirrors ports.WorkspaceProvider.Release's own already-idempotent
// behavior (a no-op if the real workspace is already released on disk, see
// its own doc comment) at the RepositoryWorkspace-row layer.
//
// # WorkspaceSet-level RELEASING
//
// workspace.WorkspaceSetReleasing existed in the domain (0001_initial_schema.sql
// era) but nothing ever transitioned into it before this task — it is used
// here, and only here, as a durable "release is in progress" marker: the
// set's own State moves from its request-time value (READY/BLOCKED/FAILED
// — RequestWorkspaceSetRelease's own eligibility gate only ever refuses
// WorkspaceSetReleased outright, never REQUESTED/PROVISIONING, since "no
// active job" already excludes those) to RELEASING before any real
// RepositoryWorkspace is touched, and from RELEASING to RELEASED only once
// every entry classifies as SKIP. A crash between those two transitions
// resumes correctly: this function's own idempotent read at the top
// detects RELEASING and skips straight to the per-repository loop, reusing
// whatever version RELEASING already carries for the final CAS — no
// RepositoryWorkspaceReleasing counterpart exists (ports.WorkspaceLifecycle
// has no transition into it), so no such marker is used at that layer
// (the RepositoryWorkspace's own State already covers idempotency there,
// as above).
func ExecuteWorkspaceSetRelease(ctx context.Context, deps ExecuteWorkspaceSetReleaseDeps, request ExecuteWorkspaceSetReleaseRequest) error {
	if deps.UnitOfWork == nil || deps.IDs == nil || deps.Provider == nil || deps.Lifecycle == nil {
		return errors.New("workspacerelease: ExecuteWorkspaceSetRelease requires UnitOfWork/IDs/Provider/Lifecycle")
	}
	if request.WorkspaceSetID == "" || request.FamilyID == "" || request.ProjectID == "" || request.ExpectedVersion == 0 {
		return errors.New("workspacerelease: ExecuteWorkspaceSetRelease request is incomplete")
	}

	var set workspace.WorkspaceSet
	if err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		s, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, request.FamilyID)
		set = s
		return err
	}); err != nil {
		return fmt.Errorf("load workspace set for family %s: %w", request.FamilyID, err)
	}
	if string(set.ID) != request.WorkspaceSetID {
		return fmt.Errorf("workspace set for family %s is %s, want %s", request.FamilyID, set.ID, request.WorkspaceSetID)
	}

	switch set.State {
	case workspace.WorkspaceSetReleased:
		// Idempotent early-return: an earlier, crashed attempt of this exact
		// job already committed the final transition. Nothing more to do —
		// mirrors workspacereconcile.loadCurrentRepositoryWorkspace's own
		// "already resolved" early return.
		return nil
	case workspace.WorkspaceSetReleasing:
		// Resume: the READY/BLOCKED/FAILED -> RELEASING transition below was
		// already committed by an earlier attempt of this exact job.
	case workspace.WorkspaceSetReady, workspace.WorkspaceSetBlocked, workspace.WorkspaceSetFailed:
		releasing, err := transitionWorkspaceSetStateTx(ctx, deps.UnitOfWork, set.ID, set.State, set.Version, workspace.WorkspaceSetReleasing, nil)
		if err != nil {
			return fmt.Errorf("transition workspace set %s to RELEASING: %w", set.ID, err)
		}
		set = releasing
	default:
		return fmt.Errorf("workspace set %s is %s, not eligible for release execution", set.ID, set.State)
	}

	for _, entry := range request.RepositoryWorkspaces {
		if err := releaseOneRepositoryWorkspace(ctx, deps, request, entry); err != nil {
			return err
		}
	}

	if _, err := transitionWorkspaceSetStateTx(ctx, deps.UnitOfWork, set.ID, workspace.WorkspaceSetReleasing, set.Version, workspace.WorkspaceSetReleased, nil); err != nil {
		return fmt.Errorf("transition workspace set %s to RELEASED: %w", set.ID, err)
	}
	return nil
}

// transitionWorkspaceSetStateTx is a thin wrapper around
// ports.WorkRepository.TransitionWorkspaceSetState, opening its own
// WithSerializedWrite — every ExecuteWorkspaceSetRelease transition is a
// single, independent fenced CAS, never batched with anything else, the
// same "one mutation per transaction" shape
// workspacereconcile.executeBlock/executeRecreate already use for their own
// single-purpose Lifecycle calls.
func transitionWorkspaceSetStateTx(
	ctx context.Context, uow ports.UnitOfWork,
	workspaceSetID workspace.WorkspaceSetID, expectedState workspace.WorkspaceSetState, expectedVersion uint64,
	nextState workspace.WorkspaceSetState, baseRevisionSet *workspace.RevisionSet,
) (workspace.WorkspaceSet, error) {
	var result workspace.WorkspaceSet
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		updated, err := tx.Work().TransitionWorkspaceSetState(ctx, ports.TransitionWorkspaceSetStateRequest{
			WorkspaceSetID: string(workspaceSetID), ExpectedState: expectedState, ExpectedVersion: expectedVersion,
			NextState: nextState, BaseRevisionSet: baseRevisionSet,
		})
		result = updated
		return err
	})
	return result, err
}

// releaseOneRepositoryWorkspace re-reads entry's own RepositoryWorkspace
// fresh (never trusting anything captured at request time beyond the
// composite key used to look it up) and acts on
// classifyRepositoryWorkspaceRelease's decision. Real I/O
// (ports.WorkspaceProvider.Release) runs entirely outside any open
// transaction, mirroring every other job handler in this codebase.
func releaseOneRepositoryWorkspace(
	ctx context.Context, deps ExecuteWorkspaceSetReleaseDeps,
	request ExecuteWorkspaceSetReleaseRequest, entry releaseRepositoryWorkspaceEntry,
) error {
	var current workspace.RepositoryWorkspace
	if err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		rw, err := tx.Work().GetRepositoryWorkspace(ctx, request.WorkspaceSetID, entry.RepositoryID, entry.Generation)
		current = rw
		return err
	}); err != nil {
		return fmt.Errorf("load repository workspace %s: %w", entry.RepositoryWorkspaceID, err)
	}
	if string(current.ID) != entry.RepositoryWorkspaceID {
		return fmt.Errorf("repository workspace for (set %s, repository %s, generation %d) is %s, want %s",
			request.WorkspaceSetID, entry.RepositoryID, entry.Generation, current.ID, entry.RepositoryWorkspaceID)
	}

	action, err := classifyRepositoryWorkspaceRelease(current.State)
	if err != nil {
		return fmt.Errorf("repository workspace %s: %w", current.ID, err)
	}
	switch action {
	case repositoryWorkspaceReleaseActionSkip:
		return nil
	case repositoryWorkspaceReleaseActionBlocked:
		return fmt.Errorf("%w: repository workspace %s", ErrRepositoryWorkspaceQuarantinedDuringRelease, current.ID)
	}

	handle, err := ports.NewWorkspaceHandle(current.Locator)
	if err != nil {
		return fmt.Errorf("repository workspace %s has an invalid locator: %w", current.ID, err)
	}
	if err := deps.Provider.Release(ctx, handle); err != nil {
		return fmt.Errorf("release repository workspace %s: %w", current.ID, err)
	}

	return deps.Lifecycle.ReleaseRepositoryWorkspace(ctx, ports.ReleaseRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: current.ID, ExpectedVersion: current.Version,
		EventID: deps.IDs.NewID(), CorrelationID: request.CorrelationID, OccurredAt: time.Now().UTC(),
	})
}

// Handler implements workerpool.Handler for WorkspaceSetReleaseJobKind,
// mirroring workspacereconcile.Handler's identical shape: depends only on
// ports.UnitOfWork/ports.WorkspaceProvider/ports.WorkspaceLifecycle/
// idsource.Source, never a concrete adapter package directly.
type Handler struct {
	deps ExecuteWorkspaceSetReleaseDeps
}

// NewHandler returns a ready-to-register Handler. Named NewHandler, not
// New, because this package already exports a different New-shaped public
// surface at the command layer (RequestWorkspaceSetRelease) — unlike
// workspacereconcile, whose only other public symbol is a command function,
// not a same-named constructor, so New itself was unambiguous there.
func NewHandler(uow ports.UnitOfWork, ids idsource.Source, provider ports.WorkspaceProvider, lifecycle ports.WorkspaceLifecycle) *Handler {
	return &Handler{deps: ExecuteWorkspaceSetReleaseDeps{UnitOfWork: uow, IDs: ids, Provider: provider, Lifecycle: lifecycle}}
}

var _ workerpool.Handler = (*Handler)(nil)

// Handle implements workerpool.Handler: unmarshal the job payload and
// delegate to ExecuteWorkspaceSetRelease. Every error
// ExecuteWorkspaceSetRelease can return is a genuine, unexpected condition
// (a stale/missing WorkspaceSet or RepositoryWorkspace row, a quarantined
// repository, a provider contract violation) — any non-nil error here
// leaves the job un-completed for retry, exactly like
// workspacereconcile.Handler.Handle's identical discipline.
func (h *Handler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload releaseJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("workspacerelease: unmarshal job %s payload: %w", job.ID, err)
	}
	if payload.WorkspaceSetID == "" || payload.FamilyID == "" || payload.ProjectID == "" || payload.ExpectedVersion == 0 {
		return fmt.Errorf("workspacerelease: job %s payload is incomplete", job.ID)
	}

	if err := ExecuteWorkspaceSetRelease(ctx, h.deps, ExecuteWorkspaceSetReleaseRequest{
		WorkspaceSetID: payload.WorkspaceSetID, FamilyID: payload.FamilyID, ProjectID: payload.ProjectID,
		ExpectedVersion: payload.ExpectedVersion, RepositoryWorkspaces: payload.RepositoryWorkspaces,
		CorrelationID: string(job.ID),
	}); err != nil {
		return fmt.Errorf("workspacerelease: execute release for job %s: %w", job.ID, err)
	}
	return nil
}
