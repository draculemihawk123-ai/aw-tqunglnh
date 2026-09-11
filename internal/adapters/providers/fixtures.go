// Package providers holds fake-CLI fixture logic shared between this
// package's own tests (which re-invoke the compiled test binary under `go
// test`) and the standalone cmd/fake-claude and cmd/fake-codex binaries
// (which the agentkit-spike acceptance suite spawns directly, since a
// released binary has no `-test.run` support). Both paths must observe the
// exact same fake protocol, so the protocol itself lives here once.
package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// FakeCLIInvocation captures what a fake Claude/Codex CLI process observed
// for one invocation: exactly what a real CLI's argv/stdin/cwd would have
// been, so a caller can assert on exactly what the adapter sent it.
type FakeCLIInvocation struct {
	Provider         string   `json:"provider"`
	Argv             []string `json:"argv"`
	Stdin            string   `json:"stdin"`
	WorkingDirectory string   `json:"workingDirectory"`
}

// FakeCLIVersion is what RunFakeProviderCLI reports for a "--version"
// invocation (V5-06) — provider-specific and clearly synthetic, so a
// contract test asserting on it can never be confused with either
// adapter's own real, hardcoded AdapterVersion/ProtocolVersion constants.
func FakeCLIVersion(provider string) string {
	return "1.0.0-fake+" + provider
}

// RunFakeProviderCLI plays one of the two fake provider CLIs' wire protocol
// for exactly one invocation and returns the process exit code the caller
// should exit with.
//
//   - provider is "claude" or "codex".
//   - arguments is the CLI's own argv, already stripped of any test-runner
//     prefix by the caller.
//   - mode selects the fixture behavior: "success" (normal completed turn),
//     "malformed" (invalid JSONL, to prove the adapter surfaces a protocol
//     error), "cancel" (emit a start event, then hang until killed), or
//     "invalid-session" (fail only if arguments look like a resume — see
//     IsResumeInvocation — so SPK-12 can prove Resume was never called: a
//     Start invocation on the same fake CLI still succeeds normally).
//   - capturePath, if non-empty, receives a FakeCLIInvocation as JSON.
//
// A "--version" invocation (V5-06's own capability/version probe) is
// checked FIRST, before any capture/mode handling: a real CLI answers a
// version query the same way regardless of task state, and this fixture
// must too.
func RunFakeProviderCLI(provider string, arguments []string, mode string, capturePath string, stdin io.Reader, stdout io.Writer) int {
	if len(arguments) == 1 && arguments[0] == "--version" {
		fmt.Fprintln(stdout, FakeCLIVersion(provider))
		return 0
	}

	input, _ := io.ReadAll(stdin)
	workingDirectory, _ := os.Getwd()
	// AGENTKIT_HELPER_WRITE_PATH (V5-15D): the one real, opt-in filesystem
	// side effect this fixture ever performs beyond its own capturePath —
	// every other mode only ever emits protocol JSONL over stdout, with
	// zero effect on any real repository mount. A caller that needs a real
	// AGENT process to really mutate a real path (V5-15D's own "checker
	// write attempt" scenario, proving a CHECKER-role AGENT's real
	// spawned write against its own real, forced-read-only mount is really
	// rejected by validateStrictlyReadOnlyDiffs) sets this env var to that
	// real absolute path via InheritedEnvironment/t.Setenv — production
	// code never sets ports.AgentExecutionRequest.Environment for AGENT at
	// all (confirmed by reading assemble_execution_request.go), so this
	// can never fire outside a test that deliberately opts in.
	if writePath := os.Getenv("AGENTKIT_HELPER_WRITE_PATH"); writePath != "" {
		_ = os.WriteFile(writePath, []byte("mutated by fake CLI\n"), 0o600)
	}
	if capturePath != "" {
		capture := FakeCLIInvocation{Provider: provider, Argv: arguments, Stdin: string(input), WorkingDirectory: workingDirectory}
		captureContent, err := json.Marshal(capture)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
		if err := os.WriteFile(capturePath, captureContent, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
	}

	if mode == "malformed" {
		fmt.Fprintln(stdout, `{"type":`)
		return 0
	}
	if mode == "invalid-session" && IsResumeInvocation(provider, arguments) {
		// A real CLI whose session expired server-side still runs to
		// completion and reports a failed turn — it does not crash. SPK-12
		// is about the orchestrator never calling Resume in the first
		// place, not about surviving a process crash.
		if !writeProviderFailedTurn(stdout, provider) {
			return 4
		}
		return 0
	}
	if !writeProviderStarted(stdout, provider) {
		return 4
	}
	if mode == "cancel" {
		time.Sleep(30 * time.Second)
		return 0
	}
	// V5-08B (confirmed with the user 2026-09-09): outcome-marker fixture
	// modes exercise claude.go/codex.go's own terminal <agentkit-outcome>
	// parsing without needing a real CLI — see this function's own doc
	// comment for the mode vocabulary these three branches implement.
	switch mode {
	case "outcome-success":
		if !writeProviderSuccessWithFinalText(stdout, provider, "done"+OutcomeMarker(os.Getenv("AGENTKIT_HELPER_OUTCOME"))) {
			return 4
		}
		return 0
	case "outcome-malformed":
		if !writeProviderSuccessWithFinalText(stdout, provider, "done<agentkit-outcome>{not-json</agentkit-outcome>") {
			return 4
		}
		return 0
	case "outcome-duplicate":
		if !writeProviderDuplicateOutcome(stdout, provider, os.Getenv("AGENTKIT_HELPER_OUTCOME")) {
			return 4
		}
		return 0
	}
	if !writeProviderSuccess(stdout, provider) {
		return 4
	}
	return 0
}

// OutcomeMarker builds the exact terminal marker text claude.go/codex.go's
// own extractOutcomeMarker expects (V5-08B) — exported so a caller
// composing its own fixture text outside this package (should one ever
// exist) never has to hand-duplicate the wire format.
func OutcomeMarker(outcome string) string {
	encoded, _ := json.Marshal(outcome)
	return `<agentkit-outcome>{"schemaVersion":1,"outcome":` + string(encoded) + `}</agentkit-outcome>`
}

// IsResumeInvocation inspects a fake CLI's own argv for the resume-shaped
// flags each real adapter emits (see codex.go/claude.go), so invalid-session
// mode can fail only resume attempts and leave Start attempts on the same
// fake CLI succeeding normally.
func IsResumeInvocation(provider string, arguments []string) bool {
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
func writeProviderFailedTurn(stdout io.Writer, provider string) bool {
	switch provider {
	case "codex":
		fmt.Fprintln(stdout, `{"type":"thread.started","thread_id":"codex-session-0001"}`)
		fmt.Fprintln(stdout, `{"type":"turn.failed"}`)
		return true
	case "claude":
		fmt.Fprintln(stdout, `{"type":"system","subtype":"init","session_id":"claude-session-0001"}`)
		fmt.Fprintln(stdout, `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"claude-session-0001","usage":{"input_tokens":1,"output_tokens":1}}`)
		return true
	default:
		return false
	}
}

func writeProviderStarted(stdout io.Writer, provider string) bool {
	switch provider {
	case "codex":
		fmt.Fprintln(stdout, `{"type":"thread.started","thread_id":"codex-session-0001"}`)
		fmt.Fprintln(stdout, `{"type":"turn.started"}`)
		return true
	case "claude":
		fmt.Fprintln(stdout, `{"type":"system","subtype":"init","session_id":"claude-session-0001"}`)
		return true
	default:
		return false
	}
}

func writeProviderSuccess(stdout io.Writer, provider string) bool {
	return writeProviderSuccessWithFinalText(stdout, provider, "done")
}

// writeProviderSuccessWithFinalText is writeProviderSuccess parameterized
// by the final assistant/agent_message text (V5-08B) — the same tool-call
// exchange either way, only the text a terminal <agentkit-outcome> marker
// would be appended to ever changes.
func writeProviderSuccessWithFinalText(stdout io.Writer, provider string, finalText string) bool {
	switch provider {
	case "codex":
		fmt.Fprintln(stdout, `{"type":"item.started","item":{"id":"tool-1","type":"command_execution","command":"git status","status":"in_progress","exit_code":null}}`)
		fmt.Fprintln(stdout, `{"type":"item.completed","item":{"id":"tool-1","type":"command_execution","command":"git status","aggregated_output":"clean","status":"completed","exit_code":0}}`)
		fmt.Fprintln(stdout, jsonLine(map[string]any{"type": "item.completed", "item": map[string]any{"id": "message-1", "type": "agent_message", "text": finalText}}))
		fmt.Fprintln(stdout, `{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":3,"output_tokens":5}}`)
		return true
	case "claude":
		fmt.Fprintln(stdout, jsonLine(map[string]any{
			"type": "assistant", "session_id": "claude-session-0001",
			"message": map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": finalText},
					map[string]any{"type": "tool_use", "id": "tool-1", "name": "Bash", "input": map[string]any{"command": "git status"}},
				},
				"usage": map[string]any{"input_tokens": 12, "cache_read_input_tokens": 3, "output_tokens": 5},
			},
		}))
		fmt.Fprintln(stdout, `{"type":"user","session_id":"claude-session-0001","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"clean","is_error":false}]}}`)
		fmt.Fprintln(stdout, `{"type":"result","subtype":"success","is_error":false,"session_id":"claude-session-0001","total_cost_usd":0.01,"usage":{"input_tokens":12,"cache_read_input_tokens":3,"output_tokens":5}}`)
		return true
	default:
		return false
	}
}

// writeProviderDuplicateOutcome emits TWO separate assistant/agent_message
// texts each carrying its own valid terminal marker (V5-08B) — proves
// claude.go/codex.go reject a duplicate occurrence rather than silently
// keeping whichever one arrived last.
func writeProviderDuplicateOutcome(stdout io.Writer, provider string, outcome string) bool {
	switch provider {
	case "codex":
		fmt.Fprintln(stdout, jsonLine(map[string]any{"type": "item.completed", "item": map[string]any{"id": "message-0", "type": "agent_message", "text": "thinking" + OutcomeMarker(outcome)}}))
		fmt.Fprintln(stdout, jsonLine(map[string]any{"type": "item.completed", "item": map[string]any{"id": "message-1", "type": "agent_message", "text": "done" + OutcomeMarker(outcome)}}))
		fmt.Fprintln(stdout, `{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":3,"output_tokens":5}}`)
		return true
	case "claude":
		fmt.Fprintln(stdout, jsonLine(map[string]any{
			"type": "assistant", "session_id": "claude-session-0001",
			"message": map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "thinking" + OutcomeMarker(outcome)}},
				"usage":   map[string]any{"input_tokens": 6, "output_tokens": 2},
			},
		}))
		fmt.Fprintln(stdout, jsonLine(map[string]any{
			"type": "assistant", "session_id": "claude-session-0001",
			"message": map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "done" + OutcomeMarker(outcome)}},
				"usage":   map[string]any{"input_tokens": 6, "output_tokens": 3},
			},
		}))
		fmt.Fprintln(stdout, `{"type":"result","subtype":"success","is_error":false,"session_id":"claude-session-0001","total_cost_usd":0.01,"usage":{"input_tokens":12,"cache_read_input_tokens":3,"output_tokens":5}}`)
		return true
	default:
		return false
	}
}

func jsonLine(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
