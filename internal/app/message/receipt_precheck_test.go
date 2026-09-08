package message_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// spyArtifactStore wraps a real ports.ArtifactStore and counts Put calls —
// the audit finding (2026-09-08) fix's own required evidence: "spy
// ArtifactStore chứng minh replay/conflict có PutCalls=0".
type spyArtifactStore struct {
	ports.ArtifactStore
	putCalls int
}

func (s *spyArtifactStore) Put(ctx context.Context, meta ports.ArtifactMetadata, body io.Reader) (ports.ArtifactRef, error) {
	s.putCalls++
	return s.ArtifactStore.Put(ctx, meta, body)
}

var _ ports.ArtifactStore = (*spyArtifactStore)(nil)

// TestAppendMessage_Replay_NeverCallsPutAgain proves the pre-check
// (loadOrValidateReceipt) actually short-circuits before ArtifactStore.Put
// on a pure replay — not just that the end result happens to be identical,
// which the pre-existing TestAppendMessage_Idempotent_ReplaysWithoutDuplicating
// could not distinguish from "Put ran again but harmlessly deduplicated".
func TestAppendMessage_Replay_NeverCallsPutAgain(t *testing.T) {
	uow, realStore, ids, workItemID := setupFixture(t)
	spy := &spyArtifactStore{ArtifactStore: realStore}
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	cmd := testCommand("idem-msg-1", "hash-msg-1")
	req := appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		Content: []byte("hello"), ContentType: "text/plain", Sensitivity: redact.Public,
	}

	if _, err := appmessage.AppendMessage(ctx, uow, spy, ids, clk, cmd, req); err != nil {
		t.Fatalf("first AppendMessage: %v", err)
	}
	if spy.putCalls != 1 {
		t.Fatalf("putCalls after first call = %d, want 1", spy.putCalls)
	}

	if _, err := appmessage.AppendMessage(ctx, uow, spy, ids, clk, cmd, req); err != nil {
		t.Fatalf("replayed AppendMessage: %v", err)
	}
	if spy.putCalls != 1 {
		t.Fatalf("putCalls after replay = %d, want still 1 (replay must never call Put again)", spy.putCalls)
	}
}

// TestAppendMessage_ReceiptConflict_NeverCallsPut proves a genuine
// RequestHash conflict is caught by the pre-check before Put ever runs for
// the conflicting (second) request.
func TestAppendMessage_ReceiptConflict_NeverCallsPut(t *testing.T) {
	uow, realStore, ids, workItemID := setupFixture(t)
	spy := &spyArtifactStore{ArtifactStore: realStore}
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	baseReq := appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		ContentType: "text/plain", Sensitivity: redact.Public,
	}

	first := baseReq
	first.Content = []byte("hello")
	if _, err := appmessage.AppendMessage(ctx, uow, spy, ids, clk, testCommand("idem-msg-1", "hash-a"), first); err != nil {
		t.Fatalf("first AppendMessage: %v", err)
	}
	if spy.putCalls != 1 {
		t.Fatalf("putCalls after first call = %d, want 1", spy.putCalls)
	}

	second := baseReq
	second.Content = []byte("a totally different message")
	_, err := appmessage.AppendMessage(ctx, uow, spy, ids, clk, testCommand("idem-msg-1", "hash-b"), second)
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("err = %v, want ports.ErrReceiptConflict", err)
	}
	if spy.putCalls != 1 {
		t.Fatalf("putCalls after conflicting call = %d, want still 1 (conflict must never call Put for the rejected request)", spy.putCalls)
	}
}
