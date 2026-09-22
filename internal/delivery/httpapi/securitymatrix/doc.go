// Package securitymatrix is V6-13's own API security and authority-boundary
// proof suite (docs/design/08-v6-api-projections.md V6-13: "chứng minh
// scope/security mặc định từ chối và HTTP không có đường tắt thực thi").
//
// It contains NO production code at all — this file exists only to give the
// directory a package clause. Every other file here is a _test.go file, and
// every one of them drives the REAL, fully-composed production route set
// (internal/delivery/httpcompose.ComposeRoutes — V6-12's own extraction of
// cmd/aw/serve.go's route-composition block) behind the REAL
// internal/delivery/httpapi.Server middleware chain, over a REAL loopback
// TCP listener, backed by a REAL temporary SQLite database, a REAL
// filesystem artifact store and REAL application commands. Nothing here
// mocks a handler, hand-lists a route, or fabricates a database row outside
// the real command/Tx paths this repo's own fixtures already use.
//
// # Why a separate package rather than more tests in httpapi itself
//
// The matrix's row set must be the WHOLE composed route set, which only
// httpcompose.ComposeRoutes can produce — and httpcompose imports
// internal/delivery/httpapi, so a test living inside package httpapi (or
// package httpapi_test) could never import it back without an import cycle.
// internal/delivery/httpapi/apicontract (V6-12) hit the identical
// constraint and solved it the identical way: its own subpackage whose
// tests walk the real composed registry. This package follows that idiom
// exactly, and is still inside the `./internal/delivery/httpapi/...`
// pattern V6-13's own Verify line names.
//
// # What "route × scope × role" actually means in THIS system
//
// V6-13's own Verify line asks for an "automated route×scope×role matrix".
// The honest shape of each of those three axes in this codebase, as it
// actually exists today, is:
//
//   - route: every one of the routes ComposeRoutes registers, enumerated
//     programmatically from httpapi.RouteRegistry.Descriptors() — never
//     hand-listed, so a route added by a future leaf task is covered the
//     moment it is wired in (and coverage_test.go fails outright if any
//     descriptor ends up matched by no scenario at all).
//
//   - scope: httpapi.RouteDescriptor.ScopeKind, ADR-025's own closed
//     INSTALLATION|PROJECT pair. A PROJECT-scoped route must derive its
//     ProjectID by RELOADING the authoritative target named in the URL
//     (contract point 3, §1 of the design doc: "Mọi item route reload
//     authoritative target để suy Project/scope và authorize; không tin ID
//     shape, payload hoặc projection"), so the real, testable boundary is
//     nested-ID OWNERSHIP: a route addressed as /projects/{A}/…/{X} must
//     never serve an X that belongs to project B, and must be unable to
//     tell the caller whether X exists at all.
//
//   - role: ADR-028's LocalPrincipalSnapshot{Actor, Roles}. This is a
//     single-operator local installation: the principal is resolved ONCE
//     per process from trusted startup config and bound for the server's
//     whole lifetime (httpapi/principal.go's BindPrincipal never reads the
//     request), so "role" is not a per-request dimension a caller can
//     vary. What IS testable — and what this suite tests — is the three
//     properties that actually make that design safe: a request can never
//     influence the observed principal (no body field, no header, no query
//     parameter), a stored idempotency receipt is keyed by the CURRENT
//     actor so a restart under a DIFFERENT principal cannot replay the
//     previous one's committed result, and a replay is reached only AFTER
//     the target reload that authorizes it (replay_test.go).
//
// # Deliberate scoping decisions
//
// Test data lives in exactly two real projects (alpha and beta) seeded with
// the six entity kinds V6-13's own "Thực hiện" line names — Run, WorkItem,
// blocker, workspace, ReleaseSet and artifact (plus the Evidence row an
// artifact is only ever reachable through). Routes whose owning entity kind
// this suite does not seed are still covered by the generic
// fabricated-identifier matrix (scope_test.go), which proves the weaker but
// universal property "no route ever answers 2xx for an identifier this
// server never issued" for every single registered route. The two are
// complementary on purpose: the seeded cross-project cases prove the strong
// ownership property where real data exists, and the generic matrix proves
// no route is exempt.
//
// This package adds no new middleware, helper or "security framework"
// (V6-13's own "Không làm"): every check below exercises a guard that already
// shipped in V6-01, V6-01A, V6-02A or an endpoint leaf task. The matrix did
// find two places where an existing guard ran in the wrong ORDER — release
// eligibility before project ownership (ownership_order_test.go) and the
// receipt lookup before the role check on an approval replay (replay_test.go,
// plus internal/app/runtime/approval_test.go for the path `aw` takes). Each
// was fixed at its source by reordering the existing check, and is pinned by
// a regression test proven to fail without the fix.
package securitymatrix
