package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// V9-03 at the classify level: the provider adapters report a wrong terminal
// outcome marker (an outcome outside the allowed set, a duplicate, a malformed
// one) as a Go error wrapping ports.ErrOutcomeMarkerRejected next to their own
// protocol error. It used to be answered with PROVIDER_UNAVAILABLE (row 1 of
// the locked mapping table), which blames the provider for the agent's wrong
// answer; it now ends exactly like a MISSING marker on a node with a choice:
// FAILED / OUTCOME_REJECTED / VALIDATION_FAILED. The checks above it (lease
// lost, quiescence) keep outranking it.

// errAdapterProtocol stands in for an adapter's own ErrProtocol: the real ones
// (claude.ErrProtocol, codex.ErrProtocol) are wrapped together with the
// sentinel, in the shape the adapters build.
var errAdapterProtocol = errors.New("invalid provider protocol")

func markerRejection(detail string) error {
	return fmt.Errorf("%w: %w: %s", errAdapterProtocol, ports.ErrOutcomeMarkerRejected, detail)
}

func TestAgentNodeExecutor_RejectedOutcomeMarkerFromAdapter_IsOutcomeRejectedNotProviderUnavailable(t *testing.T) {
	for name, agentErr := range map[string]error{
		"outcome outside the allowed set": markerRejection(`terminal outcome marker names outcome "ship-it", which is not in the allowed set`),
		"duplicate marker":                markerRejection("terminal outcome marker appeared more than once"),
		"malformed marker":                markerRejection("terminal outcome marker is malformed"),
		"wrapped again on the way up":     fmt.Errorf("start agent: %w", markerRejection("terminal outcome marker is malformed")),
	} {
		t.Run(name, func(t *testing.T) {
			executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
				diff:        defaultInScopeDiff(),
				agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
				agentErr:    agentErr,
			})
			result, err := executor.Execute(context.Background(), req)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.State != domainruntime.ExecutionAttemptFailed ||
				result.TerminationReason != domainruntime.TerminationReasonOutcomeRejected ||
				result.ErrorCode != errorcode.CodeValidationFailed {
				t.Fatalf("result = %+v, want FAILED / OUTCOME_REJECTED / VALIDATION_FAILED (it was EXECUTION_FAILED / PROVIDER_UNAVAILABLE before V9-03)", result)
			}
			if result.Evidence != nil || result.SelectedOutcome != "" {
				t.Fatalf("result = %+v, want no evidence and no selected outcome for a rejected outcome", result)
			}
		})
	}
}

// A broken stream — an adapter protocol error that does NOT carry the sentinel
// (no terminal event, undecodable JSONL) — is still the provider's failure.
func TestAgentNodeExecutor_ProtocolErrorWithoutTheMarkerSentinel_IsStillProviderUnavailable(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
		agentErr:    fmt.Errorf("%w: terminal event is missing", errAdapterProtocol),
	})
	result, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.TerminationReason != domainruntime.TerminationReasonExecutionFailed || result.ErrorCode != errorcode.CodeProviderUnavailable {
		t.Fatalf("result = %+v, want EXECUTION_FAILED / PROVIDER_UNAVAILABLE for a protocol error that is not a rejected marker", result)
	}
}

// The locked quiescence rule outranks the marker verdict, exactly as it does
// the scope verdict: a mutating attempt whose process tree never confirmed
// quiescence is indeterminate, whatever the adapter's error said.
func TestAgentNodeExecutor_RejectedOutcomeMarkerWithoutQuiescence_StaysIndeterminate(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: false},
		agentErr:    markerRejection("terminal outcome marker is malformed"),
	})
	if _, err := executor.Execute(context.Background(), req); !errors.Is(err, runtime.ErrIndeterminateExecution) {
		t.Fatalf("Execute error = %v, want ErrIndeterminateExecution", err)
	}
}

// And a lost lease outranks it as well.
func TestAgentNodeExecutor_RejectedOutcomeMarkerWithLostLease_StaysIndeterminate(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
		agentErr:    fmt.Errorf("%w; %w", ports.ErrJobLeaseLost, markerRejection("terminal outcome marker is malformed")),
	})
	if _, err := executor.Execute(context.Background(), req); !errors.Is(err, runtime.ErrIndeterminateExecution) {
		t.Fatalf("Execute error = %v, want ErrIndeterminateExecution", err)
	}
}
