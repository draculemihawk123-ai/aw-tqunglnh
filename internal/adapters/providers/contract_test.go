package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers"
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

// TestClaudeCapabilitiesProbesRealVersion proves Capabilities (V5-06)
// genuinely spawns the configured executable rather than returning a
// hardcoded constant: the fake CLI reports a clearly synthetic version
// (providers.FakeCLIVersion) that no adapter source file could produce by
// accident, so a match here is only possible via a real probe.
func TestClaudeCapabilitiesProbesRealVersion(t *testing.T) {
	t.Parallel()

	adapter, err := claude.New(processadapter.NewSupervisor(), claude.Config{
		Executable:         os.Args[0],
		PrefixArgs:         helperPrefix("claude"),
		VersionEnvironment: map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := adapter.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	want := providers.FakeCLIVersion("claude")
	if capabilities.TestedCLIVersion != want {
		t.Fatalf("TestedCLIVersion = %q, want %q (a live probe result, not a hardcoded constant)", capabilities.TestedCLIVersion, want)
	}
}

// TestClaudeCapabilitiesFailsClosedWhenProbeFails proves a probe failure
// (here: the configured executable does not exist) fails Capabilities
// itself closed rather than falling back to a stale or guessed version.
func TestClaudeCapabilitiesFailsClosedWhenProbeFails(t *testing.T) {
	t.Parallel()

	adapter, err := claude.New(processadapter.NewSupervisor(), claude.Config{
		Executable: filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Capabilities(context.Background()); err == nil {
		t.Fatal("expected Capabilities to fail closed when the configured executable cannot be probed")
	}
}

// TestCodexCapabilitiesProbesRealVersion is TestClaudeCapabilitiesProbesRealVersion's
// own mirror for Codex (V5-07 applies the identical versionprobe shape).
func TestCodexCapabilitiesProbesRealVersion(t *testing.T) {
	t.Parallel()

	adapter, err := codex.New(processadapter.NewSupervisor(), codex.Config{
		Executable:         os.Args[0],
		PrefixArgs:         helperPrefix("codex"),
		VersionEnvironment: map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := adapter.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	want := providers.FakeCLIVersion("codex")
	if capabilities.TestedCLIVersion != want {
		t.Fatalf("TestedCLIVersion = %q, want %q (a live probe result, not a hardcoded constant)", capabilities.TestedCLIVersion, want)
	}
}

// TestCodexCapabilitiesFailsClosedWhenProbeFails mirrors
// TestClaudeCapabilitiesFailsClosedWhenProbeFails for Codex.
func TestCodexCapabilitiesFailsClosedWhenProbeFails(t *testing.T) {
	t.Parallel()

	adapter, err := codex.New(processadapter.NewSupervisor(), codex.Config{
		Executable: filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Capabilities(context.Background()); err == nil {
		t.Fatal("expected Capabilities to fail closed when the configured executable cannot be probed")
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

// TestAgentExecutorTerminalOutcomeMarker exercises V5-08B's own terminal
// <agentkit-outcome> marker contract (ports.AgentProposedOutcome's own doc
// comment, confirmed with the user 2026-09-09) against both real
// normalizers via the fake CLI — a valid marker in the allowed set is
// reported; no marker at all leaves the proposal nil (deriving one when
// exactly one outcome is allowed is the bridge's own job, never the
// adapter's); an out-of-range, malformed, or duplicate marker is rejected
// as a protocol error, exactly like a missing terminal event already is.
func TestAgentExecutorTerminalOutcomeMarker(t *testing.T) {
	t.Parallel()

	for _, testCase := range providerCases() {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			executor := testCase.newExecutor(t, processadapter.NewSupervisor())

			t.Run("valid marker reported", func(t *testing.T) {
				t.Parallel()
				request := helperRequestWithOutcome(
					"outcome-valid-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"),
					"outcome-success", "pass", []string{"pass", "rework"},
				)
				request.Sandbox = testCase.startSandbox
				events := &eventCollector{}
				result, err := executor.Start(context.Background(), request, events)
				if err != nil {
					t.Fatalf("start: %v", err)
				}
				if result.Status != ports.AgentExecutionSucceeded {
					t.Fatalf("status = %v, want Succeeded", result.Status)
				}
				if result.ProposedOutcome == nil || result.ProposedOutcome.Value != "pass" ||
					result.ProposedOutcome.Source != ports.AgentOutcomeReportedByProvider || result.ProposedOutcome.SchemaVersion != 1 {
					t.Fatalf("ProposedOutcome = %+v, want a valid reported %q proposal", result.ProposedOutcome, "pass")
				}
				for _, event := range events.snapshot() {
					if event.Kind == ports.AgentEventAssistantMessage && strings.Contains(event.Message, "agentkit-outcome") {
						t.Fatalf("assistant message %q still contains the raw marker — it must be stripped before persisting", event.Message)
					}
				}
			})

			t.Run("no marker leaves proposal nil", func(t *testing.T) {
				t.Parallel()
				request := helperRequestWithOutcome(
					"outcome-absent-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"),
					"success", "", []string{"only-outcome"},
				)
				request.Sandbox = testCase.startSandbox
				result, err := executor.Start(context.Background(), request, &eventCollector{})
				if err != nil {
					t.Fatalf("start: %v", err)
				}
				if result.Status != ports.AgentExecutionSucceeded || result.ProposedOutcome != nil {
					t.Fatalf("result = %+v, want Succeeded with a nil ProposedOutcome (the adapter never derives one — that is the bridge's own job)", result)
				}
			})

			t.Run("outcome not in allowed set is rejected", func(t *testing.T) {
				t.Parallel()
				request := helperRequestWithOutcome(
					"outcome-outofrange-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"),
					"outcome-success", "pass", []string{"rework"},
				)
				request.Sandbox = testCase.startSandbox
				result, err := executor.Start(context.Background(), request, &eventCollector{})
				assertRejectedOutcomeMarker(t, testCase, result, err)
			})

			t.Run("malformed marker is rejected", func(t *testing.T) {
				t.Parallel()
				request := helperRequestWithOutcome(
					"outcome-malformed-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"),
					"outcome-malformed", "", []string{"pass"},
				)
				request.Sandbox = testCase.startSandbox
				result, err := executor.Start(context.Background(), request, &eventCollector{})
				assertRejectedOutcomeMarker(t, testCase, result, err)
			})

			t.Run("duplicate marker is rejected", func(t *testing.T) {
				t.Parallel()
				request := helperRequestWithOutcome(
					"outcome-duplicate-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"),
					"outcome-duplicate", "pass", []string{"pass"},
				)
				request.Sandbox = testCase.startSandbox
				result, err := executor.Start(context.Background(), request, &eventCollector{})
				assertRejectedOutcomeMarker(t, testCase, result, err)
			})
		})
	}
}

// assertRejectedOutcomeMarker: every wrong marker (outcome outside the allowed
// set, duplicate, malformed) is a provider protocol error (errors.Is the
// adapter's ErrProtocol still holds) AND wraps ports.ErrOutcomeMarkerRejected
// (V9-03), which the bridge maps to OUTCOME_REJECTED — a wrong answer from
// the agent, not an unreachable provider.
func assertRejectedOutcomeMarker(t *testing.T, testCase providerCase, result ports.AgentExecutionResult, err error) {
	t.Helper()
	if err == nil || !testCase.isProtocolError(err) {
		t.Fatalf("outcome marker error = %v, want provider protocol error", err)
	}
	if !errors.Is(err, ports.ErrOutcomeMarkerRejected) {
		t.Fatalf("outcome marker error = %v, want it to wrap ports.ErrOutcomeMarkerRejected", err)
	}
	if result.Status != ports.AgentExecutionFailed || result.TerminationReason != "outcome_marker_invalid" || result.ProposedOutcome != nil {
		t.Fatalf("rejected outcome marker result = %+v", result)
	}
}

// TestAgentExecutorProtocolFailuresThatAreNotTheAgentsOutcomeMarker is the
// contrast to TestAgentExecutorTerminalOutcomeMarker (V9-03): a stream that
// never delivers its terminal event, or is not even valid JSONL, is a genuine
// protocol failure of the provider side. It stays a plain ErrProtocol and must
// NOT carry ports.ErrOutcomeMarkerRejected, or the bridge would call a broken
// provider an agent's wrong answer.
func TestAgentExecutorProtocolFailuresThatAreNotTheAgentsOutcomeMarker(t *testing.T) {
	t.Parallel()

	for _, testCase := range providerCases() {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			executor := testCase.newExecutor(t, processadapter.NewSupervisor())
			for _, mode := range []struct{ name, helperMode, wantReason string }{
				{"missing terminal event", "no-terminal", "protocol_incomplete"},
				{"malformed JSONL", "malformed", "protocol_error"},
			} {
				mode := mode
				t.Run(mode.name, func(t *testing.T) {
					t.Parallel()
					// Several outcomes are allowed: the absence of a marker is
					// not what is being tested here.
					request := helperRequestWithOutcome(
						"not-marker-"+mode.helperMode+"-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"),
						mode.helperMode, "pass", []string{"pass", "rework"},
					)
					request.Sandbox = testCase.startSandbox
					result, err := executor.Start(context.Background(), request, &eventCollector{})
					if err == nil || !testCase.isProtocolError(err) {
						t.Fatalf("error = %v, want a provider protocol error", err)
					}
					if errors.Is(err, ports.ErrOutcomeMarkerRejected) {
						t.Fatalf("error = %v: a broken stream must not be reported as a rejected outcome marker", err)
					}
					if result.Status != ports.AgentExecutionFailed || result.TerminationReason != mode.wantReason {
						t.Fatalf("result = %+v, want FAILED with termination reason %q", result, mode.wantReason)
					}
				})
			}
		})
	}
}

func helperRequestWithOutcome(
	attemptID string, workingDirectory string, capturePath string, mode string, outcome string, allowedOutcomes []string,
) ports.AgentExecutionRequest {
	request := helperRequest(attemptID, workingDirectory, capturePath, mode)
	request.AllowedOutcomes = allowedOutcomes
	request.Environment["AGENTKIT_HELPER_OUTCOME"] = outcome
	return request
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
					Executable:         os.Args[0],
					PrefixArgs:         helperPrefix("codex"),
					VersionEnvironment: map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"},
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
					Executable:         os.Args[0],
					PrefixArgs:         helperPrefix("claude"),
					PermissionMode:     "dontAsk",
					VersionEnvironment: map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"},
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

func readCapture(t *testing.T, path string) providers.FakeCLIInvocation {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read invocation capture: %v", err)
	}
	var capture providers.FakeCLIInvocation
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

// TestProviderHelperProcess is the re-invoked-test-binary path used by every
// test above (see helperPrefix): it delegates to the exact same
// providers.RunFakeProviderCLI that cmd/fake-claude and cmd/fake-codex call
// directly, so the two paths (under `go test` vs. a standalone
// agentkit-spike acceptance run) can never diverge in fixture behavior.
func TestProviderHelperProcess(t *testing.T) {
	if os.Getenv("AGENTKIT_PROVIDER_HELPER") != "1" {
		return
	}
	arguments := argumentsAfterSeparator(os.Args)
	if len(arguments) < 1 {
		os.Exit(2)
	}
	code := providers.RunFakeProviderCLI(
		arguments[0], arguments[1:],
		os.Getenv("AGENTKIT_HELPER_MODE"), os.Getenv("AGENTKIT_CAPTURE_PATH"),
		os.Stdin, os.Stdout,
	)
	_ = os.Stdout.Sync()
	os.Exit(code)
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

// V9-11 (live finding F2): a real CLI is told where the repositories are. A
// CHECKER has only read-only mounts and starts in a scratch directory; the
// first live run had its reviewer write a file of its own there and approve it.
func TestClaudeAdapterNamesTheRepositoryMountsToTheCLI(t *testing.T) {
	t.Parallel()
	supervisor := processadapter.NewSupervisor()
	adapter, err := claude.New(supervisor, claude.Config{
		Executable: os.Args[0], PrefixArgs: helperPrefix("claude"), VersionEnvironment: map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, name string, mounts []ports.AgentWorkspaceMount) providers.FakeCLIInvocation {
		t.Helper()
		scratch := t.TempDir()
		capture := filepath.Join(t.TempDir(), name+".json")
		request := helperRequest(name, scratch, capture, "success")
		request.WorkspaceMounts = mounts
		if _, err := adapter.Start(context.Background(), request, &eventCollector{}); err != nil {
			t.Fatalf("start: %v", err)
		}
		return readCapture(t, capture)
	}
	argumentAfter := func(arguments []string, flag string) []string {
		var values []string
		for i, argument := range arguments {
			if argument == flag && i+1 < len(arguments) {
				values = append(values, arguments[i+1])
			}
		}
		return values
	}

	t.Run("a checker's read-only mount is added and named", func(t *testing.T) {
		t.Parallel()
		repository := t.TempDir()
		invocation := run(t, "checker", []ports.AgentWorkspaceMount{{RepositoryID: "repo-a", WorkingDirectory: repository, Access: ports.WorkspaceReadOnly}})
		if dirs := argumentAfter(invocation.Argv, "--add-dir"); len(dirs) != 1 || dirs[0] != repository {
			t.Fatalf("--add-dir = %v, want exactly the read-only repository %s (argv %v)", dirs, repository, invocation.Argv)
		}
		notice := argumentAfter(invocation.Argv, "--append-system-prompt")
		if len(notice) != 1 || !strings.Contains(notice[0], "repository repo-a: "+repository+" (READ_ONLY)") || !strings.Contains(notice[0], "must not be changed") {
			t.Fatalf("--append-system-prompt = %q, want the repository, its path and its READ_ONLY access", notice)
		}
		if invocation.Stdin != "literal prompt && not a shell command" {
			t.Fatalf("stdin = %q: the mounts must not change the prompt", invocation.Stdin)
		}
	})

	t.Run("the working directory is not added twice but is named", func(t *testing.T) {
		t.Parallel()
		scratch := t.TempDir()
		other := t.TempDir()
		capture := filepath.Join(t.TempDir(), "maker.json")
		request := helperRequest("maker", scratch, capture, "success")
		request.WorkspaceMounts = []ports.AgentWorkspaceMount{
			{RepositoryID: "repo-a", WorkingDirectory: scratch, Access: ports.WorkspaceReadWrite},
			{RepositoryID: "repo-b", WorkingDirectory: other, Access: ports.WorkspaceReadOnly},
		}
		if _, err := adapter.Start(context.Background(), request, &eventCollector{}); err != nil {
			t.Fatalf("start: %v", err)
		}
		invocation := readCapture(t, capture)
		if dirs := argumentAfter(invocation.Argv, "--add-dir"); len(dirs) != 1 || dirs[0] != other {
			t.Fatalf("--add-dir = %v, want only the mount that is not the working directory (%s)", dirs, other)
		}
		notice := argumentAfter(invocation.Argv, "--append-system-prompt")
		if len(notice) != 1 || !strings.Contains(notice[0], "repo-a: "+scratch+" (READ_WRITE)") || !strings.Contains(notice[0], "repo-b: "+other+" (READ_ONLY)") {
			t.Fatalf("--append-system-prompt = %q, want both repositories with their access", notice)
		}
	})

	t.Run("no mounts, no arguments", func(t *testing.T) {
		t.Parallel()
		invocation := run(t, "none", nil)
		for _, flag := range []string{"--add-dir", "--append-system-prompt"} {
			if values := argumentAfter(invocation.Argv, flag); len(values) != 0 {
				t.Fatalf("%s = %v with no mounts, want none", flag, values)
			}
		}
	})
}

// V9-13a (live finding F4): the effort level and a per-attempt spend ceiling are
// Config fields, not StartArgs, so they hold for a resumed attempt too — a
// ceiling that only the first attempt of a node carried would not be one.
func TestClaudeAdapterPassesEffortAndSpendCeilingOnStartAndResume(t *testing.T) {
	t.Parallel()
	supervisor := processadapter.NewSupervisor()
	adapter, err := claude.New(supervisor, claude.Config{
		Executable: os.Args[0], PrefixArgs: helperPrefix("claude"), VersionEnvironment: map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"},
		Effort: "medium", MaxBudgetUSD: 0.75,
	})
	if err != nil {
		t.Fatal(err)
	}
	valueAfter := func(arguments []string, flag string) (string, int) {
		count, value := 0, ""
		for i, argument := range arguments {
			if argument == flag && i+1 < len(arguments) {
				count, value = count+1, arguments[i+1]
			}
		}
		return value, count
	}
	check := func(t *testing.T, arguments []string) {
		t.Helper()
		if value, count := valueAfter(arguments, "--effort"); count != 1 || value != "medium" {
			t.Errorf("--effort = %q (x%d), want medium once (argv %v)", value, count, arguments)
		}
		if value, count := valueAfter(arguments, "--max-budget-usd"); count != 1 || value != "0.75" {
			t.Errorf("--max-budget-usd = %q (x%d), want 0.75 once (argv %v)", value, count, arguments)
		}
	}

	startCapture := filepath.Join(t.TempDir(), "start.json")
	if _, err := adapter.Start(context.Background(), helperRequest("start-f4", t.TempDir(), startCapture, "success"), &eventCollector{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	check(t, readCapture(t, startCapture).Argv)

	resumeCapture := filepath.Join(t.TempDir(), "resume.json")
	if _, err := adapter.Resume(context.Background(), helperRequest("resume-f4", t.TempDir(), resumeCapture, "success"),
		ports.ProviderSessionRef{Provider: ports.ProviderClaude, SessionID: "claude-session-0001"}, &eventCollector{}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	check(t, readCapture(t, resumeCapture).Argv)

	t.Run("unset settings add no argument", func(t *testing.T) {
		t.Parallel()
		plain, err := claude.New(processadapter.NewSupervisor(), claude.Config{
			Executable: os.Args[0], PrefixArgs: helperPrefix("claude"), VersionEnvironment: map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		capture := filepath.Join(t.TempDir(), "plain.json")
		if _, err := plain.Start(context.Background(), helperRequest("plain-f4", t.TempDir(), capture, "success"), &eventCollector{}); err != nil {
			t.Fatalf("start: %v", err)
		}
		for _, argument := range readCapture(t, capture).Argv {
			if argument == "--effort" || argument == "--max-budget-usd" {
				t.Fatalf("%s present with nothing configured", argument)
			}
		}
	})
}

func TestClaudeAdapterRefusesAnUnusableEffortOrSpendCeiling(t *testing.T) {
	t.Parallel()
	for name, config := range map[string]claude.Config{
		"unknown effort":    {Effort: "ludicrous"},
		"negative ceiling":  {MaxBudgetUSD: -1},
		"not-a-number":      {MaxBudgetUSD: math.NaN()},
		"infinite ceiling":  {MaxBudgetUSD: math.Inf(1)},
		"effort wrong case": {Effort: "Medium"},
	} {
		if _, err := claude.New(processadapter.NewSupervisor(), config); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max"} {
		if _, err := claude.New(processadapter.NewSupervisor(), claude.Config{Effort: effort}); err != nil {
			t.Errorf("effort %q was refused: %v", effort, err)
		}
	}
}

// V9-20: a provider CLI that reports a failed run says why ("you've hit your
// limit"); the adapter must carry that reason in its PROVIDER_REPORTED_FAILURE
// diagnostic, bounded, so the attempt's failureDetail has something to show.
func TestAgentExecutorProviderReportedFailureCarriesTheProvidersReason(t *testing.T) {
	t.Parallel()

	for _, testCase := range providerCases() {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			executor := testCase.newExecutor(t, processadapter.NewSupervisor())
			request := helperRequest("provider-failure-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"), "provider-failure")
			request.Sandbox = testCase.startSandbox
			events := &eventCollector{}
			result, err := executor.Start(context.Background(), request, events)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if result.Status != ports.AgentExecutionFailed {
				t.Fatalf("result = %+v, want FAILED", result)
			}
			var message string
			for _, event := range events.snapshot() {
				if event.Kind == ports.AgentEventDiagnostic && event.Diagnostic != nil && event.Diagnostic.Code == ports.ProviderFailureDiagnosticCode {
					message = event.Diagnostic.Message
				}
			}
			if !strings.Contains(message, "hit your") || !strings.Contains(message, "limit") {
				t.Fatalf("provider failure diagnostic message = %q, want it to carry the provider's own reason", message)
			}
			if strings.ContainsAny(message, "\n\r\t") {
				t.Fatalf("provider failure diagnostic message = %q, want whitespace collapsed to single spaces", message)
			}
		})
	}
}
