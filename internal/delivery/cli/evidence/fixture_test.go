package evidence_test

// Real, in-process coverage for V6-15K, driving this package's own Run*
// functions directly (a real invocation, just not one reached by spawning
// the built binary — the same convention internal/delivery/cli/catalog's
// own doc comment describes) against a REAL *sqlite.Store and a REAL
// filesystem internal/adapters/artifactstore.Store — never a mock, never a
// direct row fabrication (this repo's own hard rule). Every fixture helper
// below duplicates internal/delivery/httpapi/evidence's own fixture_test.go
// (itself following internal/delivery/httpapi/message's own newTestEnv
// idiom) rather than importing it — Go test helpers in a _test.go file are
// not exported across packages, and internal/delivery/cli's own archtest
// guarantee (internal/archtest/cli_boundary_test.go) only checks this
// package's own NON-TEST dependency closure, so importing sqlite/
// artifactstore here, in a _test.go file, is not a violation (that
// archtest file's own doc comment, and internal/delivery/cli/run's own
// fixture_test.go, confirm this is the established pattern).

import (
	"context"
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
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	clievidence "github.com/taQuangLing/agent-workflow/internal/delivery/cli/evidence"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// testEnv is one real UnitOfWork + real ArtifactStore pair, torn down via
// t.Cleanup — this package's own Dependencies built directly on top of it.
type testEnv struct {
	deps         clievidence.Dependencies
	uow          ports.UnitOfWork
	artifactRoot string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "evidence-cli.db"))
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

	return &testEnv{
		deps:         clievidence.Dependencies{UnitOfWork: uow, ArtifactStore: artifactStore},
		uow:          uow,
		artifactRoot: artifactRoot,
	}
}

// artifactObjectPath computes the real on-disk path a's own Locator
// resolves to under this env's own artifact root — mirrors
// internal/adapters/artifactstore/filesystem.go's own objectPath scheme
// exactly (git-style two-level sha256 sharding). Used ONLY by this
// package's own tamper tests to corrupt real on-disk bytes directly — never
// by any production code path.
func (e *testEnv) artifactObjectPath(t *testing.T, locator string) string {
	t.Helper()
	const prefix = "sha256:"
	if len(locator) != len(prefix)+64 || locator[:len(prefix)] != prefix {
		t.Fatalf("artifactObjectPath: locator %q is not a well-formed sha256 locator", locator)
	}
	hexDigest := locator[len(prefix):]
	return filepath.Join(e.artifactRoot, "objects", hexDigest[0:2], hexDigest[2:4], hexDigest)
}

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
// uses.
func (e *testEnv) seedArtifact(t *testing.T, projectID, contentType string, sensitivity redact.Sensitivity, matcher redact.Matcher, body string) artifact.Artifact {
	t.Helper()
	ctx := context.Background()
	redacted := matcher.Tagged(sensitivity, body)
	a, err := appartifact.PrepareAttachment(ctx, e.deps.ArtifactStore, idsource.Random{}, clock.System{}, appartifact.PrepareAttachmentRequest{
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

// seedArtifactBinary is seedArtifact for non-UTF8 byte content — matcher is
// always redact.NewMatcher() with redact.Public sensitivity (binary content
// is never a redaction target in these fixtures), so the raw bytes always
// round-trip unmodified.
func (e *testEnv) seedArtifactBinary(t *testing.T, projectID, contentType string, body []byte) artifact.Artifact {
	t.Helper()
	ctx := context.Background()
	a, err := appartifact.PrepareAttachment(ctx, e.deps.ArtifactStore, idsource.Random{}, clock.System{}, appartifact.PrepareAttachmentRequest{
		ProjectID: projectID, Body: strings.NewReader(string(body)), ContentType: contentType,
		Sensitivity: redact.Public, Redacted: false, RetentionClass: artifact.RetentionRawOutputTemp,
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

// seedEvidence builds and persists a real runtimedomain.Evidence row via
// the real tx.Runtime().CreateEvidence.
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
