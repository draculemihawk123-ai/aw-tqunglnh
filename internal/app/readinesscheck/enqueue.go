package readinesscheck

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
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
}

// EnqueueBaselineEvidenceJob enqueues one BASELINE_EVIDENCE durable job for
// req's own RepositoryWorkspace, composed inside the caller's own
// already-open ports.Tx — the same Jobs().EnqueueJob composition
// internal/app/catalog.RegisterRepository already establishes for
// RepositoryProbeJobKind, so a caller that also writes other state in the
// same transaction (e.g. the RepositoryWorkspace row itself) gets the
// identical atomicity guarantee for free. IdempotencyKey is deterministic
// from RepositoryWorkspaceID alone: this task's own scope enqueues at most
// one BASELINE_EVIDENCE job per RepositoryWorkspace, ever (see this
// package's own doc comment on Handler.finish for why a second job for the
// same RepositoryWorkspace, should a future task ever mint one, is still
// handled correctly rather than merely prevented).
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
		IdempotencyKey: "baseline-evidence-" + req.RepositoryWorkspaceID,
	})
}
