// ReleaseSet list/get queries (V6-10E, docs/design/08-v6-api-projections.md
// V6-10E) — the missing half of this task's own Phạm vi line
// ("list/get/create/seal/abandon"): V5-10A built CreateReleaseSet/
// SealReleaseSet/AbandonReleaseSet (release_set.go) plus the real
// ports.WorkRepository.GetReleaseSet/ListReleaseSetsForFamily persistence
// methods those commands already call internally, but never exposed a
// public query wrapper around either — every existing caller of those two
// WorkRepository methods is itself already inside a WithSerializedWrite or
// WithReadOnly closure (transitionReleaseSet, EligibilityAuthority). This
// file adds the two public, read-only entry points a future HTTP/CLI layer
// (V6-10F) can call directly, mirroring internal/app/workspaceinspection's
// own "public Queries type, opens its own uow.WithReadOnly" shape.
//
// GetReleaseSetLocalCommitStatus (bottom of file) is V6-10F's own addition:
// the missing per-operation status query for a RequestReleaseSetLocalCommit
// intent (internal/app/releasesetcommit.RequestReleaseSetLocalCommit,
// internal/domain/work.ReleaseSetLocalCommit) — until now, the only way to
// read one back was the raw Tx-level ports.WorkRepository.
// GetReleaseSetLocalCommit method, and every existing caller of that method
// is itself already inside a WithSerializedWrite/WithReadOnly closure
// (RequestReleaseSetLocalCommit's own receipt-replay reconstruction,
// execute.go's loadIntent). This mirrors GetReleaseSet immediately above it
// exactly: a public, read-only entry point opening its own uow.WithReadOnly,
// so V6-10F's own GET status route has something real to dispatch through
// rather than reaching for tx.Work() directly (which would violate this
// whole package's "commands/queries open their own uow" discipline).
package work

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// ReleaseSetDetail is GetReleaseSet/ListReleaseSetsForFamily's own result
// shape — fuller than ReleaseSetResult (which only ever needs to echo a
// command's own outcome): a query caller wants the full per-repository
// entry list a command result deliberately omits.
type ReleaseSetDetail struct {
	ReleaseSetID string                    `json:"releaseSetId"`
	ProjectID    string                    `json:"projectId"`
	FamilyID     string                    `json:"familyId"`
	State        string                    `json:"state"`
	ContentHash  string                    `json:"contentHash"`
	Version      uint64                    `json:"version"`
	Entries      []RepositoryReleaseDetail `json:"entries"`
}

// RepositoryReleaseDetail is one ReleaseSetDetail entry's own per-repository
// shape — the request-shaped mirror of workdomain.RepositoryRelease.
type RepositoryReleaseDetail struct {
	RepositoryID      string `json:"repositoryId"`
	BaseVCSObjectID   string `json:"baseVcsObjectId"`
	ResultVCSObjectID string `json:"resultVcsObjectId"`
	Verdict           string `json:"verdict"`
}

func releaseSetDetail(releaseSet workdomain.ReleaseSet) ReleaseSetDetail {
	entries := releaseSet.Entries()
	detail := ReleaseSetDetail{
		ReleaseSetID: string(releaseSet.ID), ProjectID: string(releaseSet.ProjectID), FamilyID: string(releaseSet.FamilyID),
		State: string(releaseSet.State), ContentHash: releaseSet.ContentHash(), Version: releaseSet.Version,
		Entries: make([]RepositoryReleaseDetail, 0, len(entries)),
	}
	for _, entry := range entries {
		detail.Entries = append(detail.Entries, RepositoryReleaseDetail{
			RepositoryID: string(entry.RepositoryID), BaseVCSObjectID: entry.BaseVCSObjectID,
			ResultVCSObjectID: entry.ResultVCSObjectID, Verdict: string(entry.Verdict),
		})
	}
	return detail
}

// GetReleaseSet returns releaseSetID's own full detail, or
// ports.ErrPersistenceNotFound.
func GetReleaseSet(ctx context.Context, uow ports.UnitOfWork, releaseSetID string) (ReleaseSetDetail, error) {
	if strings.TrimSpace(releaseSetID) == "" {
		return ReleaseSetDetail{}, errors.New("work: ReleaseSetID is required")
	}
	var detail ReleaseSetDetail
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		releaseSet, err := tx.Work().GetReleaseSet(ctx, releaseSetID)
		if err != nil {
			return err
		}
		detail = releaseSetDetail(releaseSet)
		return nil
	})
	return detail, err
}

// ListReleaseSetsForFamily returns every ReleaseSet ever created for
// familyID, ordered by (CreatedAt, ID) — see
// ports.WorkRepository.ListReleaseSetsForFamily's own doc comment.
func ListReleaseSetsForFamily(ctx context.Context, uow ports.UnitOfWork, familyID string) ([]ReleaseSetDetail, error) {
	if strings.TrimSpace(familyID) == "" {
		return nil, errors.New("work: FamilyID is required")
	}
	var details []ReleaseSetDetail
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		releaseSets, err := tx.Work().ListReleaseSetsForFamily(ctx, familyID)
		if err != nil {
			return err
		}
		details = make([]ReleaseSetDetail, 0, len(releaseSets))
		for _, releaseSet := range releaseSets {
			details = append(details, releaseSetDetail(releaseSet))
		}
		return nil
	})
	return details, err
}

// ReleaseSetLocalCommitStatus is GetReleaseSetLocalCommitStatus's own
// result shape — a plain, wire-friendly mirror of workdomain.
// ReleaseSetLocalCommit (never the domain type itself, the same "query
// returns its own DTO, not the aggregate" convention ReleaseSetDetail
// already follows above). FailureReason/ParentVCSObjectID/ResultVCSObjectID/
// CompletedAt are only ever populated once State has left REQUESTED — see
// workdomain.ReleaseSetLocalCommit's own field-by-field doc comment for
// exactly when each is set; this type does not narrow or re-derive that,
// it just carries whatever the row currently holds.
type ReleaseSetLocalCommitStatus struct {
	ReleaseSetLocalCommitID string     `json:"releaseSetLocalCommitId"`
	ProjectID               string     `json:"projectId"`
	ReleaseSetID            string     `json:"releaseSetId"`
	RepositoryWorkspaceID   string     `json:"repositoryWorkspaceId"`
	RepositoryID            string     `json:"repositoryId"`
	State                   string     `json:"state"`
	FailureReason           string     `json:"failureReason,omitempty"`
	ParentVCSObjectID       string     `json:"parentVcsObjectId,omitempty"`
	ResultVCSObjectID       string     `json:"resultVcsObjectId,omitempty"`
	JobID                   string     `json:"jobId"`
	CreatedAt               time.Time  `json:"createdAt"`
	CompletedAt             *time.Time `json:"completedAt,omitempty"`
	Version                 uint64     `json:"version"`
}

func releaseSetLocalCommitStatus(intent workdomain.ReleaseSetLocalCommit) ReleaseSetLocalCommitStatus {
	return ReleaseSetLocalCommitStatus{
		ReleaseSetLocalCommitID: string(intent.ID), ProjectID: string(intent.ProjectID), ReleaseSetID: string(intent.ReleaseSetID),
		RepositoryWorkspaceID: intent.RepositoryWorkspaceID, RepositoryID: string(intent.RepositoryID),
		State: string(intent.State), FailureReason: string(intent.FailureReason),
		ParentVCSObjectID: intent.ParentVCSObjectID, ResultVCSObjectID: intent.ResultVCSObjectID,
		JobID: intent.JobID, CreatedAt: intent.CreatedAt, CompletedAt: intent.CompletedAt, Version: intent.Version,
	}
}

// GetReleaseSetLocalCommitStatus returns releaseSetLocalCommitID's own
// current status — REQUESTED, COMMITTED or FAILED, exactly as
// internal/app/releasesetcommit's own producer (RequestReleaseSetLocalCommit)
// and consumer (execute.go's ExecuteReleaseSetLocalCommit) last left the
// row — or ports.ErrPersistenceNotFound. This is a plain, per-operation
// read: it never aggregates across a ReleaseSet's own multiple local-commit
// operations (one per repository entry a caller has requested a commit
// for) into a single "some/all" summary — a caller that wants to know
// where every repository in a ReleaseSet stands calls this once per
// operation ID it already holds, and gets that one operation's own exact,
// un-conflated state back every time (V6-10F's own "partial" Verify line:
// "the status query must report this accurately, not just an aggregate
// 'some/all'").
func GetReleaseSetLocalCommitStatus(ctx context.Context, uow ports.UnitOfWork, releaseSetLocalCommitID string) (ReleaseSetLocalCommitStatus, error) {
	if strings.TrimSpace(releaseSetLocalCommitID) == "" {
		return ReleaseSetLocalCommitStatus{}, errors.New("work: ReleaseSetLocalCommitID is required")
	}
	var status ReleaseSetLocalCommitStatus
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		intent, err := tx.Work().GetReleaseSetLocalCommit(ctx, releaseSetLocalCommitID)
		if err != nil {
			return err
		}
		status = releaseSetLocalCommitStatus(intent)
		return nil
	})
	return status, err
}
