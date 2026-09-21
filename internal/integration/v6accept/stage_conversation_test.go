package v6accept

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

// conversation appends a text message and a binary attachment to the
// WorkItem's conversation and proves the durable-protocol guarantees the
// contract promises for external I/O: an exact replay returns the same
// result without appending again, and the same key with a different body is
// refused.
func (j *journey) conversation(t *testing.T) {
	api := j.s.api
	base := "/projects/" + j.projectID + "/work-items/" + j.childWorkItemID

	list := func() (count int, sequences []uint64) {
		var page struct {
			Items []struct {
				MessageID string `json:"messageId"`
				Sequence  uint64 `json:"sequence"`
			} `json:"items"`
		}
		api.get(t, base+"/messages").requireStatus(t, http.StatusOK).decode(t, &page)
		for _, item := range page.Items {
			sequences = append(sequences, item.Sequence)
		}
		return len(page.Items), sequences
	}
	if count, _ := list(); count != 0 {
		t.Fatalf("a new WorkItem has %d messages, want 0", count)
	}

	message := map[string]string{"role": "USER", "content": "please keep the change small", "contentType": "text/plain"}
	first := api.post(t, base+"/messages", message, withIdempotencyKey("acc-msg-1")).requireStatus(t, http.StatusCreated)
	replay := api.post(t, base+"/messages", message, withIdempotencyKey("acc-msg-1")).requireStatus(t, http.StatusOK, http.StatusCreated)
	if string(first.body) != string(replay.body) {
		t.Fatalf("message replay differs:\nfirst:  %s\nreplay: %s", first.body, replay.body)
	}
	conflicting := map[string]string{"role": "USER", "content": "a different body", "contentType": "text/plain"}
	if got := api.post(t, base+"/messages", conflicting, withIdempotencyKey("acc-msg-1")); got.status != http.StatusConflict && got.status != http.StatusUnprocessableEntity {
		t.Fatalf("same key + different body = %d, want a conflict: %s", got.status, got.body)
	}
	if count, _ := list(); count != 1 {
		t.Fatalf("after one message and its replay the conversation has %d messages, want 1", count)
	}

	payload := []byte("attachment bytes for the acceptance journey\n")
	digest := sha256.Sum256(payload)
	attach := func(key string, body []byte) response {
		return api.do(t, http.MethodPost, base+"/attachments", body,
			withIdempotencyKey(key),
			withHeader("Content-Type", "text/plain"),
			withHeader("X-Attachment-Role", "USER"),
			withHeader("X-Attachment-Sensitivity", "PUBLIC"),
			withHeader("X-Attachment-Sha256", hex.EncodeToString(digest[:])),
		)
	}
	stored := attach("acc-att-1", payload).requireStatus(t, http.StatusCreated)
	again := attach("acc-att-1", payload).requireStatus(t, http.StatusOK, http.StatusCreated)
	if string(stored.body) != string(again.body) {
		t.Fatalf("attachment replay differs:\nfirst:  %s\nreplay: %s", stored.body, again.body)
	}
	if tampered := attach("acc-att-2", []byte("different bytes, same declared digest")); tampered.status < 400 {
		t.Fatalf("an attachment whose bytes do not match the declared sha256 was accepted (%d): %s", tampered.status, tampered.body)
	}
	if count, sequences := list(); count != 2 {
		t.Fatalf("conversation has %d messages (sequences %v), want 2 (message + attachment)", count, sequences)
	}
}
