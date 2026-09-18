package adapterbuild_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	cliadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/cli/adapterbuild"
)

// signingKeyHex loads this installation's own per-installation candidate-
// token signing key (ports.AdapterBuildRepository.LoadSigningKey) directly
// off deps' UnitOfWork and hex-encodes it — the one genuinely sensitive
// value anywhere in this whole flow (see this package's own doc.go for the
// full reasoning). No Run* function in this package has any way to return
// it (none of the four application commands this package wraps ever
// surfaces it), so this test proves that mechanically: the hex-encoded key
// must never appear as a substring of ANY stdout this package ever
// produces, JSON or human, query or mutation.
func signingKeyHex(t *testing.T, deps cliadapterbuild.Dependencies) string {
	t.Helper()
	var key []byte
	err := deps.UoW.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().LoadSigningKey(context.Background())
		key = loaded
		return err
	})
	if err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}
	if len(key) == 0 {
		t.Fatal("signing key is empty")
	}
	return hex.EncodeToString(key)
}

// TestRedaction_SigningKeyNeverLeaksInAnyOutput is this task's own
// "redaction" Verify bullet, proven mechanically rather than merely
// asserted in prose: probe (which bootstraps the signing key via
// LoadOrCreateSigningKey), register, list and show are all exercised, in
// both --json and human-output modes, and none of their combined stdout
// ever contains the raw signing key.
func TestRedaction_SigningKeyNeverLeaksInAnyOutput(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")

	var combined bytes.Buffer

	// Probe: JSON and human, fresh and replayed.
	if err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path, "--json", "--idempotency-key=probe-json"), &combined, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProbe(json) error = %v", err)
	}
	if err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path, "--idempotency-key=probe-human"), &combined, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProbe(human) error = %v", err)
	}
	if err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path, "--json", "--idempotency-key=probe-json"), &combined, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProbe(replay) error = %v", err)
	}

	key := signingKeyHex(t, deps)

	// Register: JSON and human, fresh and replayed.
	tokenForJSON, probeJSON := probeToken(t, deps, path, "--idempotency-key=probe-for-register-json")
	combined.Write(probeJSON)
	tokenFileJSON := writeTokenJSON(t, tokenForJSON)
	if err := cliadapterbuild.RunRegister(context.Background(), deps, registerArgs("--json", "--idempotency-key=register-json", "--file="+tokenFileJSON), nil, &combined, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRegister(json) error = %v", err)
	}
	if err := cliadapterbuild.RunRegister(context.Background(), deps, registerArgs("--json", "--idempotency-key=register-json", "--file="+tokenFileJSON), nil, &combined, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRegister(replay) error = %v", err)
	}
	tokenForHuman, _ := probeToken(t, deps, path, "--idempotency-key=probe-for-register-human")
	tokenFileHuman := writeTokenJSON(t, tokenForHuman)
	// Human-output register of a DIFFERENT (but still content-identical)
	// candidate reports AlreadyExisted, exercising a second real code path.
	if err := cliadapterbuild.RunRegister(context.Background(), deps, registerArgs("--idempotency-key=register-human", "--file="+tokenFileHuman), nil, &combined, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRegister(human) error = %v", err)
	}

	// List and show, JSON and human. RunList's own JSON output is captured
	// into a SEPARATE buffer purely so this test can decode the registered
	// build's own id back out cleanly for the RunShow calls below — its
	// bytes are still appended into combined afterward, so it is still part
	// of what this test scans for a leaked key.
	var listJSON bytes.Buffer
	if err := cliadapterbuild.RunList(context.Background(), deps, []string{"--json"}, &listJSON, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunList(json) error = %v", err)
	}
	combined.Write(listJSON.Bytes())
	if err := cliadapterbuild.RunList(context.Background(), deps, nil, &combined, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunList(human) error = %v", err)
	}
	var listResult struct {
		Builds []struct {
			ID string `json:"id"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(listJSON.Bytes(), &listResult); err != nil {
		t.Fatalf("decode list stdout %q: %v", listJSON.String(), err)
	}
	if len(listResult.Builds) == 0 {
		t.Fatal("expected at least one registered build")
	}
	id := listResult.Builds[0].ID
	if err := cliadapterbuild.RunShow(context.Background(), deps, []string{"--json", id}, &combined, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunShow(json) error = %v", err)
	}
	if err := cliadapterbuild.RunShow(context.Background(), deps, []string{id}, &combined, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunShow(human) error = %v", err)
	}

	out := combined.String()
	if strings.Contains(out, key) {
		t.Fatalf("combined stdout leaks the raw signing key (hex): %s", out)
	}
	// Structural denylist: no output field name in this package is ever
	// credential/secret/key-shaped (buildView/CandidateToken carry none —
	// see views.go). Matched as a quoted JSON field name specifically
	// (`"signingKey":`) rather than a bare substring — a bare substring
	// search would spuriously flag this test's own t.TempDir() path, which
	// embeds this test function's own name
	// ("TestRedaction_SigningKeyNeverLeaksInAnyOutput") inside
	// ExecutablePath's legitimate, non-secret value.
	for _, deniedField := range []string{"signingKey", "key", "credential", "password"} {
		if strings.Contains(out, `"`+deniedField+`":`) {
			t.Errorf("combined stdout contains a credential-shaped JSON field %q: %s", deniedField, out)
		}
	}
}
