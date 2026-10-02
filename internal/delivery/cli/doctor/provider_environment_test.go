package doctor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	clidoctor "github.com/taQuangLing/agent-workflow/internal/delivery/cli/doctor"
)

// V9-05 (gap G5): `aw doctor` hands Dependencies.ProviderProbe to the report,
// so a configured provider executable that cannot run in the allowlisted
// environment shows up as the typed finding in both output forms. The real
// process, real flag and real database are covered by cmd/aw's
// TestOneShotDoctor_ProviderEnvironment.
func TestRunDoctor_ProviderEnvironment_TypedFindingInBothOutputs(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(executable, []byte("fake-provider-binary-v1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig(t.TempDir())
	cfg.ProviderExecutables = map[string]string{"claude": executable}
	cfg.EnvAllowlist = []string{"PATH"}

	var probedWith []string
	deps := clidoctor.Dependencies{
		Config: cfg, Store: &fake.QueryStore{}, UnitOfWork: fake.New(), Isolation: fake.IsolationEnforcementChecker{},
		ProviderProbe: func(_ context.Context, _, _ string, inherited []string) error {
			probedWith = append([]string(nil), inherited...)
			return &ports.CapabilityProbeError{Reason: ports.CapabilityProbeNonZeroExit, ExitCode: 17}
		},
	}

	var stdout bytes.Buffer
	if err := clidoctor.RunDoctor(context.Background(), deps, []string{"--json"}, &stdout); err != nil {
		t.Fatalf("RunDoctor --json: %v", err)
	}
	var report clidoctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	var found *clidoctor.CheckResult
	for i := range report.Checks {
		if report.Checks[i].Name == "provider_environment:claude" {
			found = &report.Checks[i]
		}
	}
	if found == nil {
		t.Fatalf("no provider_environment:claude check in %+v", report.Checks)
	}
	if found.Status != "DEGRADED" || !strings.HasPrefix(found.Detail, "PROVIDER_ENV_INSUFFICIENT: ") || found.Remediation == "" {
		t.Fatalf("check = %+v, want the typed DEGRADED finding with a remediation", *found)
	}
	if report.Status != "DEGRADED" {
		t.Fatalf("status = %q, want DEGRADED", report.Status)
	}
	if !reflect.DeepEqual(probedWith, []string{"PATH"}) {
		t.Fatalf("the probe ran with %v, want the configured allowlist [PATH]", probedWith)
	}

	stdout.Reset()
	if err := clidoctor.RunDoctor(context.Background(), deps, nil, &stdout); err != nil {
		t.Fatalf("RunDoctor (text): %v", err)
	}
	for _, want := range []string{"provider_environment:claude", "PROVIDER_ENV_INSUFFICIENT: ", "remediation:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("the text report does not contain %q:\n%s", want, stdout.String())
		}
	}
}
