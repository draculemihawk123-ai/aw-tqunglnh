// V9-17 — what an agent SAID, kept as evidence.
//
// An AGENT attempt's words used to live only in the agent_events table
// (ASSISTANT_MESSAGE rows). No API, CLI command or UI view read them, so an
// operator could not see why a CHECKER chose `rework`, and the engine had
// nothing to hand to the maker that the checker sent back.
//
// Now a SUCCEEDED agent attempt leaves one more Evidence row, Kind
// AGENT_OUTPUT (runtimedomain.EvidenceKindAgentOutput), verdict RECORDED, whose
// single artifact is the attempt's final message as plain text. The words in it
// were already redacted when the sink persisted them (the outcome marker was
// removed before that: trackOutcomeMarker), so nothing is redacted twice and
// nothing unredacted is stored. Reading it needs no new command: `aw evidence
// list` names the row and `aw artifact get` prints it.
//
// The FINAL message, not the whole transcript: it is where an agent summarizes
// what it did, asks its question or gives its review verdict, and it keeps the
// artifact small and bounded by the sink's per-event size limit.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// agentOutputArtifactMediaType is the media type of an AGENT_OUTPUT artifact:
// the message itself, so `aw artifact get` prints something a person can read.
const agentOutputArtifactMediaType = "text/plain; charset=utf-8"

// finalAssistantMessage returns the text of the last non-blank ASSISTANT_MESSAGE
// of attemptID, or "" when the attempt said nothing. The events are the sink's
// redacted copies, oldest sequence first.
func finalAssistantMessage(ctx context.Context, uow ports.UnitOfWork, attemptID string) (string, error) {
	var message string
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		records, err := tx.AgentEvents().ListByAttemptAndKind(ctx, attemptID, string(ports.AgentEventAssistantMessage))
		if err != nil {
			return err
		}
		for _, record := range records {
			var payload struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal([]byte(record.PayloadJSON), &payload); err != nil {
				return fmt.Errorf("decode assistant message event %d of attempt %s: %w", record.Sequence, attemptID, err)
			}
			if strings.TrimSpace(payload.Message) != "" {
				message = strings.TrimSpace(payload.Message)
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("load the final assistant message of attempt %s: %w", attemptID, err)
	}
	return message, nil
}

// stageAgentOutputEvidence stores the attempt's final message as an ORPHAN
// artifact and proposes the AGENT_OUTPUT Evidence row that references it. It
// does nothing when the agent said nothing. The artifact is promoted
// ORPHAN->ATTACHED by FinalizeExecutionAttempt through OutputArtifactRefs, in
// the same transaction that writes the row.
func stageAgentOutputEvidence(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock, store ports.ArtifactStore,
	req ports.NodeExecutionRequest, request ports.AgentExecutionRequest, resolved resolvedExecutionResources,
	evidence *ports.AttemptFinalizationEvidence,
) error {
	message, err := finalAssistantMessage(ctx, uow, req.AttemptID)
	if err != nil {
		return err
	}
	if message == "" {
		return nil
	}
	ref, err := store.Put(ctx, ports.ArtifactMetadata{ContentType: agentOutputArtifactMediaType, Sensitivity: redact.Sensitive, Redacted: true}, bytes.NewReader([]byte(message)))
	if err != nil {
		return fmt.Errorf("put agent output artifact: %w", err)
	}
	if err := store.Verify(ctx, ref); err != nil {
		return fmt.Errorf("verify agent output artifact: %w", err)
	}
	artifactID := ids.NewID()
	record, err := artifact.NewArtifact(
		artifact.ID(artifactID), resolved.projectID, ref.Locator, ref.SHA256, ref.Size, ref.ContentType,
		ref.Sensitivity, ref.Redacted, artifact.RetentionCanonicalContext, artifact.Orphan, false, nil, clk.Now(), 1,
	)
	if err != nil {
		return fmt.Errorf("construct agent output artifact record: %w", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, record)
		return err
	}); err != nil {
		return fmt.Errorf("insert agent output artifact as ORPHAN: %w", err)
	}
	evidence.OutputArtifactRefs = append(evidence.OutputArtifactRefs, artifactID)
	evidence.EvidenceEntries = append(evidence.EvidenceEntries, ports.EvidenceProposal{
		Kind: runtimedomain.EvidenceKindAgentOutput, Verdict: runtimedomain.EvidenceVerdictRecorded,
		ArtifactReferences: []string{artifactID}, PolicyVersion: request.ExecutionProfileHash,
	})
	return nil
}
