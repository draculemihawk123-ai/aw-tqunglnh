package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	clicatalog "github.com/taQuangLing/agent-workflow/internal/delivery/cli/catalog"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func TestRunProjectCreate_GeneratesIdempotencyKeyAndReturnsActiveProject(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{"name":"widget"}`)

	if err := clicatalog.RunProjectCreate(context.Background(), deps, nil, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("RunProjectCreate() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		IdempotencyKey string                         `json:"idempotencyKey"`
		Replayed       bool                           `json:"replayed"`
		Result         appcatalog.CreateProjectResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.IdempotencyKey == "" {
		t.Fatal("no --idempotency-key given, so RunProjectCreate must have generated and returned one")
	}
	if envelope.Replayed {
		t.Fatal("first run reported Replayed = true, want false")
	}
	if envelope.Result.ProjectID == "" {
		t.Fatal("result.ProjectID is empty, want a freshly minted id")
	}
	if envelope.Result.Name != "widget" || envelope.Result.Status != string(project.ProjectActive) {
		t.Fatalf("result = %+v, want Name=widget Status=ACTIVE", envelope.Result)
	}
}

// TestRunProjectCreate_ReplaySameIdempotencyKey_NeverCreatesASecondProject
// is this task's own "Replay" Verify bullet applied to `aw project create`:
// the exact same --idempotency-key resubmitted must return the identical
// stored result rather than minting a second Project.
func TestRunProjectCreate_ReplaySameIdempotencyKey_NeverCreatesASecondProject(t *testing.T) {
	deps := newTestDeps(t)

	var first bytes.Buffer
	if err := clicatalog.RunProjectCreate(context.Background(), deps, []string{"--idempotency-key", "key-1"}, strings.NewReader(`{"name":"widget"}`), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunProjectCreate() error = %v", err)
	}
	firstID := jsonField(t, resultField(t, first.String()), "projectId")

	var second bytes.Buffer
	if err := clicatalog.RunProjectCreate(context.Background(), deps, []string{"--idempotency-key", "key-1"}, strings.NewReader(`{"name":"widget"}`), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second RunProjectCreate() error = %v", err)
	}
	var secondEnvelope struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second stdout %s: %v", second.String(), err)
	}
	if !secondEnvelope.Replayed {
		t.Fatal("second RunProjectCreate() with the identical idempotency key reported Replayed = false, want true")
	}
	secondID := jsonField(t, resultField(t, second.String()), "projectId")
	if secondID != firstID {
		t.Fatalf("replay projectId = %q, want the exact original %q", secondID, firstID)
	}

	projects, err := appcatalog.ListProjects(context.Background(), deps.UoW, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("ListProjects returned %d projects, want exactly 1 (replay must never create a second row)", len(projects))
	}
}

func TestRunProjectList_ReturnsEveryCreatedProject(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateProject(t, deps, "key-a", "alpha")
	mustCreateProject(t, deps, "key-b", "beta")

	var stdout bytes.Buffer
	if err := clicatalog.RunProjectList(context.Background(), deps, nil, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProjectList() error = %v", err)
	}
	var body struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if len(body.Projects) != 2 {
		t.Fatalf("projects = %+v, want exactly 2", body.Projects)
	}
}

func TestRunProjectShow_UnknownID_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clicatalog.RunProjectShow(context.Background(), deps, []string{"does-not-exist"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunProjectShow() for an unknown id returned nil error, want a not-found error")
	}
}

func TestRunProjectShow_KnownID_ReturnsProjectView(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-a", "widget")

	var stdout bytes.Buffer
	if err := clicatalog.RunProjectShow(context.Background(), deps, []string{projectID}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunProjectShow() error = %v", err)
	}
	var view struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if view.ID != projectID || view.Name != "widget" {
		t.Fatalf("view = %+v, want ID=%s Name=widget", view, projectID)
	}
}

func TestRunProjectShow_WrongArgCount_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clicatalog.RunProjectShow(context.Background(), deps, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunProjectShow() with no positional argument returned nil error")
	}
	if !isUsageError(err) {
		t.Fatalf("RunProjectShow() with no positional argument returned %v, want a cli.UsageError", err)
	}
}
