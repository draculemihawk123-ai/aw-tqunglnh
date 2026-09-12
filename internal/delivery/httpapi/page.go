package httpapi

import (
	"errors"
	"strconv"
)

// DefaultPageLimit is the page size a route uses when the caller's
// `limit` query parameter is absent — V6-02A's own "Phạm vi: page/limit"
// line and its "bounded defaults/max" Verify bullet.
const DefaultPageLimit = 50

// MaxPageLimit bounds every route's page size regardless of what the
// caller requests — the bound itself, not the caller's stated preference,
// is what actually protects a paging endpoint from an unbounded scan.
const MaxPageLimit = 200

// ErrInvalidLimit is returned by ResolveLimit for a `limit` value that is
// present but not a positive integer — an explicit malformed value (a
// negative number, zero, or non-numeric text) is a caller mistake worth
// surfacing, unlike an absent value (which silently defaults) or a
// too-large one (which silently clamps).
var ErrInvalidLimit = errors.New("httpapi: limit must be a positive integer")

// ResolveLimit parses raw — a route's own `?limit=` query value, or ""
// when the caller omitted it — into a bounded page size:
//
//   - "" defaults to DefaultPageLimit.
//   - A non-positive or non-numeric value is ErrInvalidLimit.
//   - A value above MaxPageLimit silently clamps down to MaxPageLimit.
//   - Anything else is returned unchanged.
func ResolveLimit(raw string) (int, error) {
	if raw == "" {
		return DefaultPageLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, ErrInvalidLimit
	}
	if n > MaxPageLimit {
		return MaxPageLimit, nil
	}
	return n, nil
}
