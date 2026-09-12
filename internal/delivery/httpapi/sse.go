package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// SSEMessage is the canonical Server-Sent-Events wire shape V6-11's real
// project-event stream reuses (V6-02A's own "Phạm vi: ... SSE envelope"
// line) — an event ID, an event type name, and a JSON data payload. V6-11's
// own vocabulary: "Event ID is relevant global JournalPosition" — ID is a
// string here (not int64) so a caller can format it however that event
// source's own position type formats best, without this shared envelope
// needing to know it. Leave ID/Event empty for a heartbeat: WriteSSEComment
// below is the dedicated heartbeat helper, matching V6-11's own "Heartbeat
// has no event ID and never advances cursor" — a heartbeat is a comment
// line, not an SSEMessage with blank fields, so the two can never be
// confused by a future reader of a stream.
type SSEMessage struct {
	ID    string
	Event string
	Data  any
}

// SSEHeaders sets the standard response headers an SSE stream needs before
// its first WriteSSE/WriteSSEComment call: a client must see
// `Content-Type: text/event-stream` before it will start treating the
// response body as an event stream at all, and no cache/proxy may buffer
// or cache a live stream.
func SSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// A common nginx-specific opt-out for proxy response buffering, which
	// would otherwise defeat streaming entirely if this server ever sits
	// behind one; harmless when it does not.
	w.Header().Set("X-Accel-Buffering", "no")
}

// WriteSSE encodes msg in the standard SSE wire format (`id: ...`,
// `event: ...`, `data: ...`, each field on its own line, terminated by a
// blank line) and flushes immediately via flusher, so a streaming client
// sees it right away rather than buffered behind this process's own
// response buffering. flusher may be nil (e.g. in a test using
// httptest.ResponseRecorder, which does not implement http.Flusher); a nil
// flusher simply skips the flush.
func WriteSSE(w http.ResponseWriter, flusher http.Flusher, msg SSEMessage) error {
	var b strings.Builder
	if msg.ID != "" {
		fmt.Fprintf(&b, "id: %s\n", msg.ID)
	}
	if msg.Event != "" {
		fmt.Fprintf(&b, "event: %s\n", msg.Event)
	}
	if msg.Data != nil {
		data, err := json.Marshal(msg.Data)
		if err != nil {
			return fmt.Errorf("httpapi: marshal SSE data: %w", err)
		}
		fmt.Fprintf(&b, "data: %s\n", data)
	}
	b.WriteString("\n")
	if _, err := w.Write([]byte(b.String())); err != nil {
		return fmt.Errorf("httpapi: write SSE message: %w", err)
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// WriteSSEComment writes a comment-only line (": <text>\n\n") — the
// standard SSE idiom for a keep-alive that must never itself be parsed as
// an event by a compliant client. This is V6-11's own dedicated heartbeat
// shape: "Heartbeat has no event ID and never advances cursor" — a comment
// line carries no `id:` field at all, so it is structurally impossible for
// a heartbeat to advance a client's own last-seen-event-ID bookkeeping.
func WriteSSEComment(w http.ResponseWriter, flusher http.Flusher, comment string) error {
	if _, err := fmt.Fprintf(w, ": %s\n\n", comment); err != nil {
		return fmt.Errorf("httpapi: write SSE comment: %w", err)
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}
