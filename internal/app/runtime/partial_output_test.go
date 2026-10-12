package runtime_test

// V11-11 (ADR-042): a FAILED or TIMED_OUT agent attempt keeps what the agent had said, as an AGENT_OUTPUT evidence row.

import (
	"context"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestFailedAgentAttempt_KeepsItsLastWordsAsAgentOutputEvidence(t *testing.T) {
	for _, state := range []domainruntime.ExecutionAttemptState{domainruntime.ExecutionAttemptFailed, domainruntime.ExecutionAttemptTimedOut} {
		t.Run(string(state), func(t *testing.T) {
			executor, req, uow, ids, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
				diff:          defaultInScopeDiff(),
				agentResult:   ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
				agentEvents:   []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
				agentMessages: []string{"Reading the spec.", "I cannot find src/App.tsx, so I stopped."},
			})
			ctx := context.Background()
			result, err := executor.Execute(ctx, req)
			if err != nil || result.State != domainruntime.ExecutionAttemptFailed {
				t.Fatalf("Execute = %+v, %v, want FAILED", result, err)
			}

			artifactID, err := executor.StagePartialOutput(ctx, req.RunID, req.NodeRunID, req.AttemptID)
			if err != nil || artifactID == "" {
				t.Fatalf("StagePartialOutput = %q, %v, want an artifact id", artifactID, err)
			}
			code, reason := errorcode.CodeExecutionFailed, domainruntime.TerminationReasonExecutionFailed
			if state == domainruntime.ExecutionAttemptTimedOut {
				code, reason = errorcode.CodeTimeout, domainruntime.TerminationReasonDeadlineExceeded
			}
			if _, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
				RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID, ExpectedVersion: loadAttemptVersion(t, uow, req.AttemptID),
				NextState: state, TerminationReason: reason, FailureCode: code, JobLease: req.JobLease, CorrelationID: "corr-1",
				PartialOutputArtifactID: artifactID,
			}); err != nil {
				t.Fatalf("FinalizeExecutionAttempt: %v", err)
			}

			if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
				rows, err := tx.Runtime().ListEvidenceForAttempt(ctx, req.AttemptID)
				if err != nil {
					return err
				}
				if len(rows) != 1 || rows[0].Kind != domainruntime.EvidenceKindAgentOutput || rows[0].Verdict != domainruntime.EvidenceVerdictRecorded ||
					len(rows[0].ArtifactReferences) != 1 || rows[0].ArtifactReferences[0] != artifactID {
					t.Fatalf("evidence = %+v, want one RECORDED AGENT_OUTPUT row naming %s", rows, artifactID)
				}
				record, err := tx.Artifacts().GetArtifact(ctx, artifactID)
				if err != nil {
					return err
				}
				if record.AttachState != "ATTACHED" {
					t.Fatalf("artifact state = %s, want ATTACHED", record.AttachState)
				}
				return nil
			}); err != nil {
				t.Fatalf("read back: %v", err)
			}
		})
	}
}

func TestStagePartialOutput_AnAgentThatSaidNothingStagesNothing(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	if _, err := executor.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	artifactID, err := executor.StagePartialOutput(context.Background(), req.RunID, req.NodeRunID, req.AttemptID)
	if err != nil || strings.TrimSpace(artifactID) != "" {
		t.Fatalf("StagePartialOutput = %q, %v, want nothing", artifactID, err)
	}
}
