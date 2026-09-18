package adapterbuild_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	cliadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/cli/adapterbuild"
)

func TestRunShow_UnknownID(t *testing.T) {
	deps := newDeps()
	err := cliadapterbuild.RunShow(context.Background(), deps, []string{"does-not-exist"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, ports.ErrAdapterBuildNotFound) {
		t.Fatalf("RunShow() error = %v, want ports.ErrAdapterBuildNotFound", err)
	}
}

func TestRunShow_Found(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")

	var probeOut bytes.Buffer
	if err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path, "--json"), &probeOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProbe() error = %v", err)
	}
	tokenFile := writeTokenFile(t, probeOut.Bytes())
	var registerOut bytes.Buffer
	if err := cliadapterbuild.RunRegister(context.Background(), deps, registerArgs("--json", "--file="+tokenFile), nil, &registerOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRegister() error = %v", err)
	}
	var registerEnvelope struct {
		Result struct {
			Build struct {
				ID string `json:"id"`
			} `json:"build"`
		} `json:"result"`
	}
	if err := json.Unmarshal(registerOut.Bytes(), &registerEnvelope); err != nil {
		t.Fatalf("decode register stdout: %v", err)
	}
	id := registerEnvelope.Result.Build.ID
	if id == "" {
		t.Fatal("registered build id is empty")
	}

	var stdout bytes.Buffer
	if err := cliadapterbuild.RunShow(context.Background(), deps, []string{"--json", id}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunShow() error = %v", err)
	}
	var view struct {
		ID             string `json:"id"`
		ProviderKey    string `json:"providerKey"`
		ExecutablePath string `json:"executablePath"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode show stdout %q: %v", stdout.String(), err)
	}
	if view.ID != id || view.ProviderKey != "claude" || view.ExecutablePath != path {
		t.Fatalf("show view = %+v, want ID=%s ProviderKey=claude ExecutablePath=%s", view, id, path)
	}
}

func TestRunShow_RequiresExactlyOnePositionalArg(t *testing.T) {
	deps := newDeps()
	if err := cliadapterbuild.RunShow(context.Background(), deps, nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !isCLIUsageError(err) {
		t.Fatalf("RunShow() with no id error = %v, want a UsageError", err)
	}
	if err := cliadapterbuild.RunShow(context.Background(), deps, []string{"a", "b"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !isCLIUsageError(err) {
		t.Fatalf("RunShow() with two ids error = %v, want a UsageError", err)
	}
}
