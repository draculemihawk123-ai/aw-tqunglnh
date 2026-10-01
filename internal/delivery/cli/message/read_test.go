package message_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	climessage "github.com/taQuangLing/agent-workflow/internal/delivery/cli/message"
)

func messageIDFromAppendResult(t *testing.T, result string) string {
	t.Helper()
	var r struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal([]byte(result), &r); err != nil || r.MessageID == "" {
		t.Fatalf("append result %q has no messageId: %v", result, err)
	}
	return r.MessageID
}

func TestRunMessageContent_StreamsTheCanonicalBytesToStdoutAloneAndMetadataToStderr(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	const content = "the canonical text of the message"
	messageID := messageIDFromAppendResult(t, mustAppendMessage(t, deps, projectID, workItemID, "idem-content", "USER", content))

	var stdout, stderr bytes.Buffer
	err := climessage.RunMessageContent(context.Background(), deps, []string{"--project-id", projectID, "--output", "-", workItemID, messageID}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunMessageContent: %v, stderr=%s", err, stderr.String())
	}
	if stdout.String() != content {
		t.Fatalf("stdout = %q, want exactly the message content %q (no wrapper, no trailing summary)", stdout.String(), content)
	}
	for _, want := range []string{"messageId=" + messageID, "contentHash=", "size="} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q does not report %q", stderr.String(), want)
		}
	}
	if strings.Contains(strings.ToLower(stderr.String()), "locator") {
		t.Fatalf("stderr leaks a locator: %s", stderr.String())
	}
}

func TestRunMessageContent_ToAFileWritesTheBytesAndPrintsBoundedMetadata(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	const content = "message content written to a file"
	messageID := messageIDFromAppendResult(t, mustAppendMessage(t, deps, projectID, workItemID, "idem-content-file", "ASSISTANT", content))
	target := filepath.Join(t.TempDir(), "message.txt")

	var stdout, stderr bytes.Buffer
	err := climessage.RunMessageContent(context.Background(), deps, []string{"--project-id", projectID, "--output", target, workItemID, messageID}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunMessageContent: %v, stderr=%s", err, stderr.String())
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != content {
		t.Fatalf("file content = %q (err %v), want %q", got, err, content)
	}
	var summary struct {
		MessageID   string `json:"messageId"`
		ContentHash string `json:"contentHash"`
		Size        int64  `json:"size"`
		Sensitivity string `json:"sensitivity"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("decode stdout %q: %v", stdout.String(), err)
	}
	if summary.MessageID != messageID || summary.ContentHash == "" || summary.Size != int64(len(content)) || summary.Sensitivity != "PUBLIC" {
		t.Fatalf("summary = %+v", summary)
	}
	if strings.Contains(strings.ToLower(stdout.String()), "locator") {
		t.Fatalf("stdout leaks a locator: %s", stdout.String())
	}
}

func TestRunMessageContent_WrongWorkItemAndUnknownMessageAreRejected(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	messageID := messageIDFromAppendResult(t, mustAppendMessage(t, deps, projectID, workItemID, "idem-content-scope", "USER", "x"))

	var stdout, stderr bytes.Buffer
	if err := climessage.RunMessageContent(context.Background(), deps, []string{"--project-id", projectID, "--output", "-", workItemID, "no-such-message"}, &stdout, &stderr); err == nil {
		t.Fatal("an unknown message id must be an error")
	}
	if err := climessage.RunMessageContent(context.Background(), deps, []string{"--project-id", "other-project", "--output", "-", workItemID, messageID}, &stdout, &stderr); err == nil {
		t.Fatal("a message must not be readable through another project")
	}
	if stdout.Len() != 0 {
		t.Fatalf("a rejected read wrote %d byte(s) to stdout", stdout.Len())
	}
}

func TestRunMessageContent_UsageErrors(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	for name, args := range map[string][]string{
		"missing output":  {"--project-id", projectID, workItemID, "m1"},
		"missing message": {"--project-id", projectID, "--output", "-", workItemID},
		"no project id":   {"--output", "-", workItemID, "m1"},
	} {
		var stdout, stderr bytes.Buffer
		err := climessage.RunMessageContent(context.Background(), deps, args, &stdout, &stderr)
		if err == nil || !isUsageError(err) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}

func TestRunMessageContextSnapshot_AMessageWithNoAttemptIsNotFoundWithTheHTTPWording(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	messageID := messageIDFromAppendResult(t, mustAppendMessage(t, deps, projectID, workItemID, "idem-snap", "USER", "no attempt here"))

	var stdout, stderr bytes.Buffer
	err := climessage.RunMessageContextSnapshot(context.Background(), deps, []string{"--project-id", projectID, workItemID, messageID}, &stdout, &stderr)
	var appErr *apperror.Error
	if err == nil || !asApp(err, &appErr) || appErr.Code != apperror.CodeNotFound {
		t.Fatalf("err = %v, want a NOT_FOUND application error", err)
	}
	if appErr.Message != "message has no linked execution attempt" {
		t.Fatalf("message = %q, want the same wording the HTTP 404 uses", appErr.Message)
	}
	if stdout.Len() != 0 {
		t.Fatalf("a not-found outcome wrote to stdout: %q", stdout.String())
	}
}

func TestRunMessageContextSnapshot_UnknownMessageAndUsageErrors(t *testing.T) {
	deps, projectID, workItemID := setupFixture(t)
	var stdout, stderr bytes.Buffer
	if err := climessage.RunMessageContextSnapshot(context.Background(), deps, []string{"--project-id", projectID, workItemID, "no-such-message"}, &stdout, &stderr); err == nil {
		t.Fatal("an unknown message id must be an error")
	}
	for name, args := range map[string][]string{
		"missing message": {"--project-id", projectID, workItemID},
		"no project id":   {workItemID, "m1"},
	} {
		err := climessage.RunMessageContextSnapshot(context.Background(), deps, args, &stdout, &stderr)
		if err == nil || !isUsageError(err) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}

func asApp(err error, target **apperror.Error) bool {
	for err != nil {
		if e, ok := err.(*apperror.Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
