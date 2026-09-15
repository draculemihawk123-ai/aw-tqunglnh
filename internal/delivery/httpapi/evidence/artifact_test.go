package evidence_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

type artifactSummaryItem struct {
	ArtifactID     string `json:"artifactId"`
	ProjectID      string `json:"projectId"`
	ContentHash    string `json:"contentHash"`
	Size           int64  `json:"size"`
	MediaType      string `json:"mediaType"`
	Sensitivity    string `json:"sensitivity"`
	Redacted       bool   `json:"redacted"`
	RetentionClass string `json:"retentionClass"`
	AttachState    string `json:"attachState"`
}

type listArtifactsResponse struct {
	Items []artifactSummaryItem `json:"items"`
}

// bodyHasNoLocatorField proves this task's own "Không làm: KHÔNG expose
// locator" line at the wire level, not merely by code review: the raw JSON
// bytes never contain the field name a Locator would be serialized under.
func bodyHasNoLocatorField(t *testing.T, raw []byte) {
	t.Helper()
	lower := strings.ToLower(string(raw))
	if strings.Contains(lower, "locator") {
		t.Fatalf("response body leaks a locator-shaped field: %s", raw)
	}
}

// TestListArtifacts_HappyPath_NeverExposesLocator is this task's own
// "artifact inventory" line plus its "Không làm: KHÔNG expose locator" line
// verified against a real HTTP response body.
func TestListArtifacts_HappyPath_NeverExposesLocator(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "gate output content")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "gate-criterion-x", "PASS", []string{string(a.ID)})

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/evidence/"+string(evidence.ID)+"/artifacts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	resp.Body.Close()
	bodyHasNoLocatorField(t, raw)

	var list listArtifactsResponse
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("items = %+v, want exactly 1", list.Items)
	}
	item := list.Items[0]
	if item.ArtifactID != string(a.ID) || item.ProjectID != "project-1" {
		t.Fatalf("item identities = %+v", item)
	}
	if item.ContentHash != a.ContentHash || item.Size != a.Size || item.MediaType != "text/plain" {
		t.Fatalf("item metadata = %+v, want ContentHash=%s Size=%d MediaType=text/plain", item, a.ContentHash, a.Size)
	}
	if item.Sensitivity != "PUBLIC" || item.Redacted {
		t.Fatalf("item sensitivity = %+v, want PUBLIC/false", item)
	}
	if item.AttachState != "ATTACHED" {
		t.Fatalf("item.AttachState = %q, want ATTACHED", item.AttachState)
	}
}

func TestListArtifacts_EvidenceBelongsToAnotherWorkItem_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	rootA := env.seedRootWorkItem(t, "project-1", "repo-a", "a")
	rootB := env.seedRootWorkItem(t, "project-1", "repo-a", "b")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", rootA.WorkItemID, rootA.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "belongs to A")
	evidence := env.seedEvidence(t, "project-1", rootA.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+rootB.WorkItemID+"/evidence/"+string(evidence.ID)+"/artifacts", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestGetArtifactContent_HappyPath_FullBody_HeadersCorrect proves the real
// content-hash ETag, no-sniff and Content-Length/Accept-Ranges headers, and
// that the full body streams back byte-for-byte.
func TestGetArtifactContent_HappyPath_FullBody_HeadersCorrect(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	const content = "0123456789"
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), content)
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	path := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence/" + string(evidence.ID) + "/artifacts/" + string(a.ID) + "/content"
	resp := env.do(t, http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != content {
		t.Fatalf("body = %q, want %q", body, content)
	}
	if got := resp.Header.Get("ETag"); got != `"`+a.ContentHash+`"` {
		t.Fatalf("ETag = %q, want %q", got, `"`+a.ContentHash+`"`)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	if got := resp.Header.Get("Content-Length"); got != "10" {
		t.Fatalf("Content-Length = %q, want 10", got)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
	if disp := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(disp, "inline") {
		t.Fatalf("Content-Disposition = %q, want inline (text/plain is on the safe allow-list)", disp)
	}
}

// TestGetArtifactContent_RangeRequest_PartialContent is this task's own
// "range requests" Verify bullet: a real HTTP Range header returns 206 with
// exactly the requested slice and a correct Content-Range.
func TestGetArtifactContent_RangeRequest_PartialContent(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	const content = "0123456789"
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), content)
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	path := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence/" + string(evidence.ID) + "/artifacts/" + string(a.ID) + "/content"
	resp := env.do(t, http.MethodGet, path, map[string]string{"Range": "bytes=2-5"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 206, body=%s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "2345" {
		t.Fatalf("body = %q, want %q", body, "2345")
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q, want bytes 2-5/10", got)
	}
	if got := resp.Header.Get("Content-Length"); got != "4" {
		t.Fatalf("Content-Length = %q, want 4", got)
	}
}

func TestGetArtifactContent_RangeNotSatisfiable_Returns416(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "0123456789")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	path := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence/" + string(evidence.ID) + "/artifacts/" + string(a.ID) + "/content"
	resp := env.do(t, http.MethodGet, path, map[string]string{"Range": "bytes=100-200"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", resp.StatusCode)
	}
}

// TestGetArtifactContent_ArtifactNotReferencedByThisEvidence_ReturnsHiddenNotFound
// is this task's own scope-boundary proof: artifactB is a real artifact, in
// the same project, real content, but it is NOT one of evidenceA's own
// ArtifactReferences — requesting it through evidenceA's own content route
// must be indistinguishable from a genuinely unknown artifact ID.
func TestGetArtifactContent_ArtifactNotReferencedByThisEvidence_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attemptA := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "a")
	attemptB := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "b")
	artifactA := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content a")
	artifactB := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content b")
	evidenceA := env.seedEvidence(t, "project-1", root.WorkItemID, attemptA, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(artifactA.ID)})
	_ = env.seedEvidence(t, "project-1", root.WorkItemID, attemptB, "gate-criterion-x", "PASS", []string{string(artifactB.ID)})

	path := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence/" + string(evidenceA.ID) + "/artifacts/" + string(artifactB.ID) + "/content"
	resp := env.do(t, http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (artifactB is real but not referenced by evidenceA)", resp.StatusCode)
	}
}

// TestGetArtifactContent_PathTraversalLookingArtifactID_ReturnsHiddenNotFound
// is this task's own "path traversal" Verify bullet: traversal is
// impossible by construction (artifactId is only ever a database lookup
// key, never a filesystem path — the real Locator is resolved entirely
// server-side from the stored Artifact row), proven here rather than only
// argued: a "../../" -shaped artifactId simply matches no real Evidence
// ArtifactReference and produces the same 404 any other unknown ID would.
func TestGetArtifactContent_PathTraversalLookingArtifactID_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "content")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	for _, artifactID := range []string{"../../../../etc/passwd", "..%2f..%2fsecret", "sha256:deadbeef"} {
		path := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence/" + string(evidence.ID) + "/artifacts/" + url.PathEscape(artifactID) + "/content"
		resp := env.do(t, http.MethodGet, path, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("artifactID=%q: status = %d, want 404", artifactID, resp.StatusCode)
		}
	}
}

// TestGetArtifactContent_TamperedContent_Returns500NotServedSilently is
// this task's own "tamper" Verify bullet: a real on-disk byte corruption
// (never a DB row) is caught by ArtifactStore.Verify and surfaced as a
// typed error — the response is never a 200 with corrupted/wrong bytes.
func TestGetArtifactContent_TamperedContent_Returns500NotServedSilently(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Public, redact.NewMatcher(), "the real, untampered gate output")
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	path := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence/" + string(evidence.ID) + "/artifacts/" + string(a.ID) + "/content"

	// Sanity: real content verifies fine BEFORE tampering.
	before := env.do(t, http.MethodGet, path, nil)
	before.Body.Close()
	if before.StatusCode != http.StatusOK {
		t.Fatalf("before tamper: status = %d, want 200", before.StatusCode)
	}

	objectPath := env.artifactObjectPath(t, a.Locator)
	if err := os.WriteFile(objectPath, []byte("corrupted for real — not the recorded hash's own content"), 0o600); err != nil {
		t.Fatalf("tamper artifact content at %s: %v", objectPath, err)
	}

	after := env.do(t, http.MethodGet, path, nil)
	defer after.Body.Close()
	body, _ := io.ReadAll(after.Body)
	if after.StatusCode == http.StatusOK {
		t.Fatalf("after tamper: status = 200, want a typed error status — body=%s", body)
	}
	if bytes.Contains(body, []byte("corrupted for real")) {
		t.Fatalf("after tamper: response body leaked the corrupted content: %s", body)
	}
}

// TestGetArtifactContent_LargeStream_StreamsCorrectly proves a real
// multi-MB body round-trips byte-for-byte through the handler's own
// io.Copy-based streaming path (artifact.go's own handleGetArtifactContent:
// ArtifactStore.Open's reader is copied straight to the ResponseWriter,
// never read into a single in-memory []byte first) — this task's own
// "large stream" Verify bullet.
func TestGetArtifactContent_LargeStream_StreamsCorrectly(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")

	const size = 8 * 1024 * 1024 // 8 MiB
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 64)
	var buf bytes.Buffer
	for buf.Len() < size {
		buf.Write(chunk)
	}
	large := buf.String()[:size]

	a := env.seedArtifact(t, "project-1", "application/octet-stream", redact.Public, redact.NewMatcher(), large)
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	path := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence/" + string(evidence.ID) + "/artifacts/" + string(a.ID) + "/content"
	resp := env.do(t, http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Length"); got != "8388608" {
		t.Fatalf("Content-Length = %q, want 8388608", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != size {
		t.Fatalf("len(body) = %d, want %d", len(body), size)
	}
	if string(body) != large {
		t.Fatal("streamed body does not match the original large content byte-for-byte")
	}
}

// TestGetArtifactContent_HTMLAndSVG_ForceDownloadNeverInline is this task's
// own "HTML/SVG fixtures" Verify bullet: media.go's own closed inline-safe
// allow-list forces both to download (Content-Disposition: attachment)
// rather than render inline, even though the bytes ARE still served.
func TestGetArtifactContent_HTMLAndSVG_ForceDownloadNeverInline(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
	}{
		{"html", "text/html; charset=utf-8", "<html><body><script>alert('xss')</script></body></html>"},
		{"svg", "image/svg+xml", "<svg xmlns=\"http://www.w3.org/2000/svg\"><script>alert('xss')</script></svg>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.seedProject(t, "project-1")
			env.seedActiveRepository(t, "project-1", "repo-a")
			root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
			attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
			a := env.seedArtifact(t, "project-1", tc.contentType, redact.Public, redact.NewMatcher(), tc.body)
			evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

			path := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence/" + string(evidence.ID) + "/artifacts/" + string(a.ID) + "/content"
			resp := env.do(t, http.MethodGet, path, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			body, _ := io.ReadAll(resp.Body)
			if string(body) != tc.body {
				t.Fatalf("body = %q, want the real stored content %q (content IS served, just never inline)", body, tc.body)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
			}
			disp := resp.Header.Get("Content-Disposition")
			if !strings.HasPrefix(disp, "attachment") {
				t.Fatalf("Content-Disposition = %q, want attachment (never inline for %s)", disp, tc.contentType)
			}
		})
	}
}

// TestGetArtifactContent_SecretSensitivity_ServesRedactedPersistedBytes is
// this task's own "secret fixtures" Verify bullet: a SECRET-sensitivity
// artifact's own stored bytes are ALREADY redacted at write time (this
// codebase's own "redact BEFORE Put" ordering,
// internal/app/message/commands.go) — this route serves exactly what is
// durably persisted, so the response body must be the redacted placeholder,
// verified against real persisted bytes, never the original secret value.
func TestGetArtifactContent_SecretSensitivity_ServesRedactedPersistedBytes(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	attempt := seedExecutionAttempt(t, env.uow, "project-1", root.WorkItemID, root.FamilyID, "1")
	const secret = "this looks harmless but is classified"
	a := env.seedArtifact(t, "project-1", "text/plain", redact.Secret, redact.NewMatcher(), secret)
	evidence := env.seedEvidence(t, "project-1", root.WorkItemID, attempt, "COMMAND_EXECUTION", "SUCCEEDED", []string{string(a.ID)})

	// Sanity: prove the persisted bytes really are redacted, independent of
	// this route, before asserting the route serves them unchanged.
	assertPersistedContent(t, env, a, "[REDACTED]")

	path := "/projects/project-1/work-items/" + root.WorkItemID + "/evidence/" + string(evidence.ID) + "/artifacts/" + string(a.ID) + "/content"
	resp := env.do(t, http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "[REDACTED]" {
		t.Fatalf("body = %q, want [REDACTED] (the real persisted bytes, never the original secret)", body)
	}
	if strings.Contains(string(body), "classified") {
		t.Fatalf("body leaked the original secret content: %q", body)
	}
}
