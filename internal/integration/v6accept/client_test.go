package v6accept

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// apiClient is the journey's only way to touch the product: a plain
// net/http client speaking the documented wire contract — session token from
// the bootstrap page, Idempotency-Key on every mutation, If-Match where the
// contract requires it. It imports the wire header names from httpapi and
// nothing else from the delivery layer, so the suite exercises the real
// server rather than calling its handlers.
type apiClient struct {
	address string
	baseURL string
	token   string
	actor   string
	http    *http.Client
	keySeq  atomic.Int64
}

func newAPIClient(address string) *apiClient {
	return &apiClient{
		address: address,
		baseURL: "http://" + address,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// response is one HTTP exchange, already read.
type response struct {
	status int
	header http.Header
	body   []byte
}

// json decodes the body into out, failing the test with the raw body on error.
func (r response) decode(t *testing.T, out any) {
	t.Helper()
	if err := json.Unmarshal(r.body, out); err != nil {
		t.Fatalf("decode response (status %d): %v\nbody: %s", r.status, err, string(r.body))
	}
}

// requireStatus fails with the response body unless the status matches.
func (r response) requireStatus(t *testing.T, want ...int) response {
	t.Helper()
	for _, status := range want {
		if r.status == status {
			return r
		}
	}
	t.Fatalf("status = %d, want one of %v\nbody: %s", r.status, want, string(r.body))
	return r
}

// etag returns the response's strong ETag exactly as served (quotes kept), so
// it can be echoed into If-Match.
func (r response) etag() string { return r.header.Get(httpapi.ETagHeader) }

// requestOption customizes one request.
type requestOption func(*http.Request)

func withIfMatch(etag string) requestOption {
	return func(r *http.Request) { r.Header.Set(httpapi.IfMatchHeader, etag) }
}

func withIdempotencyKey(key string) requestOption {
	return func(r *http.Request) { r.Header.Set(httpapi.IdempotencyKeyHeader, key) }
}

func withHeader(key, value string) requestOption {
	return func(r *http.Request) { r.Header.Set(key, value) }
}

func withoutSessionToken() requestOption {
	return func(r *http.Request) { r.Header.Del(httpapi.SessionTokenHeader) }
}

// do sends one request. body may be nil, a string/[]byte (sent as-is) or any
// value (JSON-encoded). Mutations get a fresh Idempotency-Key unless the
// caller supplied one.
func (c *apiClient) do(t *testing.T, method, path string, body any, options ...requestOption) response {
	t.Helper()
	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(value)
	case string:
		reader = strings.NewReader(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode %s %s body: %v", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if reader != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set(httpapi.SessionTokenHeader, c.token)
	}
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set(httpapi.IdempotencyKeyHeader, fmt.Sprintf("acc-%d-%d", time.Now().UnixNano(), c.keySeq.Add(1)))
	}
	for _, option := range options {
		option(request)
	}
	httpResponse, err := c.http.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer httpResponse.Body.Close()
	payload, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return response{status: httpResponse.StatusCode, header: httpResponse.Header, body: payload}
}

func (c *apiClient) get(t *testing.T, path string, options ...requestOption) response {
	t.Helper()
	return c.do(t, http.MethodGet, path, nil, options...)
}

func (c *apiClient) post(t *testing.T, path string, body any, options ...requestOption) response {
	t.Helper()
	return c.do(t, http.MethodPost, path, body, options...)
}

func (c *apiClient) put(t *testing.T, path string, body any, options ...requestOption) response {
	t.Helper()
	return c.do(t, http.MethodPut, path, body, options...)
}

var bootstrapPattern = regexp.MustCompile(`window\.__AW_BOOTSTRAP__=(\{.*?\});`)

// bootstrap loads the one page that carries the per-start session token and
// the trusted principal snapshot, exactly as the browser (or any local
// client) must — there is no other way to learn the token.
func (c *apiClient) bootstrap(t *testing.T) {
	t.Helper()
	page := c.get(t, "/").requireStatus(t, http.StatusOK)
	match := bootstrapPattern.FindSubmatch(page.body)
	if match == nil {
		t.Fatalf("bootstrap page carries no window.__AW_BOOTSTRAP__ payload:\n%s", string(page.body))
	}
	var payload struct {
		Token string   `json:"token"`
		Actor string   `json:"actor"`
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(match[1], &payload); err != nil || payload.Token == "" {
		t.Fatalf("bootstrap payload %q: %v", match[1], err)
	}
	c.token, c.actor = payload.Token, payload.Actor
}

// waitReady polls /health/ready until the server reports every dependency
// healthy.
func (c *apiClient) waitReady(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last response
	for time.Now().Before(deadline) {
		last = c.get(t, "/health/ready")
		if last.status == http.StatusOK {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("/health/ready never returned 200 within %s (last %d: %s)", within, last.status, string(last.body))
}

// waitFor polls fn every interval until it returns true, failing with what
// describes the wait if it never does. It exists so every journey wait is a
// bounded, named observation of durable product state — never a bare sleep.
func waitFor(t *testing.T, what string, within, interval time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("timed out after %s waiting for %s", within, what)
}
