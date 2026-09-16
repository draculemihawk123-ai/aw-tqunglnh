package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	clicatalog "github.com/taQuangLing/agent-workflow/internal/delivery/cli/catalog"
)

// mustDiscoverComponent simulates what V3-02's own onboarding-probe worker
// does once a probe succeeds — creating a Component row via the real
// (non-CLI, non-HTTP) appcatalog.CreateComponent helper — since this leaf
// deliberately exposes no create/mutate command for a Component at all
// (V6-15D's own "Không làm: no generic probe/component creation" line;
// see component.go's own RunComponentList doc comment).
func mustDiscoverComponent(t *testing.T, deps clicatalog.Dependencies, projectID, repositoryID, name, path string) string {
	t.Helper()
	created, err := appcatalog.CreateComponent(context.Background(), deps.UoW, deps.IDs, appcatalog.CreateComponentRequest{
		ProjectID: projectID, RepositoryID: repositoryID, Name: name, Path: path, Kind: "service",
	})
	if err != nil {
		t.Fatalf("appcatalog.CreateComponent: %v", err)
	}
	return string(created.ID)
}

// TestRunComponentList_ReflectsComponentsDiscoveredByCompletedProbe is
// this task's own "component discovery" Verify bullet: `aw component list`
// must reflect components a completed probe discovered, proven here by
// simulating the probe's own discovery step directly (mustDiscoverComponent)
// rather than through this leaf (which exposes no such command), then
// reading it back through the real RunComponentList query.
func TestRunComponentList_ReflectsComponentsDiscoveredByCompletedProbe(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")
	completeRepositoryProbeActive(t, deps.UoW, projectID, "repo-1", "probe-job-1")

	var empty bytes.Buffer
	if err := clicatalog.RunComponentList(context.Background(), deps, []string{projectID}, &empty, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunComponentList() before discovery error = %v", err)
	}
	var emptyBody struct {
		Components []interface{} `json:"components"`
	}
	if err := json.Unmarshal(empty.Bytes(), &emptyBody); err != nil {
		t.Fatalf("decode %s: %v", empty.String(), err)
	}
	if len(emptyBody.Components) != 0 {
		t.Fatalf("components before discovery = %+v, want empty", emptyBody.Components)
	}

	componentID := mustDiscoverComponent(t, deps, projectID, "repo-1", "api", "services/api")

	var stdout bytes.Buffer
	if err := clicatalog.RunComponentList(context.Background(), deps, []string{projectID}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunComponentList() after discovery error = %v", err)
	}
	var body struct {
		Components []struct {
			ID           string `json:"id"`
			RepositoryID string `json:"repositoryId"`
			Name         string `json:"name"`
			Path         string `json:"path"`
		} `json:"components"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if len(body.Components) != 1 {
		t.Fatalf("components = %+v, want exactly 1", body.Components)
	}
	got := body.Components[0]
	if got.ID != componentID || got.RepositoryID != "repo-1" || got.Name != "api" || got.Path != "services/api" {
		t.Fatalf("component = %+v, want ID=%s RepositoryID=repo-1 Name=api Path=services/api", got, componentID)
	}
}

func TestRunComponentList_UnknownProject_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clicatalog.RunComponentList(context.Background(), deps, []string{"does-not-exist"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunComponentList() for an unknown project returned nil error")
	}
}

func TestRunComponentList_WrongArgCount_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clicatalog.RunComponentList(context.Background(), deps, nil, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunComponentList() with no positional argument returned %v, want a cli.UsageError", err)
	}
}
