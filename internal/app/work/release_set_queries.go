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
package work

import (
	"context"
	"errors"
	"strings"

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
