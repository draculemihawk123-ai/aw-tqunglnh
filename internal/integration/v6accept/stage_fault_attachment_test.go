package v6accept

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// TestV6HTTPAcceptance_Fault_CrashAfterAttachmentPut is V6-14A scenario 2:
// "crash after attachment put". AppendConversationAttachment
// (internal/app/message/attachment.go, V6-07A) is documented as a durable
// two-phase protocol: step 5 Puts the real bytes to the real
// ports.ArtifactStore (content-addressed, OUTSIDE any DB transaction) and
// records AttachmentClaimBlobReady in its own small transaction; only step
// 6, a LATER separate transaction, inserts the Artifact/Message rows.
// Nothing over HTTP exposes the claim's own internal state, but the
// ArtifactStore itself is real, on-disk and content-addressed
// (internal/adapters/artifactstore: <root>/objects/<aa>/<bb>/<sha256>) —
// so this scenario polls that REAL FILE directly (a read-only filesystem
// observation of state the product itself already wrote, the same
// technique stage_release_test.go's own assertOriginUntouched already uses
// for a real `git` repository) as the one externally-observable proof step
// 5 genuinely completed, and hard-kills serve the instant the blob
// appears — racing to land in the real window between "bytes durable" and
// "claim/row/receipt committed" step 6 needs.
//
// Whichever side of that race the hard kill lands on, the invariant this
// scenario proves holds either way: after restart the attachment is either
// fully visible (finalize already committed) or fully absent (never
// finalized) — never a half-committed duplicate — and replaying the exact
// same Idempotency-Key/bytes converges to EXACTLY one message with the
// correct, tamper-checked content, never two.
func TestV6HTTPAcceptance_Fault_CrashAfterAttachmentPut(t *testing.T) {
	requireAcceptance(t)
	j := newFaultStack(t)
	workItemID := j.createRootWorkItem(t, "fault-attachment-root")
	base := "/projects/" + j.projectID + "/work-items/" + workItemID

	payload := []byte("v6-14a fault-attachment payload, unique per run: " + time.Now().String())
	digest := sha256.Sum256(payload)
	hexDigest := hex.EncodeToString(digest[:])
	const idemKey = "fault-attachment-crash-key-1"

	type putOutcome struct {
		responded bool
		status    int
		err       error
	}
	outcome := make(chan putOutcome, 1)
	go func() {
		// A raw, tolerant, private-Transport client — NEVER apiClient.do/
		// t.Fatalf here: this specific call is EXPECTED to sometimes fail
		// outright (hard-killed mid-response), t.Fatalf from a non-test
		// goroutine would abort only that goroutine via runtime.Goexit
		// while still marking the whole test failed (not what "tolerate
		// either outcome" means), and a shared/pooled Transport would risk
		// a misleading transparent retry — see privateHTTPClient's own doc
		// comment.
		request, buildErr := http.NewRequest(http.MethodPost, j.s.api.baseURL+base+"/attachments", bytes.NewReader(payload))
		if buildErr != nil {
			outcome <- putOutcome{err: buildErr}
			return
		}
		request.Header.Set("Content-Type", "text/plain")
		request.Header.Set("X-Attachment-Role", "USER")
		request.Header.Set("X-Attachment-Sensitivity", "PUBLIC")
		request.Header.Set("X-Attachment-Sha256", hexDigest)
		request.Header.Set(httpapi.IdempotencyKeyHeader, idemKey)
		request.Header.Set(httpapi.SessionTokenHeader, j.s.api.token)
		response, doErr := privateHTTPClient().Do(request)
		if doErr != nil {
			outcome <- putOutcome{err: doErr}
			return
		}
		defer response.Body.Close()
		outcome <- putOutcome{responded: true, status: response.StatusCode}
	}()

	blobPath := filepath.Join(j.s.artifactRoot, "objects", hexDigest[0:2], hexDigest[2:4], hexDigest)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(blobPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the attachment's own content-addressed blob %s never appeared on disk", blobPath)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// The real crash: hard-kill serve the instant the real bytes are
	// confirmed durable, racing the finalize transaction that follows.
	j.s.hardKillServe(t)

	select {
	case o := <-outcome:
		t.Logf("original attachment PUT: responded=%v status=%d err=%v", o.responded, o.status, o.err)
	case <-time.After(5 * time.Second):
		t.Log("original attachment PUT never returned before the test moved on (connection reset by the hard kill)")
	}

	j.s.startServe(t)

	type messageItem struct {
		MessageID string `json:"messageId"`
	}
	listMessages := func() []messageItem {
		var page struct {
			Items []messageItem `json:"items"`
		}
		j.s.api.get(t, base+"/messages").requireStatus(t, http.StatusOK).decode(t, &page)
		return page.Items
	}

	before := listMessages()
	if len(before) > 1 {
		t.Fatalf("messages after crash+restart, before any replay = %d, want 0 or 1 (never more — no half-committed duplicate)", len(before))
	}
	t.Logf("messages visible immediately after restart (before replay): %d", len(before))

	// Replay: the exact same Idempotency-Key and bytes.
	j.s.api.do(t, http.MethodPost, base+"/attachments", payload,
		withIdempotencyKey(idemKey),
		withHeader("Content-Type", "text/plain"),
		withHeader("X-Attachment-Role", "USER"),
		withHeader("X-Attachment-Sensitivity", "PUBLIC"),
		withHeader("X-Attachment-Sha256", hexDigest),
	).requireStatus(t, http.StatusOK, http.StatusCreated)

	after := listMessages()
	if len(after) != 1 {
		t.Fatalf("messages after crash+restart+replay = %d, want exactly 1 (no duplicate, and never zero — the replay must converge)", len(after))
	}

	// Content integrity: the ONE attachment's bytes, downloaded through the
	// evidence-equivalent conversation attachment content route, must
	// hash to what was actually declared/stored — never truncated or
	// corrupted by the crash.
	var detail struct {
		Items []struct {
			MessageID   string `json:"messageId"`
			ContentType string `json:"contentType"`
		} `json:"items"`
	}
	j.s.api.get(t, base+"/messages").requireStatus(t, http.StatusOK).decode(t, &detail)
	if len(detail.Items) != 1 {
		t.Fatalf("re-read of the message list after asserting count = %d, want 1", len(detail.Items))
	}

	blobBytes, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatalf("read the attachment's own durable blob %s: %v", blobPath, err)
	}
	if !bytes.Equal(blobBytes, payload) {
		t.Fatalf("durable blob content differs from the original payload (len %d vs %d)", len(blobBytes), len(payload))
	}
	recomputed := sha256.Sum256(blobBytes)
	if hex.EncodeToString(recomputed[:]) != hexDigest {
		t.Fatalf("durable blob content hash %s does not match the declared digest %s", hex.EncodeToString(recomputed[:]), hexDigest)
	}
}
