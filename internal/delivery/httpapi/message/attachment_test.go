package message_test

// Real HTTP round-trip coverage for V6-07A (docs/design/08-v6-api-projections.md):
// every test in this file drives the SAME real httpapi.Server/testEnv
// message_test.go's own append/list/context-snapshot tests already use,
// confirming the raw-body attachment route (attachment.go) is reachable
// end to end — real TCP loopback, real middleware chain, real sqlite, real
// filesystem ArtifactStore.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	message "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/message"
)

// doAttachment issues a real raw-body POST against e's own real server —
// message_test.go's own do() helper always JSON-encodes body, which this
// route's own request shape (attachment.go's own doc comment: raw bytes,
// metadata via headers) can never use.
func (e *testEnv) doAttachment(t *testing.T, path, idempotencyKey string, headers map[string]string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, testSessionToken)
	if idempotencyKey != "" {
		req.Header.Set(httpapi.IdempotencyKeyHeader, idempotencyKey)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func attachmentHeaders(contentType, role, sha256Hex string) map[string]string {
	h := map[string]string{"Content-Type": contentType, message.AttachmentRoleHeader: role, message.AttachmentSHA256Header: sha256Hex}
	return h
}

func TestHandleAppendConversationAttachment_HappyPath_Returns201WithMessageResult(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-1")
	root := e.seedRootWorkItem(t, "project-1", "repo-1", "att-1")

	content := []byte("a real png-ish payload for the http round trip")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	resp := e.doAttachment(t, "/projects/project-1/work-items/"+root.WorkItemID+"/attachments", "idem-http-att-1",
		attachmentHeaders("image/png", "USER", digest), content)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var result appmessage.AppendMessageResult
	decodeInto(t, resp, &result)
	if result.MessageID == "" || result.ContentArtifactID == "" {
		t.Fatalf("result missing IDs: %+v", result)
	}

	assertStoredContent(t, e, result.ContentArtifactID, string(content))
}

func TestHandleAppendConversationAttachment_Replay_SameIdempotencyKey_Returns200Identical(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-1")
	root := e.seedRootWorkItem(t, "project-1", "repo-1", "att-replay")

	content := []byte("replay this exact attachment")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	path := "/projects/project-1/work-items/" + root.WorkItemID + "/attachments"
	headers := attachmentHeaders("text/plain", "ASSISTANT", digest)

	first := e.doAttachment(t, path, "idem-http-replay-1", headers, content)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", first.StatusCode)
	}
	var firstResult appmessage.AppendMessageResult
	decodeInto(t, first, &firstResult)

	second := e.doAttachment(t, path, "idem-http-replay-1", headers, content)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", second.StatusCode)
	}
	var secondResult appmessage.AppendMessageResult
	decodeInto(t, second, &secondResult)
	if secondResult != firstResult {
		t.Fatalf("replay result %+v != first result %+v", secondResult, firstResult)
	}
}

func TestHandleAppendConversationAttachment_MissingIdempotencyKey_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-1")
	root := e.seedRootWorkItem(t, "project-1", "repo-1", "att-noidem")

	content := []byte("no idempotency key")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	resp := e.doAttachment(t, "/projects/project-1/work-items/"+root.WorkItemID+"/attachments", "",
		attachmentHeaders("text/plain", "USER", digest), content)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleAppendConversationAttachment_MissingDeclaredDigest_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-1")
	root := e.seedRootWorkItem(t, "project-1", "repo-1", "att-nodigest")

	resp := e.doAttachment(t, "/projects/project-1/work-items/"+root.WorkItemID+"/attachments", "idem-http-nodigest",
		map[string]string{"Content-Type": "text/plain", message.AttachmentRoleHeader: "USER"}, []byte("bytes"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleAppendConversationAttachment_TamperedContent_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-1")
	root := e.seedRootWorkItem(t, "project-1", "repo-1", "att-tamper")

	content := []byte("the real bytes actually sent over the wire")
	wrongSum := sha256.Sum256([]byte("a completely different declared payload"))
	wrongDigest := hex.EncodeToString(wrongSum[:])

	resp := e.doAttachment(t, "/projects/project-1/work-items/"+root.WorkItemID+"/attachments", "idem-http-tamper",
		attachmentHeaders("text/plain", "USER", wrongDigest), content)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeInto(t, resp, &payload)
	if payload.Error.Code != string(httpapi.ErrorCodeInvalidRequest) {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, httpapi.ErrorCodeInvalidRequest)
	}
}

func TestHandleAppendConversationAttachment_UnknownWorkItem_Returns404Shaped(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "project-1")

	content := []byte("bytes")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	resp := e.doAttachment(t, "/projects/project-1/work-items/does-not-exist/attachments", "idem-http-404",
		attachmentHeaders("text/plain", "USER", digest), content)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
