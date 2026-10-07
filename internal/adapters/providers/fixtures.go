// Package providers holds fake-CLI fixture logic shared between this
// package's own tests (which re-invoke the compiled test binary under `go
// test`) and the standalone cmd/fake-claude and cmd/fake-codex binaries
// (which the agentkit-spike acceptance suite spawns directly, since a
// released binary has no `-test.run` support). Both paths must observe the
// exact same fake protocol, so the protocol itself lives here once.
package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
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
	// EnvironmentSeen (V9-05) is what the process saw of the variables named
	// by AGENTKIT_HELPER_REPORT_ENV: for each one that was set, its name and
	// the SHA-256 of its value ("sha256:<hex>") — never the value itself, so a
	// capture file never holds what a test then scans the database for. A
	// name that is not in the map was not set in the process' environment.
	// Empty (and omitted) when AGENTKIT_HELPER_REPORT_ENV is not set.
	EnvironmentSeen map[string]string `json:"environmentSeen,omitempty"`
}

// FakeCLIMissingEnvExitCode is the exit code RunFakeProviderCLI returns when a
// variable named by AGENTKIT_HELPER_REQUIRE_ENV is not set in its environment.
// Distinctive on purpose: no other path of the fake CLI uses it, so a test can
// tell "the process lacked an inherited variable" from any other failure.
const FakeCLIMissingEnvExitCode = 17

// EnvironmentDigest is how the fake CLI reports a variable's value
// (FakeCLIInvocation.EnvironmentSeen): the SHA-256 of the value, so a test
// can confirm the process received exactly the value it set in the worker
// without that value ever being written to a file.
func EnvironmentDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
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
//     error), "cancel" (emit a start event, then hang until killed),
//     "no-terminal" (emit the start events, then exit 0 without a terminal
//     event), "invalid-session" (fail only if arguments look like a resume — see
//     IsResumeInvocation — so SPK-12 can prove Resume was never called: a
//     Start invocation on the same fake CLI still succeeds normally), or one
//     of the terminal-outcome modes "outcome-success", "outcome-malformed",
//     "outcome-duplicate" (V5-08B; the outcome comes from
//     AGENTKIT_HELPER_OUTCOME) and "outcome-from-prompt" (V9-03; the outcome
//     is chosen from the prompt's own taskContract.allowedOutcomes, steered by
//     AGENTKIT_HELPER_OUTCOME_PICK — see outcomePickedFromPrompt).
//   - capturePath, if non-empty, receives a FakeCLIInvocation as JSON.
//
// A "--version" invocation (V5-06's own capability/version probe) is
// checked FIRST, before any capture/mode handling: a real CLI answers a
// version query the same way regardless of task state, and this fixture
// must too.
func RunFakeProviderCLI(provider string, arguments []string, mode string, capturePath string, stdin io.Reader, stdout io.Writer) int {
	// AGENTKIT_HELPER_REQUIRE_ENV (V9-05, gap G5): a comma-separated list of
	// variable NAMES this process cannot run without — like a real provider
	// CLI that needs PATH to find its runtime or HOME for its configuration.
	// If any is missing it exits FakeCLIMissingEnvExitCode, naming the
	// variable on stderr (the name only). Checked before everything else,
	// including "--version", because that is the point: the capability probe
	// and the task run both depend on what the process inherited, so a test
	// can show that a name left out of the allow-list makes the very same
	// executable fail, and that putting it in makes it run. Opt-in: unset (the
	// default) the fake CLI needs nothing and every existing scenario is
	// untouched.
	for _, name := range envNames(os.Getenv("AGENTKIT_HELPER_REQUIRE_ENV")) {
		if _, ok := os.LookupEnv(name); !ok {
			fmt.Fprintf(os.Stderr, "fake %s CLI: required environment variable %s is not set\n", provider, name)
			return FakeCLIMissingEnvExitCode
		}
	}

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
	// AGENTKIT_HELPER_WRITE_IN_CWD (V9-01): a relative file name this process
	// (over)writes inside ITS OWN working directory. Unlike
	// AGENTKIT_HELPER_WRITE_PATH — one absolute path, hit by EVERY fake CLI
	// process that inherits the variable — this steers the write per node:
	// the engine spawns a MAKER in its repository mount's working directory
	// (a real change in the repository) and a CHECKER in an empty scratch
	// directory (a harmless write outside every mount), so one workflow can
	// have a maker that really changes a file and a checker that really
	// changes nothing in the repository.
	if name := os.Getenv("AGENTKIT_HELPER_WRITE_IN_CWD"); name != "" {
		_ = os.WriteFile(name, []byte("written by fake CLI in its working directory\n"), 0o600)
	}
	// AGENTKIT_HELPER_APPEND_PATH (V9-01): an absolute path this process
	// APPENDS one line to, so every invocation changes the file again (a
	// fixed-content write such as AGENTKIT_HELPER_WRITE_PATH's would leave a
	// second writer's output identical to the first's). It lets a test make
	// both a maker and a later checker touch the same repository file.
	if appendPath := os.Getenv("AGENTKIT_HELPER_APPEND_PATH"); appendPath != "" {
		if file, err := os.OpenFile(appendPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = fmt.Fprintf(file, "appended by fake CLI process %d\n", os.Getpid())
			_ = file.Close()
		}
	}
	if capturePath != "" {
		capture := FakeCLIInvocation{Provider: provider, Argv: arguments, Stdin: string(input), WorkingDirectory: workingDirectory}
		// AGENTKIT_HELPER_REPORT_ENV (V9-05): record, for each listed
		// variable name that is set, the SHA-256 of its value — see
		// FakeCLIInvocation.EnvironmentSeen.
		for _, name := range envNames(os.Getenv("AGENTKIT_HELPER_REPORT_ENV")) {
			if value, ok := os.LookupEnv(name); ok {
				if capture.EnvironmentSeen == nil {
					capture.EnvironmentSeen = map[string]string{}
				}
				capture.EnvironmentSeen[name] = EnvironmentDigest(value)
			}
		}
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
	if mode == "provider-failure" {
		// V9-20: a CLI that runs to completion and reports why it failed (a usage
		// limit), the way a real one does.
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
	if mode == "no-terminal" {
		// V9-03: a stream that starts and then simply ends with exit code 0 —
		// no terminal event. A genuine protocol failure of the provider side,
		// the contrast to an agent's wrong outcome marker.
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
	case "outcome-from-prompt":
		// V9-03: an agent that works out which outcomes it may report from the
		// prompt it was handed, like a real one following the instruction
		// artifact (schema v2, taskContract.allowedOutcomes), instead of being
		// told by the test through AGENTKIT_HELPER_OUTCOME.
		outcome, report, err := outcomePickedFromPrompt(input, os.Getenv("AGENTKIT_HELPER_OUTCOME_PICK"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 5
		}
		text := "done"
		if report {
			text += OutcomeMarker(outcome)
		}
		if !writeProviderSuccessWithFinalText(stdout, provider, text) {
			return 4
		}
		return 0
	}
	if !writeProviderSuccess(stdout, provider) {
		return 4
	}
	return 0
}

// envNames splits a comma-separated list of environment variable names (the
// shape of AGENTKIT_HELPER_REQUIRE_ENV / AGENTKIT_HELPER_REPORT_ENV),
// dropping blanks.
func envNames(list string) []string {
	var names []string
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// OutcomeNotListed is the outcome "outcome-from-prompt" reports for the pick
// "not-listed": a well-formed marker naming something no node declares.
const OutcomeNotListed = "outcome-not-in-the-allowed-list"

// outcomePickedFromPrompt implements the "outcome-from-prompt" mode (V9-03):
// it reads taskContract.allowedOutcomes from the instruction artifact the
// adapter wrote to stdin and chooses by pick (AGENTKIT_HELPER_OUTCOME_PICK):
//
//   - "first" (also the default), "last", or a zero-based decimal index into
//     the list: that listed outcome, reported in a marker;
//   - "not-listed": OutcomeNotListed, reported in a marker, so the allow-list
//     check can be seen to refuse an outcome the prompt never offered;
//   - "none": no marker at all.
//
// A prompt without taskContract.allowedOutcomes (instruction schema v1) is an
// error: the point of the mode is that the prompt tells the agent its choices,
// and silently guessing would hide a prompt that does not.
func outcomePickedFromPrompt(prompt []byte, pick string) (outcome string, report bool, err error) {
	var decoded struct {
		TaskContract struct {
			AllowedOutcomes []string `json:"allowedOutcomes"`
		} `json:"taskContract"`
	}
	if err := json.Unmarshal(prompt, &decoded); err != nil {
		return "", false, fmt.Errorf("outcome-from-prompt: the prompt is not an instruction artifact: %w", err)
	}
	listed := decoded.TaskContract.AllowedOutcomes
	if len(listed) == 0 {
		return "", false, fmt.Errorf("outcome-from-prompt: the prompt lists no taskContract.allowedOutcomes")
	}
	switch pick {
	case "none":
		return "", false, nil
	case "not-listed":
		return OutcomeNotListed, true, nil
	case "", "first":
		return listed[0], true, nil
	case "last":
		return listed[len(listed)-1], true, nil
	}
	index, convErr := strconv.Atoi(pick)
	if convErr != nil || index < 0 || index >= len(listed) {
		return "", false, fmt.Errorf("outcome-from-prompt: pick %q is not first, last, none, not-listed or an index below %d", pick, len(listed))
	}
	return listed[index], true, nil
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
		fmt.Fprintln(stdout, `{"type":"turn.failed","error":{"message":"You've hit your usage limit.\nTry again at 5pm."}}`)
		return true
	case "claude":
		fmt.Fprintln(stdout, `{"type":"system","subtype":"init","session_id":"claude-session-0001"}`)
		fmt.Fprintln(stdout, `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"You've hit your limit · resets 5pm","session_id":"claude-session-0001","usage":{"input_tokens":1,"output_tokens":1}}`)
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
