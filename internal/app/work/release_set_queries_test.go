package work_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
)

// TestGetReleaseSet_ReturnsFullDetail is this task's own "get" Phạm-vi
// completion: V5-10A never exposed a public query wrapper around
// ports.WorkRepository.GetReleaseSet, only the internal call sites
// SealReleaseSet/AbandonReleaseSet/EligibilityAuthority already make.
func TestGetReleaseSet_ReturnsFullDetail(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")

	created, err := work.CreateReleaseSet(ctx, uow, ids,
		releaseSetCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), 0),
		oneRepoRequest("project-1", familyID, "repo-1", "base-1", "result-1", string(gate.VerdictPass)))
	if err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}

	detail, err := work.GetReleaseSet(ctx, uow, created.ReleaseSetID)
	if err != nil {
		t.Fatalf("GetReleaseSet: %v", err)
	}
	if detail.ReleaseSetID != created.ReleaseSetID || detail.State != created.State || len(detail.Entries) != 1 {
		t.Fatalf("detail = %+v, want it to match the created release set with 1 entry", detail)
	}
	if detail.Entries[0].RepositoryID != "repo-1" || detail.Entries[0].BaseVCSObjectID != "base-1" {
		t.Fatalf("detail.Entries[0] = %+v, want repo-1/base-1", detail.Entries[0])
	}
}

func TestGetReleaseSet_NotFound(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	if _, err := work.GetReleaseSet(ctx, uow, "does-not-exist"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("error = %v, want ErrPersistenceNotFound", err)
	}
}

// TestListReleaseSetsForFamily_OrderedByCreatedAtThenID proves the public
// query wrapper returns every ReleaseSet a family has ever had, in the
// same stable order the underlying WorkRepository method already
// establishes.
func TestListReleaseSetsForFamily_OrderedByCreatedAtThenID(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	familyID := seedReleaseSetFamily(t, uow, ids, "project-1", "repo-1")

	first, err := work.CreateReleaseSet(ctx, uow, ids,
		releaseSetCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), 0),
		oneRepoRequest("project-1", familyID, "repo-1", "base-1", "result-1", string(gate.VerdictPass)))
	if err != nil {
		t.Fatalf("CreateReleaseSet (first): %v", err)
	}
	second, err := work.CreateReleaseSet(ctx, uow, ids,
		releaseSetCommand("idem-2", "hash-b", ports.ProjectScope("project-1"), 0),
		oneRepoRequest("project-1", familyID, "repo-1", "base-2", "result-2", string(gate.VerdictFail)))
	if err != nil {
		t.Fatalf("CreateReleaseSet (second): %v", err)
	}

	details, err := work.ListReleaseSetsForFamily(ctx, uow, familyID)
	if err != nil {
		t.Fatalf("ListReleaseSetsForFamily: %v", err)
	}
	if len(details) != 2 || details[0].ReleaseSetID != first.ReleaseSetID || details[1].ReleaseSetID != second.ReleaseSetID {
		t.Fatalf("details = %+v, want [%s, %s] in creation order", details, first.ReleaseSetID, second.ReleaseSetID)
	}
}
