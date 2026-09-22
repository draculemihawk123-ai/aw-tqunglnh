package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	appworkspaceinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/clicompose"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpcompose"
)

// liveInstall is one real, empty installation: a SQLite database, artifact
// and workspace roots, served by a REAL httpapi.Server (the same middleware
// chain `aw serve` builds: Host/Origin guard, session token, principal
// binding) and reachable from the CLI through clicompose against the SAME
// database file. HTTP and CLI therefore share one receipt authority, one
// principal source and one set of application commands — the property the
// equivalence tests exist to prove from the outside.
type liveInstall struct {
	t            *testing.T
	dbPath       string
	artifactRoot string
	workRoot     string
	token        string
	principal    httpapi.LocalPrincipalSnapshot
	server       *httpapi.Server
	client       *http.Client
}

const testToken = "parity-session-token-0123456789"

func newLiveInstall(t *testing.T, principal httpapi.LocalPrincipalSnapshot) *liveInstall {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	in := &liveInstall{
		t: t, dbPath: filepath.Join(dir, "aw.db"), artifactRoot: filepath.Join(dir, "artifacts"),
		workRoot: filepath.Join(dir, "workspaces"), token: testToken, principal: principal,
		client: &http.Client{Timeout: 30 * time.Second},
	}
	if err := os.MkdirAll(in.artifactRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.Open(ctx, in.dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	artifacts, err := artifactstore.New(in.artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := gitworktree.New(gitworktree.Config{Root: in.workRoot})
	if err != nil {
		t.Fatal(err)
	}
	agents, err := agentregistry.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	appConfig := config.Defaults()
	appConfig.DatabasePath, appConfig.ArtifactRoot, appConfig.WorkerID = in.dbPath, in.artifactRoot, "parity"
	appConfig.ProviderExecutables = map[string]string{}
	atBoot, _ := safesettingsapp.GetSafeSettings(ctx, uow)
	effective := safesettingsapp.ResolveEffective(safesettingsapp.Defaults(), safesettingsapp.StartupOverrides{}, atBoot.Desired,
		safesettingsapp.StartupOverrides{}, safesettingsapp.StartupOverrides{})

	routes := httpapi.NewRouteRegistry()
	live := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }
	httpcompose.ComposeRoutes(routes, httpcompose.Dependencies{
		UnitOfWork: uow, ArtifactStore: artifacts, WorkspaceInspectionQueries: appworkspaceinspection.New(uow, provider),
		Matcher: redact.NewMatcher(), Cursor: httpapi.NewCursorCodec([]byte("parity-cursor-secret-value")),
		Isolation: process.NewIsolationChecker(), Agents: agents, AppConfig: appConfig,
		Store: sqlite.NewQueryStore(store), SafeSettingsEffective: effective, Shutdown: ctx,
		LiveHandler: live, ReadyHandler: live, BootstrapHandler: live,
	})
	srv, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: routes, IDs: idsource.Random{}, MaxBodyBytes: 1 << 20,
		Token: in.token, Principal: principal,
	})
	if err != nil {
		t.Fatalf("httpapi.NewServer: %v", err)
	}
	in.server = srv
	go srv.Serve()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	})
	return in
}

// httpResult is one HTTP response reduced to what the comparison needs.
type httpResult struct {
	Status  int
	Body    []byte
	ETag    string
	Replay  string
	Headers http.Header
}

func (in *liveInstall) do(method, path string, headers map[string]string, body string) httpResult {
	in.t.Helper()
	req, err := http.NewRequest(method, "http://"+in.server.Addr()+path, strings.NewReader(body))
	if err != nil {
		in.t.Fatal(err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, in.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := in.client.Do(req)
	if err != nil {
		in.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return httpResult{Status: resp.StatusCode, Body: data, ETag: resp.Header.Get("ETag"), Headers: resp.Header}
}

// principalFile writes a trusted principal config the CLI selects with
// --principal-config — the CLI twin of the HTTP server's bound
// LocalPrincipalSnapshot.
func (in *liveInstall) principalFile() string {
	in.t.Helper()
	encoded, err := json.Marshal(map[string]any{"localPrincipal": map[string]any{"actor": in.principal.Actor, "roles": in.principal.Roles}})
	if err != nil {
		in.t.Fatal(err)
	}
	path := filepath.Join(in.t.TempDir(), "principal.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		in.t.Fatal(err)
	}
	return path
}

// cliFactory builds clicompose.Deps for THIS installation's database — the
// test-side twin of cmd/aw's oneShotFactory, restricted to what the
// equivalence cases need.
func (in *liveInstall) cliFactory(ctx context.Context, opts clicompose.Options, needs clicompose.Needs) (clicompose.Deps, func(), error) {
	store, err := sqlite.Open(ctx, in.dbPath)
	if err != nil {
		return clicompose.Deps{}, nil, err
	}
	deps := clicompose.Deps{UoW: sqlite.NewUnitOfWork(store), Store: sqlite.NewQueryStore(store), Matcher: redact.NewMatcher(), ArtifactRoot: in.artifactRoot}
	if needs&clicompose.NeedArtifactStore != 0 {
		if deps.ArtifactStore, err = artifactstore.New(in.artifactRoot); err != nil {
			store.Close()
			return clicompose.Deps{}, nil, err
		}
	}
	if needs&clicompose.NeedIsolation != 0 {
		deps.Isolation = process.NewIsolationChecker()
	}
	if needs&clicompose.NeedAgents != 0 {
		if deps.Agents, err = agentregistry.New(ctx); err != nil {
			store.Close()
			return clicompose.Deps{}, nil, err
		}
	}
	if needs&clicompose.NeedConfig != 0 {
		appConfig := config.Defaults()
		appConfig.DatabasePath, appConfig.ArtifactRoot, appConfig.WorkerID = in.dbPath, in.artifactRoot, "parity-cli"
		appConfig.ProviderExecutables = map[string]string{}
		deps.Config = appConfig
	}
	return deps, func() { store.Close() }, nil
}

// cliResult is one CLI invocation.
type cliResult struct {
	Code   cli.ExitCode
	Stdout string
	Stderr string
}

func (in *liveInstall) cli(stdin string, args ...string) cliResult {
	in.t.Helper()
	var stdout, stderr bytes.Buffer
	code := clicompose.Execute(context.Background(), in.cliFactory, nil, args, clicompose.IO{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
	})
	return cliResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// ---- normalization --------------------------------------------------------

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	timePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
)

// normalizer maps a decoded JSON document to a comparable canonical form:
// generated identifiers (UUIDs) become <id#n> numbered by first appearance
// within ONE document (so two installations that generate different random
// ids still compare equal when their id RELATIONSHIPS are identical),
// timestamps become <time>, and every key in dropKeys (transport-only
// fields the other side does not carry) is removed at any depth.
type normalizer struct {
	ids      map[string]int
	dropKeys map[string]bool
}

func newNormalizer(dropKeys ...string) *normalizer {
	n := &normalizer{ids: map[string]int{}, dropKeys: map[string]bool{}}
	for _, k := range dropKeys {
		n.dropKeys[k] = true
	}
	return n
}

func (n *normalizer) walk(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, child := range x {
			if n.dropKeys[k] {
				continue
			}
			out[k] = n.walk(child)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = n.walk(child)
		}
		return out
	case string:
		switch {
		case uuidPattern.MatchString(x):
			if _, seen := n.ids[x]; !seen {
				n.ids[x] = len(n.ids) + 1
			}
			return "<id#" + strconv.Itoa(n.ids[x]) + ">"
		case timePattern.MatchString(x):
			return "<time>"
		}
		return x
	default:
		return v
	}
}

func canonical(t *testing.T, raw []byte, dropKeys ...string) string {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	encoded, err := json.MarshalIndent(newNormalizer(dropKeys...).walk(decoded), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// jsonField extracts a top-level field of a JSON object.
func jsonField(t *testing.T, raw []byte, field string) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	value, ok := obj[field]
	if !ok {
		t.Fatalf("no %q field in %s", field, raw)
	}
	return value
}
