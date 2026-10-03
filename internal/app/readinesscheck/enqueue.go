package readinesscheck

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// BaselineEvidenceJobKind is durable_jobs.kind's value for the job
// Handler consumes — this package both enqueues and consumes it end to
// end (see this package's own doc comment for why, unlike
// RepositoryProbeJobKind/WorkspaceProvisionJobKind, this task does not
// hook a trigger into another already-merged package instead).
const BaselineEvidenceJobKind = "BASELINE_EVIDENCE"

// defaultBaselineEvidenceJobMaxClaims mirrors this codebase's own most
// common durable-job MaxClaims value for a single logical unit of work
// (see e.g. internal/app/catalog's own defaultProbeJobMaxClaims).
const defaultBaselineEvidenceJobMaxClaims = 3

// EnqueueBaselineEvidenceJobRequest is what a caller supplies to
// EnqueueBaselineEvidenceJob — everything Handler's own jobPayload needs.
type EnqueueBaselineEvidenceJobRequest struct {
	ProjectID             string
	RepositoryID          string
	WorkspaceSetID        string
	RepositoryWorkspaceID string
	AvailableAt           time.Time
	// ProfileVersion (V9-08) is the readiness profile version the job is asked
	// to vouch for; part of the idempotency key, so one baseline is enqueued per
	// RepositoryWorkspace per profile version, and a changed profile enqueues a
	// fresh one. The handler records the version it actually ran.
	ProfileVersion uint64
	// Nonce (V9-08) makes an operator-requested re-run (`aw repository
	// readiness verify`) a job of its own instead of the automatic one for the
	// same profile version; empty for the automatic trigger.
	Nonce string
}

// EnqueueBaselineEvidenceJob enqueues one BASELINE_EVIDENCE durable job for
// req's own RepositoryWorkspace, composed inside the caller's own
// already-open ports.Tx — the same Jobs().EnqueueJob composition
// internal/app/catalog.RegisterRepository already establishes for
// RepositoryProbeJobKind, so a caller that also writes other state in the
// same transaction (e.g. the RepositoryWorkspace row itself) gets the
// identical atomicity guarantee for free. IdempotencyKey is deterministic
// from RepositoryWorkspaceID and ProfileVersion (and Nonce, for an operator's
// re-run): one automatic baseline job per RepositoryWorkspace per profile
// version. Before V9-08 it was one per RepositoryWorkspace, ever, and nothing
// called this function outside tests; now workspace provisioning, a profile
// change and `aw repository readiness verify` do. A second job for the same
// RepositoryWorkspace is handled correctly (see Handler.finish), each one
// appending its own evidence.
func EnqueueBaselineEvidenceJob(ctx context.Context, tx ports.Tx, ids idsource.Source, req EnqueueBaselineEvidenceJobRequest) (ports.DurableJob, error) {
	if req.ProjectID == "" || req.RepositoryID == "" || req.WorkspaceSetID == "" || req.RepositoryWorkspaceID == "" {
		return ports.DurableJob{}, fmt.Errorf("readinesscheck: EnqueueBaselineEvidenceJob requires projectId/repositoryId/workspaceSetId/repositoryWorkspaceId")
	}
	payload, err := json.Marshal(jobPayload{
		ProjectID: req.ProjectID, RepositoryID: req.RepositoryID,
		WorkspaceSetID: req.WorkspaceSetID, RepositoryWorkspaceID: req.RepositoryWorkspaceID,
	})
	if err != nil {
		return ports.DurableJob{}, fmt.Errorf("readinesscheck: marshal %s job payload: %w", BaselineEvidenceJobKind, err)
	}
	return tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(ids.NewID()), ProjectID: project.ProjectID(req.ProjectID), Kind: BaselineEvidenceJobKind,
		AggregateType: "RepositoryWorkspace", AggregateID: req.RepositoryWorkspaceID, Payload: payload,
		AvailableAt: req.AvailableAt, MaxClaims: defaultBaselineEvidenceJobMaxClaims,
		IdempotencyKey: baselineJobIdempotencyKey(req),
	})
}

func baselineJobIdempotencyKey(req EnqueueBaselineEvidenceJobRequest) string {
	key := "baseline-evidence-" + req.RepositoryWorkspaceID
	if req.ProfileVersion > 0 {
		key += fmt.Sprintf("-p%d", req.ProfileVersion)
	}
	if req.Nonce != "" {
		key += "-" + req.Nonce
	}
	return key
}

// EnqueueBaselineForNewWorkspace enqueues the baseline of rw, a workspace that
// just became READY (V9-08: "baseline chạy khi tạo WorkspaceSet"), when its
// repository declared a readiness profile; with no profile there is nothing to
// check and no job is enqueued. It runs in the transaction that created rw, so
// a workspace is never READY without its baseline being on its way.
func EnqueueBaselineForNewWorkspace(ctx context.Context, tx ports.Tx, ids idsource.Source, projectID string, rw workspace.RepositoryWorkspace, availableAt time.Time) error {
	profile, err := loadProfile(ctx, tx, string(rw.RepositoryID))
	if err != nil || profile == nil {
		return err
	}
	_, err = EnqueueBaselineEvidenceJob(ctx, tx, ids, EnqueueBaselineEvidenceJobRequest{
		ProjectID: projectID, RepositoryID: string(rw.RepositoryID), WorkspaceSetID: string(rw.WorkspaceSetID),
		RepositoryWorkspaceID: string(rw.ID), AvailableAt: availableAt, ProfileVersion: profile.Version,
	})
	return err
}

// EnqueueBaselineForRepositoryWorkspaces enqueues a baseline job for every READY
// RepositoryWorkspace of repositoryID, inside the caller's transaction, and
// returns how many it enqueued. It is what a changed readiness profile and an
// operator's `verify` use; a RepositoryWorkspace that is not READY yet is
// skipped, because workspace provisioning enqueues its baseline when it
// becomes READY.
func EnqueueBaselineForRepositoryWorkspaces(
	ctx context.Context, tx ports.Tx, ids idsource.Source, projectID, repositoryID string, profileVersion uint64, nonce string, availableAt time.Time,
) (int, error) {
	all, err := tx.Work().ListRepositoryWorkspacesForRepository(ctx, repositoryID)
	if err != nil {
		return 0, err
	}
	enqueued := 0
	for _, rw := range all {
		if rw.State != workspace.RepositoryWorkspaceReady {
			continue
		}
		if _, err := EnqueueBaselineEvidenceJob(ctx, tx, ids, EnqueueBaselineEvidenceJobRequest{
			ProjectID: projectID, RepositoryID: repositoryID, WorkspaceSetID: string(rw.WorkspaceSetID),
			RepositoryWorkspaceID: string(rw.ID), AvailableAt: availableAt, ProfileVersion: profileVersion, Nonce: nonce,
		}); err != nil {
			return 0, err
		}
		enqueued++
	}
	return enqueued, nil
}
