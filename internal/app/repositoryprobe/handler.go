// Package repositoryprobe is V3-02's workerpool.Handler for the
// REPOSITORY_PROBE durable job RegisterRepository/RetryRepositoryProbe
// enqueue (internal/app/catalog.RepositoryProbeJobKind,
// docs/design/05-v3-project-workspace.md V3-02): claims a job,
// CAS-transitions the Repository REGISTERING (or already PROBING, on a
// crash-recovery reclaim) -> PROBING, runs a read-only durable probe
// against the Repository's own registered local_path entirely OUTSIDE
// any open database transaction — internal/app/workflowcompiler.CompileAndResolve's
// own doc comment documents this exact "real I/O never runs inside an
// open write transaction" rule for a different aggregate, and
// internal/archtest.TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess
// is this codebase's own enforced precedent for the identical discipline
// elsewhere — then commits a second transaction that CAS-transitions
// PROBING -> ACTIVE (creating any discovered Component candidates in the
// same transaction, per HE-06-M05's "onboarding MUST tạo hoặc xác nhận
// topology Repository -> Component -> EngineeringPack") or
// PROBING -> BLOCKED (recording a typed apperror.Code as
// LastProbeErrorCode), and always writes one repository_probe_attempts
// row for that outcome.
//
// Handle returns nil (letting workerpool.Pool mark the durable job itself
// SUCCEEDED) for BOTH outcomes: a probe that correctly determines a
// repository is unusable is the job doing its one job correctly, never a
// job failure (V3-02's own "job success vs. business outcome"
// distinction, mirroring how every other durable-job handler in this
// codebase already treats a business-level result differently from a
// technical one). Handle returns a non-nil error only for a genuine
// unexpected condition — a malformed job payload, a persistence failure,
// a ports.RepositoryProber implementation that violates its own contract
// by returning an unclassified error — leaving the job un-completed for
// workerpool's own lease-expiry/retry mechanism (see
// workerpool.Pool.runJob's own doc comment: "leave it un-completed; lease
// expiry + recovery reaper decide retry vs DEAD").
package repositoryprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// Handler implements workerpool.Handler for
// internal/app/catalog.RepositoryProbeJobKind. It depends only on
// ports.RepositoryProber (never a concrete adapter package directly) —
// internal/adapters/repoprobe is the production implementation a
// composition root wires in.
type Handler struct {
	uow    ports.UnitOfWork
	ids    idsource.Source
	prober ports.RepositoryProber
}

// New returns a ready-to-register Handler.
func New(uow ports.UnitOfWork, ids idsource.Source, prober ports.RepositoryProber) *Handler {
	return &Handler{uow: uow, ids: ids, prober: prober}
}

var _ workerpool.Handler = (*Handler)(nil)

// jobPayload mirrors the exact JSON shape
// internal/app/catalog.RegisterRepository/RetryRepositoryProbe both
// marshal: {"repositoryId": "...", "projectId": "..."}.
type jobPayload struct {
	RepositoryID string `json:"repositoryId"`
	ProjectID    string `json:"projectId"`
}

// Handle implements workerpool.Handler. See this package's own doc
// comment for the full contract.
func (h *Handler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload jobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("repositoryprobe: unmarshal job %s payload: %w", job.ID, err)
	}
	if payload.RepositoryID == "" || payload.ProjectID == "" {
		return fmt.Errorf("repositoryprobe: job %s payload missing repositoryId/projectId", job.ID)
	}

	repo, err := h.loadRepository(ctx, payload.RepositoryID)
	if err != nil {
		return fmt.Errorf("repositoryprobe: load repository %s: %w", payload.RepositoryID, err)
	}

	// Idempotent early-return ("active probe idempotent",
	// docs/design/01-system-design.md §6.1): a previous claim of this
	// exact job already drove the Repository to a resolved terminal
	// state, then crashed before this job was itself marked SUCCEEDED (the
	// window between the second transaction's commit below and
	// workerpool.Pool's own CompleteJob call). That earlier attempt
	// already did everything there is to do — re-running the git I/O or
	// the transactions again would be redundant at best (re-discovering
	// Components against an already-ACTIVE repository) and unsafe at
	// worst. DISABLED is included defensively: an operator-disabled
	// Repository has nothing left for a stale probe claim to usefully do.
	switch repo.Status {
	case project.RepositoryActive, project.RepositoryBlocked, project.RepositoryDisabled:
		return nil
	}

	if repo.Status == project.RepositoryRegistering {
		repo, err = h.beginProbing(ctx, repo)
		if err != nil {
			return fmt.Errorf("repositoryprobe: begin probing repository %s: %w", repo.ID, err)
		}
	}
	if repo.Status != project.RepositoryProbing {
		// Only REGISTERING and PROBING are reachable above (the switch
		// already returned for every other status) — REGISTERING only
		// ever transitions to PROBING or fails outright above, so this is
		// a genuine, unrecoverable data-integrity condition, not a
		// business outcome this handler can classify as BLOCKED.
		return fmt.Errorf("repositoryprobe: repository %s is %s, not REGISTERING or PROBING", repo.ID, repo.Status)
	}

	evidence, probeErr := h.prober.Probe(ctx, repo.RemoteLocator, repo.DefaultRef)
	if probeErr == nil {
		return h.finishActive(ctx, job, repo, evidence)
	}
	var appErr *apperror.Error
	if !errors.As(probeErr, &appErr) {
		// Contract violation: ports.RepositoryProber promises every
		// failure is a classified *apperror.Error (environment vs.
		// validation/business). Treat this as a genuine handler failure
		// rather than silently guessing a classification and writing a
		// possibly-wrong LastProbeErrorCode — the job is left un-completed
		// for retry, matching every other unexpected-error path here.
		return fmt.Errorf("repositoryprobe: probe returned an unclassified error: %w", probeErr)
	}
	return h.finishBlocked(ctx, job, repo, appErr)
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

// beginProbing CAS-transitions repo REGISTERING -> PROBING in its own
// transaction, then returns — the real git I/O the caller runs next
// happens entirely outside this (or any) open transaction, per this
// package's own doc comment.
func (h *Handler) beginProbing(ctx context.Context, repo project.Repository) (project.Repository, error) {
	var updated project.Repository
	err := h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		result, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: string(repo.ID), ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: repo.Version,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		})
		updated = result
		return err
	})
	return updated, err
}

// finishActive CAS-transitions repo PROBING -> ACTIVE, creates one
// Component per evidence.Components discovery candidate and records the
// repository_probe_attempts row — all inside one transaction, so a crash
// partway through never leaves an ACTIVE repository with a missing
// attempt row or a partially created Component set.
func (h *Handler) finishActive(ctx context.Context, job ports.DurableJob, repo project.Repository, evidence ports.RepositoryProbeEvidence) error {
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		updated, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: string(repo.ID), ExpectedStatus: project.RepositoryProbing, ExpectedVersion: repo.Version,
			NextStatus: project.RepositoryActive, LastProbeErrorCode: nil,
		})
		if err != nil {
			return err
		}

		// A Repository can only ever complete PROBING -> ACTIVE once in
		// its whole lifecycle (project.CanTransitionRepositoryStatus has
		// no edge back into PROBING from ACTIVE — only BLOCKED->PROBING
		// exists), so this discovery step structurally can never run
		// twice for the same repository: no duplicate-path
		// UNIQUE(repository_id, path) conflict is possible here, and no
		// pre-check against already-existing Components is needed.
		for _, candidate := range evidence.Components {
			if _, err := tx.Catalog().CreateComponent(ctx, ports.CreateComponentRequest{
				ID: h.ids.NewID(), ProjectID: string(updated.ProjectID), RepositoryID: string(updated.ID),
				Name: candidate.Name, Path: candidate.Path, Kind: candidate.Kind,
			}); err != nil {
				return err
			}
		}

		result := project.RepositoryActive
		baseCommit := evidence.BaseCommit
		dirty := evidence.Dirty
		_, err = tx.Catalog().RecordRepositoryProbeAttempt(ctx, ports.RecordRepositoryProbeAttemptRequest{
			ID: h.ids.NewID(), ProjectID: string(updated.ProjectID), RepositoryID: string(updated.ID), JobID: string(job.ID),
			State: ports.RepositoryProbeAttemptSucceeded, Result: &result, BaseCommit: &baseCommit, Dirty: &dirty,
		})
		return err
	})
}

// finishBlocked CAS-transitions repo PROBING -> BLOCKED, setting
// LastProbeErrorCode to appErr's typed apperror.Code (never a raw
// SQL/provider error string, per go-core-spec §18) and records the
// repository_probe_attempts row — same one-transaction atomicity as
// finishActive above.
func (h *Handler) finishBlocked(ctx context.Context, job ports.DurableJob, repo project.Repository, appErr *apperror.Error) error {
	code := string(appErr.Code)
	message := appErr.Message
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		updated, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: string(repo.ID), ExpectedStatus: project.RepositoryProbing, ExpectedVersion: repo.Version,
			NextStatus: project.RepositoryBlocked, LastProbeErrorCode: &code,
		})
		if err != nil {
			return err
		}

		result := project.RepositoryBlocked
		_, err = tx.Catalog().RecordRepositoryProbeAttempt(ctx, ports.RecordRepositoryProbeAttemptRequest{
			ID: h.ids.NewID(), ProjectID: string(updated.ProjectID), RepositoryID: string(updated.ID), JobID: string(job.ID),
			State: ports.RepositoryProbeAttemptSucceeded, Result: &result, ErrorCode: &code, ErrorMessage: &message,
		})
		return err
	})
}
