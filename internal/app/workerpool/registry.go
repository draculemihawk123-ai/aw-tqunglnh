package workerpool

import (
	"context"
	"fmt"
	"sync"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Handler processes one claimed durable job. Handle must be safe to call
// concurrently — Pool may run several handlers, of the same or different
// kinds, at once, up to its configured concurrency.
type Handler interface {
	Handle(ctx context.Context, job ports.DurableJob) error
}

// HandlerFunc adapts a plain function to Handler.
type HandlerFunc func(ctx context.Context, job ports.DurableJob) error

func (f HandlerFunc) Handle(ctx context.Context, job ports.DurableJob) error { return f(ctx, job) }

// Registry maps a DurableJob.Kind to the Handler that processes it
// (V1-10's own "handler registry theo kind"). Register is meant to be
// called at startup, before Pool.Run: a duplicate registration for the
// same kind panics immediately, since that is a programming error to
// catch at boot, never a runtime condition a caller branches on —
// mirroring internal/app/eventschema.Registry's Register (V1-07A).
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{handlers: map[string]Handler{}}
}

// Register adds handler for kind. Registering the same kind twice panics.
func (r *Registry) Register(kind string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[kind]; exists {
		panic(fmt.Sprintf("workerpool: handler for kind %q already registered", kind))
	}
	r.handlers[kind] = handler
}

// Lookup returns the Handler registered for kind, if any.
func (r *Registry) Lookup(kind string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.handlers[kind]
	return handler, ok
}
