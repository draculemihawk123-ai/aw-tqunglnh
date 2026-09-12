package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestRootWorkItemCreatedV1_GoldenFixtureDecodes is RootWorkItemCreated's
// own golden-fixture proof, mirroring internal/app/runtime/event_schema_test.go's
// own TestNodeRoutedV1_GoldenFixtureDecodes pattern (V6-00A).
func TestRootWorkItemCreatedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "root_work_item_created_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RootWorkItemCreatedEventType, RootWorkItemCreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RootWorkItemCreatedEventType, RootWorkItemCreatedSchemaVersion, err)
	}
	want := rootWorkItemCreatedEventPayload{
		WorkItemID: "work-item-1", ProjectID: "project-1", FamilyID: "family-1",
		WorkspaceSetID: "workspace-set-1", Title: "root task", ScopeCount: 1,
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RootWorkItemCreatedEventType, RootWorkItemCreatedSchemaVersion, got, want)
	}
}

// TestRootWorkItemCreatedV1_RealEventPayloadDecodes proves
// CreateRootWorkItem's own actual marshaled RootWorkItemCreated payload
// round-trips through the registered decoder.
func TestRootWorkItemCreatedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := rootWorkItemCreatedEventPayload{
		WorkItemID: "work-item-9", ProjectID: "project-9", FamilyID: "family-9",
		WorkspaceSetID: "workspace-set-9", Title: "root task nine", ScopeCount: 2,
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RootWorkItemCreatedEventType, RootWorkItemCreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestChildWorkItemCreatedV1_GoldenFixtureDecodes is ChildWorkItemCreated's
// own golden-fixture proof.
func TestChildWorkItemCreatedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "child_work_item_created_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(ChildWorkItemCreatedEventType, ChildWorkItemCreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", ChildWorkItemCreatedEventType, ChildWorkItemCreatedSchemaVersion, err)
	}
	want := childWorkItemCreatedEventPayload{
		WorkItemID: "work-item-2", ProjectID: "project-1", FamilyID: "family-1",
		ParentWorkItemID: "work-item-1", Title: "child task", ScopeCount: 1,
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", ChildWorkItemCreatedEventType, ChildWorkItemCreatedSchemaVersion, got, want)
	}
}

// TestChildWorkItemCreatedV1_RealEventPayloadDecodes proves
// CreateChildWorkItem's own actual marshaled ChildWorkItemCreated payload
// round-trips through the registered decoder.
func TestChildWorkItemCreatedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := childWorkItemCreatedEventPayload{
		WorkItemID: "work-item-19", ProjectID: "project-9", FamilyID: "family-9",
		ParentWorkItemID: "work-item-9", Title: "child task nine", ScopeCount: 3,
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(ChildWorkItemCreatedEventType, ChildWorkItemCreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestScopeExpansionRequestedV1_GoldenFixtureDecodes is
// ScopeExpansionRequested's own golden-fixture proof.
func TestScopeExpansionRequestedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "scope_expansion_requested_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(ScopeExpansionRequestedEventType, ScopeExpansionRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", ScopeExpansionRequestedEventType, ScopeExpansionRequestedSchemaVersion, err)
	}
	want := scopeExpansionRequestedEventPayload{
		RequestID: "scope-request-1", FamilyID: "family-1", ProjectID: "project-1",
		ReferencedWorkItemID: "work-item-2", GrantCount: 1,
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", ScopeExpansionRequestedEventType, ScopeExpansionRequestedSchemaVersion, got, want)
	}
}

// TestScopeExpansionRequestedV1_RealEventPayloadDecodes proves
// RequestScopeExpansion's own actual marshaled ScopeExpansionRequested
// payload round-trips through the registered decoder.
func TestScopeExpansionRequestedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := scopeExpansionRequestedEventPayload{
		RequestID: "scope-request-9", FamilyID: "family-9", ProjectID: "project-9", GrantCount: 2,
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(ScopeExpansionRequestedEventType, ScopeExpansionRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestScopeExpansionApprovedV1_GoldenFixtureDecodes is
// ScopeExpansionApproved's own golden-fixture proof. Compared with
// reflect-free field access since ApprovedGrants is a slice (Go's own ==
// cannot compare it) — asserted field-by-field like
// TestNodeForkedV1_GoldenFixtureDecodes uses reflect.DeepEqual for the
// same reason, just spelled out explicitly here for a single-element slice.
func TestScopeExpansionApprovedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "scope_expansion_approved_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(ScopeExpansionApprovedEventType, ScopeExpansionApprovedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", ScopeExpansionApprovedEventType, ScopeExpansionApprovedSchemaVersion, err)
	}
	gotPayload, ok := got.(scopeExpansionApprovedEventPayload)
	if !ok {
		t.Fatalf("Decode returned %T, want scopeExpansionApprovedEventPayload", got)
	}
	want := scopeExpansionApprovedEventPayload{
		RequestID: "scope-request-1", FamilyID: "family-1", ProjectID: "project-1", NewScopeVersion: 2,
		ApprovedGrants:       []ApprovedGrant{{RepositoryID: "repo-1", Access: "WRITE"}},
		ReferencedWorkItemID: "work-item-2", ApprovedBy: "actor-1",
	}
	if gotPayload.RequestID != want.RequestID || gotPayload.FamilyID != want.FamilyID ||
		gotPayload.ProjectID != want.ProjectID || gotPayload.NewScopeVersion != want.NewScopeVersion ||
		gotPayload.ReferencedWorkItemID != want.ReferencedWorkItemID || gotPayload.ApprovedBy != want.ApprovedBy ||
		len(gotPayload.ApprovedGrants) != 1 || gotPayload.ApprovedGrants[0] != want.ApprovedGrants[0] {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", ScopeExpansionApprovedEventType, ScopeExpansionApprovedSchemaVersion, gotPayload, want)
	}
}

// TestScopeExpansionApprovedV1_RealEventPayloadDecodes proves
// ApproveScopeExpansion's own actual marshaled ScopeExpansionApproved
// payload round-trips through the registered decoder.
func TestScopeExpansionApprovedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := scopeExpansionApprovedEventPayload{
		RequestID: "scope-request-9", FamilyID: "family-9", ProjectID: "project-9", NewScopeVersion: 5,
		ApprovedGrants: []ApprovedGrant{{RepositoryID: "repo-9", Access: "READ"}}, ApprovedBy: "actor-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(ScopeExpansionApprovedEventType, ScopeExpansionApprovedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	gotPayload, ok := got.(scopeExpansionApprovedEventPayload)
	if !ok {
		t.Fatalf("Decode returned %T, want scopeExpansionApprovedEventPayload", got)
	}
	if gotPayload.RequestID != produced.RequestID || len(gotPayload.ApprovedGrants) != 1 ||
		gotPayload.ApprovedGrants[0] != produced.ApprovedGrants[0] {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", gotPayload, produced)
	}
}

// TestScopeExpansionRejectedV1_GoldenFixtureDecodes is
// ScopeExpansionRejected's own golden-fixture proof.
func TestScopeExpansionRejectedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "scope_expansion_rejected_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(ScopeExpansionRejectedEventType, ScopeExpansionRejectedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", ScopeExpansionRejectedEventType, ScopeExpansionRejectedSchemaVersion, err)
	}
	want := scopeExpansionRejectedEventPayload{
		RequestID: "scope-request-1", FamilyID: "family-1", DecisionNote: "not needed", RejectedBy: "actor-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", ScopeExpansionRejectedEventType, ScopeExpansionRejectedSchemaVersion, got, want)
	}
}

// TestScopeExpansionRejectedV1_RealEventPayloadDecodes proves
// RejectScopeExpansion's own actual marshaled ScopeExpansionRejected
// payload round-trips through the registered decoder.
func TestScopeExpansionRejectedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := scopeExpansionRejectedEventPayload{
		RequestID: "scope-request-9", FamilyID: "family-9", DecisionNote: "budget", RejectedBy: "actor-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(ScopeExpansionRejectedEventType, ScopeExpansionRejectedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestScopeExpansionWithdrawnV1_GoldenFixtureDecodes is
// ScopeExpansionWithdrawn's own golden-fixture proof.
func TestScopeExpansionWithdrawnV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "scope_expansion_withdrawn_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(ScopeExpansionWithdrawnEventType, ScopeExpansionWithdrawnSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", ScopeExpansionWithdrawnEventType, ScopeExpansionWithdrawnSchemaVersion, err)
	}
	want := scopeExpansionWithdrawnEventPayload{RequestID: "scope-request-1", FamilyID: "family-1", WithdrawnBy: "actor-1"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", ScopeExpansionWithdrawnEventType, ScopeExpansionWithdrawnSchemaVersion, got, want)
	}
}

// TestScopeExpansionWithdrawnV1_RealEventPayloadDecodes proves
// WithdrawScopeExpansion's own actual marshaled ScopeExpansionWithdrawn
// payload round-trips through the registered decoder.
func TestScopeExpansionWithdrawnV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := scopeExpansionWithdrawnEventPayload{RequestID: "scope-request-9", FamilyID: "family-9", WithdrawnBy: "actor-9"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(ScopeExpansionWithdrawnEventType, ScopeExpansionWithdrawnSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
