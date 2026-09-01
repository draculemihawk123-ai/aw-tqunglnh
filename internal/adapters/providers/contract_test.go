package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestAgentExecutorContractStartAndResume(t *testing.T) {
	t.Parallel()

	for _, testCase := range providerCases() {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			workingDirectory := t.TempDir()
			executor := testCase.newExecutor(t, processadapter.NewSupervisor())

			capabilities, err := executor.Capabilities(context.Background())
			if err != nil {
				t.Fatalf("capabilities: %v", err)
			}
			if capabilities.Provider != testCase.provider || !capabilities.SupportsStart ||
				!capabilities.SupportsResume || !capabilities.SupportsCancel {
				t.Fatalf("incomplete capabilities: %+v", capabilities)
			}

			startCapture := filepath.Join(t.TempDir(), "start.json")
			startRequest := helperRequest("start-"+testCase.name, workingDirectory, startCapture, "success")
			startRequest.Sandbox = testCase.startSandbox
			startEvents := &eventCollector{}
			startResult, err := executor.Start(context.Background(), startRequest, startEvents)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			assertSuccessfulResult(t, startResult, testCase.provider, testCase.sessionID)
			assertCanonicalEvents(t, startEvents.snapshot(), startRequest.AttemptID)
			startInvocation := readCapture(t, startCapture)
			if startInvocation.Stdin != startRequest.Prompt {
				t.Fatalf("start stdin = %q, want %q", startInvocation.Stdin, startRequest.Prompt)
			}
			testCase.assertStartArgs(t, startInvocation.Argv, workingDirectory)

			resumeCapture := filepath.Join(t.TempDir(), "resume.json")
			resumeRequest := helperRequest("resume-"+testCase.name, workingDirectory, resumeCapture, "success")
			resumeRequest.Prompt = "continue from canonical checkpoint"
			resumeEvents := &eventCollector{}
			resumeResult, err := executor.Resume(context.Background(), resumeRequest, ports.ProviderSessionRef{
				Provider:  testCase.provider,
				SessionID: testCase.sessionID,
			}, resumeEvents)
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			assertSuccessfulResult(t, resumeResult, testCase.provider, testCase.sessionID)
			assertCanonicalEvents(t, resumeEvents.snapshot(), resumeRequest.AttemptID)
			resumeInvocation := readCapture(t, resumeCapture)
			if resumeInvocation.Stdin != resumeRequest.Prompt {
				t.Fatalf("resume stdin = %q, want %q", resumeInvocation.Stdin, resumeRequest.Prompt)
			}
			testCase.assertResumeArgs(t, resumeInvocation.Argv, testCase.sessionID)
		})
	}
}

func TestAgentExecutorRejectsMalformedJSONL(t *testing.T) {
	t.Parallel()

	for _, testCase := range providerCases() {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			executor := testCase.newExecutor(t, processadapter.NewSupervisor())
			request := helperRequest(
				"malformed-"+testCase.name,
				t.TempDir(),
				filepath.Join(t.TempDir(), "malformed.json"),
				"malformed",
			)
			request.Sandbox = testCase.startSandbox
			result, err := executor.Start(context.Background(), request, &eventCollector{})
			if err == nil || !testCase.isProtocolError(err) {
				t.Fatalf("malformed error = %v, want provider protocol error", err)
			}
			if result.Status != ports.AgentExecutionFailed || result.TerminationReason != "protocol_error" {
				t.Fatalf("malformed result = %+v", result)
			}
		})
	}
}

func TestAgentExecutorCancelsRealChildProcess(t *testing.T) {
	t.Parallel()

	for _, testCase := range providerCases() {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			executor := testCase.newExecutor(t, processadapter.NewSupervisor())
			request := helperRequest(
				"cancel-"+testCase.name,
				t.TempDir(),
				filepath.Join(t.TempDir(), "cancel.json"),
				"cancel",
			)
			request.Sandbox = testCase.startSandbox
			events := newSignallingCollector(ports.AgentEventExecutionStarted)
			resultChannel := make(chan ports.AgentExecutionResult, 1)
			errorChannel := make(chan error, 1)
			go func() {
				result, err := executor.Start(context.Background(), request, events)
				resultChannel <- result
				errorChannel <- err
			}()

			select {
			case <-events.signalled:
			case <-time.After(3 * time.Second):
				t.Fatal("fake provider did not emit its start event")
			}
			if err := executor.Cancel(context.Background(), request.AttemptID); err != nil {
				t.Fatalf("cancel: %v", err)
			}
			result := <-resultChannel
			if err := <-errorChannel; err != nil {
				t.Fatalf("cancelled execution returned adapter error: %v", err)
			}
			if result.Status != ports.AgentExecutionCancelled || result.TerminationReason != "cancelled" {
				t.Fatalf("cancel result = %+v", result)
			}
			if !containsKind(events.snapshot(), ports.AgentEventExecutionFinished) {
				t.Fatal("cancelled execution did not emit canonical completion")
			}
		})
	}
}

type providerCase struct {
	name             string
	provider         ports.ProviderKey
	sessionID        string
	startSandbox     string
	newExecutor      func(*testing.T, ports.ProcessSupervisor) ports.AgentExecutor
	assertStartArgs  func(*testing.T, []string, string)
	assertResumeArgs func(*testing.T, []string, string)
	isProtocolError  func(error) bool
}

func providerCases() []providerCase {
	return []providerCase{
		{
			name:         "codex",
			provider:     ports.ProviderCodex,
			sessionID:    "codex-session-0001",
			startSandbox: "read-only",
			newExecutor: func(t *testing.T, supervisor ports.ProcessSupervisor) ports.AgentExecutor {
				t.Helper()
				adapter, err := codex.New(supervisor, codex.Config{
					Executable: os.Args[0],
					PrefixArgs: helperPrefix("codex"),
				})
				if err != nil {
					t.Fatal(err)
				}
				return adapter
			},
			assertStartArgs: func(t *testing.T, arguments []string, workingDirectory string) {
				t.Helper()
				assertArgumentSequence(t, arguments, []string{"exec", "--json", "--cd", workingDirectory})
				assertArgumentSequence(t, arguments, []string{"--sandbox", "read-only"})
				if len(arguments) == 0 || arguments[len(arguments)-1] != "-" {
					t.Fatalf("Codex start must read prompt from stdin: %#v", arguments)
				}
			},
			assertResumeArgs: func(t *testing.T, arguments []string, sessionID string) {
				t.Helper()
				assertArgumentSequence(t, arguments, []string{"exec", "resume", "--json"})
				assertArgumentSequence(t, arguments, []string{sessionID, "-"})
			},
			isProtocolError: func(err error) bool { return errors.Is(err, codex.ErrProtocol) },
		},
		{
			name:      "claude",
			provider:  ports.ProviderClaude,
			sessionID: "claude-session-0001",
			newExecutor: func(t *testing.T, supervisor ports.ProcessSupervisor) ports.AgentExecutor {
				t.Helper()
				adapter, err := claude.New(supervisor, claude.Config{
					Executable:     os.Args[0],
					PrefixArgs:     helperPrefix("claude"),
					PermissionMode: "dontAsk",
				})
				if err != nil {
					t.Fatal(err)
				}
				return adapter
			},
			assertStartArgs: func(t *testing.T, arguments []string, _ string) {
				t.Helper()
				assertArgumentSequence(t, arguments, []string{"-p", "--input-format", "text", "--output-format", "stream-json", "--verbose"})
				assertArgumentSequence(t, arguments, []string{"--permission-mode", "dontAsk"})
			},
			assertResumeArgs: func(t *testing.T, arguments []string, sessionID string) {
				t.Helper()
				assertArgumentSequence(t, arguments, []string{"--resume", sessionID})
			},
			isProtocolError: func(err error) bool { return errors.Is(err, claude.ErrProtocol) },
		},
	}
}

func helperPrefix(provider string) []string {
	return []string{"-test.run=TestProviderHelperProcess", "--", provider}
}

func helperRequest(
	attemptID string,
	workingDirectory string,
	capturePath string,
	mode string,
) ports.AgentExecutionRequest {
	return ports.AgentExecutionRequest{
		AttemptID:         ports.ExecutionAttemptID(attemptID),
		ContextSnapshotID: runtime.ContextSnapshotID("context-" + attemptID),
		Prompt:            "literal prompt && not a shell command",
		WorkingDirectory:  workingDirectory,
		Environment: map[string]string{
			"AGENTKIT_PROVIDER_HELPER": "1",
			"AGENTKIT_CAPTURE_PATH":    capturePath,
			"AGENTKIT_HELPER_MODE":     mode,
		},
		Timeout: 5 * time.Second,
	}
}

func assertSuccessfulResult(
	t *testing.T,
	result ports.AgentExecutionResult,
	provider ports.ProviderKey,
	sessionID string,
) {
	t.Helper()
	if result.Provider != provider || result.Status != ports.AgentExecutionSucceeded ||
		result.TerminationReason != "completed" || result.ExitCode != 0 {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	if result.Session == nil || result.Session.Provider != provider || result.Session.SessionID != sessionID {
		t.Fatalf("unexpected session ref: %+v", result.Session)
	}
	if result.Usage.InputTokens == 0 || result.Usage.OutputTokens == 0 {
		t.Fatalf("usage was not normalized: %+v", result.Usage)
	}
}

func assertCanonicalEvents(t *testing.T, events []ports.AgentEvent, attemptID ports.ExecutionAttemptID) {
	t.Helper()
	required := []ports.AgentEventKind{
		ports.AgentEventExecutionStarted,
		ports.AgentEventAssistantMessage,
		ports.AgentEventToolCallStarted,
		ports.AgentEventToolCallFinished,
		ports.AgentEventUsageReported,
		ports.AgentEventCheckpointProposed,
		ports.AgentEventExecutionFinished,
	}
	for _, kind := range required {
		if !containsKind(events, kind) {
			t.Errorf("canonical event %s is missing from %#v", kind, eventKinds(events))
		}
	}
	for index, event := range events {
		if event.AttemptID != attemptID {
			t.Fatalf("event attempt = %q, want %q", event.AttemptID, attemptID)
		}
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event sequence at %d = %d", index, event.Sequence)
		}
		if event.ObservedAt.IsZero() {
			t.Fatalf("event %d has no timestamp", index)
		}
	}
}

func containsKind(events []ports.AgentEvent, kind ports.AgentEventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func eventKinds(events []ports.AgentEvent) []ports.AgentEventKind {
	result := make([]ports.AgentEventKind, 0, len(events))
	for _, event := range events {
		result = append(result, event.Kind)
	}
	return result
}

func assertArgumentSequence(t *testing.T, arguments, sequence []string) {
	t.Helper()
	for start := 0; start+len(sequence) <= len(arguments); start++ {
		match := true
		for offset := range sequence {
			if arguments[start+offset] != sequence[offset] {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Fatalf("argument sequence %#v is absent from %#v", sequence, arguments)
}

type invocationCapture struct {
	Provider         string   `json:"provider"`
	Argv             []string `json:"argv"`
	Stdin            string   `json:"stdin"`
	WorkingDirectory string   `json:"workingDirectory"`
}

func readCapture(t *testing.T, path string) invocationCapture {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read invocation capture: %v", err)
	}
	var capture invocationCapture
	if err := json.Unmarshal(content, &capture); err != nil {
		t.Fatalf("decode invocation capture: %v", err)
	}
	return capture
}

type eventCollector struct {
	mu        sync.Mutex
	events    []ports.AgentEvent
	want      ports.AgentEventKind
	signalled chan struct{}
	once      sync.Once
}

func newSignallingCollector(kind ports.AgentEventKind) *eventCollector {
	return &eventCollector{want: kind, signalled: make(chan struct{})}
}

func (c *eventCollector) Accept(_ context.Context, event ports.AgentEvent) error {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
	if c.signalled != nil && event.Kind == c.want {
		c.once.Do(func() { close(c.signalled) })
	}
	return nil
}

func (c *eventCollector) snapshot() []ports.AgentEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ports.AgentEvent(nil), c.events...)
}

func TestProviderHelperProcess(t *testing.T) {
	if os.Getenv("AGENTKIT_PROVIDER_HELPER") != "1" {
		return
	}
	arguments := argumentsAfterSeparator(os.Args)
	if len(arguments) < 1 {
		os.Exit(2)
	}
	provider := arguments[0]
	providerArguments := arguments[1:]
	input, _ := io.ReadAll(os.Stdin)
	workingDirectory, _ := os.Getwd()
	capture := invocationCapture{
		Provider:         provider,
		Argv:             providerArguments,
		Stdin:            string(input),
		WorkingDirectory: workingDirectory,
	}
	captureContent, _ := json.Marshal(capture)
	if err := os.WriteFile(os.Getenv("AGENTKIT_CAPTURE_PATH"), captureContent, 0o600); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}

	mode := os.Getenv("AGENTKIT_HELPER_MODE")
	if mode == "malformed" {
		_, _ = fmt.Fprintln(os.Stdout, `{"type":`)
		os.Exit(0)
	}
	if mode == "invalid-session" && isResumeInvocation(provider, providerArguments) {
		// A real CLI whose session expired server-side still runs to
		// completion and reports a failed turn — it does not crash. Exit 0
		// here on purpose: SPK-12 is about the orchestrator never calling
		// Resume in the first place, not about surviving a process crash.
		writeProviderFailedTurn(provider)
		_ = os.Stdout.Sync()
		os.Exit(0)
	}
	writeProviderStarted(provider)
	if mode == "cancel" {
		_ = os.Stdout.Sync()
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	writeProviderSuccess(provider)
	_ = os.Stdout.Sync()
	os.Exit(0)
}

// isResumeInvocation inspects the provider argv (after the "--" separator)
// for the same resume-shaped flags contract_test.go's own assertResumeArgs
// checks for, so the invalid-session mode can fail only resume attempts and
// leave Start attempts on the same fake CLI succeeding normally.
func isResumeInvocation(provider string, arguments []string) bool {
	switch provider {
	case "claude":
		for _, argument := range arguments {
			if argument == "--resume" {
				return true
			}
		}
		return false
	case "codex":
		return len(arguments) >= 2 && arguments[0] == "exec" && arguments[1] == "resume"
	default:
		return false
	}
}

// writeProviderFailedTurn emits a well-formed but failed terminal event for
// each provider's real wire protocol: Codex's "turn.failed" after
// thread.started, Claude's is_error result after system init. Both
// normalizers map this to AgentExecutionFailed/"provider_failure" — see
// codex.go and claude.go's finalStatus.
func writeProviderFailedTurn(provider string) {
	switch provider {
	case "codex":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"thread.started","thread_id":"codex-session-0001"}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"turn.failed"}`)
	case "claude":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"system","subtype":"init","session_id":"claude-session-0001"}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"claude-session-0001","usage":{"input_tokens":1,"output_tokens":1}}`)
	default:
		os.Exit(4)
	}
}

func writeProviderStarted(provider string) {
	switch provider {
	case "codex":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"thread.started","thread_id":"codex-session-0001"}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"turn.started"}`)
	case "claude":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"system","subtype":"init","session_id":"claude-session-0001"}`)
	default:
		os.Exit(4)
	}
}

func writeProviderSuccess(provider string) {
	switch provider {
	case "codex":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"item.started","item":{"id":"tool-1","type":"command_execution","command":"git status","status":"in_progress","exit_code":null}}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"item.completed","item":{"id":"tool-1","type":"command_execution","command":"git status","aggregated_output":"clean","status":"completed","exit_code":0}}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"item.completed","item":{"id":"message-1","type":"agent_message","text":"done"}}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":3,"output_tokens":5}}`)
	case "claude":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"assistant","session_id":"claude-session-0001","message":{"content":[{"type":"text","text":"done"},{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"git status"}}],"usage":{"input_tokens":12,"cache_read_input_tokens":3,"output_tokens":5}}}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"user","session_id":"claude-session-0001","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"clean","is_error":false}]}}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"result","subtype":"success","is_error":false,"session_id":"claude-session-0001","total_cost_usd":0.01,"usage":{"input_tokens":12,"cache_read_input_tokens":3,"output_tokens":5}}`)
	default:
		os.Exit(4)
	}
}

func argumentsAfterSeparator(arguments []string) []string {
	for index, argument := range arguments {
		if argument == "--" {
			return arguments[index+1:]
		}
	}
	return nil
}

var _ ports.AgentEventSink = (*eventCollector)(nil)

func TestHelperCapturePathHasNoAccidentalWhitespace(t *testing.T) {
	// A small guard around the helper environment because Windows temp paths
	// commonly contain spaces and are passed as one argv/environment value.
	path := filepath.Join(t.TempDir(), "capture with spaces.json")
	if strings.TrimSpace(path) != path {
		t.Fatalf("unexpected path whitespace: %q", path)
	}
}
