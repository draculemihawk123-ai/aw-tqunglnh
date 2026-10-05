package runtime_test

// V9-17 — a SUCCEEDED agent attempt leaves its final message as an AGENT_OUTPUT
// Evidence row (agent_output_evidence.go), for every role.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func agentOutputEntry(entries []ports.EvidenceProposal) (ports.EvidenceProposal, bool) {
	for _, entry := range entries {
		if entry.Kind == domainruntime.EvidenceKindAgentOutput {
			return entry, true
		}
	}
	return ports.EvidenceProposal{}, false
}

func TestAgentNodeExecutor_FinalMessageBecomesAnAgentOutputEvidenceRow(t *testing.T) {
	for _, role := range []workflow.AgentRole{workflow.AgentRoleMaker, workflow.AgentRoleChecker} {
		t.Run(string(role), func(t *testing.T) {
			diff := defaultInScopeDiff()
			if role == workflow.AgentRoleChecker {
				diff = defaultReadOnlyDiff()
			}
			executor, req, uow, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
				role: role, diff: diff,
				agentResult:   ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
				agentEvents:   []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
				agentMessages: []string{"Looking at the diff.", "  Not approved: no confirmation on delete.\n  "},
			})
			result, err := executor.Execute(context.Background(), req)
			if err != nil || result.State != domainruntime.ExecutionAttemptSucceeded {
				t.Fatalf("Execute = %+v, %v, want SUCCEEDED", result, err)
			}

			entry, found := agentOutputEntry(result.Evidence.EvidenceEntries)
			if !found || entry.Verdict != domainruntime.EvidenceVerdictRecorded || len(entry.ArtifactReferences) != 1 {
				t.Fatalf("evidence entries = %+v, want one %s/%s entry with one artifact", result.Evidence.EvidenceEntries, domainruntime.EvidenceKindAgentOutput, domainruntime.EvidenceVerdictRecorded)
			}
			artifactID := entry.ArtifactReferences[0]
			promoted := false
			for _, id := range result.Evidence.OutputArtifactRefs {
				promoted = promoted || id == artifactID
			}
			if !promoted {
				t.Fatalf("OutputArtifactRefs = %v, want it to name %s so finalize promotes it", result.Evidence.OutputArtifactRefs, artifactID)
			}

			// The artifact holds the FINAL message, trimmed, as plain text.
			want := "Not approved: no confirmation on delete."
			sum := sha256.Sum256([]byte(want))
			var content string
			if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
				record, err := tx.Artifacts().GetArtifact(context.Background(), artifactID)
				if err != nil {
					return err
				}
				content = record.ContentHash
				if !strings.HasPrefix(record.MediaType, "text/plain") || record.Size != int64(len(want)) {
					t.Errorf("artifact media type %q size %d, want text/plain and %d bytes", record.MediaType, record.Size, len(want))
				}
				return nil
			}); err != nil {
				t.Fatalf("load artifact: %v", err)
			}
			if !strings.HasSuffix(content, hex.EncodeToString(sum[:])) {
				t.Fatalf("artifact content hash %q does not match the final message %q", content, want)
			}

			// The existing diff-manifest row is unchanged for a maker; a checker, which
			// changed nothing, still has no diff manifest row.
			_, hasDiffRow := func() (ports.EvidenceProposal, bool) {
				for _, e := range result.Evidence.EvidenceEntries {
					if e.Kind == domainruntime.EvidenceKindAgentExecution {
						return e, true
					}
				}
				return ports.EvidenceProposal{}, false
			}()
			if hasDiffRow != (len(result.Evidence.DiffManifestArtifacts) > 0) {
				t.Fatalf("AGENT_EXECUTION row present = %v with %d diff manifests, want one exactly when there are manifests", hasDiffRow, len(result.Evidence.DiffManifestArtifacts))
			}
		})
	}
}

func TestAgentNodeExecutor_SilentAgentLeavesNoAgentOutputRow(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	result, err := executor.Execute(context.Background(), req)
	if err != nil || result.State != domainruntime.ExecutionAttemptSucceeded {
		t.Fatalf("Execute = %+v, %v, want SUCCEEDED", result, err)
	}
	if _, found := agentOutputEntry(result.Evidence.EvidenceEntries); found {
		t.Fatalf("evidence entries = %+v, want no %s row for an agent that said nothing", result.Evidence.EvidenceEntries, domainruntime.EvidenceKindAgentOutput)
	}
}
