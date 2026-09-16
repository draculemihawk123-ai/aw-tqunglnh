package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

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
