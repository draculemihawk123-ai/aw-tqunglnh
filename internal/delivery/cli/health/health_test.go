package health_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/health"
)

// TestDescriptorsRegistered proves this leaf's own init() registered both
// `aw health live` and `aw health ready` into cli.Default, with the real
// HTTP operationId each one mirrors — this test binary only ever imports
// this one leaf package (plus the shared cli framework), so cli.Default
// carries exactly these two registrations, never a registration some other
// leaf package contributed.
func TestDescriptorsRegistered(t *testing.T) {
	want := map[string]string{
		"health live":  "healthLive",
		"health ready": "healthReady",
	}
	found := map[string]bool{}
	for _, d := range cli.Default.All() {
		key := strings.Join(d.Path, " ")
		op, ok := want[key]
		if !ok {
			continue
		}
		found[key] = true
		if d.HTTPOperationID != op {
			t.Errorf("descriptor %q HTTPOperationID = %q, want %q", key, d.HTTPOperationID, op)
		}
		if d.Scope != cli.ScopeInstallation {
			t.Errorf("descriptor %q Scope = %q, want ScopeInstallation", key, d.Scope)
		}
	}
	for key := range want {
		if !found[key] {
			t.Errorf("descriptor %q was not registered", key)
		}
	}
}

func TestLive_JSONOutput(t *testing.T) {
	var stdout bytes.Buffer
	if err := health.RunLive([]string{"--json"}, &stdout); err != nil {
		t.Fatalf("RunLive() error = %v, want nil (live never fails)", err)
	}
	var result health.LiveResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if result.Status != "live" {
		t.Errorf("status = %q, want live", result.Status)
	}
}

func TestLive_HumanOutput(t *testing.T) {
	var stdout bytes.Buffer
	if err := health.RunLive(nil, &stdout); err != nil {
		t.Fatalf("RunLive() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "status: live") {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), "status: live")
	}
	// Human output must never be valid JSON on its own line the way --json
	// output is, so a caller can tell the two apart by shape alone.
	var probe map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &probe); err == nil {
		t.Errorf("human stdout %q decodes as JSON, want plain text", stdout.String())
	}
}

func newFakeDeps(t *testing.T) health.Dependencies {
	t.Helper()
	return health.Dependencies{UnitOfWork: fake.New(), ArtifactRoot: t.TempDir()}
}

// TestReady_Healthy is this leaf's own "healthy" Verify scenario: a real
// (fake) UnitOfWork and an existing artifact root pass every registered
// check.
func TestReady_Healthy(t *testing.T) {
	deps := newFakeDeps(t)
	var stdout bytes.Buffer
	if err := health.RunReady(context.Background(), deps, []string{"--json"}, &stdout); err != nil {
		t.Fatalf("RunReady() error = %v, want nil for a healthy install", err)
	}
	var result health.ReadyResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if result.Status != "ready" {
		t.Fatalf("status = %q, want ready: %+v", result.Status, result)
	}
	if len(result.Checks) != 0 {
		t.Errorf("checks = %+v, want none on a healthy install", result.Checks)
	}
}

// TestReady_Blocked_MissingArtifactRoot is this leaf's own "blocked"
// Verify scenario, and proves the non-nil error path (mirroring GET
// /health/ready's own 503, a binary load-balancer-style gate) — distinct
// from `aw doctor`'s own "always nil" contract.
func TestReady_Blocked_MissingArtifactRoot(t *testing.T) {
	deps := health.Dependencies{UnitOfWork: fake.New(), ArtifactRoot: "this/path/does/not/exist/at/all"}
	var stdout bytes.Buffer
	err := health.RunReady(context.Background(), deps, []string{"--json"}, &stdout)
	if err == nil {
		t.Fatal("RunReady() error = nil, want a non-nil failure for a missing artifact root")
	}
	if cli.IsUsageError(err) {
		t.Errorf("RunReady() error is a UsageError, want a plain failure (the flags themselves were fine)")
	}
	var result health.ReadyResult
	if jsonErr := json.Unmarshal(stdout.Bytes(), &result); jsonErr != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), jsonErr)
	}
	if result.Status != "not_ready" {
		t.Fatalf("status = %q, want not_ready", result.Status)
	}
	found := false
	for _, c := range result.Checks {
		if c.Check == "artifact_root" {
			found = true
			if c.Reason == "" {
				t.Error("artifact_root failure has no reason")
			}
		}
	}
	if !found {
		t.Errorf("checks = %+v, want an artifact_root failure", result.Checks)
	}
}

func TestReady_HumanOutput_ListsFailures(t *testing.T) {
	deps := health.Dependencies{UnitOfWork: fake.New(), ArtifactRoot: "this/path/does/not/exist/at/all"}
	var stdout bytes.Buffer
	err := health.RunReady(context.Background(), deps, nil, &stdout)
	if err == nil {
		t.Fatal("RunReady() error = nil, want a non-nil failure")
	}
	if !strings.Contains(stdout.String(), "not_ready") {
		t.Errorf("stdout = %q, want it to contain not_ready", stdout.String())
	}
	if !strings.Contains(stdout.String(), "artifact_root") {
		t.Errorf("stdout = %q, want it to name the failing artifact_root check", stdout.String())
	}
}

func TestRunLive_RejectsUnknownFlag(t *testing.T) {
	var stdout bytes.Buffer
	err := health.RunLive([]string{"--not-a-real-flag"}, &stdout)
	if err == nil {
		t.Fatal("RunLive() error = nil, want a usage error for an unknown flag")
	}
	if !cli.IsUsageError(err) {
		t.Errorf("RunLive() error is not a UsageError: %v", err)
	}
}
