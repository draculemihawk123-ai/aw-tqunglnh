package adapterbuild_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/cli/adapterbuild"
)

func TestRunProbe_FreshSuccess(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")

	var stdout bytes.Buffer
	err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path, "--json", "--idempotency-key=idem-fresh"), &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("RunProbe() error = %v", err)
	}
	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if envelope.Replayed {
		t.Error("Replayed = true on the first call, want false")
	}
	if envelope.IdempotencyKey != "idem-fresh" {
		t.Errorf("IdempotencyKey = %q, want idem-fresh", envelope.IdempotencyKey)
	}
	resultJSON, err := json.Marshal(envelope.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var token struct {
		Tuple struct {
			ProviderKey    string `json:"providerKey"`
			ExecutablePath string `json:"executablePath"`
		} `json:"tuple"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(resultJSON, &token); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if token.Tuple.ProviderKey != "claude" || token.Tuple.ExecutablePath != path {
		t.Fatalf("token tuple = %+v, want ProviderKey=claude ExecutablePath=%s", token.Tuple, path)
	}
	if token.Nonce == "" || token.Signature == "" {
		t.Fatalf("token missing nonce/signature: %+v", token)
	}
}

func TestRunProbe_GeneratedIdempotencyKeyReturned(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")
	var stdout bytes.Buffer
	if err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path, "--json"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProbe() error = %v", err)
	}
	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.IdempotencyKey == "" {
		t.Error("IdempotencyKey is empty, want a generated value")
	}
}

// TestRunProbe_Replay is the "replay" Verify scenario: an identical retry
// (same idempotency key, same flags) must replay the EXACT original
// candidate — proven here by deleting the executable between calls, so a
// real re-probe attempt would fail loudly instead of succeeding.
func TestRunProbe_Replay(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")
	args := probeArgs(path, "--json", "--idempotency-key=idem-replay")

	var first bytes.Buffer
	if err := cliadapterbuild.RunProbe(context.Background(), deps, args, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunProbe() error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove executable: %v", err)
	}

	var second bytes.Buffer
	if err := cliadapterbuild.RunProbe(context.Background(), deps, args, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("replay RunProbe() error = %v (should replay without touching the now-deleted executable)", err)
	}
	var firstEnvelope, secondEnvelope cli.ResultEnvelope
	if err := json.Unmarshal(first.Bytes(), &firstEnvelope); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if secondEnvelope.Replayed != true {
		t.Error("Replayed = false on a retry with the same idempotency key + payload, want true")
	}
	firstResult, _ := json.Marshal(firstEnvelope.Result)
	secondResult, _ := json.Marshal(secondEnvelope.Result)
	if string(firstResult) != string(secondResult) {
		t.Fatalf("replayed candidate differs from the original:\nfirst:  %s\nsecond: %s", firstResult, secondResult)
	}
}

// TestRunProbe_SameKeyDifferentPayload_IsReceiptConflict proves the same
// --idempotency-key reused for a genuinely different request (a different
// --provider-key here) conflicts rather than silently replaying or
// re-probing — caught by cli.Dispatch's own shared LookupReceipt/
// ReconcileReceipt pre-check (the SAME replay authority
// internal/delivery/httpapi uses), not a second, leaf-invented check.
func TestRunProbe_SameKeyDifferentPayload_IsReceiptConflict(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")

	if err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path, "--idempotency-key=idem-shared"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunProbe() error = %v", err)
	}
	err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path, "--idempotency-key=idem-shared", "--provider-key=codex"), &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, cli.ErrReceiptHashConflict) {
		t.Fatalf("RunProbe() error = %v, want cli.ErrReceiptHashConflict", err)
	}
}

func TestRunProbe_MissingRequiredFlag(t *testing.T) {
	path := writeExecutable(t, "content")
	fields := []string{"--provider-key=", "--executable-path=", "--protocol-version=", "--os=", "--toolchain=", "--config-identity="}
	for _, blank := range fields {
		t.Run(blank, func(t *testing.T) {
			deps := newDeps()
			flagName := strings.SplitN(blank, "=", 2)[0]
			args := probeArgs(path)
			for i, a := range args {
				if strings.HasPrefix(a, flagName+"=") {
					args[i] = blank
				}
			}
			err := cliadapterbuild.RunProbe(context.Background(), deps, args, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !isCLIUsageError(err) {
				t.Fatalf("RunProbe() error = %v, want a UsageError for missing %s", err, flagName)
			}
		})
	}
}

func TestRunProbe_InvalidCapabilityManifest_UsageError(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "content")
	args := []string{
		"--provider-key=claude", "--executable-path=" + path, "--protocol-version=v1",
		"--os=linux", "--toolchain=node-20", "--config-identity=default",
		// deliberately omit --supports-start
	}
	err := cliadapterbuild.RunProbe(context.Background(), deps, args, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !isCLIUsageError(err) {
		t.Fatalf("RunProbe() error = %v, want a UsageError for a manifest without supportsStart", err)
	}
}

func TestRunProbe_RejectsUnknownFlag(t *testing.T) {
	deps := newDeps()
	err := cliadapterbuild.RunProbe(context.Background(), deps, []string{"--not-a-real-flag"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !isCLIUsageError(err) {
		t.Fatalf("RunProbe() error = %v, want a UsageError", err)
	}
}

// TestRunProbe_HumanOutput_NotJSON proves the human-output branch renders
// plain text (not JSON) and still carries the token's key facts.
func TestRunProbe_HumanOutput_NotJSON(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "content")
	var stdout bytes.Buffer
	if err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProbe() error = %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "providerKey: claude") {
		t.Errorf("human stdout = %q, want it to report providerKey: claude", out)
	}
	var probe map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &probe); err == nil {
		t.Errorf("human stdout decodes as JSON, want plain text")
	}
}
