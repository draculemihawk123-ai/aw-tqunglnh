package v6accept

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// kanbanView is the projected board: read-model rows plus the freshness the
// server attaches to every projected response.
type kanbanView struct {
	Items     []json.RawMessage `json:"items"`
	Freshness struct {
		Generation          int    `json:"generation"`
		AsOfJournalPosition int64  `json:"asOfJournalPosition"`
		Status              string `json:"status"`
	} `json:"freshness"`
}

func (j *journey) kanban(t *testing.T) kanbanView {
	t.Helper()
	var view kanbanView
	j.s.api.get(t, "/projects/"+j.projectID+"/work-items/kanban").requireStatus(t, http.StatusOK).decode(t, &view)
	return view
}

// projectionLive proves the read model is fed by the WORKER process alone:
// nothing in this test writes a projection row, yet the board shows every
// WorkItem the API process created, marked LIVE.
func (j *journey) projectionLive(t *testing.T, wantItems int) kanbanView {
	t.Helper()
	var view kanbanView
	waitFor(t, "the live projection consumer (in aw worker) to project every WorkItem", 60*time.Second, 200*time.Millisecond, func() bool {
		view = j.kanban(t)
		return len(view.Items) >= wantItems && view.Freshness.Status == "LIVE"
	})
	return view
}

// waitProjectionCaughtUp returns the board once the live consumer has applied
// everything: LIVE, and the position it is "as of" has stopped moving for
// three consecutive reads. The position is then a real journal position that
// an event stream from cursor 0 must reach.
func (j *journey) waitProjectionCaughtUp(t *testing.T) kanbanView {
	t.Helper()
	var view kanbanView
	stable := 0
	var last int64 = -1
	waitFor(t, "projection to catch up with the journal", 60*time.Second, 300*time.Millisecond, func() bool {
		view = j.kanban(t)
		if view.Freshness.Status != "LIVE" {
			stable, last = 0, -1
			return false
		}
		if view.Freshness.AsOfJournalPosition == last {
			stable++
		} else {
			stable, last = 0, view.Freshness.AsOfJournalPosition
		}
		return stable >= 3
	})
	return view
}

// eventTrace reads the project's whole event stream from cursor 0 and returns
// it once the stream has been quiet for a moment. (The stream carries only the
// project-visible events; the projection's own position may name an event the
// stream deliberately does not surface, so the trace is taken to be complete
// when writes have settled and nothing new arrives.)
func (j *journey) eventTrace(t *testing.T) []journalEvent {
	t.Helper()
	j.waitProjectionCaughtUp(t)
	events := j.watchEventsQuiet(t, 0, 1500*time.Millisecond, 30*time.Second)
	if len(events) == 0 {
		t.Fatal("the event stream from cursor 0 carried no events")
	}
	return events
}

// watchEventsQuiet collects stream events from cursor until none has arrived
// for idle (after at least one has), or max elapses.
func (j *journey) watchEventsQuiet(t *testing.T, cursor uint64, idle, max time.Duration) []journalEvent {
	t.Helper()
	api := j.s.api
	ctx, cancel := context.WithTimeout(context.Background(), max)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, api.baseURL+"/projects/"+j.projectID+"/events/watch?cursor="+itoa(cursor), nil)
	if err != nil {
		t.Fatalf("new SSE request: %v", err)
	}
	request.Header.Set(httpapi.SessionTokenHeader, api.token)
	request.Header.Set("Accept", "text/event-stream")
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("event stream status = %d", response.StatusCode)
	}

	arrived := make(chan journalEvent, 256)
	go func() {
		defer close(arrived)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var name, data string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if name == "project.invalidated" && data != "" {
					var summary journalEvent
					if json.Unmarshal([]byte(data), &summary) == nil {
						arrived <- summary
					}
				}
				name, data = "", ""
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
	}()

	var events []journalEvent
	for {
		wait := idle
		if len(events) == 0 {
			wait = max
		}
		select {
		case event, open := <-arrived:
			if !open {
				return events
			}
			events = append(events, event)
		case <-time.After(wait):
			return events
		}
	}
}

// projectionRebuild requests a rebuild of the WorkItem projection over HTTP,
// lets the worker run the shadow build and fenced cutover, and proves the
// rebuilt read model is identical to the live one it replaced while the
// generation advanced.
func (j *journey) projectionRebuild(t *testing.T) {
	api := j.s.api
	before := j.projectionLive(t, 1)

	key := "acc-rebuild-1"
	body := map[string]string{"projectionName": "workitem"}
	requested := api.post(t, "/projects/"+j.projectID+"/projection/rebuild", body, withIdempotencyKey(key)).requireStatus(t, http.StatusAccepted)
	var operation struct {
		OperationID string `json:"operationId"`
	}
	requested.decode(t, &operation)
	if operation.OperationID == "" {
		t.Fatalf("rebuild returned no operationId: %s", requested.body)
	}
	replay := api.post(t, "/projects/"+j.projectID+"/projection/rebuild", body, withIdempotencyKey(key)).requireStatus(t, http.StatusOK, http.StatusAccepted)
	var replayed struct {
		OperationID string `json:"operationId"`
	}
	replay.decode(t, &replayed)
	if replayed.OperationID != operation.OperationID {
		t.Fatalf("rebuild replay minted a second operation: %q vs %q", replayed.OperationID, operation.OperationID)
	}

	var phase string
	waitFor(t, "projection rebuild operation to reach a terminal phase (run by the worker)", 90*time.Second, 300*time.Millisecond, func() bool {
		var status struct {
			Phase string `json:"phase"`
		}
		api.get(t, "/projects/"+j.projectID+"/projection/rebuild-operations/"+operation.OperationID).requireStatus(t, http.StatusOK).decode(t, &status)
		phase = status.Phase
		return phase == "SUCCEEDED" || phase == "FAILED"
	})
	if phase != "SUCCEEDED" {
		t.Fatalf("projection rebuild ended in phase %s", phase)
	}

	after := j.projectionLive(t, len(before.Items))
	if after.Freshness.Generation != before.Freshness.Generation+1 {
		t.Fatalf("projection generation = %d after rebuild, want %d", after.Freshness.Generation, before.Freshness.Generation+1)
	}
	if !reflect.DeepEqual(normalizeRows(t, before.Items), normalizeRows(t, after.Items)) {
		t.Fatalf("rebuilt read model differs from the live one it replaced:\nbefore: %v\nafter:  %v", before.Items, after.Items)
	}
}

// normalizeRows decodes rows so the comparison is by value, not by byte order.
func normalizeRows(t *testing.T, rows []json.RawMessage) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0, len(rows))
	for _, raw := range rows {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("decode projected row: %v", err)
		}
		out = append(out, row)
	}
	return out
}

// sseEvent is one parsed server-sent event.
type sseEvent struct {
	ID    string
	Name  string
	Data  string
	Ended bool
}

// journalEvent is the minimal, authority-free summary every real stream event
// carries.
type journalEvent struct {
	JournalPosition uint64 `json:"journalPosition"`
	EventType       string `json:"eventType"`
	SchemaVersion   int    `json:"schemaVersion"`
}

// watchEvents opens the project's SSE stream at cursor and collects real
// events until enough(events) is true or the deadline passes. It always
// closes the connection.
func (j *journey) watchEvents(t *testing.T, cursor uint64, enough func([]journalEvent) bool, within time.Duration) []journalEvent {
	t.Helper()
	api := j.s.api
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	url := api.baseURL + "/projects/" + j.projectID + "/events/watch?cursor=" + itoa(cursor)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new SSE request: %v", err)
	}
	request.Header.Set(httpapi.SessionTokenHeader, api.token)
	request.Header.Set("Accept", "text/event-stream")
	streaming := &http.Client{}
	response, err := streaming.Do(request)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("event stream status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("event stream Content-Type = %q, want text/event-stream", got)
	}

	var events []journalEvent
	var current sseEvent
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if current.Name == "project.invalidated" && current.Data != "" {
				var summary journalEvent
				if err := json.Unmarshal([]byte(current.Data), &summary); err != nil {
					t.Fatalf("decode stream event %q: %v", current.Data, err)
				}
				events = append(events, summary)
				if enough(events) {
					return events
				}
			}
			current = sseEvent{}
		case strings.HasPrefix(line, ":"): // heartbeat comment: never an event
		case strings.HasPrefix(line, "id:"):
			current.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			current.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			current.Data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	return events
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// eventStreamFromZero reads the project's journal through SSE from cursor 0
// and proves the stream is ordered, carries only authority-free summaries and
// includes the WorkItem lifecycle events the journey produced. The returned
// slice is the durable trace later compared across a restart.
func (j *journey) eventStreamFromZero(t *testing.T, mustSee ...string) []journalEvent {
	t.Helper()
	want := map[string]bool{}
	for _, name := range mustSee {
		want[name] = false
	}
	events := j.watchEvents(t, 0, func(events []journalEvent) bool {
		for _, event := range events {
			if _, tracked := want[event.EventType]; tracked {
				want[event.EventType] = true
			}
		}
		for _, seen := range want {
			if !seen {
				return false
			}
		}
		return len(events) > 0
	}, 30*time.Second)
	for name, seen := range want {
		if !seen {
			t.Fatalf("event stream from cursor 0 never carried %s (saw %d events)", name, len(events))
		}
	}
	var last uint64
	for _, event := range events {
		if event.JournalPosition <= last {
			t.Fatalf("event stream is not strictly increasing: %d after %d", event.JournalPosition, last)
		}
		last = event.JournalPosition
	}
	return events
}
