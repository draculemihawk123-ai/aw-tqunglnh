package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func TestSupervisorRunsExecutableWithoutShell(t *testing.T) {
	t.Parallel()

	workingDirectory := t.TempDir()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	literalArgument := "literal && never-run | still-one-argument"

	result, err := NewSupervisor().Run(context.Background(), ports.ProcessSpec{
		ID:               "direct-argv",
		Executable:       os.Args[0],
		Argv:             []string{"-test.run=TestProcessHelper", "--", "echo", literalArgument},
		WorkingDirectory: workingDirectory,
		Environment: map[string]string{
			"AGENTKIT_PROCESS_HELPER": "1",
			"EXPLICIT_VALUE":          "kept",
		},
		Stdin:   []byte("prompt from stdin"),
		Timeout: 5 * time.Second,
	}, stdout, stderr)
	if err != nil {
		t.Fatalf("run helper: %v", err)
	}
	if result.ExitCode != 0 || result.TimedOut || result.Cancelled || result.OutputTruncated {
		t.Fatalf("unexpected process result: %+v", result)
	}

	var capture helperCapture
	if err := json.Unmarshal(stdout.Bytes(), &capture); err != nil {
		t.Fatalf("decode helper output %q: %v", stdout.String(), err)
	}
	if len(capture.Argv) != 2 || capture.Argv[1] != literalArgument {
		t.Fatalf("argv was not preserved: %#v", capture.Argv)
	}
	if capture.Stdin != "prompt from stdin" || capture.ExplicitValue != "kept" {
		t.Fatalf("stdin or environment changed: %+v", capture)
	}
	wantDirectory, _ := filepath.EvalSymlinks(workingDirectory)
	gotDirectory, _ := filepath.EvalSymlinks(capture.WorkingDirectory)
	if !strings.EqualFold(gotDirectory, wantDirectory) {
		t.Fatalf("working directory = %q, want %q", gotDirectory, wantDirectory)
	}
	if !strings.Contains(stderr.String(), "helper diagnostic") {
		t.Fatalf("stderr was not kept separate: %q", stderr.String())
	}
}

func TestSupervisorTimesOutProcess(t *testing.T) {
	t.Parallel()

	result, err := NewSupervisor().Run(context.Background(), helperSpec("timeout", 60*time.Millisecond), nil, nil)
	if err != nil {
		t.Fatalf("run timeout helper: %v", err)
	}
	if !result.TimedOut || result.Cancelled {
		t.Fatalf("unexpected timeout result: %+v", result)
	}
}

func TestSupervisorCancelsActiveProcess(t *testing.T) {
	t.Parallel()

	supervisor := NewSupervisor()
	ready := newNotifyingWriter("ready")
	resultChannel := make(chan ports.ProcessResult, 1)
	errorChannel := make(chan error, 1)

	go func() {
		result, err := supervisor.Run(context.Background(), helperSpec("cancel", 5*time.Second), ready, nil)
		resultChannel <- result
		errorChannel <- err
	}()

	select {
	case <-ready.notified:
	case <-time.After(2 * time.Second):
		t.Fatal("helper did not become ready")
	}
	if err := supervisor.Cancel(context.Background(), "cancel"); err != nil {
		t.Fatalf("cancel active helper: %v", err)
	}
	result := <-resultChannel
	if err := <-errorChannel; err != nil {
		t.Fatalf("cancelled run returned infrastructure error: %v", err)
	}
	if !result.Cancelled || result.TimedOut {
		t.Fatalf("unexpected cancellation result: %+v", result)
	}
	if err := supervisor.Cancel(context.Background(), "cancel"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("cancel completed process error = %v, want ErrNotRunning", err)
	}
}

func TestSupervisorCancelKillsDescendantProcess(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "descendant-alive")
	supervisor := NewSupervisor()
	ready := newNotifyingWriter("ready")
	resultChannel := make(chan ports.ProcessResult, 1)
	errorChannel := make(chan error, 1)

	spec := helperSpec("descendant", 5*time.Second)
	spec.Environment["AGENTKIT_DESCENDANT_MARKER"] = marker

	go func() {
		result, err := supervisor.Run(context.Background(), spec, ready, nil)
		resultChannel <- result
		errorChannel <- err
	}()

	select {
	case <-ready.notified:
	case <-time.After(2 * time.Second):
		t.Fatal("helper did not become ready")
	}
	waitForFile(t, marker, 2*time.Second)

	if err := supervisor.Cancel(context.Background(), spec.ID); err != nil {
		t.Fatalf("cancel active helper: %v", err)
	}
	result := <-resultChannel
	if err := <-errorChannel; err != nil {
		t.Fatalf("cancelled run returned infrastructure error: %v", err)
	}
	if !result.Cancelled {
		t.Fatalf("unexpected cancellation result: %+v", result)
	}

	lastMod := modTime(t, marker)
	time.Sleep(300 * time.Millisecond)
	if modTime(t, marker) != lastMod {
		t.Fatal("descendant process kept writing its heartbeat after the parent was cancelled — it was not terminated")
	}
}

func TestSupervisorBoundsOversizedOutput(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	const limit = 1024
	spec := helperSpec("flood", 5*time.Second)
	spec.OutputLimitBytes = limit

	result, err := NewSupervisor().Run(context.Background(), spec, &stdout, nil)
	if err != nil {
		t.Fatalf("run flood helper: %v", err)
	}
	if !result.OutputTruncated {
		t.Fatalf("expected OutputTruncated=true, result: %+v", result)
	}
	if stdout.Len() > limit {
		t.Fatalf("stdout.Len() = %d, want <= %d", stdout.Len(), limit)
	}
}

func TestSupervisorRejectsRelativeWorkingDirectory(t *testing.T) {
	t.Parallel()

	spec := helperSpec("echo", time.Second)
	spec.WorkingDirectory = "relative/path"
	result, err := NewSupervisor().Run(context.Background(), spec, nil, nil)
	if err == nil {
		t.Fatal("expected an error for a relative working directory")
	}
	if !result.StartedAt.IsZero() {
		t.Fatalf("process must not have been started, StartedAt = %v", result.StartedAt)
	}
}

func TestSupervisorRejectsMissingWorkingDirectory(t *testing.T) {
	t.Parallel()

	spec := helperSpec("echo", time.Second)
	spec.WorkingDirectory = filepath.Join(t.TempDir(), "does-not-exist")
	result, err := NewSupervisor().Run(context.Background(), spec, nil, nil)
	if err == nil {
		t.Fatal("expected an error for a missing working directory")
	}
	if !result.StartedAt.IsZero() {
		t.Fatalf("process must not have been started, StartedAt = %v", result.StartedAt)
	}
}

func TestSupervisorRejectsFileAsWorkingDirectory(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	spec := helperSpec("echo", time.Second)
	spec.WorkingDirectory = file
	result, err := NewSupervisor().Run(context.Background(), spec, nil, nil)
	if err == nil {
		t.Fatal("expected an error when the working directory is a file")
	}
	if !result.StartedAt.IsZero() {
		t.Fatalf("process must not have been started, StartedAt = %v", result.StartedAt)
	}
}

func helperSpec(mode string, timeout time.Duration) ports.ProcessSpec {
	return ports.ProcessSpec{
		ID:               ports.ProcessID(mode),
		Executable:       os.Args[0],
		Argv:             []string{"-test.run=TestProcessHelper", "--", mode},
		WorkingDirectory: os.TempDir(),
		Environment:      map[string]string{"AGENTKIT_PROCESS_HELPER": "1"},
		Timeout:          timeout,
		GracePeriod:      300 * time.Millisecond,
	}
}

type helperCapture struct {
	Argv             []string `json:"argv"`
	WorkingDirectory string   `json:"workingDirectory"`
	Stdin            string   `json:"stdin"`
	ExplicitValue    string   `json:"explicitValue"`
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("AGENTKIT_PROCESS_HELPER") != "1" {
		return
	}
	arguments := argumentsAfterSeparator(os.Args)
	if len(arguments) == 0 {
		os.Exit(2)
	}

	switch arguments[0] {
	case "echo":
		input := &bytes.Buffer{}
		_, _ = input.ReadFrom(os.Stdin)
		workingDirectory, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(helperCapture{
			Argv:             arguments,
			WorkingDirectory: workingDirectory,
			Stdin:            input.String(),
			ExplicitValue:    os.Getenv("EXPLICIT_VALUE"),
		})
		_, _ = fmt.Fprintln(os.Stderr, "helper diagnostic")
	case "timeout":
		time.Sleep(5 * time.Second)
	case "cancel":
		_, _ = fmt.Fprintln(os.Stdout, "ready")
		_ = os.Stdout.Sync()
		time.Sleep(5 * time.Second)
	case "descendant":
		marker := os.Getenv("AGENTKIT_DESCENDANT_MARKER")
		child := exec.Command(os.Args[0], "-test.run=TestProcessHelper", "--", "descendant-child", marker)
		child.Env = append(os.Environ(), "AGENTKIT_PROCESS_HELPER=1")
		if err := child.Start(); err != nil {
			os.Exit(4)
		}
		_, _ = fmt.Fprintln(os.Stdout, "ready")
		_ = os.Stdout.Sync()
		_ = child.Wait()
	case "descendant-child":
		if len(arguments) < 2 {
			os.Exit(5)
		}
		marker := arguments[1]
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			_ = os.WriteFile(marker, []byte(time.Now().String()), 0o600)
			time.Sleep(20 * time.Millisecond)
		}
	case "flood":
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for i := 0; i < 64; i++ {
			_, _ = os.Stdout.Write(chunk)
		}
	default:
		os.Exit(3)
	}
	os.Exit(0)
}

func argumentsAfterSeparator(arguments []string) []string {
	for index, argument := range arguments {
		if argument == "--" {
			return arguments[index+1:]
		}
	}
	return nil
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear within %s", path, timeout)
}

func modTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.ModTime()
}

type notifyingWriter struct {
	mu       sync.Mutex
	buffer   strings.Builder
	needle   string
	notified chan struct{}
	once     sync.Once
}

func newNotifyingWriter(needle string) *notifyingWriter {
	return &notifyingWriter{needle: needle, notified: make(chan struct{})}
}

func (w *notifyingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.buffer.Write(data)
	found := strings.Contains(w.buffer.String(), w.needle)
	w.mu.Unlock()
	if found {
		w.once.Do(func() { close(w.notified) })
	}
	return len(data), nil
}
