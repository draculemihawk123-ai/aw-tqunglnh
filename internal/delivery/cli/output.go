package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// ResultEnvelope is the one finite JSON document a mutating command
// writes to stdout (V6-15B's own "finite JSON one-document stdout" line):
// IdempotencyKey is ALWAYS present — caller-supplied or freshly generated
// by BuildEnvelope — so an operator who omitted --idempotency-key can
// read the generated value back out of this same response and reuse it
// for a deliberate retry (the "generated key returned" verify bullet).
// Every mutating leaf gets the identical shape whether or not it happened
// to supply its own key, rather than a schema that only sometimes carries
// the field.
type ResultEnvelope struct {
	IdempotencyKey string `json:"idempotencyKey"`
	Replayed       bool   `json:"replayed"`
	Result         any    `json:"result"`
}

// EncodeCommandResult writes envelope as the one JSON document on stdout.
// It must be the only thing a mutating leaf ever writes to stdout for one
// invocation — progress/diagnostic text belongs on a separate writer (see
// Diagnosticf), never mixed into this same stream, so a JSON-consuming
// caller's stdout parse is never polluted (V6-15B's own "diagnostics
// stderr" line).
func EncodeCommandResult(stdout io.Writer, envelope ResultEnvelope) error {
	return writeStableJSON(stdout, envelope)
}

// EncodeQueryResult writes result as the one JSON document on stdout for
// a read-only leaf command (list/show/...), which carries no idempotency
// key or replay flag at all — those concepts exist only for mutations.
func EncodeQueryResult(stdout io.Writer, result any) error {
	return writeStableJSON(stdout, result)
}

// writeStableJSON encodes value as indented JSON with a trailing newline —
// mirroring cmd/aw/adapter.go's own writeStableJSON exactly (V6-15B's own
// task brief names this as the existing "one JSON document stdout"
// precedent to follow): every value this package ever passes here
// (ResultEnvelope, or a leaf's own typed view) has a fixed struct shape
// with json tags, so field order/name/nesting is fixed by the Go type
// declaration, never by map iteration order.
func writeStableJSON(stdout io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("cli: encode JSON output: %w", err)
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	return err
}

// Diagnosticf writes one human-readable diagnostic/progress line to w
// (always the process' own stderr in real use, injected here for
// testability) — V6-15B's own "diagnostics stderr" rule: a
// JSON-consuming caller's stdout parse must never see this text mixed in.
func Diagnosticf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format+"\n", args...)
}
