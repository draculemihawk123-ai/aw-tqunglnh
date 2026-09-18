package workspace_test

// Real, in-process coverage for V6-15L's own workspace-set show/release and
// repository-workspace reconcile subcommands, driving this package's own
// Run* functions directly against a REAL *sqlite.Store — never a mock,
// never a direct row fabrication beyond the shared sqlite fixture helpers
// (internal/adapters/sqlite/fixtures.go) internal/delivery/httpapi/workspace_test.go
// already uses for the identical scenarios (quarantine/lease/generation/
// replay), mirrored here one-for-one but driving the CLI leaf instead of a
// real HTTP round trip.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	cliworkspace "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
)

// releaseTestEnv is one real UnitOfWork + this package's own Dependencies
// pair, torn down via t.Cleanup.
type releaseTestEnv struct {
	deps  cliworkspace.Dependencies
	uow   ports.UnitOfWork
	store *sqlite.Store
}

func newReleaseTestEnv(t *testing.T) *releaseTestEnv {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "workspace-cli.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	authority := work.NewEligibilityAuthority(uow)

	return &releaseTestEnv{
		deps:  cliworkspace.Dependencies{UOW: uow, IDs: idsource.Random{}, Authority: authority},
		uow:   uow,
		store: store,
	}
}

// seededWorkspace names every real row seedReadyWorkspace plants for one
// test: a single RepositoryWorkspace at generation 1, state READY, inside a
// WorkspaceSet also at version 1 — mirrors
// internal/delivery/httpapi/workspace_test.go's own seededWorkspace/
// seedReadyWorkspace exactly.
type seededWorkspace struct {
	projectID, familyID, workItemID                     string
	workspaceSetID, repositoryID, repositoryWorkspaceID string
}

func (e *releaseTestEnv) seedReadyWorkspace(t *testing.T, suffix string) seededWorkspace {
	t.Helper()
	ctx := context.Background()
	sw := seededWorkspace{
		projectID: "project-" + suffix, familyID: "family-" + suffix, workItemID: "work-" + suffix,
		workspaceSetID: "set-" + suffix, repositoryID: "repo-" + suffix, repositoryWorkspaceID: "rw-" + suffix,
	}
	if err := sqlite.SeedFixtureOwners(ctx, e.store, sw.projectID, sw.familyID, sw.workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, e.store, sw.projectID, sw.familyID, sw.workspaceSetID, sw.repositoryID, sw.repositoryWorkspaceID); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	return sw
}

// sealReleaseSet mirrors internal/delivery/httpapi/workspace_test.go's own
// identical helper: creates and seals a real ReleaseSet for sw's own
// family through the real internal/app/work.CreateReleaseSet/SealReleaseSet
// public commands, so work.EligibilityAuthority.IsReleaseAuthorized (the
// real ports.ReleaseEligibilityAuthority this test env wires in, never a
// fake) reports this family as release-authorized (GC-INV-26).
func (e *releaseTestEnv) sealReleaseSet(t *testing.T, sw seededWorkspace) {
	t.Helper()
	ctx := context.Background()
	createCmd := ports.Command{
		ID: "create-release-set-" + sw.familyID, IdempotencyKey: "create-release-set-" + sw.familyID,
		Actor: "operator", Scope: ports.ProjectScope(sw.projectID), Type: "CreateReleaseSet",
		RequestHash: "sha256:test-create-" + sw.familyID, RequestedAt: time.Now().UTC(),
	}
	created, err := work.CreateReleaseSet(ctx, e.uow, idsource.Random{}, createCmd, work.CreateReleaseSetRequest{
		ProjectID: sw.projectID, FamilyID: sw.familyID,
		Repositories: []work.RepositoryReleaseRequest{{
			RepositoryID: sw.repositoryID, BaseVCSObjectID: "base-sha", ResultVCSObjectID: "base-sha", Verdict: "PASS",
		}},
	})
	if err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}
	sealCmd := ports.Command{
		ID: "seal-release-set-" + sw.familyID, IdempotencyKey: "seal-release-set-" + sw.familyID,
		Actor: "operator", Scope: ports.ProjectScope(sw.projectID), Type: "SealReleaseSet",
		RequestHash: "sha256:test-seal-" + sw.familyID, RequestedAt: time.Now().UTC(), ExpectedVersion: created.Version,
	}
	if _, err := work.SealReleaseSet(ctx, e.uow, sealCmd, work.SealReleaseSetRequest{ReleaseSetID: created.ReleaseSetID}); err != nil {
		t.Fatalf("SealReleaseSet: %v", err)
	}
}
