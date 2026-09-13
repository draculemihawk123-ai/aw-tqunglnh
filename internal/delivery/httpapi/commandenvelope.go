// This file is V6-02's own "header-to-command mapping, canonical semantic
// hash, receipt replay" primitives (docs/design/08-v6-api-projections.md
// V6-02) — the HTTP-side equivalent of cmd/aw/definition.go's own
// requestHash helper, generalized for a real JSON request body instead of
// a handful of CLI flag strings. This package NEVER writes or caches a
// receipt itself (V6-02's own "Không làm" line) — every helper here is
// read-only or pure; the authoritative receipt write always happens
// inside the real application command's own transaction, exactly like
// every command this codebase already ships (internal/app/catalog.CreateProject
// and siblings) — see receiptreplay.go for the one read-only lookup this
// package is allowed to perform, and event_catalog-style architecture
// test in commandenvelope_test.go that proves it.
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// IdempotencyKeyHeader is the header every HTTP mutation must carry
// (V6-02's own "Idempotency-Key bắt buộc").
const IdempotencyKeyHeader = "Idempotency-Key"

// IfMatchHeader is the header an update must carry, carrying the
// caller's own expected current ETag (V6-02's own "update bắt buộc
// strong If-Match").
const IfMatchHeader = "If-Match"

// ETagHeader is the response header carrying the resulting resource's own
// current ETag after a successful mutation.
const ETagHeader = "ETag"

// ErrIdempotencyKeyRequired is returned by RequireIdempotencyKey when the
// header is absent or empty.
var ErrIdempotencyKeyRequired = errors.New("httpapi: Idempotency-Key header is required")

// ErrIfMatchRequired is returned by RequireIfMatch when the header is
// absent or empty.
var ErrIfMatchRequired = errors.New("httpapi: If-Match header is required")

// RequireIdempotencyKey extracts and validates the Idempotency-Key header.
func RequireIdempotencyKey(r *http.Request) (string, error) {
	key := r.Header.Get(IdempotencyKeyHeader)
	if key == "" {
		return "", ErrIdempotencyKeyRequired
	}
	return key, nil
}

// RequireIfMatch extracts and validates the If-Match header — every
// update-shaped mutation calls this (never GetIfMatch, which does not
// exist: a create has no current resource to match, and V6-02's own
// spec never asks for an optional If-Match).
func RequireIfMatch(r *http.Request) (string, error) {
	value := r.Header.Get(IfMatchHeader)
	if value == "" {
		return "", ErrIfMatchRequired
	}
	return value, nil
}

// ETagFromVersion deterministically encodes an aggregate's own version
// counter as a strong HTTP ETag — the one place this package decides that
// encoding, so a version number and its own wire ETag never drift apart
// across two different call sites choosing two different formats.
func ETagFromVersion(version uint64) string {
	return `"` + strconv.FormatUint(version, 10) + `"`
}

// VersionFromETag parses an ETagFromVersion-produced ETag back into the
// version it encodes, or a typed error if value is not exactly that
// shape — never a best-effort partial parse.
func VersionFromETag(value string) (uint64, error) {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, fmt.Errorf("httpapi: %q is not a strong ETag", value)
	}
	version, err := strconv.ParseUint(value[1:len(value)-1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("httpapi: %q does not encode a version: %w", value, err)
	}
	return version, nil
}

// CanonicalizeJSON strictly decodes raw into dst (unknown fields rejected,
// exactly one JSON value required — DecodeJSON's own contract, reused
// here rather than duplicated) and returns dst re-marshaled: a canonical,
// key-order-independent, whitespace-independent byte encoding two
// requests with the same semantic content always produce identically,
// regardless of how the original bytes happened to be formatted
// (encoding/json.Marshal sorts map keys and always emits a struct's own
// fields in declared order — see cursor.go's own Fingerprint doc comment
// for the same reasoning applied to a different shared primitive).
func CanonicalizeJSON(r *http.Request, limitBytes int64, dst any) ([]byte, error) {
	if err := DecodeJSON(r, limitBytes, dst); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(dst)
	if err != nil {
		return nil, fmt.Errorf("httpapi: marshal canonical payload: %w", err)
	}
	return canonical, nil
}

// SemanticHash computes V6-02's own canonical semantic hash: command
// type, scope, normalized payload, an optional exact content digest (for
// a command carrying raw/binary content no JSON canonicalization applies
// to, e.g. a future attachment upload — pass "" when there is none), and
// the expected version — deliberately excluding JSON formatting, the
// request/correlation ID, session token, and any other transport metadata
// (V6-02's own "Thực hiện" line: two requests differing only in those
// respects must hash identically). Mirrors cmd/aw/definition.go's own
// requestHash exactly (NUL-separated parts, sha256, "sha256:" prefix) so
// every RequestHash this codebase ever computes, HTTP or CLI, follows the
// one convention.
func SemanticHash(commandType string, scope ports.CommandScope, normalizedPayload []byte, extraContentDigest string, expectedVersion uint64) string {
	h := sha256.New()
	parts := []string{commandType, scope.Key(), string(normalizedPayload), extraContentDigest, strconv.FormatUint(expectedVersion, 10)}
	for _, part := range parts {
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
