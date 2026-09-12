package httpapi_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// TestWriteSSE_MatchesGoldenWireFormat is the "shared ... SSE golden"
// Verify bullet's own proof: a real SSEMessage encodes to the exact,
// frozen wire bytes — id/event/data lines in that order, each newline
// terminated, followed by one blank line.
func TestWriteSSE_MatchesGoldenWireFormat(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "golden", "sse_message_v1.txt"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}

	// A struct, not a map, so field order (and so the golden fixture's own
	// byte-for-byte "data:" line) is exactly the declared order —
	// encoding/json.Marshal sorts map keys alphabetically but preserves a
	// struct's declared field order.
	type workItemUpdated struct {
		WorkItemID string `json:"workItemId"`
		Status     string `json:"status"`
	}
	rec := httptest.NewRecorder()
	msg := httpapi.SSEMessage{
		ID:    "42",
		Event: "work_item_updated",
		Data:  workItemUpdated{WorkItemID: "wi-1", Status: "BLOCKED"},
	}
	if err := httpapi.WriteSSE(rec, nil, msg); err != nil {
		t.Fatalf("WriteSSE: %v", err)
	}

	if rec.Body.String() != string(want) {
		t.Fatalf("WriteSSE wire bytes = %q, want %q", rec.Body.String(), string(want))
	}
}

func TestWriteSSE_OmitsAbsentFields(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := httpapi.WriteSSE(rec, nil, httpapi.SSEMessage{Event: "ping"}); err != nil {
		t.Fatalf("WriteSSE: %v", err)
	}
	got := rec.Body.String()
	if strings.Contains(got, "id:") {
		t.Fatalf("wire bytes = %q, must not contain an id: line when ID is empty", got)
	}
	if !strings.Contains(got, "event: ping\n") {
		t.Fatalf("wire bytes = %q, want an event: ping line", got)
	}
	if strings.Contains(got, "data:") {
		t.Fatalf("wire bytes = %q, must not contain a data: line when Data is nil", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("wire bytes = %q, must end with a blank line", got)
	}
}

// TestWriteSSEComment_NeverIncludesEventID is V6-11's own dedicated
// heartbeat proof: "Heartbeat has no event ID and never advances cursor" —
// a comment line structurally carries no id: field at all.
func TestWriteSSEComment_NeverIncludesEventID(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := httpapi.WriteSSEComment(rec, nil, "heartbeat"); err != nil {
		t.Fatalf("WriteSSEComment: %v", err)
	}
	got := rec.Body.String()
	if got != ": heartbeat\n\n" {
		t.Fatalf("wire bytes = %q, want %q", got, ": heartbeat\n\n")
	}
	if strings.Contains(got, "id:") || strings.Contains(got, "event:") {
		t.Fatalf("wire bytes = %q, a comment must never carry id:/event: fields", got)
	}
}

func TestSSEHeaders_SetsExpectedHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.SSEHeaders(rec)

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if got := rec.Header().Get("Connection"); got != "keep-alive" {
		t.Fatalf("Connection = %q, want keep-alive", got)
	}
}
