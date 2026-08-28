package spikeacceptance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunOfflineBaselineSealsVerifiableEvidence(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	runner := &recordingRunner{result: CommandResult{
		Command: "go test -count=1 ./...", ExitCode: 0,
		Output: []byte("ok internal/spike\ncredential-value\n"),
	}}
	t.Setenv("OPENAI_API_KEY", "credential-value")
	result, err := RunOfflineBaseline(context.Background(), runner, RunRequest{
		EvidenceRoot: root, WorkingDir: t.TempDir(), GoExecutable: "go-test", Now: now, SuiteID: "offline-pass",
	})
	if err != nil {
		t.Fatalf("RunOfflineBaseline() error = %v", err)
	}
	if !result.Passed || runner.executable != "go-test" || strings.Join(runner.arguments, " ") != "test -count=1 ./..." {
		t.Fatalf("unexpected baseline result=%+v runner=%+v", result, runner)
	}
	if err := VerifySuite(root, result.SuiteID); err != nil {
		t.Fatalf("VerifySuite() error = %v", err)
	}
	log, err := os.ReadFile(filepath.Join(result.EvidenceDir, "assertions", "offline-go-test.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "credential-value") || !strings.Contains(string(log), "[REDACTED]") {
		t.Fatalf("sensitive output was persisted: %q", log)
	}
}

func TestRunOfflineBaselineKeepsSealedFailureEvidence(t *testing.T) {
	root := t.TempDir()
	runner := &recordingRunner{result: CommandResult{Command: "go test -count=1 ./...", ExitCode: 1, Output: []byte("FAIL")}, err: errors.New("exit status 1")}
	result, err := RunOfflineBaseline(context.Background(), runner, RunRequest{
		EvidenceRoot: root, WorkingDir: t.TempDir(), Now: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC), SuiteID: "offline-fail",
	})
	if err == nil || result.Passed {
		t.Fatalf("failure result=%+v error=%v, want sealed failed evidence", result, err)
	}
	if verifyErr := VerifySuite(root, result.SuiteID); verifyErr != nil {
		t.Fatalf("failed suite evidence must still verify: %v", verifyErr)
	}
}

type recordingRunner struct {
	result     CommandResult
	err        error
	executable string
	arguments  []string
	directory  string
}

func (r *recordingRunner) Run(_ context.Context, executable string, arguments []string, directory string) (CommandResult, error) {
	r.executable = executable
	r.arguments = append([]string(nil), arguments...)
	r.directory = directory
	return r.result, r.err
}
