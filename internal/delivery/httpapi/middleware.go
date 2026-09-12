package httpapi

import (
	"context"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
)

// CorrelationIDHeader is the header a caller may set to propagate its own
// correlation ID through this server; CorrelationID mints a fresh one
// (via ids.NewID()) whenever a caller omits it, so every request/response
// pair, and every log line the request causes, carries one either way.
const CorrelationIDHeader = "X-Correlation-Id"

type correlationIDContextKey struct{}

// CorrelationIDFromContext returns the correlation ID CorrelationID
// attached to ctx, or "" if the middleware never ran.
func CorrelationIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(correlationIDContextKey{}).(string)
	return id
}

// CorrelationID reads CorrelationIDHeader off the inbound request, or
// mints a fresh one via ids, then makes it available to downstream
// handlers (via context, retrievable with CorrelationIDFromContext) and to
// the caller (echoed back on the response header) — V6-01's own
// "correlation" scope line.
func CorrelationID(ids idsource.Source) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(CorrelationIDHeader)
			if id == "" {
				id = ids.NewID()
			}
			w.Header().Set(CorrelationIDHeader, id)
			ctx := context.WithValue(r.Context(), correlationIDContextKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Recover catches a panic anywhere downstream, logs it (with the
// request's own correlation ID, if CorrelationID middleware already ran)
// and responds with a typed 500 instead of crashing the whole server —
// V6-01's own "panic recovery" scope line. A panic after the handler
// already wrote a response status is still logged, even though the
// response itself cannot change at that point (net/http's own
// ResponseWriter contract).
func Recover(logger *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					if logger != nil {
						_ = logger.Error(
							logging.Identity{CorrelationID: CorrelationIDFromContext(r.Context())},
							apperror.CodeInternal,
							"http handler panic",
							map[string]any{"panic": recovered, "path": r.URL.Path, "method": r.Method},
						)
					}
					writeJSON(w, http.StatusInternalServerError, map[string]string{
						"error": "internal_error",
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBytes wraps r's Body so reading past limit fails with a typed error
// instead of allowing an unbounded read — V6-01's own "strict JSON/body
// limit" scope line. Call this before any handler reads the body.
func MaxBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// Chain applies middlewares to handler in the given order — the first
// middleware listed is outermost (runs first on the way in, last on the
// way out).
func Chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
