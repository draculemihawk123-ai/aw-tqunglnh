package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestRedoEvents_GoldenFixturesDecode proves WORK_ITEM_RETRIED and WORK_ITEM_RECREATED (V11-10) decode from their golden
// fixtures, the same replayability guarantee every registered event has.
func TestRedoEvents_GoldenFixturesDecode(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	cases := []struct {
		file      string
		eventType string
		version   int
		want      any
	}{
		{"work_item_retried_v1.json", WorkItemRetriedEventType, WorkItemRetriedSchemaVersion, workItemRetriedEventPayload{
			WorkItemID: "work-item-1", PreviousRunID: "run-1", RunID: "run-2", WorkflowVersionID: "wf-v1",
			ResolvedBlockerIDs: []string{"blocker-1"}, NoteMessageID: "message-1", Actor: "actor-1",
		}},
		{"work_item_recreated_v1.json", WorkItemRecreatedEventType, WorkItemRecreatedSchemaVersion, workItemRecreatedEventPayload{
			OldWorkItemID: "work-item-1", NewWorkItemID: "work-item-2", RunID: "run-2", PreviousWorkflowVersionID: "wf-v1",
			WorkflowVersionID: "wf-v2", RedoNumber: 2, CarriedMessageCount: 1, Reason: "kit fixed", Actor: "actor-1",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join("testdata", "golden", tc.file))
			if err != nil {
				t.Fatalf("read golden fixture: %v", err)
			}
			got, err := registry.Decode(tc.eventType, tc.version, string(payload))
			if err != nil {
				t.Fatalf("Decode(%s v%d): %v", tc.eventType, tc.version, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Decode(%s v%d) = %+v, want %+v", tc.eventType, tc.version, got, tc.want)
			}
			// the producer's own payload must decode back to itself
			produced, err := json.Marshal(tc.want)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			again, err := registry.Decode(tc.eventType, tc.version, string(produced))
			if err != nil || !reflect.DeepEqual(again, tc.want) {
				t.Fatalf("Decode(marshal(want)) = %+v, %v; want %+v", again, err, tc.want)
			}
		})
	}
}
