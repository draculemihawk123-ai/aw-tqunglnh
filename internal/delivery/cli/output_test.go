package cli_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func TestEncodeCommandResultTableDriven(t *testing.T) {
	tests := []struct {
		name     string
		envelope cli.ResultEnvelope
	}{
		{name: "fresh result", envelope: cli.ResultEnvelope{IdempotencyKey: "key-1", Replayed: false, Result: map[string]any{"id": "abc"}}},
		{name: "replayed result", envelope: cli.ResultEnvelope{IdempotencyKey: "key-2", Replayed: true, Result: json.RawMessage(`{"id":"abc"}`)}},
		{name: "nil result", envelope: cli.ResultEnvelope{IdempotencyKey: "key-3", Replayed: false, Result: nil}},
		{name: "generated key returned even with no domain result yet", envelope: cli.ResultEnvelope{IdempotencyKey: "generated-xyz", Replayed: false, Result: struct{}{}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := cli.EncodeCommandResult(&stdout, tc.envelope); err != nil {
				t.Fatalf("EncodeCommandResult() error = %v", err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr got written to: %q", stderr.String())
			}
			if !strings.HasSuffix(stdout.String(), "\n") {
				t.Fatal("stdout does not end with a trailing newline")
			}

			var decoded map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
				t.Fatalf("stdout is not exactly one JSON document: %v (%q)", err, stdout.String())
			}
			if decoded["idempotencyKey"] != tc.envelope.IdempotencyKey {
				t.Fatalf("idempotencyKey = %v, want %q", decoded["idempotencyKey"], tc.envelope.IdempotencyKey)
			}
			if decoded["replayed"] != tc.envelope.Replayed {
				t.Fatalf("replayed = %v, want %v", decoded["replayed"], tc.envelope.Replayed)
			}

			// Exactly one JSON document: decode the one value that must be
			// there, then prove nothing else follows it (io.EOF) —
			// mirroring commandenvelope.go's own CanonicalizeJSON "reject
			// a second value" discipline applied here to OUTPUT rather
			// than input.
			decoder := json.NewDecoder(&stdout)
			var first json.RawMessage
			if err := decoder.Decode(&first); err != nil {
				t.Fatalf("decode the one JSON document stdout must carry: %v", err)
			}
			var second json.RawMessage
			if err := decoder.Decode(&second); err != io.EOF {
				t.Fatalf("stdout carried more than one JSON document: err=%v extra=%s", err, second)
			}
		})
	}
}

func TestEncodeQueryResultNeverIncludesIdempotencyShape(t *testing.T) {
	var stdout bytes.Buffer
	if err := cli.EncodeQueryResult(&stdout, map[string]any{"items": []string{"a", "b"}}); err != nil {
		t.Fatalf("EncodeQueryResult() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := decoded["idempotencyKey"]; ok {
		t.Fatal("EncodeQueryResult output carries an idempotencyKey field — queries have no idempotency concept")
	}
	if _, ok := decoded["replayed"]; ok {
		t.Fatal("EncodeQueryResult output carries a replayed field — queries have no replay concept")
	}
}

// TestWriteBinaryOutput_Dash_StreamsRawBytesToStdout proves this task's own
// "binary stdout" Verify bullet: genuinely binary, non-UTF8 content
// round-trips byte-for-byte to the given stdout writer with no text-mode
// corruption, and nothing else (no trailing JSON, no extra newline) is
// appended.
func TestWriteBinaryOutput_Dash_StreamsRawBytesToStdout(t *testing.T) {
	binary := []byte{0x00, 0x01, 0xFF, 0xFE, 0x0A, 0x0D, 0x00, 'h', 'i', 0x80, 0x81}
	var stdout bytes.Buffer
	n, err := cli.WriteBinaryOutput(&stdout, "-", bytes.NewReader(binary))
	if err != nil {
		t.Fatalf("WriteBinaryOutput() error = %v", err)
	}
	if n != int64(len(binary)) {
		t.Fatalf("n = %d, want %d", n, len(binary))
	}
	if !bytes.Equal(stdout.Bytes(), binary) {
		t.Fatalf("stdout = %v, want exactly %v (byte-for-byte)", stdout.Bytes(), binary)
	}
}

// TestWriteBinaryOutput_RealFile_WritesExactBytes proves a real file target
// receives byte-identical content, never buffered/truncated/altered.
func TestWriteBinaryOutput_RealFile_WritesExactBytes(t *testing.T) {
	content := bytes.Repeat([]byte{0x00, 0x01, 0x02, 0xFF}, 1024)
	path := filepath.Join(t.TempDir(), "out.bin")
	var stdout bytes.Buffer
	n, err := cli.WriteBinaryOutput(&stdout, path, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("WriteBinaryOutput() error = %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("n = %d, want %d", n, len(content))
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout got written to when writing to a real file: %q", stdout.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("file content does not match the original byte-for-byte")
	}
}

// TestWriteBinaryOutput_RealFile_OverwritesExisting proves a second write to
// the same path replaces the prior content entirely (O_TRUNC), rather than
// appending or leaving stale trailing bytes from a longer previous write.
func TestWriteBinaryOutput_RealFile_OverwritesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.bin")
	var stdout bytes.Buffer
	if _, err := cli.WriteBinaryOutput(&stdout, path, strings.NewReader("a much longer first write")); err != nil {
		t.Fatalf("first WriteBinaryOutput() error = %v", err)
	}
	if _, err := cli.WriteBinaryOutput(&stdout, path, strings.NewReader("short")); err != nil {
		t.Fatalf("second WriteBinaryOutput() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "short" {
		t.Fatalf("file content = %q, want %q (second write must fully overwrite the first)", got, "short")
	}
}

// TestWriteBinaryOutput_CopyFailure_RemovesPartialFile proves a mid-copy
// failure never leaves a truncated, silently-wrong file behind at a real
// file target — the file is removed on a copy error rather than kept
// half-written.
func TestWriteBinaryOutput_CopyFailure_RemovesPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.bin")
	failingReader := &erroringReader{after: []byte("partial-bytes-"), err: errors.New("simulated read failure")}
	var stdout bytes.Buffer
	if _, err := cli.WriteBinaryOutput(&stdout, path, failingReader); err == nil {
		t.Fatal("WriteBinaryOutput() error = nil, want the simulated read failure")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("partial file was left behind at %s after a copy failure", path)
	}
}

// TestWriteBinaryOutput_RejectsEmptyPath proves an empty outputPath (a
// leaf that forgot to require --output, or a caller that never validated
// it) is a clean, typed error rather than a silent no-op or a write to an
// unintended location.
func TestWriteBinaryOutput_RejectsEmptyPath(t *testing.T) {
	var stdout bytes.Buffer
	if _, err := cli.WriteBinaryOutput(&stdout, "", strings.NewReader("x")); err == nil {
		t.Fatal("WriteBinaryOutput() with empty outputPath error = nil, want an error")
	}
}

// erroringReader returns `after` once, then always fails with err — used to
// simulate a mid-stream I/O failure without needing real disk exhaustion.
type erroringReader struct {
	after []byte
	err   error
	sent  bool
}

func (r *erroringReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		n := copy(p, r.after)
		return n, nil
	}
	return 0, r.err
}

// TestEncodeNDJSONLine_CompactNoIndentation proves the encoded line is
// compact JSON (no newline/indentation inside the object itself) followed
// by exactly one trailing "\n" — the opposite shape of writeStableJSON's
// own MarshalIndent.
func TestEncodeNDJSONLine_CompactNoIndentation(t *testing.T) {
	var buf bytes.Buffer
	if err := cli.EncodeNDJSONLine(&buf, map[string]any{"a": 1, "b": "two"}); err != nil {
		t.Fatalf("EncodeNDJSONLine() error = %v", err)
	}
	got := buf.String()
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("output has %d newlines, want exactly 1: %q", strings.Count(got, "\n"), got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("output does not end with a trailing newline: %q", got)
	}
	if strings.Contains(strings.TrimSuffix(got, "\n"), "\n") || strings.Contains(got, "  ") {
		t.Fatalf("output is not compact (contains indentation/extra whitespace): %q", got)
	}
}

// TestEncodeNDJSONLine_MultipleCalls_ContinuousNDJSONParsing is this task's
// own single most important test for the NDJSON half of this task: several
// EncodeNDJSONLine calls against the SAME writer produce output where every
// line decodes as a complete, independent JSON document via
// bufio.Scanner+json.Unmarshal — never a partial line, never two objects
// merged onto one line, never interleaved output.
func TestEncodeNDJSONLine_MultipleCalls_ContinuousNDJSONParsing(t *testing.T) {
	var buf bytes.Buffer
	type line struct {
		N int `json:"n"`
	}
	const count = 50
	for i := 0; i < count; i++ {
		if err := cli.EncodeNDJSONLine(&buf, line{N: i}); err != nil {
			t.Fatalf("EncodeNDJSONLine(%d) error = %v", i, err)
		}
	}

	scanner := bufio.NewScanner(&buf)
	got := 0
	for scanner.Scan() {
		var decoded line
		if err := json.Unmarshal(scanner.Bytes(), &decoded); err != nil {
			t.Fatalf("line %d: not valid, complete JSON: %v (%q)", got, err, scanner.Text())
		}
		if decoded.N != got {
			t.Fatalf("line %d decoded N = %d, want %d (lines out of order or corrupted)", got, decoded.N, got)
		}
		got++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if got != count {
		t.Fatalf("scanned %d lines, want %d", got, count)
	}
}

// slowWriter sleeps briefly before every Write call — this task's own
// "slow consumer" Verify bullet: EncodeNDJSONLine must neither corrupt
// output nor deadlock when its sink is deliberately slow (a stand-in for a
// slow downstream consumer such as `jq` reading a real OS pipe with natural
// backpressure — see internal/delivery/cli/events's own doc comment for why
// a real CLI never needs a bounded-channel/disconnect policy the way
// internal/delivery/httpapi/eventstream's own HTTP server does).
type slowWriter struct {
	w     io.Writer
	delay time.Duration
}

func (s *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(s.delay)
	return s.w.Write(p)
}

func TestEncodeNDJSONLine_SlowConsumer_NeverCorruptsOrDeadlocks(t *testing.T) {
	var buf bytes.Buffer
	sink := &slowWriter{w: &buf, delay: 5 * time.Millisecond}
	type line struct {
		N int `json:"n"`
	}
	const count = 20
	start := time.Now()
	for i := 0; i < count; i++ {
		if err := cli.EncodeNDJSONLine(sink, line{N: i}); err != nil {
			t.Fatalf("EncodeNDJSONLine(%d) error = %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s for %d slow writes — looks deadlocked/hung rather than merely slow", elapsed, count)
	}

	scanner := bufio.NewScanner(&buf)
	got := 0
	for scanner.Scan() {
		var decoded line
		if err := json.Unmarshal(scanner.Bytes(), &decoded); err != nil {
			t.Fatalf("line %d corrupted by a slow sink: %v (%q)", got, err, scanner.Text())
		}
		if decoded.N != got {
			t.Fatalf("line %d = %d, want %d", got, decoded.N, got)
		}
		got++
	}
	if got != count {
		t.Fatalf("scanned %d lines, want %d", got, count)
	}
}

func TestDiagnosticfWritesOnlyToItsOwnWriterNeverStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cli.Diagnosticf(&stderr, "probing %s (%d/%d)", "repo", 1, 3)
	if stdout.Len() != 0 {
		t.Fatalf("stdout got written to: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "probing repo (1/3)") {
		t.Fatalf("stderr = %q, want it to contain the formatted diagnostic", stderr.String())
	}
}
