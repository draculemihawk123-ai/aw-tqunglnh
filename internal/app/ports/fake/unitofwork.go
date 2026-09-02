// Package fake provides in-memory test doubles for internal/app/ports
// interfaces, so an application-layer test never has to import
// internal/adapters/sqlite (docs/design/03-v1-alpha-foundation.md V1-05's
// "app service có thể test không SQLite").
package fake

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
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
	working := u.Snapshot.clone()
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
// ports.Tx's own placeholder interfaces — see its doc comment), except
// Events and Receipts, which V1-06 gives real in-memory behavior; a
// concern gains real, stateful fields here in the same task that gives
// ports.<Concern>Repository real methods. Events/Receipts are held by
// pointer so a handler's writes inside fn are visible to that same fn
// call; clone() deep-copies both before each attempt so a failed or
// read-only attempt never mutates the committed Snapshot.
type Tx struct {
	catalog       CatalogRepository
	work          WorkRepository
	definitions   *DefinitionsRepository
	runtime       RuntimeRepository
	jobs          JobsRepository
	events        *EventsRepository
	receipts      *ReceiptsRepository
	adapterBuilds *AdapterBuildRepository
}

func newTx() Tx {
	return Tx{
		events:        &EventsRepository{},
		receipts:      &ReceiptsRepository{},
		adapterBuilds: &AdapterBuildRepository{},
		definitions:   &DefinitionsRepository{},
	}
}

func (t Tx) clone() Tx {
	clone := t
	clone.events = t.events.clone()
	clone.receipts = t.receipts.clone()
	clone.adapterBuilds = t.adapterBuilds.clone()
	clone.definitions = t.definitions.clone()
	return clone
}

var _ ports.Tx = Tx{}

func (t Tx) Catalog() ports.CatalogRepository            { return t.catalog }
func (t Tx) Work() ports.WorkRepository                  { return t.work }
func (t Tx) Definitions() ports.DefinitionsRepository    { return t.definitions }
func (t Tx) Runtime() ports.RuntimeRepository            { return t.runtime }
func (t Tx) Jobs() ports.JobsRepository                  { return t.jobs }
func (t Tx) Events() ports.EventsRepository              { return t.events }
func (t Tx) Receipts() ports.ReceiptsRepository          { return t.receipts }
func (t Tx) AdapterBuilds() ports.AdapterBuildRepository { return t.adapterBuilds }

type CatalogRepository struct{}
type WorkRepository struct{}
type RuntimeRepository struct{}
type JobsRepository struct{}

// EventsRepository is an in-memory ports.EventsRepository: Append rejects
// a duplicate (aggregate_type, aggregate_id, sequence) the same way the
// sqlite adapter's UNIQUE constraint does, so a handler test exercises
// the same conflict behavior against either implementation.
type EventsRepository struct {
	items []ports.DomainEvent
}

var _ ports.EventsRepository = (*EventsRepository)(nil)

func (e *EventsRepository) clone() *EventsRepository {
	items := make([]ports.DomainEvent, len(e.items))
	copy(items, e.items)
	return &EventsRepository{items: items}
}

func (e *EventsRepository) Append(_ context.Context, event ports.DomainEvent) error {
	for _, existing := range e.items {
		if existing.AggregateType == event.AggregateType &&
			existing.AggregateID == event.AggregateID &&
			existing.Sequence == event.Sequence {
			return fmt.Errorf("fake: duplicate domain event (aggregate_type=%q aggregate_id=%q sequence=%d)",
				event.AggregateType, event.AggregateID, event.Sequence)
		}
	}
	e.items = append(e.items, event)
	return nil
}

// Items returns a copy of every event appended so far, newest last — for
// test assertions.
func (e *EventsRepository) Items() []ports.DomainEvent {
	items := make([]ports.DomainEvent, len(e.items))
	copy(items, e.items)
	return items
}

// ReceiptsRepository is an in-memory ports.ReceiptsRepository, keyed the
// same way the sqlite adapter's command_receipts primary key is (actor,
// scope key, idempotency key, command type).
type ReceiptsRepository struct {
	records map[receiptKey]ports.Receipt
}

type receiptKey struct {
	actor          string
	scopeKey       string
	idempotencyKey string
	commandType    string
}

var _ ports.ReceiptsRepository = (*ReceiptsRepository)(nil)

func (r *ReceiptsRepository) clone() *ReceiptsRepository {
	records := make(map[receiptKey]ports.Receipt, len(r.records))
	for k, v := range r.records {
		records[k] = v
	}
	return &ReceiptsRepository{records: records}
}

func (r *ReceiptsRepository) key(actor string, scope ports.CommandScope, idempotencyKey, commandType string) receiptKey {
	return receiptKey{actor: actor, scopeKey: scope.Key(), idempotencyKey: idempotencyKey, commandType: commandType}
}

func (r *ReceiptsRepository) Load(_ context.Context, actor string, scope ports.CommandScope, idempotencyKey, commandType string) (ports.Receipt, bool, error) {
	receipt, ok := r.records[r.key(actor, scope, idempotencyKey, commandType)]
	return receipt, ok, nil
}

func (r *ReceiptsRepository) Record(_ context.Context, receipt ports.Receipt) error {
	key := r.key(receipt.Actor, receipt.Scope, receipt.IdempotencyKey, receipt.CommandType)
	if existing, ok := r.records[key]; ok {
		if existing.RequestHash != receipt.RequestHash {
			return ports.ErrReceiptConflict
		}
		return nil
	}
	if r.records == nil {
		r.records = map[receiptKey]ports.Receipt{}
	}
	r.records[key] = receipt
	return nil
}

// DefinitionsRepository is an in-memory ports.DefinitionsRepository —
// V2-09 gives this concern real behavior (LoadVersion), unlike its
// still-empty siblings above. Unlike Events/Receipts/AdapterBuilds, no
// port-level write method exists to populate it (LoadVersion is
// read-only by design — publishing a Version is a different concern,
// reserved for V2-10), so a test seeds known Version data directly via
// Seed before exercising whatever reads it.
type DefinitionsRepository struct {
	versions map[string]definition.VersionFields
}

var _ ports.DefinitionsRepository = (*DefinitionsRepository)(nil)

func (d *DefinitionsRepository) clone() *DefinitionsRepository {
	versions := make(map[string]definition.VersionFields, len(d.versions))
	for k, v := range d.versions {
		versions[k] = v
	}
	return &DefinitionsRepository{versions: versions}
}

func (d *DefinitionsRepository) LoadVersion(_ context.Context, versionID string) (definition.VersionFields, error) {
	fields, ok := d.versions[versionID]
	if !ok {
		return definition.VersionFields{}, ports.ErrDefinitionVersionNotFound
	}
	return fields, nil
}

// Seed registers fields as resolvable by its own ID() — test setup
// standing in for a real publish that already happened before the code
// under test ever runs.
func (d *DefinitionsRepository) Seed(fields definition.VersionFields) {
	if d.versions == nil {
		d.versions = map[string]definition.VersionFields{}
	}
	d.versions[fields.ID()] = fields
}

// AdapterBuildRepository is an in-memory ports.AdapterBuildRepository —
// V2-07A gives this concern real behavior from the start (unlike
// Catalog/Work/Definitions/Runtime/Jobs above), so an application-layer
// test of ProbeAdapterBuild/RegisterAdapterBuild never needs sqlite.
type AdapterBuildRepository struct {
	signingKey []byte
	builds     map[string]adapterbuild.Build
}

var _ ports.AdapterBuildRepository = (*AdapterBuildRepository)(nil)

func (a *AdapterBuildRepository) clone() *AdapterBuildRepository {
	builds := make(map[string]adapterbuild.Build, len(a.builds))
	for k, v := range a.builds {
		builds[k] = v
	}
	key := append([]byte(nil), a.signingKey...)
	return &AdapterBuildRepository{signingKey: key, builds: builds}
}

func (a *AdapterBuildRepository) LoadOrCreateSigningKey(context.Context) ([]byte, error) {
	if len(a.signingKey) == 0 {
		a.signingKey = randomKey()
	}
	return a.signingKey, nil
}

func (a *AdapterBuildRepository) LoadSigningKey(context.Context) ([]byte, error) {
	if len(a.signingKey) == 0 {
		return nil, ports.ErrNoSigningKey
	}
	return a.signingKey, nil
}

func (a *AdapterBuildRepository) RotateSigningKey(context.Context) ([]byte, error) {
	a.signingKey = randomKey()
	return a.signingKey, nil
}

func (a *AdapterBuildRepository) InsertIfAbsent(_ context.Context, build adapterbuild.Build) (adapterbuild.Build, bool, error) {
	if existing, ok := a.builds[build.ID()]; ok {
		return existing, true, nil
	}
	if a.builds == nil {
		a.builds = map[string]adapterbuild.Build{}
	}
	a.builds[build.ID()] = build
	return build, false, nil
}

func (a *AdapterBuildRepository) Get(_ context.Context, id string) (adapterbuild.Build, error) {
	build, ok := a.builds[id]
	if !ok {
		return adapterbuild.Build{}, ports.ErrAdapterBuildNotFound
	}
	return build, nil
}

func (a *AdapterBuildRepository) List(context.Context) ([]adapterbuild.Build, error) {
	builds := make([]adapterbuild.Build, 0, len(a.builds))
	for _, build := range a.builds {
		builds = append(builds, build)
	}
	sort.Slice(builds, func(i, j int) bool { return builds[i].RegisteredAt().Before(builds[j].RegisteredAt()) })
	return builds, nil
}

func randomKey() []byte {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return key
}

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
