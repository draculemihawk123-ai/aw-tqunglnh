package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// CursorState is every piece of state httpapi's opaque pagination cursor
// binds together — V6-02A's own "Thực hiện" line: "cursor bind project,
// query/filter/sort, projection generation, upper watermark và last key".
//
//   - ProjectID: the project this cursor was issued for; a cursor handed
//     back on one project's route must never resume a walk on another.
//   - QueryFingerprint: Fingerprint's own hash of the exact filter/sort
//     shape the walk started under (see Fingerprint below) — a cursor
//     issued under one filter/sort combination can never be silently
//     reused, or silently produce wrong results, under a different one.
//   - Generation: the projection generation (V6-08's own row key
//     "(ProjectID, ProjectionName, Generation, EntityKey)") the walk's
//     rows were read from. V6-09A's own fenced cutover atomically swaps
//     the active generation out from under a long-lived walk; a cursor
//     still naming the old generation must resync, never silently mix
//     rows from two generations.
//   - UpperWatermark: the greatest key/position visible when the FIRST
//     page of this walk was issued (V6-08's own "Cursor is greatest
//     scanned global JournalPosition" vocabulary, applied here to bound
//     one client's own walk rather than the projector's own consumer
//     cursor). Every later page of the SAME walk stays bounded by this
//     same value, so a row written after the walk began never appears
//     partway through it — see the "stable paging across a concurrent
//     write" test in cursor_test.go for exactly what this prevents.
//   - LastKey: the last-seen sort key — the actual keyset-pagination
//     position a route's own query resumes after.
//
// Binding all five together in one signed token is what makes "resuming a
// previous page with a stale/mismatched cursor" detectable as a typed
// resync (Bind, ResyncError below) instead of silently wrong or duplicated
// results.
type CursorState struct {
	ProjectID        string `json:"projectId"`
	QueryFingerprint string `json:"queryFingerprint"`
	Generation       int    `json:"generation"`
	UpperWatermark   int64  `json:"upperWatermark"`
	LastKey          string `json:"lastKey"`
}

// cursorEnvelope is the exact JSON shape CursorCodec signs/verifies: the
// still-encoded CursorState payload bytes plus the signature computed over
// those exact bytes. Keeping Payload as raw bytes (rather than
// re-marshaling a decoded CursorState before verifying) means the
// signature is always checked against the literal bytes a forger would
// have had to reproduce, with no re-encoding step in between that could
// let two different byte sequences verify identically.
type cursorEnvelope struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

// ErrCursorInvalid is returned by CursorCodec.Decode for a token that
// cannot be base64/JSON-decoded, or whose signature does not match its
// payload — garbage input and a genuinely tampered/forged token both
// produce this same error. Mirroring
// internal/domain/adapterbuild.VerifyToken's own ErrInvalidSignature
// discipline: HMAC gives no way to distinguish "corrupted in transit" from
// "an attacker hand-edited this," and none is needed, since the
// caller-facing remedy (reject; the client must obtain a fresh cursor from
// a plain, cursor-less request) is identical either way. This is
// deliberately a different error from ResyncError: a ResyncError means the
// cursor decoded and verified FINE but no longer applies to the caller's
// current project/query/generation, which is an expected, recoverable
// condition an endpoint reports distinctly from "this token is not one we
// ever signed."
var ErrCursorInvalid = errors.New("httpapi: opaque cursor is malformed or has an invalid signature")

// CursorCodec signs and opens opaque pagination-cursor tokens with a
// server-held HMAC-SHA256 secret. This is the one thing standing between a
// client and being able to hand-edit a cursor's ProjectID/LastKey/Generation
// to see another project's rows, skip ahead in (or replay) a walk it was
// never issued for, or force a stale generation to keep being read after a
// cutover — only whoever holds secret can mint a token Decode will accept.
//
// A CursorCodec is safe for concurrent use (it holds no mutable state after
// construction).
type CursorCodec struct {
	secret []byte
}

// NewCursorCodec returns a CursorCodec signing/verifying with secret.
// secret must be non-empty — an empty secret is a composition-root wiring
// mistake to catch immediately at construction (mirroring this package's
// own RouteRegistry.Register/ReadinessChecker.Register "panic on a
// programming error, don't let it become a wrong-at-runtime silent
// success" discipline), never a condition to defer to Encode/Decode. A
// composition root mints one per process — e.g. crypto/rand, mirroring
// V6-01A's own per-start local session token — never a fixed compiled-in
// value; that wiring belongs to whichever future task first issues a real
// cursor, not to this shared codec.
func NewCursorCodec(secret []byte) *CursorCodec {
	if len(secret) == 0 {
		panic("httpapi: CursorCodec secret is required")
	}
	return &CursorCodec{secret: append([]byte(nil), secret...)}
}

// Encode signs state and returns the opaque token string a paged response
// hands back to the caller as its next page's cursor.
func (c *CursorCodec) Encode(state CursorState) (string, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("httpapi: marshal cursor payload: %w", err)
	}
	envelope := cursorEnvelope{Payload: payload, Signature: c.sign(payload)}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("httpapi: marshal cursor envelope: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode opens token, verifying its signature before ever unmarshaling the
// payload into a caller-visible CursorState — a caller must never observe
// even a syntactically-parsed CursorState for a token whose signature does
// not check out. Every failure mode (bad base64, bad envelope/payload
// JSON, wrong signature) reports the single ErrCursorInvalid.
func (c *CursorCodec) Decode(token string) (CursorState, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return CursorState{}, ErrCursorInvalid
	}
	var envelope cursorEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return CursorState{}, ErrCursorInvalid
	}
	if !hmac.Equal([]byte(c.sign(envelope.Payload)), []byte(envelope.Signature)) {
		return CursorState{}, ErrCursorInvalid
	}
	var state CursorState
	if err := json.Unmarshal(envelope.Payload, &state); err != nil {
		return CursorState{}, ErrCursorInvalid
	}
	return state, nil
}

func (c *CursorCodec) sign(payload []byte) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Fingerprint canonically hashes an arbitrary filter/sort/query value —
// typically the small struct a route's own query parameters decode into —
// into the short opaque string CursorState.QueryFingerprint binds to. Pass
// a struct, not a map: encoding/json.Marshal emits a struct's fields in
// their declared order deterministically, which is exactly what makes
// calling this twice with two equivalent values produce the same
// fingerprint, and what makes Bind's later fingerprint comparison
// meaningful (a map's key order is not itself the hazard — Go's
// encoding/json already sorts map keys — but a struct keeps the caller
// from needing to think about it and matches how every other canonical
// hash in this codebase, e.g. command semantic hash, is described: a
// stable encoding of a fixed shape, not of caller-provided key order).
func Fingerprint(query any) (string, error) {
	canonical, err := json.Marshal(query)
	if err != nil {
		return "", fmt.Errorf("httpapi: marshal query for fingerprint: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// ResyncReason names exactly which bound state a cursor no longer matches
// — a client (or an operator reading logs) sees precisely why, rather than
// a bare "conflict."
type ResyncReason string

const (
	// ResyncReasonProjectMismatch: the cursor was issued for a different
	// project than the one this request is scoped to.
	ResyncReasonProjectMismatch ResyncReason = "PROJECT_MISMATCH"
	// ResyncReasonQueryChanged: the cursor's own QueryFingerprint no
	// longer matches the filter/sort actually requested.
	ResyncReasonQueryChanged ResyncReason = "QUERY_CHANGED"
	// ResyncReasonGenerationChanged: V6-09A's own fenced cutover swapped
	// the active projection generation out from under this walk.
	ResyncReasonGenerationChanged ResyncReason = "GENERATION_CHANGED"
)

// ResyncError is the typed error Bind returns when a cursor decoded (and
// verified) fine but no longer applies to the caller's current request —
// V6-02A's own "Thực hiện" line: "mismatch/swap trả typed resync". A
// caller checks for this specifically (errors.As), distinct from
// ErrCursorInvalid, and reports it via WriteResyncRequired (errors.go)
// rather than a generic conflict: the correct client behavior is "restart
// the walk from a fresh, cursor-less request," not "retry the same
// request" or "treat this as a permanent failure."
type ResyncError struct {
	Reason ResyncReason
}

func (e *ResyncError) Error() string {
	return fmt.Sprintf("httpapi: cursor requires resync: %s", e.Reason)
}

// Bind checks a decoded CursorState against the caller's current request
// context (want) — the project actually being queried, the fingerprint of
// the filter/sort actually requested, and the projection generation
// actually active right now (want.UpperWatermark/want.LastKey are not
// compared; those describe where THIS cursor itself resumes from, not
// something to check it against) — and returns a typed *ResyncError naming
// exactly which one no longer matches, checked in this fixed order
// (project, then query, then generation), rather than risk a silent wrong
// result. A nil return means state is still valid to resume from as-is.
func Bind(state, want CursorState) error {
	if state.ProjectID != want.ProjectID {
		return &ResyncError{Reason: ResyncReasonProjectMismatch}
	}
	if state.QueryFingerprint != want.QueryFingerprint {
		return &ResyncError{Reason: ResyncReasonQueryChanged}
	}
	if state.Generation != want.Generation {
		return &ResyncError{Reason: ResyncReasonGenerationChanged}
	}
	return nil
}
