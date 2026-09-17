package adapterbuild_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/cli/adapterbuild"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// probeToken runs `aw adapter probe` for path and returns the decoded
// domainadapterbuild.CandidateToken plus its raw JSON bytes.
func probeToken(t *testing.T, deps cliadapterbuild.Dependencies, path string, extraArgs ...string) (domainadapterbuild.CandidateToken, []byte) {
	t.Helper()
	var stdout bytes.Buffer
	args := probeArgs(path, append([]string{"--json"}, extraArgs...)...)
	if err := cliadapterbuild.RunProbe(context.Background(), deps, args, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProbe() error = %v", err)
	}
	var envelope struct {
		Result domainadapterbuild.CandidateToken `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode probe stdout %q: %v", stdout.String(), err)
	}
	return envelope.Result, stdout.Bytes()
}

func writeTokenJSON(t *testing.T, token domainadapterbuild.CandidateToken) string {
	t.Helper()
	raw, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	path := filepath.Join(t.TempDir(), "candidate-token.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return path
}

func TestRunRegister_FreshSuccess(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")
	token, _ := probeToken(t, deps, path)
	tokenFile := writeTokenJSON(t, token)

	var stdout bytes.Buffer
	args := registerArgs("--json", "--idempotency-key=register-1", "--file="+tokenFile)
	if err := cliadapterbuild.RunRegister(context.Background(), deps, args, nil, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRegister() error = %v", err)
	}
	var envelope struct {
		IdempotencyKey string `json:"idempotencyKey"`
		Replayed       bool   `json:"replayed"`
		Result         struct {
			Build struct {
				ID             string `json:"id"`
				RegisteredBy   string `json:"registeredBy"`
				ExecutablePath string `json:"executablePath"`
			} `json:"build"`
			AlreadyExisted bool `json:"alreadyExisted"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if envelope.Replayed {
		t.Error("Replayed = true on the first call, want false")
	}
	if envelope.Result.AlreadyExisted {
		t.Error("AlreadyExisted = true on the first registration, want false")
	}
	if envelope.Result.Build.ID == "" {
		t.Fatal("registered build id is empty")
	}
	if envelope.Result.Build.ExecutablePath != path {
		t.Errorf("ExecutablePath = %q, want %q", envelope.Result.Build.ExecutablePath, path)
	}
}

// TestRunRegister_DuplicateFingerprint_AlreadyExisted proves the
// content-hash fingerprint dedup axis: two independent probe+register
// round trips (distinct idempotency keys throughout) of the identical
// unchanged executable report AlreadyExisted=true the second time, and
// share the same build id.
func TestRunRegister_DuplicateFingerprint_AlreadyExisted(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")

	firstToken, _ := probeToken(t, deps, path, "--idempotency-key=probe-1")
	firstFile := writeTokenJSON(t, firstToken)
	var firstOut bytes.Buffer
	if err := cliadapterbuild.RunRegister(context.Background(), deps, registerArgs("--json", "--idempotency-key=register-1", "--file="+firstFile), nil, &firstOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunRegister() error = %v", err)
	}

	secondToken, _ := probeToken(t, deps, path, "--idempotency-key=probe-2")
	secondFile := writeTokenJSON(t, secondToken)
	var secondOut bytes.Buffer
	if err := cliadapterbuild.RunRegister(context.Background(), deps, registerArgs("--json", "--idempotency-key=register-2", "--file="+secondFile), nil, &secondOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("second RunRegister() error = %v", err)
	}

	decode := func(buf bytes.Buffer) (id string, alreadyExisted bool) {
		var envelope struct {
			Result struct {
				Build struct {
					ID string `json:"id"`
				} `json:"build"`
				AlreadyExisted bool `json:"alreadyExisted"`
			} `json:"result"`
		}
		if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return envelope.Result.Build.ID, envelope.Result.AlreadyExisted
	}
	firstID, firstAlready := decode(firstOut)
	secondID, secondAlready := decode(secondOut)
	if firstAlready {
		t.Error("first registration reported AlreadyExisted=true, want false")
	}
	if !secondAlready {
		t.Error("second (duplicate) registration reported AlreadyExisted=false, want true")
	}
	if firstID != secondID {
		t.Fatalf("duplicate registration produced a different build id: %s vs %s", firstID, secondID)
	}
}

// TestRunRegister_Replay is the "replay" Verify scenario: an identical
// retry (same idempotency key, same flags/file) replays the exact original
// RegisterResult.
func TestRunRegister_Replay(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")
	token, _ := probeToken(t, deps, path)
	tokenFile := writeTokenJSON(t, token)
	args := registerArgs("--json", "--idempotency-key=idem-replay", "--file="+tokenFile)

	var first bytes.Buffer
	if err := cliadapterbuild.RunRegister(context.Background(), deps, args, nil, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunRegister() error = %v", err)
	}
	var second bytes.Buffer
	if err := cliadapterbuild.RunRegister(context.Background(), deps, args, nil, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("replay RunRegister() error = %v", err)
	}
	var secondEnvelope cli.ResultEnvelope
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if !secondEnvelope.Replayed {
		t.Error("Replayed = false on a retry with the same idempotency key + payload, want true")
	}
	if first.String() == "" || second.String() == "" {
		t.Fatal("expected non-empty stdout for both calls")
	}
	var firstDecoded, secondDecoded struct {
		Result struct {
			Build struct {
				ID string `json:"id"`
			} `json:"build"`
		} `json:"result"`
	}
	if err := json.Unmarshal(first.Bytes(), &firstDecoded); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if err := json.Unmarshal(second.Bytes(), &secondDecoded); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if firstDecoded.Result.Build.ID != secondDecoded.Result.Build.ID {
		t.Fatalf("replayed build id = %s, want %s", secondDecoded.Result.Build.ID, firstDecoded.Result.Build.ID)
	}
}

// TestRunRegister_RejectsExpiredToken is the "expiry" Verify scenario: a
// token whose ExpiresAt has passed must be rejected, never silently
// accepted.
func TestRunRegister_RejectsExpiredToken(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")
	token, _ := probeToken(t, deps, path)
	// Force the token into the past without re-signing it — VerifyToken
	// must catch this as an invalid signature (the signed payload changed)
	// or an expiry failure either way; both are correct rejections, mirrors
	// internal/app/adapterbuild/commands_test.go's own identical
	// expireToken helper.
	token.ExpiresAt = token.ExpiresAt.Add(-24 * time.Hour)
	tokenFile := writeTokenJSON(t, token)

	err := cliadapterbuild.RunRegister(context.Background(), deps, registerArgs("--file="+tokenFile), nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("RunRegister() error = nil, want a rejection for an expired token")
	}
}

// TestRunRegister_RejectsExecutableDrift is the "fingerprint/drift" Verify
// scenario: the executable is overwritten after probe but before register,
// so RegisterAdapterBuild's own re-hash observes a different content hash
// than the token pinned.
func TestRunRegister_RejectsExecutableDrift(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "original-content")
	token, _ := probeToken(t, deps, path)
	tokenFile := writeTokenJSON(t, token)

	if err := os.WriteFile(path, []byte("swapped-content"), 0o755); err != nil {
		t.Fatalf("swap executable content: %v", err)
	}

	err := cliadapterbuild.RunRegister(context.Background(), deps, registerArgs("--file="+tokenFile), nil, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, appadapterbuild.ErrExecutableDrift) {
		t.Fatalf("RunRegister() error = %v, want appadapterbuild.ErrExecutableDrift", err)
	}
}

// TestRunRegister_RejectsCapabilityManifestMismatch is the "capability
// mismatch" Verify scenario: registering with a DIFFERENT manifest than the
// one probed must be rejected, not silently accepted.
func TestRunRegister_RejectsCapabilityManifestMismatch(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")
	token, _ := probeToken(t, deps, path)
	tokenFile := writeTokenJSON(t, token)

	// probeArgs/registerArgs both declare --supports-cancel; omit it here so
	// the re-derived manifest hash no longer matches what the token bound.
	args := []string{"--supports-start", "--supports-resume", "--canonical-event-kinds=TEXT_DELTA,TOOL_CALL", "--file=" + tokenFile}
	err := cliadapterbuild.RunRegister(context.Background(), deps, args, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, appadapterbuild.ErrCapabilityManifestDrift) {
		t.Fatalf("RunRegister() error = %v, want appadapterbuild.ErrCapabilityManifestDrift", err)
	}
}

func TestRunRegister_MissingFile_UsageError(t *testing.T) {
	deps := newDeps()
	args := registerArgs("--file=" + filepath.Join(t.TempDir(), "does-not-exist.json"))
	err := cliadapterbuild.RunRegister(context.Background(), deps, args, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !isCLIUsageError(err) {
		t.Fatalf("RunRegister() error = %v, want a UsageError for a missing token file", err)
	}
}

func TestRunRegister_InvalidTokenJSON_UsageError(t *testing.T) {
	deps := newDeps()
	path := filepath.Join(t.TempDir(), "bad-token.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write bad token file: %v", err)
	}
	args := registerArgs("--file=" + path)
	err := cliadapterbuild.RunRegister(context.Background(), deps, args, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !isCLIUsageError(err) {
		t.Fatalf("RunRegister() error = %v, want a UsageError for invalid token JSON", err)
	}
}

func TestRunRegister_InvalidCapabilityManifest_UsageError(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "content")
	token, _ := probeToken(t, deps, path)
	tokenFile := writeTokenJSON(t, token)
	// Deliberately omit --supports-start.
	err := cliadapterbuild.RunRegister(context.Background(), deps, []string{"--file=" + tokenFile}, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !isCLIUsageError(err) {
		t.Fatalf("RunRegister() error = %v, want a UsageError for a manifest without supportsStart", err)
	}
}

func TestRunRegister_ReadsTokenFromStdin(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")
	token, _ := probeToken(t, deps, path)
	raw, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	var stdout bytes.Buffer
	args := registerArgs("--json")
	if err := cliadapterbuild.RunRegister(context.Background(), deps, args, bytes.NewReader(raw), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRegister() (stdin) error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"alreadyExisted": false`) {
		t.Errorf("stdout = %q, want alreadyExisted: false", stdout.String())
	}
}
