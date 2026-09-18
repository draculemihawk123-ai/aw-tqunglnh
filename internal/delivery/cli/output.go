package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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

// flusher is satisfied by a buffered writer (e.g. *bufio.Writer) a caller
// might wrap stdout in for a long-lived streaming command — EncodeNDJSONLine
// opportunistically flushes through it when present. A plain os.Stdout
// (unbuffered — each Write already reaches the OS) or a *bytes.Buffer (test)
// does not implement this, and EncodeNDJSONLine works correctly with either.
type flusher interface{ Flush() error }

// EncodeNDJSONLine writes one compact (no indentation) JSON-encoded line to
// w, terminated by "\n", and flushes immediately if w supports it — the
// deliberate opposite shape of writeStableJSON/EncodeQueryResult/
// EncodeCommandResult above (V6-15N's own task brief: "the OPPOSITE shape
// of NDJSON... unbounded stream, flushed incrementally"), for a leaf whose
// own result is a live, potentially-unbounded event stream rather than one
// finite document (`aw events watch`, and any future leaf with the
// identical shape).
//
// Design choice, documented here per this task's own brief (promote vs.
// scope narrowly, mirroring V6-15K's own identical decision for
// WriteBinaryOutput/BindOutputFlag): promoted to this shared package rather
// than kept private to internal/delivery/cli/events, since NDJSON-per-line
// output is a generic wire SHAPE ("compact JSON object, one per line,
// flushed as produced") with no events-domain concept baked into it —
// exactly the same reasoning that already promoted WriteBinaryOutput
// (a generic "stream raw bytes" shape) to this package instead of scoping
// it to internal/delivery/cli/evidence. Any future streaming leaf (a log
// tail, a job-progress watch) wants the identical helper rather than
// reinventing "marshal compact, append newline, write in one Write call,
// flush" for itself.
//
// The encode+append+write happens in exactly ONE call to w.Write — never a
// separate Write for the JSON bytes and a second for the trailing "\n" —
// so a concurrent reader on the other end of a pipe/socket can never
// observe a half-written line split across two underlying writes: this is
// what makes "every line is independently, completely parseable" hold even
// against a consumer reading faster than this producer than expected, or a
// producer whose caller mixes NDJSON lines with nothing else on the same
// writer.
func EncodeNDJSONLine(w io.Writer, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cli: encode NDJSON line: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := w.Write(encoded); err != nil {
		return fmt.Errorf("cli: write NDJSON line: %w", err)
	}
	if f, ok := w.(flusher); ok {
		if err := f.Flush(); err != nil {
			return fmt.Errorf("cli: flush NDJSON line: %w", err)
		}
	}
	return nil
}

// Diagnosticf writes one human-readable diagnostic/progress line to w
// (always the process' own stderr in real use, injected here for
// testability) — V6-15B's own "diagnostics stderr" rule: a
// JSON-consuming caller's stdout parse must never see this text mixed in.
func Diagnosticf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format+"\n", args...)
}

// WriteBinaryOutput streams content to outputPath — the real, raw-content
// counterpart of EncodeQueryResult/EncodeCommandResult's JSON-document
// writers above, for a leaf whose own result IS a byte stream (V6-15K's
// own `aw artifact get --output <path|->`, and any future leaf with the
// identical shape). outputPath == "-" streams directly to stdout (the same
// io.Writer a leaf's other output goes to — a caller choosing "-" is
// choosing to make stdout carry exactly, and only, these raw bytes, never
// mixed with a JSON document); any other value is treated as a real file
// path, freshly created (never appended to, never following a symlink into
// an unexpected location — os.O_EXCL is deliberately NOT used, since a
// second `aw artifact get` to the same path is an ordinary, expected
// overwrite, not a hazard this framework needs to guard against).
//
// content is copied via io.Copy — never buffered whole into memory first —
// so an arbitrarily large artifact never risks this process' own memory
// budget, mirroring internal/delivery/httpapi/evidence's own
// handleGetArtifactContent streaming discipline exactly. The CALLER is
// responsible for ensuring content is only ever handed to this function
// after any integrity check (e.g. ports.ArtifactStore.Verify) has already
// succeeded — this function starts writing the moment it is called, so a
// leaf that verifies-then-opens-then-calls-this-function (never opens
// before verifying) gets the same "tamper caught before any byte streams
// out" guarantee the HTTP route already has, for free. On a real file
// target, a copy failure removes the partial file (best-effort) rather
// than leaving a truncated, silently-wrong file behind.
func WriteBinaryOutput(stdout io.Writer, outputPath string, content io.Reader) (int64, error) {
	if outputPath == "" {
		return 0, fmt.Errorf("cli: WriteBinaryOutput: outputPath must not be empty (use \"-\" for stdout)")
	}
	if outputPath == "-" {
		n, err := io.Copy(stdout, content)
		if err != nil {
			return n, fmt.Errorf("cli: write output to stdout: %w", err)
		}
		return n, nil
	}

	f, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("cli: create output file %s: %w", outputPath, err)
	}
	n, copyErr := io.Copy(f, content)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(outputPath)
		return n, fmt.Errorf("cli: write output file %s: %w", outputPath, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(outputPath)
		return n, fmt.Errorf("cli: close output file %s: %w", outputPath, closeErr)
	}
	return n, nil
}
