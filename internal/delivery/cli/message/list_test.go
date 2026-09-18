package message_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	climessage "github.com/taQuangLing/agent-workflow/internal/delivery/cli/message"
)

// mustAppendMessage runs `aw message append` for real (never a direct
// Messages() insert) — shared setup for list/context-metadata tests.
func mustAppendMessage(t *testing.T, deps climessage.Dependencies, projectID, workItemID, idempotencyKey, role, content string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--idempotency-key", idempotencyKey, "--role", role, workItemID}
	if err := climessage.RunMessageAppend(context.Background(), deps, args, strings.NewReader(content), &stdout, &stderr); err != nil {
		t.Fatalf("RunMessageAppend(%s) error = %v, stderr = %s", idempotencyKey, err, stderr.String())
	}
	return resultField(t, stdout.String())
}

func TestRunMessageList_SurfacesContextMetadataInSequenceOrder(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)

	mustAppendMessage(t, deps, projectID, workItemID, "idem-1", "USER", "first message")
	mustAppendMessage(t, deps, projectID, workItemID, "idem-2", "ASSISTANT", "second message")

	var stdout, stderr bytes.Buffer
	if err := climessage.RunMessageList(context.Background(), deps, []string{"--project-id", projectID, workItemID}, &stdout, &stderr); err != nil {
		t.Fatalf("RunMessageList() error = %v, stderr = %s", err, stderr.String())
	}

	var response struct {
		Items []struct {
			MessageID         string `json:"messageId"`
			ProjectID         string `json:"projectId"`
			WorkItemID        string `json:"workItemId"`
			Sequence          uint64 `json:"sequence"`
			Actor             string `json:"actor"`
			Role              string `json:"role"`
			ContentArtifactID string `json:"contentArtifactId"`
			CreatedAt         string `json:"createdAt"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if len(response.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(response.Items))
	}
	first, second := response.Items[0], response.Items[1]
	if first.Sequence >= second.Sequence {
		t.Fatalf("items not in Sequence-ascending order: %d then %d", first.Sequence, second.Sequence)
	}
	if first.Role != "USER" || second.Role != "ASSISTANT" {
		t.Fatalf("roles = %q, %q, want USER, ASSISTANT", first.Role, second.Role)
	}
	if first.ProjectID != projectID || first.WorkItemID != workItemID {
		t.Fatalf("first item scope = (%s, %s), want (%s, %s)", first.ProjectID, first.WorkItemID, projectID, workItemID)
	}
	if first.MessageID == "" || first.ContentArtifactID == "" || first.CreatedAt == "" || first.Actor == "" {
		t.Fatalf("item missing context metadata: %+v", first)
	}

	// "No locator output": ContentArtifactID must be a bounded ID, never a
	// raw filesystem/storage locator (this package never touches
	// ports.ArtifactStore/ArtifactRef.Locator at all).
	if strings.Contains(first.ContentArtifactID, "/") || strings.Contains(first.ContentArtifactID, "\\") {
		t.Fatalf("ContentArtifactID looks like a raw storage locator, not a bounded ID: %q", first.ContentArtifactID)
	}
}

func TestRunMessageList_UnknownWorkItem_ReturnsErrWorkItemNotFound(t *testing.T) {
	deps, projectID, _ := setupFixture(t)
	var stdout, stderr bytes.Buffer
	err := climessage.RunMessageList(context.Background(), deps, []string{"--project-id", projectID, "does-not-exist"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunMessageList(unknown workItemId) error = nil, want ErrWorkItemNotFound")
	}
}

func TestRunMessageList_WrongProject_ReturnsErrWorkItemNotFound(t *testing.T) {
	deps, _, workItemID := setupFixture(t)
	var stdout, stderr bytes.Buffer
	err := climessage.RunMessageList(context.Background(), deps, []string{"--project-id", "some-other-project", workItemID}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunMessageList(wrong project) error = nil, want ErrWorkItemNotFound (leakage-normalized)")
	}
	if err != climessage.ErrWorkItemNotFound {
		t.Fatalf("RunMessageList(wrong project) error = %v, want ErrWorkItemNotFound exactly (leakage-normalized, not a distinguishable scope-mismatch error)", err)
	}
}

func TestRunMessageList_MissingProjectID_IsUsageError(t *testing.T) {
	deps, _, workItemID := setupFixture(t)
	var stdout, stderr bytes.Buffer
	err := climessage.RunMessageList(context.Background(), deps, []string{workItemID}, &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunMessageList(no --project-id) error = %v, want a cli.UsageError", err)
	}
}

func TestRunMessageList_NoWorkItemArgument_IsUsageError(t *testing.T) {
	deps, projectID, _ := setupFixture(t)
	var stdout, stderr bytes.Buffer
	err := climessage.RunMessageList(context.Background(), deps, []string{"--project-id", projectID}, &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunMessageList(no <workItemId>) error = %v, want a cli.UsageError", err)
	}
}
