package runapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Runner timings.
const (
	DefaultTickInterval = 2 * time.Second
	stopGrace           = 5 * time.Second
	logLimitBytes       = 8 << 20
	// orphanAfter is how long after its deadline a session another worker took is closed FAILED (that worker is gone).
	orphanAfter = 10 * time.Minute
)

// WorkspaceDirectories gives a worktree's directory (the Git workspace provider).
type WorkspaceDirectories interface {
	WorkingDirectory(ctx context.Context, handle ports.WorkspaceHandle) (string, error)
}

// RunnerDeps is what the worker's runner needs.
type RunnerDeps struct {
	UnitOfWork ports.UnitOfWork
	Host       ports.RunAppHost
	Supervisor ports.ProcessSupervisor
	Workspaces WorkspaceDirectories
	// Secrets resolves a profile's secret names: the repository store, then the worker's environment (ADR-061).
	Secrets ports.ScopedSecretResolver
	// Redact is the worker's matcher; a session adds its secret values to it for its logs.
	Redact redact.Matcher
	// InheritedEnvironment is the worker's --env-allowlist (PATH, HOME, … a dev server needs).
	InheritedEnvironment []string
	WorkerID             string
	Now                  func() time.Time
	// Log reports a runner problem (nil: dropped).
	Log func(message string, fields map[string]any)
}

// Runner runs the sessions of one worker.
type Runner struct {
	d       RunnerDeps
	mu      sync.Mutex
	running map[string]*runningSession
	wg      sync.WaitGroup
}

type runningSession struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	// stopReason is set when the runner (not a process exit) ends the session; the session then ends STOPPED.
	stopReason string
}

func (r *runningSession) stop(reason string) {
	r.mu.Lock()
	if r.stopReason == "" {
		r.stopReason = reason
	}
	r.mu.Unlock()
	r.cancel()
}

func (r *runningSession) reason() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopReason
}

// NewRunner builds a runner.
func NewRunner(d RunnerDeps) *Runner {
	return &Runner{d: d, running: map[string]*runningSession{}}
}

func (r *Runner) now() time.Time {
	if r.d.Now != nil {
		return r.d.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Runner) log(message string, fields map[string]any) {
	if r.d.Log != nil {
		r.d.Log(message, fields)
	}
}

// Run ticks until ctx ends, then stops every session it runs (killing each process tree) and waits for them.
func (r *Runner) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultTickInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.Tick(ctx); err != nil && ctx.Err() == nil {
			r.log("run app tick failed", map[string]any{"error": err.Error()})
		}
		select {
		case <-ctx.Done():
			r.StopAll("the worker stopped")
			r.Wait()
			return
		case <-ticker.C:
		}
	}
}

// StopAll stops every session this runner runs.
func (r *Runner) StopAll(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.running {
		s.stop(reason)
	}
}

// Wait waits for every session goroutine.
func (r *Runner) Wait() { r.wg.Wait() }

var activeStates = []ports.RunAppSessionState{ports.RunAppRequested, ports.RunAppRunning, ports.RunAppStopRequested}

// Tick claims requested sessions and stops the ones that must stop.
func (r *Runner) Tick(ctx context.Context) error {
	var sessions []ports.RunAppSession
	if err := r.d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		sessions, err = tx.RunApp().ListRunAppSessionsInStates(ctx, activeStates)
		return err
	}); err != nil {
		return err
	}
	now := r.now()
	for _, s := range sessions {
		r.mu.Lock()
		mine := r.running[s.ID]
		r.mu.Unlock()
		switch {
		case s.State == ports.RunAppRequested:
			r.claim(ctx, s)
		case s.WorkerID == r.d.WorkerID && mine == nil:
			// This worker restarted while it ran, or the goroutine ended without closing it: end what is left of its processes.
			r.finish(ctx, s.ID, ports.RunAppFailed, "the worker restarted while the application ran"+r.reaped(s.ID))
		case s.WorkerID == r.d.WorkerID:
			if reason := r.mustStop(ctx, s, now); reason != "" {
				mine.stop(reason)
			}
		case s.WorkerID != r.d.WorkerID && r.d.Host != nil && r.d.Host.WorkerGone(s.ID):
			// The worker that ran it is gone (killed, crashed): its processes may still hold the ports.
			r.finish(ctx, s.ID, ports.RunAppFailed, fmt.Sprintf("worker %s stopped while the application ran", s.WorkerID)+r.reaped(s.ID))
		case now.After(s.DeadlineAt.Add(orphanAfter)):
			r.finish(ctx, s.ID, ports.RunAppFailed, fmt.Sprintf("worker %s did not stop it at its deadline (that worker is gone)", s.WorkerID))
		}
	}
	return nil
}

// reaped ends the processes a dead worker left for the session and says which.
func (r *Runner) reaped(sessionID string) string {
	if r.d.Host == nil {
		return ""
	}
	if killed := r.d.Host.ReapSession(sessionID); len(killed) > 0 {
		return "; ended the processes it left: " + strings.Join(killed, ", ")
	}
	return ""
}

func (r *Runner) mustStop(ctx context.Context, s ports.RunAppSession, now time.Time) string {
	if s.State == ports.RunAppStopRequested {
		return "stopped on request"
	}
	if !now.Before(s.DeadlineAt) {
		return "stopped automatically at its deadline"
	}
	var reason string
	_ = r.d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, s.RepositoryWorkspaceID)
		if err != nil {
			reason = "the repository workspace is gone"
			return nil
		}
		if record.Workspace.State != workspace.RepositoryWorkspaceReady {
			reason = fmt.Sprintf("stopped: the repository workspace is %s", record.Workspace.State)
			return nil
		}
		if writer, err := tx.Work().HasActiveWriteLease(ctx, []string{s.RepositoryWorkspaceID}); err == nil && writer {
			reason = "stopped: an attempt took the worktree's write lease"
		}
		return nil
	})
	return reason
}

func (r *Runner) claim(ctx context.Context, s ports.RunAppSession) {
	now := r.now()
	claimed := s
	claimed.State, claimed.WorkerID, claimed.StartedAt = ports.RunAppRunning, r.d.WorkerID, &now
	if err := r.d.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		claimed, err = tx.RunApp().UpdateRunAppSession(ctx, claimed, s.Version)
		return err
	}); err != nil {
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			r.log("run app claim failed", map[string]any{"session": s.ID, "error": err.Error()})
		}
		return // another worker took it, or it was stopped
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	rs := &runningSession{cancel: cancel}
	r.mu.Lock()
	r.running[s.ID] = rs
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer cancel()
		state, problem := r.execute(runCtx, claimed, rs)
		r.finish(context.WithoutCancel(ctx), claimed.ID, state, problem)
		r.mu.Lock()
		delete(r.running, claimed.ID)
		r.mu.Unlock()
	}()
}

// finish closes a session (re-reading it until its version matches); a session already ended is left as it is.
func (r *Runner) finish(ctx context.Context, id string, state ports.RunAppSessionState, problem string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < 5; attempt++ {
		err := r.d.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			s, err := tx.RunApp().GetRunAppSession(ctx, id)
			if err != nil || !s.State.Active() {
				return err
			}
			now := r.now()
			expected := s.Version
			s.State, s.EndedAt, s.Problem = state, &now, problem
			_, err = tx.RunApp().UpdateRunAppSession(ctx, s, expected)
			return err
		})
		if err == nil {
			return
		}
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			r.log("run app close failed", map[string]any{"session": id, "error": err.Error()})
			return
		}
	}
}

type prepared struct {
	profile Profile
	dir     string
	repo    string
}

func (r *Runner) prepare(ctx context.Context, s ports.RunAppSession) (prepared, error) {
	var p prepared
	var locator string
	if err := r.d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		rec, err := tx.RunApp().GetRunAppProfile(ctx, s.RepositoryID)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNoProfile, err)
		}
		if err := json.Unmarshal([]byte(rec.Document), &p.profile); err != nil {
			return err
		}
		ws, err := tx.Work().GetRepositoryWorkspaceByID(ctx, s.RepositoryWorkspaceID)
		if err != nil {
			return err
		}
		locator = ws.Workspace.Locator
		return nil
	}); err != nil {
		return p, err
	}
	handle, err := ports.NewWorkspaceHandle(locator)
	if err != nil {
		return p, err
	}
	if r.d.Workspaces == nil {
		return p, errors.New("this worker has no Git workspace provider")
	}
	if p.dir, err = r.d.Workspaces.WorkingDirectory(ctx, handle); err != nil {
		return p, fmt.Errorf("the worktree cannot be opened: %w", err)
	}
	p.repo = s.RepositoryID
	return p, nil
}

type exit struct {
	name   string
	result ports.ProcessResult
	err    error
}

// execute runs the profile until a process exits or rs is stopped; it returns how the session ends.
func (r *Runner) execute(ctx context.Context, s ports.RunAppSession, rs *runningSession) (ports.RunAppSessionState, string) {
	p, err := r.prepare(ctx, s)
	if err != nil {
		return ports.RunAppFailed, err.Error()
	}
	for _, proc := range p.profile.Processes {
		if proc.Port != 0 && r.d.Host.PortInUse(proc.Port) {
			holder := "another program"
			if who := r.d.Host.PortHolder(proc.Port); who != "" {
				holder = who
			}
			return ports.RunAppFailed, fmt.Sprintf("port %d (%s) is in use: %s holds it — stop it, then start again", proc.Port, proc.Name, holder)
		}
	}
	if err := r.d.Host.RecordWorker(s.ID); err != nil {
		r.log("run app: cannot record the worker of the session", map[string]any{"session": s.ID, "error": err.Error()})
	}
	envs := make([]map[string]string, len(p.profile.Processes))
	var secretValues []string
	for i, proc := range p.profile.Processes {
		env := map[string]string{}
		for k, v := range proc.Env {
			env[k] = v
		}
		for _, name := range proc.SecretNames {
			if r.d.Secrets == nil {
				return ports.RunAppFailed, "this worker has no secret resolver"
			}
			value, err := r.d.Secrets.ResolveScoped(ctx, []string{p.repo}, name)
			if err != nil {
				return ports.RunAppFailed, fmt.Sprintf("secret %s (%s) cannot be resolved: set it in the repository's Secrets", name, proc.Name)
			}
			env[name] = value
			secretValues = append(secretValues, value)
		}
		envs[i] = env
	}
	matcher := r.d.Redact.WithSecrets(secretValues...)
	timeout := time.Until(s.DeadlineAt) + time.Minute
	if timeout <= time.Minute {
		return ports.RunAppStopped, "stopped automatically at its deadline"
	}
	procCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()
	exits := make(chan exit, len(p.profile.Processes))
	var logs []io.Closer
	started := 0
	for i, proc := range p.profile.Processes {
		logFile, err := r.d.Host.OpenLog(s.ID, proc.Name)
		if err != nil {
			cancelAll()
			r.drain(exits, started)
			closeAll(logs)
			return ports.RunAppFailed, fmt.Sprintf("the log of %s cannot be opened: %v", proc.Name, err)
		}
		w := newRedactingWriter(logFile, matcher)
		logs = append(logs, w)
		spec := ports.ProcessSpec{
			ID: ports.ProcessID("runapp/" + s.ID + "/" + proc.Name), Executable: proc.Argv[0], Argv: proc.Argv[1:],
			WorkingDirectory: filepath.Join(p.dir, filepath.FromSlash(proc.Dir)), Environment: envs[i],
			InheritedEnvironment: r.d.InheritedEnvironment, Timeout: timeout, GracePeriod: stopGrace, OutputLimitBytes: logLimitBytes,
			KillWithParent: true,
		}
		name := proc.Name
		spec.OnStarted = func(pid int) {
			if err := r.d.Host.RecordProcess(s.ID, name, pid); err != nil {
				r.log("run app: cannot record a process", map[string]any{"session": s.ID, "process": name, "error": err.Error()})
			}
		}
		started++
		go func(name string) {
			result, err := r.d.Supervisor.Run(procCtx, spec, w, w)
			exits <- exit{name: name, result: result, err: err}
		}(proc.Name)
	}
	first := <-exits
	cancelAll() // kill every other process tree
	rest := r.drain(exits, started-1)
	closeAll(logs)
	if reason := rs.reason(); reason != "" {
		return ports.RunAppStopped, reason
	}
	problem := describeExit(first)
	for _, e := range rest {
		if !e.result.TreeQuiesced && e.err == nil {
			problem += fmt.Sprintf("; %s's process tree may still be running", e.name)
		}
	}
	if first.err == nil && first.result.TimedOut {
		return ports.RunAppStopped, "stopped automatically at its deadline"
	}
	redacted, _ := matcher.Redact([]byte(problem))
	return ports.RunAppFailed, string(redacted)
}

func (r *Runner) drain(exits chan exit, n int) []exit {
	out := make([]exit, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, <-exits)
	}
	return out
}

func describeExit(e exit) string {
	if e.err != nil {
		return fmt.Sprintf("%s did not start: %v", e.name, e.err)
	}
	return fmt.Sprintf("%s exited (code %d); see its log", e.name, e.result.ExitCode)
}

func closeAll(cs []io.Closer) {
	for _, c := range cs {
		_ = c.Close()
	}
}

// redactingWriter redacts whole lines (a secret split across two writes is still caught); stdout and stderr share it.
type redactingWriter struct {
	mu      sync.Mutex
	w       io.WriteCloser
	matcher redact.Matcher
	buf     bytes.Buffer
}

const maxPendingLine = 16 << 10

func newRedactingWriter(w io.WriteCloser, m redact.Matcher) *redactingWriter {
	return &redactingWriter{w: w, matcher: m}
}

func (r *redactingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.Write(p)
	for {
		data := r.buf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if r.buf.Len() > maxPendingLine {
				r.flushLocked()
			}
			return len(p), nil
		}
		line := string(data[:i+1])
		r.buf.Next(i + 1)
		if _, err := r.w.Write(r.redact(line)); err != nil {
			return len(p), nil // a log that cannot be written must not stop the application
		}
	}
}

func (r *redactingWriter) redact(s string) []byte {
	out, _ := r.matcher.Redact([]byte(s))
	return out
}

func (r *redactingWriter) flushLocked() {
	if r.buf.Len() == 0 {
		return
	}
	_, _ = r.w.Write(r.redact(r.buf.String()))
	r.buf.Reset()
}

func (r *redactingWriter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushLocked()
	return r.w.Close()
}
