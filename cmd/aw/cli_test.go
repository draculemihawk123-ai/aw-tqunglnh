package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
)

func TestRun_NoArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "Usage: aw") {
		t.Errorf("stderr should contain usage, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty on a usage error, got %q", stdout.String())
	}
}

func TestRun_Help(t *testing.T) {
	for _, flag := range []string{"-h", "--help", "help"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{flag}, &stdout, &stderr)
		if code != exitSuccess {
			t.Errorf("run(%q): exit code = %d, want %d", flag, code, exitSuccess)
		}
		if !strings.Contains(stdout.String(), "Usage: aw") {
			t.Errorf("run(%q): stdout should contain usage, got %q", flag, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("run(%q): stderr should be empty, got %q", flag, stderr.String())
		}
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"launch-the-missiles"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), `unknown command "launch-the-missiles"`) {
		t.Errorf("stderr should name the unknown command, got %q", stderr.String())
	}
}

func TestRun_StubCommandsReportNotYetImplemented(t *testing.T) {
	// "serve" is V6-01's own real implementation now (see serve.go/serve_test.go)
	// — it no longer belongs in this stub-only list.
	for _, name := range []string{"worker", "doctor"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{name}, &stdout, &stderr)
		if code != exitFailure {
			t.Errorf("run(%q): exit code = %d, want %d (exitFailure)", name, code, exitFailure)
		}
		if !strings.Contains(stderr.String(), "not yet implemented") {
			t.Errorf("run(%q): stderr should say not yet implemented, got %q", name, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("run(%q): stdout should be empty, got %q", name, stdout.String())
		}
	}
}

func TestRun_ServeRejectsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"serve", "--not-a-real-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d (exitUsage) for a malformed flag, not exitFailure", code, exitUsage)
	}
}

func TestRun_EvidenceVerify_MissingArguments(t *testing.T) {
	cases := [][]string{
		{"evidence"},
		{"evidence", "verify"},
		{"evidence", "verify", "--evidence-dir", "somewhere"},
		{"evidence", "verify", "--suite", "some-suite"},
		{"evidence", "not-verify"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		if code != exitUsage {
			t.Errorf("run(%v): exit code = %d, want %d (exitUsage)", args, code, exitUsage)
		}
	}
}

func TestRun_EvidenceVerify_RealSealedBundle(t *testing.T) {
	root := t.TempDir()
	bundle, err := evidence.Create(root, "v1-01-test-suite")
	if err != nil {
		t.Fatalf("evidence.Create: %v", err)
	}
	if _, err := bundle.PutJSON("report.json", map[string]string{"status": "ok"}); err != nil {
		t.Fatalf("PutJSON: %v", err)
	}
	if _, err := bundle.Finalize(map[string]string{"suiteId": "v1-01-test-suite"}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"evidence", "verify", "--evidence-dir", root, "--suite", "v1-01-test-suite"}, &stdout, &stderr)
	if code != exitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%q", code, exitSuccess, stderr.String())
	}
	wantPrefix := "verified=" + filepath.Join(root, "v1-01-test-suite")
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), wantPrefix) {
		t.Errorf("stdout = %q, want prefix %q", stdout.String(), wantPrefix)
	}
}

func TestRun_EvidenceVerify_TamperedBundleFails(t *testing.T) {
	root := t.TempDir()
	bundle, err := evidence.Create(root, "v1-01-tamper-suite")
	if err != nil {
		t.Fatalf("evidence.Create: %v", err)
	}
	if _, err := bundle.PutJSON("report.json", map[string]string{"status": "ok"}); err != nil {
		t.Fatalf("PutJSON: %v", err)
	}
	if _, err := bundle.Finalize(map[string]string{"suiteId": "v1-01-tamper-suite"}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	tamperedFile := filepath.Join(bundle.Directory(), "report.json")
	if err := os.WriteFile(tamperedFile, []byte(`{"status":"tampered"}`), 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"evidence", "verify", "--evidence-dir", root, "--suite", "v1-01-tamper-suite"}, &stdout, &stderr)
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d (exitFailure) for a tampered bundle", code, exitFailure)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty on failure, got %q", stdout.String())
	}
}

// TestRun_AllSubcommandsAreWiredAtCompositionRootOnly is a light structural
// check on V1-01's own "wiring chỉ ở composition root" requirement: every
// name the usage text advertises must have a real handler, and vice versa,
// so the two can never drift apart silently.
func TestRun_AllSubcommandsAreWiredAtCompositionRootOnly(t *testing.T) {
	want := []string{"serve", "worker", "doctor", "definition", "evidence", "adapter"}
	if len(subcommands) != len(want) {
		t.Fatalf("subcommands has %d entries, want %d: %v", len(subcommands), len(want), subcommands)
	}
	for _, name := range want {
		if _, ok := subcommands[name]; !ok {
			t.Errorf("usage advertises %q but no handler is registered", name)
		}
		if !strings.Contains(usage, name) {
			t.Errorf("handler %q is registered but usage text does not mention it", name)
		}
	}
}

func TestExitCodesAreDistinct(t *testing.T) {
	if exitSuccess == exitFailure || exitFailure == exitUsage || exitSuccess == exitUsage {
		t.Fatalf("exit codes must be pairwise distinct: success=%d failure=%d usage=%d", exitSuccess, exitFailure, exitUsage)
	}
}

func TestMain_DoesNotHangOnStartup(t *testing.T) {
	// Guards against a future composition-root change accidentally blocking
	// on I/O (e.g. a real server listen call) before argument dispatch even
	// happens; run() itself must always return promptly for a bad/empty
	// argument list.
	done := make(chan exitCode, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		done <- run([]string{"doctor"}, &stdout, &stderr)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return within 5s for a stub subcommand")
	}
}
