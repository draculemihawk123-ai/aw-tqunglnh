package workspacereconcile_test

// V12-30 (ADR-062): runapp against real sqlite, a real Git worktree, the real process supervisor and the real host adapter. The
// fixture lives in this package (realFixture); the code under test is internal/app/runapp.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/runapphost"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runapp"
)

type mapSecrets map[string]string

func (m mapSecrets) Resolve(_ context.Context, name string) (string, error) {
	if v, ok := m[name]; ok {
		return v, nil
	}
	return "", errors.New("no such secret")
}

func (m mapSecrets) ResolveScoped(ctx context.Context, _ []string, name string) (string, error) {
	return m.Resolve(ctx, name)
}

func runAppCommand(f *realFixture, key, typ string) ports.Command {
	cmd := f.adoptCommand(key, 0)
	cmd.Type, cmd.ExpectedVersion = typ, 0
	return cmd
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitSession(t *testing.T, f *realFixture, d runapp.Deps, want func(runapp.Status) bool) runapp.Status {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, err := runapp.GetStatus(context.Background(), d, f.projectID, string(f.rw.ID))
		if err != nil {
			t.Fatal(err)
		}
		if want(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out; status %+v", st.Session)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRunApp_StartRunsTheProfileRedactsItsLogAndStopKillsIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	f := newRealFixture(t, "runapp.db")
	ctx := context.Background()
	host := runapphost.New(t.TempDir())
	d := runapp.Deps{UnitOfWork: f.uow, Host: host}
	if err := os.MkdirAll(filepath.Join(f.workingDirectory(t), "web"), 0o755); err != nil {
		t.Fatal(err)
	}

	st, err := runapp.GetStatus(ctx, d, f.projectID, string(f.rw.ID))
	if err != nil || st.Profile.Profile != nil || len(st.StartBlockers) != 1 {
		t.Fatalf("status before a profile = %+v, %v", st, err)
	}
	if _, err := runapp.Start(ctx, d, runAppCommand(f, "start-0", "StartRunApp"), f.projectID, string(f.rw.ID)); !errors.Is(err, runapp.ErrNoProfile) {
		t.Fatalf("start without a profile: %v", err)
	}
	if _, err := runapp.SetProfile(ctx, d, runAppCommand(f, "bad", "SetRunAppProfile"), f.projectID, f.repositoryID,
		runapp.Profile{Processes: []runapp.Process{{Name: "x", Argv: []string{"sh"}, Dir: "../up"}}}, 0); !errors.Is(err, runapp.ErrInvalidProfile) {
		t.Fatalf("a dir outside the worktree: %v", err)
	}
	port := freePort(t)
	profile := runapp.Profile{Processes: []runapp.Process{
		{Name: "backend", Argv: []string{"sh", "-c", `echo "db=$DB_PASSWORD mode=$MODE"; pwd; exec sleep 60`}, Dir: "web", Port: port,
			Env: map[string]string{"MODE": "qa"}, SecretNames: []string{"DB_PASSWORD"}},
		{Name: "frontend", Argv: []string{"sh", "-c", "echo front; exec sleep 60"}},
	}, PreviewURL: "http://localhost:" + strconv.Itoa(port)}
	view, err := runapp.SetProfile(ctx, d, runAppCommand(f, "set", "SetRunAppProfile"), f.projectID, f.repositoryID, profile, 0)
	if err != nil || view.Version != 1 || view.Profile.StopAfterMinutes != runapp.DefaultStopAfterMinutes {
		t.Fatalf("set profile = %+v, %v", view, err)
	}
	if _, err := runapp.SetProfile(ctx, d, runAppCommand(f, "set-stale", "SetRunAppProfile"), f.projectID, f.repositoryID, profile, 0); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("a stale profile write: %v", err)
	}

	session, err := runapp.Start(ctx, d, runAppCommand(f, "start", "StartRunApp"), f.projectID, string(f.rw.ID))
	if err != nil || session.State != string(ports.RunAppRequested) {
		t.Fatalf("start = %+v, %v", session, err)
	}
	if again, err := runapp.Start(ctx, d, runAppCommand(f, "start", "StartRunApp"), f.projectID, string(f.rw.ID)); err != nil || again.ID != session.ID {
		t.Fatalf("a replayed start = %+v, %v", again, err)
	}
	if _, err := runapp.Start(ctx, d, runAppCommand(f, "start-2", "StartRunApp"), f.projectID, string(f.rw.ID)); !errors.Is(err, runapp.ErrAlreadyActive) {
		t.Fatalf("a second start: %v", err)
	}

	runner := runapp.NewRunner(runapp.RunnerDeps{
		UnitOfWork: f.uow, Host: host, Supervisor: process.NewSupervisor(), Workspaces: f.provider,
		Secrets: mapSecrets{"DB_PASSWORD": "hunter2-secret"}, Redact: redact.NewMatcher(), InheritedEnvironment: []string{"PATH"}, WorkerID: "w1",
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { runner.Run(runCtx, 20*time.Millisecond); close(done) }()
	defer func() { cancel(); <-done }()

	st = waitSession(t, f, d, func(s runapp.Status) bool {
		return s.Session != nil && s.Session.State == string(ports.RunAppRunning) && len(s.Logs) == 2 &&
			strings.Contains(s.Logs[0].Tail, "mode=qa") && strings.Contains(s.Logs[1].Tail, "front")
	})
	if strings.Contains(st.Logs[0].Tail, "hunter2-secret") || !strings.Contains(st.Logs[0].Tail, "/web") {
		t.Fatalf("backend log = %q (the secret must be redacted; it runs in web/)", st.Logs[0].Tail)
	}
	if st.Session.WorkerID != "w1" || len(st.StartBlockers) != 1 {
		t.Fatalf("running status = %+v", st)
	}

	if _, err := runapp.Stop(ctx, d, runAppCommand(f, "stop", "StopRunApp"), f.projectID, string(f.rw.ID)); err != nil {
		t.Fatal(err)
	}
	st = waitSession(t, f, d, func(s runapp.Status) bool { return s.Session != nil && s.Session.State == string(ports.RunAppStopped) })
	if st.Session.Problem != "stopped on request" || len(st.StartBlockers) != 0 {
		t.Fatalf("stopped status = %+v", st)
	}
	if _, err := runapp.Stop(ctx, d, runAppCommand(f, "stop-2", "StopRunApp"), f.projectID, string(f.rw.ID)); !errors.Is(err, runapp.ErrNotActive) {
		t.Fatalf("stop when nothing runs: %v", err)
	}
}

func TestRunApp_APortInUseOrAnExitingProcessEndsTheSessionFailed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	f := newRealFixture(t, "runapp-fail.db")
	ctx := context.Background()
	host := runapphost.New(t.TempDir())
	d := runapp.Deps{UnitOfWork: f.uow, Host: host}
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port
	if _, err := runapp.SetProfile(ctx, d, runAppCommand(f, "set", "SetRunAppProfile"), f.projectID, f.repositoryID,
		runapp.Profile{Processes: []runapp.Process{{Name: "api", Argv: []string{"sh", "-c", "exec sleep 60"}, Port: port}}}, 0); err != nil {
		t.Fatal(err)
	}
	runner := runapp.NewRunner(runapp.RunnerDeps{UnitOfWork: f.uow, Host: host, Supervisor: process.NewSupervisor(), Workspaces: f.provider,
		Secrets: mapSecrets{}, Redact: redact.NewMatcher(), InheritedEnvironment: []string{"PATH"}, WorkerID: "w1"})
	if _, err := runapp.Start(ctx, d, runAppCommand(f, "start", "StartRunApp"), f.projectID, string(f.rw.ID)); err != nil {
		t.Fatal(err)
	}
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runner.Wait()
	st, _ := runapp.GetStatus(ctx, d, f.projectID, string(f.rw.ID))
	// The holder is named: this test process listens there.
	if st.Session.State != string(ports.RunAppFailed) || !strings.Contains(st.Session.Problem, "port "+strconv.Itoa(port)+" (api) is in use") ||
		(runtime.GOOS == "linux" && !strings.Contains(st.Session.Problem, fmt.Sprintf("(pid %d) holds it", os.Getpid()))) {
		t.Fatalf("session = %+v", st.Session)
	}

	// A process that exits by itself ends the session FAILED and kills the others.
	if _, err := runapp.SetProfile(ctx, d, runAppCommand(f, "set-2", "SetRunAppProfile"), f.projectID, f.repositoryID,
		runapp.Profile{Processes: []runapp.Process{
			{Name: "api", Argv: []string{"sh", "-c", "echo boom; exit 3"}},
			{Name: "web", Argv: []string{"sh", "-c", "exec sleep 60"}},
		}}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := runapp.Start(ctx, d, runAppCommand(f, "start-2", "StartRunApp"), f.projectID, string(f.rw.ID)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runner.Wait()
	st, _ = runapp.GetStatus(ctx, d, f.projectID, string(f.rw.ID))
	if st.Session.State != string(ports.RunAppFailed) || st.Session.Problem != "api exited (code 3); see its log" || time.Since(started) > 20*time.Second {
		t.Fatalf("session = %+v", st.Session)
	}

	// A secret that does not resolve ends it FAILED, naming the secret only.
	if _, err := runapp.SetProfile(ctx, d, runAppCommand(f, "set-3", "SetRunAppProfile"), f.projectID, f.repositoryID,
		runapp.Profile{Processes: []runapp.Process{{Name: "api", Argv: []string{"sh"}, SecretNames: []string{"MISSING"}}}}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := runapp.Start(ctx, d, runAppCommand(f, "start-3", "StartRunApp"), f.projectID, string(f.rw.ID)); err != nil {
		t.Fatal(err)
	}
	_ = runner.Tick(ctx)
	runner.Wait()
	st, _ = runapp.GetStatus(ctx, d, f.projectID, string(f.rw.ID))
	if st.Session.State != string(ports.RunAppFailed) || !strings.Contains(st.Session.Problem, "secret MISSING (api) cannot be resolved") {
		t.Fatalf("session = %+v", st.Session)
	}
}

func TestRunApp_RefusesAWriterAndStopsWhenOneAppears(t *testing.T) {
	f := newRealFixture(t, "runapp-writer.db")
	ctx := context.Background()
	d := runapp.Deps{UnitOfWork: f.uow}
	if _, err := runapp.SetProfile(ctx, d, runAppCommand(f, "set", "SetRunAppProfile"), f.projectID, f.repositoryID,
		runapp.Profile{Processes: []runapp.Process{{Name: "api", Argv: []string{"x"}}}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.SeedFixtureWriteLease(ctx, f.store, f.projectID, f.familyID, f.workItemID, f.repositoryID, string(f.rw.ID), f.rw.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := runapp.Start(ctx, d, runAppCommand(f, "start", "StartRunApp"), f.projectID, string(f.rw.ID)); !errors.Is(err, runapp.ErrWriterActive) {
		t.Fatalf("start with a writer: %v", err)
	}
	if _, err := runapp.GetStatus(ctx, d, "another-project", string(f.rw.ID)); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("another project's workspace: %v", err)
	}
	if _, err := runapp.GetProfile(ctx, d, "another-project", f.repositoryID); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("another project's repository: %v", err)
	}
}

func TestRunApp_AWorkerThatStartsAgainEndsWhatADeadWorkerLeft(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses process groups")
	}
	f := newRealFixture(t, "runapp-orphan.db")
	ctx := context.Background()
	root := t.TempDir()
	host := runapphost.New(root)
	d := runapp.Deps{UnitOfWork: f.uow, Host: host}
	if _, err := runapp.SetProfile(ctx, d, runAppCommand(f, "set", "SetRunAppProfile"), f.projectID, f.repositoryID,
		runapp.Profile{Processes: []runapp.Process{{Name: "api", Argv: []string{"sleep", "60"}}}}, 0); err != nil {
		t.Fatal(err)
	}
	session, err := runapp.Start(ctx, d, runAppCommand(f, "start", "StartRunApp"), f.projectID, string(f.rw.ID))
	if err != nil {
		t.Fatal(err)
	}
	// The dead worker: it took the session and started "api" in its own process group, then died hard.
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		s, err := tx.RunApp().GetRunAppSession(ctx, session.ID)
		if err != nil {
			return err
		}
		v := s.Version
		s.State, s.WorkerID = ports.RunAppRunning, "worker-dead"
		_, err = tx.RunApp().UpdateRunAppSession(ctx, s, v)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	orphan := exec.Command("sleep", "60")
	orphan.SysProcAttr = ownProcessGroup()
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = orphan.Wait(); close(exited) }()
	t.Cleanup(func() { _ = orphan.Process.Kill() })
	if err := host.RecordProcess(session.ID, "api", orphan.Process.Pid); err != nil {
		t.Fatal(err)
	}
	// A worker record whose process is gone: a pid with another start time.
	if err := os.WriteFile(filepath.Join(root, "run-app", session.ID, "aw.worker.pid"), []byte(fmt.Sprintf("%d\nnot-its-start-time\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if !host.WorkerGone(session.ID) {
		t.Fatal("the recorded worker should be gone")
	}

	runner := runapp.NewRunner(runapp.RunnerDeps{UnitOfWork: f.uow, Host: host, Supervisor: process.NewSupervisor(), Workspaces: f.provider,
		Secrets: mapSecrets{}, Redact: redact.NewMatcher(), WorkerID: "worker-new"})
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the orphan was not ended")
	}
	st, _ := runapp.GetStatus(ctx, d, f.projectID, string(f.rw.ID))
	if st.Session.State != string(ports.RunAppFailed) || !strings.Contains(st.Session.Problem, "worker worker-dead stopped") ||
		!strings.Contains(st.Session.Problem, fmt.Sprintf("api (pid %d)", orphan.Process.Pid)) {
		t.Fatalf("session = %+v", st.Session)
	}
}
