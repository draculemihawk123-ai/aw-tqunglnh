package workspacereconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file is V12-20's "Sync with base branch" (docs/design/15-v12-ui-operations.md, UX-24, ADR-055). After the family's work was
// merged on the forge (a PR merged on GitHub), the family branch still stood on the old commit, and the operator ran
// `git merge --ff-only` in the worktree and then adopt-head.sh. SyncWorkspaceWithBase does it in one audited step: fetch the base
// branch from origin, fast-forward the worktree (never a merge, never a push), and record the new HEAD through AdoptWorkspaceHead
// (its WorkspaceHeadAdopted event is the audit). A diverged branch or a dirty worktree is reported and nothing moves.
//
// Preconditions are AdoptWorkspaceHead's (READY, no active writer, the version the caller observed), plus: the worktree's HEAD is
// the revision aw recorded — commits made outside aw are adopted on their own first, so a sync never folds them in unseen.

// ErrSyncHeadNotRecorded: the worktree HEAD is not the revision aw recorded.
var ErrSyncHeadNotRecorded = errors.New("workspacereconcile: worktree HEAD is not the revision aw recorded; adopt those commits first")

// SyncWorkspaceWithBaseDeps is what SyncWorkspaceWithBase depends on.
type SyncWorkspaceWithBaseDeps struct {
	Adopt  AdoptWorkspaceHeadDeps
	Syncer ports.WorkspaceBaseSyncer
}

// SyncWorkspaceWithBaseResult is the sync and, when the worktree moved, the adoption that recorded it.
type SyncWorkspaceWithBaseResult struct {
	RepositoryWorkspaceID string                    `json:"repositoryWorkspaceId"`
	Sync                  ports.BaseSyncResult      `json:"sync"`
	Adopted               *AdoptWorkspaceHeadResult `json:"adopted,omitempty"`
	// Version is the repository workspace's version after the call.
	Version uint64 `json:"version"`
}

// SyncWorkspaceWithBase is the public command (see this file's doc comment). Idempotent by cmd's receipt.
func SyncWorkspaceWithBase(ctx context.Context, deps SyncWorkspaceWithBaseDeps, cmd ports.Command, req AdoptWorkspaceHeadRequest) (SyncWorkspaceWithBaseResult, error) {
	uow := deps.Adopt.UnitOfWork
	if uow == nil || deps.Adopt.Auditor == nil || deps.Syncer == nil {
		return SyncWorkspaceWithBaseResult{}, errors.New("workspacereconcile: SyncWorkspaceWithBase requires UnitOfWork, Auditor and Syncer")
	}
	if cmd.ExpectedVersion == 0 {
		return SyncWorkspaceWithBaseResult{}, errors.New("workspacereconcile: SyncWorkspaceWithBase requires cmd.ExpectedVersion")
	}
	var replayed *SyncWorkspaceWithBaseResult
	var rw workspace.RepositoryWorkspace
	var baseRef string
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		existing, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existing.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			var result SyncWorkspaceWithBaseResult
			if err := json.Unmarshal([]byte(existing.ResultJSON), &result); err != nil {
				return err
			}
			replayed = &result
			return nil
		}
		if rw, err = loadAdoptableWorkspace(ctx, tx, req, cmd.ExpectedVersion); err != nil {
			return err
		}
		repo, err := tx.Catalog().GetRepository(ctx, string(rw.RepositoryID))
		baseRef = repo.DefaultRef
		return err
	}); err != nil {
		return SyncWorkspaceWithBaseResult{}, err
	}
	if replayed != nil {
		return *replayed, nil
	}
	if strings.TrimSpace(baseRef) == "" {
		return SyncWorkspaceWithBaseResult{}, fmt.Errorf("%w: repository %s has no default ref", ports.ErrPersistenceNotFound, rw.RepositoryID)
	}
	handle, err := ports.NewWorkspaceHandle(rw.Locator)
	if err != nil {
		return SyncWorkspaceWithBaseResult{}, err
	}
	recorded := rw.CurrentRevision
	if recorded == "" {
		recorded = rw.BaseRevision
	}
	audit, err := deps.Adopt.Auditor.AuditHead(ctx, ports.AuditHeadRequest{Handle: handle, PinnedRevision: recorded, CommitLimit: 1})
	if err != nil {
		return SyncWorkspaceWithBaseResult{}, err
	}
	if audit.Head != recorded {
		return SyncWorkspaceWithBaseResult{}, fmt.Errorf("%w: HEAD %s, recorded %s", ErrSyncHeadNotRecorded, audit.Head, recorded)
	}

	sync, err := deps.Syncer.SyncWithBase(ctx, ports.BaseSyncRequest{Handle: handle, BaseRef: baseRef, Fetch: true})
	if err != nil {
		return SyncWorkspaceWithBaseResult{}, err
	}
	result := SyncWorkspaceWithBaseResult{RepositoryWorkspaceID: string(rw.ID), Sync: sync, Version: rw.Version}
	if sync.Outcome == ports.BaseSyncFastForwarded {
		adoptCmd := cmd
		adoptCmd.IdempotencyKey, adoptCmd.ID, adoptCmd.Type = cmd.IdempotencyKey+":adopt", cmd.ID+"-adopt", "AdoptWorkspaceHead"
		adopted, err := AdoptWorkspaceHead(ctx, deps.Adopt, adoptCmd, req)
		if err != nil {
			// The worktree moved but aw did not record it: adopt-head records it (the sync can be sent again too).
			return SyncWorkspaceWithBaseResult{}, fmt.Errorf("the worktree was fast-forwarded to %s but recording it failed (run adopt-head): %w", sync.Head, err)
		}
		result.Adopted, result.Version = &adopted, adopted.Version
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return SyncWorkspaceWithBaseResult{}, err
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
			RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON), CreatedAt: cmd.RequestedAt,
		})
	}); err != nil {
		return SyncWorkspaceWithBaseResult{}, err
	}
	return result, nil
}

// GetWorkspaceBaseStatus reads how a repository workspace relates to its repository's base branch — ahead, behind, merged — without
// fetching and without changing anything.
func GetWorkspaceBaseStatus(ctx context.Context, uow ports.UnitOfWork, syncer ports.WorkspaceBaseSyncer, projectID, repositoryWorkspaceID string) (ports.BaseSyncResult, error) {
	var locator, baseRef string
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, repositoryWorkspaceID)
		if err != nil {
			return err
		}
		repo, err := tx.Catalog().GetRepository(ctx, string(record.Workspace.RepositoryID))
		if err != nil {
			return err
		}
		if string(repo.ProjectID) != projectID {
			return fmt.Errorf("%w: repository workspace %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
		}
		if record.Workspace.State != workspace.RepositoryWorkspaceReady {
			return fmt.Errorf("%w: repository workspace %s is %s", ErrAdoptWorkspaceNotReady, repositoryWorkspaceID, record.Workspace.State)
		}
		locator, baseRef = record.Workspace.Locator, repo.DefaultRef
		return nil
	}); err != nil {
		return ports.BaseSyncResult{}, err
	}
	handle, err := ports.NewWorkspaceHandle(locator)
	if err != nil {
		return ports.BaseSyncResult{}, err
	}
	return syncer.BaseStatus(ctx, ports.BaseSyncRequest{Handle: handle, BaseRef: baseRef})
}
