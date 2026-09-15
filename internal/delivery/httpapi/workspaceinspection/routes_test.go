package workspaceinspection_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// --- GetSource ---

func TestGetWorkspaceSource_HappyPath(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["path"] = "service.txt"
	query["revision"] = env.baseRevision.VCSObjectID
	query["generation"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)

	resp := env.do(t, env.sourceURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "line-1\n" {
		t.Fatalf("body = %q, want %q", body, "line-1\n")
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain prefix", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", resp.Header.Get("X-Content-Type-Options"))
	}
	if resp.Header.Get("X-Aw-Source-Binary") != "false" {
		t.Fatalf("X-Aw-Source-Binary = %q, want false", resp.Header.Get("X-Aw-Source-Binary"))
	}
	if resp.Header.Get("X-Aw-Source-Truncated") != "false" {
		t.Fatalf("X-Aw-Source-Truncated = %q, want false", resp.Header.Get("X-Aw-Source-Truncated"))
	}
	if resp.Header.Get("ETag") == "" {
		t.Fatal("ETag header is empty, want a non-empty content-addressed value")
	}
}

func TestGetWorkspaceSource_MissingRequiredQueryParam(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["revision"] = env.baseRevision.VCSObjectID
	query["generation"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)
	// "path" deliberately omitted.

	resp := env.do(t, env.sourceURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing path)", resp.StatusCode)
	}
}

func TestGetWorkspaceSource_MalformedGeneration(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["path"] = "service.txt"
	query["revision"] = env.baseRevision.VCSObjectID
	query["generation"] = "not-a-number"

	resp := env.do(t, env.sourceURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (malformed generation)", resp.StatusCode)
	}
}

func TestGetWorkspaceSource_UnknownRepositoryWorkspaceIsHidden(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["path"] = "service.txt"
	query["revision"] = env.baseRevision.VCSObjectID
	query["generation"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)

	url := env.base + "/projects/" + testProjectID + "/repository-workspaces/does-not-exist/source" + queryStringFrom(query)
	resp := env.do(t, url)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (unknown repository workspace)", resp.StatusCode)
	}
}

func TestGetWorkspaceSource_ScopeMismatchIsHidden(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["repositoryId"] = "someone-elses-repository"
	query["path"] = "service.txt"
	query["revision"] = env.baseRevision.VCSObjectID
	query["generation"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)

	resp := env.do(t, env.sourceURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (scope mismatch, leakage-normalized)", resp.StatusCode)
	}
}

func TestGetWorkspaceSource_QuarantinedWorkspaceIsConflict(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceQuarantined)

	query := scopeQuery()
	query["path"] = "service.txt"
	query["revision"] = env.baseRevision.VCSObjectID
	query["generation"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)

	resp := env.do(t, env.sourceURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (workspace not READY)", resp.StatusCode)
	}
}

func TestGetWorkspaceSource_PathTraversalIsRejected(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["path"] = "../outside.txt"
	query["revision"] = env.baseRevision.VCSObjectID
	query["generation"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)

	resp := env.do(t, env.sourceURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (path traversal)", resp.StatusCode)
	}
}

func TestGetWorkspaceSource_ArbitraryRevisionIsRejected(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["path"] = "service.txt"
	query["revision"] = "HEAD"
	query["generation"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)

	resp := env.do(t, env.sourceURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (arbitrary ref expression rejected)", resp.StatusCode)
	}
}

func TestGetWorkspaceSource_PathNotFoundAtRevision(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["path"] = "does-not-exist.txt"
	query["revision"] = env.baseRevision.VCSObjectID
	query["generation"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)

	resp := env.do(t, env.sourceURL(query))
	defer resp.Body.Close()
	// gitworktree.ErrPathNotFound cannot be named by this package (its own
	// concrete sentinel lives in the forbidden adapter package) — it falls
	// into writeQueryError's own opaque default bucket, 400.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (path does not exist at revision)", resp.StatusCode)
	}
}

// --- GetDiff ---

func TestGetWorkspaceDiff_HappyPath(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)
	current := commitWorkspaceChange(t, env.provider, env.handle, "service.txt", "line-1\nline-2\n")

	query := scopeQuery()
	query["base"] = env.baseRevision.VCSObjectID
	query["baseGeneration"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)
	query["result"] = current.VCSObjectID
	query["resultGeneration"] = strconv.FormatUint(current.WorkspaceGeneration, 10)

	resp := env.do(t, env.diffURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
		Patch []byte `json:"patch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Files) != 1 || body.Files[0].Path != "service.txt" {
		t.Fatalf("files = %+v, want exactly one entry for service.txt", body.Files)
	}
	if len(body.Patch) == 0 {
		t.Fatal("patch is empty, want a real unified diff")
	}
}

func TestGetWorkspaceDiff_UnauthorizedRevisionIsRejected(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["base"] = "0000000000000000000000000000000000000000"
	query["baseGeneration"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)
	query["result"] = env.currentRevision.VCSObjectID
	query["resultGeneration"] = strconv.FormatUint(env.currentRevision.WorkspaceGeneration, 10)

	resp := env.do(t, env.diffURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (unauthorized base revision)", resp.StatusCode)
	}
}

func TestGetWorkspaceDiff_MissingRequiredQueryParam(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["base"] = env.baseRevision.VCSObjectID
	query["baseGeneration"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)
	// "result"/"resultGeneration" deliberately omitted.

	resp := env.do(t, env.diffURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing result revision)", resp.StatusCode)
	}
}

// --- GetRepositoryLog ---

func TestGetWorkspaceRepositoryLog_PaginatesWithCursor(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)
	var anchor workspace.Revision
	for i := 0; i < 3; i++ {
		anchor = commitWorkspaceChange(t, env.provider, env.handle, "service.txt", fmt.Sprintf("revision-%d\n", i))
	}

	query := scopeQuery()
	query["anchor"] = anchor.VCSObjectID
	query["anchorGeneration"] = strconv.FormatUint(anchor.WorkspaceGeneration, 10)
	query["limit"] = "2"

	resp := env.do(t, env.repositoryLogURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var page1 struct {
		Entries []struct {
			CommitID string `json:"commitId"`
		} `json:"entries"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page1); err != nil {
		t.Fatalf("decode page1: %v", err)
	}
	if len(page1.Entries) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v, want 2 entries and a non-empty cursor", page1)
	}

	query["cursor"] = page1.NextCursor
	resp2 := env.do(t, env.repositoryLogURL(query))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}
	var page2 struct {
		Entries []struct {
			CommitID string `json:"commitId"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&page2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page2.Entries) == 0 {
		t.Fatal("page2 returned zero entries")
	}
	if page2.Entries[0].CommitID == page1.Entries[0].CommitID {
		t.Fatal("page2 repeated page1's own first entry — cursor did not advance")
	}
}

func TestGetWorkspaceRepositoryLog_InvalidCursorIsRejected(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["anchor"] = env.baseRevision.VCSObjectID
	query["anchorGeneration"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)
	query["cursor"] = "not-a-real-commit-object-id"

	resp := env.do(t, env.repositoryLogURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid cursor)", resp.StatusCode)
	}
}

func TestGetWorkspaceRepositoryLog_UnknownRepositoryWorkspaceIsHidden(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)

	query := scopeQuery()
	query["anchor"] = env.baseRevision.VCSObjectID
	query["anchorGeneration"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)

	url := env.base + "/projects/" + testProjectID + "/repository-workspaces/does-not-exist/repository-log" + queryStringFrom(query)
	resp := env.do(t, url)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (unknown repository workspace)", resp.StatusCode)
	}
}

// queryStringFrom is a small local alias so tests that build a custom URL
// (not through env.sourceURL/diffURL/repositoryLogURL) can still reuse
// fixture_test.go's own encodeQuery.
func queryStringFrom(query map[string]string) string { return encodeQuery(query) }
