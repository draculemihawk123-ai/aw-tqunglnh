// ReleaseSet create/seal/abandon commands (V5-10A,
// docs/design/07-v5-execution-evidence.md; AK-ARCH-015C, GC-DS-04,
// GC-INV-26) — the "schema/commands create-seal-abandon" half of this
// task's own Thực hiện line. CreateReleaseSet persists a new CREATED
// ReleaseSet from the exact per-repository base/result/verdict a caller
// (V5-11's own CompletionPolicy service, once it exists) already computed;
// SealReleaseSet/AbandonReleaseSet close its lifecycle. Neither command
// touches a real workspace's filesystem or Git state — that authority
// belongs only to internal/adapters/gitworktree.Provider.CreateLocalCommit,
// a separate primitive this same task also builds (localcommit.go, ports
// package) — mirroring internal/app/workspacerelease's own established
// "producer builds the durable record, a documented future/sibling piece
// owns the real I/O" split.
//
// EligibilityAuthority (this file, bottom half) is the real implementation
// ports.ReleaseEligibilityAuthority's own doc comment names as deferred to
// "that later task": internal/app/workspacerelease.RequestWorkspaceSetRelease
// depends on that interface alone (never a concrete type), and this file
// backs it with a real, persisted ReleaseSet for the first time — with zero
// change to workspacerelease itself, since Go's structural typing already
// satisfies the interface.
package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// ErrReleaseSetNotOpen is returned when Seal/AbandonReleaseSet targets a
// ReleaseSet that is no longer CREATED — mirrors
// workspacerelease.ErrWorkspaceSetAlreadyReleased's own precedent: checked
// explicitly, before the version-fencing CAS, so a genuine "duplicate
// seal/abandon" (a distinct command, not a receipt replay of the same one)
// gets this specific, readable error rather than a generic
// ports.ErrOptimisticConflict that would also fire for an unrelated stale
// version.
var ErrReleaseSetNotOpen = errors.New("work: release set is not open (already sealed or abandoned)")

// RepositoryReleaseRequest is one repository's own exact contribution to a
// ReleaseSet under construction — the request-shaped mirror of
// workdomain.RepositoryRelease (plain strings, not domain types, the same
// convention CreateRootWorkItemRequest's own ScopeGrantRequest already
// follows).
type RepositoryReleaseRequest struct {
	RepositoryID      string
	BaseVCSObjectID   string
	ResultVCSObjectID string
	// Verdict is one of gate.Verdict's own closed 5-value set (PASS, FAIL,
	// ERROR, NOT_RUN, NOT_APPLICABLE) as a plain string — workdomain.NewReleaseSet
	// itself rejects anything else via gate.Verdict.IsValid(), so this
	// command does not duplicate that check.
	Verdict string
}

// CreateReleaseSetRequest is what a caller supplies to CreateReleaseSet.
type CreateReleaseSetRequest struct {
	ProjectID    string
	FamilyID     string
	Repositories []RepositoryReleaseRequest
}

// ReleaseSetResult is what CreateReleaseSet/SealReleaseSet/AbandonReleaseSet
// return (and what a replayed command-receipt reconstructs).
type ReleaseSetResult struct {
	ReleaseSetID string `json:"releaseSetId"`
	ProjectID    string `json:"projectId"`
	FamilyID     string `json:"familyId"`
	State        string `json:"state"`
	ContentHash  string `json:"contentHash"`
	Version      uint64 `json:"version"`
}

func releaseSetResult(releaseSet workdomain.ReleaseSet) ReleaseSetResult {
	return ReleaseSetResult{
		ReleaseSetID: string(releaseSet.ID), ProjectID: string(releaseSet.ProjectID), FamilyID: string(releaseSet.FamilyID),
		State: string(releaseSet.State), ContentHash: releaseSet.ContentHash(), Version: releaseSet.Version,
	}
}

// CreateReleaseSet is the public command that persists a new CREATED
// ReleaseSet: req.FamilyID must name a real TaskFamily belonging to
// req.ProjectID (the identical cross-project check CreateRootWorkItem/
// RequestWorkspaceSetRelease already apply to their own family/set
// references), and every repository entry is validated and content-hashed
// by workdomain.NewReleaseSet itself. Follows V1-06's idempotent-command
// shape exactly: a retry with the same IdempotencyKey and RequestHash
// replays the first call's result; the same key with a different
// RequestHash is rejected as ports.ErrReceiptConflict. ReleaseSetID is
// minted internally via ids (a ReleaseSet has no caller-meaningful identity
// of its own before this command creates it, the same reasoning
// CreateRootWorkItemRequest's own doc comment already gives for
// WorkItemID/FamilyID/WorkspaceSetID).
func CreateReleaseSet(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req CreateReleaseSetRequest,
) (ReleaseSetResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return ReleaseSetResult{}, errors.New("work: ProjectID is required")
	}
	if strings.TrimSpace(req.FamilyID) == "" {
		return ReleaseSetResult{}, errors.New("work: FamilyID is required")
	}
	if len(req.Repositories) == 0 {
		return ReleaseSetResult{}, errors.New("work: at least one repository release is required")
	}

	var result ReleaseSetResult
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

		family, err := tx.Work().GetTaskFamily(ctx, req.FamilyID)
		if err != nil {
			return err
		}
		if string(family.ProjectID) != req.ProjectID {
			return fmt.Errorf("%w: task family %s belongs to project %s, not %s",
				ports.ErrCrossProjectReference, req.FamilyID, family.ProjectID, req.ProjectID)
		}

		entries := make([]workdomain.RepositoryRelease, 0, len(req.Repositories))
		for _, repository := range req.Repositories {
			entries = append(entries, workdomain.RepositoryRelease{
				RepositoryID:      project.RepositoryID(repository.RepositoryID),
				BaseVCSObjectID:   repository.BaseVCSObjectID,
				ResultVCSObjectID: repository.ResultVCSObjectID,
				Verdict:           gate.Verdict(repository.Verdict),
			})
		}
		releaseSet, err := workdomain.NewReleaseSet(
			workdomain.ReleaseSetID(ids.NewID()), project.ProjectID(req.ProjectID), workdomain.TaskFamilyID(req.FamilyID),
			entries, cmd.RequestedAt,
		)
		if err != nil {
			return err
		}

		stored, err := tx.Work().CreateReleaseSet(ctx, releaseSet)
		if err != nil {
			return err
		}

		result = releaseSetResult(stored)
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

// SealReleaseSetRequest is what a caller supplies to SealReleaseSet.
// cmd.ExpectedVersion (the ReleaseSet version the caller observed) is the
// fence against a stale transition — the same convention
// RequestWorkspaceSetRelease's own cmd.ExpectedVersion already establishes.
type SealReleaseSetRequest struct {
	ReleaseSetID string
}

// AbandonReleaseSetRequest is what a caller supplies to AbandonReleaseSet.
type AbandonReleaseSetRequest struct {
	ReleaseSetID string
}

// SealReleaseSet closes a ReleaseSet's lifecycle as SEALED — the terminal
// state GC-INV-26 requires before a future cleanup sweep (V5-14) may treat
// it as authorization to release the family's own WorkspaceSet.
func SealReleaseSet(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req SealReleaseSetRequest) (ReleaseSetResult, error) {
	return transitionReleaseSet(ctx, uow, cmd, req.ReleaseSetID, workdomain.ReleaseSetSealed)
}

// AbandonReleaseSet closes a ReleaseSet's lifecycle as ABANDONED — the
// other terminal state GC-INV-26 recognizes (a release that was never
// authorized to seal, but is still done being considered).
func AbandonReleaseSet(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req AbandonReleaseSetRequest) (ReleaseSetResult, error) {
	return transitionReleaseSet(ctx, uow, cmd, req.ReleaseSetID, workdomain.ReleaseSetAbandoned)
}

func transitionReleaseSet(
	ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, releaseSetID string, nextState workdomain.ReleaseSetState,
) (ReleaseSetResult, error) {
	releaseSetID = strings.TrimSpace(releaseSetID)
	if releaseSetID == "" {
		return ReleaseSetResult{}, errors.New("work: ReleaseSetID is required")
	}
	if cmd.ExpectedVersion == 0 {
		return ReleaseSetResult{}, errors.New(
			"work: release set transition requires cmd.ExpectedVersion (the ReleaseSet version the caller observed) — this is the fence that rejects a stale transition")
	}

	var result ReleaseSetResult
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

		current, err := tx.Work().GetReleaseSet(ctx, releaseSetID)
		if err != nil {
			return err
		}
		if current.State != workdomain.ReleaseSetCreated {
			return fmt.Errorf("%w: release set %s is %s", ErrReleaseSetNotOpen, releaseSetID, current.State)
		}

		transitioned, err := tx.Work().TransitionReleaseSetState(ctx, ports.TransitionReleaseSetStateRequest{
			ReleaseSetID: releaseSetID, ExpectedState: workdomain.ReleaseSetCreated, ExpectedVersion: cmd.ExpectedVersion,
			NextState: nextState, OccurredAt: cmd.RequestedAt,
		})
		if err != nil {
			return err
		}

		result = releaseSetResult(transitioned)
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

// IsCleanupEligible reports whether releaseSet has reached a terminal state
// — GC-INV-26's own "ReleaseSet local phải được seal hoặc abandon trước
// cleanup": a still-CREATED ReleaseSet is not yet eligible for a future
// cleanup sweep (V5-14) to consider. This is the "application policy" half
// of this task's own Phạm vi line ("ReleaseSet, local commit và application
// policy") — a policy decision over already-persisted state, not a domain
// invariant of ReleaseSet itself (which allows a caller to inspect a
// CREATED ReleaseSet freely; it is only cleanup that must wait).
func IsCleanupEligible(releaseSet workdomain.ReleaseSet) bool {
	return releaseSet.State == workdomain.ReleaseSetSealed || releaseSet.State == workdomain.ReleaseSetAbandoned
}

// EligibilityAuthority is the real ports.ReleaseEligibilityAuthority
// implementation ports.ReleaseEligibilityAuthority's own doc comment
// defers to "that later task": a family is release-authorized exactly when
// its own most recently created ReleaseSet (ListReleaseSetsForFamily's own
// (CreatedAt, ID) ordering makes "most recent" well-defined) is SEALED or
// ABANDONED. Opens its own read-only transaction rather than taking a raw
// ports.WorkRepository, since ports.UnitOfWork's own doc comment is the one
// sanctioned way to read persisted state — the identical reasoning
// RequestWorkspaceSetRelease's own doc comment gives for calling this
// interface outside of (never inside) a WithSerializedWrite closure.
type EligibilityAuthority struct {
	uow ports.UnitOfWork
}

// NewEligibilityAuthority builds an EligibilityAuthority backed by uow.
func NewEligibilityAuthority(uow ports.UnitOfWork) EligibilityAuthority {
	return EligibilityAuthority{uow: uow}
}

var _ ports.ReleaseEligibilityAuthority = EligibilityAuthority{}

// IsReleaseAuthorized implements ports.ReleaseEligibilityAuthority.
func (a EligibilityAuthority) IsReleaseAuthorized(ctx context.Context, familyID string) (bool, string, error) {
	var authorized bool
	var reason string
	err := a.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		releaseSets, err := tx.Work().ListReleaseSetsForFamily(ctx, familyID)
		if err != nil {
			return err
		}
		if len(releaseSets) == 0 {
			reason = fmt.Sprintf("no release set exists for family %s", familyID)
			return nil
		}
		latest := releaseSets[len(releaseSets)-1]
		if IsCleanupEligible(latest) {
			authorized = true
			return nil
		}
		reason = fmt.Sprintf("release set %s for family %s is still %s", latest.ID, familyID, latest.State)
		return nil
	})
	if err != nil {
		return false, "", err
	}
	return authorized, reason, nil
}
