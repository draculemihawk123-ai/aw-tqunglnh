package message_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// redactedPlaceholder mirrors internal/app/redact's own private "placeholder"
// constant ("[REDACTED]") — that package's test (internal/app/redact/redact_test.go)
// can reference it directly since it lives in the same package; this test
// lives outside it, so the known literal value is asserted instead.
const redactedPlaceholder = "[REDACTED]"

func testCommand(idempotencyKey, requestHash string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"),
		RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type:        "AppendMessage", RequestHash: requestHash,
	}
}

// setupFixture returns a fake UnitOfWork with one project, one ACTIVE
// repository and one root WorkItem already created — the minimum a message
// can attach to — plus a real filesystem ArtifactStore rooted at a fresh
// temp directory. Mirrors internal/app/work/commands_test.go's own
// mustCreateProject/mustCreateActiveRepository helpers (test setup, not the
// behavior under test).
func setupFixture(t *testing.T) (*fake.UnitOfWork, ports.ArtifactStore, idsource.Source, string) {
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

	regCmd := testCommand("idem-repo-1", "hash-repo-1")
	regCmd.Type = "RegisterRepository"
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

	rootCmd := testCommand("idem-root-1", "hash-root-1")
	rootCmd.Type = "CreateRootWorkItem"
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
	return uow, store, ids, root.WorkItemID
}

func TestAppendMessage_HappyPath_ProducesRetrievableArtifactAndMessage(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())

	result, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-1", "hash-msg-1"), appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		Content: []byte("hello from the user"), ContentType: "text/plain", Sensitivity: redact.Public,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if result.Sequence != 1 || result.WorkItemID != workItemID {
		t.Fatalf("unexpected result: %+v", result)
	}

	var artifactRef ports.ArtifactRef
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		a, err := tx.Artifacts().GetArtifact(ctx, result.ContentArtifactID)
		artifactRef = ports.ArtifactRef{Locator: a.Locator, SHA256: a.ContentHash, Size: a.Size}
		return err
	}); err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	reader, err := store.Open(ctx, artifactRef)
	if err != nil {
		t.Fatalf("Open content: %v", err)
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(content) != "hello from the user" {
		t.Fatalf("stored content = %q, want the original text", content)
	}

	messages, err := appmessage.ListMessages(ctx, uow, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 || string(messages[0].ID) != result.MessageID {
		t.Fatalf("ListMessages = %+v, want exactly the one appended message", messages)
	}
}

func TestAppendMessage_Idempotent_ReplaysWithoutDuplicating(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	cmd := testCommand("idem-msg-1", "hash-msg-1")
	req := appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		Content: []byte("hello"), ContentType: "text/plain", Sensitivity: redact.Public,
	}

	first, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, cmd, req)
	if err != nil {
		t.Fatalf("first AppendMessage: %v", err)
	}
	second, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, cmd, req)
	if err != nil {
		t.Fatalf("second AppendMessage: %v", err)
	}
	if first != second {
		t.Fatalf("replay returned %+v, want identical %+v", second, first)
	}

	messages, err := appmessage.ListMessages(ctx, uow, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want exactly 1 (no duplicate from the replay)", len(messages))
	}
}

func TestAppendMessage_DifferentRequestHash_SameKey_ReturnsReceiptConflict(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	baseReq := appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		ContentType: "text/plain", Sensitivity: redact.Public,
	}

	first := baseReq
	first.Content = []byte("hello")
	if _, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-1", "hash-a"), first); err != nil {
		t.Fatalf("first AppendMessage: %v", err)
	}

	second := baseReq
	second.Content = []byte("a totally different message")
	_, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-1", "hash-b"), second)
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("err = %v, want ports.ErrReceiptConflict", err)
	}
}

func TestAppendMessage_ContentTooLarge_Rejected(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())

	_, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-1", "hash-1"), appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		Content: make([]byte, appmessage.MaxContentSize+1), ContentType: "text/plain", Sensitivity: redact.Public,
	})
	if !errors.Is(err, appmessage.ErrContentTooLarge) {
		t.Fatalf("err = %v, want appmessage.ErrContentTooLarge", err)
	}
}

func TestAppendMessage_SensitivitySecret_ContentRedactedInStorage(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())

	result, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-1", "hash-1"), appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		Content: []byte("sk-live-super-secret-value"), ContentType: "text/plain", Sensitivity: redact.Secret,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	assertStoredContent(t, ctx, uow, store, result.ContentArtifactID, redactedPlaceholder)
}

func TestAppendMessage_MatcherExactMatch_ContentRedactedInStorage(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	secret := "hunter2"

	result, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-1", "hash-1"), appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		// Sensitivity Public on its own would NOT redact this — only the
		// Matcher's own exact-value match does, confirming Tagged's "an
		// exact secret match always wins regardless of declared
		// Sensitivity" contract.
		Content: []byte(secret), ContentType: "text/plain", Sensitivity: redact.Public,
		Matcher: redact.NewMatcher(secret),
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	assertStoredContent(t, ctx, uow, store, result.ContentArtifactID, redactedPlaceholder)
}

func TestAppendMessage_MatcherNeverScansSubstringWithinFreeText(t *testing.T) {
	// Documents redact.Matcher's own confirmed contract (V1-09's own
	// checklist correction): exact equality only, never a substring scan.
	// A secret embedded inside a larger free-text message is NOT redacted
	// — this is expected, not a gap this task's own redaction policy
	// closes.
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	secret := "hunter2"

	result, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-1", "hash-1"), appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		Content: []byte("leaked " + secret + " in message"), ContentType: "text/plain", Sensitivity: redact.Public,
		Matcher: redact.NewMatcher(secret),
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	assertStoredContent(t, ctx, uow, store, result.ContentArtifactID, "leaked "+secret+" in message")
}

func TestAppendMessage_SequentialAppends_IncrementSequenceInOrder(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())

	for i, text := range []string{"first", "second", "third"} {
		result, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-msg-"+text, "hash-"+text), appmessage.AppendMessageRequest{
			ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
			Content: []byte(text), ContentType: "text/plain", Sensitivity: redact.Public,
		})
		if err != nil {
			t.Fatalf("AppendMessage(%s): %v", text, err)
		}
		if result.Sequence != uint64(i+1) {
			t.Fatalf("AppendMessage(%s).Sequence = %d, want %d", text, result.Sequence, i+1)
		}
	}

	messages, err := appmessage.ListMessages(ctx, uow, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}
	for i, m := range messages {
		if m.Sequence != uint64(i+1) {
			t.Fatalf("messages[%d].Sequence = %d, want %d", i, m.Sequence, i+1)
		}
	}
}

func TestAppendMessage_RejectsInvalidRequests(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	base := appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		Content: []byte("hello"), ContentType: "text/plain", Sensitivity: redact.Public,
	}

	cases := []struct {
		name   string
		mutate func(appmessage.AppendMessageRequest) appmessage.AppendMessageRequest
	}{
		{"empty ProjectID", func(r appmessage.AppendMessageRequest) appmessage.AppendMessageRequest { r.ProjectID = ""; return r }},
		{"empty WorkItemID", func(r appmessage.AppendMessageRequest) appmessage.AppendMessageRequest { r.WorkItemID = ""; return r }},
		{"unknown Role", func(r appmessage.AppendMessageRequest) appmessage.AppendMessageRequest {
			r.Role = messagedomain.Role("BOGUS")
			return r
		}},
		{"empty Content", func(r appmessage.AppendMessageRequest) appmessage.AppendMessageRequest { r.Content = nil; return r }},
		{"empty ContentType", func(r appmessage.AppendMessageRequest) appmessage.AppendMessageRequest {
			r.ContentType = ""
			return r
		}},
		{"unknown Sensitivity", func(r appmessage.AppendMessageRequest) appmessage.AppendMessageRequest {
			r.Sensitivity = redact.Sensitivity(99)
			return r
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := appmessage.AppendMessage(ctx, uow, store, ids, clk, testCommand("idem-"+tc.name, "hash-"+tc.name), tc.mutate(base))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func assertStoredContent(t *testing.T, ctx context.Context, uow *fake.UnitOfWork, store ports.ArtifactStore, artifactID, want string) {
	t.Helper()
	var artifactRef ports.ArtifactRef
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		a, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		artifactRef = ports.ArtifactRef{Locator: a.Locator, SHA256: a.ContentHash, Size: a.Size}
		return err
	}); err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	reader, err := store.Open(ctx, artifactRef)
	if err != nil {
		t.Fatalf("Open content: %v", err)
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(content) != want {
		t.Fatalf("stored content = %q, want %q", content, want)
	}
}
