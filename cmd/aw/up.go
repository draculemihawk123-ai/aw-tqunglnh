package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

// runUp is `aw up` (V12-08, UX-08): `aw serve` and `aw worker` in ONE process and one terminal, started together, stopped
// together (Ctrl+C, or either of them ending), writing to the same stdout/stderr. Before, the operator needed two terminal
// windows, and starting both at once on a fresh database failed one of them with "duplicate column name" (the migration race
// fixed in internal/adapters/sqlite: the migration now re-checks under the write lock).
//
// The flags both commands share are given once: --db, --artifact-root, --workspace-root, --claude-executable,
// --codex-executable, --env-allowlist. Anything only one of them takes goes through --serve-args / --worker-args, a
// space-separated list (no quoting), e.g. --worker-args "--claude-permission-mode acceptEdits --worker-concurrency 2".
func runUp(arguments []string, stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return up(ctx, arguments, stdout)
}

func up(ctx context.Context, arguments []string, stdout io.Writer) error {
	serveArgs, workerArgs, err := upArguments(arguments)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &lockedWriter{w: stdout}
	type ended struct {
		name string
		err  error
	}
	results := make(chan ended, 2)
	var wg sync.WaitGroup
	for _, part := range []struct {
		name string
		run  func(context.Context, []string, io.Writer) error
		args []string
	}{{"serve", serve, serveArgs}, {"worker", worker, workerArgs}} {
		wg.Add(1)
		go func(name string, run func(context.Context, []string, io.Writer) error, args []string) {
			defer wg.Done()
			err := run(runCtx, args, out)
			results <- ended{name, err}
			cancel() // the other one stops too
		}(part.name, part.run, part.args)
	}
	wg.Wait()
	close(results)
	var errs []error
	for r := range results {
		if r.err != nil && !(errors.Is(r.err, context.Canceled) && ctx.Err() == nil) {
			errs = append(errs, fmt.Errorf("aw up: %s: %w", r.name, r.err))
		}
	}
	return errors.Join(errs...)
}

// upArguments splits `aw up`'s flags into the argument lists of serve and worker.
func upArguments(arguments []string) (serveArgs, workerArgs []string, err error) {
	flags := flag.NewFlagSet("up", flag.ContinueOnError)
	shared := []string{"db", "artifact-root", "workspace-root", "claude-executable", "codex-executable", "env-allowlist"}
	values := make(map[string]*string, len(shared))
	for _, name := range shared {
		values[name] = flags.String(name, "", "passed to both `aw serve` and `aw worker` (see their --"+name+")")
	}
	serveExtra := flags.String("serve-args", "", "extra flags for `aw serve` only, space-separated (e.g. \"--port 8787\")")
	workerExtra := flags.String("worker-args", "", "extra flags for `aw worker` only, space-separated (e.g. \"--claude-permission-mode acceptEdits\")")
	if err := flags.Parse(arguments); err != nil {
		return nil, nil, usageError{err}
	}
	if flags.NArg() > 0 {
		return nil, nil, usageError{fmt.Errorf("unexpected argument %q: aw up takes flags only", flags.Arg(0))}
	}
	for _, name := range shared {
		if v := strings.TrimSpace(*values[name]); v != "" {
			serveArgs = append(serveArgs, "--"+name, v)
			workerArgs = append(workerArgs, "--"+name, v)
		}
	}
	return append(serveArgs, strings.Fields(*serveExtra)...), append(workerArgs, strings.Fields(*workerExtra)...), nil
}

// lockedWriter serializes the two commands' writes to one stdout.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
