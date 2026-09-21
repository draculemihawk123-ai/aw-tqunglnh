package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/artifactsweep"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// declaredJobKinds returns every durable job kind declared as a
// `const ...JobKind = "KIND"` anywhere under internal/app, mapped to where it
// is declared. It parses source rather than listing kinds by hand so a kind
// added later is picked up without anyone editing this file.
func declaredJobKinds(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "internal", "app")
	fset := token.NewFileSet()
	kinds := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				valueSpec := spec.(*ast.ValueSpec)
				for i, name := range valueSpec.Names {
					if !strings.HasSuffix(name.Name, "JobKind") || i >= len(valueSpec.Values) {
						continue
					}
					literal, ok := valueSpec.Values[i].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					value, err := strconv.Unquote(literal.Value)
					if err != nil {
						return err
					}
					kinds[value] = filepath.ToSlash(path) + ":" + name.Name
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan job kinds: %v", err)
	}
	return kinds
}

type workerDirs struct{ db, artifacts, workspaces string }

func newWorkerDirs(t *testing.T) workerDirs {
	t.Helper()
	root := t.TempDir()
	dirs := workerDirs{
		db:         filepath.Join(root, "agentkit.db"),
		artifacts:  filepath.Join(root, "artifacts"),
		workspaces: filepath.Join(root, "workspaces"),
	}
	if err := os.MkdirAll(dirs.artifacts, 0o755); err != nil {
		t.Fatalf("create artifact root: %v", err)
	}
	return dirs
}

func (d workerDirs) options(workerID string) workerOptions {
	return workerOptions{
		dbPath: d.db, artifactRoot: d.artifacts, workspaceRoot: d.workspaces, workerID: workerID,
		concurrency: 2, leaseTTL: 6 * time.Second, leaseHeartbeat: 2 * time.Second,
		pollInterval: 20 * time.Millisecond, shutdownGrace: 5 * time.Second,
		projectionInterval: 50 * time.Millisecond, completionInterval: 50 * time.Millisecond,
		reaperInterval: 50 * time.Millisecond, sweepInterval: 50 * time.Millisecond,
	}
}

func (d workerDirs) args(workerID string) []string {
	return []string{
		"--db", d.db, "--artifact-root", d.artifacts, "--workspace-root", d.workspaces,
		"--worker-id", workerID, "--worker-concurrency", "2",
		"--lease-ttl", "6s", "--lease-heartbeat", "2s", "--poll-interval", "20ms",
		"--shutdown-grace", "5s", "--projection-interval", "50ms", "--completion-interval", "50ms",
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required for this end-to-end test: %v", err)
	}
}

func createGitRepository(t *testing.T, path string) {
	t.Helper()
	run := func(dir string, args ...string) {
		t.Helper()
		if dir != "" {
			args = append([]string{"-C", dir}, args...)
		}
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("", "init", "--initial-branch=main", path)
	run(path, "config", "user.name", "Agent Kit Test")
	run(path, "config", "user.email", "agent-kit@example.invalid")
	if err := os.WriteFile(filepath.Join(path, "service.txt"), []byte("v0\n"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	run(path, "add", "--", "service.txt")
	run(path, "commit", "-m", "initial fixture")
}

// startWorker runs the real `aw worker` entry point and returns a stop func
// that cancels it and returns whatever worker returned.
func startWorker(t *testing.T, dirs workerDirs, workerID string) (stdout *syncBuffer, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stdout = &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- worker(ctx, dirs.args(workerID), stdout) }()
	stopped := false
	stop = func() error {
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(30 * time.Second):
			t.Fatal("worker did not return within 30s of cancellation")
			return nil
		}
	}
	t.Cleanup(func() { _ = stop() })
	return stdout, stop
}

func waitUntil(t *testing.T, what string, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// TestWorkerRegistersAHandlerForEveryDurableJobKind is the guard for the gap
// V6-14 closed: durable jobs could be enqueued for years with no production
// code registered to run them. Every declared job kind must have a handler in
// the registry the real worker builds.
func TestWorkerRegistersAHandlerForEveryDurableJobKind(t *testing.T) {
	requireGit(t)
	kinds := declaredJobKinds(t)
	if len(kinds) < 17 {
		t.Fatalf("scan found only %d job kinds (%v), want at least the 17 known at V6-14 — the scan itself is broken", len(kinds), kinds)
	}

	dirs := newWorkerDirs(t)
	assembled, err := assembleWorker(context.Background(), dirs.options("aw-worker-inventory"))
	if err != nil {
		t.Fatalf("assembleWorker: %v", err)
	}
	defer assembled.close()

	for kind, declaredAt := range kinds {
		if _, ok := assembled.registry.Lookup(kind); !ok {
			t.Errorf("durable job kind %q (%s) has no handler registered by `aw worker` — a job of this kind would be enqueued and never run", kind, declaredAt)
		}
	}
}

// TestWorkerRunsARegisteredRepositoryProbeEndToEnd drives one job through the
// real binary path: the command enqueues a REPOSITORY_PROBE job, `aw worker`
// claims it, the real git prober runs, and the repository becomes ACTIVE. The
// same worker also creates the project's projection checkpoint, which no
// production code did before.
func TestWorkerRunsARegisteredRepositoryProbeEndToEnd(t *testing.T) {
	requireGit(t)
	dirs := newWorkerDirs(t)
	repoPath := filepath.Join(t.TempDir(), "origin-repo")
	createGitRepository(t, repoPath)

	ctx := context.Background()
	store, uow, err := openDefinitionDB(ctx, dirs.db)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	const projectID, repositoryID = "worker-e2e-project", "worker-e2e-repo"
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "worker e2e"})
		return err
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	registered, err := catalog.RegisterRepository(ctx, uow, idsource.Random{}, ports.Command{
		ID: "cmd-worker-e2e", IdempotencyKey: "worker-e2e-register", Actor: "operator-1", CorrelationID: "corr-worker-e2e",
		Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-worker-e2e-register",
	}, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID, RemoteLocator: repoPath, DefaultRef: "main",
	})
	if err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	if registered.ProbeJobID == "" {
		t.Fatalf("RegisterRepository enqueued no probe job: %+v", registered)
	}

	stdout, stop := startWorker(t, dirs, "aw-worker-e2e")
	waitUntil(t, "worker to announce readiness", 15*time.Second, func() bool {
		return strings.Contains(stdout.String(), `"workerId":"aw-worker-e2e"`)
	})

	waitUntil(t, "the probe job to run and the repository to become ACTIVE", 30*time.Second, func() bool {
		var status project.RepositoryStatus
		_ = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			repo, err := tx.Catalog().GetRepository(ctx, repositoryID)
			status = repo.Status
			return err
		})
		return status == project.RepositoryActive
	})

	waitUntil(t, "the live projection consumer to checkpoint the project", 15*time.Second, func() bool {
		var checkpoint ports.ProjectionCheckpoint
		found := false
		_ = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			generation, ok, err := tx.Projections().GetActiveGeneration(ctx, projectID, projection.ProjectionName)
			if err != nil || !ok {
				return err
			}
			checkpoint, err = tx.Projections().GetProjectionCheckpoint(ctx, projectID, projection.ProjectionName, generation)
			found = err == nil
			return err
		})
		return found && checkpoint.Status == ports.ProjectionLive && checkpoint.Cursor > 0
	})

	if err := stop(); err != nil {
		t.Fatalf("worker did not shut down cleanly: %v", err)
	}
}

func TestWorkerShutsDownCleanlyWhenIdle(t *testing.T) {
	requireGit(t)
	dirs := newWorkerDirs(t)
	stdout, stop := startWorker(t, dirs, "aw-worker-idle")
	waitUntil(t, "worker to announce readiness", 15*time.Second, func() bool {
		return strings.Contains(stdout.String(), `"workerId":"aw-worker-idle"`)
	})
	begin := time.Now()
	if err := stop(); err != nil {
		t.Fatalf("idle worker returned an error on shutdown: %v", err)
	}
	if elapsed := time.Since(begin); elapsed > 10*time.Second {
		t.Errorf("idle worker took %s to shut down, want a prompt return", elapsed)
	}
}

// TestWorkerDoesNotSpinItsSelfReschedulingControlJobsWhenIdle is the guard for
// a production bug the first real `aw worker` exposed: RECOVERY_REAPER and
// ARTIFACT_SWEEP re-enqueued their successor as claimable immediately, so an
// idle worker ran the sweep ~200 times a second and appended one
// ARTIFACT_SWEEP_COMPLETED journal event each time, without bound. With the
// default intervals the startup sweep runs once and its successor is an hour
// away, so an idle worker appends at most that one event.
func TestWorkerDoesNotSpinItsSelfReschedulingControlJobsWhenIdle(t *testing.T) {
	requireGit(t)
	dirs := newWorkerDirs(t)
	stdout, stop := startWorker(t, dirs, "aw-worker-no-spin")
	waitUntil(t, "worker to announce readiness", 15*time.Second, func() bool {
		return strings.Contains(stdout.String(), `"workerId":"aw-worker-no-spin"`)
	})
	time.Sleep(3 * time.Second)
	if err := stop(); err != nil {
		t.Fatalf("worker returned an error on shutdown: %v", err)
	}

	ctx := context.Background()
	store, uow, err := openDefinitionDB(ctx, dirs.db)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()
	sweeps := 0
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		events, err := tx.Events().ScanJournal(ctx, 0, 100000)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.EventType == artifactsweep.ArtifactSweepCompletedEventType {
				sweeps++
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("scan journal: %v", err)
	}
	if sweeps > 1 {
		t.Fatalf("an idle worker recorded %d %s events in ~3s, want at most 1 (the startup sweep): the control job is spinning",
			sweeps, artifactsweep.ArtifactSweepCompletedEventType)
	}
}

func TestWorkerRejectsMissingOrInvalidFlags(t *testing.T) {
	dirs := newWorkerDirs(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no flags", nil, "--db is required"},
		{"missing artifact root flag", []string{"--db", dirs.db, "--workspace-root", dirs.workspaces}, "--artifact-root is required"},
		{"missing workspace root flag", []string{"--db", dirs.db, "--artifact-root", dirs.artifacts}, "--workspace-root is required"},
		{"artifact root that does not exist", []string{"--db", dirs.db, "--artifact-root", filepath.Join(dirs.artifacts, "missing"), "--workspace-root", dirs.workspaces}, "is not an existing directory"},
		{"heartbeat not shorter than lease", append(dirs.args("aw-worker-bad"), "--lease-heartbeat", "10m"), "lease_heartbeat"},
		{"non-positive projection interval", append(dirs.args("aw-worker-bad"), "--projection-interval", "0s"), "--projection-interval must be positive"},
		{"non-positive completion interval", append(dirs.args("aw-worker-bad"), "--completion-interval", "0s"), "--completion-interval must be positive"},
		{"unknown flag", []string{"--not-a-real-flag"}, "not-a-real-flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"worker"}, tc.args...), &stdout, &stderr)
			if code != exitUsage {
				t.Errorf("exit code = %d, want %d (exitUsage); stderr = %q", code, exitUsage, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want it to mention %q", stderr.String(), tc.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty on a usage error", stdout.String())
			}
		})
	}
}
