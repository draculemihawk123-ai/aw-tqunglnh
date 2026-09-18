package adapterbuild_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/cli/adapterbuild"
)

// newDeps builds a fresh cliadapterbuild.Dependencies over an in-memory
// fake.UnitOfWork with a deterministic idsource/clock — mirrors
// internal/delivery/cli/settings/settings_test.go's own identical newDeps
// helper.
func newDeps() cliadapterbuild.Dependencies {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	return cliadapterbuild.Dependencies{
		UoW: fake.New(), IDs: idsource.NewSequential("adapterbuild"),
		Now: func() time.Time { return now },
	}
}

// writeExecutable writes content to a fresh temp file and returns its path
// — mirrors internal/app/adapterbuild/commands_test.go's own identical
// helper exactly.
func writeExecutable(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-cli")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	return path
}

// probeArgs builds a valid `aw adapter probe` argument list against path,
// with extra appended verbatim (letting a test override/add flags such as
// --idempotency-key or --json).
func probeArgs(path string, extra ...string) []string {
	args := []string{
		"--provider-key=claude", "--executable-path=" + path, "--protocol-version=claude-stream-json/v1",
		"--os=linux", "--toolchain=node-20", "--config-identity=default",
		"--supports-start", "--supports-resume", "--supports-cancel",
		"--canonical-event-kinds=TEXT_DELTA,TOOL_CALL",
	}
	return append(args, extra...)
}

// registerArgs builds a valid `aw adapter register` argument list matching
// probeArgs' own manifest, with extra appended verbatim.
func registerArgs(extra ...string) []string {
	args := []string{"--supports-start", "--supports-resume", "--supports-cancel", "--canonical-event-kinds=TEXT_DELTA,TOOL_CALL"}
	return append(args, extra...)
}

// writeTokenFile extracts the "result" field from a `RunProbe --json` JSON
// stdout capture (cli.ResultEnvelope's own {idempotencyKey, replayed,
// result} shape) and writes it, alone, to a fresh temp file — the exact
// candidate token JSON an operator would save from `aw adapter probe
// --json` and hand to `aw adapter register --file`.
func writeTokenFile(t *testing.T, probeStdout []byte) string {
	t.Helper()
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(probeStdout, &envelope); err != nil {
		t.Fatalf("decode probe stdout %q: %v", string(probeStdout), err)
	}
	path := filepath.Join(t.TempDir(), "candidate-token.json")
	if err := os.WriteFile(path, envelope.Result, 0o644); err != nil {
		t.Fatalf("write candidate token file: %v", err)
	}
	return path
}

// isCLIUsageError reports whether err is a cli.UsageError — a small
// same-package-name-shadowing-avoiding wrapper so every _test.go file in
// this package can call it without repeating the cli import alias.
func isCLIUsageError(err error) bool { return cli.IsUsageError(err) }

// TestDescriptorsRegistered proves this leaf's own init() registered all
// four `aw adapter ...` commands into cli.Default with the real HTTP
// operationIds V6-10J already established, every one installation-scoped
// (this task's own "Không làm: no ProjectID" line).
func TestDescriptorsRegistered(t *testing.T) {
	want := map[string]string{
		"adapter list":     "listAdapterBuilds",
		"adapter show":     "getAdapterBuild",
		"adapter probe":    "probeAdapterBuild",
		"adapter register": "registerAdapterBuild",
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
