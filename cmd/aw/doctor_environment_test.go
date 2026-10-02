package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"sync"
	"testing"
)

// V9-05 (gap G5): `aw doctor` reports, with a typed code, whether a configured
// provider executable can run in the environment an agent would get from the
// worker. These tests drive the real `aw doctor` entry point against a real
// database and a REAL fake provider CLI process (cmd/fake-claude, built once
// with `go build`): the CLI genuinely exits non-zero when a variable it
// requires (AGENTKIT_HELPER_REQUIRE_ENV) was not passed to it, so whether the
// check is healthy depends on what `--env-allowlist` let through, not on a
// stub.

var (
	fakeClaudeBuildOnce sync.Once
	fakeClaudeBuilt     string
	fakeClaudeBuildErr  error
)

// fakeClaudeBinary returns the path of a compiled cmd/fake-claude, built once
// for the package's tests (mirrors internal/integration/v5accept and
// internal/spikeacceptance, which build it the same way).
func fakeClaudeBinary(t *testing.T) string {
	t.Helper()
	fakeClaudeBuildOnce.Do(func() {
		_, file, _, ok := stdruntime.Caller(0)
		if !ok {
			fakeClaudeBuildErr = fmt.Errorf("resolve this test file's location")
			return
		}
		root := filepath.Join(filepath.Dir(file), "..", "..")
		binDir, err := os.MkdirTemp("", "aw-doctor-fake-claude")
		if err != nil {
			fakeClaudeBuildErr = err
			return
		}
		goExecutable := filepath.Join(stdruntime.GOROOT(), "bin", "go")
		output := filepath.Join(binDir, "fake-claude")
		if stdruntime.GOOS == "windows" {
			goExecutable += ".exe"
			output += ".exe"
		}
		build := exec.Command(goExecutable, "build", "-o", output, "./cmd/fake-claude")
		build.Dir = root
		if out, err := build.CombinedOutput(); err != nil {
			fakeClaudeBuildErr = fmt.Errorf("build cmd/fake-claude: %v\n%s", err, out)
			return
		}
		fakeClaudeBuilt = output
	})
	if fakeClaudeBuildErr != nil {
		t.Fatalf("fakeClaudeBinary: %v", fakeClaudeBuildErr)
	}
	return fakeClaudeBuilt
}

type doctorCheck struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation"`
}

type doctorDocument struct {
	Status string        `json:"status"`
	Checks []doctorCheck `json:"checks"`
}

func (d doctorDocument) check(name string) (doctorCheck, bool) {
	for _, c := range d.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return doctorCheck{}, false
}

func runDoctor(t *testing.T, a *awInstall, args ...string) (doctorDocument, string) {
	t.Helper()
	full := append([]string{"doctor", "--json"}, args...)
	stdout := a.mustOK("", full...)
	var document doctorDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("decode the doctor report: %v\n%s", err, stdout)
	}
	return document, stdout
}

// TestOneShotDoctor_ProviderEnvironment: the executable is the same in every
// step; only what `--env-allowlist` lets the probe inherit changes.
func TestOneShotDoctor_ProviderEnvironment(t *testing.T) {
	a := newAWInstall(t)
	fake := fakeClaudeBinary(t)
	// The fake provider cannot run without PATH. The variable that tells it so
	// is itself passed through the allowlist, so a name left out really is
	// absent from the process.
	t.Setenv("AGENTKIT_HELPER_REQUIRE_ENV", "PATH")
	t.Setenv("AW_ENV_ALLOWLIST", "")

	t.Run("insufficient environment is a typed finding", func(t *testing.T) {
		document, stdout := runDoctor(t, a, "--claude-executable", fake, "--env-allowlist", "AGENTKIT_HELPER_REQUIRE_ENV")
		check, ok := document.check("provider_environment:claude")
		if !ok {
			t.Fatalf("no provider_environment:claude check in the report:\n%s", stdout)
		}
		if check.Status != "DEGRADED" || check.Category != "CAPABILITY" {
			t.Fatalf("check = %+v, want DEGRADED/CAPABILITY", check)
		}
		if !strings.HasPrefix(check.Detail, "PROVIDER_ENV_INSUFFICIENT: ") || !strings.Contains(check.Detail, "exited with code 17") {
			t.Fatalf("Detail = %q, want the typed code first and the fake CLI's missing-variable exit code", check.Detail)
		}
		if !strings.Contains(check.Remediation, "--env-allowlist") || !strings.Contains(check.Remediation, "envAllowlist") {
			t.Fatalf("Remediation = %q, want it to name the worker allowlist and the profile allowlist", check.Remediation)
		}
		if document.Status != "DEGRADED" {
			t.Fatalf("report status = %s, want DEGRADED", document.Status)
		}
		// The existing finding about the executable itself is untouched.
		if executable, ok := document.check("provider:claude"); !ok || executable.Status != "HEALTHY" {
			t.Fatalf("provider:claude = %+v, want the existing HEALTHY fingerprint finding", executable)
		}
	})

	t.Run("sufficient environment is healthy", func(t *testing.T) {
		document, stdout := runDoctor(t, a, "--claude-executable", fake, "--env-allowlist", "AGENTKIT_HELPER_REQUIRE_ENV,PATH")
		check, ok := document.check("provider_environment:claude")
		if !ok {
			t.Fatalf("no provider_environment:claude check in the report:\n%s", stdout)
		}
		if check.Status != "HEALTHY" || strings.Contains(check.Detail, "PROVIDER_ENV_INSUFFICIENT") {
			t.Fatalf("check = %+v, want HEALTHY", check)
		}
		// Names only: the value of PATH is nowhere in the report.
		if value := os.Getenv("PATH"); len(value) > 8 && strings.Contains(stdout, value) {
			t.Fatalf("the doctor report contains the value of PATH:\n%s", stdout)
		}
		if !strings.Contains(check.Detail, "AGENTKIT_HELPER_REQUIRE_ENV") || !strings.Contains(check.Detail, "PATH") {
			t.Fatalf("Detail = %q, want it to name the variables the probe ran with", check.Detail)
		}
	})

	t.Run("AW_ENV_ALLOWLIST is the environment fallback of --env-allowlist", func(t *testing.T) {
		t.Setenv("AW_ENV_ALLOWLIST", "AGENTKIT_HELPER_REQUIRE_ENV,PATH")
		document, _ := runDoctor(t, a, "--claude-executable", fake)
		if check, ok := document.check("provider_environment:claude"); !ok || check.Status != "HEALTHY" {
			t.Fatalf("check = %+v ok=%v, want HEALTHY with the allowlist taken from AW_ENV_ALLOWLIST", check, ok)
		}
		// ...and the flag beats it.
		document, _ = runDoctor(t, a, "--claude-executable", fake, "--env-allowlist", "AGENTKIT_HELPER_REQUIRE_ENV")
		if check, ok := document.check("provider_environment:claude"); !ok || check.Status != "DEGRADED" {
			t.Fatalf("check = %+v ok=%v, want DEGRADED: the flag overrides AW_ENV_ALLOWLIST", check, ok)
		}
	})

	t.Run("the human report shows the code and the remediation", func(t *testing.T) {
		stdout := a.mustOK("", "doctor", "--claude-executable", fake, "--env-allowlist", "AGENTKIT_HELPER_REQUIRE_ENV")
		for _, want := range []string{"provider_environment:claude", "PROVIDER_ENV_INSUFFICIENT: ", "remediation:"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("the text report does not contain %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("no provider configured, no environment check", func(t *testing.T) {
		document, _ := runDoctor(t, a)
		for _, c := range document.Checks {
			if strings.HasPrefix(c.Name, "provider_environment:") {
				t.Fatalf("unexpected check %+v without a configured provider", c)
			}
		}
	})
}
