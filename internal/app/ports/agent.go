package ports

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ExecutionAttemptID aliases the runtime aggregate identity at the outbound
// port so adapters never invent a parallel identity from provider sessions.
type ExecutionAttemptID = domainruntime.ExecutionAttemptID

type ProviderKey string

const (
	ProviderClaude ProviderKey = "claude"
	ProviderCodex  ProviderKey = "codex"
)

type AgentCapabilities struct {
	Provider            ProviderKey
	AdapterVersion      string
	ProtocolVersion     string
	TestedCLIVersion    string
	SupportsStart       bool
	SupportsResume      bool
	SupportsCancel      bool
	CanonicalEventKinds []AgentEventKind
}

type WorkspaceAccess string

const (
	WorkspaceReadOnly  WorkspaceAccess = "READ_ONLY"
	WorkspaceReadWrite WorkspaceAccess = "READ_WRITE"
)

// AgentWorkspaceMount is an already-resolved execution-plane mount. The
// opaque Handle remains the identity; WorkingDirectory is passed only to the
// worker-side process adapter and must not be persisted as repository identity.
type AgentWorkspaceMount struct {
	RepositoryID     project.RepositoryID
	Handle           WorkspaceHandle
	WorkingDirectory string
	Access           WorkspaceAccess
}

type AgentExecutionRequest struct {
	AttemptID            ExecutionAttemptID
	ContextSnapshotID    domainruntime.ContextSnapshotID
	Prompt               string
	WorkingDirectory     string
	WorkspaceMounts      []AgentWorkspaceMount
	Environment          map[string]string
	InheritedEnvironment []string
	Timeout              time.Duration
	Model                string
	Sandbox              string
}

type ProviderSessionRef struct {
	Provider  ProviderKey
	SessionID string
}

type AgentEventKind string

const (
	AgentEventExecutionStarted   AgentEventKind = "EXECUTION_STARTED"
	AgentEventStatusChanged      AgentEventKind = "STATUS_CHANGED"
	AgentEventAssistantMessage   AgentEventKind = "ASSISTANT_MESSAGE"
	AgentEventToolCallStarted    AgentEventKind = "TOOL_CALL_STARTED"
	AgentEventToolCallFinished   AgentEventKind = "TOOL_CALL_FINISHED"
	AgentEventArtifactProduced   AgentEventKind = "ARTIFACT_PRODUCED"
	AgentEventUsageReported      AgentEventKind = "USAGE_REPORTED"
	AgentEventCheckpointProposed AgentEventKind = "CHECKPOINT_PROPOSED"
	AgentEventDiagnostic         AgentEventKind = "DIAGNOSTIC"
	AgentEventExecutionFinished  AgentEventKind = "EXECUTION_FINISHED"
)

type AgentToolEvent struct {
	CallID   string
	Name     string
	Input    json.RawMessage
	Output   string
	IsError  bool
	ExitCode *int
}

type AgentUsage struct {
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	CostUSD           float64
}

type AgentDiagnostic struct {
	Code    string
	Message string
}

type AgentEvent struct {
	AttemptID        ExecutionAttemptID
	Sequence         uint64
	Kind             AgentEventKind
	ObservedAt       time.Time
	Message          string
	Tool             *AgentToolEvent
	Usage            *AgentUsage
	Session          *ProviderSessionRef
	Diagnostic       *AgentDiagnostic
	ProviderMetadata map[string]string
}

type AgentEventSink interface {
	Accept(context.Context, AgentEvent) error
}

type AgentEventSinkFunc func(context.Context, AgentEvent) error

func (f AgentEventSinkFunc) Accept(ctx context.Context, event AgentEvent) error {
	return f(ctx, event)
}

type AgentExecutionStatus string

const (
	AgentExecutionSucceeded AgentExecutionStatus = "SUCCEEDED"
	AgentExecutionFailed    AgentExecutionStatus = "FAILED"
	AgentExecutionTimedOut  AgentExecutionStatus = "TIMED_OUT"
	AgentExecutionCancelled AgentExecutionStatus = "CANCELLED"
)

type AgentExecutionResult struct {
	AttemptID         ExecutionAttemptID
	Provider          ProviderKey
	Status            AgentExecutionStatus
	TerminationReason string
	Session           *ProviderSessionRef
	Usage             AgentUsage
	ExitCode          int
	StartedAt         time.Time
	FinishedAt        time.Time
}

// AgentExecutor is provider-neutral. Start and Resume block until the
// provider process terminates while events are streamed to sink. A concurrent
// caller may cancel the active process using the stable attempt ID.
type AgentExecutor interface {
	Capabilities(context.Context) (AgentCapabilities, error)
	Start(context.Context, AgentExecutionRequest, AgentEventSink) (AgentExecutionResult, error)
	Resume(context.Context, AgentExecutionRequest, ProviderSessionRef, AgentEventSink) (AgentExecutionResult, error)
	Cancel(context.Context, ExecutionAttemptID) error
}

type ProcessID string

// ProcessSpec is intentionally argv-based. It has no command-string or shell
// field. Environment contains exact overrides; InheritedEnvironment is an
// explicit allow-list of parent environment keys. WorkingDirectory must be
// an absolute path to an existing directory (V5-05) — a relative path would
// resolve against the calling process's own cwd, an ambiguous, implicit
// trust boundary this codebase's own "explicit, not implicit" discipline
// rejects.
type ProcessSpec struct {
	ID                   ProcessID
	Executable           string
	Argv                 []string
	WorkingDirectory     string
	Environment          map[string]string
	InheritedEnvironment []string
	Stdin                []byte
	Timeout              time.Duration
	// GracePeriod is how long Cancel (or Timeout firing) waits after
	// asking the process tree to exit gracefully before force-killing it
	// (V5-05). Zero uses Supervisor's own default.
	GracePeriod time.Duration
	// OutputLimitBytes bounds how many bytes of stdout/stderr each
	// Supervisor implementation keeps — a runaway child must never be able
	// to exhaust worker memory. Zero uses Supervisor's own default rather
	// than meaning "unbounded"; this port has no unbounded option (V5-05).
	OutputLimitBytes int
}

type ProcessResult struct {
	ID         ProcessID
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	TimedOut   bool
	Cancelled  bool
	// OutputTruncated is true when stdout and/or stderr hit
	// OutputLimitBytes and further bytes were discarded (V5-05) — the
	// caller must never treat a truncated capture as a complete one for
	// evidence purposes.
	OutputTruncated bool
}

// ProcessSupervisor executes an executable directly with argv. stdout and
// stderr are independent streams so a provider adapter can parse JSONL while
// retaining bounded diagnostics.
type ProcessSupervisor interface {
	Run(context.Context, ProcessSpec, io.Writer, io.Writer) (ProcessResult, error)
	Cancel(context.Context, ProcessID) error
}
