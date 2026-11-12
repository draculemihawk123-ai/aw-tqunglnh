// Package awdocuments is V12-33 (docs/design/15-v12-ui-operations.md, UX-45, ADR-065): the per-task working documents a project
// keeps in aw (the AW location). An agent writes them into the worktree's aw-private directory (.aw/features/…, ignored by git); aw
// copies each one into an ATTACHED artifact when an agent attempt that ran in the worktree succeeds and before the worktree is released
// (Snapshot), so they outlive the worktree. ListDocuments shows the worktree's files and, for what the worktree no longer has, aw's
// copies; Export writes them into the repository's docs/features/ — from the worktree, or from aw's copy when the file is gone — so
// the normal commit flow can keep them. Export writes into the worktree: it needs it READY and no attempt holding its write lease.
package awdocuments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Errors.
var (
	ErrUnavailable  = errors.New("awdocuments: this server has no Git workspace provider")
	ErrNotReady     = errors.New("awdocuments: the repository workspace is not READY")
	ErrWriterActive = errors.New("awdocuments: an attempt is writing in the worktree; export after it finishes")
	ErrConflicts    = errors.New("awdocuments: some documents already exist in the repository with other content; export again with overwrite")
)

const documentMediaType = "text/markdown; charset=utf-8"

// Deps is what the commands need.
type Deps struct {
	UnitOfWork ports.UnitOfWork
	// Store is the Git workspace provider (nil: only aw's copies are shown, nothing is exported).
	Store ports.AwDocumentStore
	// Artifacts holds aw's copies (nil: none is read or written).
	Artifacts ports.ArtifactStore
	IDs       idsource.Source
	Now       func() time.Time
	// Matcher redacts known secret values before a copy is stored.
	Matcher redact.Matcher
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

// List is a worktree's aw-private documents.
type List struct {
	RepositoryWorkspaceID string             `json:"repositoryWorkspaceId"`
	WorkspaceState        string             `json:"workspaceState"`
	Documents             []ports.AwDocument `json:"documents"`
}

type target struct {
	rw       workspace.RepositoryWorkspace
	familyID string
	handle   ports.WorkspaceHandle
}

func load(ctx context.Context, d Deps, projectID, repositoryWorkspaceID string, forWrite bool) (target, error) {
	var t target
	err := d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, repositoryWorkspaceID)
		if err != nil {
			return err
		}
		t.rw, t.familyID = record.Workspace, record.FamilyID
		repo, err := tx.Catalog().GetRepository(ctx, string(t.rw.RepositoryID))
		if err != nil {
			return err
		}
		if string(repo.ProjectID) != projectID {
			return fmt.Errorf("%w: repository workspace %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
		}
		if !forWrite {
			return nil
		}
		if t.rw.State != workspace.RepositoryWorkspaceReady {
			return fmt.Errorf("%w (it is %s)", ErrNotReady, t.rw.State)
		}
		active, err := tx.Work().HasActiveWriteLease(ctx, []string{repositoryWorkspaceID})
		if err != nil {
			return err
		}
		if active {
			return ErrWriterActive
		}
		return nil
	})
	if err != nil {
		return target{}, err
	}
	if t.rw.State == workspace.RepositoryWorkspaceReady {
		t.handle, err = ports.NewWorkspaceHandle(t.rw.Locator)
	}
	return t, err
}

func (d Deps) snapshots(ctx context.Context, familyID, repositoryID string) (map[string]ports.AwDocumentSnapshot, error) {
	out := map[string]ports.AwDocumentSnapshot{}
	err := d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		rows, err := tx.AwDocuments().ListAwDocumentSnapshots(ctx, familyID, repositoryID)
		for _, r := range rows {
			out[r.Path] = r
		}
		return err
	})
	return out, err
}

func (d Deps) readSnapshot(ctx context.Context, s ports.AwDocumentSnapshot) (string, error) {
	if d.Artifacts == nil {
		return "", errors.New("awdocuments: this process has no artifact store")
	}
	var record artifact.Artifact
	if err := d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		record, err = tx.Artifacts().GetArtifact(ctx, s.ArtifactID)
		return err
	}); err != nil {
		return "", err
	}
	reader, err := d.Artifacts.Open(ctx, ports.ArtifactRef{Locator: record.Locator, SHA256: record.ContentHash, Size: record.Size,
		ContentType: record.MediaType, Sensitivity: record.Sensitivity, Redacted: record.Redacted})
	if err != nil {
		return "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 1<<20+1))
	return string(data), err
}

// documents merges the worktree's files (when READY) with aw's copies of the files it no longer has.
func (d Deps) documents(ctx context.Context, t target) ([]ports.AwDocument, error) {
	var docs []ports.AwDocument
	if t.handle != (ports.WorkspaceHandle{}) && d.Store != nil {
		var err error
		if docs, err = d.Store.ListAwDocuments(ctx, t.handle); err != nil {
			return nil, err
		}
	}
	snaps, err := d.snapshots(ctx, t.familyID, string(t.rw.RepositoryID))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range docs {
		seen[docs[i].Path] = true
		if s, ok := snaps[docs[i].Path]; ok {
			at := s.UpdatedAt
			docs[i].SnapshotAt = &at
		}
	}
	for path, s := range snaps {
		if seen[path] {
			continue
		}
		content, err := d.readSnapshot(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("awdocuments: read aw's copy of %s: %w", path, err)
		}
		at := s.UpdatedAt
		docs = append(docs, ports.AwDocument{Path: path, Size: s.Size, Content: content, Source: ports.AwDocumentFromSnapshot, SnapshotAt: &at})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	if docs == nil {
		docs = []ports.AwDocument{}
	}
	return docs, nil
}

// ListDocuments reads the task documents of a repository workspace: its .aw/features/ files and aw's copies of what is gone. A
// released worktree shows aw's copies only.
func ListDocuments(ctx context.Context, d Deps, projectID, repositoryWorkspaceID string) (List, error) {
	t, err := load(ctx, d, projectID, repositoryWorkspaceID, false)
	if err != nil {
		return List{}, err
	}
	docs, err := d.documents(ctx, t)
	if err != nil {
		return List{}, err
	}
	return List{RepositoryWorkspaceID: repositoryWorkspaceID, WorkspaceState: string(t.rw.State), Documents: docs}, nil
}

// SnapshotTarget names the worktree to copy from.
type SnapshotTarget struct {
	ProjectID    string
	FamilyID     string
	RepositoryID string
	Handle       ports.WorkspaceHandle
}

// Snapshot copies every .aw/features/ document of the worktree that changed since aw's last copy into an ATTACHED artifact
// (CANONICAL_CONTEXT: no TTL, never swept) and records it. It returns how many it copied. Known secret values are redacted first.
func Snapshot(ctx context.Context, d Deps, t SnapshotTarget) (int, error) {
	if d.Store == nil || d.Artifacts == nil || d.IDs == nil {
		return 0, nil
	}
	docs, err := d.Store.ListAwDocuments(ctx, t.Handle)
	if err != nil || len(docs) == 0 {
		return 0, err
	}
	snaps, err := d.snapshots(ctx, t.FamilyID, t.RepositoryID)
	if err != nil {
		return 0, err
	}
	copied := 0
	for _, doc := range docs {
		if doc.Truncated {
			continue
		}
		content, _ := d.Matcher.Redact([]byte(doc.Content))
		sum := sha256.Sum256(content)
		digest := hex.EncodeToString(sum[:])
		if s, ok := snaps[doc.Path]; ok && s.SHA256 == digest {
			continue
		}
		ref, err := d.Artifacts.Put(ctx, ports.ArtifactMetadata{ContentType: documentMediaType, Sensitivity: redact.Sensitive, Redacted: true}, bytes.NewReader(content))
		if err != nil {
			return copied, err
		}
		if err := d.Artifacts.Verify(ctx, ref); err != nil {
			return copied, err
		}
		now := d.now()
		record, err := artifact.NewArtifact(artifact.ID(d.IDs.NewID()), project.ProjectID(t.ProjectID), ref.Locator, ref.SHA256, ref.Size,
			ref.ContentType, ref.Sensitivity, ref.Redacted, artifact.RetentionCanonicalContext, artifact.Attached, false, nil, now, 1)
		if err != nil {
			return copied, err
		}
		if err := d.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			if _, err := tx.Artifacts().InsertArtifact(ctx, record); err != nil {
				return err
			}
			return tx.AwDocuments().PutAwDocumentSnapshot(ctx, ports.AwDocumentSnapshot{
				FamilyID: t.FamilyID, RepositoryID: t.RepositoryID, Path: doc.Path, ProjectID: t.ProjectID,
				ArtifactID: string(record.ID), SHA256: digest, Size: int64(len(content)), UpdatedAt: now,
			})
		}); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, nil
}

// SnapshotRepositoryWorkspace is Snapshot for a repository workspace by id (the release job's hook).
func SnapshotRepositoryWorkspace(ctx context.Context, d Deps, repositoryWorkspaceID string) (int, error) {
	var t SnapshotTarget
	if err := d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, repositoryWorkspaceID)
		if err != nil {
			return err
		}
		repo, err := tx.Catalog().GetRepository(ctx, string(record.Workspace.RepositoryID))
		if err != nil {
			return err
		}
		handle, err := ports.NewWorkspaceHandle(record.Workspace.Locator)
		t = SnapshotTarget{ProjectID: string(repo.ProjectID), FamilyID: record.FamilyID, RepositoryID: string(record.Workspace.RepositoryID), Handle: handle}
		return err
	}); err != nil {
		return 0, err
	}
	return Snapshot(ctx, d, t)
}

// ExportResult is what Export did.
type ExportResult struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	ports.AwDocumentsExport
}

// Export writes the task documents to docs/features/<rest> in the worktree (uncommitted): each from the worktree, or from aw's copy
// when the worktree no longer has it. Without overwrite, a target that exists with other content refuses the whole export
// (ErrConflicts, listing them) and writes nothing.
func Export(ctx context.Context, d Deps, cmd ports.Command, projectID, repositoryWorkspaceID string, overwrite bool) (ExportResult, error) {
	var result ExportResult
	if done, err := replay(ctx, d.UnitOfWork, cmd, &result); err != nil || done {
		return result, err
	}
	t, err := load(ctx, d, projectID, repositoryWorkspaceID, true)
	if err != nil {
		return ExportResult{}, err
	}
	if d.Store == nil {
		return ExportResult{}, ErrUnavailable
	}
	docs, err := d.documents(ctx, t)
	if err != nil {
		return ExportResult{}, err
	}
	written, err := d.Store.WriteFeatureDocuments(ctx, t.handle, docs, overwrite)
	if err != nil {
		return ExportResult{}, err
	}
	result = ExportResult{RepositoryWorkspaceID: repositoryWorkspaceID, AwDocumentsExport: written}
	if len(written.Conflicts) > 0 && !overwrite {
		return result, fmt.Errorf("%w: %v", ErrConflicts, written.Conflicts)
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
