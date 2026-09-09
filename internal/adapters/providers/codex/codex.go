package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/internal/jsonl"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/internal/versionprobe"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

const (
	AdapterVersion  = "codex-exec-jsonl/v1"
	ProtocolVersion = "codex-exec-jsonl/v1"
)

// defaultVersionProbeTimeout bounds Capabilities' own live "--version"
// spawn (V5-07, mirroring claude.go's V5-06 shape exactly) — short, since
// a real CLI's version flag is expected to return near-instantly with no
// task/network work involved.
const defaultVersionProbeTimeout = 5 * time.Second

var ErrProtocol = errors.New("invalid Codex JSONL protocol")

type Config struct {
	Executable           string
	PrefixArgs           []string
	StartArgs            []string
	ResumeArgs           []string
	InheritedEnvironment []string
	MaxJSONLLineBytes    int
	// VersionArgs is the argv Capabilities uses to probe the configured
	// executable's real, live version (V5-07) — defaults to ["--version"],
	// the common CLI convention. Override if the configured Codex CLI
	// build reports its version a different way.
	VersionArgs []string
	// VersionEnvironment is passed as-is to the capability probe's own
	// process spawn (V5-07) — a production caller leaves this nil; a test
	// fixture sets AGENTKIT_PROVIDER_HELPER so a probe spawning the
	// re-invoked test binary itself is recognized the same way execute's
	// own Start/Resume spawns already are (helperRequest's own contract).
	VersionEnvironment map[string]string
}

type Adapter struct {
	process ports.ProcessSupervisor
	config  Config
}

func New(process ports.ProcessSupervisor, config Config) (*Adapter, error) {
	if process == nil {
		return nil, errors.New("process supervisor is required")
	}
	if strings.TrimSpace(config.Executable) == "" {
		config.Executable = "codex"
	}
	config.PrefixArgs = append([]string(nil), config.PrefixArgs...)
	config.StartArgs = append([]string(nil), config.StartArgs...)
	config.ResumeArgs = append([]string(nil), config.ResumeArgs...)
	config.InheritedEnvironment = append([]string(nil), config.InheritedEnvironment...)
	if config.MaxJSONLLineBytes <= 0 {
		config.MaxJSONLLineBytes = jsonl.DefaultMaxLineBytes
	}
	if len(config.VersionArgs) == 0 {
		config.VersionArgs = []string{"--version"}
	} else {
		config.VersionArgs = append([]string(nil), config.VersionArgs...)
	}
	config.VersionEnvironment = cloneEnvironment(config.VersionEnvironment)
	return &Adapter{process: process, config: config}, nil
}

// Capabilities probes the CONFIGURED executable live (V5-07, mirroring
// claude.go's V5-06 shape exactly) — a separate, minimal invocation
// (config.PrefixArgs + config.VersionArgs) entirely independent of the
// JSONL task-execution protocol execute builds below. It fails closed: a
// probe error means Capabilities itself returns an error, never a stale
// or guessed TestedCLIVersion.
func (a *Adapter) Capabilities(ctx context.Context) (ports.AgentCapabilities, error) {
	argv := append(append([]string(nil), a.config.PrefixArgs...), a.config.VersionArgs...)
	observedVersion, err := versionprobe.Probe(ctx, a.process, capabilityProbeProcessID(), a.config.Executable, argv, a.config.VersionEnvironment, defaultVersionProbeTimeout)
	if err != nil {
		return ports.AgentCapabilities{}, fmt.Errorf("codex: capability probe: %w", err)
	}
	return ports.AgentCapabilities{
		Provider:         ports.ProviderCodex,
		AdapterVersion:   AdapterVersion,
		ProtocolVersion:  ProtocolVersion,
		TestedCLIVersion: observedVersion,
		SupportsStart:    true,
		SupportsResume:   true,
		SupportsCancel:   true,
		CanonicalEventKinds: []ports.AgentEventKind{
			ports.AgentEventExecutionStarted,
			ports.AgentEventStatusChanged,
			ports.AgentEventAssistantMessage,
			ports.AgentEventToolCallStarted,
			ports.AgentEventToolCallFinished,
			ports.AgentEventUsageReported,
			ports.AgentEventCheckpointProposed,
			ports.AgentEventDiagnostic,
			ports.AgentEventExecutionFinished,
		},
	}, nil
}

func (a *Adapter) Start(
	ctx context.Context,
	request ports.AgentExecutionRequest,
	sink ports.AgentEventSink,
) (ports.AgentExecutionResult, error) {
	return a.execute(ctx, request, nil, sink)
}

func (a *Adapter) Resume(
	ctx context.Context,
	request ports.AgentExecutionRequest,
	session ports.ProviderSessionRef,
	sink ports.AgentEventSink,
) (ports.AgentExecutionResult, error) {
	if session.Provider != ports.ProviderCodex || strings.TrimSpace(session.SessionID) == "" {
		return failedResult(request, "invalid_resume_session"), errors.New("Codex resume session is invalid")
	}
	return a.execute(ctx, request, &session, sink)
}

func (a *Adapter) Cancel(ctx context.Context, attemptID ports.ExecutionAttemptID) error {
	if attemptID == "" {
		return errors.New("attempt id is required")
	}
	return a.process.Cancel(ctx, processID(attemptID))
}

func (a *Adapter) execute(
	ctx context.Context,
	request ports.AgentExecutionRequest,
	resume *ports.ProviderSessionRef,
	sink ports.AgentEventSink,
) (ports.AgentExecutionResult, error) {
	if err := validateRequest(request, sink); err != nil {
		return failedResult(request, "invalid_request"), err
	}

	normalizer := newNormalizer(ctx, request.AttemptID, sink)
	stdout, err := jsonl.NewWriter(a.config.MaxJSONLLineBytes, normalizer.consume)
	if err != nil {
		return failedResult(request, "adapter_configuration"), err
	}
	stderr := jsonl.NewTailBuffer(64 << 10)

	arguments := append([]string(nil), a.config.PrefixArgs...)
	if resume == nil {
		arguments = append(arguments, "exec", "--json", "--cd", request.WorkingDirectory)
		if request.Model != "" {
			arguments = append(arguments, "--model", request.Model)
		}
		if request.Sandbox != "" {
			if err := validateSandbox(request.Sandbox); err != nil {
				return failedResult(request, "invalid_request"), err
			}
			arguments = append(arguments, "--sandbox", request.Sandbox)
		}
		arguments = append(arguments, a.config.StartArgs...)
		arguments = append(arguments, "-")
	} else {
		arguments = append(arguments, "exec", "resume", "--json")
		if request.Model != "" {
			arguments = append(arguments, "--model", request.Model)
		}
		arguments = append(arguments, a.config.ResumeArgs...)
		arguments = append(arguments, resume.SessionID, "-")
		normalizer.session = cloneSession(resume)
	}

	processResult, runErr := a.process.Run(ctx, ports.ProcessSpec{
		ID:                   processID(request.AttemptID),
		Executable:           a.config.Executable,
		Argv:                 arguments,
		WorkingDirectory:     request.WorkingDirectory,
		Environment:          cloneEnvironment(request.Environment),
		InheritedEnvironment: mergeEnvironmentKeys(a.config.InheritedEnvironment, request.InheritedEnvironment),
		Stdin:                []byte(request.Prompt),
		Timeout:              request.Timeout,
	}, stdout, stderr)
	closeErr := stdout.Close()
	parseErr := stdout.Err()
	if parseErr == nil {
		parseErr = closeErr
	}

	result := normalizer.result(request, processResult)
	if parseErr != nil {
		result.Status = ports.AgentExecutionFailed
		result.TerminationReason = "protocol_error"
		if errors.Is(parseErr, ErrProtocol) {
			_ = normalizer.finish(ports.AgentExecutionFailed, "protocol_error")
			return result, parseErr
		}
		return result, fmt.Errorf("consume Codex events: %w", parseErr)
	}
	if runErr != nil {
		result.Status = ports.AgentExecutionFailed
		result.TerminationReason = "process_error"
		_ = normalizer.finish(ports.AgentExecutionFailed, "process_error")
		return result, runErr
	}

	status, reason, proposed, protocolErr := normalizer.finalStatus(processResult, resume == nil, request.AllowedOutcomes)
	result.Status = status
	result.TerminationReason = reason
	result.ProposedOutcome = proposed
	if err := normalizer.finish(status, reason); err != nil {
		return result, fmt.Errorf("emit Codex completion: %w", err)
	}
	if protocolErr != nil {
		return result, protocolErr
	}
	return result, nil
}

func validateRequest(request ports.AgentExecutionRequest, sink ports.AgentEventSink) error {
	if request.AttemptID == "" {
		return errors.New("attempt id is required")
	}
	if request.ContextSnapshotID == "" {
		return errors.New("context snapshot id is required")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return errors.New("agent prompt is required")
	}
	if strings.TrimSpace(request.WorkingDirectory) == "" {
		return errors.New("agent working directory is required")
	}
	if request.Timeout <= 0 {
		return errors.New("agent timeout must be greater than zero")
	}
	if sink == nil {
		return errors.New("agent event sink is required")
	}
	return nil
}

func validateSandbox(value string) error {
	switch value {
	case "read-only", "workspace-write", "danger-full-access":
		return nil
	default:
		return fmt.Errorf("unsupported Codex sandbox %q", value)
	}
}

func processID(attemptID ports.ExecutionAttemptID) ports.ProcessID {
	return ports.ProcessID(string(ports.ProviderCodex) + ":" + string(attemptID))
}

// capabilityProbeProcessID mints a fresh id per Capabilities call — unlike
// processID above, a probe has no caller-supplied AttemptID to derive one
// from, and a fixed literal would collide if two probes on the same
// Adapter ever raced (Supervisor rejects a re-used in-flight process ID).
func capabilityProbeProcessID() ports.ProcessID {
	return ports.ProcessID(string(ports.ProviderCodex) + ":capability-probe:" + strconv.FormatInt(time.Now().UnixNano(), 10))
}

func failedResult(request ports.AgentExecutionRequest, reason string) ports.AgentExecutionResult {
	return ports.AgentExecutionResult{
		AttemptID:         request.AttemptID,
		Provider:          ports.ProviderCodex,
		Status:            ports.AgentExecutionFailed,
		TerminationReason: reason,
		ExitCode:          -1,
	}
}

type normalizer struct {
	ctx          context.Context
	attemptID    ports.ExecutionAttemptID
	sink         ports.AgentEventSink
	sequence     uint64
	session      *ports.ProviderSessionRef
	usage        ports.AgentUsage
	terminalSeen bool
	providerOK   bool
	// outcomeOccurrences/outcomeValid/outcomeValue track the terminal
	// <agentkit-outcome> marker (V5-08B, ports.AgentProposedOutcome's own
	// doc comment, mirroring claude.go's own identical fields) across
	// every ASSISTANT_MESSAGE this normalizer emits — only the FIRST
	// occurrence's own parsed shape is remembered; a second occurrence
	// anywhere in the run is a protocol error regardless of either one's
	// own validity.
	outcomeOccurrences int
	outcomeValid       bool
	outcomeValue       string
}

func newNormalizer(ctx context.Context, attemptID ports.ExecutionAttemptID, sink ports.AgentEventSink) *normalizer {
	return &normalizer{ctx: ctx, attemptID: attemptID, sink: sink}
}

func (n *normalizer) consume(line []byte) error {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return fmt.Errorf("%w: decode event: %v", ErrProtocol, err)
	}
	if envelope.Type == "" {
		return fmt.Errorf("%w: event type is required", ErrProtocol)
	}

	switch envelope.Type {
	case "thread.started":
		var event struct {
			ThreadID string `json:"thread_id"`
		}
		if err := json.Unmarshal(line, &event); err != nil || strings.TrimSpace(event.ThreadID) == "" {
			return fmt.Errorf("%w: thread.started requires thread_id", ErrProtocol)
		}
		n.session = &ports.ProviderSessionRef{Provider: ports.ProviderCodex, SessionID: event.ThreadID}
		return n.emit(ports.AgentEvent{Kind: ports.AgentEventExecutionStarted, Session: cloneSession(n.session)}, envelope.Type)
	case "turn.started":
		return n.emit(ports.AgentEvent{Kind: ports.AgentEventStatusChanged, Message: "turn started"}, envelope.Type)
	case "item.started", "item.updated", "item.completed":
		return n.consumeItem(line, envelope.Type)
	case "turn.completed":
		var event struct {
			Usage codexUsage `json:"usage"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("%w: decode turn.completed: %v", ErrProtocol, err)
		}
		n.usage = event.Usage.canonical()
		n.terminalSeen = true
		n.providerOK = true
		if err := n.emit(ports.AgentEvent{Kind: ports.AgentEventUsageReported, Usage: cloneUsage(n.usage)}, envelope.Type); err != nil {
			return err
		}
		return n.emit(ports.AgentEvent{Kind: ports.AgentEventCheckpointProposed, Session: cloneSession(n.session)}, envelope.Type)
	case "turn.failed", "error":
		n.terminalSeen = true
		n.providerOK = false
		return n.emit(ports.AgentEvent{
			Kind: ports.AgentEventDiagnostic,
			Diagnostic: &ports.AgentDiagnostic{
				Code:    "PROVIDER_REPORTED_FAILURE",
				Message: "Codex reported a failed turn",
			},
		}, envelope.Type)
	default:
		return n.emit(ports.AgentEvent{
			Kind: ports.AgentEventDiagnostic,
			Diagnostic: &ports.AgentDiagnostic{
				Code:    "UNRECOGNIZED_PROVIDER_EVENT",
				Message: "Codex event was not mapped to a domain action",
			},
		}, envelope.Type)
	}
}

func (n *normalizer) consumeItem(line []byte, eventType string) error {
	var event struct {
		Item codexItem `json:"item"`
	}
	if err := json.Unmarshal(line, &event); err != nil || event.Item.Type == "" {
		return fmt.Errorf("%w: item event requires item.type", ErrProtocol)
	}
	metadata := map[string]string{"item_id": event.Item.ID, "item_type": event.Item.Type}
	switch event.Item.Type {
	case "agent_message":
		if eventType != "item.completed" {
			return nil
		}
		return n.emit(ports.AgentEvent{
			Kind:             ports.AgentEventAssistantMessage,
			Message:          event.Item.Text,
			ProviderMetadata: metadata,
		}, eventType)
	case "command_execution":
		if eventType == "item.updated" {
			return n.emit(ports.AgentEvent{
				Kind:             ports.AgentEventStatusChanged,
				Message:          "command execution updated",
				ProviderMetadata: metadata,
			}, eventType)
		}
		tool := &ports.AgentToolEvent{
			CallID:   event.Item.ID,
			Name:     "command_execution",
			Input:    jsonString(event.Item.Command),
			Output:   event.Item.AggregatedOutput,
			ExitCode: event.Item.ExitCode,
		}
		if event.Item.ExitCode != nil {
			tool.IsError = *event.Item.ExitCode != 0
		}
		kind := ports.AgentEventToolCallStarted
		if eventType == "item.completed" {
			kind = ports.AgentEventToolCallFinished
		}
		return n.emit(ports.AgentEvent{Kind: kind, Tool: tool, ProviderMetadata: metadata}, eventType)
	case "mcp_tool_call":
		if eventType == "item.updated" {
			return n.emit(ports.AgentEvent{
				Kind:             ports.AgentEventStatusChanged,
				Message:          "MCP tool call updated",
				ProviderMetadata: metadata,
			}, eventType)
		}
		tool := &ports.AgentToolEvent{
			CallID:  event.Item.ID,
			Name:    event.Item.Tool,
			Input:   cloneRawJSON(event.Item.Arguments),
			Output:  event.Item.Result,
			IsError: event.Item.Error != "",
		}
		kind := ports.AgentEventToolCallStarted
		if eventType == "item.completed" {
			kind = ports.AgentEventToolCallFinished
		}
		return n.emit(ports.AgentEvent{Kind: kind, Tool: tool, ProviderMetadata: metadata}, eventType)
	case "reasoning":
		return n.emit(ports.AgentEvent{Kind: ports.AgentEventStatusChanged, Message: "reasoning", ProviderMetadata: metadata}, eventType)
	default:
		return n.emit(ports.AgentEvent{
			Kind: ports.AgentEventDiagnostic,
			Diagnostic: &ports.AgentDiagnostic{
				Code:    "UNRECOGNIZED_PROVIDER_ITEM",
				Message: "Codex item was not mapped to a domain action",
			},
			ProviderMetadata: metadata,
		}, eventType)
	}
}

func (n *normalizer) emit(event ports.AgentEvent, rawType string) error {
	if event.Kind == ports.AgentEventAssistantMessage {
		event.Message = n.trackOutcomeMarker(event.Message)
	}
	n.sequence++
	event.AttemptID = n.attemptID
	event.Sequence = n.sequence
	event.ObservedAt = time.Now().UTC()
	if event.ProviderMetadata == nil {
		event.ProviderMetadata = make(map[string]string, 1)
	}
	event.ProviderMetadata["raw_type"] = rawType
	return n.sink.Accept(n.ctx, event)
}

// trackOutcomeMarker mirrors claude.go's own identical method exactly —
// see its doc comment for the full contract (this package has no shared
// base normalizer with claude's own, so the logic is intentionally
// duplicated rather than factored into a third package neither adapter
// otherwise needs).
func (n *normalizer) trackOutcomeMarker(text string) string {
	stripped, found, valid, outcome := extractOutcomeMarker(text)
	if !found {
		return text
	}
	n.outcomeOccurrences++
	if n.outcomeOccurrences == 1 {
		n.outcomeValid = valid
		n.outcomeValue = outcome
	}
	return stripped
}

// resolveProposedOutcome mirrors claude.go's own identical method — see
// its doc comment for the full rejection-rule contract.
func (n *normalizer) resolveProposedOutcome(allowedOutcomes []string) (*ports.AgentProposedOutcome, error) {
	switch {
	case n.outcomeOccurrences == 0:
		return nil, nil
	case n.outcomeOccurrences > 1:
		return nil, fmt.Errorf("%w: terminal outcome marker appeared more than once", ErrProtocol)
	case !n.outcomeValid:
		return nil, fmt.Errorf("%w: terminal outcome marker is malformed", ErrProtocol)
	}
	if !containsOutcome(allowedOutcomes, n.outcomeValue) {
		return nil, fmt.Errorf("%w: terminal outcome marker names outcome %q, which is not in the allowed set", ErrProtocol, n.outcomeValue)
	}
	return &ports.AgentProposedOutcome{
		Value: n.outcomeValue, Source: ports.AgentOutcomeReportedByProvider, SchemaVersion: outcomeMarkerSchemaVersion,
	}, nil
}

const (
	outcomeMarkerOpenTag       = "<agentkit-outcome>"
	outcomeMarkerCloseTag      = "</agentkit-outcome>"
	outcomeMarkerSchemaVersion = 1
)

type outcomeMarkerPayload struct {
	SchemaVersion int    `json:"schemaVersion"`
	Outcome       string `json:"outcome"`
}

// extractOutcomeMarker mirrors claude.go's own identical function exactly
// — see its doc comment for the full trailing-marker matching contract.
func extractOutcomeMarker(text string) (stripped string, found bool, valid bool, outcome string) {
	trimmed := strings.TrimRight(text, " \t\r\n")
	if !strings.HasSuffix(trimmed, outcomeMarkerCloseTag) {
		return text, false, false, ""
	}
	body := trimmed[:len(trimmed)-len(outcomeMarkerCloseTag)]
	openIdx := strings.LastIndex(body, outcomeMarkerOpenTag)
	if openIdx < 0 {
		return text, true, false, ""
	}
	stripped = strings.TrimRight(body[:openIdx], " \t\r\n")
	var payload outcomeMarkerPayload
	if err := json.Unmarshal([]byte(body[openIdx+len(outcomeMarkerOpenTag):]), &payload); err != nil {
		return stripped, true, false, ""
	}
	if payload.SchemaVersion != outcomeMarkerSchemaVersion || strings.TrimSpace(payload.Outcome) == "" {
		return stripped, true, false, ""
	}
	return stripped, true, true, payload.Outcome
}

func containsOutcome(allowed []string, value string) bool {
	for _, candidate := range allowed {
		if candidate == value {
			return true
		}
	}
	return false
}

func (n *normalizer) finish(status ports.AgentExecutionStatus, reason string) error {
	return n.emit(ports.AgentEvent{
		Kind:    ports.AgentEventExecutionFinished,
		Message: string(status),
		Diagnostic: &ports.AgentDiagnostic{
			Code:    reason,
			Message: "Codex execution finished",
		},
	}, "process.finished")
}

func (n *normalizer) finalStatus(
	process ports.ProcessResult,
	requireNewSession bool,
	allowedOutcomes []string,
) (ports.AgentExecutionStatus, string, *ports.AgentProposedOutcome, error) {
	if process.TimedOut {
		return ports.AgentExecutionTimedOut, "timeout", nil, nil
	}
	if process.Cancelled {
		return ports.AgentExecutionCancelled, "cancelled", nil, nil
	}
	if process.ExitCode != 0 {
		return ports.AgentExecutionFailed, "process_exit", nil, nil
	}
	if !n.terminalSeen {
		return ports.AgentExecutionFailed, "protocol_incomplete", nil, fmt.Errorf("%w: terminal event is missing", ErrProtocol)
	}
	if requireNewSession && (n.session == nil || n.session.SessionID == "") {
		return ports.AgentExecutionFailed, "protocol_incomplete", nil, fmt.Errorf("%w: thread.started is missing", ErrProtocol)
	}
	if !n.providerOK {
		return ports.AgentExecutionFailed, "provider_failure", nil, nil
	}
	proposed, err := n.resolveProposedOutcome(allowedOutcomes)
	if err != nil {
		return ports.AgentExecutionFailed, "outcome_marker_invalid", nil, err
	}
	return ports.AgentExecutionSucceeded, "completed", proposed, nil
}

func (n *normalizer) result(request ports.AgentExecutionRequest, process ports.ProcessResult) ports.AgentExecutionResult {
	return ports.AgentExecutionResult{
		AttemptID:    request.AttemptID,
		Provider:     ports.ProviderCodex,
		Session:      cloneSession(n.session),
		Usage:        n.usage,
		ExitCode:     process.ExitCode,
		StartedAt:    process.StartedAt,
		FinishedAt:   process.FinishedAt,
		TreeQuiesced: process.TreeQuiesced,
	}
}

type codexUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

func (u codexUsage) canonical() ports.AgentUsage {
	return ports.AgentUsage{
		InputTokens:       u.InputTokens,
		CachedInputTokens: u.CachedInputTokens,
		OutputTokens:      u.OutputTokens,
	}
}

type codexItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Command          string          `json:"command"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Result           string          `json:"result"`
	Error            string          `json:"error"`
}

func jsonString(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func cloneRawJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneSession(value *ports.ProviderSessionRef) *ports.ProviderSessionRef {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneUsage(value ports.AgentUsage) *ports.AgentUsage {
	copy := value
	return &copy
}

func cloneEnvironment(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func mergeEnvironmentKeys(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, group := range groups {
		for _, key := range group {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, key)
		}
	}
	return result
}

var _ ports.AgentExecutor = (*Adapter)(nil)
