// Package awdocuments is V12-33 (docs/design/15-v12-ui-operations.md, UX-45, ADR-065): the per-task working documents a project
// keeps in aw (the AW location): read them from a worktree's aw-private directory, and export them into the repository's
// docs/features/ so they are not lost with the installation. Export writes into the worktree, so it needs the worktree READY and no
// attempt holding its write lease; the copies are left uncommitted, for the normal commit flow.
package awdocuments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Errors.
var (
	ErrUnavailable  = errors.New("awdocuments: this server has no Git workspace provider")
	ErrNotReady     = errors.New("awdocuments: the repository workspace is not READY")
	ErrWriterActive = errors.New("awdocuments: an attempt is writing in the worktree; export after it finishes")
	ErrConflicts    = errors.New("awdocuments: some documents already exist in the repository with other content; export again with overwrite")
)

// Deps is what the commands need.
type Deps struct {
	UnitOfWork ports.UnitOfWork
	Store      ports.AwDocumentStore
}

// List is a worktree's aw-private documents.
type List struct {
	RepositoryWorkspaceID string             `json:"repositoryWorkspaceId"`
	Documents             []ports.AwDocument `json:"documents"`
}

func load(ctx context.Context, d Deps, projectID, repositoryWorkspaceID string, forWrite bool) (ports.WorkspaceHandle, error) {
	var rw workspace.RepositoryWorkspace
	err := d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, repositoryWorkspaceID)
		if err != nil {
			return err
		}
		rw = record.Workspace
		repo, err := tx.Catalog().GetRepository(ctx, string(rw.RepositoryID))
		if err != nil {
			return err
		}
		if string(repo.ProjectID) != projectID {
			return fmt.Errorf("%w: repository workspace %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
		}
		if rw.State != workspace.RepositoryWorkspaceReady {
			return fmt.Errorf("%w (it is %s)", ErrNotReady, rw.State)
		}
		if forWrite {
			active, err := tx.Work().HasActiveWriteLease(ctx, []string{repositoryWorkspaceID})
			if err != nil {
				return err
			}
			if active {
				return ErrWriterActive
			}
		}
		return nil
	})
	if err != nil {
		return ports.WorkspaceHandle{}, err
	}
	if d.Store == nil {
		return ports.WorkspaceHandle{}, ErrUnavailable
	}
	return ports.NewWorkspaceHandle(rw.Locator)
}

// ListDocuments reads the documents under .aw/features/ of a repository workspace.
func ListDocuments(ctx context.Context, d Deps, projectID, repositoryWorkspaceID string) (List, error) {
	handle, err := load(ctx, d, projectID, repositoryWorkspaceID, false)
	if err != nil {
		return List{}, err
	}
	docs, err := d.Store.ListAwDocuments(ctx, handle)
	if err != nil {
		return List{}, err
	}
	return List{RepositoryWorkspaceID: repositoryWorkspaceID, Documents: docs}, nil
}

// ExportResult is what Export did.
type ExportResult struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	ports.AwDocumentsExport
}

// Export copies .aw/features/<rest> to docs/features/<rest> in the worktree (uncommitted). Without overwrite, a target that exists
// with other content refuses the whole export (ErrConflicts, listing them) and writes nothing.
func Export(ctx context.Context, d Deps, cmd ports.Command, projectID, repositoryWorkspaceID string, overwrite bool) (ExportResult, error) {
	var result ExportResult
	if done, err := replay(ctx, d.UnitOfWork, cmd, &result); err != nil || done {
		return result, err
	}
	handle, err := load(ctx, d, projectID, repositoryWorkspaceID, true)
	if err != nil {
		return ExportResult{}, err
	}
	exported, err := d.Store.ExportAwDocuments(ctx, handle, overwrite)
	if err != nil {
		return ExportResult{}, err
	}
	result = ExportResult{RepositoryWorkspaceID: repositoryWorkspaceID, AwDocumentsExport: exported}
	if len(exported.Conflicts) > 0 && !overwrite {
		return result, fmt.Errorf("%w: %v", ErrConflicts, exported.Conflicts)
	}
	body, err := json.Marshal(result)
	if err != nil {
		return ExportResult{}, err
	}
	err = d.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
			RequestHash: cmd.RequestHash, ResultJSON: string(body), CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}

func replay(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, out any) (bool, error) {
	found := false
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		receipt, ok, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil || !ok {
			return err
		}
		if receipt.RequestHash != cmd.RequestHash {
			return ports.ErrReceiptConflict
		}
		found = true
		return json.Unmarshal([]byte(receipt.ResultJSON), out)
	})
	return found, err
}
