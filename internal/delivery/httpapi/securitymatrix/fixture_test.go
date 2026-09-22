package securitymatrix

// Every fixture helper in this file composes REAL production code paths: a
// real *sqlite.Store, a real filesystem internal/adapters/artifactstore, a
// real internal/delivery/httpcompose.ComposeRoutes call (the identical one
// cmd/aw/serve.go makes at real startup), a real httpapi.Server bound to a
// real loopback TCP listener behind the real middleware chain, and real
// application commands (catalog.RegisterRepository, work.CreateRootWorkItem,
// work.CreateReleaseSet, workspaceprovision.Handler, artifact.PrepareAttachment)
// for every seeded row. Where a row has no public command that can create it
// yet, it is built through the same low-level ports.Tx accessors this repo's
// own already-merged fixtures use for exactly that row
// (internal/delivery/httpapi/evidence/fixture_test.go's seedExecutionAttempt/
// seedEvidence, internal/delivery/httpapi/run/fixture_test.go's
// readyWorkItemFixture) — never a hand-written INSERT, never a mock
// UnitOfWork.

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	appartifact "github.com/taQuangLing/agent-workflow/internal/app/artifact"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	appworkspaceinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacestate"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpcompose"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const (
	// sessionToken is this fixture server's own per-start session token —
	// the exact value a mutating request must carry in
	// httpapi.SessionTokenHeader (ADR-016). It lives only in this test
	// process's memory, exactly like the real one NewServer binds.
	sessionToken = "securitymatrix-per-start-session-token"

	// alpha/beta are the two real, separately-owned projects every
	// cross-project ownership case in this suite pairs against each other.
	alphaProject = "project-alpha"
	betaProject  = "project-beta"

	// fabricatedID is the well-formed-but-never-issued identifier every
	// generic negative case substitutes into a route's own {param}
	// placeholders. It is deliberately shaped like a plausible ID (no
	// characters ServeMux or a validator would reject on sight) so a route
	// that rejects it is genuinely rejecting an UNKNOWN id, not merely a
	// malformed string.
	fabricatedID = "sm-fabricated-identifier-0001"

	// fixtureVCSObjectID is a well-formed 40-hex commit id, the same shape
	// internal/delivery/httpapi/run/fixture_test.go already uses.
	fixtureVCSObjectID = "cafebabecafebabecafebabecafebabecafebabe"
)

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

// projectFixture is every real, seeded identifier belonging to ONE project —
// one of each entity kind V6-13's own "Thực hiện" line names (Run, WorkItem,
// blocker, workspace, ReleaseSet, artifact) plus the Evidence row an
// artifact is only reachable through and the TaskFamily/WorkspaceSet that
// own them.
type projectFixture struct {
	ProjectID             string
	RepositoryID          string
	WorkItemID            string
	FamilyID              string
	WorkspaceSetID        string
	RepositoryWorkspaceID string
	RunID                 string
	NodeRunID             string
	AttemptID             string
	EvidenceID            string
	ArtifactID            string
	ReleaseSetID          string
	BlockerID             string
}

// env is one fully-composed production server plus the real infrastructure
// behind it, torn down via t.Cleanup.
type env struct {
	server        *httpapi.Server
	routes        *httpapi.RouteRegistry
	base          string // "http://127.0.0.1:PORT"
	host          string // "127.0.0.1:PORT" — the ONLY Host header this server accepts
	origin        string // "http://127.0.0.1:PORT" — the ONLY Origin it accepts
	client        *http.Client
	uow           ports.UnitOfWork
	artifactStore ports.ArtifactStore
	// deps is the exact httpcompose.Dependencies set this env's own server
	// was composed from, kept so forkAs can build a SECOND server over the
	// identical real infrastructure (same database, same artifact store,
	// same everything) differing only in its bound LocalPrincipalSnapshot —
	// the only way to observe a principal change, since ADR-028 makes the
	// principal immutable for a process's whole lifetime.
	deps httpcompose.Dependencies
	// shutdownCancel cancels the context httpcompose passes to the
	// eventstream package as Dependencies.Shutdown — the SAME context a
	// real composition root (cmd/aw/serve.go) cancels on SIGINT/SIGTERM
	// BEFORE calling Server.Shutdown, so every open SSE stream closes
	// itself first. Calling Server.Shutdown without cancelling this would
	// block on an unbounded streaming response, which is precisely why
	// production does it in this order.
	shutdownCancel context.CancelFunc
	alpha          projectFixture
	beta           projectFixture
}

// forkAs starts a SECOND real server over this env's own identical
// infrastructure under a different principal — the in-test stand-in for
// "the operator edited their trusted principal config and restarted `aw
// serve`". Used to prove that a receipt committed by one actor can never be
// replayed by another (replay_test.go).
func (e *env) forkAs(t *testing.T, principal httpapi.LocalPrincipalSnapshot) *env {
	t.Helper()
	routes := httpapi.NewRouteRegistry()
	httpcompose.ComposeRoutes(routes, e.deps)
	server, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: routes, IDs: idsource.Random{},
		Logger:       logging.New(io.Discard, logging.JSON, e.deps.Matcher),
		MaxBodyBytes: 1 << 20, Token: sessionToken, Principal: principal,
	})
	if err != nil {
		t.Fatalf("fork httpapi.NewServer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Errorf("fork Shutdown: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("fork Serve returned error after Shutdown: %v", err)
		}
	})
	forked := *e
	forked.server = server
	forked.routes = routes
	forked.base = "http://" + server.Addr()
	forked.host = server.Addr()
	forked.origin = "http://" + server.Addr()
	return &forked
}

// newEnv builds the real, fully-composed server. Every dependency mirrors
// internal/delivery/httpapi/apicontract/composetest_test.go's own
// buildRealRegistry (itself mirroring cmd/aw/serve.go) — the difference is
// that this one also STARTS the server behind the real middleware chain, so
// every assertion in this package is made against a real HTTP round trip
// rather than against a descriptor list.
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "securitymatrix.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	artifactRoot := t.TempDir()
	artifactStoreImpl, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}

	workspaceProvider, err := gitworktree.New(gitworktree.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	inspectionQueries := appworkspaceinspection.New(uow, workspaceProvider)

	matcher := redact.NewMatcher("securitymatrix-known-secret")
	cursorCodec := httpapi.NewCursorCodec([]byte("securitymatrix-cursor-secret-value"))

	agents, err := agentregistry.New(ctx)
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}

	appConfig := config.Defaults()
	appConfig.DatabasePath = filepath.Join(t.TempDir(), "securitymatrix.db")
	appConfig.ArtifactRoot = artifactRoot
	appConfig.WorkerID = "securitymatrix-test"
	appConfig.ProviderExecutables = map[string]string{}

	// The SIGINT/SIGTERM stand-in every open SSE stream watches — see
	// env.shutdownCancel.
	streamShutdownCtx, streamShutdownCancel := context.WithCancel(ctx)
	t.Cleanup(streamShutdownCancel)

	safeSettingsAtBoot, _ := safesettingsapp.GetSafeSettings(ctx, uow)
	effective := safesettingsapp.ResolveEffective(
		safesettingsapp.Defaults(), safesettingsapp.StartupOverrides{}, safeSettingsAtBoot.Desired,
		safesettingsapp.StartupOverrides{}, safesettingsapp.StartupOverrides{},
	)

	deps := httpcompose.Dependencies{
		UnitOfWork:                 uow,
		ArtifactStore:              artifactStoreImpl,
		WorkspaceInspectionQueries: inspectionQueries,
		Matcher:                    matcher,
		Cursor:                     cursorCodec,
		Isolation:                  process.NewIsolationChecker(),
		Agents:                     agents,
		AppConfig:                  appConfig,
		Store:                      sqlite.NewQueryStore(store),
		SafeSettingsEffective:      effective,
		Shutdown:                   streamShutdownCtx,
		LiveHandler:                healthPlaceholder,
		ReadyHandler:               healthPlaceholder,
		BootstrapHandler:           bootstrapPlaceholder,
	}
	routes := httpapi.NewRouteRegistry()
	httpcompose.ComposeRoutes(routes, deps)

	server, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: routes, IDs: idsource.Random{},
		Logger:       logging.New(io.Discard, logging.JSON, matcher),
		MaxBodyBytes: 1 << 20, Token: sessionToken, Principal: testPrincipal(),
	})
	if err != nil {
		t.Fatalf("httpapi.NewServer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		streamShutdownCancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve returned error after Shutdown: %v", err)
		}
	})

	e := &env{
		server: server, routes: routes,
		base:   "http://" + server.Addr(),
		host:   server.Addr(),
		origin: "http://" + server.Addr(),
		// Redirects are never followed: net/http.ServeMux answers an
		// un-normalized path with a 301 of its own, and this suite must
		// observe that raw 301 (injection_test.go) rather than silently
		// chase it to whatever it points at.
		client: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		uow: uow, artifactStore: artifactStoreImpl, deps: deps, shutdownCancel: streamShutdownCancel,
	}
	e.alpha = e.seedProjectFixture(t, alphaProject, "alpha")
	e.beta = e.seedProjectFixture(t, betaProject, "beta")
	return e
}

// healthPlaceholder/bootstrapPlaceholder stand in for the real
// health/bootstrap handlers a composition root builds from its own
// ReadinessChecker/session token/principal — see
// internal/delivery/httpcompose.Dependencies.LiveHandler's own doc comment
// for why a caller that does not exercise those three specific routes'
// BODIES may pass a placeholder. Their STATUS still matters to this suite:
// every transport-layer assertion here is about a request being rejected
// BEFORE it ever reaches a handler, so a placeholder that answers 204/200
// is exactly the "the guard did not stop this" signal the matrix needs.
func healthPlaceholder(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

func bootstrapPlaceholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<!doctype html><title>bootstrap placeholder</title>"))
}

// ---------------------------------------------------------------------------
// Request helpers
// ---------------------------------------------------------------------------

// tokenMode selects what httpapi.SessionTokenHeader a request carries.
type tokenMode int

const (
	tokenValid   tokenMode = iota // this server's own real per-start token
	tokenMissing                  // header absent entirely
	tokenWrong                    // header present with a token this server never issued
)

// req is one fully-specified HTTP request against e's own real server.
// Every field's zero value is the "nothing unusual" case: the real bound
// Host, no Origin header (a non-browser client), the real session token and
// no body.
type req struct {
	Method string
	Path   string
	Body   string
	// Host overrides the Host header; "" means e.host (the valid one).
	Host string
	// Origin sets an Origin header; "" means send none.
	Origin string
	Token  tokenMode
	// IdempotencyKey sets httpapi's own Idempotency-Key header when
	// non-empty.
	IdempotencyKey string
	Headers        map[string]string
}

// resp is the small, comparable view of a response this suite asserts on —
// deliberately including the raw body bytes, because leakage normalization
// is a claim about BYTES being identical, not merely about status codes
// matching.
type resp struct {
	Status int
	Body   string
	Header http.Header
}

func (e *env) do(t *testing.T, r req) resp {
	t.Helper()
	var body io.Reader
	if r.Body != "" {
		body = strings.NewReader(r.Body)
	}
	httpReq, err := http.NewRequest(r.Method, e.base+r.Path, body)
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", r.Method, r.Path, err)
	}
	// req.Host (not the Host header map entry) is what net/http actually
	// puts on the wire — setting the header map alone would be silently
	// ignored, which would make every Host assertion in this suite a
	// tautology.
	if r.Host != "" {
		httpReq.Host = r.Host
	} else {
		httpReq.Host = e.host
	}
	if r.Origin != "" {
		httpReq.Header.Set("Origin", r.Origin)
	}
	switch r.Token {
	case tokenValid:
		httpReq.Header.Set(httpapi.SessionTokenHeader, sessionToken)
	case tokenMissing:
		// deliberately no header at all
	case tokenWrong:
		httpReq.Header.Set(httpapi.SessionTokenHeader, "not-this-servers-token")
	}
	if r.IdempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", r.IdempotencyKey)
	}
	if r.Body != "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.Headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := e.client.Do(httpReq)
	if err != nil {
		t.Fatalf("%s %s: %v", r.Method, r.Path, err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		t.Fatalf("read body of %s %s: %v", r.Method, r.Path, err)
	}
	return resp{Status: httpResp.StatusCode, Body: string(raw), Header: httpResp.Header.Clone()}
}

// rawRequest writes requestLine plus headers to the server over a raw TCP
// connection, bypassing net/http's own client-side URL parsing and path
// normalization entirely — the only way to prove what this server does with
// a request-target a well-behaved client would never construct (see
// injection_test.go).
func (e *env) rawRequest(t *testing.T, requestLine string, extraHeaders ...string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", e.host, 10*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", e.host, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	var b strings.Builder
	b.WriteString(requestLine + "\r\n")
	b.WriteString("Host: " + e.host + "\r\n")
	b.WriteString(httpapi.SessionTokenHeader + ": " + sessionToken + "\r\n")
	for _, h := range extraHeaders {
		b.WriteString(h + "\r\n")
	}
	b.WriteString("Connection: close\r\n\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		t.Fatalf("write raw request: %v", err)
	}
	out, err := io.ReadAll(conn)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read raw response: %v", err)
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// Seeding
// ---------------------------------------------------------------------------

func (e *env) seedProjectFixture(t *testing.T, projectID, suffix string) projectFixture {
	t.Helper()
	ctx := context.Background()
	ids := idsource.Random{}

	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + suffix})
		return err
	}); err != nil {
		t.Fatalf("CreateProject(%s): %v", projectID, err)
	}

	repositoryID := "repo-" + suffix
	regCmd := seedCommand(projectID, "reg-"+suffix, "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, e.uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID,
		RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}

	root, err := workapp.CreateRootWorkItem(ctx, e.uow, ids, seedCommand(projectID, "root-"+suffix, "CreateRootWorkItem"),
		workapp.CreateRootWorkItemRequest{
			ProjectID: projectID, Title: "Task " + suffix,
			InitialScope: []workapp.ScopeGrantRequest{{
				RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
				PathScopes: []string{"**"}, Reason: "seed " + suffix,
			}},
		})
	if err != nil {
		t.Fatalf("CreateRootWorkItem(%s): %v", suffix, err)
	}

	// Drive the WorkspaceSet to READY through the REAL workspaceprovision
	// handler so a real RepositoryWorkspace row exists to address —
	// mirroring internal/delivery/httpapi/run/fixture_test.go's own
	// readyWorkItemFixture, including its scripted ports.WorkspaceProvider
	// (no real git I/O: this suite asserts on HTTP authority boundaries, and
	// a real worktree would add process/filesystem dependencies with no
	// bearing on any of them).
	provider := &scriptedProvider{
		handle:   mustHandle(t, "handle-"+suffix),
		revision: workspace.Revision{RepositoryID: project.RepositoryID(repositoryID), VCSObjectID: fixtureVCSObjectID, WorkspaceGeneration: 1},
	}
	provisionPayload := []byte(`{"workItemId":"` + root.WorkItemID + `","projectId":"` + projectID +
		`","familyId":"` + root.FamilyID + `","workspaceSetId":"` + root.WorkspaceSetID +
		`","repositoryId":"` + repositoryID + `"}`)
	if err := workspaceprovision.New(e.uow, ids, provider).Handle(ctx, ports.DurableJob{
		ID: ports.JobID(root.ProvisionedRepositories[0].ProvisionJobID), Kind: workapp.WorkspaceProvisionJobKind, Payload: provisionPayload,
	}); err != nil {
		t.Fatalf("workspaceprovision.Handle(%s): %v", suffix, err)
	}

	setState, err := workspacestate.GetWorkspaceSetState(ctx, e.uow, workspacestate.GetWorkspaceSetStateRequest{
		ProjectID: projectID, FamilyID: root.FamilyID,
	})
	if err != nil {
		t.Fatalf("GetWorkspaceSetState(%s): %v", suffix, err)
	}
	if len(setState.RepositoryWorkspaces) == 0 {
		t.Fatalf("seed %s: workspace set has no RepositoryWorkspace rows", suffix)
	}
	repositoryWorkspaceID := setState.RepositoryWorkspaces[0].RepositoryWorkspaceID

	attempt := e.seedExecutionAttempt(t, projectID, root.WorkItemID, root.FamilyID, suffix)

	art := e.seedArtifact(t, projectID, "text/plain", "artifact body for "+suffix)
	evidenceID := e.seedEvidence(t, projectID, root.WorkItemID, attempt, suffix, string(art.ID))

	releaseSet, err := workapp.CreateReleaseSet(ctx, e.uow, ids, seedCommand(projectID, "release-"+suffix, "CreateReleaseSet"),
		workapp.CreateReleaseSetRequest{
			ProjectID: projectID, FamilyID: root.FamilyID,
			Repositories: []workapp.RepositoryReleaseRequest{{
				RepositoryID: repositoryID, BaseVCSObjectID: fixtureVCSObjectID,
				ResultVCSObjectID: fixtureVCSObjectID, Verdict: "PASS",
			}},
		})
	if err != nil {
		t.Fatalf("CreateReleaseSet(%s): %v", suffix, err)
	}

	blockerID := e.seedBlocker(t, projectID, root.WorkItemID, suffix)

	return projectFixture{
		ProjectID: projectID, RepositoryID: repositoryID,
		WorkItemID: root.WorkItemID, FamilyID: root.FamilyID, WorkspaceSetID: root.WorkspaceSetID,
		RepositoryWorkspaceID: repositoryWorkspaceID,
		RunID:                 attempt.RunID, NodeRunID: attempt.NodeRunID, AttemptID: attempt.AttemptID,
		EvidenceID: evidenceID, ArtifactID: string(art.ID), ReleaseSetID: releaseSet.ReleaseSetID, BlockerID: blockerID,
	}
}

func seedCommand(projectID, suffix, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + suffix, IdempotencyKey: "idem-" + suffix, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: commandType, RequestHash: "hash-" + suffix,
	}
}

// attemptFixture is the WorkflowRun -> NodeRun -> ExecutionAttempt identity
// triple every Evidence row's own FOREIGN KEY columns require.
type attemptFixture struct {
	RunID     string
	NodeRunID string
	AttemptID string
}

// seedExecutionAttempt mirrors
// internal/delivery/httpapi/evidence/fixture_test.go's own identical helper
// exactly (a minimal, non-dispatchable Run chain built through the real
// CreateWorkflowRun/CreateNodeRun/CreateExecutionAttempt Tx methods): this
// suite needs a Run that genuinely resolves to a specific Project/WorkItem,
// not a schedulable one.
func (e *env) seedExecutionAttempt(t *testing.T, projectID, workItemID, familyID, suffix string) attemptFixture {
	t.Helper()
	ctx := context.Background()
	pid := project.ProjectID(projectID)
	definition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID("wf-def-" + suffix), ProjectID: &pid,
		Name: "workflow " + suffix, Status: workflow.DefinitionActive, Version: 1,
	}
	version, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID("wf-ver-" + suffix), VersionNumber: 1,
		Document: workflow.WorkflowDocument{
			SchemaVersion: "1",
			Nodes: []workflow.Node{
				{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
				{Key: "end", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{{Key: "start-to-end", From: "start", Outcome: "next", To: "end"}},
		},
		PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("workflow.Compile(%s): %v", suffix, err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Definitions().PublishWorkflowVersion(ctx, definition, version)
		return err
	}); err != nil {
		t.Fatalf("PublishWorkflowVersion(%s): %v", suffix, err)
	}

	runID := runtimedomain.WorkflowRunID("wf-run-" + suffix)
	run, err := runtimedomain.NewWorkflowRun(runID, pid, workdomain.WorkItemID(workItemID), version, workdomain.TaskFamilyID(familyID), 1, []byte(`{}`))
	if err != nil {
		t.Fatalf("NewWorkflowRun(%s): %v", suffix, err)
	}
	nodeRunID := runtimedomain.NodeRunID("node-run-" + suffix)
	nodeRun, err := runtimedomain.NewNodeRun(nodeRunID, runID, "agent", 1, 0, nil, "sha256:input-fixture", "sha256:profile-fixture")
	if err != nil {
		t.Fatalf("NewNodeRun(%s): %v", suffix, err)
	}
	attemptID := "attempt-" + suffix
	attempt, err := runtimedomain.NewExecutionAttempt(runtimedomain.ExecutionAttemptID(attemptID), nodeRunID, 1, "sha256:profile-fixture", "fake-provider", nil)
	if err != nil {
		t.Fatalf("NewExecutionAttempt(%s): %v", suffix, err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Runtime().CreateWorkflowRun(ctx, run); err != nil {
			return err
		}
		if _, err := tx.Runtime().CreateNodeRun(ctx, nodeRun); err != nil {
			return err
		}
		_, err := tx.Runtime().CreateExecutionAttempt(ctx, attempt)
		return err
	}); err != nil {
		t.Fatalf("seed execution attempt(%s): %v", suffix, err)
	}
	return attemptFixture{RunID: string(runID), NodeRunID: string(nodeRunID), AttemptID: attemptID}
}

// seedArtifact stores body through the REAL
// internal/app/artifact.PrepareAttachment (durable Put + hash Verify outside
// any transaction) and attaches it via the real tx.Artifacts().InsertArtifact
// — the identical two-step sequence every production attach caller uses.
func (e *env) seedArtifact(t *testing.T, projectID, contentType, body string) artifact.Artifact {
	t.Helper()
	ctx := context.Background()
	a, err := appartifact.PrepareAttachment(ctx, e.artifactStore, idsource.Random{}, clock.System{}, appartifact.PrepareAttachmentRequest{
		ProjectID: projectID, Body: strings.NewReader(body), ContentType: contentType,
		Sensitivity: redact.Public, Redacted: false, RetentionClass: artifact.RetentionRawOutputTemp,
	})
	if err != nil {
		t.Fatalf("PrepareAttachment(%s): %v", projectID, err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, a)
		return err
	}); err != nil {
		t.Fatalf("InsertArtifact(%s): %v", projectID, err)
	}
	return a
}

// seedEvidence persists one real runtimedomain.Evidence row referencing
// artifactID via the real tx.Runtime().CreateEvidence — mirrors
// internal/delivery/httpapi/evidence/fixture_test.go's own identical helper.
func (e *env) seedEvidence(t *testing.T, projectID, workItemID string, attempt attemptFixture, suffix, artifactID string) string {
	t.Helper()
	ctx := context.Background()
	revisions, err := workspace.NewRevisionSet([]workspace.Revision{
		{RepositoryID: project.RepositoryID("repo-" + suffix), VCSObjectID: fixtureVCSObjectID, WorkspaceGeneration: 1},
	})
	if err != nil {
		t.Fatalf("NewRevisionSet(%s): %v", suffix, err)
	}
	evidenceID := runtimedomain.EvidenceID("evidence-" + suffix)
	evidence, err := runtimedomain.NewEvidence(
		evidenceID, project.ProjectID(projectID), workdomain.WorkItemID(workItemID),
		runtimedomain.WorkflowRunID(attempt.RunID), runtimedomain.NodeRunID(attempt.NodeRunID),
		runtimedomain.ExecutionAttemptID(attempt.AttemptID), "GATE", "PASS",
		[]string{artifactID}, revisions, "policy-v1", time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewEvidence(%s): %v", suffix, err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateEvidence(ctx, evidence)
		return err
	}); err != nil {
		t.Fatalf("CreateEvidence(%s): %v", suffix, err)
	}
	return string(evidenceID)
}

// seedBlocker persists one OPEN WorkItemBlocker through the real
// tx.Work().CreateWorkItemBlocker — the same accessor
// internal/app/work's own createWorkItemBlockerTx uses in production.
func (e *env) seedBlocker(t *testing.T, projectID, workItemID, suffix string) string {
	t.Helper()
	ctx := context.Background()
	blocker, err := workdomain.NewWorkItemBlocker(
		workdomain.BlockerID("blocker-"+suffix), project.ProjectID(projectID), workdomain.WorkItemID(workItemID),
		workdomain.BlockerScopeExpansionRequired, "", "", "", "seeded blocker for "+suffix, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewWorkItemBlocker(%s): %v", suffix, err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().CreateWorkItemBlocker(ctx, blocker)
		return err
	}); err != nil {
		t.Fatalf("CreateWorkItemBlocker(%s): %v", suffix, err)
	}
	return "blocker-" + suffix
}

// scriptedProvider is the same non-I/O ports.WorkspaceProvider double
// internal/delivery/httpapi/run/fixture_test.go and internal/app/runtime's
// own tests already use to drive a WorkspaceSet to READY without touching a
// real git worktree. Every method this suite's fixtures never legitimately
// reach returns an explicit error rather than a zero value, so an
// accidental call fails loudly instead of silently succeeding.
type scriptedProvider struct {
	handle   ports.WorkspaceHandle
	revision workspace.Revision
}

func (s *scriptedProvider) Provision(context.Context, ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	return s.handle, nil
}

func (s *scriptedProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, errors.New("scriptedProvider: Inspect must not be called")
}

func (s *scriptedProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	return s.revision, nil
}

func (s *scriptedProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return ports.WorkspaceDiff{}, errors.New("scriptedProvider: Diff must not be called")
}

func (s *scriptedProvider) Release(context.Context, ports.WorkspaceHandle) error {
	return errors.New("scriptedProvider: Release must not be called")
}

func (s *scriptedProvider) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return "", errors.New("scriptedProvider: WorkingDirectory must not be called")
}

func mustHandle(t *testing.T, token string) ports.WorkspaceHandle {
	t.Helper()
	h, err := ports.NewWorkspaceHandle(token)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle(%s): %v", token, err)
	}
	return h
}
