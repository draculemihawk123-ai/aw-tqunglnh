package v6accept

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"testing"
)

// snapshotPaths are the read-only queries whose answers must be identical
// before and after a restart of both processes. They cover every read model
// the journey has touched: installation registry, catalog, work, family,
// workspace, conversation, definitions and the projected board.
func (j *journey) snapshotPaths() []string {
	project := "/projects/" + j.projectID
	paths := []string{
		"/adapter-builds",
		"/settings/safe",
		project,
		project + "/repositories",
		project + "/work-items",
		project + "/work-items/" + j.rootWorkItemID,
		project + "/work-items/" + j.childWorkItemID,
		project + "/work-items/" + j.childWorkItemID + "/messages",
		project + "/task-families/" + j.familyID,
		project + "/workspace-sets/" + j.familyID,
		project + "/work-items/kanban",
	}
	if j.runID != "" {
		paths = append(paths, "/runs/"+j.runID, "/runs/"+j.runID+"/graph", "/runs/"+j.runID+"/timeline")
	}
	for _, id := range j.extraSnapshotPaths {
		paths = append(paths, id)
	}
	return paths
}

// snapshot fetches every snapshot path and returns the normalized bodies.
func (j *journey) snapshot(t *testing.T) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, path := range j.snapshotPaths() {
		response := j.s.api.get(t, path)
		if response.status != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, response.status, response.body)
		}
		var decoded any
		if err := json.Unmarshal(response.body, &decoded); err != nil {
			t.Fatalf("decode GET %s: %v", path, err)
		}
		out[path] = stripVolatile(decoded)
	}
	return out
}

// stripVolatile removes the fields that legitimately differ between two
// reads of unchanged state: the freshness envelope carries the journal
// position "as of" the read, and a restarted worker may append recovery
// bookkeeping events, so that position is not part of the equality claim.
func stripVolatile(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key := range typed {
			if key == "freshness" {
				delete(typed, key)
				continue
			}
			typed[key] = stripVolatile(typed[key])
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = stripVolatile(typed[i])
		}
		return typed
	default:
		return value
	}
}

// restartEquality stops BOTH processes gracefully, starts fresh ones on the
// same database, and proves every recorded query answers identically and the
// event journal, read from cursor 0 through SSE, has the identical prefix.
func (j *journey) restartEquality(t *testing.T) {
	before := j.snapshot(t)
	eventsBefore := j.eventTrace(t)
	lastBefore := eventsBefore[len(eventsBefore)-1].JournalPosition

	j.s.restart(t)

	after := j.snapshot(t)
	var differing []string
	for path, want := range before {
		if !reflect.DeepEqual(want, after[path]) {
			differing = append(differing, path)
		}
	}
	sort.Strings(differing)
	for _, path := range differing {
		wantJSON, _ := json.Marshal(before[path])
		gotJSON, _ := json.Marshal(after[path])
		t.Errorf("GET %s differs across restart:\nbefore: %s\nafter:  %s", path, tail(string(wantJSON), 600), tail(string(gotJSON), 600))
	}
	if len(differing) > 0 {
		t.FailNow()
	}

	eventsAfter := j.eventTrace(t)
	if eventsAfter[len(eventsAfter)-1].JournalPosition < lastBefore {
		t.Fatalf("after restart the stream ends at position %d, before the %d it had reached", eventsAfter[len(eventsAfter)-1].JournalPosition, lastBefore)
	}
	if len(eventsAfter) < len(eventsBefore) {
		t.Fatalf("after restart the stream has %d events, want at least the %d recorded before", len(eventsAfter), len(eventsBefore))
	}
	for i, want := range eventsBefore {
		if eventsAfter[i] != want {
			t.Fatalf("event %d differs across restart: before %+v, after %+v", i, want, eventsAfter[i])
		}
	}
	t.Logf("restart preserved %d snapshot queries and a %d-event journal prefix", len(before), len(eventsBefore))
}
