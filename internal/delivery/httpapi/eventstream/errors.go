package eventstream

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// errProjectNotAuthorized is probeProject's own sentinel for "the Project
// exists but is not project.ProjectActive" — see eventstream.go's own doc
// comment ("Authorization") for why this folds into the identical
// leakage-normalized response as ports.ErrPersistenceNotFound rather than a
// distinguishable 403.
var errProjectNotAuthorized = errors.New("eventstream: project is not authorized for streaming")

// probeProject reloads projectID's own authoritative Project and scans up
// to limit journal rows past afterCursor, both inside the SAME read-only
// transaction — used identically at connect time (the retention probe,
// handler.go) and on every steady-state poll tick (stream.go): the one
// place this package ever touches ports.Tx directly, so authorization and
// the journal scan it gates can never observe two different
// (Project, cursor-window) snapshots of the database.
func probeProject(ctx context.Context, uow ports.UnitOfWork, projectID string, afterCursor uint64, limit int) ([]ports.JournalEvent, error) {
	var events []ports.JournalEvent
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		p, err := tx.Catalog().GetProject(ctx, projectID)
		if err != nil {
			return err
		}
		if p.Status != project.ProjectActive {
			return errProjectNotAuthorized
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

// writeStreamError maps probeProject's own error vocabulary onto the
// canonical httpapi envelope — mirrors internal/delivery/httpapi/evidence
// and internal/delivery/httpapi/catalog's own identical
// ErrPersistenceNotFound-folds-into-WriteResourceHidden discipline, plus
// this package's own errProjectNotAuthorized folding into the exact same
// response (never a distinguishable 403 — see eventstream.go's own doc
// comment).
func writeStreamError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, errProjectNotAuthorized) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeValidationError writes a single field-level 400 — this package's
// own pre-dispatch path/query-parameter validation, mirroring
// internal/delivery/httpapi/evidence's own identical helper.
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}

// resyncReasonRetentionExceeded is this package's own RESYNC_REQUIRED
// reason text — see eventstream.go's own doc comment ("Retention policy").
const resyncReasonRetentionExceeded = "cursor is older than this stream's retention window; re-fetch current authoritative/projected state and reconnect with a fresh observed cursor"

// writeRetentionResyncRequired writes the typed full-resync response this
// task's own "too-old cursor returns typed full-resync" line requires —
// deliberately NOT httpapi.WriteResyncRequired (cursor.go): that helper's
// fixed message text ("restart pagination without a cursor") is
// httpapi.CursorCodec paging-specific wording this package never uses (see
// eventstream.go's own "Cursor design" doc comment for why); this package
// reuses only the shared wire vocabulary (httpapi.ErrorCodeResyncRequired,
// 409) with its own message.
func writeRetentionResyncRequired(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeResyncRequired,
		"cursor is no longer within this stream's retention window; reconnect with a fresh observed cursor",
		[]httpapi.ErrorDetail{{Field: "cursor", Message: resyncReasonRetentionExceeded}})
}

// parseCursor parses the required `cursor` query parameter as a base-10
// uint64 JournalPosition — see eventstream.go's own "Cursor design" doc
// comment for why it is required and why it is a plain integer rather than
// an opaque httpapi.CursorCodec token.
func parseCursor(raw string) (uint64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
