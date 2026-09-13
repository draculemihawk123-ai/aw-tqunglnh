// Package workspacestate is V6-10B's own "state queries" half of its Phạm
// vi line (docs/design/08-v6-api-projections.md V6-10B): expose a
// WorkspaceSet's and a RepositoryWorkspace's own persisted
// state/lease/fence/quarantine so a caller (internal/delivery/httpapi) can
// decide whether requesting workspacerelease.RequestWorkspaceSetRelease or
// workspacereconcile.RequestWorkspaceReconciliation is worth attempting —
// advisory only, never authoritative (see httpapi.ValidAction's own doc
// comment: the real command always re-validates live state under its own
// cmd.ExpectedVersion fence, regardless of what a query reported a moment
// earlier).
//
// This is genuinely new work, not a wrapper: a grep for a WorkspaceSet/
// RepositoryWorkspace read query across internal/app before this task found
// none — workspacerelease.RequestWorkspaceSetRelease and
// workspacereconcile.RequestWorkspaceReconciliation each reload the
// ownership chain they need internally, for their own eligibility checks,
// but neither exposes that reload as a public, independently callable query
// a read-only HTTP GET could dispatch. This package is that query, mirroring
// internal/app/workspaceinspection's own "public application query, bound
// output, before HTTP exposure" shape (V6-10C) — except, unlike
// workspaceinspection, it never touches Git/the filesystem: every field
// already lives in already-persisted rows this codebase's own
// ports.WorkRepository/ports.CatalogRepository already expose, so no new
// port method is added here — GetWorkspaceSetByFamilyID,
// ListWorkspaceSetRepositoryWorkspaces, GetRepositoryWorkspaceByID,
// GetRepository and HasActiveWriteLease are all real and already tested
// against workspacerelease/workspacereconcile's own eligibility checks; a
// read query over the identical rows needs only a new caller, not new
// persistence.
//
// This codebase's own "lease/fence/quarantine" vocabulary (the design doc's
// own V6-10B Mục tiêu line) maps onto already-real fields exactly:
//
//   - "Fence" is RepositoryWorkspace.Generation — immutable per row (a
//     RECREATE always inserts a brand new row at generation+1; it never
//     mutates a generation in place — internal/adapters/sqlite/workspace_lifecycle.go's
//     own RecreateRepositoryWorkspace doc comment).
//   - "Quarantine" is State == workspace.RepositoryWorkspaceQuarantined.
//   - "Lease" is HasActiveWriteLease — the same real, live
//     julianday(lease_until) > julianday('now') check
//     workspacerelease.RequestWorkspaceSetRelease's own eligibility gate
//     already uses, called once per RepositoryWorkspace row here (rather
//     than the aggregate-across-every-row-in-the-set question that command's
//     own eligibility check asks) so a caller can see exactly which
//     repository, not just "the set has one somewhere", holds the active
//     write lease.
package workspacestate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ErrScopeMismatch reports that the WorkspaceSet/RepositoryWorkspace a
// request resolved belongs to a different project than the caller declared.
// Deliberately the same name as workspaceinspection.ErrScopeMismatch and
// distinct from ports.ErrScopeMismatch — V6-03's own Quyết định #6 already
// established "same name, different package, different concern" is fine in
// this codebase: workspaceinspection's is about
// RepositoryWorkspace/Project/Repository/WorkspaceSet chain referential
// integrity, ports' own is about CommandScope authorization, and this one is
// this package's own identical referential-integrity concern for its own two
// read queries. The caller (httpapi) normalizes this into the exact same
// leakage-normalized 404 as ports.ErrPersistenceNotFound — see this
// package's own doc comment and httpapi's own WriteResourceHidden.
var ErrScopeMismatch = errors.New("workspacestate: resolved workspace does not belong to the caller's project")

// RepositoryWorkspaceState is one RepositoryWorkspace's own bounded, typed
// state/lease/fence/quarantine snapshot.
type RepositoryWorkspaceState struct {
	RepositoryWorkspaceID  string
	WorkspaceSetID         string
	RepositoryID           string
	Generation             uint64
	State                  workspace.RepositoryWorkspaceState
	Version                uint64
	BranchRef              string
	CurrentRevision        string
	LastProvisionErrorCode *string
	HasActiveWriteLease    bool
}

// WorkspaceSetState is a WorkspaceSet's own bounded, typed state, plus every
// one of its own RepositoryWorkspace children's state — enough for a caller
// to see exactly which child, if any, currently blocks
// workspacerelease.RequestWorkspaceSetRelease's own eligibility checks
// (quarantine, active write lease) without guessing or re-deriving them.
type WorkspaceSetState struct {
	WorkspaceSetID string
	FamilyID       string
	ProjectID      string
	State          workspace.WorkspaceSetState
	Version        uint64
	// HasBaseRevisionSet reports whether WorkspaceSet.BaseRevisionSet is
	// non-nil — that field is only ever meaningful/non-nil once the set has
	// reached READY (workspace.WorkspaceSet's own doc comment), and this
	// query never exposes the RevisionSet's own contents (a caller that
	// needs the actual per-repository revisions has no read need this state
	// query exists to serve — V6-10B's own Phạm vi line asks for
	// state/lease/fence/quarantine, not revision content).
	HasBaseRevisionSet   bool
	RepositoryWorkspaces []RepositoryWorkspaceState
}

// GetWorkspaceSetStateRequest is what a caller supplies to
// GetWorkspaceSetState.
type GetWorkspaceSetStateRequest struct {
	ProjectID string
	FamilyID  string
}

// GetWorkspaceSetState reloads req.FamilyID's own WorkspaceSet — the only
// lookup this codebase's own ports.WorkRepository exposes for it
// (workspace_sets.family_id is UNIQUE: one TaskFamily has at most one
// WorkspaceSet, ever), mirroring workspacerelease.RequestWorkspaceSetRelease's
// own identical reload — plus every RepositoryWorkspace it owns, each
// annotated with its own live HasActiveWriteLease. Returns
// ports.ErrPersistenceNotFound if no WorkspaceSet exists for req.FamilyID, or
// ErrScopeMismatch if one exists but belongs to a different project than
// req.ProjectID.
func GetWorkspaceSetState(ctx context.Context, uow ports.UnitOfWork, req GetWorkspaceSetStateRequest) (WorkspaceSetState, error) {
	if strings.TrimSpace(req.FamilyID) == "" {
		return WorkspaceSetState{}, errors.New("workspacestate: FamilyID is required")
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return WorkspaceSetState{}, errors.New("workspacestate: ProjectID is required")
	}

	var result WorkspaceSetState
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, req.FamilyID)
		if err != nil {
			return err
		}
		if string(set.ProjectID) != req.ProjectID {
			return fmt.Errorf("%w: workspace set for family %s", ErrScopeMismatch, req.FamilyID)
		}

		repoWorkspaces, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, string(set.ID))
		if err != nil {
			return err
		}
		states := make([]RepositoryWorkspaceState, 0, len(repoWorkspaces))
		for _, rw := range repoWorkspaces {
			state, err := loadRepositoryWorkspaceState(ctx, tx, rw)
			if err != nil {
				return err
			}
			states = append(states, state)
		}

		result = WorkspaceSetState{
			WorkspaceSetID: string(set.ID), FamilyID: req.FamilyID, ProjectID: req.ProjectID,
			State: set.State, Version: set.Version, HasBaseRevisionSet: set.BaseRevisionSet != nil,
			RepositoryWorkspaces: states,
		}
		return nil
	})
	return result, err
}

// GetRepositoryWorkspaceStateRequest is what a caller supplies to
// GetRepositoryWorkspaceState.
type GetRepositoryWorkspaceStateRequest struct {
	ProjectID             string
	RepositoryWorkspaceID string
}

// GetRepositoryWorkspaceState reloads req.RepositoryWorkspaceID's own
// RepositoryWorkspace and its owning Repository's own ProjectID — the
// identical ownership-chain reload
// workspacereconcile.RequestWorkspaceReconciliation's own commands.go already
// performs for its own eligibility check (tx.Work().GetRepositoryWorkspaceByID
// then tx.Catalog().GetRepository). Returns ports.ErrPersistenceNotFound if
// no such row exists, or ErrScopeMismatch if it exists but its owning
// Repository belongs to a different project than req.ProjectID.
func GetRepositoryWorkspaceState(ctx context.Context, uow ports.UnitOfWork, req GetRepositoryWorkspaceStateRequest) (RepositoryWorkspaceState, error) {
	if strings.TrimSpace(req.RepositoryWorkspaceID) == "" {
		return RepositoryWorkspaceState{}, errors.New("workspacestate: RepositoryWorkspaceID is required")
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return RepositoryWorkspaceState{}, errors.New("workspacestate: ProjectID is required")
	}

	var result RepositoryWorkspaceState
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, req.RepositoryWorkspaceID)
		if err != nil {
			return err
		}
		repo, err := tx.Catalog().GetRepository(ctx, string(record.Workspace.RepositoryID))
		if err != nil {
			return err
		}
		if string(repo.ProjectID) != req.ProjectID {
			return fmt.Errorf("%w: repository workspace %s", ErrScopeMismatch, req.RepositoryWorkspaceID)
		}
		state, err := loadRepositoryWorkspaceState(ctx, tx, record.Workspace)
		if err != nil {
			return err
		}
		result = state
		return nil
	})
	return result, err
}

// loadRepositoryWorkspaceState annotates rw with its own live
// HasActiveWriteLease — the one piece of RepositoryWorkspaceState that is
// never itself a column on rw, always a fresh read against write_leases.
func loadRepositoryWorkspaceState(ctx context.Context, tx ports.Tx, rw workspace.RepositoryWorkspace) (RepositoryWorkspaceState, error) {
	hasLease, err := tx.Work().HasActiveWriteLease(ctx, []string{string(rw.ID)})
	if err != nil {
		return RepositoryWorkspaceState{}, fmt.Errorf("workspacestate: check active write lease for %s: %w", rw.ID, err)
	}
	return RepositoryWorkspaceState{
		RepositoryWorkspaceID: string(rw.ID), WorkspaceSetID: string(rw.WorkspaceSetID), RepositoryID: string(rw.RepositoryID),
		Generation: rw.Generation, State: rw.State, Version: rw.Version, BranchRef: rw.BranchRef,
		CurrentRevision: rw.CurrentRevision, LastProvisionErrorCode: rw.LastProvisionErrorCode, HasActiveWriteLease: hasLease,
	}, nil
}
