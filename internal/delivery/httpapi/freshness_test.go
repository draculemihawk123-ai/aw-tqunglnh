package httpapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// TestFreshness_GoldenFixtureDecodes proves Freshness's wire shape is
// frozen: the golden fixture decodes into exactly the expected value.
// Mirrors internal/app/catalog/event_schema_test.go's own
// golden-fixture-decode pattern.
func TestFreshness_GoldenFixtureDecodes(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "freshness_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	var got httpapi.Freshness
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal golden fixture: %v", err)
	}
	want := httpapi.Freshness{Generation: 3, AsOfJournalPosition: 10245, Status: httpapi.FreshnessDegraded}
	if got != want {
		t.Fatalf("Freshness golden decode = %+v, want %+v", got, want)
	}
}

// TestFreshness_RoundTripsThroughMarshal proves a real Freshness value
// marshals to JSON and back to the identical value — mirroring
// internal/app/catalog/event_schema_test.go's own
// "RealEventPayloadDecodes" round-trip pattern.
func TestFreshness_RoundTripsThroughMarshal(t *testing.T) {
	produced := httpapi.Freshness{Generation: 9, AsOfJournalPosition: 555, Status: httpapi.FreshnessLive}
	raw, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced Freshness: %v", err)
	}
	var got httpapi.Freshness
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal marshaled Freshness: %v", err)
	}
	if got != produced {
		t.Fatalf("round-trip = %+v, want %+v", got, produced)
	}
}

// TestValidAction_GoldenFixtureDecodes proves ValidAction's wire shape is
// frozen, mirroring TestFreshness_GoldenFixtureDecodes above.
func TestValidAction_GoldenFixtureDecodes(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "valid_action_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	var got httpapi.ValidAction
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal golden fixture: %v", err)
	}
	want := httpapi.ValidAction{OperationID: "resolveWorkItemBlocker", ScopeKind: httpapi.ScopeProject, TargetVersion: 7}
	if got != want {
		t.Fatalf("ValidAction golden decode = %+v, want %+v", got, want)
	}
}

// TestValidAction_RoundTripsThroughMarshal mirrors
// TestFreshness_RoundTripsThroughMarshal above, for ValidAction.
func TestValidAction_RoundTripsThroughMarshal(t *testing.T) {
	produced := httpapi.ValidAction{OperationID: "cancelRun", ScopeKind: httpapi.ScopeInstallation, TargetVersion: 1}
	raw, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced ValidAction: %v", err)
	}
	var got httpapi.ValidAction
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal marshaled ValidAction: %v", err)
	}
	if got != produced {
		t.Fatalf("round-trip = %+v, want %+v", got, produced)
	}
}
