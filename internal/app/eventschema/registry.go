// Package eventschema is the domain event registry V1-07A requires:
// domain_events.schema_version only means anything once something
// enforces it (docs/design/03-v1-alpha-foundation.md V1-07A's own Mục
// tiêu). A Decoder is registered per (EventType, SchemaVersion); a raw
// stored event is immutable forever (ADR-008/ADR-015), so a breaking
// payload change is expressed as a NEW schema version plus an Upcaster
// from the previous one, never by editing what an existing version's
// Decoder expects. This package deliberately stops at the
// decode/upcast/enforce contract — it does not build a projection (V6's
// job).
package eventschema

import (
	"errors"
	"fmt"
	"sync"
)

// Decoder decodes one registered (EventType, SchemaVersion)'s
// payload_json into a typed value. Decoders must be deterministic and
// side-effect free: the exact same payloadJSON must always decode to an
// equal value, since a V6 projection rebuild's replay determinism
// depends on it.
type Decoder func(payloadJSON string) (any, error)

// Upcaster transforms a decoded value at schema version `from` into the
// shape version `from+1` expects, one step at a time. DecodeLatest chains
// these to bring an old historical event up to a current shape without
// the raw stored event or its original Decoder ever changing.
type Upcaster func(previous any) (any, error)

type schemaKey struct {
	EventType     string
	SchemaVersion int
}

// ErrNotRegistered is returned by Decode/IsRegistered's callers when no
// Decoder is registered for the given (EventType, SchemaVersion) — this
// is V1-07A's own "từ chối emit event chưa đăng ký ngay tại boundary
// append" signal.
var ErrNotRegistered = errors.New("eventschema: event type/schema version is not registered")

// Registry is the (EventType, SchemaVersion) -> Decoder map. Register and
// RegisterUpcaster are meant to be called at package init/startup time
// (mirroring Go's own encoding/gob type-registration idiom): a duplicate
// registration panics immediately, since that is a programming error to
// catch at build/boot time, never a runtime condition a caller branches
// on.
type Registry struct {
	mu        sync.RWMutex
	decoders  map[schemaKey]Decoder
	upcasters map[string]map[int]Upcaster // upcasters[eventType][from] -> from+1
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		decoders:  map[schemaKey]Decoder{},
		upcasters: map[string]map[int]Upcaster{},
	}
}

// Register adds decode for (eventType, schemaVersion). Registering the
// same pair twice panics.
func (r *Registry) Register(eventType string, schemaVersion int, decode Decoder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := schemaKey{eventType, schemaVersion}
	if _, exists := r.decoders[key]; exists {
		panic(fmt.Sprintf("eventschema: %s v%d already registered", eventType, schemaVersion))
	}
	r.decoders[key] = decode
}

// RegisterUpcaster registers how to transform a decoded `from`-version
// value into the `from+1` shape. Registering an upcaster for a version
// that has no registered Decoder, or registering the same (eventType,
// from) pair twice, panics.
func (r *Registry) RegisterUpcaster(eventType string, from int, upcast Upcaster) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.decoders[schemaKey{eventType, from}]; !ok {
		panic(fmt.Sprintf("eventschema: cannot register upcaster for %s v%d: v%d has no registered decoder", eventType, from, from))
	}
	if r.upcasters[eventType] == nil {
		r.upcasters[eventType] = map[int]Upcaster{}
	}
	if _, exists := r.upcasters[eventType][from]; exists {
		panic(fmt.Sprintf("eventschema: upcaster %s v%d->v%d already registered", eventType, from, from+1))
	}
	r.upcasters[eventType][from] = upcast
}

// IsRegistered reports whether a Decoder is registered for (eventType,
// schemaVersion).
func (r *Registry) IsRegistered(eventType string, schemaVersion int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.decoders[schemaKey{eventType, schemaVersion}]
	return ok
}

// Decode decodes payloadJSON using the Decoder registered for
// (eventType, schemaVersion), or returns ErrNotRegistered.
func (r *Registry) Decode(eventType string, schemaVersion int, payloadJSON string) (any, error) {
	r.mu.RLock()
	decode, ok := r.decoders[schemaKey{eventType, schemaVersion}]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s v%d", ErrNotRegistered, eventType, schemaVersion)
	}
	return decode(payloadJSON)
}

// DecodeLatest decodes payloadJSON at schemaVersion, then applies every
// registered upcaster in order up to targetVersion. This is what lets a
// V6 projection rebuild replay an old historical event through today's
// shape without the stored raw event ever changing.
func (r *Registry) DecodeLatest(eventType string, schemaVersion int, payloadJSON string, targetVersion int) (any, error) {
	value, err := r.Decode(eventType, schemaVersion, payloadJSON)
	if err != nil {
		return nil, err
	}
	for v := schemaVersion; v < targetVersion; v++ {
		r.mu.RLock()
		upcast, ok := r.upcasters[eventType][v]
		r.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("eventschema: no upcaster registered for %s v%d -> v%d", eventType, v, v+1)
		}
		value, err = upcast(value)
		if err != nil {
			return nil, fmt.Errorf("eventschema: upcast %s v%d -> v%d: %w", eventType, v, v+1, err)
		}
	}
	return value, nil
}
