package doctor_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/doctor"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// V9-05 (gap G5): `aw doctor` says, with a typed code, when a configured
// provider executable cannot run in the environment an agent would get from the
// worker.

// recordingProbe is a ProviderProbe that records what it was asked and answers
// with err.
type recordingProbe struct {
	err       error
	calls     int
	provider  string
	path      string
	inherited []string
}

func (p *recordingProbe) probe(_ context.Context, provider, executable string, inherited []string) error {
	p.calls++
	p.provider, p.path, p.inherited = provider, executable, append([]string(nil), inherited...)
	return p.err
}

func fixtureExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-provider")
	if err := os.WriteFile(path, []byte("fake-provider-binary-v1\n"), 0o755); err != nil {
		t.Fatalf("write fixture executable: %v", err)
	}
	return path
}

func TestCheckProviderEnvironment_ProbeSucceeds_HealthyNamesTheVariables(t *testing.T) {
	probe := &recordingProbe{}
	path := fixtureExecutable(t)
	result, ok := doctor.CheckProviderEnvironment(context.Background(), "claude", path, []string{"PATH", "HOME", "PATH"}, probe.probe)
	if !ok {
		t.Fatal("CheckProviderEnvironment returned no result for a configured, existing executable")
	}
	if result.Status != doctor.StatusHealthy || result.Category != doctor.CategoryCapability || result.Name != "provider_environment:claude" {
		t.Fatalf("result = %+v, want a HEALTHY CAPABILITY provider_environment:claude", result)
	}
	if !strings.Contains(result.Detail, "HOME, PATH") {
		t.Fatalf("Detail = %q, want it to name the variables the probe ran with, sorted", result.Detail)
	}
	if strings.Contains(result.Detail, doctor.ProviderEnvInsufficient) {
		t.Fatalf("a healthy result must not carry the failure code: %q", result.Detail)
	}
	// The probe was given the canonical list: sorted, deduplicated, the exact
	// executable and provider.
	if probe.calls != 1 || probe.provider != "claude" || probe.path != path || !reflect.DeepEqual(probe.inherited, []string{"HOME", "PATH"}) {
		t.Fatalf("probe saw calls=%d provider=%q path=%q inherited=%v", probe.calls, probe.provider, probe.path, probe.inherited)
	}
}

func TestCheckProviderEnvironment_NothingAllowed_StillProbesAndSaysSo(t *testing.T) {
	probe := &recordingProbe{}
	result, ok := doctor.CheckProviderEnvironment(context.Background(), "codex", fixtureExecutable(t), nil, probe.probe)
	if !ok || result.Status != doctor.StatusHealthy {
		t.Fatalf("result = %+v ok=%v, want HEALTHY", result, ok)
	}
	if probe.calls != 1 || len(probe.inherited) != 0 {
		t.Fatalf("probe calls=%d inherited=%v, want one probe with an empty environment", probe.calls, probe.inherited)
	}
	if !strings.Contains(result.Detail, "(none)") {
		t.Fatalf("Detail = %q, want it to say that no variable name is inherited", result.Detail)
	}
}

// TestCheckProviderEnvironment_ProbeFails_TypedDegradedWithRemediation is the
// regression for the doctor half of G5: each typed failure of the probe is a
// DEGRADED result whose Detail STARTS with the stable code, gives the reason in
// the check's own words, and whose Remediation names the allowlists to extend.
func TestCheckProviderEnvironment_ProbeFails_TypedDegradedWithRemediation(t *testing.T) {
	tests := []struct {
		name   string
		err    *ports.CapabilityProbeError
		reason string
	}{
		{"non-zero exit", &ports.CapabilityProbeError{Reason: ports.CapabilityProbeNonZeroExit, ExitCode: 17}, "exited with code 17"},
		{"could not start", &ports.CapabilityProbeError{Reason: ports.CapabilityProbeStartFailed}, "could not be started"},
		{"timed out", &ports.CapabilityProbeError{Reason: ports.CapabilityProbeTimedOut}, "did not answer within"},
		{"cancelled", &ports.CapabilityProbeError{Reason: ports.CapabilityProbeCancelled}, "was cancelled"},
		{"no output", &ports.CapabilityProbeError{Reason: ports.CapabilityProbeEmptyOutput}, "printed no version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// The adapters wrap the typed error with their own context; the
			// check must find it through that.
			wrapped := fmt.Errorf("claude: capability probe: %w", test.err)
			result, ok := doctor.CheckProviderEnvironment(context.Background(), "claude", fixtureExecutable(t), []string{"PATH"}, (&recordingProbe{err: wrapped}).probe)
			if !ok {
				t.Fatal("no result")
			}
			if result.Status != doctor.StatusDegraded || result.Category != doctor.CategoryCapability {
				t.Fatalf("result = %+v, want DEGRADED/CAPABILITY", result)
			}
			if !strings.HasPrefix(result.Detail, "PROVIDER_ENV_INSUFFICIENT: ") || doctor.ProviderEnvInsufficient != "PROVIDER_ENV_INSUFFICIENT" {
				t.Fatalf("Detail = %q, want it to START with the stable code PROVIDER_ENV_INSUFFICIENT:", result.Detail)
			}
			if !strings.Contains(result.Detail, test.reason) {
				t.Fatalf("Detail = %q, want the reason %q", result.Detail, test.reason)
			}
			if !strings.Contains(result.Detail, "PATH") {
				t.Fatalf("Detail = %q, want it to name the variable names the probe ran with", result.Detail)
			}
			for _, want := range []string{"--env-allowlist", "envAllowlist", "PATH", "HOME", "BOTH"} {
				if !strings.Contains(result.Remediation, want) {
					t.Fatalf("Remediation = %q, want it to mention %q", result.Remediation, want)
				}
			}
		})
	}
}

// TestCheckProviderEnvironment_NeverPrintsAnErrorTextOrAValue: what the probe's
// error says (a path, an operating-system message) and what the environment
// holds never reach the result — it is built from the typed reason and the
// variable NAMES.
func TestCheckProviderEnvironment_NeverPrintsAnErrorTextOrAValue(t *testing.T) {
	const (
		secretValue = "sentinel-secret-value-do-not-print-3f9a"
		leakyText   = "open /etc/secret-location/credentials: permission denied"
	)
	t.Setenv("AW_V905_DOCTOR_SECRET", secretValue)
	failure := &ports.CapabilityProbeError{
		Reason: ports.CapabilityProbeStartFailed, Err: errors.New(leakyText + " " + secretValue),
	}
	result, _ := doctor.CheckProviderEnvironment(context.Background(), "claude", fixtureExecutable(t), []string{"AW_V905_DOCTOR_SECRET"}, (&recordingProbe{err: failure}).probe)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secretValue, "secret-location", "permission denied"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("the result leaks %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(result.Detail, "AW_V905_DOCTOR_SECRET") {
		t.Fatalf("Detail = %q, want the variable NAME", result.Detail)
	}
}

func TestCheckProviderEnvironment_UntypedProbeError_IsADistinctCode(t *testing.T) {
	result, ok := doctor.CheckProviderEnvironment(context.Background(), "claude", fixtureExecutable(t), []string{"PATH"},
		(&recordingProbe{err: errors.New("adapter: unknown provider")}).probe)
	if !ok || result.Status != doctor.StatusDegraded {
		t.Fatalf("result = %+v ok=%v, want DEGRADED", result, ok)
	}
	if !strings.HasPrefix(result.Detail, "PROVIDER_PROBE_ERROR: ") || strings.Contains(result.Detail, doctor.ProviderEnvInsufficient) {
		t.Fatalf("Detail = %q, want the PROVIDER_PROBE_ERROR code, not an environment verdict the probe never reached", result.Detail)
	}
	if result.Remediation == "" {
		t.Fatal("a non-HEALTHY check must carry a remediation")
	}
}

// TestCheckProviderEnvironment_SaysNothingWhenThereIsNothingToProbe: no probe
// wired, no executable configured, or an executable that is not there (the
// existing provider:<name> check already reports that one) — no result, so a
// report is exactly what it was before V9-05.
func TestCheckProviderEnvironment_SaysNothingWhenThereIsNothingToProbe(t *testing.T) {
	probe := &recordingProbe{}
	missing := filepath.Join(t.TempDir(), "no-such-binary")
	for name, call := range map[string]func() (doctor.CheckResult, bool){
		"no probe wired": func() (doctor.CheckResult, bool) {
			return doctor.CheckProviderEnvironment(context.Background(), "claude", fixtureExecutable(t), nil, nil)
		},
		"no executable": func() (doctor.CheckResult, bool) {
			return doctor.CheckProviderEnvironment(context.Background(), "claude", "", nil, probe.probe)
		},
		"blank executable": func() (doctor.CheckResult, bool) {
			return doctor.CheckProviderEnvironment(context.Background(), "claude", "  ", nil, probe.probe)
		},
		"executable is not there": func() (doctor.CheckResult, bool) {
			return doctor.CheckProviderEnvironment(context.Background(), "claude", missing, nil, probe.probe)
		},
	} {
		if result, ok := call(); ok {
			t.Fatalf("%s: got a result %+v, want none", name, result)
		}
	}
	if probe.calls != 0 {
		t.Fatalf("the probe ran %d time(s) although there was nothing to probe", probe.calls)
	}
}

func runOptions(t *testing.T, providers map[string]string, allowlist []string, probe doctor.ProviderProbe) doctor.Options {
	t.Helper()
	cfg := config.Config{
		DatabasePath: filepath.Join(t.TempDir(), "agentkit.db"), ArtifactRoot: t.TempDir(), WorkerID: "worker-1",
		WorkerConcurrency: 4, LeaseTTL: 30 * time.Second, LeaseHeartbeat: 10 * time.Second,
		ProcessOutputLimit: 1 << 20, ProviderExecutables: providers, EnvAllowlist: allowlist,
	}
	return doctor.Options{
		Config: cfg, Store: &fake.QueryStore{},
		WorkerConfig:  workerpool.Config{Owner: "worker-1", LeaseTTL: 30 * time.Second, HeartbeatEvery: 10 * time.Second},
		CheckWorker:   true,
		ProviderProbe: probe,
	}
}

func checkNames(report doctor.Report) []string {
	names := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		names = append(names, check.Name)
	}
	return names
}

// TestRun_WithoutAProviderProbe_ReportIsUnchanged: the golden reports
// (report_golden_test.go) run exactly this shape; with no probe wired there is
// no provider_environment check at all, so they did not change.
func TestRun_WithoutAProviderProbe_ReportIsUnchanged(t *testing.T) {
	report := doctor.Run(context.Background(), runOptions(t, map[string]string{"claude": fixtureExecutable(t)}, []string{"PATH"}, nil))
	for _, name := range checkNames(report) {
		if strings.HasPrefix(name, "provider_environment:") {
			t.Fatalf("a report without a ProviderProbe contains %q", name)
		}
	}
}

// TestRun_ProbesEveryExistingProviderWithTheConfiguredAllowlist: one probe per
// configured, existing executable, run with Config.EnvAllowlist; a failing one
// makes the whole report DEGRADED, a healthy one does not.
func TestRun_ProbesEveryExistingProviderWithTheConfiguredAllowlist(t *testing.T) {
	claudePath, codexPath := fixtureExecutable(t), fixtureExecutable(t)
	missingPath := filepath.Join(t.TempDir(), "no-such-binary")
	var probed []string
	probe := func(_ context.Context, provider, executable string, inherited []string) error {
		probed = append(probed, provider)
		if !reflect.DeepEqual(inherited, []string{"HOME", "PATH"}) {
			t.Errorf("%s was probed with %v, want the configured allowlist [HOME PATH]", provider, inherited)
		}
		if provider == "codex" {
			return &ports.CapabilityProbeError{Reason: ports.CapabilityProbeNonZeroExit, ExitCode: 3}
		}
		return nil
	}
	report := doctor.Run(context.Background(), runOptions(t,
		map[string]string{"claude": claudePath, "codex": codexPath, "ghost": missingPath}, []string{"PATH", "HOME"}, probe))

	if want := []string{"claude", "codex"}; !reflect.DeepEqual(probed, want) {
		t.Fatalf("probed %v, want %v (the missing executable is not probed)", probed, want)
	}
	byName := map[string]doctor.CheckResult{}
	for _, check := range report.Checks {
		byName[check.Name] = check
	}
	if got := byName["provider_environment:claude"]; got.Status != doctor.StatusHealthy {
		t.Fatalf("claude environment check = %+v, want HEALTHY", got)
	}
	if got := byName["provider_environment:codex"]; got.Status != doctor.StatusDegraded || !strings.HasPrefix(got.Detail, "PROVIDER_ENV_INSUFFICIENT: ") {
		t.Fatalf("codex environment check = %+v, want a typed DEGRADED", got)
	}
	if _, present := byName["provider_environment:ghost"]; present {
		t.Fatal("an executable that does not exist got an environment verdict; provider:ghost already reports it")
	}
	if byName["provider:ghost"].Status != doctor.StatusDegraded {
		t.Fatalf("provider:ghost = %+v, want the existing DEGRADED finding to stay", byName["provider:ghost"])
	}
	if report.Status != doctor.StatusDegraded {
		t.Fatalf("report.Status = %v, want DEGRADED (a provider cannot run in the agent environment)", report.Status)
	}
}

// TestRun_HealthyEnvironmentKeepsTheReportHealthy: a probe that works adds a
// HEALTHY check and leaves a healthy installation healthy.
func TestRun_HealthyEnvironmentKeepsTheReportHealthy(t *testing.T) {
	report := doctor.Run(context.Background(), runOptions(t, map[string]string{"claude": fixtureExecutable(t)}, []string{"PATH"},
		func(context.Context, string, string, []string) error { return nil }))
	if report.Status != doctor.StatusHealthy {
		t.Fatalf("report.Status = %v, want HEALTHY: %+v", report.Status, report.Checks)
	}
	found := false
	for _, check := range report.Checks {
		found = found || check.Name == "provider_environment:claude"
	}
	if !found {
		t.Fatalf("no provider_environment:claude check in %v", checkNames(report))
	}
}
