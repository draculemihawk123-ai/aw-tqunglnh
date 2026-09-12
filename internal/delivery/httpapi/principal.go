package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// LocalPrincipalSnapshot is ADR-028's own `{Actor, Roles[]}` authentication
// context (docs/architecture/02-architecture-decisions.md ADR-028,
// docs/design/08-v6-api-projections.md V6-01A). A composition root resolves
// exactly one of these from trusted startup config (internal/app/config's
// LocalPrincipal, via LoadLocalPrincipalFile) and binds it into a Server for
// that process's entire lifetime — this type carries no source information
// of its own, on purpose: it is the delivery-layer's own copy of "who is
// making every request on this server", never a value any request is
// allowed to influence.
type LocalPrincipalSnapshot struct {
	Actor string
	Roles []string
}

// validate reports the same non-empty/unique-roles shape
// config.ValidateLocalPrincipal enforces on the config-side type, checked
// again here since NewServer must fail closed even if some future caller
// constructs a Config by hand instead of going through the config package.
func (p LocalPrincipalSnapshot) validate() error {
	if strings.TrimSpace(p.Actor) == "" {
		return errors.New("httpapi: Config.Principal.Actor is required")
	}
	if len(p.Roles) == 0 {
		return errors.New("httpapi: Config.Principal.Roles is required")
	}
	seen := make(map[string]struct{}, len(p.Roles))
	for _, role := range p.Roles {
		if strings.TrimSpace(role) == "" {
			return errors.New("httpapi: Config.Principal.Roles must not contain a blank entry")
		}
		if _, dup := seen[role]; dup {
			return errors.New("httpapi: Config.Principal.Roles must not contain a case-sensitive duplicate (" + role + ")")
		}
		seen[role] = struct{}{}
	}
	return nil
}

type principalContextKey struct{}

// PrincipalFromContext returns the LocalPrincipalSnapshot BindPrincipal
// attached to ctx, or the zero value if BindPrincipal never ran (e.g. a
// handler under test that bypasses the real middleware chain).
func PrincipalFromContext(ctx context.Context) LocalPrincipalSnapshot {
	p, _ := ctx.Value(principalContextKey{}).(LocalPrincipalSnapshot)
	return p
}

// BindPrincipal injects principal into every request's context — the ONLY
// path any downstream handler or command dispatch has to learn the current
// actor/roles (ADR-028: "Actor và ActorRoles là authentication context,
// không phải input tự khai... HTTP body/header và CLI flag MUST NOT được
// phép override actor/roles"). It never reads r at all — not the body, not
// any header, not the URL — precisely so a caller sending its own
// `{"actor":"..."}` field or `X-Actor` header cannot influence what a
// handler observes; PrincipalFromContext always returns this same bound
// value for the life of the Server, and the only way to change it is a
// fresh process start (a new Server, i.e. a new Config.Principal), never a
// request.
func BindPrincipal(principal LocalPrincipalSnapshot) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
