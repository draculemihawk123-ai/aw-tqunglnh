package workspaceinspection

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// requireQueryParam reads name from query, trims it, and writes a 400
// field-level validation error (returning ok=false) when it is absent —
// every one of this package's own required scope/revision parameters goes
// through this one helper so a missing value always produces the identical
// shape of response.
func requireQueryParam(w http.ResponseWriter, query url.Values, name string) (string, bool) {
	value := strings.TrimSpace(query.Get(name))
	if value == "" {
		writeValidationError(w, name, "is required")
		return "", false
	}
	return value, true
}

// requireUint64QueryParam parses name as an unsigned 64-bit integer —
// workspace.Revision.WorkspaceGeneration's own real type — writing a 400
// field-level validation error (ok=false) for either an absent or a
// non-numeric value.
func requireUint64QueryParam(w http.ResponseWriter, query url.Values, name string) (uint64, bool) {
	raw, ok := requireQueryParam(w, query, name)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		writeValidationError(w, name, "must be a non-negative integer")
		return 0, false
	}
	return n, true
}

// optionalInt64QueryParam parses name as a positive int64 when present,
// returning 0 (this package's own app-layer default sentinel — see
// internal/app/workspaceinspection.clamp's own doc comment: "a caller that
// supplies zero ... gets the default") when absent. A present-but-malformed
// value (non-numeric, or negative) is a 400 field-level validation error.
func optionalInt64QueryParam(w http.ResponseWriter, query url.Values, name string) (int64, bool) {
	raw := strings.TrimSpace(query.Get(name))
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		writeValidationError(w, name, "must be a non-negative integer")
		return 0, false
	}
	return n, true
}

// optionalIntQueryParam mirrors optionalInt64QueryParam for the two
// int-typed limits (GetDiffRequest.FileLimit, GetRepositoryLogRequest.Limit).
func optionalIntQueryParam(w http.ResponseWriter, query url.Values, name string) (int, bool) {
	raw := strings.TrimSpace(query.Get(name))
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		writeValidationError(w, name, "must be a non-negative integer")
		return 0, false
	}
	return n, true
}

// requirePathParam reads name from an already-matched route's own path
// value — mirrors internal/delivery/httpapi/workspacestate.go's own
// identical projectId/repositoryWorkspaceId checks exactly, defensively
// re-validated here even though net/http's own ServeMux never actually
// matches an empty path segment against a "{name}" pattern.
func requirePathParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	value := strings.TrimSpace(r.PathValue(name))
	if value == "" {
		writeValidationError(w, name, "is required")
		return "", false
	}
	return value, true
}
