package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

type createProjectRequest struct {
	Name string `json:"name"`
}

type createProjectResult struct {
	ProjectID string `json:"project_id"`
}

// handleCreateProject is V1-06's own illustrative "handler mẫu": it loads
// the command-receipt idempotency ledger first (so an identical retry
// replays the stored result and a same-key/different-payload retry is
// rejected as a conflict, per docs/design/03-v1-alpha-foundation.md
// V1-06's Verify), and otherwise commits the new project row, its
// ProjectCreated domain event, and the receipt recording that outcome all
// inside one WithSerializedWrite call — never separately, so a crash
// between steps can never leave state and event/receipt out of sync.
func handleCreateProject(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req createProjectRequest) (createProjectResult, error) {
	var result createProjectResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		existing, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existing.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(existing.ResultJSON), &result)
		}

		projectID, isProjectScoped := cmd.Scope.ProjectID()
		if !isProjectScoped {
			return apperror.New(apperror.CodeInvalidArgument, "CreateProject requires a project-scoped command", false)
		}

		adapter, ok := tx.(*txAdapter)
		if !ok {
			return apperror.New(apperror.CodeInternal, "handleCreateProject requires the sqlite Tx adapter", false)
		}
		now := cmd.RequestedAt.UTC().Format(time.RFC3339Nano)
		if _, err := adapter.tx.ExecContext(ctx,
			`INSERT INTO projects(id, name, status, version, created_at, updated_at) VALUES (?, ?, 'ACTIVE', 1, ?, ?)`,
			projectID, req.Name, now, now,
		); err != nil {
			return MapSQLiteError(fmt.Errorf("insert project: %w", err))
		}

		payload, err := json.Marshal(struct {
			ProjectID string `json:"project_id"`
			Name      string `json:"name"`
		}{ProjectID: projectID, Name: req.Name})
		if err != nil {
			return fmt.Errorf("marshal ProjectCreated payload: %w", err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID:            cmd.ID + "-created",
			ProjectID:     projectID,
			AggregateType: "Project",
			AggregateID:   projectID,
			Sequence:      1,
			EventType:     "ProjectCreated",
			SchemaVersion: 1,
			PayloadJSON:   string(payload),
			CorrelationID: cmd.CorrelationID,
			CreatedAt:     cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = createProjectResult{ProjectID: projectID}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor:          cmd.Actor,
			Scope:          cmd.Scope,
			IdempotencyKey: cmd.IdempotencyKey,
			CommandType:    cmd.Type,
			RequestHash:    cmd.RequestHash,
			ResultJSON:     string(resultJSON),
			CreatedAt:      cmd.RequestedAt,
		})
	})
	return result, err
}

func newCreateProjectCommand(idempotencyKey, projectID, requestHash string) ports.Command {
	return ports.Command{
		ID:              "cmd-" + idempotencyKey,
		IdempotencyKey:  idempotencyKey,
		Actor:           "actor-1",
		CorrelationID:   "corr-1",
		Scope:           ports.ProjectScope(projectID),
		ExpectedVersion: 0,
		RequestedAt:     time.Now().UTC(),
		Type:            "CreateProject",
		RequestHash:     requestHash,
	}
}

func TestHandleCreateProject_CommitsStateEventAndReceiptAtomically(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-handler-atomic.db")
	uow := NewUnitOfWork(store)

	cmd := newCreateProjectCommand("idem-1", "proj-1", "hash-a")
	result, err := handleCreateProject(ctx, uow, cmd, createProjectRequest{Name: "demo"})
	if err != nil {
		t.Fatalf("handleCreateProject: %v", err)
	}
	if result.ProjectID != "proj-1" {
		t.Fatalf("result.ProjectID = %q, want %q", result.ProjectID, "proj-1")
	}

	var projectCount, eventCount, receiptCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, "proj-1").Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE aggregate_id = ?`, "proj-1").Scan(&eventCount); err != nil {
		t.Fatalf("count domain_events: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM command_receipts WHERE idempotency_key = ?`, "idem-1").Scan(&receiptCount); err != nil {
		t.Fatalf("count command_receipts: %v", err)
	}
	if projectCount != 1 || eventCount != 1 || receiptCount != 1 {
		t.Fatalf("project/event/receipt counts = %d/%d/%d, want 1/1/1", projectCount, eventCount, receiptCount)
	}
}

func TestHandleCreateProject_DuplicateSamePayload_ReplaysStoredResultWithoutReinserting(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-handler-replay.db")
	uow := NewUnitOfWork(store)

	cmd := newCreateProjectCommand("idem-1", "proj-1", "hash-a")
	first, err := handleCreateProject(ctx, uow, cmd, createProjectRequest{Name: "demo"})
	if err != nil {
		t.Fatalf("first handleCreateProject: %v", err)
	}

	second, err := handleCreateProject(ctx, uow, cmd, createProjectRequest{Name: "demo"})
	if err != nil {
		t.Fatalf("second (replayed) handleCreateProject: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first result %+v", second, first)
	}

	var projectCount, eventCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, "proj-1").Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE aggregate_id = ?`, "proj-1").Scan(&eventCount); err != nil {
		t.Fatalf("count domain_events: %v", err)
	}
	if projectCount != 1 || eventCount != 1 {
		t.Fatalf("project/event counts after replay = %d/%d, want 1/1 (a replay must never redo the mutation)", projectCount, eventCount)
	}
}

func TestHandleCreateProject_DuplicateDifferentPayload_ReturnsConflictAndDoesNotMutate(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-handler-conflict.db")
	uow := NewUnitOfWork(store)

	first := newCreateProjectCommand("idem-1", "proj-1", "hash-a")
	if _, err := handleCreateProject(ctx, uow, first, createProjectRequest{Name: "demo"}); err != nil {
		t.Fatalf("first handleCreateProject: %v", err)
	}

	second := newCreateProjectCommand("idem-1", "proj-1", "hash-b")
	_, err := handleCreateProject(ctx, uow, second, createProjectRequest{Name: "different-name"})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("second handleCreateProject err = %v, want ports.ErrReceiptConflict", err)
	}

	var name string
	if err := store.db.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = ?`, "proj-1").Scan(&name); err != nil {
		t.Fatalf("read back project name: %v", err)
	}
	if name != "demo" {
		t.Fatalf("project name = %q, want %q (the conflicting retry must never overwrite the original commit)", name, "demo")
	}
}

func TestHandleCreateProject_InstallationScopedCommand_Rejected(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-handler-installation-rejected.db")
	uow := NewUnitOfWork(store)

	cmd := ports.Command{
		ID: "cmd-installation", IdempotencyKey: "idem-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.InstallationScope(),
		RequestedAt: time.Now().UTC(), Type: "CreateProject", RequestHash: "hash-a",
	}
	_, err := handleCreateProject(ctx, uow, cmd, createProjectRequest{Name: "demo"})
	if apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("err = %v, want apperror.CodeInvalidArgument (CreateProject is inherently project-scoped)", err)
	}
}
