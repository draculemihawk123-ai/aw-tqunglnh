package main

import (
	"reflect"
	"testing"
)

// V12-08: aw up gives the shared flags to both commands and the rest to the one they name.
func TestUpArguments_SplitsSharedAndOwnFlags(t *testing.T) {
	serveArgs, workerArgs, err := upArguments([]string{
		"--db", "aw.db", "--artifact-root", "a", "--workspace-root", "w", "--claude-executable", "claude",
		"--serve-args", "--port 8787", "--worker-args", "--claude-permission-mode acceptEdits --worker-concurrency 2",
	})
	if err != nil {
		t.Fatal(err)
	}
	shared := []string{"--db", "aw.db", "--artifact-root", "a", "--workspace-root", "w", "--claude-executable", "claude"}
	if want := append(append([]string{}, shared...), "--port", "8787"); !reflect.DeepEqual(serveArgs, want) {
		t.Fatalf("serve args = %v, want %v", serveArgs, want)
	}
	if want := append(append([]string{}, shared...), "--claude-permission-mode", "acceptEdits", "--worker-concurrency", "2"); !reflect.DeepEqual(workerArgs, want) {
		t.Fatalf("worker args = %v, want %v", workerArgs, want)
	}
	if _, _, err := upArguments([]string{"positional"}); !isUsageError(err) {
		t.Fatalf("a positional argument must be a usage error, got %v", err)
	}
}
