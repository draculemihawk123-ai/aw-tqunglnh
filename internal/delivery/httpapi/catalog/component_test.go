package catalog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// seedTestComponent inserts a Component directly via the real
// internal/app/catalog.CreateComponent application command — standing in
// for what V3-02's own onboarding-probe worker would otherwise do (see
// internal/app/repositoryprobe's finishActive, which calls this exact
// same function when a real probe discovers topology). This package's own
// HTTP routes never call CreateComponent themselves
// (internal/archtest's own TestHTTPAPICatalogNeverCallsCreateComponent
// proves it) — a real Component has to come from somewhere for
// listProjectComponents/listComponentPackAssignments/assignComponentPack
// to have anything to read, and this is the one real production path that
// creates one.
func seedTestComponent(t *testing.T, uow ports.UnitOfWork, projectID, repositoryID, componentID string) {
	t.Helper()
	_, err := appcatalog.CreateComponent(context.Background(), uow, idsource.Random{}, appcatalog.CreateComponentRequest{
		ProjectID: projectID, RepositoryID: repositoryID, Name: componentID, Path: "cmd/" + componentID, Kind: "service",
	})
	if err != nil {
		t.Fatalf("seedTestComponent CreateComponent: %v", err)
	}
	// CreateComponent mints its own ComponentID via idsource rather than
	// taking a caller-chosen one (this package's own doc comment on
	// CreateComponent) — the caller-supplied componentID parameter here is
	// only ever used as this fixture's own Name/Path seed, matched back up
	// in each test via listProjectComponents' own returned Name field, not
	// as the real persisted ComponentID. Tests needing the real minted ID
	// look it up via listProjectComponents instead of assuming one.
}

func TestListProjectComponents_ReturnsDiscoveredComponents(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	seedTestComponent(t, uow, projectID, "repo-1", "api")
	seedTestComponent(t, uow, projectID, "repo-1", "worker")

	rec := doRequest(handler, http.MethodGet, "/projects/"+projectID+"/components", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s, want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Components []struct {
			ID           string `json:"id"`
			RepositoryID string `json:"repositoryId"`
			Name         string `json:"name"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if len(body.Components) != 2 {
		t.Fatalf("components = %+v, want exactly 2", body.Components)
	}
	names := map[string]bool{}
	for _, c := range body.Components {
		if c.RepositoryID != "repo-1" {
			t.Fatalf("component %+v, want RepositoryID=repo-1", c)
		}
		names[c.Name] = true
	}
	if !names["api"] || !names["worker"] {
		t.Fatalf("components = %+v, want both api and worker", body.Components)
	}
}

func TestListProjectComponents_UnknownProject_Returns404(t *testing.T) {
	handler, _ := newTestHandler(t)
	rec := doRequest(handler, http.MethodGet, "/projects/does-not-exist/components", "", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body %s, want 404", rec.Code, rec.Body.String())
	}
}

func TestListComponentPackAssignments_UnknownComponent_Returns404(t *testing.T) {
	handler, _ := newTestHandler(t)
	rec := doRequest(handler, http.MethodGet, "/components/does-not-exist/pack-assignments", "", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body %s, want 404", rec.Code, rec.Body.String())
	}
}

// getSeededComponentID looks up the one real minted ComponentID
// seedTestComponent's own CreateComponent call produced, by listing the
// project's components and matching on Name (seedTestComponent's own
// Name==componentID convention) — every assignment test needs the real
// ID, never a guessed one, since assignComponentPack/pack-assignments
// routes address a Component by that real ID alone.
func getSeededComponentID(t *testing.T, handler http.Handler, projectID, name string) string {
	t.Helper()
	rec := doRequest(handler, http.MethodGet, "/projects/"+projectID+"/components", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /projects/%s/components = %d, body %s", projectID, rec.Code, rec.Body.String())
	}
	var body struct {
		Components []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	for _, c := range body.Components {
		if c.Name == name {
			return c.ID
		}
	}
	t.Fatalf("no seeded component named %q found in %+v", name, body.Components)
	return ""
}

func TestListComponentPackAssignments_NoAssignmentYet_EffectiveIsNil(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	seedTestComponent(t, uow, projectID, "repo-1", "api")
	componentID := getSeededComponentID(t, handler, projectID, "api")

	rec := doRequest(handler, http.MethodGet, "/components/"+componentID+"/pack-assignments", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s, want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		ComponentID string        `json:"componentId"`
		Assignments []interface{} `json:"assignments"`
		Effective   interface{}   `json:"effective"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if body.ComponentID != componentID {
		t.Fatalf("componentId = %q, want %q", body.ComponentID, componentID)
	}
	if len(body.Assignments) != 0 {
		t.Fatalf("assignments = %+v, want empty", body.Assignments)
	}
	if body.Effective != nil {
		t.Fatalf("effective = %+v, want nil (never assigned yet)", body.Effective)
	}
}

// TestAssignComponentPack_CreatesAssignmentAndBecomesEffective is V6-03A's
// own "assignment pin exact version" line proven end to end: the assigned
// PackVersionID comes back byte-for-byte in both the POST response and
// the follow-up GET's own "effective" field.
func TestAssignComponentPack_CreatesAssignmentAndBecomesEffective(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	seedTestComponent(t, uow, projectID, "repo-1", "api")
	componentID := getSeededComponentID(t, handler, projectID, "api")

	body := `{"packVersionId":"pack-v1"}`
	rec := doRequest(handler, http.MethodPost, "/components/"+componentID+"/pack-assignments", "key-assign", "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s, want 201", rec.Code, rec.Body.String())
	}
	var result appcatalog.AssignComponentPackResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if result.ComponentID != componentID || result.PackVersionID != "pack-v1" {
		t.Fatalf("result = %+v, want ComponentID=%s PackVersionID=pack-v1", result, componentID)
	}

	listRec := doRequest(handler, http.MethodGet, "/components/"+componentID+"/pack-assignments", "", "", "")
	var listBody struct {
		Assignments []struct {
			PackVersionID string `json:"packVersionId"`
		} `json:"assignments"`
		Effective struct {
			PackVersionID string `json:"packVersionId"`
		} `json:"effective"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list body %s: %v", listRec.Body.String(), err)
	}
	if len(listBody.Assignments) != 1 || listBody.Assignments[0].PackVersionID != "pack-v1" {
		t.Fatalf("assignments = %+v, want exactly 1 with PackVersionID=pack-v1", listBody.Assignments)
	}
	if listBody.Effective.PackVersionID != "pack-v1" {
		t.Fatalf("effective = %+v, want PackVersionID=pack-v1 (the only assignment, immediately effective)", listBody.Effective)
	}
}

// TestAssignComponentPack_FutureEffectiveAt_NotYetEffective proves
// EffectiveAt is honored exactly as supplied (never silently forced to
// "now"): an assignment pinned for tomorrow shows up in the full history
// but is NOT reported as the component's own "effective" pack today.
func TestAssignComponentPack_FutureEffectiveAt_NotYetEffective(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	seedTestComponent(t, uow, projectID, "repo-1", "api")
	componentID := getSeededComponentID(t, handler, projectID, "api")

	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339Nano)
	body := `{"packVersionId":"pack-future","effectiveAt":"` + future + `"}`
	rec := doRequest(handler, http.MethodPost, "/components/"+componentID+"/pack-assignments", "key-assign", "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s, want 201", rec.Code, rec.Body.String())
	}

	listRec := doRequest(handler, http.MethodGet, "/components/"+componentID+"/pack-assignments", "", "", "")
	var listBody struct {
		Assignments []struct {
			PackVersionID string `json:"packVersionId"`
		} `json:"assignments"`
		Effective *struct {
			PackVersionID string `json:"packVersionId"`
		} `json:"effective"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list body %s: %v", listRec.Body.String(), err)
	}
	if len(listBody.Assignments) != 1 || listBody.Assignments[0].PackVersionID != "pack-future" {
		t.Fatalf("assignments = %+v, want exactly 1 with PackVersionID=pack-future (still in the history)", listBody.Assignments)
	}
	if listBody.Effective != nil {
		t.Fatalf("effective = %+v, want nil (pack-future is not effective until 48h from now)", listBody.Effective)
	}
}

func TestAssignComponentPack_MissingIdempotencyKey_Returns400(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	seedTestComponent(t, uow, projectID, "repo-1", "api")
	componentID := getSeededComponentID(t, handler, projectID, "api")

	rec := doRequest(handler, http.MethodPost, "/components/"+componentID+"/pack-assignments", "", "", `{"packVersionId":"pack-v1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %s, want 400", rec.Code, rec.Body.String())
	}
}
