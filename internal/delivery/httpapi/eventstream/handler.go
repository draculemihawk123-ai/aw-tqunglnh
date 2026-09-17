package eventstream

import (
	"net/http"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// httpEventSink is eventSink's real, production implementation — the one
// place this package ever touches a real http.ResponseWriter/http.Flusher.
// Only streamLoop's own single writer goroutine ever calls WriteEvent/
// WriteHeartbeat (stream.go's own doc comment), so this type itself needs
// no locking.
type httpEventSink struct {
	w            http.ResponseWriter
	flusher      http.Flusher
	writeTimeout time.Duration
}

// deadline sets a bounded deadline on the next real socket write —
// defense in depth alongside streamLoop's own channel-level backpressure
// detection, for a single write that itself blocks on an already-frozen
// socket rather than merely falling behind. Best-effort: a ResponseWriter
// that does not support it (e.g. httptest.ResponseRecorder) returns
// http.ErrNotSupported, silently ignored — WriteTimeout is a real-server
// hardening measure, never a contract this package's own tests must
// satisfy.
func (s *httpEventSink) deadline() {
	if s.writeTimeout <= 0 {
		return
	}
	_ = http.NewResponseController(s.w).SetWriteDeadline(time.Now().Add(s.writeTimeout))
}

func (s *httpEventSink) WriteEvent(msg httpapi.SSEMessage) error {
	s.deadline()
	return httpapi.WriteSSE(s.w, s.flusher, msg)
}

func (s *httpEventSink) WriteHeartbeat(comment string) error {
	s.deadline()
	return httpapi.WriteSSEComment(s.w, s.flusher, comment)
}

var _ eventSink = (*httpEventSink)(nil)

// handleWatchProjectEvents implements GET /projects/{id}/events/watch
// (operationId watchProjectEvents) — see eventstream.go's own package doc
// comment for the full cursor/retention/authorization design this handler
// composes: parse+require `cursor`, authorize+retention-probe atomically
// (probeProject), and — only once both pass — upgrade to SSE and hand off
// to streamLoop.
func handleWatchProjectEvents(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID := strings.TrimSpace(r.PathValue("id"))
		if projectID == "" {
			writeValidationError(w, "id", "is required")
			return
		}
		cursor, ok := parseCursor(r.URL.Query().Get("cursor"))
		if !ok {
			writeValidationError(w, "cursor", "is required and must be a non-negative integer observed cursor from a prior fetch response (e.g. a projection-backed query's own Freshness.AsOfJournalPosition)")
			return
		}

		retentionLimit := deps.retentionScanLimit()
		initial, err := probeProject(ctx, deps.UnitOfWork, projectID, cursor, retentionLimit+1)
		if err != nil {
			writeStreamError(w, err)
			return
		}
		if len(initial) > retentionLimit {
			writeRetentionResyncRequired(w)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "streaming is not supported by this response writer", nil)
			return
		}
		httpapi.SSEHeaders(w)
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		sink := &httpEventSink{w: w, flusher: flusher, writeTimeout: deps.writeTimeout()}
		streamLoop(ctx, deps, projectID, cursor, initial, sink)
	}
}
