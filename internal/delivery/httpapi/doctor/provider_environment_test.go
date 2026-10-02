package doctor_test

import (
	"context"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// V9-05 (gap G5): GET /doctor reports the same typed provider-environment
// finding `aw doctor` does, over a real HTTP round trip, when the composition
// root wires a ProviderProbe.
func TestGetDoctor_ProviderEnvironment_TypedFindingOverHTTP(t *testing.T) {
	artifactDir := t.TempDir()
	cfg := validConfig(filepath.Join(t.TempDir(), "agentkit.db"), artifactDir, "aw-serve-test")
	cfg.ProviderExecutables = map[string]string{"claude": writeFixtureExecutable(t, t.TempDir(), "claude")}
	cfg.EnvAllowlist = []string{"PATH", "HOME"}

	var probedWith []string
	failing := true
	probe := func(_ context.Context, provider, _ string, inherited []string) error {
		probedWith = append([]string(nil), inherited...)
		if failing {
			return &ports.CapabilityProbeError{Reason: ports.CapabilityProbeNonZeroExit, ExitCode: 17}
		}
		return nil
	}
	env := newTestEnvWithProbe(t, cfg, process.NewIsolationChecker(), false, probe)

	resp := env.do(t, http.MethodGet, "/doctor", "", "", "")
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /doctor status = %d, want 200 (the severity lives in the body), body=%s", resp.StatusCode, raw)
	}
	body := decodeResponse(t, raw)
	check, ok := findCheck(body, "provider_environment:claude")
	if !ok {
		t.Fatalf("missing provider_environment:claude in %s", raw)
	}
	if check.Status != "DEGRADED" || check.Category != "CAPABILITY" ||
		!strings.HasPrefix(check.Detail, "PROVIDER_ENV_INSUFFICIENT: ") || !strings.Contains(check.Detail, "exited with code 17") ||
		!strings.Contains(check.Remediation, "--env-allowlist") {
		t.Fatalf("check = %+v, want the typed DEGRADED finding with a remediation naming the allowlist", check)
	}
	if body.Status != "DEGRADED" {
		t.Fatalf("response status = %s, want DEGRADED", body.Status)
	}
	if !reflect.DeepEqual(probedWith, []string{"HOME", "PATH"}) {
		t.Fatalf("the probe was run with %v, want the configured allowlist [HOME PATH]", probedWith)
	}

	// Once the executable can run, the same request is healthy.
	failing = false
	body = decodeResponse(t, readBody(t, env.do(t, http.MethodGet, "/doctor", "", "", "")))
	if check, ok := findCheck(body, "provider_environment:claude"); !ok || check.Status != "HEALTHY" {
		t.Fatalf("check = %+v ok=%v, want HEALTHY", check, ok)
	}
}

// TestGetDoctor_WithoutAProviderProbe_HasNoProviderEnvironmentCheck: the
// response of an installation whose composition root does not wire the probe is
// what it was before V9-05.
func TestGetDoctor_WithoutAProviderProbe_HasNoProviderEnvironmentCheck(t *testing.T) {
	cfg := validConfig(filepath.Join(t.TempDir(), "agentkit.db"), t.TempDir(), "aw-serve-test")
	cfg.ProviderExecutables = map[string]string{"claude": writeFixtureExecutable(t, t.TempDir(), "claude")}
	env := newTestEnv(t, cfg, process.NewIsolationChecker(), false)

	body := decodeResponse(t, readBody(t, env.do(t, http.MethodGet, "/doctor", "", "", "")))
	for _, check := range body.Checks {
		if strings.HasPrefix(check.Name, "provider_environment:") {
			t.Fatalf("unexpected check %+v", check)
		}
	}
}
