// Package releasesetcommit is V6-10E's own producer+consumer package for
// RequestReleaseSetLocalCommit (docs/design/08-v6-api-projections.md
// V6-10E; ADR-014, AK-ARCH-015C, GC-INV-26) — mirroring
// internal/app/workspacerelease's own established shape exactly: this
// file's own public RequestReleaseSetLocalCommit command is the "producer"
// (eligibility checks against real, already-persisted state, then an
// atomic intent+job+event+receipt write, never any real Git/filesystem
// call), execute.go's own ExecuteReleaseSetLocalCommit and Handler are the
// "consumer" (claims the job, does the real, crash-safe Git work entirely
// outside any transaction, then a separate fenced finalize transaction
// commits the result) — kept in ONE package, like workspacerelease, rather
// than split the way internal/app/work (pure ReleaseSet CRUD, explicitly
// documented as never importing real I/O) and this package are: a worker
// that actually calls a real ports.LocalCommitCreator/
// ports.LocalCommitWriteLeaseManager belongs structurally apart from
// internal/app/work's own "no reason to import os/os/exec" boundary
// (release_set.go's own doc comment).
//
// # Không làm (locked scope, V6-10E's own line)
//
// No push/fetch/PR/merge/rebase/force-push, ever, from anywhere in this
// package — there is no method on any port this package imports that could
// reach one even in principle (ports.LocalCommitCreator's own doc comment:
// "CreateLocalCommit is the only Git-mutating method this port — or any
// port in this codebase — declares"). No Git call inside
// RequestReleaseSetLocalCommit's own transaction: every real Git operation
// happens in execute.go, entirely outside any ports.UnitOfWork transaction.
// No implicit commit on ReleaseSet seal: sealing a ReleaseSet
// (internal/app/work.SealReleaseSet) never itself enqueues this package's
// own job — a caller must request a local commit as its own separate,
// explicit operation.
package releasesetcommit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ReleaseSetLocalCommitJobKind is durable_jobs.kind's value for the job
// RequestReleaseSetLocalCommit enqueues, following
// WorkspaceSetReleaseJobKind/WORKSPACE_RECONCILIATION's own established
// naming.
const ReleaseSetLocalCommitJobKind = "RELEASE_SET_LOCAL_COMMIT"

// aggregateType names this package's own durable_jobs.aggregate_type /
// domain_events.aggregate_type value — every job/event this package
// produces is scoped to one ReleaseSetLocalCommit operation, identified by
// its own freshly minted ID (mirrors WorkspaceSetRelease's own identical
// "freshly minted ID as AggregateID" convention, commands.go).
const aggregateType = "ReleaseSetLocalCommit"

// defaultLocalCommitJobMaxClaims mirrors defaultReleaseJobMaxClaims's own
// identical choice for a single logical unit of crash-recoverable work.
const defaultLocalCommitJobMaxClaims = 5

var (
	// ErrReleaseSetEntryNotFound is returned when the target
	// RepositoryWorkspace's own RepositoryID names no entry in the
	// requested ReleaseSet — "request pins ReleaseSet entry" (V6-10E's own
	// Thực hiện line) requires that entry to actually exist.
	ErrReleaseSetEntryNotFound = errors.New("releasesetcommit: release set has no entry for this repository workspace's own repository")
	// ErrWorkspaceNotReady is returned when the target RepositoryWorkspace
	// is not currently READY.
	ErrWorkspaceNotReady = errors.New("releasesetcommit: repository workspace is not READY")
)

// RequestReleaseSetLocalCommitRequest is what a caller supplies to
// RequestReleaseSetLocalCommit. ExpectedReleaseSetVersion/
// ExpectedWorkspaceVersion are the fences a stale caller trips —
// "request pins ReleaseSet entry/version, workspace generation/fence"
// (V6-10E's own Thực hiện line): the caller states the exact version of
// each it last observed, and this command rejects a mismatch with
// ports.ErrOptimisticConflict before pinning anything.
type RequestReleaseSetLocalCommitRequest struct {
	ProjectID                 string
	ReleaseSetID              string
	ExpectedReleaseSetVersion uint64
	RepositoryWorkspaceID     string
	ExpectedWorkspaceVersion  uint64
	Message                   string
	AuthorName                string
	AuthorEmail               string
}

// RequestReleaseSetLocalCommitResult is what RequestReleaseSetLocalCommit
// returns (and what a replayed command-receipt reconstructs).
type RequestReleaseSetLocalCommitResult struct {
	ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
	ReleaseSetID            string `json:"releaseSetId"`
	RepositoryWorkspaceID   string `json:"repositoryWorkspaceId"`
	State                   string `json:"state"`
	JobID                   string `json:"jobId"`
	Marker                  string `json:"marker"`
}

// localCommitJobPayload is this package's own job payload — deliberately
// minimal: just the intent's own ID. Every other field a worker needs
// (ReleaseSetID/version, RepositoryWorkspaceID/generation, actor, message,
// marker, ...) is already durably pinned on the release_set_local_commits
// row itself, so the job payload never duplicates it (a worker always
// re-reads the row fresh — see execute.go's own doc comment for why that
// matters for crash-safety).
type localCommitJobPayload struct {
	ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
}

// RequestReleaseSetLocalCommit is the public command: see this package's
// own doc comment for the full producer/consumer contract. Follows the
// identical idempotent-command shape every command in this codebase
// follows: a retry with the same IdempotencyKey and RequestHash replays the
// first call's result; the same key with a different RequestHash is
// rejected as ports.ErrReceiptConflict.
func RequestReleaseSetLocalCommit(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req RequestReleaseSetLocalCommitRequest,
) (RequestReleaseSetLocalCommitResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return RequestReleaseSetLocalCommitResult{}, errors.New("releasesetcommit: ProjectID is required")
	}
	if strings.TrimSpace(req.ReleaseSetID) == "" {
		return RequestReleaseSetLocalCommitResult{}, errors.New("releasesetcommit: ReleaseSetID is required")
	}
	if req.ExpectedReleaseSetVersion == 0 {
		return RequestReleaseSetLocalCommitResult{}, errors.New(
			"releasesetcommit: ExpectedReleaseSetVersion is required — this is the fence that rejects a stale request")
	}
	if strings.TrimSpace(req.RepositoryWorkspaceID) == "" {
		return RequestReleaseSetLocalCommitResult{}, errors.New("releasesetcommit: RepositoryWorkspaceID is required")
	}
	if req.ExpectedWorkspaceVersion == 0 {
		return RequestReleaseSetLocalCommitResult{}, errors.New(
			"releasesetcommit: ExpectedWorkspaceVersion is required — this is the fence that rejects a stale request")
	}
	message := strings.TrimSpace(req.Message)
	authorName := strings.TrimSpace(req.AuthorName)
	authorEmail := strings.TrimSpace(req.AuthorEmail)
	if message == "" || authorName == "" || authorEmail == "" {
		return RequestReleaseSetLocalCommitResult{}, errors.New("releasesetcommit: Message, AuthorName and AuthorEmail are required")
	}

	var result RequestReleaseSetLocalCommitResult
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

		releaseSet, err := tx.Work().GetReleaseSet(ctx, req.ReleaseSetID)
		if err != nil {
			return err
		}
		if string(releaseSet.ProjectID) != req.ProjectID {
			return fmt.Errorf("%w: release set %s belongs to project %s, not %s",
				ports.ErrCrossProjectReference, req.ReleaseSetID, releaseSet.ProjectID, req.ProjectID)
		}
		if releaseSet.Version != req.ExpectedReleaseSetVersion {
			return fmt.Errorf("%w: release set %s expected version %d, currently %d",
				ports.ErrOptimisticConflict, releaseSet.ID, req.ExpectedReleaseSetVersion, releaseSet.Version)
		}

		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, req.RepositoryWorkspaceID)
		if err != nil {
			return err
		}
		if record.FamilyID != string(releaseSet.FamilyID) {
			return fmt.Errorf("%w: repository workspace %s belongs to family %s, not %s",
				ports.ErrCrossProjectReference, req.RepositoryWorkspaceID, record.FamilyID, releaseSet.FamilyID)
		}
		if record.Workspace.Version != req.ExpectedWorkspaceVersion {
			return fmt.Errorf("%w: repository workspace %s expected version %d, currently %d",
				ports.ErrOptimisticConflict, req.RepositoryWorkspaceID, req.ExpectedWorkspaceVersion, record.Workspace.Version)
		}
		if _, ok := releaseSet.ReleaseFor(record.Workspace.RepositoryID); !ok {
			return fmt.Errorf("%w: release set %s, repository %s", ErrReleaseSetEntryNotFound, releaseSet.ID, record.Workspace.RepositoryID)
		}
		if record.Workspace.State != workspace.RepositoryWorkspaceReady {
			return fmt.Errorf("%w: repository workspace %s is %s", ErrWorkspaceNotReady, req.RepositoryWorkspaceID, record.Workspace.State)
		}

		messageHash := computeMessageHash(message)
		marker := computeOperationMarker(
			string(releaseSet.ID), releaseSet.Version, req.RepositoryWorkspaceID, record.Workspace.Generation, cmd.Actor, messageHash,
		)

		intentID := ids.NewID()
		intent, err := workdomain.NewReleaseSetLocalCommit(
			workdomain.ReleaseSetLocalCommitID(intentID), project.ProjectID(req.ProjectID), releaseSet.ID, releaseSet.Version,
			string(record.Workspace.ID), record.Workspace.RepositoryID, record.Workspace.Generation, record.Workspace.Version,
			cmd.Actor, message, authorName, authorEmail, messageHash, marker, cmd.RequestedAt,
		)
		if err != nil {
			return err
		}
		stored, err := tx.Work().CreateReleaseSetLocalCommit(ctx, intent)
		if err != nil {
			return err
		}

		payload, err := json.Marshal(localCommitJobPayload{ReleaseSetLocalCommitID: string(stored.ID)})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", ReleaseSetLocalCommitJobKind, err)
		}
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(ids.NewID()), ProjectID: project.ProjectID(req.ProjectID), Kind: ReleaseSetLocalCommitJobKind,
			AggregateType: aggregateType, AggregateID: string(stored.ID), Payload: payload,
			AvailableAt: cmd.RequestedAt, MaxClaims: defaultLocalCommitJobMaxClaims,
			IdempotencyKey: "release-set-local-commit:" + string(stored.ID),
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(releaseSetLocalCommitRequestedEventPayload{
			ReleaseSetLocalCommitID: string(stored.ID), ReleaseSetID: string(releaseSet.ID),
			RepositoryWorkspaceID: req.RepositoryWorkspaceID, Marker: stored.Marker, JobID: string(job.ID),
		})
		if err != nil {
			return fmt.Errorf("marshal %s event payload: %w", ReleaseSetLocalCommitRequestedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-requested", ProjectID: req.ProjectID,
			AggregateType: aggregateType, AggregateID: string(stored.ID), Sequence: 1,
			EventType: ReleaseSetLocalCommitRequestedEventType, SchemaVersion: ReleaseSetLocalCommitRequestedSchemaVersion,
			PayloadJSON: string(eventPayload), CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = RequestReleaseSetLocalCommitResult{
			ReleaseSetLocalCommitID: string(stored.ID), ReleaseSetID: string(releaseSet.ID),
			RepositoryWorkspaceID: req.RepositoryWorkspaceID, State: string(stored.State),
			JobID: string(job.ID), Marker: stored.Marker,
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
