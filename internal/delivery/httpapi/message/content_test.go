package message_test

// Real HTTP round-trip coverage for getMessageContent (V7-15's own real,
// previously-missing route — see content.go's own doc comment for why no
// route anywhere in this codebase could otherwise ever read a Message's
// own actual text).

import (
	"io"
	"net/http"
	"testing"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
)

func TestGetMessageContent_ReturnsRealStoredBytes(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	appendResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("ASSISTANT", "real assistant reply text", "text/plain", "", ""))
	if appendResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(appendResp.Body)
		t.Fatalf("append status = %d, want 201, body=%s", appendResp.StatusCode, body)
	}
	var result appmessage.AppendMessageResult
	decodeInto(t, appendResp, &result)

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/messages/"+result.MessageID+"/content", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain" {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
	if resp.Header.Get("Content-Disposition") == "" {
		t.Fatalf("Content-Disposition header is missing")
	}
	if resp.Header.Get("ETag") == "" {
		t.Fatalf("ETag header is missing")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "real assistant reply text" {
		t.Fatalf("body = %q, want %q", body, "real assistant reply text")
	}
}

// TestGetMessageContent_ForeignWorkItem_HidesAsResourceNotFound is this
// route's own "authorized against a Message's own real ContentArtifactID,
// never merely a real Artifact ID this caller could otherwise guess" case:
// a genuinely real messageId, requested through a DIFFERENT WorkItem's own
// path, must be leakage-normalized identically to a nonexistent one — the
// same ports.ErrScopeMismatch->WriteResourceHidden discipline
// getMessageContextSnapshot already proves for its own sibling route.
func TestGetMessageContent_ForeignWorkItem_HidesAsResourceNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	rootA := env.seedRootWorkItem(t, "project-1", "repo-a", "a")
	rootB := env.seedRootWorkItem(t, "project-1", "repo-a", "b")

	appendResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+rootA.WorkItemID+"/messages", "idem-msg-a",
		appendBody("ASSISTANT", "belongs to work item a", "text/plain", "", ""))
	if appendResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(appendResp.Body)
		t.Fatalf("append status = %d, want 201, body=%s", appendResp.StatusCode, body)
	}
	var result appmessage.AppendMessageResult
	decodeInto(t, appendResp, &result)

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+rootB.WorkItemID+"/messages/"+result.MessageID+"/content", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404 (leakage-normalized), body=%s", resp.StatusCode, body)
	}
}

func TestGetMessageContent_UnknownMessageID_Returns404(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/messages/does-not-exist/content", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, body)
	}
}
