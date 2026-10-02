package providers_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// V9-05 (gap G5): the environment a provider CLI process inherits decides
// whether it can run at all.
//
// The fake provider CLI here is a REAL process (this test binary re-invoked
// as the CLI, exactly like contract_test.go) that genuinely depends on its
// environment: AGENTKIT_HELPER_REQUIRE_ENV=PATH,HOME makes it exit
// providers.FakeCLIMissingEnvExitCode when either variable was not passed to
// it. Nothing in these tests inspects an environment slice; the only evidence
// is whether the process ran.
//
// PATH exists on every operating system. HOME does not exist on Windows, so
// each test sets it (to a temporary directory) so that the same two names mean
// the same thing on both runners. These tests do not use t.Parallel — they
// change the process environment with t.Setenv.

const requiredByTheFakeCLI = "PATH,HOME"

func useHomeVariable(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if _, ok := os.LookupEnv("PATH"); !ok {
		t.Skip("PATH is not set in this environment; the scenario needs a variable that exists on every machine")
	}
}

type probeAdapterCase struct {
	name string
	// newAdapter builds the real adapter pointed at the fake CLI, probing with
	// the given inherited allow-list.
	newAdapter func(t *testing.T, inherited []string) ports.AgentExecutor
}

func probeAdapterCases() []probeAdapterCase {
	versionEnvironment := map[string]string{
		"AGENTKIT_PROVIDER_HELPER":    "1",
		"AGENTKIT_HELPER_REQUIRE_ENV": requiredByTheFakeCLI,
	}
	return []probeAdapterCase{
		{
			name: "claude",
			newAdapter: func(t *testing.T, inherited []string) ports.AgentExecutor {
				t.Helper()
				adapter, err := claude.New(processadapter.NewSupervisor(), claude.Config{
					Executable: os.Args[0], PrefixArgs: helperPrefix("claude"),
					VersionEnvironment: versionEnvironment, VersionInheritedEnvironment: inherited,
				})
				if err != nil {
					t.Fatal(err)
				}
				return adapter
			},
		},
		{
			name: "codex",
			newAdapter: func(t *testing.T, inherited []string) ports.AgentExecutor {
				t.Helper()
				adapter, err := codex.New(processadapter.NewSupervisor(), codex.Config{
					Executable: os.Args[0], PrefixArgs: helperPrefix("codex"),
					VersionEnvironment: versionEnvironment, VersionInheritedEnvironment: inherited,
				})
				if err != nil {
					t.Fatal(err)
				}
				return adapter
			},
		},
	}
}

// TestCapabilitiesProbe_DependsOnTheInheritedEnvironment is the design's
// "thiếu PATH thì probe lỗi có kiểu; có PATH/HOME thì provider giả lập chạy":
// with nothing inherited, or with PATH but not HOME, the probe fails with a
// typed *ports.CapabilityProbeError (a non-zero exit carrying the fake CLI's
// own missing-variable exit code); with both names allowed the very same
// executable reports its version.
func TestCapabilitiesProbe_DependsOnTheInheritedEnvironment(t *testing.T) {
	useHomeVariable(t)
	for _, testCase := range probeAdapterCases() {
		t.Run(testCase.name, func(t *testing.T) {
			for name, inherited := range map[string][]string{
				"nothing inherited":     nil,
				"PATH without HOME":     {"PATH"},
				"HOME without PATH":     {"HOME"},
				"an unrelated variable": {"AW_V905_UNRELATED"},
			} {
				_, err := testCase.newAdapter(t, inherited).Capabilities(context.Background())
				var probeErr *ports.CapabilityProbeError
				if !errors.As(err, &probeErr) {
					t.Fatalf("%s: err = %v (%T), want a typed *ports.CapabilityProbeError", name, err, err)
				}
				if probeErr.Reason != ports.CapabilityProbeNonZeroExit || probeErr.ExitCode != providers.FakeCLIMissingEnvExitCode {
					t.Fatalf("%s: probeErr = %+v, want NON_ZERO_EXIT with exit code %d (the fake CLI's missing-variable exit)", name, probeErr, providers.FakeCLIMissingEnvExitCode)
				}
			}

			capabilities, err := testCase.newAdapter(t, []string{"PATH", "HOME"}).Capabilities(context.Background())
			if err != nil {
				t.Fatalf("with PATH and HOME inherited the probe must succeed: %v", err)
			}
			if want := providers.FakeCLIVersion(testCase.name); capabilities.TestedCLIVersion != want {
				t.Fatalf("TestedCLIVersion = %q, want %q", capabilities.TestedCLIVersion, want)
			}
		})
	}
}

// TestCapabilitiesProbe_InheritedEnvironmentIsOnlyForTheProbe: the probe's list
// is its own. Putting names in it must not change what Start inherits — the
// attempt's environment is formed per attempt from the request.
func TestCapabilitiesProbe_InheritedEnvironmentIsOnlyForTheProbe(t *testing.T) {
	useHomeVariable(t)
	for _, testCase := range providerCases() {
		t.Run(testCase.name, func(t *testing.T) {
			var executor ports.AgentExecutor
			switch testCase.provider {
			case ports.ProviderClaude:
				adapter, err := claude.New(processadapter.NewSupervisor(), claude.Config{
					Executable: os.Args[0], PrefixArgs: helperPrefix("claude"), PermissionMode: "dontAsk",
					VersionEnvironment:          map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"},
					VersionInheritedEnvironment: []string{"PATH", "HOME"},
				})
				if err != nil {
					t.Fatal(err)
				}
				executor = adapter
			default:
				adapter, err := codex.New(processadapter.NewSupervisor(), codex.Config{
					Executable: os.Args[0], PrefixArgs: helperPrefix("codex"),
					VersionEnvironment:          map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"},
					VersionInheritedEnvironment: []string{"PATH", "HOME"},
				})
				if err != nil {
					t.Fatal(err)
				}
				executor = adapter
			}

			request := helperRequest("probe-only-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"), "success")
			request.Sandbox = testCase.startSandbox
			request.Environment["AGENTKIT_HELPER_REQUIRE_ENV"] = requiredByTheFakeCLI
			result, err := executor.Start(context.Background(), request, &eventCollector{})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			if result.Status != ports.AgentExecutionFailed || result.ExitCode != providers.FakeCLIMissingEnvExitCode {
				t.Fatalf("result = %+v: names given only to the probe must not reach the attempt's process", result)
			}
		})
	}
}

// TestStart_DependsOnTheRequestsInheritedEnvironment is the run half: the same
// fake CLI, started through the real adapter, fails when the request's
// InheritedEnvironment leaves out a variable it needs, and runs when the
// request names both. This is the field the AGENT executor fills from the
// pinned execution profile.
func TestStart_DependsOnTheRequestsInheritedEnvironment(t *testing.T) {
	useHomeVariable(t)
	for _, testCase := range providerCases() {
		t.Run(testCase.name, func(t *testing.T) {
			executor := testCase.newExecutor(t, processadapter.NewSupervisor())

			for name, inherited := range map[string][]string{
				"nothing inherited": nil,
				"PATH without HOME": {"PATH"},
				"HOME without PATH": {"HOME"},
			} {
				request := helperRequest("env-missing-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"), "success")
				request.Sandbox = testCase.startSandbox
				request.Environment["AGENTKIT_HELPER_REQUIRE_ENV"] = requiredByTheFakeCLI
				request.InheritedEnvironment = inherited
				result, err := executor.Start(context.Background(), request, &eventCollector{})
				if err != nil {
					t.Fatalf("%s: start: %v", name, err)
				}
				if result.Status != ports.AgentExecutionFailed || result.TerminationReason != "process_exit" || result.ExitCode != providers.FakeCLIMissingEnvExitCode {
					t.Fatalf("%s: result = %+v, want FAILED/process_exit with the fake CLI's missing-variable exit code %d", name, result, providers.FakeCLIMissingEnvExitCode)
				}
			}

			request := helperRequest("env-present-"+testCase.name, t.TempDir(), filepath.Join(t.TempDir(), "capture.json"), "success")
			request.Sandbox = testCase.startSandbox
			request.Environment["AGENTKIT_HELPER_REQUIRE_ENV"] = requiredByTheFakeCLI
			request.InheritedEnvironment = []string{"PATH", "HOME"}
			result, err := executor.Start(context.Background(), request, &eventCollector{})
			if err != nil {
				t.Fatalf("start with PATH and HOME inherited: %v", err)
			}
			assertSuccessfulResult(t, result, testCase.provider, testCase.sessionID)
		})
	}
}

// TestFakeCLIReportsWhatItInheritedWithoutRevealingValues: AGENTKIT_HELPER_REPORT_ENV
// makes the capture file say which of the listed variables were set and the
// digest of each value, never the value. The capture of a process that did not
// inherit a name simply lacks it.
func TestFakeCLIReportsWhatItInheritedWithoutRevealingValues(t *testing.T) {
	const (
		inheritedName = "AW_V905_REPORTED_INHERITED"
		withheldName  = "AW_V905_REPORTED_WITHHELD"
		inheritedVal  = "sentinel-value-that-must-not-be-written-to-the-capture-1f6c"
		withheldVal   = "sentinel-value-that-the-process-never-receives-9d2e"
	)
	t.Setenv(inheritedName, inheritedVal)
	t.Setenv(withheldName, withheldVal)

	for _, testCase := range providerCases() {
		t.Run(testCase.name, func(t *testing.T) {
			executor := testCase.newExecutor(t, processadapter.NewSupervisor())
			capturePath := filepath.Join(t.TempDir(), "capture.json")
			request := helperRequest("report-"+testCase.name, t.TempDir(), capturePath, "success")
			request.Sandbox = testCase.startSandbox
			request.Environment["AGENTKIT_HELPER_REPORT_ENV"] = inheritedName + "," + withheldName
			request.InheritedEnvironment = []string{inheritedName}
			if _, err := executor.Start(context.Background(), request, &eventCollector{}); err != nil {
				t.Fatalf("start: %v", err)
			}

			capture := readCapture(t, capturePath)
			if got, want := capture.EnvironmentSeen[inheritedName], providers.EnvironmentDigest(inheritedVal); got != want {
				t.Fatalf("EnvironmentSeen[%s] = %q, want the digest %q of the value the worker holds", inheritedName, got, want)
			}
			if digest, present := capture.EnvironmentSeen[withheldName]; present {
				t.Fatalf("the process saw %s (digest %q) although it was not in the request's inherited environment", withheldName, digest)
			}
			raw, err := os.ReadFile(capturePath)
			if err != nil {
				t.Fatalf("read capture: %v", err)
			}
			for _, value := range []string{inheritedVal, withheldVal} {
				if strings.Contains(string(raw), value) {
					t.Fatalf("the capture file holds a variable value: %s", raw)
				}
			}
		})
	}
}
