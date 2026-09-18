package events

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// ProjectEventSummary is the one JSON shape every NDJSON line `aw events
// watch` writes carries — a deliberate, independent duplicate of
// internal/delivery/httpapi/eventstream.ProjectEventSummary's own field set
// (never a content feed — see doc.go's own "Wire shape and redaction"
// section for why this package never imports that one instead).
type ProjectEventSummary struct {
	JournalPosition uint64 `json:"journalPosition"`
	EventType       string `json:"eventType"`
	SchemaVersion   int    `json:"schemaVersion"`
}

// ErrProjectNotAuthorized is probeProjectEvents' own sentinel for "the
// Project exists but is not project.ProjectActive" — mirrors
// internal/delivery/httpapi/eventstream/errors.go's own errProjectNotAuthorized
// exactly. Unlike that package, this one has no cross-tenant leakage
// concern to normalize against (a CLI invocation is a trusted local
// operator, not an untrusted network caller — the same reasoning
// internal/delivery/cli/adapterbuild's own doc comment gives for skipping
// HTTP's WriteResourceHidden funnel), so this is returned to the caller
// distinguishably from ports.ErrPersistenceNotFound rather than folded into
// one normalized response.
var ErrProjectNotAuthorized = errors.New("cli/events: project is not authorized for streaming")

// ErrResyncRequired is returned by RunWatch when --from-cursor is old
// enough that more than Dependencies.RetentionScanLimit events are already
// pending — see doc.go's own "Resync / retention policy" section.
var ErrResyncRequired = errors.New("cli/events: --from-cursor is outside this stream's retention window; re-fetch a fresh cursor (e.g. from `aw projection status`) and reconnect")

// probeProjectEvents reloads projectID's own authoritative Project and
// scans up to limit journal rows past afterCursor, both inside the SAME
// read-only transaction — used identically at start and on every
// steady-state poll tick (RunWatch, below): the one place this package
// ever touches ports.Tx directly, so authorization and the journal scan it
// gates can never observe two different (Project, cursor-window) snapshots
// of the database. A deliberate, independently written ~15-line mirror of
// internal/delivery/httpapi/eventstream/errors.go's own probeProject (read
// there first, per this task's own instruction) — that function is
// unexported and architecturally the wrong package for a CLI leaf to call
// into anyway (doc.go's own "independent reimplementation" section).
func probeProjectEvents(ctx context.Context, uow ports.UnitOfWork, projectID string, afterCursor uint64, limit int) ([]ports.JournalEvent, error) {
	var events []ports.JournalEvent
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		p, err := tx.Catalog().GetProject(ctx, projectID)
		if err != nil {
			return err
		}
		if p.Status != project.ProjectActive {
			return ErrProjectNotAuthorized
		}
		scanned, err := tx.Events().ScanJournal(ctx, afterCursor, limit)
		if err != nil {
			return err
		}
		events = scanned
		return nil
	})
	return events, err
}

// emitBatch advances cursor past EVERY event in events (including a
// foreign-project one — mirrors
// internal/delivery/httpapi/eventstream/stream.go's own process closure
// exactly: "advance the local scan cursor past every row... but only ever
// enqueue a summary for one that belongs to projectID"), writing one
// redacted NDJSON ProjectEventSummary line per event that actually belongs
// to projectID. Returns the advanced cursor so the caller's own next probe
// starts exactly where this batch left off, even when every event in it
// belonged to some other project (never re-scanning the same foreign rows
// forever).
func emitBatch(stdout io.Writer, matcher redact.Matcher, projectID string, cursor uint64, events []ports.JournalEvent) (uint64, error) {
	for _, event := range events {
		cursor = event.JournalPosition
		if event.ProjectID != projectID {
			continue
		}
		summary := ProjectEventSummary{
			JournalPosition: event.JournalPosition,
			EventType:       matcher.String(event.EventType),
			SchemaVersion:   event.SchemaVersion,
		}
		if err := cli.EncodeNDJSONLine(stdout, summary); err != nil {
			return cursor, err
		}
	}
	return cursor, nil
}

// RunWatch implements
// `aw events watch --project-id <id> [--from-cursor <n>]
// [--poll-interval <duration>]` — see doc.go for the full design (no
// bounded-channel/disconnect policy, no heartbeat, re-authorization on
// every poll tick, --from-cursor's own default/resync policy).
//
// This runs entirely in the CALLER's own goroutine — no goroutine is ever
// spawned by this function — so ctx cancellation is observed directly by
// the same select loop that drives polling, and there is nothing for a
// caller to ever leak: RunWatch simply returns nil once ctx is done, having
// written nothing more after that point (this task's own "shutdown...
// cleanly, no partial/corrupted final line" Verify bullet — the last thing
// this function ever does after ctx fires is return, never a trailing
// partial write).
func RunWatch(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("events watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	fromCursor := fs.Uint64("from-cursor", 0, "resume from this JournalPosition (exclusive); omit to replay the full journal then follow live (see this package's own doc comment for why this differs from the HTTP stream's own required cursor)")
	pollInterval := fs.Duration("poll-interval", 0, "how often to poll for new events and re-check authorization (0 uses this command's own default)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw events watch --project-id <projectId> [--from-cursor <n>] [--poll-interval <duration>]")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}

	interval := deps.pollInterval()
	if *pollInterval > 0 {
		interval = *pollInterval
	}
	retentionLimit := deps.retentionScanLimit()
	cursor := *fromCursor

	initial, err := probeProjectEvents(ctx, deps.UoW, *projectID, cursor, retentionLimit+1)
	if err != nil {
		return err
	}
	if len(initial) > retentionLimit {
		return ErrResyncRequired
	}
	cursor, err = emitBatch(stdout, deps.Matcher, *projectID, cursor, initial)
	if err != nil {
		return fmt.Errorf("cli/events: write NDJSON line: %w", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			events, err := probeProjectEvents(ctx, deps.UoW, *projectID, cursor, deps.pollBatchLimit())
			if err != nil {
				return err
			}
			cursor, err = emitBatch(stdout, deps.Matcher, *projectID, cursor, events)
			if err != nil {
				return fmt.Errorf("cli/events: write NDJSON line: %w", err)
			}
		}
	}
}
