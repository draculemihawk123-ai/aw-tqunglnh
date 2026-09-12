package sqlite

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure"
// (docs/design/08-v6-api-projections.md V6-00A) retrofit for three
// events found only by a second, deeper sweep: workspace_lifecycle.go's
// own Quarantine/Release/RecreateRepositoryWorkspace each append their
// own domain event via a raw `INSERT INTO domain_events` inside this
// package — entirely bypassing ports.EventsRepository.Append (and thus
// eventschema.EnforcingEventsRepository, even once something finally
// wires it into a real composition root) — which is exactly why the
// first pass over internal/app/*'s own ports.DomainEvent{}/Events().Append(
// call sites missed them. They previously used an inline
// map[string]string payload; this task types them (repositoryWorkspaceEvent.payload
// is now `any`, not `map[string]string`) without changing the JSON shape
// each one already produced.
//
// This is a real, separate architectural fact worth carrying forward,
// not just a decode gap: even after V6-00A's own catalog closes and
// EnforcingEventsRepository is eventually wired in, THESE three events
// would still bypass that enforcement, because they never call
// ports.EventsRepository.Append at all. Closing that gap for real would
// mean routing this package's own raw domain_events inserts through the
// same interface every app-layer command already uses — a bigger,
// separate refactor V6-00A's own "Không làm: không đổi business
// transition" deliberately does not attempt here.

const (
	RepositoryWorkspaceQuarantinedEventType     = "REPOSITORY_WORKSPACE_QUARANTINED"
	RepositoryWorkspaceQuarantinedSchemaVersion = 1

	RepositoryWorkspaceReleasedEventType     = "REPOSITORY_WORKSPACE_RELEASED"
	RepositoryWorkspaceReleasedSchemaVersion = 1

	RepositoryWorkspaceRecreatedEventType     = "REPOSITORY_WORKSPACE_RECREATED"
	RepositoryWorkspaceRecreatedSchemaVersion = 1
)

// repositoryWorkspaceQuarantinedEventPayload is
// REPOSITORY_WORKSPACE_QUARANTINED v1's own shape
// (quarantineRepositoryWorkspaceTx, workspace_lifecycle.go).
type repositoryWorkspaceQuarantinedEventPayload struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	Reason                string `json:"reason"`
}

// repositoryWorkspaceReleasedEventPayload is
// REPOSITORY_WORKSPACE_RELEASED v1's own shape
// (Store.ReleaseRepositoryWorkspace, workspace_lifecycle.go).
type repositoryWorkspaceReleasedEventPayload struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
}

// repositoryWorkspaceRecreatedEventPayload is
// REPOSITORY_WORKSPACE_RECREATED v1's own shape
// (Store.RecreateRepositoryWorkspace, workspace_lifecycle.go) — Generation
// stays a string, matching the original fmt.Sprintf("%d", ...) exactly,
// so this retrofit changes no caller-visible JSON type.
type repositoryWorkspaceRecreatedEventPayload struct {
	PreviousRepositoryWorkspaceID string `json:"previousRepositoryWorkspaceId"`
	RepositoryWorkspaceID         string `json:"repositoryWorkspaceId"`
	Generation                    string `json:"generation"`
}

func DecodeRepositoryWorkspaceQuarantinedV1(payloadJSON string) (any, error) {
	var payload repositoryWorkspaceQuarantinedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeRepositoryWorkspaceReleasedV1(payloadJSON string) (any, error) {
	var payload repositoryWorkspaceReleasedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeRepositoryWorkspaceRecreatedV1(payloadJSON string) (any, error) {
	var payload repositoryWorkspaceRecreatedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// This second group is the same second-sweep finding, for three more
// events found in node_dispatch.go/workflow_store.go — runtime-engine
// concepts (NodeRun/WorkflowRun), but appended via the identical raw-SQL,
// ports.EventsRepository-bypassing pattern as the three above, so their
// decoders live here rather than in internal/app/runtime/event_schema.go
// (which owns only the events IT actually appends through
// ports.EventsRepository.Append). The event_type/schema_version values
// stay SQL literals at each call site (`'NODE_RUN_COMPLETED', 1`, etc.)
// — untouched, since threading a shared Go constant through would mean
// parameterizing SQL that works fine as literals today, a cosmetic
// change beyond this task's own decoder-closure scope; the constants
// below exist only for the registry's own bookkeeping and must be kept
// byte-for-byte in sync with those literals by inspection.

const (
	NodeRunCompletedEventType     = "NODE_RUN_COMPLETED"
	NodeRunCompletedSchemaVersion = 1

	NodeRunDispatchedEventType     = "NODE_RUN_DISPATCHED"
	NodeRunDispatchedSchemaVersion = 1

	WorkflowRunFinalizedEventType     = "WORKFLOW_RUN_FINALIZED"
	WorkflowRunFinalizedSchemaVersion = 1
)

// nodeRunCompletedEventPayload is NODE_RUN_COMPLETED v1's own shape
// (Store.CompleteNodeAndDispatchNext, node_dispatch.go).
type nodeRunCompletedEventPayload struct {
	NodeRunID       string `json:"nodeRunId"`
	SelectedOutcome string `json:"selectedOutcome"`
}

// nodeRunDispatchedEventPayload is NODE_RUN_DISPATCHED v1's own shape
// (node_dispatch.go).
type nodeRunDispatchedEventPayload struct {
	RunID     string `json:"runId"`
	NodeRunID string `json:"nodeRunId"`
	NodeKey   string `json:"nodeKey"`
}

// workflowRunFinalizedEventPayload is WORKFLOW_RUN_FINALIZED v1's own
// shape (workflow_store.go).
type workflowRunFinalizedEventPayload struct {
	JobID         string `json:"jobId"`
	JobLeaseOwner string `json:"jobLeaseOwner"`
	RunID         string `json:"runId"`
	TerminalState string `json:"terminalState"`
}

func DecodeNodeRunCompletedV1(payloadJSON string) (any, error) {
	var payload nodeRunCompletedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeNodeRunDispatchedV1(payloadJSON string) (any, error) {
	var payload nodeRunDispatchedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeWorkflowRunFinalizedV1(payloadJSON string) (any, error) {
	var payload workflowRunFinalizedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/runtime/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(RepositoryWorkspaceQuarantinedEventType, RepositoryWorkspaceQuarantinedSchemaVersion, DecodeRepositoryWorkspaceQuarantinedV1)
	registry.Register(RepositoryWorkspaceReleasedEventType, RepositoryWorkspaceReleasedSchemaVersion, DecodeRepositoryWorkspaceReleasedV1)
	registry.Register(RepositoryWorkspaceRecreatedEventType, RepositoryWorkspaceRecreatedSchemaVersion, DecodeRepositoryWorkspaceRecreatedV1)
	registry.Register(NodeRunCompletedEventType, NodeRunCompletedSchemaVersion, DecodeNodeRunCompletedV1)
	registry.Register(NodeRunDispatchedEventType, NodeRunDispatchedSchemaVersion, DecodeNodeRunDispatchedV1)
	registry.Register(WorkflowRunFinalizedEventType, WorkflowRunFinalizedSchemaVersion, DecodeWorkflowRunFinalizedV1)
}
