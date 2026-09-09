package fake

import (
	"context"
	"io"
	"os"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ProcessSupervisor is a scripted ports.ProcessSupervisor for tests that
// exercise a caller's own argv/cwd/env wiring (CommandNodeExecutor,
// command_node_executor_test.go) without spawning a real process — the
// same testing boundary fake.AgentExecutor already draws for
// AgentNodeExecutor's own tests: a real internal/adapters/process.Supervisor
// is exhaustively tested on its own (supervisor_test.go); this fake lets a
// caller's own bridge logic be tested in isolation from that.
type ProcessSupervisor struct {
	Result ports.ProcessResult
	Err    error
	Stdout string
	Stderr string
	// Calls records every ProcessSpec Run received, in order.
	Calls []ports.ProcessSpec
	// ExecutableContent records, for each call, the real file content
	// found at that call's own spec.Executable — proving a caller's own
	// script-materialization step actually wrote real content to a real
	// file before ever invoking Run, without this fake needing to spawn
	// anything itself to observe it.
	ExecutableContent []string
}

var _ ports.ProcessSupervisor = (*ProcessSupervisor)(nil)

func (s *ProcessSupervisor) Run(_ context.Context, spec ports.ProcessSpec, stdout, stderr io.Writer) (ports.ProcessResult, error) {
	s.Calls = append(s.Calls, spec)
	content, _ := os.ReadFile(spec.Executable)
	s.ExecutableContent = append(s.ExecutableContent, string(content))
	if stdout != nil {
		_, _ = stdout.Write([]byte(s.Stdout))
	}
	if stderr != nil {
		_, _ = stderr.Write([]byte(s.Stderr))
	}
	return s.Result, s.Err
}

func (s *ProcessSupervisor) Cancel(context.Context, ports.ProcessID) error {
	return nil
}
