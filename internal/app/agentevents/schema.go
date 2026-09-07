// Package agentevents is V5-08A's production AgentEvent contract
// (docs/design/07-v5-execution-evidence.md V5-08A): a real
// ports.AgentEventSink that normalizes every ports.AgentEvent a live
// ports.AgentExecutor.Start/Resume call streams through registry V1-07A
// (internal/app/eventschema), persists it durably (bounded batching, never
// one round trip per event), captures an immutable Checkpoint with a
// scope-validated workspace diff whenever the provider proposes one, and
// redacts every payload before it ever reaches storage.
//
// This package deliberately stops at the sink/checkpoint/diff contract — it
// never decides an ExecutionAttempt's terminal state and never calls
// FinalizeExecutionAttempt. Per V5-08's own explicit scope note (confirmed
// with the user before writing this package): the real
// ports.NodeExecutor bridge that calls AgentExecutor.Start/Resume as part
// of ExecuteNodeHandler's own production dispatch, maps the result to
// ports.NodeExecutionResult and feeds the existing fenced finalize
// transaction is V5-08B's job ("worker chỉ propose outcome; commit chỉ xảy
// ra khi toàn bộ fencing còn hợp lệ") — V5-08B additionally hardens that
// finalize transaction with the diff-scope/evidence/generation checks a
// real (non-fake) executor result newly requires. Nothing in this package
// is wired into ExecuteNodeHandler; its own tests drive a Sink directly
// against a fake or real AgentExecutor.
//
// HE-01-M03 ("agent output MUST carry a structured assumption/question
// list; an unresolved material assumption transitions the node to
// WAITING/NEEDS_INFO") is cited as this task's own rationale, not a build
// item: like ADR-005/ADR-017 (this task's other two cited sources, both
// policy context rather than a literal build requirement), it explains WHY
// raw provider metadata must never be trusted to route state on its own —
// it must always pass through this package's own registry+redaction
// contract first. This package does NOT add an Assumption type to
// ports.AgentEvent/AgentDiagnostic, does not infer assumptions from
// provider text, and does not transition any NodeRun to WAITING — a real
// structured assumption-surface/NEEDS_INFO feature remains a separate,
// not-yet-scheduled obligation (confirmed with the user before writing
// this package: HE-01-M03 stays unimplemented, not silently marked done).
package agentevents

import (
	"encoding/json"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// schemaVersionV1 is the one schema version every AgentEventKind is
// registered at today. A future breaking payload change registers
// schemaVersionV1+1 for that one kind plus an eventschema.Upcaster, exactly
// like every other registry consumer in this codebase — it never edits
// what version 1's own Decoder expects.
const schemaVersionV1 = 1

// Payload is the exact shape persisted into agent_events.payload_json —
// every ports.AgentEvent field except AttemptID/Sequence/Kind, which are
// already their own columns (ports.AgentEventRecord). ObservedAt is kept
// here (not promoted to its own column) since agent_events.created_at
// already carries it — see Sink.buildRecord's own doc comment.
type Payload struct {
	ObservedAt       time.Time                 `json:"observedAt"`
	Message          string                    `json:"message,omitempty"`
	Tool             *ports.AgentToolEvent     `json:"tool,omitempty"`
	Usage            *ports.AgentUsage         `json:"usage,omitempty"`
	Session          *ports.ProviderSessionRef `json:"session,omitempty"`
	Diagnostic       *ports.AgentDiagnostic    `json:"diagnostic,omitempty"`
	ProviderMetadata map[string]string         `json:"providerMetadata,omitempty"`
}

// decodePayload is the eventschema.Decoder registered for every
// ports.AgentEventKind at schemaVersionV1 — a plain, deterministic JSON
// decode into Payload, never anything that inspects or routes on the
// decoded value (this package's own "raw provider metadata không route
// state" bar: the registry only proves an event is well-formed and
// known, it does not interpret it).
func decodePayload(payloadJSON string) (any, error) {
	var payload Payload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// agentEventKinds is every ports.AgentEventKind this package registers —
// kept as its own slice (rather than inlined into RegisterEventSchemas) so
// Sink's own registration-membership check
// (registry.IsRegistered(string(kind), schemaVersionV1)) and this
// package's tests can both enumerate the exact set once.
var agentEventKinds = []ports.AgentEventKind{
	ports.AgentEventExecutionStarted,
	ports.AgentEventStatusChanged,
	ports.AgentEventAssistantMessage,
	ports.AgentEventToolCallStarted,
	ports.AgentEventToolCallFinished,
	ports.AgentEventArtifactProduced,
	ports.AgentEventUsageReported,
	ports.AgentEventCheckpointProposed,
	ports.AgentEventDiagnostic,
	ports.AgentEventExecutionFinished,
}

// RegisterEventSchemas registers every ports.AgentEventKind's Decoder into
// registry at schemaVersionV1 — mirroring runtime.RegisterEventSchemas's
// own "one function a composition root calls once at startup" shape
// (internal/app/runtime/event_schema.go). Registering the same Registry
// twice panics (Registry.Register's own documented behavior on a duplicate
// key) — callers (composition roots, tests) call this exactly once per
// Registry instance, the same discipline runtime.RegisterEventSchemas'
// own callers already follow.
func RegisterEventSchemas(registry *eventschema.Registry) {
	for _, kind := range agentEventKinds {
		registry.Register(string(kind), schemaVersionV1, decodePayload)
	}
}
