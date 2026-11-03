package kitpublish

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A stand-in publisher: prints its arguments, the aw binary and the database it was given, and fails on PUBLISH when asked to.
const fakePublisher = `import os, sys
print("args:", " ".join(sys.argv[1:]))
print("AW=" + os.environ.get("AW", ""), "AW_DB=" + os.environ.get("AW_DB", ""), "token hunter2")
print("cwd:", os.getcwd())
sys.exit(3 if "--check" not in sys.argv and os.path.exists("fail") else 0)
`

func TestRunner_RunsTheKitPublisherOnTheConfiguredManifest(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kit", "kit.json"), `{}`)
	writeFile(t, filepath.Join(root, "kit", "scripts", "aw-publish.py"), fakePublisher)
	manifest := filepath.Join(root, "project", "aw-project.json")
	writeFile(t, manifest, `{"kit": "../kit/kit.json"}`)

	runner, err := New(Config{Python: python, AwExecutable: "/usr/local/bin/aw", Env: []string{"AW_DB=/data/aw.db"},
		Manifests: map[string]string{"p1": manifest}, Matcher: redact.NewMatcher("hunter2")})
	if err != nil {
		t.Fatal(err)
	}
	status := runner.ManifestStatus("p1")
	if !status.Configured || status.ScriptPath != filepath.Join(root, "kit", "scripts", "aw-publish.py") || status.LastRun != nil {
		t.Fatalf("status = %+v", status)
	}
	run, err := runner.RunManifest(context.Background(), "p1", ports.ManifestCheck)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Succeeded || !strings.Contains(run.Output, "args: "+manifest+" --check") || !strings.Contains(run.Output, "AW=/usr/local/bin/aw AW_DB=/data/aw.db") ||
		!strings.Contains(run.Output, "cwd: "+filepath.Join(root, "project")) || strings.Contains(run.Output, "hunter2") {
		t.Fatalf("check run = %+v", run)
	}
	writeFile(t, filepath.Join(root, "project", "fail"), "")
	run, err = runner.RunManifest(context.Background(), "p1", ports.ManifestPublish)
	if err != nil {
		t.Fatal(err)
	}
	if run.Succeeded || run.ExitCode != 3 || strings.Contains(run.Output, "--check") {
		t.Fatalf("publish run = %+v, want exit code 3 without --check", run)
	}
	if last := runner.ManifestStatus("p1").LastRun; last == nil || last.Mode != ports.ManifestPublish {
		t.Fatalf("last run = %+v", last)
	}
	if _, err := runner.RunManifest(context.Background(), "other", ports.ManifestCheck); err != ports.ErrManifestNotConfigured {
		t.Fatalf("err = %v, want ErrManifestNotConfigured", err)
	}
}
