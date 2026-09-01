package eventschema_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// projectCreatedV1/V2 and the decode/upcast functions below are an
// illustrative example schema (ProjectCreated gaining owner_id in v2) —
// not a real production event type. They exist to prove the registry +
// decoder/upcaster + golden fixture contract end to end, exactly the way
// V1-06's handleCreateProject proved UnitOfWork's contract without being
// real production wiring.

type projectCreatedV1 struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
}

type projectCreatedV2 struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	OwnerID   string `json:"owner_id"` // added in v2 — the breaking change that forced a new schema version
}

func decodeProjectCreatedV1(payloadJSON string) (any, error) {
	var v projectCreatedV1
	if err := json.Unmarshal([]byte(payloadJSON), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func decodeProjectCreatedV2(payloadJSON string) (any, error) {
	var v projectCreatedV2
	if err := json.Unmarshal([]byte(payloadJSON), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func upcastProjectCreatedV1ToV2(previous any) (any, error) {
	v1, ok := previous.(projectCreatedV1)
	if !ok {
		return nil, fmt.Errorf("upcastProjectCreatedV1ToV2: unexpected input type %T", previous)
	}
	// v1 events predate OwnerID entirely; upcasting them exposes an empty
	// owner rather than inventing one.
	return projectCreatedV2{ProjectID: v1.ProjectID, Name: v1.Name, OwnerID: ""}, nil
}

func newExampleRegistry() *eventschema.Registry {
	r := eventschema.NewRegistry()
	r.Register("ProjectCreated", 1, decodeProjectCreatedV1)
	r.Register("ProjectCreated", 2, decodeProjectCreatedV2)
	r.RegisterUpcaster("ProjectCreated", 1, upcastProjectCreatedV1ToV2)
	return r
}

// goldenFixtures maps each golden fixture file under testdata/golden to
// the (eventType, schemaVersion) it exercises and the exact decoded value
// it must produce.
var goldenFixtures = []struct {
	file          string
	eventType     string
	schemaVersion int
	want          any
}{
	{"project_created_v1.json", "ProjectCreated", 1, projectCreatedV1{ProjectID: "proj-1", Name: "demo"}},
	{"project_created_v2.json", "ProjectCreated", 2, projectCreatedV2{ProjectID: "proj-1", Name: "demo", OwnerID: "user-1"}},
}

// TestGoldenFixtures_DecodeEveryRegisteredVersion is V1-07A's own
// "decode golden fixture của mọi version đã đăng ký" Verify requirement,
// and — because it fails loudly the moment a fixture's decoder is
// missing or broken — also its "test fail khi decoder đang được fixture
// tham chiếu bị xóa" requirement: delete decodeProjectCreatedV1 (or its
// Register call) while project_created_v1.json still exists and this
// test fails, catching exactly the kind of change that would otherwise
// only surface much later during a V6 projection rebuild.
func TestGoldenFixtures_DecodeEveryRegisteredVersion(t *testing.T) {
	registry := newExampleRegistry()
	for _, fixture := range goldenFixtures {
		t.Run(fixture.file, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join("testdata", "golden", fixture.file))
			if err != nil {
				t.Fatalf("read golden fixture: %v", err)
			}
			got, err := registry.Decode(fixture.eventType, fixture.schemaVersion, string(payload))
			if err != nil {
				t.Fatalf("Decode(%s v%d): %v", fixture.eventType, fixture.schemaVersion, err)
			}
			if got != fixture.want {
				t.Fatalf("Decode(%s v%d) = %+v, want %+v", fixture.eventType, fixture.schemaVersion, got, fixture.want)
			}
		})
	}
}

// TestGoldenFixtures_EveryTestdataFileIsCovered ensures goldenFixtures
// accounts for every file actually on disk — a fixture added without a
// matching table entry would otherwise silently never be decoded or
// verified by anything.
func TestGoldenFixtures_EveryTestdataFileIsCovered(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "golden"))
	if err != nil {
		t.Fatalf("read testdata/golden: %v", err)
	}
	covered := map[string]bool{}
	for _, fixture := range goldenFixtures {
		covered[fixture.file] = true
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !covered[entry.Name()] {
			t.Fatalf("testdata/golden/%s has no entry in goldenFixtures — it is never decoded or verified", entry.Name())
		}
	}
}

// TestDecodeLatest_UpcastsHistoricalVersionToCurrentShape proves a v1
// event (the actual golden fixture, unmodified) can still be replayed
// through to today's v2 shape — the exact guarantee V6's projection
// rebuild needs "replay được mọi event version lịch sử, không riêng
// version mới nhất."
func TestDecodeLatest_UpcastsHistoricalVersionToCurrentShape(t *testing.T) {
	registry := newExampleRegistry()
	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "project_created_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.DecodeLatest("ProjectCreated", 1, string(payload), 2)
	if err != nil {
		t.Fatalf("DecodeLatest: %v", err)
	}
	want := projectCreatedV2{ProjectID: "proj-1", Name: "demo", OwnerID: ""}
	if got != want {
		t.Fatalf("DecodeLatest upcast result = %+v, want %+v", got, want)
	}
}
