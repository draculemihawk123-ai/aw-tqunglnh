// Package fake provides in-memory test doubles for internal/app/ports
// interfaces, so an application-layer test never has to import
// internal/adapters/sqlite (docs/design/03-v1-alpha-foundation.md V1-05's
// "app service có thể test không SQLite").
package fake

import (
	"context"
	"errors"
	"sync"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ErrNestedTransaction is returned when WithSerializedWrite or
// WithReadOnly is called again from inside an already-open transaction —
// nesting a transaction inside another is exactly what V1-05 forbids
// ("không nested transaction... trong UoW"), and a fake that silently
// allowed it would let a handler test pass against behavior a real
// adapter must reject.
var ErrNestedTransaction = errors.New("fake: nested UnitOfWork call (WithSerializedWrite/WithReadOnly called from inside an already-open transaction)")

// UnitOfWork is an in-memory ports.UnitOfWork. Each call works on a clone
// of the last committed Snapshot; the clone only replaces Snapshot if fn
// returns nil, giving the same commit/rollback semantics a real adapter
// has without needing separate undo logic.
type UnitOfWork struct {
	mu       sync.Mutex
	inTx     bool
	Snapshot Tx
}

// New returns a ready-to-use fake UnitOfWork with an empty Snapshot.
func New() *UnitOfWork {
	return &UnitOfWork{Snapshot: newTx()}
}

var _ ports.UnitOfWork = (*UnitOfWork)(nil)

func (u *UnitOfWork) WithSerializedWrite(_ context.Context, fn func(ports.Tx) error) error {
	return u.run(fn, true)
}

func (u *UnitOfWork) WithReadOnly(_ context.Context, fn func(ports.Tx) error) error {
	return u.run(fn, false)
}

func (u *UnitOfWork) run(fn func(ports.Tx) error, persistOnSuccess bool) error {
	u.mu.Lock()
	if u.inTx {
		u.mu.Unlock()
		return ErrNestedTransaction
	}
	u.inTx = true
	working := u.Snapshot
	u.mu.Unlock()

	err := fn(working)

	u.mu.Lock()
	u.inTx = false
	if err == nil && persistOnSuccess {
		u.Snapshot = working
	}
	u.mu.Unlock()
	return err
}

// Tx is the fake's in-memory ports.Tx. Every repository accessor
// currently returns a zero-method placeholder value (mirroring
// ports.Tx's own placeholder interfaces — see its doc comment); a concern
// gains real, stateful fields here in the same task that gives
// ports.<Concern>Repository real methods.
type Tx struct {
	catalog     CatalogRepository
	work        WorkRepository
	definitions DefinitionsRepository
	runtime     RuntimeRepository
	jobs        JobsRepository
	events      EventsRepository
	receipts    ReceiptsRepository
}

func newTx() Tx { return Tx{} }

var _ ports.Tx = Tx{}

func (t Tx) Catalog() ports.CatalogRepository         { return t.catalog }
func (t Tx) Work() ports.WorkRepository               { return t.work }
func (t Tx) Definitions() ports.DefinitionsRepository { return t.definitions }
func (t Tx) Runtime() ports.RuntimeRepository         { return t.runtime }
func (t Tx) Jobs() ports.JobsRepository               { return t.jobs }
func (t Tx) Events() ports.EventsRepository           { return t.events }
func (t Tx) Receipts() ports.ReceiptsRepository       { return t.receipts }

type CatalogRepository struct{}
type WorkRepository struct{}
type DefinitionsRepository struct{}
type RuntimeRepository struct{}
type JobsRepository struct{}
type EventsRepository struct{}
type ReceiptsRepository struct{}

// QueryStore is an in-memory ports.QueryStore that is always reachable.
type QueryStore struct {
	// Unreachable, when true, makes Ping fail — for a handler test that
	// needs to exercise the "query store is down" path without a real
	// database to actually take offline.
	Unreachable bool
}

var _ ports.QueryStore = (*QueryStore)(nil)

func (q *QueryStore) Ping(context.Context) error {
	if q.Unreachable {
		return errors.New("fake: query store unreachable")
	}
	return nil
}
