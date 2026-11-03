package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ProcessStartFailedDiagnosticCode (V12-04, UX-18) is the DIAGNOSTIC event a COMMAND or MACHINE_GATE attempt records when its
// process could not be started at all, so the attempt's failure detail says why (chochuyennha P2-02: gate1's fourth attempt ended
// EXECUTION_FAILED with no evidence and no reason). The run timeline reads it back into failureDetail.
const ProcessStartFailedDiagnosticCode = "PROCESS_START_FAILED"

// windowsProcessStartFailureCodes are the NTSTATUS values Windows reports as a process's exit code when the process never
// really started: STATUS_DLL_INIT_FAILED (0xC0000142, a console-less worker), STATUS_DLL_NOT_FOUND (0xC0000135) and
// STATUS_ENTRYPOINT_NOT_FOUND (0xC0000139). Such an exit is the machine's fault, never the command's verdict.
var windowsProcessStartFailureCodes = map[int64]string{
	0xC0000142: "STATUS_DLL_INIT_FAILED: the process could not initialize (the worker may have lost its console)",
	0xC0000135: "STATUS_DLL_NOT_FOUND: a library the process needs was not found",
	0xC0000139: "STATUS_ENTRYPOINT_NOT_FOUND",
}

// processStartFailure reports whether a process run did not start at all, and why: a spawn error, or a Windows exit code that
// means the process could not initialize.
func processStartFailure(result ports.ProcessResult, runErr error) (string, bool) {
	if runErr != nil {
		return fmt.Sprintf("the process could not be started: %v", runErr), true
	}
	code := int64(uint32(result.ExitCode))
	if why, ok := windowsProcessStartFailureCodes[code]; ok {
		return fmt.Sprintf("the process exited %d (0x%X) before it ran: %s", result.ExitCode, code, why), true
	}
	return "", false
}

// recordProcessStartFailure appends the PROCESS_START_FAILED diagnostic to sink when the process did not start. Best effort:
// the attempt's transition never depends on it.
func recordProcessStartFailure(ctx context.Context, sink ports.AgentEventSink, attemptID string, sequence uint64, result ports.ProcessResult, runErr error) {
	why, failed := processStartFailure(result, runErr)
	if !failed {
		return
	}
	_ = sink.Accept(ctx, ports.AgentEvent{
		AttemptID: ports.ExecutionAttemptID(attemptID), Sequence: sequence, Kind: ports.AgentEventDiagnostic, ObservedAt: time.Now().UTC(),
		Diagnostic: &ports.AgentDiagnostic{Code: ProcessStartFailedDiagnosticCode, Message: why},
	})
}
