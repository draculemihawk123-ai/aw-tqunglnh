package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	if result.ExitCode != 0 || result.TimedOut || result.Cancelled {
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

func helperSpec(mode string, timeout time.Duration) ports.ProcessSpec {
	return ports.ProcessSpec{
		ID:               ports.ProcessID(mode),
		Executable:       os.Args[0],
		Argv:             []string{"-test.run=TestProcessHelper", "--", mode},
		WorkingDirectory: os.TempDir(),
		Environment:      map[string]string{"AGENTKIT_PROCESS_HELPER": "1"},
		Timeout:          timeout,
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
