// cli_test.go is V6-15P's own transport: everything the terminal journey
// (stage_terminal_test.go) needs to drive `aw <resource> <action>` as a
// real, separate OS process against the SAME --db/--artifact-root/
// --workspace-root the stack's `aw serve`/`aw worker` already use — never
// an in-process function call, matching this package's own "opt-in real
// binaries, real processes" discipline build_test.go/stack_test.go already
// establish for serve/worker.
package v6accept

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cliInvocation is one `aw <resource> <action> ...` run: the raw process
// result, kept whole (not just stdout) so a failing assertion can show
// stderr (progress/diagnostics — never mixed into stdout, V6-15B's own
// "diagnostics stderr" rule) alongside it.
type cliInvocation struct {
	args     []string
	exitCode int
	stdout   []byte
	stderr   []byte
	err      error // non-nil only for a launch failure (binary missing, etc.), never a nonzero exit
}

// runCLI runs one `aw` one-shot invocation to completion (these are
// bounded, request/response commands — never `serve`/`worker`/`events
// watch`, which get their own runCLIStreaming below) against s's own
// installation: every call gets --db/--artifact-root/--workspace-root
// prepended so a caller never has to repeat them, exactly mirroring how
// `aw serve`/`aw worker` are started with the SAME three roots
// (stack.start, stack_test.go) — this is what makes a CLI invocation and
// the background serve+worker pair cooperate on one shared installation
// rather than accidentally opening a second, empty one.
//
// stdin may be nil (no input piped). A 60-second bound is generous for a
// one-shot command that does at most a handful of real DB transactions;
// anything slower than that is hung, not merely busy, and the test should
// fail loudly rather than hang the whole suite.
func runCLI(t *testing.T, s *stack, stdin []byte, args ...string) cliInvocation {
	t.Helper()
	full := append([]string{"--db", s.dbPath, "--artifact-root", s.artifactRoot, "--workspace-root", s.workspaceRoot}, args...)
	cmd := exec.Command(s.bin.aw, full...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return cliInvocation{args: args, exitCode: -1, err: err}
	}
	go func() { done <- cmd.Wait() }()

	select {
	case waitErr := <-done:
		exitCode := 0
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return cliInvocation{args: args, exitCode: -1, stdout: stdout.Bytes(), stderr: stderr.Bytes(), err: waitErr}
			}
		}
		return cliInvocation{args: args, exitCode: exitCode, stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("aw %s did not exit within 60s (stdout so far: %s; stderr so far: %s)",
			strings.Join(args, " "), tail(stdout.String(), 2000), tail(stderr.String(), 2000))
		return cliInvocation{}
	}
}

// requireOK fails the test with the full invocation (args, exit code, both
// streams) unless the process exited 0 — a single, consistent failure
// message shape every cliInvocation caller below reuses, instead of each
// one re-deriving its own.
func (c cliInvocation) requireOK(t *testing.T) cliInvocation {
	t.Helper()
	if c.err != nil {
		t.Fatalf("aw %s: launch failed: %v", strings.Join(c.args, " "), c.err)
	}
	if c.exitCode != 0 {
		t.Fatalf("aw %s: exit %d\n---- stdout ----\n%s\n---- stderr ----\n%s",
			strings.Join(c.args, " "), c.exitCode, tail(string(c.stdout), 2000), tail(string(c.stderr), 2000))
	}
	return c
}

// requireExit fails the test unless the process exited with exactly want —
// for the small number of places this journey deliberately drives a
// negative case (an unversioned mutation refused, for instance) and needs
// to prove it was refused for the RIGHT reason, not just "didn't succeed".
func (c cliInvocation) requireExit(t *testing.T, want int) cliInvocation {
	t.Helper()
	if c.err != nil {
		t.Fatalf("aw %s: launch failed: %v", strings.Join(c.args, " "), c.err)
	}
	if c.exitCode != want {
		t.Fatalf("aw %s: exit %d, want %d\n---- stdout ----\n%s\n---- stderr ----\n%s",
			strings.Join(c.args, " "), c.exitCode, want, tail(string(c.stdout), 2000), tail(string(c.stderr), 2000))
	}
	return c
}

// resultEnvelope mirrors cli.ResultEnvelope field-for-field (this package
// deliberately never imports internal/delivery/cli — the same "public
// surface only" discipline client_test.go's apiClient already keeps
// against internal/delivery/httpapi) — the one JSON document a mutating
// `aw` command writes to stdout.
type resultEnvelope struct {
	IdempotencyKey string          `json:"idempotencyKey"`
	Replayed       bool            `json:"replayed"`
	Result         json.RawMessage `json:"result"`
}

// decodeEnvelope parses c.stdout as a resultEnvelope and decodes its own
// Result field into into — the standard shape for any MUTATING `aw`
// command's stdout (`run start`, `work-item create`, `approval resolve`,
// ...). Calling this on a QUERY command's output (decodeQuery's job
// instead) fails loudly rather than silently returning a zero value.
func (c cliInvocation) decodeEnvelope(t *testing.T, into any) resultEnvelope {
	t.Helper()
	var envelope resultEnvelope
	if err := json.Unmarshal(c.stdout, &envelope); err != nil {
		t.Fatalf("aw %s: decode ResultEnvelope: %v\nstdout: %s", strings.Join(c.args, " "), err, tail(string(c.stdout), 2000))
	}
	if envelope.IdempotencyKey == "" {
		t.Fatalf("aw %s: ResultEnvelope carries no idempotencyKey: %s", strings.Join(c.args, " "), c.stdout)
	}
	if into != nil {
		if err := json.Unmarshal(envelope.Result, into); err != nil {
			t.Fatalf("aw %s: decode ResultEnvelope.result: %v\nresult: %s", strings.Join(c.args, " "), err, envelope.Result)
		}
	}
	return envelope
}

// decodeQuery parses c.stdout directly into into — the shape a read-only
// `aw` command (`work-item show`, `run show`, `evidence list`, ...) writes:
// no envelope, no idempotency key, the query result document itself.
func (c cliInvocation) decodeQuery(t *testing.T, into any) {
	t.Helper()
	if err := json.Unmarshal(c.stdout, into); err != nil {
		t.Fatalf("aw %s: decode query result: %v\nstdout: %s", strings.Join(c.args, " "), err, tail(string(c.stdout), 2000))
	}
}

// runCLIStreaming runs one long-lived, unbounded-stream `aw` command
// (`events watch` — its own NDJSON design, EncodeNDJSONLine's own doc
// comment: "unbounded stream, flushed incrementally", the deliberate
// opposite of every bounded command runCLI above handles) for exactly
// within, then terminates it and returns whatever complete lines it wrote
// before that deadline. This is not a failure/timeout the way runCLI's own
// 60s bound is — an unbounded stream is SUPPOSED to still be running when
// the caller stops watching; within is this test's own deliberate "watch
// for this long, then stop", not a symptom of the command hanging.
func runCLIStreaming(t *testing.T, s *stack, within time.Duration, args ...string) string {
	t.Helper()
	full := append([]string{"--db", s.dbPath, "--artifact-root", s.artifactRoot, "--workspace-root", s.workspaceRoot}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin.aw, full...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("aw %s: stdout pipe: %v", strings.Join(args, " "), err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("aw %s: start: %v", strings.Join(args, " "), err)
	}

	var collected bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		collected.WriteString(scanner.Text())
		collected.WriteByte('\n')
	}
	_ = cmd.Wait() // ctx's own deadline killed it; a nonzero exit from that is expected, not a failure.
	if collected.Len() == 0 {
		t.Fatalf("aw %s produced no output in %s (stderr: %s)", strings.Join(args, " "), within, tail(stderr.String(), 2000))
	}
	return collected.String()
}

// writeTempFile writes content to name inside dir and returns the full
// path — for the one place this journey needs a real file argument
// (`--file`) rather than piping stdin: `adapter probe`'s own stdout (the
// signed candidate token) becomes `adapter register --file`'s own input,
// and stdin is not reusable across two separate subprocess invocations.
func writeTempFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// readFile reads path and fails the test on any error — the one place
// this journey needs to see raw content `aw repository-workspace source
// --output` wrote to disk.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
