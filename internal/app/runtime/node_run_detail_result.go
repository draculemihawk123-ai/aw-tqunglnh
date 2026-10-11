package runtime

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func definitionView(node workflow.Node) NodeDefinitionView {
	view := NodeDefinitionView{Type: string(node.Type), Outcomes: node.Outcomes}
	pin := func(kind, definitionID, versionID string) {
		view.Pins = append(view.Pins, NodePinView{Kind: kind, DefinitionID: definitionID, VersionID: versionID})
	}
	switch {
	case node.Agent != nil:
		view.Role = string(node.Agent.Role)
		pin(string(node.Agent.ProfileRef.Kind), node.Agent.ProfileRef.DefinitionID, node.Agent.ProfileRef.VersionID)
		for _, p := range node.Agent.PolicyRefs {
			pin(string(p.Kind), p.DefinitionID, p.VersionID)
		}
	case node.Command != nil:
		view.FailureOutcome = node.Command.FailureOutcome
		pin(string(node.Command.CommandRef.Kind), node.Command.CommandRef.DefinitionID, node.Command.CommandRef.VersionID)
		for _, p := range node.Command.PolicyRefs {
			pin(string(p.Kind), p.DefinitionID, p.VersionID)
		}
	case node.MachineGate != nil:
		view.FailureOutcome = node.MachineGate.FailureOutcome
		pin(string(node.MachineGate.GateRef.Kind), node.MachineGate.GateRef.DefinitionID, node.MachineGate.GateRef.VersionID)
		for _, p := range node.MachineGate.PolicyRefs {
			pin(string(p.Kind), p.DefinitionID, p.VersionID)
		}
	case node.Approval != nil:
		view.AuthorizedRoles = node.Approval.AuthorizedRoles
		view.TimeoutSeconds = node.Approval.TimeoutSeconds
	}
	return view
}

// progressOf summarizes the newest attempt's stream into the one live line the node pane shows.
func progressOf(attempt runtimedomain.ExecutionAttempt, records []ports.AgentEventRecord, matcher redact.Matcher) *NodeProgressView {
	progress := &NodeProgressView{
		AttemptID: string(attempt.ID), AttemptState: string(attempt.State), Running: !attemptTerminal(attempt.State), EventCount: len(records),
	}
	for _, record := range records {
		switch record.Kind {
		case string(ports.AgentEventToolCallStarted):
			progress.ToolCalls++
		case string(ports.AgentEventAssistantMessage):
			progress.Messages++
		}
		if record.Kind == string(ports.AgentEventUsageReported) {
			continue
		}
		view := toAttemptEventView(record, matcher)
		at := record.CreatedAt
		switch record.Kind {
		case string(ports.AgentEventAssistantMessage):
			progress.LastActivity = "said: " + firstLine(view.Message, 140)
		case string(ports.AgentEventToolCallStarted):
			if view.Tool != nil {
				progress.LastActivity = strings.TrimSpace("tool " + view.Tool.Name + " " + firstLine(view.Tool.Input, 100))
			}
		case string(ports.AgentEventToolCallFinished):
			if view.Tool != nil {
				progress.LastActivity = "finished tool " + view.Tool.Name
				if view.Tool.IsError {
					progress.LastActivity += " (error)"
				}
			}
		case string(ports.AgentEventDiagnostic):
			if view.Diagnostic != nil {
				progress.LastActivity = view.Diagnostic.Code + ": " + firstLine(view.Diagnostic.Message, 120)
			}
		default:
			progress.LastActivity = strings.ToLower(strings.ReplaceAll(record.Kind, "_", " "))
		}
		progress.LastKind = record.Kind
		progress.LastAt = &at
	}
	return progress
}

func firstLine(text string, bound int) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	if len(text) > bound {
		text = truncateUTF8(text, bound) + "…"
	}
	return text
}

// resultOf builds the node's result by kind of node, reading the few artifacts worth summarizing. A missing store or an unreadable
// artifact leaves that part out (the evidence list still names it); it never fails the whole detail.
func resultOf(ctx context.Context, store ports.ArtifactStore, matcher redact.Matcher, base nodeRunBase, detail NodeRunDetail) NodeResultView {
	result := NodeResultView{SelectedOutcome: base.nodeRun.SelectedOutcome}
	for _, entry := range detail.Attempts {
		if entry.Usage != nil {
			if result.Usage == nil {
				result.Usage = &UsageView{}
			}
			result.Usage.InputTokens += entry.Usage.InputTokens
			result.Usage.CachedInputTokens += entry.Usage.CachedInputTokens
			result.Usage.OutputTokens += entry.Usage.OutputTokens
			result.Usage.CostUSD += entry.Usage.CostUSD
		}
		if entry.StartedAt != nil && entry.FinishedAt != nil {
			result.DurationMillis += entry.FinishedAt.Sub(*entry.StartedAt).Milliseconds()
		}
	}
	attemptStates := map[string]string{}
	for _, a := range base.attempts {
		attemptStates[string(a.ID)] = string(a.State)
	}
	latestOf := func(kind string) *runtimedomain.Evidence {
		var found *runtimedomain.Evidence
		for i := range base.evidence {
			if base.evidence[i].Kind == kind {
				found = &base.evidence[i]
			}
		}
		return found
	}

	switch base.node.Type {
	case workflow.NodeAgent:
		result.Kind = "AGENT"
		if e := latestOf(runtimedomain.EvidenceKindAgentOutput); e != nil && len(e.ArtifactReferences) > 0 {
			if text, truncated, ok := readArtifactText(ctx, store, matcher, base.artifacts[e.ArtifactReferences[0]], maxResultTextBytes); ok {
				result.FinalMessage = &NodeTextView{
					Text: text, Truncated: truncated, EvidenceID: string(e.ID), Partial: attemptStates[string(e.AttemptID)] != string(runtimedomain.ExecutionAttemptSucceeded),
				}
			}
		}
		result.ChangedFiles, result.ChangedTruncated = changedFilesOf(ctx, store, base)
	case workflow.NodeCommand:
		result.Kind = "COMMAND"
		if e := latestOf(runtimedomain.EvidenceKindCommandExecution); e != nil && len(e.ArtifactReferences) > 0 {
			result.Command = commandResultOf(ctx, store, matcher, base.artifacts[e.ArtifactReferences[0]], string(e.ID))
		}
	case workflow.NodeMachineGate:
		result.Kind = "MACHINE_GATE"
		var latestAttempt string
		if n := len(base.attempts); n > 0 {
			latestAttempt = string(base.attempts[n-1].ID)
		}
		for _, e := range base.evidence {
			if latestAttempt == "" || string(e.AttemptID) == latestAttempt {
				result.Criteria = append(result.Criteria, NodeCriterionResult{EvidenceID: string(e.ID), Kind: e.Kind, Verdict: e.Verdict})
			}
		}
	case workflow.NodeApproval:
		result.Kind = "APPROVAL"
		switch {
		case base.approval == nil:
			result.Note = "no approval request yet"
		case base.approval.ResolvedOutcome != "":
			result.Note = "decided: " + base.approval.ResolvedOutcome
		case base.approval.State == "PENDING":
			result.Note = "waiting for a decision from " + strings.Join(base.approval.AuthorizedRoles, ", ")
		default:
			result.Note = strings.ToLower(base.approval.State)
		}
	case workflow.NodeWait:
		result.Kind = "WAIT"
	default:
		result.Kind = "STRUCTURAL"
		if base.nodeRun.SelectedOutcome != "" {
			result.Note = "passed through, took the " + base.nodeRun.SelectedOutcome + " branch"
		} else {
			result.Note = "structural node, " + strings.ToLower(string(base.nodeRun.State))
		}
	}
	return result
}

// readArtifactText reads at most bound bytes of a stored artifact as redacted text. ok is false when there is nothing to read.
func readArtifactText(ctx context.Context, store ports.ArtifactStore, matcher redact.Matcher, record artifact.Artifact, bound int) (text string, truncated, ok bool) {
	if store == nil || record.ID == "" {
		return "", false, false
	}
	reader, err := store.Open(ctx, ports.ArtifactRef{
		Locator: record.Locator, SHA256: record.ContentHash, Size: record.Size, ContentType: record.MediaType,
		Sensitivity: record.Sensitivity, Redacted: record.Redacted,
	})
	if err != nil {
		return "", false, false
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, int64(bound)+1))
	if err != nil {
		return "", false, false
	}
	if len(data) > bound {
		truncated = true
		data = data[:bound]
	}
	return redactText(matcher, truncateUTF8(string(data), bound)), truncated, true
}

// commandResultOf parses a COMMAND node's recorded execution artifact (media type application/vnd.agentkit.command-output+json).
func commandResultOf(ctx context.Context, store ports.ArtifactStore, matcher redact.Matcher, record artifact.Artifact, evidenceID string) *NodeCommandResult {
	body, _, ok := readArtifactText(ctx, store, redact.Matcher{}, record, 1<<20)
	if !ok {
		return nil
	}
	var content commandOutputArtifactContent
	if err := json.Unmarshal([]byte(body), &content); err != nil {
		return nil
	}
	out := &NodeCommandResult{
		ExitCode: content.ExitCode, DurationMillis: content.DurationMillis, Truncated: content.Truncated, EvidenceID: evidenceID,
	}
	for _, arg := range content.Argv {
		out.Argv = append(out.Argv, redactText(matcher, arg))
	}
	cut := func(text string) string {
		if len(text) > maxResultTextBytes {
			out.TextTruncated = true
			return truncateUTF8(text, maxResultTextBytes)
		}
		return text
	}
	out.Stdout, out.Stderr = cut(redactText(matcher, content.Stdout)), cut(redactText(matcher, content.Stderr))
	return out
}

// changedFilesOf lists the files an agent attempt changed, from the diff manifests its AGENT_EXECUTION evidence references (the newest
// attempt that left one).
func changedFilesOf(ctx context.Context, store ports.ArtifactStore, base nodeRunBase) ([]NodeChangedFile, bool) {
	if store == nil {
		return nil, false
	}
	var evidenceRow *runtimedomain.Evidence
	for i := range base.evidence {
		if base.evidence[i].Kind == runtimedomain.EvidenceKindAgentExecution {
			evidenceRow = &base.evidence[i]
		}
	}
	if evidenceRow == nil {
		return nil, false
	}
	var files []NodeChangedFile
	truncated := false
	for _, id := range evidenceRow.ArtifactReferences {
		text, _, ok := readArtifactText(ctx, store, redact.Matcher{}, base.artifacts[id], 4<<20)
		if !ok {
			continue
		}
		var manifest struct {
			RepositoryID string `json:"RepositoryID"`
			Files        []struct {
				Code string `json:"Code"`
				Path string `json:"Path"`
			} `json:"Files"`
		}
		if err := json.Unmarshal([]byte(text), &manifest); err != nil {
			continue
		}
		for _, f := range manifest.Files {
			if len(files) >= maxChangedFiles {
				truncated = true
				break
			}
			files = append(files, NodeChangedFile{RepositoryID: manifest.RepositoryID, Path: f.Path, Status: fileStatusOf(f.Code)})
		}
	}
	return files, truncated
}

// fileStatusOf maps a porcelain status code ("XY") to a word.
func fileStatusOf(code string) string {
	code = strings.TrimSpace(code)
	switch {
	case strings.Contains(code, "?"), strings.HasPrefix(code, "A"):
		return "ADDED"
	case strings.Contains(code, "D"):
		return "DELETED"
	case strings.Contains(code, "R"):
		return "RENAMED"
	default:
		return "MODIFIED"
	}
}

// failureOf explains the newest failed attempt of the node, or nil when nothing failed.
func failureOf(detail NodeRunDetail) *ExplainedFailure {
	for i := len(detail.Attempts) - 1; i >= 0; i-- {
		entry := detail.Attempts[i]
		if entry.FailureCode == "" {
			continue
		}
		explained := ExplainFailure(entry.FailureCode, entry.FailureDetail)
		return &explained
	}
	if detail.State == string(runtimedomain.NodeRunFailed) {
		explained := ExplainFailure("EXECUTION_FAILED", detail.BlockReason)
		return &explained
	}
	return nil
}
