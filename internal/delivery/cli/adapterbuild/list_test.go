package adapterbuild_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	cliadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/cli/adapterbuild"
)

func TestRunList_EmptyRegistry(t *testing.T) {
	deps := newDeps()
	var stdout bytes.Buffer
	if err := cliadapterbuild.RunList(context.Background(), deps, []string{"--json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunList() error = %v", err)
	}
	var result struct {
		Builds []any `json:"builds"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if len(result.Builds) != 0 {
		t.Fatalf("builds = %+v, want empty", result.Builds)
	}
}

func TestRunList_AfterRegister_ReturnsOneBuild(t *testing.T) {
	deps := newDeps()
	path := writeExecutable(t, "binary-content-v1")

	var probeOut bytes.Buffer
	if err := cliadapterbuild.RunProbe(context.Background(), deps, probeArgs(path, "--json", "--idempotency-key=probe-1"), &probeOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProbe() error = %v", err)
	}
	tokenFile := writeTokenFile(t, probeOut.Bytes())
	if err := cliadapterbuild.RunRegister(context.Background(), deps, registerArgs("--json", "--idempotency-key=register-1", "--file="+tokenFile), nil, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRegister() error = %v", err)
	}

	var stdout bytes.Buffer
	if err := cliadapterbuild.RunList(context.Background(), deps, []string{"--json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunList() error = %v", err)
	}
	var result struct {
		Builds []struct {
			ID string `json:"id"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if len(result.Builds) != 1 || result.Builds[0].ID == "" {
		t.Fatalf("builds = %+v, want exactly one build with a non-empty id", result.Builds)
	}
}

func TestRunList_HumanOutput_NotJSON(t *testing.T) {
	deps := newDeps()
	var stdout bytes.Buffer
	if err := cliadapterbuild.RunList(context.Background(), deps, nil, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunList() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "builds: 0") {
		t.Errorf("human stdout = %q, want it to report builds: 0", stdout.String())
	}
	var probe map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &probe); err == nil {
		t.Errorf("human stdout decodes as JSON, want plain text")
	}
}

func TestRunList_RejectsPositionalArgs(t *testing.T) {
	deps := newDeps()
	err := cliadapterbuild.RunList(context.Background(), deps, []string{"unexpected"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("RunList() error = nil, want a usage error for an unexpected positional arg")
	}
	if !isCLIUsageError(err) {
		t.Errorf("RunList() error is not a UsageError: %v", err)
	}
}
