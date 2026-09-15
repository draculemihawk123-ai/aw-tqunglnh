package work

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure"
// (docs/design/08-v6-api-projections.md V6-00A) retrofit for this
// package's six events: CreateRootWorkItem/CreateChildWorkItem
// (commands.go) and RequestScopeExpansion/ApproveScopeExpansion/
// RejectScopeExpansion/WithdrawScopeExpansion (scope_expansion.go) each
// originally appended an inline anonymous struct under a bare string
// EventType, with no eventschema.Registry decoder, golden fixture, or
// typed constant. The payload shapes below are unchanged from what their
// own files already marshaled; this task only names them and wires them
// through eventschema, never a business-transition change.

const (
	RootWorkItemCreatedEventType     = "RootWorkItemCreated"
	RootWorkItemCreatedSchemaVersion = 1

	ChildWorkItemCreatedEventType     = "ChildWorkItemCreated"
	ChildWorkItemCreatedSchemaVersion = 1

	ScopeExpansionRequestedEventType     = "ScopeExpansionRequested"
	ScopeExpansionRequestedSchemaVersion = 1

	ScopeExpansionApprovedEventType     = "ScopeExpansionApproved"
	ScopeExpansionApprovedSchemaVersion = 1

	ScopeExpansionRejectedEventType     = "ScopeExpansionRejected"
	ScopeExpansionRejectedSchemaVersion = 1

	ScopeExpansionWithdrawnEventType     = "ScopeExpansionWithdrawn"
	ScopeExpansionWithdrawnSchemaVersion = 1

	// WorkItemMarkedReadyEventType is deliberately SCREAMING_SNAKE_CASE,
	// unlike every other EventType constant in this file — ADR-028 §30 and
	// go-core-spec §8's own command table both name the wire value verbatim,
	// repeatedly, as "WORK_ITEM_MARKED_READY", the same vocabulary
	// internal/app/runtime's own WORK_ITEM_BLOCKED/WORK_ITEM_CANCELLED event
	// types already use for this exact "WORK_ITEM_*" family — not a
	// typo/inconsistency to "fix" into PascalCase.
	WorkItemMarkedReadyEventType     = "WORK_ITEM_MARKED_READY"
	WorkItemMarkedReadySchemaVersion = 1
)

// rootWorkItemCreatedEventPayload is RootWorkItemCreated v1's own shape
// (CreateRootWorkItem, commands.go).
type rootWorkItemCreatedEventPayload struct {
	WorkItemID     string `json:"workItemId"`
	ProjectID      string `json:"projectId"`
	FamilyID       string `json:"familyId"`
	WorkspaceSetID string `json:"workspaceSetId"`
	Title          string `json:"title"`
	ScopeCount     int    `json:"scopeCount"`
}

// childWorkItemCreatedEventPayload is ChildWorkItemCreated v1's own shape
// (CreateChildWorkItem, commands.go).
type childWorkItemCreatedEventPayload struct {
	WorkItemID       string `json:"workItemId"`
	ProjectID        string `json:"projectId"`
	FamilyID         string `json:"familyId"`
	ParentWorkItemID string `json:"parentWorkItemId"`
	Title            string `json:"title"`
	ScopeCount       int    `json:"scopeCount"`
}

// scopeExpansionRequestedEventPayload is ScopeExpansionRequested v1's own
// shape (RequestScopeExpansion, scope_expansion.go).
type scopeExpansionRequestedEventPayload struct {
	RequestID            string `json:"requestId"`
	FamilyID             string `json:"familyId"`
	ProjectID            string `json:"projectId"`
	ReferencedWorkItemID string `json:"referencedWorkItemId,omitempty"`
	GrantCount           int    `json:"grantCount"`
}

// scopeExpansionApprovedEventPayload is ScopeExpansionApproved v1's own
// shape (ApproveScopeExpansion, scope_expansion.go).
type scopeExpansionApprovedEventPayload struct {
	RequestID            string          `json:"requestId"`
	FamilyID             string          `json:"familyId"`
	ProjectID            string          `json:"projectId"`
	NewScopeVersion      uint64          `json:"newScopeVersion"`
	ApprovedGrants       []ApprovedGrant `json:"approvedGrants"`
	ReferencedWorkItemID string          `json:"referencedWorkItemId,omitempty"`
	ApprovedBy           string          `json:"approvedBy"`
}

// scopeExpansionRejectedEventPayload is ScopeExpansionRejected v1's own
// shape (RejectScopeExpansion, scope_expansion.go).
type scopeExpansionRejectedEventPayload struct {
	RequestID    string `json:"requestId"`
	FamilyID     string `json:"familyId"`
	DecisionNote string `json:"decisionNote"`
	RejectedBy   string `json:"rejectedBy"`
}

// scopeExpansionWithdrawnEventPayload is ScopeExpansionWithdrawn v1's own
// shape (WithdrawScopeExpansion, scope_expansion.go).
type scopeExpansionWithdrawnEventPayload struct {
	RequestID   string `json:"requestId"`
	FamilyID    string `json:"familyId"`
	WithdrawnBy string `json:"withdrawnBy"`
}

// workItemMarkedReadyEventPayload is WORK_ITEM_MARKED_READY v1's own shape
// (MarkWorkItemReady, mark_ready.go).
type workItemMarkedReadyEventPayload struct {
	WorkItemID string `json:"workItemId"`
	ProjectID  string `json:"projectId"`
	FamilyID   string `json:"familyId"`
	MarkedBy   string `json:"markedBy"`
}

func DecodeRootWorkItemCreatedV1(payloadJSON string) (any, error) {
	var payload rootWorkItemCreatedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeChildWorkItemCreatedV1(payloadJSON string) (any, error) {
	var payload childWorkItemCreatedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeScopeExpansionRequestedV1(payloadJSON string) (any, error) {
	var payload scopeExpansionRequestedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeScopeExpansionApprovedV1(payloadJSON string) (any, error) {
	var payload scopeExpansionApprovedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeScopeExpansionRejectedV1(payloadJSON string) (any, error) {
	var payload scopeExpansionRejectedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeScopeExpansionWithdrawnV1(payloadJSON string) (any, error) {
	var payload scopeExpansionWithdrawnEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeWorkItemMarkedReadyV1(payloadJSON string) (any, error) {
	var payload workItemMarkedReadyEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/runtime/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(RootWorkItemCreatedEventType, RootWorkItemCreatedSchemaVersion, DecodeRootWorkItemCreatedV1)
	registry.Register(ChildWorkItemCreatedEventType, ChildWorkItemCreatedSchemaVersion, DecodeChildWorkItemCreatedV1)
	registry.Register(ScopeExpansionRequestedEventType, ScopeExpansionRequestedSchemaVersion, DecodeScopeExpansionRequestedV1)
	registry.Register(ScopeExpansionApprovedEventType, ScopeExpansionApprovedSchemaVersion, DecodeScopeExpansionApprovedV1)
	registry.Register(ScopeExpansionRejectedEventType, ScopeExpansionRejectedSchemaVersion, DecodeScopeExpansionRejectedV1)
	registry.Register(ScopeExpansionWithdrawnEventType, ScopeExpansionWithdrawnSchemaVersion, DecodeScopeExpansionWithdrawnV1)
	registry.Register(WorkItemMarkedReadyEventType, WorkItemMarkedReadySchemaVersion, DecodeWorkItemMarkedReadyV1)
}
