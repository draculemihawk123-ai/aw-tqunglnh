// Package workspaceinspection is V6-10C's application-layer entry point for
// bounded, read-only Git content inspection: GetSource, GetDiff and
// GetRepositoryLog (docs/design/08-v6-api-projections.md — "public
// GetSource, GetDiff, GetRepositoryLog before HTTP/CLI exposure"), the
// three named queries a later HTTP/CLI task (V6-10D) will expose without
// ever letting a caller run an arbitrary Git ref expression, escape a
// repository root via path traversal/symlink, or read unbounded output.
//
// Every query here follows the same two-phase shape:
//
//  1. reload the caller-claimed Project/Repository/WorkspaceSet/
//     RepositoryWorkspace ownership chain from persistence
//     (ports.UnitOfWork.WithReadOnly) and reject a caller whose claimed
//     identifiers do not actually match the real, persisted ownership
//     graph (ErrScopeMismatch) or whose named RepositoryWorkspace is not
//     currently READY (ErrWorkspaceNotReady) — this task's own "reload
//     workspace/repository/project" step, run fresh on every call rather
//     than cached anywhere;
//  2. resolve the workspace's own opaque ports.WorkspaceHandle from its
//     persisted Locator and delegate the real, bounded Git read to
//     ports.WorkspaceInspectionReader — a real
//     internal/adapters/gitworktree.Provider at the composition root, never
//     a concrete dependency of this package (internal/archtest's own
//     TestDomainAppNeverImportAdapters enforces exactly this: this file
//     imports only internal/app/ports and internal/domain/..., never
//     internal/adapters/...).
//
// This package never accepts a raw filesystem path, a symbolic Git ref
// expression, or an opaque WorkspaceHandle from its own caller: every
// public request names a RepositoryWorkspace by its own persisted business
// identifiers (WorkspaceScope) and a revision by its own already-resolved
// workspace.Revision value — the identical "resolve exact authorized
// revision" contract ports.WorkspaceInspectionReader itself independently
// re-validates one layer down (never trusted solely because this package
// already checked it).
package workspaceinspection

import (
	"context"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ErrScopeMismatch is returned by every query in this package when the
// caller-claimed WorkspaceScope does not match the real, persisted
// ownership chain for the named RepositoryWorkspaceID — worded without ever
// echoing back the real Locator, path or actual owning
// project/repository/workspace-set a mismatched caller does not already
// know (this task's own "no path leak").
var ErrScopeMismatch = errors.New("workspaceinspection: repository workspace does not match the given project/repository/workspace set")

// ErrWorkspaceNotReady is returned when the named RepositoryWorkspace's own
// persisted lifecycle state is anything other than READY (PROVISIONING,
// QUARANTINED, RELEASING, RELEASED or FAILED) — inspection only ever
// targets the one generation currently authoritative for writers, mirroring
// every other handler in this codebase that treats a non-READY
// RepositoryWorkspace as not the one to act on (e.g.
// internal/app/readinesscheck.Handler.Handle's own identical check).
var ErrWorkspaceNotReady = errors.New("workspaceinspection: repository workspace is not READY")

// Bounds a caller-supplied limit is clamped into before ever reaching
// ports.WorkspaceInspectionReader — "ByteLimit/LineLimit are always
// already-clamped, positive values by the time a real adapter sees them"
// (ports/workspaceinspection.go's own doc comment). A caller that supplies
// zero, a negative value, or nothing at all gets the default; a caller that
// asks for more than the maximum gets the maximum, never an error — bounded
// results are this package's whole point, not a caller-facing failure mode.
const (
	defaultByteLimit int64 = 256 * 1024
	maxByteLimit     int64 = 8 * 1024 * 1024
	defaultLineLimit int64 = 2000
	maxLineLimit     int64 = 50000
	defaultFileLimit int   = 500
	maxFileLimit     int   = 5000
	defaultLogLimit  int   = 50
	maxLogLimit      int   = 500
)

// Queries is the composition-root-wired entry point for this package's
// three named application queries. reader is always a real
// ports.WorkspaceInspectionReader (a real
// internal/adapters/gitworktree.Provider in production), injected by
// whoever wires the composition root.
type Queries struct {
	uow    ports.UnitOfWork
	reader ports.WorkspaceInspectionReader
}

// New returns a ready-to-use Queries.
func New(uow ports.UnitOfWork, reader ports.WorkspaceInspectionReader) *Queries {
	return &Queries{uow: uow, reader: reader}
}

// WorkspaceScope names the RepositoryWorkspace a query targets by its own
// persisted business identifiers alone — never an opaque
// ports.WorkspaceHandle, which every query in this package resolves
// internally after ownership is confirmed.
type WorkspaceScope struct {
	ProjectID             string
	RepositoryID          string
	WorkspaceSetID        string
	RepositoryWorkspaceID string
}

// GetSourceRequest is what a caller supplies to Queries.GetSource.
type GetSourceRequest struct {
	Scope     WorkspaceScope
	Revision  workspace.Revision
	Path      string
	ByteLimit int64
	LineLimit int64
}

// GetSource returns req.Path's exact blob content at req.Revision — see
// ports.WorkspaceInspectionReader.ReadSource for the full safety contract
// this delegates to.
func (q *Queries) GetSource(ctx context.Context, req GetSourceRequest) (ports.SourceContent, error) {
	handle, err := q.resolveScope(ctx, req.Scope)
	if err != nil {
		return ports.SourceContent{}, err
	}
	return q.reader.ReadSource(ctx, ports.ReadSourceRequest{
		Handle:    handle,
		Revision:  req.Revision,
		Path:      req.Path,
		ByteLimit: clamp(req.ByteLimit, defaultByteLimit, maxByteLimit),
		LineLimit: clamp(req.LineLimit, defaultLineLimit, maxLineLimit),
	})
}

// GetDiffRequest is what a caller supplies to Queries.GetDiff.
type GetDiffRequest struct {
	Scope          WorkspaceScope
	BaseRevision   workspace.Revision
	ResultRevision workspace.Revision
	ByteLimit      int64
	FileLimit      int
}

// GetDiff returns the exact patch between req.BaseRevision and
// req.ResultRevision — see ports.WorkspaceInspectionReader.ReadDiff.
func (q *Queries) GetDiff(ctx context.Context, req GetDiffRequest) (ports.DiffContent, error) {
	handle, err := q.resolveScope(ctx, req.Scope)
	if err != nil {
		return ports.DiffContent{}, err
	}
	return q.reader.ReadDiff(ctx, ports.ReadDiffRequest{
		Handle:         handle,
		BaseRevision:   req.BaseRevision,
		ResultRevision: req.ResultRevision,
		ByteLimit:      clamp(req.ByteLimit, defaultByteLimit, maxByteLimit),
		FileLimit:      clampInt(req.FileLimit, defaultFileLimit, maxFileLimit),
	})
}

// GetRepositoryLogRequest is what a caller supplies to
// Queries.GetRepositoryLog.
type GetRepositoryLogRequest struct {
	Scope     WorkspaceScope
	Anchor    workspace.Revision
	Cursor    string
	Limit     int
	ByteLimit int64
}

// GetRepositoryLog returns one page of req.Anchor's own commit ancestry —
// see ports.WorkspaceInspectionReader.ReadRepositoryLog.
func (q *Queries) GetRepositoryLog(ctx context.Context, req GetRepositoryLogRequest) (ports.RepositoryLogPage, error) {
	handle, err := q.resolveScope(ctx, req.Scope)
	if err != nil {
		return ports.RepositoryLogPage{}, err
	}
	return q.reader.ReadRepositoryLog(ctx, ports.ReadRepositoryLogRequest{
		Handle:    handle,
		Anchor:    req.Anchor,
		Cursor:    req.Cursor,
		Limit:     clampInt(req.Limit, defaultLogLimit, maxLogLimit),
		ByteLimit: clamp(req.ByteLimit, defaultByteLimit, maxByteLimit),
	})
}

// resolveScope reloads scope's own claimed RepositoryWorkspace/Repository/
// Project ownership chain from persistence, rejects any mismatch
// (ErrScopeMismatch) or non-READY RepositoryWorkspace
// (ErrWorkspaceNotReady), and resolves the workspace's own opaque handle —
// this task's own "reload workspace/repository/project" step. This never
// caches a prior resolution: a RepositoryWorkspace quarantined or released
// between two calls is never inspected on stale authority.
func (q *Queries) resolveScope(ctx context.Context, scope WorkspaceScope) (ports.WorkspaceHandle, error) {
	if scope.ProjectID == "" || scope.RepositoryID == "" || scope.WorkspaceSetID == "" || scope.RepositoryWorkspaceID == "" {
		return ports.WorkspaceHandle{}, fmt.Errorf(
			"%w: project, repository, workspace set and repository workspace id are all required", ErrScopeMismatch)
	}

	var record ports.RepositoryWorkspaceRecord
	var repositoryProjectID string
	err := q.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		record, err = tx.Work().GetRepositoryWorkspaceByID(ctx, scope.RepositoryWorkspaceID)
		if err != nil {
			return err
		}
		repository, err := tx.Catalog().GetRepository(ctx, string(record.Workspace.RepositoryID))
		if err != nil {
			return err
		}
		repositoryProjectID = string(repository.ProjectID)
		return nil
	})
	if err != nil {
		return ports.WorkspaceHandle{}, err
	}

	if string(record.Workspace.RepositoryID) != scope.RepositoryID ||
		string(record.Workspace.WorkspaceSetID) != scope.WorkspaceSetID ||
		repositoryProjectID != scope.ProjectID {
		return ports.WorkspaceHandle{}, ErrScopeMismatch
	}
	if record.Workspace.State != workspace.RepositoryWorkspaceReady {
		return ports.WorkspaceHandle{}, ErrWorkspaceNotReady
	}

	handle, err := ports.NewWorkspaceHandle(record.Workspace.Locator)
	if err != nil {
		return ports.WorkspaceHandle{}, fmt.Errorf("workspaceinspection: resolve workspace handle: %w", err)
	}
	return handle, nil
}

func clamp(value int64, def int64, max int64) int64 {
	if value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}

func clampInt(value int, def int, max int) int {
	if value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}
