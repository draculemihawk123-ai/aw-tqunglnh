package v6accept

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteFileURI builds the identical file: URI
// internal/adapters/sqlite.Open itself builds (db.go) — required on
// Windows, where a naive "file:"+path concatenation mishandles a drive
// letter and backslashes and can silently make the sqlite driver open (or
// even create) an entirely different, wrong file instead of erroring
// (confirmed empirically while building this scenario: without this exact
// conversion, the poison event landed nowhere the real server processes
// ever read). Duplicated rather than imported because db.go's own Open
// returns an opaque *Store with no raw *sql.DB access — exactly the
// narrow reason this one scenario needs a raw connection at all.
func sqliteFileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	uriPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" && uriPath[0] != '/' {
		uriPath = "/" + uriPath
	}
	u := &url.URL{Scheme: "file", Path: uriPath}
	query := u.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = query.Encode()
	return u.String()
}

// seedPoisonDomainEvent is the ONE deliberate exception, in this whole
// suite, to "driven only through the public HTTP surface" — documented
// here and in baocaov6checklist.md's own V6-14A section exactly why: no
// route in this product writes an arbitrary raw domain event (by
// construction — every real command emits only events its own aggregate
// logic already knows how to produce), so there is no public way to make
// the journal contain something projection.Catalog cannot classify. That
// is precisely the point of this scenario: prove what happens when the
// journal DOES contain such a thing (a real operational possibility — a
// downgrade, a schema migration gap, on-disk corruption) — which can only
// ever be produced by writing the row directly, with BOTH real processes
// stopped first so this write can never race a live writer.
//
// The row shape mirrors internal/adapters/sqlite/domain_events.go's own
// Append exactly (same columns, same journal_position = MAX+1 allocation,
// same one-outbox-row-per-event invariant), except EventType/SchemaVersion
// name a pair projection.Catalog has never registered — the "unregistered/
// unclassified event" poison path consumer.go's own applyLeasedBatch
// already implements and this scenario now proves end-to-end.
func seedPoisonDomainEvent(t *testing.T, dbPath, projectID string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteFileURI(dbPath))
	if err != nil {
		t.Fatalf("open sqlite %s directly: %v", dbPath, err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin direct sqlite transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	var journalPosition int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(journal_position), 0) + 1 FROM domain_events`).Scan(&journalPosition); err != nil {
		t.Fatalf("allocate journal position directly: %v", err)
	}

	const eventID = "v6-14a-poison-event-1"
	const eventType = "V6_14A_UNCLASSIFIABLE_POISON_EVENT"
	const schemaVersion = 999
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
INSERT INTO domain_events (
    id, project_id, aggregate_type, aggregate_id, sequence, journal_position,
    event_type, schema_version, payload_json, correlation_id, causation_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID, projectID, "WorkItem", "v6-14a-poison-aggregate", 1, journalPosition,
		eventType, schemaVersion, `{"note":"v6-14a deliberate poison event"}`, "v6-14a-poison-correlation", nil, now,
	); err != nil {
		t.Fatalf("insert poison domain event directly: %v", err)
	}
	if _, err := tx.Exec(`
INSERT INTO outbox (id, event_id, topic, payload_json, status, available_at, created_at)
VALUES (?, ?, ?, ?, 'AVAILABLE', ?, ?)`,
		eventID+"-outbox", eventID, eventType, `{"note":"v6-14a deliberate poison event"}`, now, now,
	); err != nil {
		t.Fatalf("insert poison event's own outbox row directly: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit direct sqlite transaction: %v", err)
	}
	t.Logf("seeded poison event %s (%s v%d) at journal position %d", eventID, eventType, schemaVersion, journalPosition)
}

// TestV6HTTPAcceptance_Fault_PoisonProjection is V6-14A scenario 10:
// "poison projection". See seedPoisonDomainEvent's own doc comment for why
// direct SQLite seeding is the one deliberate, documented exception to
// this whole suite's "public HTTP only" discipline here.
//
// internal/app/projection/consumer.go's own applyLeasedBatch: an event
// whose (EventType, SchemaVersion) the Catalog does not recognize rolls
// back the whole apply transaction and records the poison in a SEPARATE
// transaction that CASes the checkpoint to DEGRADED at the last-good
// cursor (never past the poison). Critically — read directly off that
// same file, not assumed — a DEGRADED generation then stays frozen:
// applyLeasedBatch's own very first check is `if lease.Status ==
// ProjectionDegraded { return }`, so the live consumer never even attempts
// to scan past the poison again. This scenario proves that literal,
// current behavior rather than the looser assumption that only the ONE
// poison event would be skipped: a WorkItem created AFTER the poison is
// verified to NEVER appear in the "workitem" projection, while the
// WorkItem created BEFORE it remains visible and correct.
//
// Verify: the worker process does not crash (it keeps answering
// /health/live and other real routes throughout), the pre-poison
// WorkItem's own row is untouched, the post-poison WorkItem's own row
// never appears, kanban's own freshness.status genuinely reports DEGRADED
// (never fabricated as LIVE), the worker's own stderr carries a real
// PROJECTION_POISON log line, and — checked explicitly, per this task's
// own "if nothing surfaces it today, say so plainly" instruction —
// GET /doctor does NOT surface this degradation anywhere in its own
// checks today.
func TestV6HTTPAcceptance_Fault_PoisonProjection(t *testing.T) {
	requireAcceptance(t)
	j := newFaultStack(t)

	beforeID := j.createRootWorkItem(t, "fault-poison-before")
	beforeView := j.projectionLive(t, 1)
	if beforeView.Freshness.Status != "LIVE" {
		t.Fatalf("freshness before poisoning = %s, want LIVE", beforeView.Freshness.Status)
	}

	serveGraceful, workerGraceful := j.s.stop(t)
	if !serveGraceful || !workerGraceful {
		t.Fatalf("stop before seeding the poison event was not graceful: serve=%v worker=%v", serveGraceful, workerGraceful)
	}
	seedPoisonDomainEvent(t, j.s.dbPath, j.projectID)
	j.s.start(t)

	// The worker must not crash: it keeps answering ordinary routes.
	j.s.api.get(t, "/health/live").requireStatus(t, http.StatusOK)

	afterID := j.createRootWorkItem(t, "fault-poison-after")

	// The live projection consumer's own lease (projectionLeaseTTL, 30s,
	// cmd/aw/worker.go) is never explicitly released on a graceful worker
	// shutdown — a real, general property of this mechanism, not specific
	// to this scenario's own crash — so the FRESH worker started above
	// cannot actually acquire it (ErrOptimisticConflict, silently retried
	// every tick) until the OLD, now-dead worker's own last renewal
	// finally ages out. The bound below accommodates that real handoff
	// delay; it is not evidence of anything broken.
	waitFor(t, "the projection to report DEGRADED after the poison event (after the prior worker's own consumer lease ages out)", 45*time.Second, 500*time.Millisecond, func() bool {
		return j.kanban(t).Freshness.Status == "DEGRADED"
	})

	// Give the worker a real, bounded further window in case it were (it
	// should not be) about to make more progress, then take a final
	// reading.
	time.Sleep(500 * time.Millisecond)
	final := j.kanban(t)
	if final.Freshness.Status != "DEGRADED" {
		t.Fatalf("freshness status after the poison event = %s, want DEGRADED", final.Freshness.Status)
	}

	var rows []struct {
		WorkItemID string `json:"workItemId"`
	}
	for _, raw := range final.Items {
		var row struct {
			WorkItemID string `json:"workItemId"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("decode projected row: %v", err)
		}
		rows = append(rows, row)
	}
	foundBefore, foundAfter := false, false
	for _, row := range rows {
		if row.WorkItemID == beforeID {
			foundBefore = true
		}
		if row.WorkItemID == afterID {
			foundAfter = true
		}
	}
	if !foundBefore {
		t.Fatalf("the pre-poison WorkItem %s is missing from the projection after the poison event — it must remain visible and correct", beforeID)
	}
	if foundAfter {
		t.Fatalf("the post-poison WorkItem %s IS visible in the projection — expected it to NEVER appear: a DEGRADED generation stays frozen at the last-good cursor and never resumes scanning past the poison (consumer.go's own documented behavior)", afterID)
	}
	if len(rows) != 1 {
		t.Fatalf("projected row count after the poison event = %d, want exactly 1 (only the pre-poison WorkItem)", len(rows))
	}

	if !strings.Contains(j.s.worker.stderr.String(), "PROJECTION_POISON") {
		t.Fatalf("worker stderr never logged PROJECTION_POISON:\n%s", tail(j.s.worker.stderr.String(), 4000))
	}

	// Documented, checked explicitly: /doctor does not surface this today.
	var doctor struct {
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	j.s.api.get(t, "/doctor").requireStatus(t, http.StatusOK).decode(t, &doctor)
	for _, check := range doctor.Checks {
		if strings.Contains(strings.ToLower(check.Name), "projection") {
			t.Fatalf("GET /doctor unexpectedly names a projection check (%s = %s) — this test's own doc comment (and baocaov6checklist.md) must be corrected to describe it instead of asserting its absence", check.Name, check.Status)
		}
	}
	t.Logf("confirmed: GET /doctor's own %d checks do not mention the projection at all (status=%s) — a degraded projection is not surfaced there today", len(doctor.Checks), doctor.Status)
}
