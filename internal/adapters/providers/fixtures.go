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
	if !writeProviderSuccess(stdout, provider) {
		return 4
	}
	return 0
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
	switch provider {
	case "codex":
		fmt.Fprintln(stdout, `{"type":"item.started","item":{"id":"tool-1","type":"command_execution","command":"git status","status":"in_progress","exit_code":null}}`)
		fmt.Fprintln(stdout, `{"type":"item.completed","item":{"id":"tool-1","type":"command_execution","command":"git status","aggregated_output":"clean","status":"completed","exit_code":0}}`)
		fmt.Fprintln(stdout, `{"type":"item.completed","item":{"id":"message-1","type":"agent_message","text":"done"}}`)
		fmt.Fprintln(stdout, `{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":3,"output_tokens":5}}`)
		return true
	case "claude":
		fmt.Fprintln(stdout, `{"type":"assistant","session_id":"claude-session-0001","message":{"content":[{"type":"text","text":"done"},{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"git status"}}],"usage":{"input_tokens":12,"cache_read_input_tokens":3,"output_tokens":5}}}`)
		fmt.Fprintln(stdout, `{"type":"user","session_id":"claude-session-0001","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"clean","is_error":false}]}}`)
		fmt.Fprintln(stdout, `{"type":"result","subtype":"success","is_error":false,"session_id":"claude-session-0001","total_cost_usd":0.01,"usage":{"input_tokens":12,"cache_read_input_tokens":3,"output_tokens":5}}`)
		return true
	default:
		return false
	}
}
