package evidence_test

// Real HTTP round-trip coverage for V6-07B (docs/design/08-v6-api-projections.md):
// every test in this package drives a REAL httpapi.Server (real TCP
// loopback listener, real middleware chain, real session-token/Origin/Host
// guards) backed by a REAL *sqlite.Store and a REAL filesystem
// internal/adapters/artifactstore.Store — never a mock, never a direct row
// fabrication (this repo's own hard rule; mirrors
// internal/delivery/httpapi/message/message_test.go's own newTestEnv idiom).
//
// Every fixture helper below composes REAL production code paths — real
// commands (RegisterRepository/CreateRootWorkItem), real low-level Tx
// accessors this codebase's own existing tests already use to build a
// minimal (non-dispatchable) WorkflowRun/NodeRun/ExecutionAttempt chain
// (internal/delivery/httpapi/message/context_snapshot_test.go's own
// seedFakeExecutionAttempt, duplicated here per this codebase's own
// per-package test-fixture convention rather than a shared test-only
// package), and the real internal/app/artifact.PrepareAttachment +
// tx.Artifacts().InsertArtifact sequence every real attach caller in this
// codebase uses (V5-01's own Done-when bar: durable AND hash-verified
// before any database commit) — never a fabricated Artifact/Evidence row
// bypassing those real code paths.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	appartifact "github.com/taQuangLing/agent-workflow/internal/app/artifact"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	evidencehttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/evidence"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const testSessionToken = "test-evidence-session-token"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

// testEnv is one real Server + real UnitOfWork + real ArtifactStore triple,
// torn down via t.Cleanup.
type testEnv struct {
	server        *httpapi.Server
	base          string
	client        *http.Client
	uow           ports.UnitOfWork
	artifactStore ports.ArtifactStore
	artifactRoot  string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "evidence-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	artifactStore, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}

	reg := httpapi.NewRouteRegistry()
	evidencehttp.RegisterRoutes(reg, evidencehttp.Dependencies{UnitOfWork: uow, ArtifactStore: artifactStore})

	server, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: reg, IDs: idsource.Random{},
		Logger:       logging.New(io.Discard, logging.JSON, redact.NewMatcher()),
		MaxBodyBytes: 16 << 20, Token: testSessionToken, Principal: testPrincipal(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve returned error after Shutdown: %v", err)
		}
	})

	return &testEnv{
		server: server, base: "http://" + server.Addr(), client: &http.Client{Timeout: 30 * time.Second},
		uow: uow, artifactStore: artifactStore, artifactRoot: artifactRoot,
	}
}

// artifactObjectPath computes the real on-disk path locator resolves to
// under this env's own artifact root — mirrors
// internal/adapters/artifactstore/filesystem.go's own objectPath scheme
// exactly (git-style two-level sha256 sharding), the same formula
// internal/integration/v5accept's own fixture_test.go already uses for its
// identical real-tamper test. Used ONLY by this package's own tamper test
// to corrupt real on-disk bytes directly — never by any production code
// path, which must never construct a filesystem path from a Locator this
// way outside internal/adapters/artifactstore itself.
func (e *testEnv) artifactObjectPath(t *testing.T, locator string) string {
	t.Helper()
	const prefix = "sha256:"
	if len(locator) != len(prefix)+64 || locator[:len(prefix)] != prefix {
		t.Fatalf("artifactObjectPath: locator %q is not a well-formed sha256 locator", locator)
	}
	hexDigest := locator[len(prefix):]
	return filepath.Join(e.artifactRoot, "objects", hexDigest[0:2], hexDigest[2:4], hexDigest)
}

// do issues a real HTTP request against e's own real server. headers may be
// nil.
func (e *testEnv) do(t *testing.T, method, path string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.base+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, testSessionToken)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decodeInto(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

// seedProject mirrors internal/delivery/httpapi/message/message_test.go's
// own seedProject exactly.
func (e *testEnv) seedProject(t *testing.T, id string) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

// seedActiveRepository mirrors message_test.go's own identical helper.
func (e *testEnv) seedActiveRepository(t *testing.T, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := ports.Command{
		ID: "cmd-reg-" + repositoryID, IdempotencyKey: "idem-reg-" + repositoryID, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-reg-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, e.uow, idsource.Random{}, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID,
		RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
	}
	err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
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
	})
	if err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}
}

// seedRootWorkItem mirrors message_test.go's own identical helper.
func (e *testEnv) seedRootWorkItem(t *testing.T, projectID, repositoryID, suffix string) workapp.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	cmd := ports.Command{
		ID: "cmd-root-" + suffix, IdempotencyKey: "idem-root-" + suffix, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-" + suffix,
	}
	result, err := workapp.CreateRootWorkItem(ctx, e.uow, idsource.Random{}, cmd, workapp.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Task " + suffix,
		InitialScope: []workapp.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"services/" + suffix}, Reason: "seed " + suffix,
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem(%s): %v", suffix, err)
	}
	return result
}

// attemptFixture is the minimal WorkflowRun->NodeRun->ExecutionAttempt
// chain identity triple an Evidence row's own FOREIGN KEY columns require.
type attemptFixture struct {
	RunID     string
	NodeRunID string
	AttemptID string
}

// seedExecutionAttempt mirrors
// internal/delivery/httpapi/message/context_snapshot_test.go's own
// seedFakeExecutionAttempt (a minimal WorkflowRun->NodeRun->ExecutionAttempt
// chain via the low-level CreateWorkflowRun/CreateNodeRun/
// CreateExecutionAttempt Tx methods — no AgentProfile/Policy/scheduler
// machinery needed, since Evidence only needs a real Attempt/NodeRun/Run
// that resolves to a specific WorkItem/Project, not a dispatchable one),
// extended to return every one of the three IDs Evidence's own row needs.
func seedExecutionAttempt(t *testing.T, uow ports.UnitOfWork, projectID, workItemID, familyID, suffix string) attemptFixture {
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
		t.Fatalf("workflow.Compile: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Definitions().PublishWorkflowVersion(ctx, definition, version)
		return err
	}); err != nil {
		t.Fatalf("PublishWorkflowVersion: %v", err)
	}

	runID := runtimedomain.WorkflowRunID("wf-run-" + suffix)
	run, err := runtimedomain.NewWorkflowRun(
		runID, project.ProjectID(projectID), workdomain.WorkItemID(workItemID), version, workdomain.TaskFamilyID(familyID), 1, []byte(`{}`),
	)
	if err != nil {
		t.Fatalf("NewWorkflowRun: %v", err)
	}
	nodeRunID := runtimedomain.NodeRunID("node-run-" + suffix)
	nodeRun, err := runtimedomain.NewNodeRun(nodeRunID, runID, "agent", 1, 0, nil, "sha256:input-fixture", "sha256:profile-fixture")
	if err != nil {
		t.Fatalf("NewNodeRun: %v", err)
	}
	attemptID := "attempt-" + suffix
	attempt, err := runtimedomain.NewExecutionAttempt(
		runtimedomain.ExecutionAttemptID(attemptID), nodeRunID, 1, "sha256:profile-fixture", "fake-provider", nil,
	)
	if err != nil {
		t.Fatalf("NewExecutionAttempt: %v", err)
	}

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Runtime().CreateWorkflowRun(ctx, run); err != nil {
			return err
		}
		if _, err := tx.Runtime().CreateNodeRun(ctx, nodeRun); err != nil {
			return err
		}
		_, err := tx.Runtime().CreateExecutionAttempt(ctx, attempt)
		return err
	}); err != nil {
		t.Fatalf("seed fixture execution attempt: %v", err)
	}
	return attemptFixture{RunID: string(runID), NodeRunID: string(nodeRunID), AttemptID: attemptID}
}

// seedArtifact stores body durably via the real
// internal/app/artifact.PrepareAttachment (Put+Verify OUTSIDE any
// transaction) then attaches it via the real tx.Artifacts().InsertArtifact
// — the identical two-step sequence every real production attach caller
// (AppendMessage, gate/command executors) uses; matcher/sensitivity control
// whether the persisted bytes are already redacted, mirroring
// internal/app/message/commands.go's own "Redact BEFORE Put" ordering.
func (e *testEnv) seedArtifact(t *testing.T, projectID, contentType string, sensitivity redact.Sensitivity, matcher redact.Matcher, body string) artifact.Artifact {
	t.Helper()
	ctx := context.Background()
	redacted := matcher.Tagged(sensitivity, body)
	a, err := appartifact.PrepareAttachment(ctx, e.artifactStore, idsource.Random{}, clock.System{}, appartifact.PrepareAttachmentRequest{
		ProjectID: projectID, Body: strings.NewReader(redacted), ContentType: contentType,
		Sensitivity: sensitivity, Redacted: redacted != body, RetentionClass: artifact.RetentionRawOutputTemp,
	})
	if err != nil {
		t.Fatalf("PrepareAttachment: %v", err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, a)
		return err
	}); err != nil {
		t.Fatalf("InsertArtifact: %v", err)
	}
	return a
}

// assertPersistedContent opens a's own real content through the real
// ArtifactStore, independent of any HTTP route — mirrors
// internal/delivery/httpapi/message/message_test.go's own identical
// assertStoredContent idiom, used to prove redaction against ACTUAL
// persisted content rather than merely an absence-of-code check.
func assertPersistedContent(t *testing.T, env *testEnv, a artifact.Artifact, want string) {
	t.Helper()
	ctx := context.Background()
	ref := ports.ArtifactRef{Locator: a.Locator, SHA256: a.ContentHash, Size: a.Size}
	reader, err := env.artifactStore.Open(ctx, ref)
	if err != nil {
		t.Fatalf("Open content: %v", err)
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(content) != want {
		t.Fatalf("persisted content = %q, want %q", content, want)
	}
}

// seedEvidence builds and persists a real runtimedomain.Evidence row via
// the real tx.Runtime().CreateEvidence — mirrors
// internal/app/runtime/finalize.go's own real production write path,
// composed directly here (no full engine/scheduler run) the same way this
// package's own seedExecutionAttempt above composes CreateWorkflowRun/
// CreateNodeRun/CreateExecutionAttempt directly.
func (e *testEnv) seedEvidence(t *testing.T, projectID, workItemID string, attempt attemptFixture, kind, verdict string, artifactReferences []string) runtimedomain.Evidence {
	t.Helper()
	ctx := context.Background()
	revisions, err := workspace.NewRevisionSet([]workspace.Revision{
		{RepositoryID: "repo-a", VCSObjectID: "commit-sha-fixture", WorkspaceGeneration: 1},
	})
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}
	evidenceID := runtimedomain.EvidenceID("evidence-" + attempt.AttemptID + "-" + kind)
	evidence, err := runtimedomain.NewEvidence(
		evidenceID, project.ProjectID(projectID), workdomain.WorkItemID(workItemID), runtimedomain.WorkflowRunID(attempt.RunID),
		runtimedomain.NodeRunID(attempt.NodeRunID), runtimedomain.ExecutionAttemptID(attempt.AttemptID), kind, verdict,
		artifactReferences, revisions, "policy-v1", time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewEvidence: %v", err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().CreateEvidence(ctx, evidence)
		return err
	}); err != nil {
		t.Fatalf("CreateEvidence: %v", err)
	}
	return evidence
}

// seedContextSnapshot mirrors
// internal/delivery/httpapi/message/context_snapshot_test.go's own
// identical helper.
func (e *testEnv) seedContextSnapshot(t *testing.T, projectID, workItemID, attemptID, suffix string) contextsnapshot.Snapshot {
	t.Helper()
	ctx := context.Background()
	revisions, err := workspace.NewRevisionSet(nil)
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}
	snapshot, err := contextsnapshot.NewSnapshot(
		contextsnapshot.ID("snapshot-"+suffix), project.ProjectID(projectID), workdomain.WorkItemID(workItemID),
		contextsnapshot.AttemptID(attemptID), nil,
		[]contextsnapshot.ResourceRef{{OwnerVersionID: "layer-version-1", ResourceKey: "resource-key-1", ContentHash: "sha256:resource-fixture"}},
		nil, revisions, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.ContextSnapshots().CreateSnapshot(ctx, snapshot)
		return err
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	return snapshot
}
