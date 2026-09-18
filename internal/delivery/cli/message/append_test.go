package message_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	climessage "github.com/taQuangLing/agent-workflow/internal/delivery/cli/message"
)

func TestRunMessageAppend_GeneratesIdempotencyKeyAndAppends(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "USER", workItemID}

	if err := climessage.RunMessageAppend(context.Background(), deps, args, strings.NewReader("hello there"), &stdout, &stderr); err != nil {
		t.Fatalf("RunMessageAppend() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		IdempotencyKey string                         `json:"idempotencyKey"`
		Replayed       bool                           `json:"replayed"`
		Result         appmessage.AppendMessageResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.IdempotencyKey == "" {
		t.Fatal("no --idempotency-key given, so RunMessageAppend must have generated and returned one")
	}
	if envelope.Replayed {
		t.Fatal("first run reported Replayed = true, want false")
	}
	if envelope.Result.MessageID == "" || envelope.Result.ContentArtifactID == "" {
		t.Fatalf("result missing IDs: %+v", envelope.Result)
	}
	if envelope.Result.ProjectID != projectID || envelope.Result.WorkItemID != workItemID {
		t.Fatalf("result scope = (%s, %s), want (%s, %s)", envelope.Result.ProjectID, envelope.Result.WorkItemID, projectID, workItemID)
	}
}

// TestRunMessageAppend_Replay_NeverAppendsTwice is the "Replay" Verify
// bullet: the exact same --idempotency-key resubmitted must return the
// identical stored result and report Replayed=true, never append a second
// Message.
func TestRunMessageAppend_Replay_NeverAppendsTwice(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	args := []string{"--project-id", projectID, "--role", "USER", "--idempotency-key", "key-1", workItemID}

	var first bytes.Buffer
	if err := climessage.RunMessageAppend(context.Background(), deps, args, strings.NewReader("hello"), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunMessageAppend() error = %v", err)
	}
	var second bytes.Buffer
	if err := climessage.RunMessageAppend(context.Background(), deps, args, strings.NewReader("hello"), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second RunMessageAppend() error = %v", err)
	}

	var firstEnvelope, secondEnvelope struct {
		Replayed bool                           `json:"replayed"`
		Result   appmessage.AppendMessageResult `json:"result"`
	}
	if err := json.Unmarshal(first.Bytes(), &firstEnvelope); err != nil {
		t.Fatalf("decode first stdout: %v", err)
	}
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second stdout: %v", err)
	}
	if secondEnvelope.Replayed != true {
		t.Fatal("second RunMessageAppend() with the identical idempotency key reported Replayed = false, want true")
	}
	if secondEnvelope.Result.MessageID != firstEnvelope.Result.MessageID {
		t.Fatalf("replay MessageID = %s, want the exact original %s", secondEnvelope.Result.MessageID, firstEnvelope.Result.MessageID)
	}
	if secondEnvelope.Result.ContentArtifactID != firstEnvelope.Result.ContentArtifactID {
		t.Fatalf("replay ContentArtifactID = %s, want the exact original %s", secondEnvelope.Result.ContentArtifactID, firstEnvelope.Result.ContentArtifactID)
	}

	msgs, err := appmessage.ListMessages(context.Background(), deps.UnitOfWork, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want exactly 1 (no duplicate append from replay)", len(msgs))
	}
}

// TestRunMessageAppend_ExceedsMaxContentSize_IsRejectedCleanly is the
// "Size" Verify bullet: an input exceeding appmessage.MaxContentSize must be
// rejected outright by the leaf's own bounded read, never silently
// truncated and never allowed to reach appmessage.AppendMessage's own
// storage layer at all.
func TestRunMessageAppend_ExceedsMaxContentSize_IsRejectedCleanly(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	oversized := bytes.Repeat([]byte("a"), appmessage.MaxContentSize+1)

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "USER", workItemID}
	err := climessage.RunMessageAppend(context.Background(), deps, args, bytes.NewReader(oversized), &stdout, &stderr)
	if err == nil {
		t.Fatal("RunMessageAppend(oversized content) error = nil, want a usage error")
	}
	if !isUsageError(err) {
		t.Fatalf("RunMessageAppend(oversized content) error = %v, want a cli.UsageError wrapping cli.ErrInputTooLarge", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout got written to on a rejected oversized input: %q", stdout.String())
	}

	msgs, err := appmessage.ListMessages(context.Background(), deps.UnitOfWork, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("len(msgs) = %d, want 0 (rejected input must never be silently truncated and stored)", len(msgs))
	}
}

// TestRunMessageAppend_DefaultRoleIsUser proves --role's own documented
// default (USER) when omitted, matching this task's own "Command surface"
// line bracketing --role as optional for `aw message append` (unlike
// upload-attachment's own required --role).
func TestRunMessageAppend_DefaultRoleIsUser(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, workItemID}
	if err := climessage.RunMessageAppend(context.Background(), deps, args, strings.NewReader("hi"), &stdout, &stderr); err != nil {
		t.Fatalf("RunMessageAppend() error = %v, stderr = %s", err, stderr.String())
	}

	msgs, err := appmessage.ListMessages(context.Background(), deps.UnitOfWork, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 || string(msgs[0].Role) != "USER" {
		t.Fatalf("msgs = %+v, want exactly one USER-role message", msgs)
	}
}

func TestRunMessageAppend_InvalidRole_IsUsageError(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "NOT_A_ROLE", workItemID}
	err := climessage.RunMessageAppend(context.Background(), deps, args, strings.NewReader("hi"), &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunMessageAppend(bad --role) error = %v, want a cli.UsageError", err)
	}
}

func TestRunMessageAppend_EmptyContent_IsUsageError(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "USER", workItemID}
	err := climessage.RunMessageAppend(context.Background(), deps, args, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunMessageAppend(empty content) error = %v, want a cli.UsageError", err)
	}
}

// TestRunMessageAppend_StdoutStderrSeparation is the "Stdout/stderr
// separation" Verify bullet: the command's own RESULT (JSON) goes to
// stdout, and ONLY stdout — every diagnostic/progress line goes to stderr,
// never mixed into the same stream a JSON-consuming caller parses.
func TestRunMessageAppend_StdoutStderrSeparation(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", projectID, "--role", "USER", workItemID}
	if err := climessage.RunMessageAppend(context.Background(), deps, args, strings.NewReader("separated"), &stdout, &stderr); err != nil {
		t.Fatalf("RunMessageAppend() error = %v", err)
	}

	// stdout must be exactly one JSON document.
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%q)", err, stdout.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var first json.RawMessage
	if err := decoder.Decode(&first); err != nil {
		t.Fatalf("decode the one JSON document stdout must carry: %v", err)
	}
	var second json.RawMessage
	if err := decoder.Decode(&second); err == nil {
		t.Fatalf("stdout carried more than one JSON document: extra=%s", second)
	}

	// stderr must carry the diagnostic line, and stdout must never contain
	// any of that diagnostic text.
	if stderr.Len() == 0 {
		t.Fatal("stderr got nothing written to it — expected a diagnostic/progress line")
	}
	if !strings.Contains(stderr.String(), workItemID) {
		t.Fatalf("stderr = %q, want it to mention the work item ID", stderr.String())
	}
	if strings.Contains(stdout.String(), "message append:") {
		t.Fatalf("stdout contains diagnostic text that belongs only on stderr: %q", stdout.String())
	}
}
