package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestRepositoryLifecycleEvents_GoldenFixturesDecode proves RepositoryUpdated, RepositoryDisabled and RepositoryDeleted (V11-12) decode from their
// golden fixtures and that what the producer marshals decodes back to itself.
func TestRepositoryLifecycleEvents_GoldenFixturesDecode(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)
	cases := []struct {
		file      string
		eventType string
		version   int
		want      any
	}{
		{"repository_updated_v1.json", RepositoryUpdatedEventType, RepositoryUpdatedSchemaVersion, repositoryUpdatedEventPayload{
			RepositoryID: "repo-1", ProjectID: "project-1", Name: "svc", RemoteLocator: "D:/right/svc", DefaultRef: "main",
			PreviousName: "svc", PreviousRemoteLocator: "D:/wrong/svc", PreviousDefaultRef: "main", Status: "PROBING", ProbeJobID: "job-3",
		}},
		{"repository_disabled_v1.json", RepositoryDisabledEventType, RepositoryDisabledSchemaVersion, repositoryDisabledEventPayload{
			RepositoryID: "repo-1", ProjectID: "project-1", Name: "svc", PreviousStatus: "BLOCKED", Reason: "wrong folder", Actor: "actor-1",
		}},
		{"repository_deleted_v1.json", RepositoryDeletedEventType, RepositoryDeletedSchemaVersion, repositoryDeletedEventPayload{
			RepositoryID: "repo-1", ProjectID: "project-1", Name: "svc", RemoteLocator: "D:/wrong/svc", PreviousStatus: "BLOCKED", Reason: "typo", Actor: "actor-1",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join("testdata", "golden", tc.file))
			if err != nil {
				t.Fatalf("read golden fixture: %v", err)
			}
			got, err := registry.Decode(tc.eventType, tc.version, string(payload))
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Decode(%s v%d) = %+v, %v; want %+v", tc.eventType, tc.version, got, err, tc.want)
			}
			produced, _ := json.Marshal(tc.want)
			again, err := registry.Decode(tc.eventType, tc.version, string(produced))
			if err != nil || !reflect.DeepEqual(again, tc.want) {
				t.Fatalf("Decode(marshal(want)) = %+v, %v", again, err)
			}
		})
	}
}

// TestProjectLifecycleEvents_GoldenFixturesDecode proves ProjectRenamed, ProjectArchived and ProjectRestored (V11-13) decode from their golden fixtures.
func TestProjectLifecycleEvents_GoldenFixturesDecode(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)
	cases := []struct {
		file      string
		eventType string
		version   int
		want      any
	}{
		{"project_renamed_v1.json", ProjectRenamedEventType, ProjectRenamedSchemaVersion, projectRenamedEventPayload{
			ProjectID: "project-1", PreviousName: "old name", Name: "new name", Actor: "actor-1",
		}},
		{"project_archived_v1.json", ProjectArchivedEventType, ProjectArchivedSchemaVersion, projectStatusChangedEventPayload{
			ProjectID: "project-1", Name: "demo", PreviousStatus: "ACTIVE", Status: "ARCHIVED", Reason: "finished", Actor: "actor-1",
		}},
		{"project_restored_v1.json", ProjectRestoredEventType, ProjectRestoredSchemaVersion, projectStatusChangedEventPayload{
			ProjectID: "project-1", Name: "demo", PreviousStatus: "ARCHIVED", Status: "ACTIVE", Actor: "actor-1",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join("testdata", "golden", tc.file))
			if err != nil {
				t.Fatalf("read golden fixture: %v", err)
			}
			got, err := registry.Decode(tc.eventType, tc.version, string(payload))
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Decode(%s v%d) = %+v, %v; want %+v", tc.eventType, tc.version, got, err, tc.want)
			}
			produced, _ := json.Marshal(tc.want)
			again, err := registry.Decode(tc.eventType, tc.version, string(produced))
			if err != nil || !reflect.DeepEqual(again, tc.want) {
				t.Fatalf("Decode(marshal(want)) = %+v, %v", again, err)
			}
		})
	}
}
