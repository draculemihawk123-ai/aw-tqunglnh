// Package projectionrebuild is V6-09's own command+query package for
// RequestProjectionRebuild/GetProjectionRebuildStatus
// (docs/design/08-v6-api-projections.md V6-09) — mirroring
// internal/app/releasesetcommit's own "producer" shape (that package's own
// doc comment): eligibility checks against real, already-persisted state,
// then one atomic intent(+job+event+receipt) write, never any real rebuild
// work itself. Deliberately its own package rather than folded into
// internal/app/projection (V6-08/V6-08A's own live-consumer/classification
// concern, whose own doc comment already commits to "pure CRUD/CAS ... no
// method decides WHAT/WHEN to apply") or internal/app/runtime — this task's
// own new operation model is neither of those, it is a third, genuinely new
// concern with its own migration (0041_projection_rebuild_operations.sql)
// and its own port (ports.ProjectionRebuildRepository).
//
// # Không làm (locked scope, V6-09's own line)
//
// This package never does the actual rebuild work: RequestProjectionRebuild
// creates a REQUESTED operation+job+event+receipt, atomically, and nothing
// else. It never clears projection_rows, never edits
// projection_checkpoints.cursor, and never invokes a rebuild worker inline
// or otherwise — that worker is V6-09A, a separate, not-yet-built task
// this package has no dependency on. It never exposes an HTTP endpoint
// either — that is V6-09B, also not yet built.
package projectionrebuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// aggregateType names this package's own durable_jobs.aggregate_type /
// domain_events.aggregate_type value — every job/event this package
// produces is scoped to one ProjectionRebuildOperation, identified by its
// own freshly minted ID (mirrors releasesetcommit's own identical
// "freshly minted ID as AggregateID" convention).
const aggregateType = "ProjectionRebuildOperation"

// ProjectionRebuildJobKind is durable_jobs.kind's value for the job
// RequestProjectionRebuild enqueues — V6-09A's own not-yet-built worker is
// this job kind's only ever consumer.
const ProjectionRebuildJobKind = "PROJECTION_REBUILD"

// defaultProjectionRebuildJobMaxClaims mirrors
// releasesetcommit.defaultLocalCommitJobMaxClaims's own identical choice
// for a single logical unit of crash-recoverable work.
const defaultProjectionRebuildJobMaxClaims = 5

// projectionRebuildJobPayload is this package's own job payload —
// deliberately minimal (mirrors releasesetcommit's own
// localCommitJobPayload doc comment): every other field a worker needs is
// already durably pinned on the projection_rebuild_operations row itself,
// so the job payload never duplicates it — a worker always re-reads the
// row fresh.
type projectionRebuildJobPayload struct {
	OperationID    string `json:"operationId"`
	ProjectID      string `json:"projectId"`
	ProjectionName string `json:"projectionName"`
}

// activeOperationIDDetailKey is the apperror.Error.Details map key
// newActiveProjectionRebuildConflictError populates —
// ActiveProjectionRebuildOperationID's own only reader; kept unexported so
// nothing outside this package depends on the literal key name.
const activeOperationIDDetailKey = "activeOperationId"

// newActiveProjectionRebuildConflictError builds the typed conflict
// RequestProjectionRebuild returns when a NEW idempotency key is used
// while (projectID, projectionName) already has a nonterminal rebuild
// operation in progress — V6-09's own Thực hiện line: "new key while
// project projection has nonterminal operation returns typed conflict
// with active ID," so a caller/CLI/HTTP layer (V6-09B, not yet built) can
// show "operation X is already running" rather than a bare "conflict".
//
// Design choice (documented here per this task's own brief, after reading
// apperror.go and several of its existing app-layer callers):
// this wraps apperror.Error rather than inventing a second, parallel,
// carries-structured-data error shape. apperror.Error is this codebase's
// own EXISTING general mechanism for exactly this — its own Details field
// doc comment already promises "safe to log, return over the API, or show
// an operator" — even though, before this task, no application-command
// caller had actually populated Details for a business conflict yet (the
// one existing app-layer apperror.New call site, runtime/approval.go, uses
// it for a policy-denial with no structured payload; every OTHER bespoke
// command conflict in this codebase — releasesetcommit.ErrReleaseSetEntryNotFound,
// workspacerelease.ErrWorkspaceSetHasActiveJob — is a plain errors.New
// sentinel, human-readable text only, wrapped with fmt.Errorf("%w: ...")).
// Inventing a second, ad hoc structured-data error type alongside
// apperror.Error's own pre-existing Details mechanism would just be two
// ways to do the same thing for no reason. A caller extracts the active ID
// with ActiveProjectionRebuildOperationID below, never by reaching into
// apperror.Error.Details directly — the map key name stays this package's
// own implementation detail, not a public contract.
func newActiveProjectionRebuildConflictError(projectID, projectionName, activeOperationID string) error {
	return apperror.New(
		apperror.CodeConflict,
		fmt.Sprintf("project %s projection %s already has an active (nonterminal) rebuild operation %s",
			projectID, projectionName, activeOperationID),
		false,
	).WithDetails(map[string]string{activeOperationIDDetailKey: activeOperationID})
}

// ActiveProjectionRebuildOperationID extracts the already-active
// operation's own ID from an error RequestProjectionRebuild returned, when
// err is (or wraps) the active-rebuild conflict
// newActiveProjectionRebuildConflictError builds above. Returns ("",
// false) for any other error, including a nil err or an *apperror.Error of
// some unrelated CodeConflict.
func ActiveProjectionRebuildOperationID(err error) (string, bool) {
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.CodeConflict {
		return "", false
	}
	id, ok := appErr.Details[activeOperationIDDetailKey]
	return id, ok
}

// RequestProjectionRebuildRequest is what a caller supplies to
// RequestProjectionRebuild.
type RequestProjectionRebuildRequest struct {
	ProjectID      string
	ProjectionName string
}

// RequestProjectionRebuildResult is what RequestProjectionRebuild returns
// (and what a replayed command receipt reconstructs).
type RequestProjectionRebuildResult struct {
	OperationID    string `json:"operationId"`
	ProjectID      string `json:"projectId"`
	ProjectionName string `json:"projectionName"`
	Phase          string `json:"phase"`
	JobID          string `json:"jobId"`
}

// RequestProjectionRebuild is the public command: see this package's own
// doc comment for the full contract. Follows the identical idempotent-
// command shape every command in this codebase follows: a retry with the
// same IdempotencyKey and RequestHash replays the first call's result
// (same OperationID, never a second operation); the same key with a
// different RequestHash is rejected as ports.ErrReceiptConflict. A NEW key
// while (ProjectID, ProjectionName) already has a nonterminal rebuild
// operation is rejected with the typed conflict
// newActiveProjectionRebuildConflictError builds, carrying that active
// operation's own ID.
func RequestProjectionRebuild(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req RequestProjectionRebuildRequest,
) (RequestProjectionRebuildResult, error) {
	projectID := strings.TrimSpace(req.ProjectID)
	projectionName := strings.TrimSpace(req.ProjectionName)
	if projectID == "" {
		return RequestProjectionRebuildResult{}, errors.New("projectionrebuild: ProjectID is required")
	}
	if projectionName == "" {
		return RequestProjectionRebuildResult{}, errors.New("projectionrebuild: ProjectionName is required")
	}

	var result RequestProjectionRebuildResult
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

		// Eligibility: at most one nonterminal rebuild operation per
		// (ProjectID, ProjectionName) — see
		// ports.ProjectionRebuildRepository.GetActiveOperation's own doc
		// comment and migration 0041's own partial unique index for the
		// two-layer (application check here, schema backstop there)
		// enforcement this relies on.
		active, hasActive, err := tx.ProjectionRebuilds().GetActiveOperation(ctx, projectID, projectionName)
		if err != nil {
			return err
		}
		if hasActive {
			return newActiveProjectionRebuildConflictError(projectID, projectionName, active.ID)
		}

		operationID := ids.NewID()
		jobID := ports.JobID(ids.NewID())

		payload, err := json.Marshal(projectionRebuildJobPayload{
			OperationID: operationID, ProjectID: projectID, ProjectionName: projectionName,
		})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", ProjectionRebuildJobKind, err)
		}

		stored, err := tx.ProjectionRebuilds().CreateOperation(ctx, ports.ProjectionRebuildOperation{
			ID: operationID, ProjectID: projectID, ProjectionName: projectionName, Phase: ports.ProjectionRebuildRequested,
			JobID: string(jobID), RequestedAt: cmd.RequestedAt, UpdatedAt: cmd.RequestedAt, Version: 1,
		})
		if err != nil {
			return err
		}

		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: jobID, ProjectID: project.ProjectID(projectID), Kind: ProjectionRebuildJobKind,
			AggregateType: aggregateType, AggregateID: stored.ID, Payload: payload,
			AvailableAt: cmd.RequestedAt, MaxClaims: defaultProjectionRebuildJobMaxClaims,
			IdempotencyKey: "projection-rebuild:" + stored.ID,
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(projectionRebuildRequestedEventPayload{
			OperationID: stored.ID, ProjectID: projectID, ProjectionName: projectionName, JobID: string(job.ID),
		})
		if err != nil {
			return fmt.Errorf("marshal %s event payload: %w", ProjectionRebuildRequestedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-requested", ProjectID: projectID,
			AggregateType: aggregateType, AggregateID: stored.ID, Sequence: 1,
			EventType: ProjectionRebuildRequestedEventType, SchemaVersion: ProjectionRebuildRequestedSchemaVersion,
			PayloadJSON: string(eventPayload), CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = RequestProjectionRebuildResult{
			OperationID: stored.ID, ProjectID: projectID, ProjectionName: projectionName,
			Phase: string(stored.Phase), JobID: string(job.ID),
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

// ProjectionRebuildStatus is GetProjectionRebuildStatus's own result shape
// — a plain, wire-friendly mirror of ports.ProjectionRebuildOperation
// (never the port type itself, the same "query returns its own DTO, not
// the persisted record" convention work.ReleaseSetLocalCommitStatus
// already follows). W0/ShadowGeneration/ShadowCursor/CutoverCursor/
// ErrorCode/ErrorMessage are only ever populated once V6-09A's own
// not-yet-built worker actually reaches the phase that first sets each —
// this type does not narrow or re-derive that, it just carries whatever
// the row currently holds.
type ProjectionRebuildStatus struct {
	OperationID      string  `json:"operationId"`
	ProjectID        string  `json:"projectId"`
	ProjectionName   string  `json:"projectionName"`
	Phase            string  `json:"phase"`
	W0               *uint64 `json:"w0,omitempty"`
	ShadowGeneration *uint64 `json:"shadowGeneration,omitempty"`
	ShadowCursor     *uint64 `json:"shadowCursor,omitempty"`
	CutoverCursor    *uint64 `json:"cutoverCursor,omitempty"`
	// ErrorCode/ErrorMessage are the safe (non-leaking) failure summary
	// V6-09's own Thực hiện line requires the status query to record for a
	// failed operation — see ports.ProjectionRebuildOperation's own doc
	// comment on the same two fields for what "safe" means here.
	ErrorCode    string    `json:"errorCode,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	JobID        string    `json:"jobId"`
	RequestedAt  time.Time `json:"requestedAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Version      uint64    `json:"version"`
}

func projectionRebuildStatus(op ports.ProjectionRebuildOperation) ProjectionRebuildStatus {
	return ProjectionRebuildStatus{
		OperationID: op.ID, ProjectID: op.ProjectID, ProjectionName: op.ProjectionName, Phase: string(op.Phase),
		W0: op.W0, ShadowGeneration: op.ShadowGeneration, ShadowCursor: op.ShadowCursor, CutoverCursor: op.CutoverCursor,
		ErrorCode: op.ErrorCode, ErrorMessage: op.ErrorMessage, JobID: op.JobID,
		RequestedAt: op.RequestedAt, UpdatedAt: op.UpdatedAt, Version: op.Version,
	}
}

// GetProjectionRebuildStatus returns operationID's own current, exact
// status — REQUESTED (this task's own only ever-written phase) or
// whatever V6-09A's own not-yet-built worker has since progressed it to —
// or ports.ErrPersistenceNotFound. This is a plain, per-operation EXACT
// lookup by ID (V6-09's own "exact operation lookup" Verify line): it
// never infers "the latest" operation for a project/projection pair —
// GetActiveOperation (ports.ProjectionRebuildRepository) exists only for
// RequestProjectionRebuild's own internal eligibility check above, never
// exposed as a public query by this task.
func GetProjectionRebuildStatus(ctx context.Context, uow ports.UnitOfWork, operationID string) (ProjectionRebuildStatus, error) {
	if strings.TrimSpace(operationID) == "" {
		return ProjectionRebuildStatus{}, errors.New("projectionrebuild: OperationID is required")
	}
	var status ProjectionRebuildStatus
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		op, err := tx.ProjectionRebuilds().GetOperation(ctx, operationID)
		if err != nil {
			return err
		}
		status = projectionRebuildStatus(op)
		return nil
	})
	return status, err
}
