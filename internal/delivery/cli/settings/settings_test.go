package settings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	clisettings "github.com/taQuangLing/agent-workflow/internal/delivery/cli/settings"
)

type fixedClock struct{ now time.Time }

func (f fixedClock) Now() time.Time { return f.now }

const secretRef = "keychain:cli-settings-test-secret-xyz"

const validDesiredBody = `{
	"managedWorkspaceRoot":"data/workspaces",
	"managedArtifactRoot":"data/artifacts",
	"evidenceRetention":"168h0m0s",
	"processOutputLimit":1048576,
	"providerExecutablePath":"C:/tools/claude/claude.exe",
	"providerDefaultModel":"claude-sonnet-4-5",
	"providerCredentialRef":"` + secretRef + `"
}`

func newDeps() clisettings.Dependencies {
	return clisettings.Dependencies{
		UnitOfWork: fake.New(), IDs: idsource.NewSequential("evt"),
		Clock:   fixedClock{now: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)},
		Matcher: redact.NewMatcher(),
	}
}

// TestDescriptorsRegistered proves this leaf's own init() registered both
// `aw settings show` and `aw settings update` into cli.Default with
// GET/PUT /settings/safe's own real operationIds.
func TestDescriptorsRegistered(t *testing.T) {
	want := map[string]string{
		"settings show":   "getSafeSettings",
		"settings update": "updateSafeSettings",
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

// TestRunShow_NeverConfigured is this leaf's own "never configured"
// baseline: a fresh install's own desired document is the zero value and
// restartRequired is false.
func TestRunShow_NeverConfigured(t *testing.T) {
	deps := newDeps()
	var stdout bytes.Buffer
	if err := clisettings.RunShow(context.Background(), deps, []string{"--json"}, &stdout); err != nil {
		t.Fatalf("RunShow() error = %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if response["restartRequired"] != false {
		t.Errorf("restartRequired = %v, want false", response["restartRequired"])
	}
	if response["version"].(float64) != 1 {
		t.Errorf("version = %v, want 1 (the seeded never-configured row)", response["version"])
	}
}

// TestRunUpdate_FreshSuccess_MasksCredential is this leaf's own "fresh
// mutation" and "mask/redaction" Verify scenario together: a real update
// succeeds, and the raw credential reference never appears anywhere in
// stdout (JSON or human), even though it was a perfectly valid input.
func TestRunUpdate_FreshSuccess_MasksCredential(t *testing.T) {
	deps := newDeps()
	var stdout bytes.Buffer
	args := []string{"--json", "--expected-version=1", "--idempotency-key=idem-fresh"}
	err := clisettings.RunUpdate(context.Background(), deps, args, strings.NewReader(validDesiredBody), &stdout)
	if err != nil {
		t.Fatalf("RunUpdate() error = %v", err)
	}
	out := stdout.String()
	if strings.Contains(out, secretRef) {
		t.Fatalf("stdout leaks the raw credential ref: %s", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Errorf("stdout does not show a masked placeholder: %s", out)
	}
	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %q: %v", out, err)
	}
	if envelope.Replayed {
		t.Error("Replayed = true on the first call, want false")
	}
	if envelope.IdempotencyKey != "idem-fresh" {
		t.Errorf("IdempotencyKey = %q, want idem-fresh", envelope.IdempotencyKey)
	}
}

// TestRunUpdate_GeneratedIdempotencyKeyReturned proves omitting
// --idempotency-key still returns a generated one in the JSON result
// (cli.BuildEnvelope's own "generated key returned" contract).
func TestRunUpdate_GeneratedIdempotencyKeyReturned(t *testing.T) {
	deps := newDeps()
	var stdout bytes.Buffer
	args := []string{"--json", "--expected-version=1"}
	if err := clisettings.RunUpdate(context.Background(), deps, args, strings.NewReader(validDesiredBody), &stdout); err != nil {
		t.Fatalf("RunUpdate() error = %v", err)
	}
	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.IdempotencyKey == "" {
		t.Error("IdempotencyKey is empty, want a generated value")
	}
}

// TestRunUpdate_Replay is this leaf's own "replay" Verify scenario: an
// identical retry (same idempotency key, same payload) replays the first
// result instead of executing again, and the replayed output still masks
// the credential.
func TestRunUpdate_Replay(t *testing.T) {
	deps := newDeps()
	args := []string{"--json", "--expected-version=1", "--idempotency-key=idem-replay"}

	var first bytes.Buffer
	if err := clisettings.RunUpdate(context.Background(), deps, args, strings.NewReader(validDesiredBody), &first); err != nil {
		t.Fatalf("first RunUpdate() error = %v", err)
	}
	var second bytes.Buffer
	if err := clisettings.RunUpdate(context.Background(), deps, args, strings.NewReader(validDesiredBody), &second); err != nil {
		t.Fatalf("replay RunUpdate() error = %v", err)
	}

	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(second.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !envelope.Replayed {
		t.Error("Replayed = false on a retry with the same idempotency key + payload, want true")
	}
	if strings.Contains(second.String(), secretRef) {
		t.Fatalf("replayed stdout leaks the raw credential ref: %s", second.String())
	}
}

// TestRunUpdate_MissingExpectedVersion proves --expected-version is
// required and maps to a UsageError (how the command was invoked, not a
// failure while doing real work).
func TestRunUpdate_MissingExpectedVersion(t *testing.T) {
	deps := newDeps()
	var stdout bytes.Buffer
	err := clisettings.RunUpdate(context.Background(), deps, []string{"--idempotency-key=idem-x"}, strings.NewReader(validDesiredBody), &stdout)
	if err == nil {
		t.Fatal("RunUpdate() error = nil, want ErrExpectedVersionRequired")
	}
	if !cli.IsUsageError(err) {
		t.Errorf("RunUpdate() error is not a UsageError: %v", err)
	}
}

// TestRunUpdate_StaleExpectedVersion is this leaf's own "stale version"
// Verify scenario: a second update still claiming the now-superseded
// version 1 fails, and the failure is NOT a UsageError (the flags
// themselves were well-formed; the state simply moved on).
func TestRunUpdate_StaleExpectedVersion(t *testing.T) {
	deps := newDeps()
	seedArgs := []string{"--expected-version=1", "--idempotency-key=idem-seed"}
	if err := clisettings.RunUpdate(context.Background(), deps, seedArgs, strings.NewReader(validDesiredBody), &bytes.Buffer{}); err != nil {
		t.Fatalf("seed RunUpdate() error = %v", err)
	}

	staleArgs := []string{"--expected-version=1", "--idempotency-key=idem-stale"}
	var stdout bytes.Buffer
	err := clisettings.RunUpdate(context.Background(), deps, staleArgs, strings.NewReader(validDesiredBody), &stdout)
	if err == nil {
		t.Fatal("RunUpdate() error = nil, want a stale-expected-version failure")
	}
	if cli.IsUsageError(err) {
		t.Errorf("stale-version error should not be a UsageError: %v", err)
	}
}

// TestRunUpdate_InvalidDesired_UsageError proves a semantically invalid
// desired document (safesettings.Validate) fails as a UsageError before
// ever touching storage.
func TestRunUpdate_InvalidDesired_UsageError(t *testing.T) {
	deps := newDeps()
	body := `{"managedWorkspaceRoot":"","managedArtifactRoot":"data/artifacts","evidenceRetention":"1h0m0s","processOutputLimit":1024,"providerExecutablePath":"bin/claude","providerDefaultModel":"m","providerCredentialRef":"ref"}`
	err := clisettings.RunUpdate(context.Background(), deps, []string{"--expected-version=1"}, strings.NewReader(body), &bytes.Buffer{})
	if err == nil {
		t.Fatal("RunUpdate() error = nil, want a validation failure (empty managedWorkspaceRoot)")
	}
	if !cli.IsUsageError(err) {
		t.Errorf("RunUpdate() error is not a UsageError: %v", err)
	}
}

// TestRunUpdate_UnknownField_Rejected proves this task's own "no
// principal/security config mutation" scope rule is enforced structurally:
// a body naming a field outside SafeSettings' own closed 7-field allowlist
// (here, a nonsense "databasePath") is rejected at decode time as a
// UsageError, never silently accepted or forwarded anywhere.
func TestRunUpdate_UnknownField_Rejected(t *testing.T) {
	deps := newDeps()
	body := `{"managedWorkspaceRoot":"data/workspaces","databasePath":"should-not-be-allowed"}`
	err := clisettings.RunUpdate(context.Background(), deps, []string{"--expected-version=1"}, strings.NewReader(body), &bytes.Buffer{})
	if err == nil {
		t.Fatal("RunUpdate() error = nil, want an unknown-field decode failure")
	}
	if !cli.IsUsageError(err) {
		t.Errorf("RunUpdate() error is not a UsageError: %v", err)
	}
}

// TestRunShow_HumanOutput_MasksCredential is this leaf's own JSON/human
// dual-output contract Verify scenario for the human branch: the
// credential is masked there too, not just in JSON.
func TestRunShow_HumanOutput_MasksCredential(t *testing.T) {
	deps := newDeps()
	seedArgs := []string{"--expected-version=1", "--idempotency-key=idem-human"}
	if err := clisettings.RunUpdate(context.Background(), deps, seedArgs, strings.NewReader(validDesiredBody), &bytes.Buffer{}); err != nil {
		t.Fatalf("seed RunUpdate() error = %v", err)
	}

	var stdout bytes.Buffer
	if err := clisettings.RunShow(context.Background(), deps, nil, &stdout); err != nil {
		t.Fatalf("RunShow() error = %v", err)
	}
	out := stdout.String()
	if strings.Contains(out, secretRef) {
		t.Fatalf("human stdout leaks the raw credential ref: %s", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Errorf("human stdout does not show a masked placeholder: %s", out)
	}
	if !strings.Contains(out, "restartRequired: true") {
		t.Errorf("human stdout = %q, want restartRequired: true after a real update", out)
	}
	var probe map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &probe); err == nil {
		t.Errorf("human stdout decodes as JSON, want plain text")
	}
}

func TestRunUpdate_RejectsUnknownFlag(t *testing.T) {
	deps := newDeps()
	err := clisettings.RunUpdate(context.Background(), deps, []string{"--not-a-real-flag"}, strings.NewReader(validDesiredBody), &bytes.Buffer{})
	if err == nil {
		t.Fatal("RunUpdate() error = nil, want a usage error for an unknown flag")
	}
	if !cli.IsUsageError(err) {
		t.Errorf("RunUpdate() error is not a UsageError: %v", err)
	}
}
