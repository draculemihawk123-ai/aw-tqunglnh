package workspacereconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file is V11-01's operator command for "someone committed in the
// worktree outside aw" (docs/design/14-v11-bugfix.md, ADR-032): the worktree's
// HEAD moved past the revision aw recorded as the RepositoryWorkspace's
// CurrentRevision, so every MACHINE_GATE of a run pinned to the old revision
// refuses to evaluate. AdoptWorkspaceHead records the live HEAD as the
// CurrentRevision — deliberately NOT part of reconciliation (which decides
// whether a workspace may stay in service) and NOT automatic: adopting an outside
// commit puts code that never passed a gate into the history aw treats as its
// own, so it is an explicit, audited, typed operator command that fails closed on
// anything it cannot prove safe.
//
// It is synchronous (no durable job), unlike RequestWorkspaceReconciliation,
// because it is cheap and the operator needs the answer: the Git reads happen
// outside every transaction (go-core-spec §11.1), the single write is one short
// transaction that re-checks the fences the reads relied on.
//
// Preconditions, all of them checked, none assumed:
//   - the caller names the workspace version it observed (cmd.ExpectedVersion);
//   - the workspace is READY (a QUARANTINED one is refused with
//     ports.ErrWorkspaceQuarantined — only a recreate leaves quarantine);
//   - no attempt holds a live write lease on it;
//   - the worktree is clean (uncommitted changes are an attempt's, or the
//     operator's, work in progress — never silently folded into the history);
//   - HEAD descends from the recorded CurrentRevision (a fast-forward): after
//     a reset or rebase aw would drop commits it recorded;
//   - HEAD actually differs from CurrentRevision.

var (
	// ErrAdoptWorkspaceNotReady: the RepositoryWorkspace is neither READY nor
	// QUARANTINED (provisioning, releasing, released, failed): there is no
	// live worktree to adopt from.
	ErrAdoptWorkspaceNotReady = errors.New("workspacereconcile: repository workspace is not READY")
	// ErrAdoptWriterActive: an attempt holds a live write lease on the
	// workspace, so HEAD may still move under aw's own hands.
	ErrAdoptWriterActive = errors.New("workspacereconcile: repository workspace has an active writer")
	// ErrAdoptWorktreeDirty: the worktree has uncommitted changes.
	ErrAdoptWorktreeDirty = errors.New("workspacereconcile: worktree has uncommitted changes")
	// ErrAdoptHeadNotDescendant: HEAD is not a descendant of the recorded
	// CurrentRevision (a reset or a rebase happened).
	ErrAdoptHeadNotDescendant = errors.New("workspacereconcile: worktree HEAD does not descend from the recorded revision")
	// ErrAdoptNothingToAdopt: HEAD already equals the recorded CurrentRevision.
	ErrAdoptNothingToAdopt = errors.New("workspacereconcile: worktree HEAD already is the recorded revision")
)

const (
	// WorkspaceHeadAdoptedEventType is the domain event AdoptWorkspaceHead
	// appends (audit: the commits adopted, their authors, the operator).
	WorkspaceHeadAdoptedEventType     = "WorkspaceHeadAdopted"
	WorkspaceHeadAdoptedSchemaVersion = 1

	// maxAdoptedCommitsReported bounds the commits named in the result and in
	// the audit event; the total is always carried.
	maxAdoptedCommitsReported = 50
	maxAdoptedSubjectRunes    = 200
)

// AdoptWorkspaceHeadDeps is what AdoptWorkspaceHead depends on.
type AdoptWorkspaceHeadDeps struct {
	UnitOfWork ports.UnitOfWork
	// Auditor reads HEAD and its relation to the recorded revision
	// (internal/adapters/gitworktree.Provider in production).
	Auditor ports.WorkspaceHeadAuditor
	// Matcher redacts commit subjects and author names before they are stored
	// in the audit event or returned.
	Matcher redact.Matcher
}

// AdoptWorkspaceHeadRequest names the workspace to adopt HEAD for.
type AdoptWorkspaceHeadRequest struct {
	RepositoryWorkspaceID string
	ProjectID             string
}

// AdoptedCommit is one commit that adoption recorded.
type AdoptedCommit struct {
	CommitID    string `json:"commitId"`
	AuthorName  string `json:"authorName"`
	AuthorEmail string `json:"authorEmail"`
	Subject     string `json:"subject"`
}

// AdoptWorkspaceHeadResult is what AdoptWorkspaceHead returns (and what a
// replayed command receipt reconstructs).
type AdoptWorkspaceHeadResult struct {
	RepositoryWorkspaceID string          `json:"repositoryWorkspaceId"`
	ProjectID             string          `json:"projectId"`
	RepositoryID          string          `json:"repositoryId"`
	PreviousRevision      string          `json:"previousRevision"`
	AdoptedRevision       string          `json:"adoptedRevision"`
	AdoptedCommitCount    int             `json:"adoptedCommitCount"`
	AdoptedCommits        []AdoptedCommit `json:"adoptedCommits"`
	Version               uint64          `json:"version"`
}

// workspaceHeadAdoptedEventPayload is WorkspaceHeadAdopted v1's own shape.
type workspaceHeadAdoptedEventPayload struct {
	RepositoryWorkspaceID string          `json:"repositoryWorkspaceId"`
	ProjectID             string          `json:"projectId"`
	RepositoryID          string          `json:"repositoryId"`
	PreviousRevision      string          `json:"previousRevision"`
	AdoptedRevision       string          `json:"adoptedRevision"`
	AdoptedCommitCount    int             `json:"adoptedCommitCount"`
	AdoptedCommits        []AdoptedCommit `json:"adoptedCommits"`
	Actor                 string          `json:"actor"`
}

// AdoptWorkspaceHead is the public command: see this file's own doc comment.
// Follows V1-06's idempotent-command shape: a retry with the same
// IdempotencyKey and RequestHash replays the first call's result (no second
// adoption, no Git read); the same key with a different RequestHash is
// ports.ErrReceiptConflict.
func AdoptWorkspaceHead(ctx context.Context, deps AdoptWorkspaceHeadDeps, cmd ports.Command, req AdoptWorkspaceHeadRequest) (AdoptWorkspaceHeadResult, error) {
	if deps.UnitOfWork == nil || deps.Auditor == nil {
		return AdoptWorkspaceHeadResult{}, errors.New("workspacereconcile: AdoptWorkspaceHead requires UnitOfWork and Auditor")
	}
	if strings.TrimSpace(req.RepositoryWorkspaceID) == "" {
		return AdoptWorkspaceHeadResult{}, errors.New("workspacereconcile: RepositoryWorkspaceID is required")
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return AdoptWorkspaceHeadResult{}, errors.New("workspacereconcile: ProjectID is required")
	}
	if cmd.ExpectedVersion == 0 {
		return AdoptWorkspaceHeadResult{}, errors.New(
			"workspacereconcile: AdoptWorkspaceHead requires cmd.ExpectedVersion (the RepositoryWorkspace version the caller observed)")
	}

	// Phase 1 — read-only: replay, ownership, version, state, writer.
	var replayed *AdoptWorkspaceHeadResult
	var rw workspace.RepositoryWorkspace
	err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		existing, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existing.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			var result AdoptWorkspaceHeadResult
			if err := json.Unmarshal([]byte(existing.ResultJSON), &result); err != nil {
				return err
			}
			replayed = &result
			return nil
		}
		loaded, err := loadAdoptableWorkspace(ctx, tx, req, cmd.ExpectedVersion)
		rw = loaded
		return err
	})
	if err != nil {
		return AdoptWorkspaceHeadResult{}, err
	}
	if replayed != nil {
		return *replayed, nil
	}

	// Phase 2 — Git reads, outside every transaction.
	handle, err := ports.NewWorkspaceHandle(rw.Locator)
	if err != nil {
		return AdoptWorkspaceHeadResult{}, fmt.Errorf("repository workspace %s has an invalid locator: %w", rw.ID, err)
	}
	recorded := rw.CurrentRevision
	if recorded == "" {
		recorded = rw.BaseRevision
	}
	audit, err := deps.Auditor.AuditHead(ctx, ports.AuditHeadRequest{Handle: handle, PinnedRevision: recorded, CommitLimit: maxAdoptedCommitsReported})
	if err != nil {
		return AdoptWorkspaceHeadResult{}, fmt.Errorf("audit worktree HEAD of repository workspace %s: %w", rw.ID, err)
	}
	switch {
	case audit.Dirty:
		return AdoptWorkspaceHeadResult{}, fmt.Errorf("%w: commit or discard the changes in the worktree of repository workspace %s first", ErrAdoptWorktreeDirty, rw.ID)
	case !audit.FastForward:
		return AdoptWorkspaceHeadResult{}, fmt.Errorf(
			"%w: HEAD %s does not descend from the recorded revision %s; put the worktree back on the recorded revision or reconcile the workspace",
			ErrAdoptHeadNotDescendant, audit.Head, recorded)
	case audit.Head == recorded:
		return AdoptWorkspaceHeadResult{}, fmt.Errorf("%w: %s", ErrAdoptNothingToAdopt, recorded)
	}

	commits := make([]AdoptedCommit, 0, len(audit.Commits))
	for _, commit := range audit.Commits {
		commits = append(commits, AdoptedCommit{
			CommitID: commit.CommitID, AuthorName: boundedRedacted(deps.Matcher, commit.AuthorName, maxAdoptedSubjectRunes), AuthorEmail: boundedRedacted(deps.Matcher, commit.AuthorEmail, maxAdoptedSubjectRunes),
			Subject: boundedRedacted(deps.Matcher, commit.Subject, maxAdoptedSubjectRunes),
		})
	}

	// Phase 3 — one short write transaction that re-checks every fence the reads
	// relied on, advances the recorded revision, and writes the audit trail.
	var result AdoptWorkspaceHeadResult
	err = deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		existing, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existing.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(existing.ResultJSON), &result)
		}
		current, err := loadAdoptableWorkspace(ctx, tx, req, cmd.ExpectedVersion)
		if err != nil {
			return err
		}
		if err := tx.Work().AdvanceRepositoryWorkspaceRevision(ctx, ports.AdvanceRepositoryWorkspaceRevisionUpdate{
			RepositoryWorkspaceID: current.ID, ExpectedVersion: current.Version, Revision: audit.Head, OccurredAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}
		result = AdoptWorkspaceHeadResult{
			RepositoryWorkspaceID: string(current.ID), ProjectID: req.ProjectID, RepositoryID: string(current.RepositoryID),
			PreviousRevision: recorded, AdoptedRevision: audit.Head, AdoptedCommitCount: audit.TotalCommits,
			AdoptedCommits: commits, Version: current.Version + 1,
		}
		payload, err := json.Marshal(workspaceHeadAdoptedEventPayload{
			RepositoryWorkspaceID: result.RepositoryWorkspaceID, ProjectID: result.ProjectID, RepositoryID: result.RepositoryID,
			PreviousRevision: result.PreviousRevision, AdoptedRevision: result.AdoptedRevision,
			AdoptedCommitCount: result.AdoptedCommitCount, AdoptedCommits: result.AdoptedCommits, Actor: cmd.Actor,
		})
		if err != nil {
			return fmt.Errorf("marshal %s payload: %w", WorkspaceHeadAdoptedEventType, err)
		}
		// The aggregate is the freshly minted command id, not the
		// RepositoryWorkspace: that aggregate's own event stream
		// (REPOSITORY_WORKSPACE_QUARANTINED/RECREATED/RELEASED) owns its
		// sequence numbers, the same reason RequestWorkspaceReconciliation keys
		// its event by job id. A command id is unique, so Sequence 1 never collides.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-adopted", ProjectID: req.ProjectID,
			AggregateType: "RepositoryWorkspaceHeadAdoption", AggregateID: cmd.ID, Sequence: 1,
			EventType: WorkspaceHeadAdoptedEventType, SchemaVersion: WorkspaceHeadAdoptedSchemaVersion, PayloadJSON: string(payload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON), CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}

// loadAdoptableWorkspace loads the workspace and proves, inside tx, the
// preconditions that need no Git: project ownership, the version fence, READY
// (a quarantined workspace gets its own typed error) and no live writer.
func loadAdoptableWorkspace(ctx context.Context, tx ports.Tx, req AdoptWorkspaceHeadRequest, expectedVersion uint64) (workspace.RepositoryWorkspace, error) {
	record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, req.RepositoryWorkspaceID)
	if err != nil {
		return workspace.RepositoryWorkspace{}, err
	}
	rw := record.Workspace
	repo, err := tx.Catalog().GetRepository(ctx, string(rw.RepositoryID))
	if err != nil {
		return workspace.RepositoryWorkspace{}, err
	}
	if string(repo.ProjectID) != req.ProjectID {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("%w: repository workspace %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, rw.ID, repo.ProjectID, req.ProjectID)
	}
	if expectedVersion != rw.Version {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("%w: repository workspace %s expected version %d, currently %d",
			ports.ErrOptimisticConflict, rw.ID, expectedVersion, rw.Version)
	}
	switch rw.State {
	case workspace.RepositoryWorkspaceReady:
	case workspace.RepositoryWorkspaceQuarantined:
		return workspace.RepositoryWorkspace{}, fmt.Errorf("%w: repository workspace %s; reconcile it (a recreate starts a new generation) instead", ports.ErrWorkspaceQuarantined, rw.ID)
	default:
		return workspace.RepositoryWorkspace{}, fmt.Errorf("%w: repository workspace %s is %s", ErrAdoptWorkspaceNotReady, rw.ID, rw.State)
	}
	active, err := tx.Work().HasActiveWriteLease(ctx, []string{string(rw.ID)})
	if err != nil {
		return workspace.RepositoryWorkspace{}, err
	}
	if active {
		return workspace.RepositoryWorkspace{}, fmt.Errorf("%w: wait for the running attempt on repository workspace %s to finish, or cancel it", ErrAdoptWriterActive, rw.ID)
	}
	return rw, nil
}

// boundedRedacted redacts known secrets in text and caps it at limit runes.
func boundedRedacted(matcher redact.Matcher, text string, limit int) string {
	redacted, _ := matcher.Redact([]byte(text))
	text = string(redacted)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
