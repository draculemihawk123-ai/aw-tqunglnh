package ports

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
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
	Provider        ProviderKey
	AdapterVersion  string
	ProtocolVersion string
	// TestedCLIVersion is the version the CONFIGURED executable actually
	// reports right now (V5-06: Capabilities probes it live, e.g. via
	// "--version"), never a hardcoded claim about what this adapter code
	// was built against — AdapterVersion/ProtocolVersion already cover
	// that. A caller that needs "what did we test this against" pins its
	// own known-good value elsewhere (e.g. an AdapterBuildVersion record)
	// and compares it to this field, rather than reading an assumption out
	// of this one.
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
//
// VCSObjectID/WorkspaceGeneration (V5-08B0, go-core-spec.md §14: "Mỗi
// WorkspaceMount chỉ rõ RepositoryID, revision/generation và READ_ONLY hoặc
// READ_WRITE") pin the exact revision this mount was resolved against —
// field names deliberately match workspace.Revision's own so a caller
// building this from a ContextSnapshot's own Revisions (workspace.RevisionSet)
// entry needs no translation.
type AgentWorkspaceMount struct {
	RepositoryID        project.RepositoryID
	Handle              WorkspaceHandle
	WorkingDirectory    string
	Access              WorkspaceAccess
	VCSObjectID         string
	WorkspaceGeneration uint64
}

// ContextSnapshotPin is a request's own pin of a V5-04
// internal/domain/contextsnapshot.Snapshot — deliberately a NEW, separate
// field from AgentExecutionRequest's own legacy ContextSnapshotID below
// (domainruntime.ContextSnapshotID names the pre-existing, spike-era
// runtime.ContextSnapshot/checkpoint-recovery system). The two
// context-snapshot systems coexist deliberately (see the contextsnapshot
// package's own doc comment: "no bridge between them, and no ID of one
// kind is ever loaded through the other kind's own repository") — a
// request built by V5-08B0's own assembler
// (internal/app/runtime.AssembleAgentExecutionRequest) populates this
// field and never the legacy one; legacy recovery (internal/app/worker)
// populates ContextSnapshotID and never this one.
type ContextSnapshotPin struct {
	ID           contextsnapshot.ID
	ManifestHash string
}

// AgentExecutionRequest is provider-neutral: go-core-spec.md §14's own
// AgentExecutionRequest, "tối thiểu" (at minimum) requiring AttemptID,
// ProviderKey, AdapterBuildVersion, InstructionArtifact, ContextSnapshot,
// WorkspaceMounts[], EffectiveScope, ExecutionProfileHash, IsolationProfile,
// AllowedCapabilities, Timeout, CancellationToken, optional
// RecoveryCheckpoint/IdempotencyKey. "Tối thiểu" is a floor, not a ceiling
// (docs/design/07-v5-execution-evidence.md's own V5-08B0 entry) — the
// pre-existing Prompt/WorkingDirectory/Environment/InheritedEnvironment/
// Model/Sandbox fields stay exactly as they were (every existing caller of
// this struct — claude.go, codex.go, internal/app/worker's legacy recovery
// path, spike acceptance — already reads/writes them via keyed struct
// literals, so this is a purely additive change).
//
// ProviderAdapter (claude.go/codex.go) must never infer permission/mount
// grants from Prompt's own content (go-core-spec.md §14) — WorkspaceMounts,
// EffectiveScope, AllowedCapabilities and IsolationProfile are the only
// authorization-bearing fields; Prompt is instruction content only.
type AgentExecutionRequest struct {
	AttemptID         ExecutionAttemptID
	ContextSnapshotID domainruntime.ContextSnapshotID

	// ProviderKey, AdapterBuildID and ContextSnapshot pin exactly which
	// provider/build/manifest this request was assembled against — an
	// adapter or fencing caller that needs to re-verify identity compares
	// against these, never re-derives them from Prompt or WorkspaceMounts.
	ProviderKey ProviderKey
	// AdapterBuildID names the exact registered adapterbuild.Build (ADR-022)
	// this request was verified against — a plain string (matching
	// internal/app/runtime's own resolvedExecutionProfileView.AdapterBuild.BuildID)
	// rather than the full domain Build value: a request is a wire-shaped
	// pin, not a place to carry an entire capability manifest.
	AdapterBuildID string
	// InstructionArtifact is the durable, hash-verified V5-01 Artifact a
	// deterministic materialization of task contract + messages + resources
	// was Put/Verified into (V5-08B0) — reuses ArtifactRef exactly as V5-01
	// defined it rather than inventing a parallel reference shape.
	InstructionArtifact ArtifactRef
	// ContextSnapshot pins the V5-04 Snapshot this request was assembled
	// from — see ContextSnapshotPin's own doc comment for why this is a
	// separate field from ContextSnapshotID above. Nil only for a request
	// built outside V5-08B0's own assembler (e.g. today's tests/spikes,
	// which predate it).
	ContextSnapshot *ContextSnapshotPin
	// EffectiveScope is the authorization-plane grant this Attempt's own
	// NodeRun actually carries (internal/domain/work.RepositoryScope) —
	// distinct from WorkspaceMounts, which is the execution-plane resolved
	// detail (opaque Handle, working directory, exact revision/generation).
	// A fencing caller can cross-check the two agree without re-deriving
	// either from the other.
	EffectiveScope []work.RepositoryScope
	// ExecutionProfileHash pins the exact ResolvedExecutionProfileV1 this
	// Attempt was admitted under (internal/app/runtime's own
	// resolvedExecutionProfileView/running.ExecutionProfileHash).
	ExecutionProfileHash string
	// IsolationProfile is the exact IsolationTier (ADR-013/ADR-023) this
	// Attempt was admitted under.
	IsolationProfile policy.IsolationTier
	// AllowedCapabilities is the exact granted-capability set this Attempt
	// was admitted under (internal/app/runtime admission's own
	// profile.AllowedCapabilities).
	AllowedCapabilities []string
	// CancellationToken is a placeholder field for V5-08C's own
	// cancellation-execution-path wiring — always empty until that task
	// gives it real meaning; declared now only so AgentExecutionRequest's
	// shape already matches go-core-spec.md §14 in full.
	CancellationToken string
	// RecoveryCheckpoint is optional (go-core-spec.md §14's own "?") —
	// V5-13's own checkpoint/recovery integration is what gives this real
	// meaning; nil until then.
	RecoveryCheckpoint *string
	// IdempotencyKey is this request's own dedupe key — AttemptID itself
	// is already a durable, unique identity for exactly one execution
	// intent (ADR-005: "Alpha luôn khởi động agent mới từ ContextSnapshot"
	// — no Attempt is ever legitimately started twice), so V5-08B0's own
	// assembler sets this to AttemptID's own string value rather than
	// minting a second, parallel identity.
	IdempotencyKey string
	// AllowedOutcomes is the node's own agent-selectable Outcomes — its
	// declared Outcomes minus its own CyclePolicy.EscalationOutcome, if
	// any (V5-08B, confirmed with the user 2026-09-09: the runtime always
	// assigns the escalation outcome itself, on a SKIPPED NodeRun, once a
	// cycle exhausts its own MaxIterations — the agent is never invoked
	// for that round and never needs to name it). Always has at least one
	// element (internal/domain/workflow's own validation rejects an AGENT
	// node whose every declared outcome IS its own escalation outcome).
	// A provider adapter validates a parsed terminal outcome marker
	// against this exact set — see AgentProposedOutcome's own doc comment.
	AllowedOutcomes []string

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

// AgentOutcomeSource records how AgentProposedOutcome.Value was determined
// — V5-08B (confirmed with the user 2026-09-09), distinguishing a value the
// provider itself reported (via the terminal <agentkit-outcome> marker,
// AgentProposedOutcome's own doc comment) from one the bridge derived on
// the provider's behalf because no report was possible.
type AgentOutcomeSource string

const (
	// AgentOutcomeReportedByProvider means the provider's own terminal
	// assistant message carried a valid <agentkit-outcome> marker naming
	// this Value — required whenever AgentExecutionRequest.AllowedOutcomes
	// has more than one entry (a real choice existed).
	AgentOutcomeReportedByProvider AgentOutcomeSource = "REPORTED_BY_PROVIDER"
	// AgentOutcomeDerivedSingleAllowed means no marker was required or
	// present: AgentExecutionRequest.AllowedOutcomes had exactly one
	// entry, so the bridge derived Value from the workflow's own pinned
	// declaration rather than asking the provider to self-report a choice
	// that was never actually a choice.
	AgentOutcomeDerivedSingleAllowed AgentOutcomeSource = "DERIVED_SINGLE_ALLOWED"
)

// AgentProposedOutcome is a PROPOSAL, never trusted as-is — exactly like
// NodeExecutionResult.SelectedOutcome, which this eventually becomes once
// FinalizeExecutionAttempt's own allow-list/evidence checks accept it
// (V5-08B, confirmed with the user 2026-09-09, after finding claude.go/
// codex.go had no existing mechanism for a provider to self-report which
// of a node's own declared Outcomes it selected — go-core-spec.md §14
// requires this "typed proposed outcome" but AgentExecutionResult never
// had a field for one before this task).
//
// The wire mechanism is a single terminal marker — the LAST content
// (outside whitespace) of the provider's own FINAL assistant message,
// exactly one occurrence across the whole execution:
//
//	<agentkit-outcome>{"schemaVersion":1,"outcome":"pass"}</agentkit-outcome>
//
// claude.go/codex.go strip this marker from the ASSISTANT_MESSAGE event
// they persist (it never reaches durable storage) and populate this field
// only once the terminal provider event confirms that message really was
// final. A missing marker when AllowedOutcomes has more than one entry, a
// duplicate marker anywhere in the run, a malformed body, or a Value
// outside AllowedOutcomes are ALL protocol errors (AgentExecutionStatus
// stays FAILED, this field stays nil) — never silently defaulted to
// AllowedOutcomes[0].
type AgentProposedOutcome struct {
	Value         string
	Source        AgentOutcomeSource
	SchemaVersion int
}

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
	// TreeQuiesced mirrors ProcessResult.TreeQuiesced (V5-08B) — a
	// provider adapter that spawns via ports.ProcessSupervisor copies its
	// own Run result through; a mutating attempt's evidence bridge must
	// never trust a final diff measured while this is false.
	TreeQuiesced bool
	// ArtifactRefs names every durable artifact.Artifact.ID this execution
	// itself produced (V5-08B, go-core-spec.md §14's own "AgentExecutionResult
	// phải có artifact refs"). Neither claude.go nor codex.go emits an
	// ARTIFACT_PRODUCED AgentEvent today, so this stays empty coming out of
	// either adapter — the shape exists now (the same "floor, not ceiling"
	// precedent V5-08B0 already set for AgentExecutionRequest) for a future
	// adapter capability, not invented here.
	ArtifactRefs []string
	// ProposedOutcome is populated only when Status is
	// AgentExecutionSucceeded — see AgentProposedOutcome's own doc comment
	// for the full terminal-marker contract and every rejection case.
	ProposedOutcome *AgentProposedOutcome
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
	// TreeQuiesced is true only when Supervisor confirmed every process it
	// ever grouped under spec.ID (the direct child and any descendants it
	// spawned) has genuinely exited by the time Run returns — checked on
	// EVERY exit path, including a plain, successful process exit, not
	// only cancellation/timeout (V5-08B's own normal-exit quiescence
	// postcondition: a parent that exits while a descendant keeps
	// running/writing must never look identical to a fully quiesced
	// tree). false means the caller must NOT trust the working directory
	// as settled — a mutating attempt must never measure its own final
	// diff, or commit any evidence derived from one, while this is false.
	TreeQuiesced bool
}

// ProcessSupervisor executes an executable directly with argv. stdout and
// stderr are independent streams so a provider adapter can parse JSONL while
// retaining bounded diagnostics.
type ProcessSupervisor interface {
	Run(context.Context, ProcessSpec, io.Writer, io.Writer) (ProcessResult, error)
	Cancel(context.Context, ProcessID) error
}
