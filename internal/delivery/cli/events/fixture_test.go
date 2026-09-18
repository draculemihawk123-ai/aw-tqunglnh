package events_test

// This file mirrors internal/delivery/cli/workitem's and
// internal/delivery/httpapi/eventstream's own fixture helpers (newTestDeps/
// mustCreateProject/appendTestEvents, and stream_internal_test.go's own
// archivableCatalog/archivableTx/archivableUOW "flip a Project's own
// authorized-for-streaming status mid-test" wrapper) — duplicated rather
// than imported, both because Go test helpers in a _test.go file are not
// exported across packages, and because internal/delivery/httpapi/eventstream's
// own version lives in an internal (white-box) _test.go file this package
// could not import even if test helpers were exported.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	clievents "github.com/taQuangLing/agent-workflow/internal/delivery/cli/events"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// newTestDeps builds a fresh in-memory clievents.Dependencies over uow —
// short PollInterval/PollBatchLimit so a test never has to wait long for a
// poll tick; RetentionScanLimit left at the caller-supplied value (0 uses
// the real production default, 1000, which most tests want to never
// actually hit).
func newTestDeps(uow ports.UnitOfWork) clievents.Dependencies {
	return clievents.Dependencies{
		UoW: uow, Matcher: redact.NewMatcher(), PollInterval: 5 * time.Millisecond,
	}
}

func mustCreateProject(t *testing.T, uow ports.UnitOfWork, id string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

func newFakeProject(t *testing.T, id string) *fake.UnitOfWork {
	t.Helper()
	uow := fake.New()
	mustCreateProject(t, uow, id)
	return uow
}

// appendEventSeq is a package-level counter guaranteeing a unique
// AggregateID/event ID across every appendTestEvents call within one test
// binary run — a test that calls appendTestEvents more than once for the
// same projectID (e.g. to prove a --from-cursor restart never duplicates)
// would otherwise collide on fake.EventsRepository's own duplicate-event
// guard (aggregate_type/aggregate_id/sequence), which is keyed independently
// of JournalPosition.
var appendEventSeq atomic.Int64

// appendTestEvents appends n domain events for projectID, EventType
// "Test.Event.<i>" (i restarting at 0 on every call — a distinct literal
// per event so a test can assert EventType round-trips exactly through
// redact.Matcher unchanged for a non-secret value; the real uniqueness
// guarantee is appendEventSeq above, threaded through ID/AggregateID
// instead).
func appendTestEvents(t *testing.T, uow ports.UnitOfWork, projectID string, n int) {
	t.Helper()
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		for i := 0; i < n; i++ {
			seq := appendEventSeq.Add(1)
			if err := tx.Events().Append(context.Background(), ports.DomainEvent{
				ID: fmt.Sprintf("evt-%s-%d", projectID, seq), ProjectID: projectID,
				AggregateType: "TestAggregate", AggregateID: fmt.Sprintf("agg-%s-%d", projectID, seq), Sequence: 1,
				EventType: fmt.Sprintf("Test.Event.%d", i), SchemaVersion: 1, PayloadJSON: "{}", CreatedAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("append %d test events for %s: %v", n, projectID, err)
	}
}

func isUsageError(err error) bool {
	return cli.IsUsageError(err)
}

// --- archivable* : flips one Project's own authorized-for-streaming status
// mid-test, standing in for the "authorization revoked mid-stream"
// capability this codebase has no real production mutator for yet — a
// black-box-package equivalent of
// internal/delivery/httpapi/eventstream/stream_internal_test.go's own
// identically-shaped, identically-purposed helper (that file's own doc
// comment explains the "no ArchiveProject/TransitionProject mutator exists"
// background in full; read there first).

type archivableCatalog struct {
	ports.CatalogRepository
	projectID string
	archived  *atomic.Bool
}

func (c archivableCatalog) GetProject(ctx context.Context, id string) (project.Project, error) {
	p, err := c.CatalogRepository.GetProject(ctx, id)
	if err != nil {
		return p, err
	}
	if id == c.projectID && c.archived.Load() {
		p.Status = project.ProjectArchived
	}
	return p, nil
}

type archivableTx struct {
	ports.Tx
	projectID string
	archived  *atomic.Bool
}

func (t archivableTx) Catalog() ports.CatalogRepository {
	return archivableCatalog{t.Tx.Catalog(), t.projectID, t.archived}
}

type archivableUOW struct {
	inner     ports.UnitOfWork
	projectID string
	archived  *atomic.Bool
}

func (u archivableUOW) WithReadOnly(ctx context.Context, fn func(ports.Tx) error) error {
	return u.inner.WithReadOnly(ctx, func(tx ports.Tx) error { return fn(archivableTx{tx, u.projectID, u.archived}) })
}

func (u archivableUOW) WithSerializedWrite(ctx context.Context, fn func(ports.Tx) error) error {
	return u.inner.WithSerializedWrite(ctx, func(tx ports.Tx) error { return fn(archivableTx{tx, u.projectID, u.archived}) })
}

var _ ports.UnitOfWork = archivableUOW{}
