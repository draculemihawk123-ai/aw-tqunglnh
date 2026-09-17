package message_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	climessage "github.com/taQuangLing/agent-workflow/internal/delivery/cli/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// testCommand/newTestDeps/setupFixture mirror internal/app/message's own
// identically-named test helpers (commands_test.go, attachment_test.go)
// exactly: a fake.UnitOfWork with one project, one ACTIVE repository and
// one root WorkItem already created, plus a real filesystem ArtifactStore
// rooted at a fresh temp directory — the minimum a message/attachment can
// attach to. Every test in this package gets its own isolated fixture,
// never shared state between tests.

func testCommand(idempotencyKey, requestHash, commandType string) ports.Command {
	return ports.Command{
		ID: commandType + "-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"),
		RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type:        commandType, RequestHash: requestHash,
	}
}

// setupFixture returns a ready climessage.Dependencies (fake UnitOfWork,
// real filesystem ArtifactStore, deterministic idsource.Sequential, fixed
// Clock) plus the project ID and root WorkItem ID a test can dispatch
// against.
func setupFixture(t *testing.T) (climessage.Dependencies, string, string) {
	t.Helper()
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: "project-1", Name: "project-1"})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	regCmd := testCommand("idem-repo-1", "hash-repo-1", "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "repo-1",
		RemoteLocator: "https://example.invalid/repo-1.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("activate repository: %v", err)
	}

	rootCmd := testCommand("idem-root-1", "hash-root-1", "CreateRootWorkItem")
	root, err := work.CreateRootWorkItem(ctx, uow, ids, rootCmd, work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "Implement the thing",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"services/api"}, Reason: "implement the thing",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}

	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}

	fixedNow := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	deps := climessage.Dependencies{
		UnitOfWork: uow, ArtifactStore: store, IDs: ids,
		Clock: fixedClock{now: fixedNow},
	}
	return deps, "project-1", root.WorkItemID
}

// fixedClock is this package's own minimal clock.Clock test double — a
// tiny local copy rather than importing internal/app/clock.Fixed, since
// that type's own Advance method needs no exercise here and a bare struct
// keeps this test file self-contained.
type fixedClock struct{ now time.Time }

func (f fixedClock) Now() time.Time { return f.now }

func isUsageError(err error) bool {
	return cli.IsUsageError(err)
}

// resultField extracts the top-level "result" object from a
// cli.ResultEnvelope-shaped JSON document and returns it re-encoded as its
// own JSON string — mirrors internal/delivery/cli/definitions's own
// identically named test helper.
func resultField(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return string(envelope.Result)
}
