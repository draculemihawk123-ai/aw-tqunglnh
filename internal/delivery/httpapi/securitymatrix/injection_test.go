package securitymatrix

// Path/content-injection, MIME and bind half of V6-13's own matrix:
// "path/content injection, MIME and cancellation" plus AK-ARCH-025A's own
// "the local API must reject external binds".

import (
	"net/http"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// TestLoopbackOnlyBind is AK-ARCH-025A/ADR-016's own "external bind phải bị
// từ chối, không chỉ off-by-default": httpapi.NewServer must refuse to
// construct at all for any host that is not, or does not resolve to, a
// loopback address — checked BEFORE net.Listen ever runs
// (server.go's validateLoopbackHost), so a misconfigured host can never
// even momentarily be listening on a routable interface.
func TestLoopbackOnlyBind(t *testing.T) {
	routes := httpapi.NewRouteRegistry()
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/health/live", OperationID: "healthLive",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: healthPlaceholder,
	})
	newServer := func(host string) (*httpapi.Server, error) {
		return httpapi.NewServer(httpapi.Config{
			Host: host, Port: 0, Routes: routes, IDs: idsource.Random{},
			MaxBodyBytes: 1 << 20, Token: sessionToken, Principal: testPrincipal(),
		})
	}

	for _, host := range []string{
		"0.0.0.0",      // every interface — the classic accidental external bind
		"::",           // the IPv6 form of the same mistake
		"192.168.1.10", // a routable private address
		"10.0.0.5",     // another routable private address
		"203.0.113.7",  // a routable public address
		"example.com",  // a hostname that does not resolve to loopback
		"",             // no host at all
	} {
		t.Run("rejects "+host, func(t *testing.T) {
			server, err := newServer(host)
			if err == nil {
				_ = server.Shutdown(t.Context())
				t.Fatalf("NewServer(Host: %q) succeeded — an external bind must be refused", host)
			}
		})
	}

	// Positive control: the two literal loopback forms an operator may
	// legitimately configure still work, so the check above is a real
	// discriminator rather than a blanket refusal.
	for _, host := range []string{"127.0.0.1", "localhost"} {
		t.Run("accepts "+host, func(t *testing.T) {
			server, err := newServer(host)
			if err != nil {
				t.Fatalf("NewServer(Host: %q) failed: %v", host, err)
			}
			if err := server.Shutdown(t.Context()); err != nil {
				t.Errorf("Shutdown: %v", err)
			}
		})
	}
}

// TestPathTraversalIsNeverServed proves no request-target can walk out of
// the registered route space — neither a literal `../`, nor a
// percent-encoded one, nor a mixed/double-encoded form, nor one smuggled in
// through a path parameter's own value.
//
// Every probe is sent over a RAW TCP connection, not through net/http's own
// client: a client would silently normalize `/a/../b` to `/b` before it
// ever left the process, which would make this test assert nothing about
// the server at all.
func TestPathTraversalIsNeverServed(t *testing.T) {
	e := newEnv(t)
	artifactPath := "/projects/" + e.alpha.ProjectID + "/work-items/" + e.alpha.WorkItemID +
		"/evidence/" + e.alpha.EvidenceID + "/artifacts/" + e.alpha.ArtifactID + "/content"

	probes := []struct {
		name   string
		target string
	}{
		{"literal dot-dot out of the route space", "/projects/" + e.alpha.ProjectID + "/../../etc/passwd"},
		{"percent-encoded dot-dot", "/projects/" + e.alpha.ProjectID + "/%2e%2e/%2e%2e/etc/passwd"},
		{"double-encoded dot-dot", "/projects/" + e.alpha.ProjectID + "/%252e%252e/%252e%252e/etc/passwd"},
		{"dot-dot inside a path parameter", "/projects/..%2f..%2f..%2fetc%2fpasswd"},
		{"backslash traversal", `/projects/` + e.alpha.ProjectID + `/..\..\windows\win.ini`},
		{"traversal in place of an artifact id", strings.Replace(artifactPath, e.alpha.ArtifactID, "..%2f..%2f..%2fetc%2fpasswd", 1)},
		{"null byte in a path parameter", "/projects/" + e.alpha.ProjectID + "%00/work-items"},
	}
	// A 200 here is not automatically a finding: "GET /" (bootstrap) is a
	// real, registered net/http.ServeMux SUBTREE pattern, so ANY GET that
	// matches no more specific route legitimately falls through to the SPA
	// shell — documented, pre-existing behaviour that
	// internal/delivery/httpapi/apicontract's own
	// TestRouteInventory_ServedEqualsDeclaredBothDirections already records.
	// What must NEVER happen is a traversal reaching content: a file from
	// outside the route space, an artifact's stored bytes, or any real
	// identifier this server issued.
	const bootstrapShell = "<!doctype html><title>bootstrap placeholder</title>"
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			raw := e.rawRequest(t, "GET "+probe.target+" HTTP/1.1")
			statusLine, _, _ := strings.Cut(raw, "\r\n")
			_, body, _ := strings.Cut(raw, "\r\n\r\n")
			for _, forbidden := range []string{"root:x:", "[fonts]", "artifact body for", "<script>alert"} {
				if strings.Contains(body, forbidden) {
					t.Errorf("request-target %q returned content it must never reach: %s", probe.target, truncate(raw))
				}
			}
			assertNoSeededIdentifier(t, e, "traversal "+probe.target, body)
			if strings.Contains(statusLine, " 200 ") {
				trimmed := strings.TrimSpace(body)
				if trimmed != bootstrapShell && trimmed != `{"items":[]}` {
					t.Errorf("request-target %q answered 200 with something other than the SPA shell or an empty collection: %s",
						probe.target, truncate(raw))
				}
			}
		})
	}
}

// TestQueryParameterPathIsNeverResolvedOutsideTheWorkspace covers the one
// route family that accepts a filesystem-shaped path as DATA
// (GET /projects/{p}/repository-workspaces/{rw}/source?path=...): a
// traversal attempt must be refused with the same opaque answer every other
// bad revision/path/cursor gets (internal/delivery/httpapi/workspaceinspection's
// own writeQueryError default branch), never a distinguishable error and
// never file content.
func TestQueryParameterPathIsNeverResolvedOutsideTheWorkspace(t *testing.T) {
	e := newEnv(t)
	base := "/projects/" + e.alpha.ProjectID + "/repository-workspaces/" + e.alpha.RepositoryWorkspaceID + "/source"
	scope := "&repositoryId=" + e.alpha.RepositoryID + "&workspaceSetId=" + e.alpha.WorkspaceSetID +
		"&revision=" + fixtureVCSObjectID + "&generation=1"

	for _, treePath := range []string{
		"../../../../etc/passwd",
		"..%2F..%2F..%2Fetc%2Fpasswd",
		"/etc/passwd",
		"C:%5CWindows%5Cwin.ini",
		"subdir/../../../outside.txt",
	} {
		t.Run(treePath, func(t *testing.T) {
			got := e.do(t, req{Method: http.MethodGet, Path: base + "?path=" + treePath + scope})
			if got.Status >= 200 && got.Status < 300 {
				t.Errorf("path=%q answered %d: %s", treePath, got.Status, truncate(got.Body))
			}
			for _, forbidden := range []string{"root:x:", "[fonts]"} {
				if strings.Contains(got.Body, forbidden) {
					t.Errorf("path=%q returned file content from outside the workspace: %s", treePath, truncate(got.Body))
				}
			}
			// The response must not name a filesystem path, which would
			// disclose this installation's own layout.
			for _, leak := range []string{"C:\\", "/tmp/", "AppData"} {
				if strings.Contains(got.Body, leak) {
					t.Errorf("path=%q leaked a filesystem path in its error: %s", treePath, truncate(got.Body))
				}
			}
		})
	}
}

// TestArtifactContentMediaHandling is V6-13's own "MIME" bullet against the
// one route that streams caller-reachable bytes
// (getArtifactContent). Every assertion is about what a BROWSER would do
// with the response, since that is the whole point of
// httpapi.ApplyContentHeaders' own closed inline-safe allow-list.
func TestArtifactContentMediaHandling(t *testing.T) {
	e := newEnv(t)

	// A second artifact, script-capable content type, attached to its own
	// real Evidence row — the case the allow-list exists for.
	htmlArtifact := e.seedArtifact(t, e.alpha.ProjectID, "text/html", "<script>alert(1)</script>")
	htmlEvidence := e.seedEvidence(t, e.alpha.ProjectID, e.alpha.WorkItemID, attemptFixture{
		RunID: e.alpha.RunID, NodeRunID: e.alpha.NodeRunID, AttemptID: e.alpha.AttemptID,
	}, "alpha-html", string(htmlArtifact.ID))

	contentPath := func(evidenceID, artifactID string) string {
		return "/projects/" + e.alpha.ProjectID + "/work-items/" + e.alpha.WorkItemID +
			"/evidence/" + evidenceID + "/artifacts/" + artifactID + "/content"
	}

	t.Run("script-capable content is forced to download", func(t *testing.T) {
		got := e.do(t, req{Method: http.MethodGet, Path: contentPath(htmlEvidence, string(htmlArtifact.ID))})
		if got.Status != http.StatusOK {
			t.Fatalf("status = %d body = %s, want 200", got.Status, truncate(got.Body))
		}
		if disposition := got.Header.Get("Content-Disposition"); !strings.HasPrefix(disposition, "attachment;") {
			t.Errorf("Content-Disposition = %q, want an attachment — text/html must never render inline from this origin", disposition)
		}
		if nosniff := got.Header.Get("X-Content-Type-Options"); nosniff != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want %q", nosniff, "nosniff")
		}
	})

	t.Run("inline-safe content keeps its own type", func(t *testing.T) {
		got := e.do(t, req{Method: http.MethodGet, Path: contentPath(e.alpha.EvidenceID, e.alpha.ArtifactID)})
		if got.Status != http.StatusOK {
			t.Fatalf("status = %d body = %s, want 200", got.Status, truncate(got.Body))
		}
		if ct := got.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("Content-Type = %q, want text/plain", ct)
		}
		if disposition := got.Header.Get("Content-Disposition"); !strings.HasPrefix(disposition, "inline;") {
			t.Errorf("Content-Disposition = %q, want inline for an allow-listed type", disposition)
		}
		if got.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("X-Content-Type-Options missing on an inline response")
		}
		if got.Body != "artifact body for alpha" {
			t.Errorf("body = %q, want the exact stored bytes", got.Body)
		}
	})

	t.Run("an unsatisfiable Range is refused", func(t *testing.T) {
		got := e.do(t, req{
			Method: http.MethodGet, Path: contentPath(e.alpha.EvidenceID, e.alpha.ArtifactID),
			Headers: map[string]string{"Range": "bytes=999999-1000000"},
		})
		if got.Status != http.StatusRequestedRangeNotSatisfiable {
			t.Errorf("status = %d, want 416", got.Status)
		}
	})

	t.Run("an artifact of another project's evidence is hidden", func(t *testing.T) {
		got := e.do(t, req{Method: http.MethodGet, Path: contentPath(e.alpha.EvidenceID, e.beta.ArtifactID)})
		if got.Status != http.StatusNotFound || strings.TrimSpace(got.Body) != hiddenResourceBody {
			t.Errorf("status = %d body = %s, want the leakage-normalized 404 — an Artifact is only reachable through the "+
				"Evidence row that actually references it", got.Status, truncate(got.Body))
		}
	})

	t.Run("no response header can be injected through stored metadata", func(t *testing.T) {
		got := e.do(t, req{Method: http.MethodGet, Path: contentPath(e.alpha.EvidenceID, e.alpha.ArtifactID)})
		for name := range got.Header {
			if strings.ContainsAny(name, "\r\n") {
				t.Errorf("response carries a header name containing CR/LF: %q", name)
			}
			for _, value := range got.Header[name] {
				if strings.ContainsAny(value, "\r\n") {
					t.Errorf("header %s carries a CR/LF in its value: %q", name, value)
				}
			}
		}
	})
}

// TestOversizedBodyIsRefusedBeforeAnyHandlerRuns proves httpapi.MaxBytes'
// own server-wide ceiling (Config.MaxBodyBytes, 1 MiB here) bounds every
// mutating route regardless of what its own handler would have done — a
// caller cannot make this process read an unbounded request into memory.
func TestOversizedBodyIsRefusedBeforeAnyHandlerRuns(t *testing.T) {
	e := newEnv(t)
	oversized := `{"title":"` + strings.Repeat("A", 2<<20) + `"}`
	got := e.do(t, req{
		Method: http.MethodPost, Path: "/projects/" + e.alpha.ProjectID + "/work-items",
		Body: oversized, IdempotencyKey: "sm-oversized-body",
	})
	if got.Status != http.StatusRequestEntityTooLarge && got.Status != http.StatusBadRequest {
		t.Errorf("status = %d body = %s, want 413 (or 400) for a body past the server-wide limit", got.Status, truncate(got.Body))
	}
	assertNoWorkItemTitled(t, e, strings.Repeat("A", 2<<20))
}

// assertNoWorkItemTitled proves the oversized request above committed
// nothing, by reading the real WorkItem table through a read-only ports.Tx.
func assertNoWorkItemTitled(t *testing.T, e *env, title string) {
	t.Helper()
	ctx := t.Context()
	if err := e.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		items, err := tx.Work().ListWorkItemsByProject(ctx, e.alpha.ProjectID)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Title == title {
				t.Errorf("an oversized request still created a WorkItem")
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("list work items: %v", err)
	}
}
