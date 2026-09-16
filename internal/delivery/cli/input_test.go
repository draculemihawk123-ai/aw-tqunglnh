package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func TestReadBoundedInputTableDriven(t *testing.T) {
	dir := t.TempDir()
	smallFile := filepath.Join(dir, "small.json")
	if err := os.WriteFile(smallFile, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bigFile := filepath.Join(dir, "big.json")
	if err := os.WriteFile(bigFile, bytes.Repeat([]byte("a"), 100), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		stdin    string
		filePath string
		maxBytes int64
		want     string
		wantErr  error
	}{
		{name: "reads from stdin when no file given", stdin: `{"x":1}`, maxBytes: 100, want: `{"x":1}`},
		{name: "reads from file when given, ignoring stdin", stdin: `{"ignored":true}`, filePath: smallFile, maxBytes: 100, want: `{"a":1}`},
		{name: "stdin exactly at bound is accepted", stdin: strings.Repeat("a", 10), maxBytes: 10, want: strings.Repeat("a", 10)},
		{name: "stdin exceeding bound is rejected, not truncated", stdin: strings.Repeat("a", 11), maxBytes: 10, wantErr: cli.ErrInputTooLarge},
		{name: "file exceeding bound is rejected", filePath: bigFile, maxBytes: 10, wantErr: cli.ErrInputTooLarge},
		{name: "missing file is a typed error, not silently empty", filePath: filepath.Join(dir, "missing.json"), maxBytes: 100, wantErr: os.ErrNotExist},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cli.ReadBoundedInput(strings.NewReader(tc.stdin), tc.filePath, tc.maxBytes)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ReadBoundedInput() error = %v, want wrapping %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadBoundedInput() error = %v, want nil", err)
			}
			if string(got) != tc.want {
				t.Fatalf("ReadBoundedInput() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadBoundedInputDefaultsMaxBytesWhenNonPositive(t *testing.T) {
	got, err := cli.ReadBoundedInput(strings.NewReader("hello"), "", 0)
	if err != nil {
		t.Fatalf("ReadBoundedInput() error = %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("ReadBoundedInput() = %q, want %q", got, "hello")
	}
}

func TestReadBoundedInputNoSourceIsATypedError(t *testing.T) {
	_, err := cli.ReadBoundedInput(nil, "", 100)
	if err == nil {
		t.Fatal("ReadBoundedInput(nil, \"\", ...) error = nil, want a typed no-source error")
	}
}
