package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
)

// ReadinessCheck is one named dependency /health/ready must prove is
// actually usable right now — not merely "was usable once at boot". It is
// called fresh on every /health/ready request (V6-01's own "ready gọi
// installation-scoped health query"), so a dependency that later degrades
// (e.g. the database connection drops) is reflected immediately, not just
// during the startup window.
type ReadinessCheck func(ctx context.Context) error

// ReadinessChecker aggregates every named ReadinessCheck the composition
// root registers (config, UnitOfWork, artifact root, route composition —
// V6-01's own "Thực hiện" line) and reports typed failure for whichever
// ones do not currently pass. Registering checks is not safe for
// concurrent use with checking; the composition root registers every
// check once at startup, before the server begins accepting connections.
type ReadinessChecker struct {
	mu     sync.RWMutex
	checks map[string]ReadinessCheck
	order  []string
}

// NewReadinessChecker returns an empty ReadinessChecker.
func NewReadinessChecker() *ReadinessChecker {
	return &ReadinessChecker{checks: map[string]ReadinessCheck{}}
}

// Register adds a named check. Registering the same name twice panics —
// the same "catch a programming error at boot" discipline this package's
// RouteRegistry.Register and internal/app/eventschema.Registry.Register
// already use.
func (c *ReadinessChecker) Register(name string, check ReadinessCheck) {
	if name == "" {
		panic("httpapi: ReadinessCheck name is required")
	}
	if check == nil {
		panic("httpapi: ReadinessCheck func is required (" + name + ")")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.checks[name]; exists {
		panic("httpapi: readiness check " + name + " already registered")
	}
	c.checks[name] = check
	c.order = append(c.order, name)
}

// readinessFailure is one failed check's typed reason, in the /health/ready
// response body.
type readinessFailure struct {
	Check  string `json:"check"`
	Reason string `json:"reason"`
}

// Evaluate runs every registered check and returns the failures, if any,
// sorted by check name for a stable response body.
func (c *ReadinessChecker) Evaluate(ctx context.Context) []readinessFailure {
	c.mu.RLock()
	names := append([]string(nil), c.order...)
	checks := make(map[string]ReadinessCheck, len(c.checks))
	for k, v := range c.checks {
		checks[k] = v
	}
	c.mu.RUnlock()

	var failures []readinessFailure
	for _, name := range names {
		if err := checks[name](ctx); err != nil {
			failures = append(failures, readinessFailure{Check: name, Reason: err.Error()})
		}
	}
	sort.Slice(failures, func(i, j int) bool { return failures[i].Check < failures[j].Check })
	return failures
}

// LiveHandler proves only that this process's own HTTP event loop is still
// servicing requests (V6-01's own "live chỉ chứng minh process/event loop
// còn phục vụ") — it never inspects a dependency, so it must keep
// responding even while every ReadinessCheck fails.
func LiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
	}
}

// ReadyHandler returns 200 only once every registered check passes, and a
// typed 503 body naming exactly which checks still fail otherwise (V6-01's
// own Verify line: "ready fail typed trước readiness và pass sau
// startup").
func (c *ReadinessChecker) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		failures := c.Evaluate(r.Context())
		if len(failures) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "not_ready",
				"checks": failures,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
