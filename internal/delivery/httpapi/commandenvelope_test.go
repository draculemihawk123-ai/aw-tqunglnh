package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func TestRequireIdempotencyKey_MissingReturnsTypedError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	_, err := httpapi.RequireIdempotencyKey(req)
	if err != httpapi.ErrIdempotencyKeyRequired {
		t.Fatalf("err = %v, want ErrIdempotencyKeyRequired", err)
	}
}

func TestRequireIdempotencyKey_PresentReturnsValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(httpapi.IdempotencyKeyHeader, "key-1")
	got, err := httpapi.RequireIdempotencyKey(req)
	if err != nil {
		t.Fatalf("RequireIdempotencyKey: %v", err)
	}
	if got != "key-1" {
		t.Fatalf("got = %q, want key-1", got)
	}
}

func TestRequireIfMatch_MissingReturnsTypedError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/", nil)
	_, err := httpapi.RequireIfMatch(req)
	if err != httpapi.ErrIfMatchRequired {
		t.Fatalf("err = %v, want ErrIfMatchRequired", err)
	}
}

func TestETagFromVersion_RoundTripsThroughVersionFromETag(t *testing.T) {
	etag := httpapi.ETagFromVersion(42)
	if etag != `"42"` {
		t.Fatalf("etag = %q, want \"42\"", etag)
	}
	got, err := httpapi.VersionFromETag(etag)
	if err != nil {
		t.Fatalf("VersionFromETag: %v", err)
	}
	if got != 42 {
		t.Fatalf("got = %d, want 42", got)
	}
}

func TestVersionFromETag_MalformedReturnsTypedError(t *testing.T) {
	for _, bad := range []string{"", "42", `"abc"`, `"`, `""x`} {
		if _, err := httpapi.VersionFromETag(bad); err == nil {
			t.Errorf("VersionFromETag(%q) = nil error, want an error", bad)
		}
	}
}

type canonPayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// TestSemanticHash_ReorderedJSONProducesSameHash is V6-02's own explicit
// Verify bullet: "reordered JSON same hash".
func TestSemanticHash_ReorderedJSONProducesSameHash(t *testing.T) {
	scope := ports.InstallationScope()

	req1 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"widget","count":3}`))
	var dst1 canonPayload
	canon1, err := httpapi.CanonicalizeJSON(req1, 1<<20, &dst1)
	if err != nil {
		t.Fatalf("CanonicalizeJSON (1): %v", err)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"count":3,"name":"widget"}`))
	var dst2 canonPayload
	canon2, err := httpapi.CanonicalizeJSON(req2, 1<<20, &dst2)
	if err != nil {
		t.Fatalf("CanonicalizeJSON (2): %v", err)
	}

	hash1 := httpapi.SemanticHash("CreateWidget", scope, canon1, "", 0)
	hash2 := httpapi.SemanticHash("CreateWidget", scope, canon2, "", 0)
	if hash1 != hash2 {
		t.Fatalf("hash1 = %q, hash2 = %q, want equal for reordered-but-equivalent JSON", hash1, hash2)
	}
}

func TestSemanticHash_DifferentPayloadProducesDifferentHash(t *testing.T) {
	scope := ports.InstallationScope()
	hashA := httpapi.SemanticHash("CreateWidget", scope, []byte(`{"name":"a"}`), "", 0)
	hashB := httpapi.SemanticHash("CreateWidget", scope, []byte(`{"name":"b"}`), "", 0)
	if hashA == hashB {
		t.Fatal("different payloads must not hash the same")
	}
}

func TestSemanticHash_DifferentCommandTypeProducesDifferentHash(t *testing.T) {
	scope := ports.InstallationScope()
	payload := []byte(`{"name":"a"}`)
	hashA := httpapi.SemanticHash("CreateWidget", scope, payload, "", 0)
	hashB := httpapi.SemanticHash("UpdateWidget", scope, payload, "", 0)
	if hashA == hashB {
		t.Fatal("different command types must not hash the same")
	}
}

func TestSemanticHash_DifferentScopeProducesDifferentHash(t *testing.T) {
	payload := []byte(`{"name":"a"}`)
	hashInstall := httpapi.SemanticHash("CreateWidget", ports.InstallationScope(), payload, "", 0)
	hashProject := httpapi.SemanticHash("CreateWidget", ports.ProjectScope("proj-1"), payload, "", 0)
	if hashInstall == hashProject {
		t.Fatal("installation scope and project scope must not hash the same")
	}
}

func TestSemanticHash_DifferentExpectedVersionProducesDifferentHash(t *testing.T) {
	scope := ports.ProjectScope("proj-1")
	payload := []byte(`{"name":"a"}`)
	hash1 := httpapi.SemanticHash("UpdateWidget", scope, payload, "", 1)
	hash2 := httpapi.SemanticHash("UpdateWidget", scope, payload, "", 2)
	if hash1 == hash2 {
		t.Fatal("different expected versions must not hash the same")
	}
}

func TestSemanticHash_DifferentExtraContentDigestProducesDifferentHash(t *testing.T) {
	scope := ports.ProjectScope("proj-1")
	payload := []byte(`{"name":"a"}`)
	hash1 := httpapi.SemanticHash("UploadAttachment", scope, payload, "sha256:aaa", 0)
	hash2 := httpapi.SemanticHash("UploadAttachment", scope, payload, "sha256:bbb", 0)
	if hash1 == hash2 {
		t.Fatal("different extra content digests must not hash the same")
	}
}

func TestCanonicalizeJSON_RejectsUnknownField(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"a","extra":true}`))
	var dst canonPayload
	if _, err := httpapi.CanonicalizeJSON(req, 1<<20, &dst); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}
