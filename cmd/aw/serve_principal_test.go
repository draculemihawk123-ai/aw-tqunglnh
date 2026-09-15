package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// startServeForTest starts serve() in the background with a real SQLite
// database and artifact root, waits for it to announce its bound address,
// and returns that address, the db path, and a stop func. It mirrors
// TestServe_StartsServesHealthAndShutsDownGracefully's own setup. stop is
// safe to call at most meaningfully once (guarded by sync.Once) and is also
// registered via t.Cleanup, so a test that needs the server fully shut down
// before inspecting its SQLite file directly (no live process holding its
// own connections open concurrently) can call stop() itself early, and
// tests that don't care can just let cleanup handle it.
func startServeForTest(t *testing.T, extraArgs ...string) (addr, dbPath string, stop func()) {
	t.Helper()
	dbPath = serveTestDB(t)
	artifactRoot := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	var stdout syncBuffer
	serveDone := make(chan error, 1)
	args := append([]string{
		"--db", dbPath,
		"--artifact-root", artifactRoot,
		"--workspace-root", t.TempDir(),
		"--host", "127.0.0.1",
		"--port", "0",
	}, extraArgs...)
	go func() {
		serveDone <- serve(ctx, args, &stdout)
	}()

	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-serveDone
		})
	}
	t.Cleanup(stop)

	return waitForServeAddress(t, &stdout), dbPath, stop
}

// bootstrapPayload mirrors httpapi.bootstrapPayload's JSON shape — the
// window.__AW_BOOTSTRAP__ blob the real bootstrap HTML embeds.
type bootstrapPayload struct {
	Token string   `json:"token"`
	Actor string   `json:"actor"`
	Roles []string `json:"roles"`
}

var bootstrapVarPattern = regexp.MustCompile(`window\.__AW_BOOTSTRAP__=(\{.*?\});`)

// fetchBootstrap performs the exact same real HTTP GET a browser would to
// bootstrap itself, and extracts the embedded token/principal — this is the
// one legitimate channel the token is ever exposed through, so every test
// below that needs to know the token's value gets it this way instead of
// reaching into serve()'s internals.
func fetchBootstrap(t *testing.T, addr string) bootstrapPayload {
	t.Helper()
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if readErr != nil {
			break
		}
	}
	match := bootstrapVarPattern.FindSubmatch(buf)
	if match == nil {
		t.Fatalf("bootstrap response does not contain window.__AW_BOOTSTRAP__=...; body: %s", buf)
	}
	var payload bootstrapPayload
	if err := json.Unmarshal(match[1], &payload); err != nil {
		t.Fatalf("unmarshal bootstrap payload %s: %v", match[1], err)
	}
	if payload.Token == "" {
		t.Fatal("bootstrap payload has an empty token")
	}
	return payload
}

// TestServe_BootstrapUsesDefaultPrincipalWhenNoConfigFlag proves ADR-028's
// safe single-user default applies end-to-end through the real `aw serve`
// composition root when --principal-config is not passed at all.
func TestServe_BootstrapUsesDefaultPrincipalWhenNoConfigFlag(t *testing.T) {
	addr, _, _ := startServeForTest(t)
	payload := fetchBootstrap(t, addr)
	if payload.Actor != "local-operator" {
		t.Fatalf("actor = %q, want local-operator (the ADR-028 default)", payload.Actor)
	}
	if len(payload.Roles) != 1 || payload.Roles[0] != "operator" {
		t.Fatalf("roles = %v, want [operator] (the ADR-028 default)", payload.Roles)
	}
}

// TestServe_BootstrapUsesCustomPrincipalFromConfigFile proves
// --principal-config is the one legitimate "global composition option chọn
// một trusted config file" ADR-028 allows: a real JSON file's own
// localPrincipal.actor/roles ends up bound into the real running server.
func TestServe_BootstrapUsesCustomPrincipalFromConfigFile(t *testing.T) {
	principalPath := filepath.Join(t.TempDir(), "principal.json")
	content := `{"localPrincipal": {"actor": "alice", "roles": ["operator", "auditor"]}}`
	if err := os.WriteFile(principalPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	addr, _, _ := startServeForTest(t, "--principal-config", principalPath)
	payload := fetchBootstrap(t, addr)
	if payload.Actor != "alice" {
		t.Fatalf("actor = %q, want alice", payload.Actor)
	}
	if len(payload.Roles) != 2 || payload.Roles[0] != "operator" || payload.Roles[1] != "auditor" {
		t.Fatalf("roles = %v, want [operator auditor]", payload.Roles)
	}
}

// TestServe_RejectsInvalidPrincipalConfigAtStartup proves a malformed
// principal (e.g. no roles) fails closed at startup rather than silently
// running with a broken/empty AuthorizedRoles set.
func TestServe_RejectsInvalidPrincipalConfigAtStartup(t *testing.T) {
	principalPath := filepath.Join(t.TempDir(), "principal.json")
	content := `{"localPrincipal": {"actor": "alice", "roles": []}}`
	if err := os.WriteFile(principalPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout bytes.Buffer
	err := serve(context.Background(), []string{
		"--db", serveTestDB(t),
		"--artifact-root", t.TempDir(),
		"--workspace-root", t.TempDir(),
		"--principal-config", principalPath,
	}, &stdout)
	if err == nil {
		t.Fatal("serve with an empty roles list in --principal-config should fail at startup")
	}
}

// TestServe_SessionTokenNeverPersistedInSQLite is V6-01A's own end-to-end
// secret-scan: drive real traffic against a real `aw serve` process backed
// by a real SQLite database (the exact production code path, not a
// fabricated row), extract the real per-start token through the one
// legitimate channel (the bootstrap response), then open the same SQLite
// file directly and prove that token substring appears in NO table, NO
// column, anywhere — an explicit assertion, not an absence of a code path
// that would write it.
func TestServe_SessionTokenNeverPersistedInSQLite(t *testing.T) {
	addr, dbPath, stop := startServeForTest(t)

	payload := fetchBootstrap(t, addr)
	token := payload.Token

	client := &http.Client{Timeout: 5 * time.Second}
	for _, path := range []string{"/health/live", "/health/ready", "/"} {
		resp, err := client.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
	}

	// Also attempt a mutating-shaped request carrying the token in the
	// (correct) header — even though no production mutating route exists
	// yet, this proves the token's journey through the middleware chain
	// itself never causes it to be written anywhere durable.
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/does-not-exist", strings.NewReader("{}"))
	req.Header.Set("X-Aw-Session-Token", token)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}

	// Shut the server down fully (closing its own sqlite.Store connection
	// pool) before opening a second, independent connection to scan the
	// file — avoids any WAL-mode concurrent-connection contention with the
	// still-live production connections, and more accurately answers "what
	// did the process leave on disk" rather than "what can a concurrent
	// reader see mid-flight".
	stop()
	assertTokenNotInSQLiteFile(t, dbPath, token)
}

// assertTokenNotInSQLiteFile opens dbPath with a fresh, independent
// connection (not the production sqlite.Store — deliberately, so this test
// exercises the real on-disk bytes production code wrote, not an
// in-process view of them) and scans every user table's every row/column
// for token as a substring.
func assertTokenNotInSQLiteFile(t *testing.T, dbPath, token string) {
	t.Helper()

	// Mirrors internal/adapters/sqlite.Open's own DSN construction (db.go):
	// a Windows absolute path's drive letter (e.g. "C:/...") is not itself a
	// leading "/", which url.URL would otherwise mistake for a URI
	// authority instead of an absolute file path.
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", dbPath, err)
	}
	uriPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" && uriPath[0] != '/' {
		uriPath = "/" + uriPath
	}
	u := &url.URL{Scheme: "file", Path: uriPath}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatalf("open sqlite for scan: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if len(tables) == 0 {
		t.Fatal("no tables found — migration did not run, this test is not exercising anything")
	}

	for _, table := range tables {
		scanTableForToken(t, db, table, token)
	}
}

func scanTableForToken(t *testing.T, db *sql.DB, table, token string) {
	t.Helper()
	// #nosec G201 -- table comes only from sqlite_master, not user input.
	rows, err := db.Query(fmt.Sprintf(`SELECT * FROM "%s"`, table))
	if err != nil {
		t.Fatalf("select * from %s: %v", table, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns for %s: %v", table, err)
	}

	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan row in %s: %v", table, err)
		}
		for i, v := range values {
			var s string
			switch val := v.(type) {
			case []byte:
				s = string(val)
			case string:
				s = val
			default:
				continue
			}
			if strings.Contains(s, token) {
				t.Fatalf("table %s column %s contains the session token verbatim — this must never happen", table, cols[i])
			}
		}
	}
}
