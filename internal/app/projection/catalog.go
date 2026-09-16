package projection

import (
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// Outcome is V6-08's own two-value classification every registered
// (EventType, SchemaVersion) key must resolve to — "no implicit default"
// (V6-08's own Thực hiện bullet).
type Outcome int

const (
	// Ignore means this event has no effect on ProjectionName's own row
	// set — see each Classification's own Reason for WHY (never a bare
	// "not needed" with no explanation).
	Ignore Outcome = iota
	// Apply means this event has a real, deterministic Reducer that
	// updates one WorkItemCardRow.
	Apply
)

// Reducer is a pure function: given the target row's own prior state (the
// zero WorkItemCardRow{} — Exists() == false — when no row exists yet for
// this EntityKey) and one event's raw payload JSON, returns the row's new
// state. It performs no I/O and reads no wall clock; the caller (V6-08A's
// own live scanner, not yet built) is solely responsible for atomicity,
// generation/lease fencing, duplicate suppression (LastAppliedJournalPosition)
// and persisting the result via ports.ProjectionRepository.UpsertProjectionRow.
type Reducer func(prior WorkItemCardRow, payloadJSON string) (WorkItemCardRow, error)

// EntityKeyFunc resolves which WorkItemCardRow (by its own EntityKey, the
// WorkItemID) one event's payload targets. ok=false means the payload alone
// does not name a WorkItemID directly — see each such Classification's own
// EntityKeyNote for the resolution strategy a caller (V6-08A) must use
// instead (always expressible as a query already exposed by
// ports.ProjectionRepository, e.g. ListProjectionRows plus a scan predicate
// — never a capability this schema doesn't already provide, matching this
// task's own "Hoàn thành khi: ... without new design choice").
type EntityKeyFunc func(payloadJSON string) (entityKey string, ok bool, err error)

// Classification is one (EventType, SchemaVersion) key's own frozen
// disposition for ProjectionName.
type Classification struct {
	Outcome Outcome
	// HandlerVersion is meaningful only when Outcome == Apply — a future
	// change to a Reducer's own logic (not just a new SchemaVersion of the
	// SAME event) bumps this, the same "handlerVersion" concept V6-08's
	// own spec text names directly ("APPLY(handlerVersion)").
	HandlerVersion int
	Reducer        Reducer
	EntityKeyOf    EntityKeyFunc
	// EntityKeyNote documents EntityKeyOf's own resolution strategy for an
	// event whose EntityKeyFunc can return ok=false — empty when
	// EntityKeyOf always succeeds. Meaningful only when Outcome == Apply.
	EntityKeyNote string
	// FallbackMatch is set exactly when EntityKeyOf can return ok=false
	// (see EntityKeyNote) — it decodes payloadJSON and returns a predicate
	// a caller (V6-08A) applies over ListProjectionRows(generation)'s own
	// result set to find the ONE row this event actually targets, per
	// EntityKeyNote's own documented strategy (e.g. "FamilyID matches and
	// IsRoot" or "ActiveRunID matches"). Zero matches is the "missing
	// referenced authority" gap (V6-08's own exhaustive gap list); more
	// than one match is an internal consistency violation (this
	// projection's own invariant — e.g. two rows both IsRoot for the same
	// FamilyID — broken); both are the caller's own poison case to record,
	// never this function's to decide.
	FallbackMatch FallbackMatchFunc
	// Reason explains WHY this key is Ignore — always populated when
	// Outcome == Ignore, always empty when Outcome == Apply.
	Reason string
}

// FallbackMatchFunc decodes payloadJSON and returns a predicate for
// locating the one WorkItemCardRow an EntityKeyOf ok=false event actually
// targets — see Classification.FallbackMatch's own doc comment.
type FallbackMatchFunc func(payloadJSON string) (predicate func(WorkItemCardRow) bool, err error)

// Catalog is the full, frozen classification of every (EventType,
// SchemaVersion) key this codebase's real eventschema registries currently
// register. Build via NewCatalog — never construct one by hand outside
// this package, so BuildCatalog's own exhaustiveness (verified by
// TestCatalog_ClassifiesEveryRegisteredEventKey, catalog_test.go) stays the
// single source of truth.
type Catalog struct {
	classifications map[eventschema.EventKey]Classification
}

// Classify returns key's own Classification, or (Classification{}, false)
// if key is not in this Catalog at all — distinct from a real Ignore
// classification (Outcome == Ignore, ok == true): an unclassified key is a
// gap this package's own inventory-totality test never lets ship, so a
// caller seeing ok == false has found a real bug, not an expected Ignore.
func (c *Catalog) Classify(key eventschema.EventKey) (Classification, bool) {
	classification, ok := c.classifications[key]
	return classification, ok
}

// Keys returns every (EventType, SchemaVersion) key this Catalog
// classifies, order unspecified — mirrors eventschema.Registry.Keys' own
// read-only introspection shape.
func (c *Catalog) Keys() []eventschema.EventKey {
	keys := make([]eventschema.EventKey, 0, len(c.classifications))
	for key := range c.classifications {
		keys = append(keys, key)
	}
	return keys
}

func (c *Catalog) classify(eventType string, schemaVersion int, classification Classification) {
	key := eventschema.EventKey{EventType: eventType, SchemaVersion: schemaVersion}
	if _, exists := c.classifications[key]; exists {
		panic(fmt.Sprintf("projection: duplicate classification for %s v%d", eventType, schemaVersion))
	}
	c.classifications[key] = classification
}

func (c *Catalog) apply(eventType string, schemaVersion, handlerVersion int, reducer Reducer, entityKeyOf EntityKeyFunc, entityKeyNote string) {
	c.classify(eventType, schemaVersion, Classification{
		Outcome: Apply, HandlerVersion: handlerVersion, Reducer: reducer,
		EntityKeyOf: entityKeyOf, EntityKeyNote: entityKeyNote,
	})
}

// applyWithFallback is apply plus a FallbackMatch — for an Apply event
// whose EntityKeyOf can return ok=false (see EntityKeyNote/FallbackMatch's
// own doc comments).
func (c *Catalog) applyWithFallback(eventType string, schemaVersion, handlerVersion int, reducer Reducer, entityKeyOf EntityKeyFunc, entityKeyNote string, fallbackMatch FallbackMatchFunc) {
	c.classify(eventType, schemaVersion, Classification{
		Outcome: Apply, HandlerVersion: handlerVersion, Reducer: reducer,
		EntityKeyOf: entityKeyOf, EntityKeyNote: entityKeyNote, FallbackMatch: fallbackMatch,
	})
}

func (c *Catalog) ignore(eventType string, schemaVersion int, reason string) {
	c.classify(eventType, schemaVersion, Classification{Outcome: Ignore, Reason: reason})
}

// entityKeyFromField returns an EntityKeyFunc that decodes payloadJSON into
// a fresh T and reads its WorkItemID via get — the common case (16 of this
// Catalog's 20 Apply entries carry a WorkItemID directly), factored into
// one generic helper so every direct-WorkItemID Reducer's own EntityKeyOf
// wiring is a one-line call, not a hand-written closure repeated 16 times.
func entityKeyFromField[T any](decode func(payloadJSON string) (T, error), get func(T) string) EntityKeyFunc {
	return func(payloadJSON string) (string, bool, error) {
		decoded, err := decode(payloadJSON)
		if err != nil {
			return "", false, err
		}
		key := get(decoded)
		if key == "" {
			return "", false, nil
		}
		return key, true, nil
	}
}

// NewCatalog builds and returns the full, frozen Catalog — see this file's
// own doc comment on Catalog and catalog_registrations.go for the actual
// per-event Apply/Ignore wiring (kept in a separate file since this one is
// the package's own small, stable mechanism, while the registrations file
// grows with every future event this codebase adds).
func NewCatalog() *Catalog {
	c := &Catalog{classifications: map[eventschema.EventKey]Classification{}}
	registerClassifications(c)
	return c
}
