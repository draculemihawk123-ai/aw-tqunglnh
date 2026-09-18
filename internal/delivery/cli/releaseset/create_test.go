package releaseset_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliReleaseSet "github.com/taQuangLing/agent-workflow/internal/delivery/cli/releaseset"
)

func decodeReleaseSetResult(t *testing.T, stdout *bytes.Buffer) workapp.ReleaseSetResult {
	t.Helper()
	var result workapp.ReleaseSetResult
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &result); err != nil {
		t.Fatalf("decode ReleaseSetResult: %v", err)
	}
	return result
}

func TestRunCreate_CreatesReleaseSet(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	body := `{"repositories":[{"repositoryId":"repo-1","baseVcsObjectId":"base-1","resultVcsObjectId":"result-1","verdict":"PASS"}]}`
	args := []string{"--project-id", "project-1", "--family-id", root.FamilyID, "--idempotency-key", "key-create-1"}
	if err := cliReleaseSet.RunCreate(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr); err != nil {
		t.Fatalf("RunCreate() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeReleaseSetResult(t, &stdout)
	if result.ProjectID != "project-1" || result.FamilyID != root.FamilyID || result.State != "CREATED" {
		t.Fatalf("result = %+v, want ProjectID=project-1 FamilyID=%s State=CREATED", result, root.FamilyID)
	}
	if result.ReleaseSetID == "" || result.ContentHash == "" || result.Version != 1 {
		t.Fatalf("result = %+v, want non-empty ReleaseSetID/ContentHash and Version=1", result)
	}
}

// TestRunCreate_ReplaySameIdempotencyKey_ReturnsIdenticalResult is this
// task's own "replay" Verify bullet applied to create: a second call with
// the identical --idempotency-key must replay the first call's exact
// result, never create a second ReleaseSet.
func TestRunCreate_ReplaySameIdempotencyKey_ReturnsIdenticalResult(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"repositories":[{"repositoryId":"repo-1","baseVcsObjectId":"base-1","resultVcsObjectId":"result-1","verdict":"PASS"}]}`
	args := []string{"--project-id", "project-1", "--family-id", root.FamilyID, "--idempotency-key", "key-create-replay"}

	var first bytes.Buffer
	if err := cliReleaseSet.RunCreate(context.Background(), deps, args, strings.NewReader(body), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunCreate() error = %v", err)
	}
	firstResult := decodeReleaseSetResult(t, &first)
	if replayedField(t, first.String()) {
		t.Fatal("first call reported Replayed = true, want false")
	}

	var second bytes.Buffer
	if err := cliReleaseSet.RunCreate(context.Background(), deps, args, strings.NewReader(body), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second (replay) RunCreate() error = %v", err)
	}
	secondResult := decodeReleaseSetResult(t, &second)
	if !replayedField(t, second.String()) {
		t.Fatal("second call reported Replayed = false, want true")
	}
	if secondResult.ReleaseSetID != firstResult.ReleaseSetID {
		t.Fatalf("replay result = %+v, want the exact original %+v", secondResult, firstResult)
	}

	list, err := workapp.ListReleaseSetsForFamily(context.Background(), u, root.FamilyID)
	if err != nil {
		t.Fatalf("ListReleaseSetsForFamily: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("release sets for family after replay = %d, want exactly 1 (the replay must never create a second one)", len(list))
	}
}

// TestRunCreate_DuplicateRepositoryEntry_IsUsageError is this task's own
// "partial" Verify bullet: an entry list with a duplicate repositoryId is
// rejected before any partial ReleaseSet is created.
func TestRunCreate_DuplicateRepositoryEntry_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"repositories":[
		{"repositoryId":"repo-1","baseVcsObjectId":"base-1","resultVcsObjectId":"result-1","verdict":"PASS"},
		{"repositoryId":"repo-1","baseVcsObjectId":"base-2","resultVcsObjectId":"result-2","verdict":"PASS"}
	]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1", "--family-id", root.FamilyID}
	err := cliReleaseSet.RunCreate(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunCreate() with a duplicate repositoryId returned %v, want a cli.UsageError", err)
	}

	list, listErr := workapp.ListReleaseSetsForFamily(context.Background(), u, root.FamilyID)
	if listErr != nil {
		t.Fatalf("ListReleaseSetsForFamily: %v", listErr)
	}
	if len(list) != 0 {
		t.Fatalf("release sets for family after a rejected create = %d, want 0 (no partial ReleaseSet)", len(list))
	}
}

// TestRunCreate_InvalidVerdict_IsUsageError is the other half of the
// "partial" Verify bullet: an invalid verdict is rejected the same way.
func TestRunCreate_InvalidVerdict_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"repositories":[{"repositoryId":"repo-1","baseVcsObjectId":"base-1","resultVcsObjectId":"result-1","verdict":"MAYBE"}]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1", "--family-id", root.FamilyID}
	err := cliReleaseSet.RunCreate(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunCreate() with verdict=MAYBE returned %v, want a cli.UsageError", err)
	}
}

func TestRunCreate_EmptyRepositories_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"repositories":[]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1", "--family-id", root.FamilyID}
	err := cliReleaseSet.RunCreate(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunCreate() with no repositories returned %v, want a cli.UsageError", err)
	}
}

func TestRunCreate_MissingFamilyID_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	body := `{"repositories":[{"repositoryId":"repo-1","baseVcsObjectId":"base-1","resultVcsObjectId":"result-1","verdict":"PASS"}]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1"}
	err := cliReleaseSet.RunCreate(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunCreate() with no --family-id returned %v, want a cli.UsageError", err)
	}
}

func TestRunCreate_MissingProjectID_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	body := `{"repositories":[{"repositoryId":"repo-1","baseVcsObjectId":"base-1","resultVcsObjectId":"result-1","verdict":"PASS"}]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--family-id", "family-1"}
	err := cliReleaseSet.RunCreate(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunCreate() with no --project-id returned %v, want a cli.UsageError", err)
	}
}
