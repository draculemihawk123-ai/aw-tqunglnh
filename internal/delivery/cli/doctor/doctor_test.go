package doctor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	clidoctor "github.com/taQuangLing/agent-workflow/internal/delivery/cli/doctor"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

func validConfig(artifactRoot string) config.Config {
	cfg := config.Defaults()
	cfg.DatabasePath = "unused-doctor-cli-test.db"
	cfg.ArtifactRoot = artifactRoot
	cfg.WorkerID = "aw-doctor-cli-test"
	return cfg
}

// TestDescriptorRegistered proves this leaf's own init() registered
// `aw doctor` into cli.Default with GET /doctor's own real operationId.
func TestDescriptorRegistered(t *testing.T) {
	found := false
	for _, d := range cli.Default.All() {
		if strings.Join(d.Path, " ") != "doctor" {
			continue
		}
		found = true
		if d.HTTPOperationID != "doctor" {
			t.Errorf("HTTPOperationID = %q, want doctor", d.HTTPOperationID)
		}
		if d.Scope != cli.ScopeInstallation {
			t.Errorf("Scope = %q, want ScopeInstallation", d.Scope)
		}
		if d.AppOperation != "Doctor" {
			t.Errorf("AppOperation = %q, want Doctor", d.AppOperation)
		}
	}
	if !found {
		t.Fatal("doctor descriptor was not registered")
	}
}

// TestRunDoctor_Healthy is this leaf's own "healthy" golden Verify
// scenario, and also proves RunDoctor always returns nil regardless of
// status (severity lives in the body, never the command's own exit code —
// mirroring GET /doctor's own "always 200" contract).
func TestRunDoctor_Healthy(t *testing.T) {
	deps := clidoctor.Dependencies{
		Config: validConfig(t.TempDir()), Store: &fake.QueryStore{}, UnitOfWork: fake.New(),
		Isolation: fake.IsolationEnforcementChecker{},
	}
	var stdout bytes.Buffer
	if err := clidoctor.RunDoctor(context.Background(), deps, []string{"--json"}, &stdout); err != nil {
		t.Fatalf("RunDoctor() error = %v, want nil", err)
	}
	var report clidoctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if report.Status != "HEALTHY" {
		t.Fatalf("status = %q, want HEALTHY: %+v", report.Status, report.Checks)
	}
	if report.RestartRequired {
		t.Error("restartRequired = true on a never-configured install, want false")
	}
	for _, name := range []string{"database", "artifact_root", "git", "safe_settings", "isolation_enforcement"} {
		found := false
		for _, c := range report.Checks {
			if c.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %q check in %+v", name, report.Checks)
		}
	}
	wantLinks := map[string]string{
		"adapterBuilds": "/adapter-builds",
		"safeSettings":  "/settings/safe",
		"projects":      "/projects",
		"healthLive":    "/health/live",
		"healthReady":   "/health/ready",
	}
	if len(report.Links) != len(wantLinks) {
		t.Fatalf("links = %+v, want %+v", report.Links, wantLinks)
	}
	for k, v := range wantLinks {
		if report.Links[k] != v {
			t.Errorf("links[%q] = %q, want %q", k, report.Links[k], v)
		}
	}
}

// TestRunDoctor_Degraded is this leaf's own "degraded" golden Verify
// scenario: the artifact root does not exist yet.
func TestRunDoctor_Degraded(t *testing.T) {
	missingRoot := "this/artifact/root/does/not/exist/yet"
	deps := clidoctor.Dependencies{
		Config: validConfig(missingRoot), Store: &fake.QueryStore{}, UnitOfWork: fake.New(),
		Isolation: fake.IsolationEnforcementChecker{},
	}
	var stdout bytes.Buffer
	if err := clidoctor.RunDoctor(context.Background(), deps, []string{"--json"}, &stdout); err != nil {
		t.Fatalf("RunDoctor() error = %v, want nil", err)
	}
	var report clidoctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if report.Status != "DEGRADED" {
		t.Fatalf("status = %q, want DEGRADED: %+v", report.Status, report.Checks)
	}
}

// TestRunDoctor_Blocked is this leaf's own "blocked" golden Verify
// scenario: the query store is unreachable.
func TestRunDoctor_Blocked(t *testing.T) {
	deps := clidoctor.Dependencies{
		Config: validConfig(t.TempDir()), Store: &fake.QueryStore{Unreachable: true}, UnitOfWork: fake.New(),
		Isolation: fake.IsolationEnforcementChecker{},
	}
	var stdout bytes.Buffer
	if err := clidoctor.RunDoctor(context.Background(), deps, []string{"--json"}, &stdout); err != nil {
		t.Fatalf("RunDoctor() error = %v, want nil (severity lives in the body, not the command's own exit)", err)
	}
	var report clidoctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if report.Status != "BLOCKED" {
		t.Fatalf("status = %q, want BLOCKED: %+v", report.Status, report.Checks)
	}
}

// TestRunDoctor_NilIsolation proves isolationCheck's own defensive branch
// (a composition-root wiring gap, never expected against a real
// checker) reports BLOCKED rather than panicking.
func TestRunDoctor_NilIsolation(t *testing.T) {
	deps := clidoctor.Dependencies{
		Config: validConfig(t.TempDir()), Store: &fake.QueryStore{}, UnitOfWork: fake.New(),
		Isolation: nil,
	}
	var stdout bytes.Buffer
	if err := clidoctor.RunDoctor(context.Background(), deps, []string{"--json"}, &stdout); err != nil {
		t.Fatalf("RunDoctor() error = %v, want nil", err)
	}
	var report clidoctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if report.Status != "BLOCKED" {
		t.Fatalf("status = %q, want BLOCKED with a nil isolation checker", report.Status)
	}
}

// TestRunDoctor_RestartRequired_AfterSettingsUpdate mirrors V6-10H's own
// restartRequired concept: a real safe-settings update flips it from false
// to true, visible through `aw doctor` without this leaf ever re-deriving
// the semantic itself (restartRequired forwards
// safesettingsapp.GetSafeSettings' own already-computed bool verbatim).
func TestRunDoctor_RestartRequired_AfterSettingsUpdate(t *testing.T) {
	uow := fake.New()
	deps := clidoctor.Dependencies{
		Config: validConfig(t.TempDir()), Store: &fake.QueryStore{}, UnitOfWork: uow,
		Isolation: fake.IsolationEnforcementChecker{},
	}

	ctx := context.Background()
	var before clidoctor.Report
	beforeOut := &bytes.Buffer{}
	if err := clidoctor.RunDoctor(ctx, deps, []string{"--json"}, beforeOut); err != nil {
		t.Fatalf("RunDoctor() error = %v", err)
	}
	if err := json.Unmarshal(beforeOut.Bytes(), &before); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if before.RestartRequired {
		t.Fatal("restartRequired = true before any safe-settings update, want false")
	}

	desired := safesettings.SafeSettings{
		ManagedWorkspaceRoot: "data/workspaces", ManagedArtifactRoot: "data/artifacts",
		EvidenceRetention: 168 * time.Hour, ProcessOutputLimit: 1 << 20,
		ProviderExecutablePath: "bin/claude", ProviderDefaultModel: "claude-sonnet-4-5",
		ProviderCredentialRef: "keychain:doctor-cli-test-secret",
	}
	raw, err := json.Marshal(desired)
	if err != nil {
		t.Fatal(err)
	}
	cmd := ports.Command{
		ID: "UpdateSafeSettings-doctor-cli-1", IdempotencyKey: "doctor-cli-1", Actor: "local-operator",
		ActorRoles: []string{"operator"}, CorrelationID: "UpdateSafeSettings-doctor-cli-1",
		Scope: ports.InstallationScope(), ExpectedVersion: 1, RequestedAt: time.Now().UTC(),
		Type: "UpdateSafeSettings", RequestHash: "sha256:doctor-cli-test",
	}
	if _, err := safesettingsapp.UpdateSafeSettings(ctx, uow, idsource.NewSequential("evt"), cmd,
		safesettingsapp.UpdateSafeSettingsRequest{DesiredJSON: raw}); err != nil {
		t.Fatalf("UpdateSafeSettings: %v", err)
	}

	var after clidoctor.Report
	afterOut := &bytes.Buffer{}
	if err := clidoctor.RunDoctor(ctx, deps, []string{"--json"}, afterOut); err != nil {
		t.Fatalf("RunDoctor() error = %v", err)
	}
	if err := json.Unmarshal(afterOut.Bytes(), &after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !after.RestartRequired {
		t.Fatal("restartRequired = false after a real safe-settings update, want true")
	}
}

func TestRunDoctor_HumanOutput(t *testing.T) {
	deps := clidoctor.Dependencies{
		Config: validConfig(t.TempDir()), Store: &fake.QueryStore{}, UnitOfWork: fake.New(),
		Isolation: fake.IsolationEnforcementChecker{},
	}
	var stdout bytes.Buffer
	if err := clidoctor.RunDoctor(context.Background(), deps, nil, &stdout); err != nil {
		t.Fatalf("RunDoctor() error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "status: HEALTHY") {
		t.Errorf("stdout = %q, want it to contain \"status: HEALTHY\"", out)
	}
	if !strings.Contains(out, "checks:") {
		t.Errorf("stdout = %q, want it to contain \"checks:\"", out)
	}
}
