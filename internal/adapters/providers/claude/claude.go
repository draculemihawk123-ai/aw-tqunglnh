package claude

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
	AdapterVersion  = "claude-stream-json/v1"
	ProtocolVersion = "claude-stream-json/v1"
)

// defaultVersionProbeTimeout bounds Capabilities' own live "--version"
// spawn (V5-06) — short, since a real CLI's version flag is expected to
// return near-instantly with no task/network work involved.
const defaultVersionProbeTimeout = 5 * time.Second

var ErrProtocol = errors.New("invalid Claude stream-json protocol")

type Config struct {
	Executable           string
	PrefixArgs           []string
	StartArgs            []string
	ResumeArgs           []string
	PermissionMode       string
	InheritedEnvironment []string
	MaxJSONLLineBytes    int
	// VersionArgs is the argv Capabilities uses to probe the configured
	// executable's real, live version (V5-06) — defaults to ["--version"],
	// the common CLI convention. Override if the configured Claude CLI
	// build reports its version a different way.
	VersionArgs []string
	// VersionEnvironment is passed as-is to the capability probe's own
	// process spawn (V5-06) — a production caller leaves this nil; a test
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
		config.Executable = "claude"
	}
	if err := validatePermissionMode(config.PermissionMode); err != nil {
		return nil, err
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

// Capabilities probes the CONFIGURED executable live (V5-06) — a separate,
// minimal invocation (config.PrefixArgs + config.VersionArgs) entirely
// independent of the stream-json task-execution protocol execute builds
// below. It fails closed: a probe error means Capabilities itself
// returns an error, never a stale or guessed TestedCLIVersion.
func (a *Adapter) Capabilities(ctx context.Context) (ports.AgentCapabilities, error) {
	argv := append(append([]string(nil), a.config.PrefixArgs...), a.config.VersionArgs...)
	observedVersion, err := versionprobe.Probe(ctx, a.process, capabilityProbeProcessID(), a.config.Executable, argv, a.config.VersionEnvironment, defaultVersionProbeTimeout)
	if err != nil {
		return ports.AgentCapabilities{}, fmt.Errorf("claude: capability probe: %w", err)
	}
	return ports.AgentCapabilities{
		Provider:         ports.ProviderClaude,
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
	if session.Provider != ports.ProviderClaude || strings.TrimSpace(session.SessionID) == "" {
		return failedResult(request, "invalid_resume_session"), errors.New("Claude resume session is invalid")
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
	arguments = append(arguments, "-p", "--input-format", "text", "--output-format", "stream-json", "--verbose")
	if request.Model != "" {
		arguments = append(arguments, "--model", request.Model)
	}
	if a.config.PermissionMode != "" {
		arguments = append(arguments, "--permission-mode", a.config.PermissionMode)
	}
	if resume == nil {
		arguments = append(arguments, a.config.StartArgs...)
	} else {
		arguments = append(arguments, "--resume", resume.SessionID)
		arguments = append(arguments, a.config.ResumeArgs...)
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
		return result, fmt.Errorf("consume Claude events: %w", parseErr)
	}
	if runErr != nil {
		result.Status = ports.AgentExecutionFailed
		result.TerminationReason = "process_error"
		_ = normalizer.finish(ports.AgentExecutionFailed, "process_error")
		return result, runErr
	}

	status, reason, protocolErr := normalizer.finalStatus(processResult, resume == nil)
	result.Status = status
	result.TerminationReason = reason
	if err := normalizer.finish(status, reason); err != nil {
		return result, fmt.Errorf("emit Claude completion: %w", err)
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

func validatePermissionMode(value string) error {
	switch value {
	case "", "acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan":
		return nil
	default:
		return fmt.Errorf("unsupported Claude permission mode %q", value)
	}
}

func processID(attemptID ports.ExecutionAttemptID) ports.ProcessID {
	return ports.ProcessID(string(ports.ProviderClaude) + ":" + string(attemptID))
}

// capabilityProbeProcessID mints a fresh id per Capabilities call — unlike
// processID above, a probe has no caller-supplied AttemptID to derive one
// from, and a fixed literal would collide if two probes on the same
// Adapter ever raced (Supervisor rejects a re-used in-flight process ID).
func capabilityProbeProcessID() ports.ProcessID {
	return ports.ProcessID(string(ports.ProviderClaude) + ":capability-probe:" + strconv.FormatInt(time.Now().UnixNano(), 10))
}

func failedResult(request ports.AgentExecutionRequest, reason string) ports.AgentExecutionResult {
	return ports.AgentExecutionResult{
		AttemptID:         request.AttemptID,
		Provider:          ports.ProviderClaude,
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
	case "system":
		return n.consumeSystem(line, envelope.Type)
	case "assistant":
		return n.consumeAssistant(line, envelope.Type)
	case "user":
		return n.consumeUser(line, envelope.Type)
	case "result":
		return n.consumeResult(line, envelope.Type)
	case "stream_event", "tool_progress", "tool_use_summary", "rate_limit_event":
		return n.emit(ports.AgentEvent{Kind: ports.AgentEventStatusChanged, Message: envelope.Type}, envelope.Type)
	default:
		return n.emit(ports.AgentEvent{
			Kind: ports.AgentEventDiagnostic,
			Diagnostic: &ports.AgentDiagnostic{
				Code:    "UNRECOGNIZED_PROVIDER_EVENT",
				Message: "Claude event was not mapped to a domain action",
			},
		}, envelope.Type)
	}
}

func (n *normalizer) consumeSystem(line []byte, rawType string) error {
	var event struct {
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return fmt.Errorf("%w: decode system event: %v", ErrProtocol, err)
	}
	if event.Subtype != "init" {
		return n.emit(ports.AgentEvent{Kind: ports.AgentEventStatusChanged, Message: "system." + event.Subtype}, rawType)
	}
	if strings.TrimSpace(event.SessionID) == "" {
		return fmt.Errorf("%w: system init requires session_id", ErrProtocol)
	}
	n.session = &ports.ProviderSessionRef{Provider: ports.ProviderClaude, SessionID: event.SessionID}
	return n.emit(ports.AgentEvent{Kind: ports.AgentEventExecutionStarted, Session: cloneSession(n.session)}, rawType)
}

func (n *normalizer) consumeAssistant(line []byte, rawType string) error {
	var event struct {
		SessionID string        `json:"session_id"`
		Message   claudeMessage `json:"message"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return fmt.Errorf("%w: decode assistant event: %v", ErrProtocol, err)
	}
	if n.session == nil && event.SessionID != "" {
		n.session = &ports.ProviderSessionRef{Provider: ports.ProviderClaude, SessionID: event.SessionID}
	}
	if event.Message.Usage.hasValue() {
		n.usage = event.Message.Usage.canonical(0)
	}
	blocks, err := decodeContentBlocks(event.Message.Content)
	if err != nil {
		return fmt.Errorf("%w: assistant content: %v", ErrProtocol, err)
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if err := n.emit(ports.AgentEvent{Kind: ports.AgentEventAssistantMessage, Message: block.Text}, rawType); err != nil {
				return err
			}
		case "tool_use":
			if err := n.emit(ports.AgentEvent{
				Kind: ports.AgentEventToolCallStarted,
				Tool: &ports.AgentToolEvent{
					CallID: block.ID,
					Name:   block.Name,
					Input:  cloneRawJSON(block.Input),
				},
			}, rawType); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *normalizer) consumeUser(line []byte, rawType string) error {
	var event struct {
		Message claudeMessage `json:"message"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return fmt.Errorf("%w: decode user event: %v", ErrProtocol, err)
	}
	blocks, err := decodeContentBlocks(event.Message.Content)
	if err != nil {
		return fmt.Errorf("%w: user content: %v", ErrProtocol, err)
	}
	for _, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		if err := n.emit(ports.AgentEvent{
			Kind: ports.AgentEventToolCallFinished,
			Tool: &ports.AgentToolEvent{
				CallID:  block.ToolUseID,
				Name:    "tool_result",
				Output:  contentText(block.Content),
				IsError: block.IsError,
			},
		}, rawType); err != nil {
			return err
		}
	}
	return nil
}

func (n *normalizer) consumeResult(line []byte, rawType string) error {
	var event struct {
		Subtype      string      `json:"subtype"`
		IsError      bool        `json:"is_error"`
		SessionID    string      `json:"session_id"`
		Usage        claudeUsage `json:"usage"`
		TotalCostUSD float64     `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return fmt.Errorf("%w: decode result event: %v", ErrProtocol, err)
	}
	if event.SessionID != "" {
		n.session = &ports.ProviderSessionRef{Provider: ports.ProviderClaude, SessionID: event.SessionID}
	}
	n.usage = event.Usage.canonical(event.TotalCostUSD)
	n.terminalSeen = true
	n.providerOK = event.Subtype == "success" && !event.IsError
	if err := n.emit(ports.AgentEvent{Kind: ports.AgentEventUsageReported, Usage: cloneUsage(n.usage)}, rawType); err != nil {
		return err
	}
	if err := n.emit(ports.AgentEvent{Kind: ports.AgentEventCheckpointProposed, Session: cloneSession(n.session)}, rawType); err != nil {
		return err
	}
	if !n.providerOK {
		return n.emit(ports.AgentEvent{
			Kind: ports.AgentEventDiagnostic,
			Diagnostic: &ports.AgentDiagnostic{
				Code:    "PROVIDER_REPORTED_FAILURE",
				Message: "Claude reported a failed result",
			},
		}, rawType)
	}
	return nil
}

func (n *normalizer) emit(event ports.AgentEvent, rawType string) error {
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

func (n *normalizer) finish(status ports.AgentExecutionStatus, reason string) error {
	return n.emit(ports.AgentEvent{
		Kind:    ports.AgentEventExecutionFinished,
		Message: string(status),
		Diagnostic: &ports.AgentDiagnostic{
			Code:    reason,
			Message: "Claude execution finished",
		},
	}, "process.finished")
}

func (n *normalizer) finalStatus(
	process ports.ProcessResult,
	requireNewSession bool,
) (ports.AgentExecutionStatus, string, error) {
	if process.TimedOut {
		return ports.AgentExecutionTimedOut, "timeout", nil
	}
	if process.Cancelled {
		return ports.AgentExecutionCancelled, "cancelled", nil
	}
	if process.ExitCode != 0 {
		return ports.AgentExecutionFailed, "process_exit", nil
	}
	if !n.terminalSeen {
		return ports.AgentExecutionFailed, "protocol_incomplete", fmt.Errorf("%w: terminal result event is missing", ErrProtocol)
	}
	if requireNewSession && (n.session == nil || n.session.SessionID == "") {
		return ports.AgentExecutionFailed, "protocol_incomplete", fmt.Errorf("%w: system init event is missing", ErrProtocol)
	}
	if !n.providerOK {
		return ports.AgentExecutionFailed, "provider_failure", nil
	}
	return ports.AgentExecutionSucceeded, "completed", nil
}

func (n *normalizer) result(request ports.AgentExecutionRequest, process ports.ProcessResult) ports.AgentExecutionResult {
	return ports.AgentExecutionResult{
		AttemptID:  request.AttemptID,
		Provider:   ports.ProviderClaude,
		Session:    cloneSession(n.session),
		Usage:      n.usage,
		ExitCode:   process.ExitCode,
		StartedAt:  process.StartedAt,
		FinishedAt: process.FinishedAt,
	}
}

type claudeMessage struct {
	Content json.RawMessage `json:"content"`
	Usage   claudeUsage     `json:"usage"`
}

type claudeContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func decodeContentBlocks(content json.RawMessage) ([]claudeContentBlock, error) {
	if len(content) == 0 || string(content) == "null" {
		return nil, nil
	}
	var blocks []claudeContentBlock
	if err := json.Unmarshal(content, &blocks); err == nil {
		return blocks, nil
	}
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return []claudeContentBlock{{Type: "text", Text: text}}, nil
	}
	return nil, errors.New("content must be a string or block array")
}

func contentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

func (u claudeUsage) hasValue() bool {
	return u.InputTokens != 0 || u.CacheCreationInputTokens != 0 || u.CacheReadInputTokens != 0 || u.OutputTokens != 0
}

func (u claudeUsage) canonical(costUSD float64) ports.AgentUsage {
	return ports.AgentUsage{
		InputTokens:       u.InputTokens + u.CacheCreationInputTokens,
		CachedInputTokens: u.CacheReadInputTokens,
		OutputTokens:      u.OutputTokens,
		CostUSD:           costUSD,
	}
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
