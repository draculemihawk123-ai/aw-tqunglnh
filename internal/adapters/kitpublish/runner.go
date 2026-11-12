// Package kitpublish runs the kit's definition publisher (kit/scripts/aw-publish.py) for the server (V12-21, ADR-056): the operator
// names each project's manifest (aw-project.json) when starting `aw serve`, and the UI's Check and Publish buttons run the script on
// it with the same `aw` binary and stores the server uses. The publish logic stays in the kit; the engine only runs it.
package kitpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

// MaxOutputBytes bounds the output kept from one run.
const MaxOutputBytes = 64 << 10

// Config is what the runner needs.
type Config struct {
	// Python runs the script ("python3" by default).
	Python string
	// AwExecutable is the `aw` binary the script calls (AW).
	AwExecutable string
	// Env is added to the inherited environment: AW_DB, AW_ARTIFACT_ROOT, AW_WORKSPACE_ROOT, AW_CLAUDE_EXECUTABLE.
	Env []string
	// Manifests maps a project id to its manifest path.
	Manifests map[string]string
	// Timeout bounds one run (10 minutes by default).
	Timeout time.Duration
	Matcher redact.Matcher
}

// Runner implements ports.ManifestPublisher.
type Runner struct {
	cfg  Config
	mu   sync.Mutex
	busy map[string]bool
	last map[string]ports.ManifestRun
}

var _ ports.ManifestPublisher = (*Runner)(nil)

// DefaultPython is the interpreter used when none is configured: `python` on Windows, where `python3` is usually the Microsoft
// Store alias that only prints an install hint and exits 9009 (a python.org install provides python.exe only), `python3` elsewhere.
func DefaultPython() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// New returns a runner; manifest paths are made absolute.
func New(cfg Config) (*Runner, error) {
	if cfg.Python == "" {
		cfg.Python = DefaultPython()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	manifests := map[string]string{}
	for project, path := range cfg.Manifests {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("kitpublish: manifest of %s: %w", project, err)
		}
		manifests[project] = abs
	}
	cfg.Manifests = manifests
	return &Runner{cfg: cfg, busy: map[string]bool{}, last: map[string]ports.ManifestRun{}}, nil
}

// ParseManifestFlag reads one `--project-manifest <projectId>=<path>` value.
func ParseManifestFlag(value string) (string, string, error) {
	project, path, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(project) == "" || strings.TrimSpace(path) == "" {
		return "", "", fmt.Errorf("--project-manifest wants <projectId>=<path to aw-project.json>, got %q", value)
	}
	return strings.TrimSpace(project), strings.TrimSpace(path), nil
}

// ScriptFor finds aw-publish.py for a manifest: <directory of the kit.json the manifest's "kit" names>/scripts, or, for a manifest
// without "kit" (the kit's own kit.json), <its directory>/scripts.
func ScriptFor(manifestPath string) (string, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Kit json.RawMessage `json:"kit"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("manifest %s is not JSON: %w", manifestPath, err)
	}
	base := filepath.Dir(manifestPath)
	kitDir := base
	if len(manifest.Kit) > 0 && string(manifest.Kit) != "null" {
		var path string
		if err := json.Unmarshal(manifest.Kit, &path); err != nil {
			var obj struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(manifest.Kit, &obj); err != nil || obj.Path == "" {
				return "", fmt.Errorf("manifest %s: \"kit\" must be a path or {\"path\": ...}", manifestPath)
			}
			path = obj.Path
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		kitDir = filepath.Dir(path)
	}
	script := filepath.Join(kitDir, "scripts", "aw-publish.py")
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("kit publisher not found at %s: %w", script, err)
	}
	return script, nil
}

// ManifestStatus implements ports.ManifestPublisher.
func (r *Runner) ManifestStatus(projectID string) ports.ManifestStatus {
	status := ports.ManifestStatus{ProjectID: projectID}
	path, ok := r.cfg.Manifests[projectID]
	if !ok {
		return status
	}
	status.Configured, status.ManifestPath = true, path
	status.ScriptPath, _ = ScriptFor(path)
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, ok := r.last[projectID]; ok {
		status.LastRun = &last
	}
	return status
}

// RunManifest implements ports.ManifestPublisher: one run at a time per project, bounded by Timeout.
func (r *Runner) RunManifest(ctx context.Context, projectID, mode string) (ports.ManifestRun, error) {
	if mode != ports.ManifestCheck && mode != ports.ManifestPublish {
		return ports.ManifestRun{}, fmt.Errorf("kitpublish: mode must be CHECK or PUBLISH, got %q", mode)
	}
	manifest, ok := r.cfg.Manifests[projectID]
	if !ok {
		return ports.ManifestRun{}, ports.ErrManifestNotConfigured
	}
	script, err := ScriptFor(manifest)
	if err != nil {
		return ports.ManifestRun{}, err
	}
	r.mu.Lock()
	if r.busy[projectID] {
		r.mu.Unlock()
		return ports.ManifestRun{}, ports.ErrManifestRunBusy
	}
	r.busy[projectID] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.busy, projectID)
		r.mu.Unlock()
	}()

	args := []string{script, manifest}
	if mode == ports.ManifestCheck {
		args = append(args, "--check")
	}
	runCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, r.cfg.Python, args...)
	command.Dir = filepath.Dir(manifest)
	env := append(os.Environ(), r.cfg.Env...)
	if r.cfg.AwExecutable != "" {
		env = append(env, "AW="+r.cfg.AwExecutable)
	}
	command.Env = env
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	started := time.Now().UTC()
	runErr := command.Run()
	run := ports.ManifestRun{Mode: mode, StartedAt: started, DurationMs: time.Since(started).Milliseconds()}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		run.Succeeded = true
	case errors.As(runErr, &exitErr):
		run.ExitCode = exitErr.ExitCode()
	default:
		return ports.ManifestRun{}, fmt.Errorf("kitpublish: start %s: %w", r.cfg.Python, runErr)
	}
	if runCtx.Err() != nil {
		output.WriteString("\n[aw] the run was stopped after " + r.cfg.Timeout.String())
		run.Succeeded = false
	}
	text := output.Bytes()
	if len(text) > MaxOutputBytes {
		text, run.Truncated = text[len(text)-MaxOutputBytes:], true
	}
	redacted, _ := r.cfg.Matcher.Redact(text)
	run.Output = string(redacted)
	r.mu.Lock()
	r.last[projectID] = run
	r.mu.Unlock()
	return run, nil
}
