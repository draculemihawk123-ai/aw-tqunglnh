package decision_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/decision"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V11-07: an approval decision that also commits, and the read side / restore command around it, over HTTP
// against a real sqlite store. Git is stubbed (identity reader only): what is proven here is the wire
// contract, the policy and identity refusals, and that nothing is written when they refuse.

type stubIdentities struct {
	identity ports.GitIdentity
}

func (s stubIdentities) ReadGitIdentity(context.Context, ports.WorkspaceHandle) (ports.GitIdentity, error) {
	return s.identity, nil
}

type releaseEnv struct {
	uow               ports.UnitOfWork
	server            *httptest.Server
	runID             string
	approvalRequestID string
	familyID          string
	workspaceID       string
	workspaceVersion  uint64
}

var reviewerIdentity = ports.GitIdentity{Name: "Reviewer", Email: "reviewer@example.test", NameSource: "global", EmailSource: "global"}

func newReleaseEnv(t *testing.T, identity ports.GitIdentity, withRelease bool) *releaseEnv {
	t.Helper()
	ctx := context.Background()
	_, uow := openDecisionTestStore(t, "release.db")
	ids := idsource.NewSequential("id")
	seedProject(t, uow, "project-1")
	root := readyWorkItemFixtureInExistingProject(t, uow, ids, "project-1", "repo-1")

	document := approvalDocument(600)
	for index := range document.Nodes {
		if document.Nodes[index].Key == "gate" {
			document.Nodes[index].Approval.Release = &workflow.ApprovalReleaseConfig{Commit: workflow.ReleaseCommitOffered, Outcomes: []string{"approved"}}
		}
	}
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-r", "wf-v-r", document)
	start, err := runtime.StartWorkflowRun(ctx, uow, ids, ports.Command{
		ID: "cmd-start-r", IdempotencyKey: "idem-start-r", Actor: "actor-1", CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"),
		RequestedAt: time.Now().UTC(), Type: "StartWorkflowRun", RequestHash: "hash-start-r",
	}, runtime.StartWorkflowRunRequest{ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID())})
	if err != nil {
		t.Fatal(err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: start.RunID, NodeRunID: start.NodeRunID})
	if err != nil || hop.NextApprovalRequestID == "" {
		t.Fatalf("AdvanceRun = %+v, %v", hop, err)
	}
	env := &releaseEnv{uow: uow, runID: start.RunID, approvalRequestID: hop.NextApprovalRequestID, familyID: root.FamilyID}
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		repositories, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, root.WorkspaceSetID)
		if err != nil || len(repositories) != 1 {
			t.Fatalf("repositories = %d, %v", len(repositories), err)
		}
		env.workspaceID, env.workspaceVersion = string(repositories[0].ID), repositories[0].Version
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	deps := decision.Dependencies{UOW: uow, IDs: ids}
	if withRelease {
		deps.Release = decision.ReleaseSupport{Identities: stubIdentities{identity: identity}}
	}
	registry := httpapi.NewRouteRegistry()
	decision.RegisterRoutes(registry, deps)
	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	env.server = httptest.NewServer(httpapi.Chain(mux, httpapi.BindPrincipal(reviewerPrincipal())))
	t.Cleanup(env.server.Close)
	return env
}

func (e *releaseEnv) commitBody(outcome string, mutate func(*decision.ResolveApprovalCommitBody)) string {
	commit := &decision.ResolveApprovalCommitBody{
		Message: "contract: add the issue API",
		Repositories: []decision.ResolveApprovalCommitRepositoryBody{{
			RepositoryWorkspaceID: e.workspaceID, ExpectedWorkspaceVersion: e.workspaceVersion,
			Files:      []decision.ResolveApprovalFile{{Path: "spec/openapi.json", Status: "ADDED", ContentHash: "abc"}},
			Unselected: "KEEP",
		}},
	}
	if mutate != nil {
		mutate(commit)
	}
	body, _ := json.Marshal(decision.ResolveApprovalBody{Outcome: outcome, Reason: "ok", Commit: commit})
	return string(body)
}

func (e *releaseEnv) state(t *testing.T) runtimedomain.ApprovalRequest {
	t.Helper()
	return mustGetApprovalRequest(t, e.uow, e.approvalRequestID)
}

func (e *releaseEnv) get(t *testing.T, path string, into any) *http.Response {
	t.Helper()
	resp, err := http.Get(e.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	if into != nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
	return resp
}

func TestResolveApproval_HTTP_WithCommit_RecordsTheDecisionAndTheRequestedCommit(t *testing.T) {
	e := newReleaseEnv(t, reviewerIdentity, true)
	resp := postResolveApproval(t, e.server.URL, e.runID, e.approvalRequestID, "idem-commit-1", `"1"`, e.commitBody("approved", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, mustBody(t, resp))
	}
	body := decodeResolveApprovalResponse(t, resp)
	if !body.Won || body.Commit == nil || len(body.Commit.Operations) != 1 || body.CommitSkipped != "" {
		t.Fatalf("body = %+v, want a won decision with one requested commit operation", body)
	}
	if e.state(t).State != runtimedomain.ApprovalRequestDecided {
		t.Fatal("the decision must be recorded")
	}

	var release struct {
		ApprovalRequestID string `json:"approvalRequestId"`
		Operations        []struct {
			ReleaseSetLocalCommitID string   `json:"releaseSetLocalCommitId"`
			State                   string   `json:"state"`
			Files                   []string `json:"files"`
			Unselected              string   `json:"unselected"`
		} `json:"operations"`
	}
	if r := e.get(t, "/runs/"+e.runID+"/approval-requests/"+e.approvalRequestID+"/release", &release); r.StatusCode != http.StatusOK {
		t.Fatalf("GET release status = %d", r.StatusCode)
	}
	if len(release.Operations) != 1 || release.Operations[0].State != "REQUESTED" || release.Operations[0].Unselected != "KEEP" ||
		len(release.Operations[0].Files) != 1 || release.Operations[0].Files[0] != "spec/openapi.json" ||
		release.Operations[0].ReleaseSetLocalCommitID != body.Commit.Operations[0].ReleaseSetLocalCommitID {
		t.Fatalf("release = %+v", release)
	}
}

func TestResolveApproval_HTTP_WithCommit_AMissingGitIdentityIs422WithTheRepositoryNamedAndTheGateStaysOpen(t *testing.T) {
	e := newReleaseEnv(t, ports.GitIdentity{Name: "Only A Name"}, true)
	resp := postResolveApproval(t, e.server.URL, e.runID, e.approvalRequestID, "idem-commit-id", `"1"`, e.commitBody("approved", nil))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	body := decodeErrorResponse(t, resp)
	if !strings.Contains(body.Error.Message, "COMMIT_IDENTITY_MISSING") || !strings.Contains(body.Error.Message, "git config --global") ||
		len(body.Error.Details) != 1 || !strings.Contains(body.Error.Details[0].Message, "COMMIT_IDENTITY_MISSING") {
		t.Fatalf("error = %+v, want COMMIT_IDENTITY_MISSING with the fix and a per-repository detail", body.Error)
	}
	if e.state(t).State != runtimedomain.ApprovalRequestPending {
		t.Fatal("the gate must stay PENDING when the commit cannot be authored")
	}
}

func TestResolveApproval_HTTP_WithCommit_AnOutcomeThePolicyDoesNotOfferIs422AndNothingIsWritten(t *testing.T) {
	e := newReleaseEnv(t, reviewerIdentity, true)
	resp := postResolveApproval(t, e.server.URL, e.runID, e.approvalRequestID, "idem-commit-no", `"1"`, e.commitBody("rejected", nil))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body %s)", resp.StatusCode, mustBody(t, resp))
	}
	if e.state(t).State != runtimedomain.ApprovalRequestPending {
		t.Fatal("a refused commit must leave the gate PENDING")
	}
}

func TestResolveApproval_HTTP_WithCommit_BadSpecificationIs400(t *testing.T) {
	e := newReleaseEnv(t, reviewerIdentity, true)
	for name, mutate := range map[string]func(*decision.ResolveApprovalCommitBody){
		"no message":   func(c *decision.ResolveApprovalCommitBody) { c.Message = " " },
		"bad handling": func(c *decision.ResolveApprovalCommitBody) { c.Repositories[0].Unselected = "DELETE" },
		"empty path":   func(c *decision.ResolveApprovalCommitBody) { c.Repositories[0].Files[0].Path = "" },
	} {
		resp := postResolveApproval(t, e.server.URL, e.runID, e.approvalRequestID, "idem-bad-"+strings.ReplaceAll(name, " ", "-"), `"1"`, e.commitBody("approved", mutate))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", name, resp.StatusCode, mustBody(t, resp))
		}
	}
	if e.state(t).State != runtimedomain.ApprovalRequestPending {
		t.Fatal("a malformed commit must leave the gate PENDING")
	}
}

func TestResolveApproval_HTTP_WithCommit_SameKeyWithADifferentCommitIsAConflictNotAReplay(t *testing.T) {
	e := newReleaseEnv(t, reviewerIdentity, true)
	first := postResolveApproval(t, e.server.URL, e.runID, e.approvalRequestID, "idem-commit-same", `"1"`, e.commitBody("approved", nil))
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first = %d", first.StatusCode)
	}
	replay := postResolveApproval(t, e.server.URL, e.runID, e.approvalRequestID, "idem-commit-same", `"1"`, e.commitBody("approved", nil))
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("an identical replay must be 200, got %d", replay.StatusCode)
	}
	other := postResolveApproval(t, e.server.URL, e.runID, e.approvalRequestID, "idem-commit-same", `"1"`,
		e.commitBody("approved", func(c *decision.ResolveApprovalCommitBody) { c.Message = "a different message" }))
	if other.StatusCode != http.StatusConflict {
		t.Fatalf("a different commit under the same key: status = %d, want 409", other.StatusCode)
	}
}

func TestResolveApproval_HTTP_WithCommit_WithoutAGitProviderIsUnavailable(t *testing.T) {
	e := newReleaseEnv(t, reviewerIdentity, false)
	resp := postResolveApproval(t, e.server.URL, e.runID, e.approvalRequestID, "idem-commit-nogit", `"1"`, e.commitBody("approved", nil))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if e.state(t).State != runtimedomain.ApprovalRequestPending {
		t.Fatal("the gate must stay PENDING")
	}
}

func TestGetFamilyReleaseContext_HTTP_ReportsTheAuthorPerRepository(t *testing.T) {
	e := newReleaseEnv(t, reviewerIdentity, true)
	var got struct {
		Repositories []struct {
			RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
			Version               uint64 `json:"version"`
			Identity              struct {
				Name  string `json:"name"`
				Ready bool   `json:"ready"`
			} `json:"identity"`
		} `json:"repositories"`
		Parked []any `json:"parked"`
	}
	if r := e.get(t, "/projects/project-1/task-families/"+e.familyID+"/release-context", &got); r.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", r.StatusCode)
	}
	if len(got.Repositories) != 1 || got.Repositories[0].RepositoryWorkspaceID != e.workspaceID || !got.Repositories[0].Identity.Ready || got.Repositories[0].Identity.Name != "Reviewer" || len(got.Parked) != 0 {
		t.Fatalf("context = %+v", got)
	}
	if r := e.get(t, "/projects/project-other/task-families/"+e.familyID+"/release-context", nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("another project's id must be hidden: status = %d", r.StatusCode)
	}
}

func TestRestoreParkedChanges_HTTP_RefusesWhenNothingIsParkedAndWithoutAProvider(t *testing.T) {
	e := newReleaseEnv(t, reviewerIdentity, false)
	post := func(key, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, e.server.URL+"/projects/project-1/repository-workspaces/"+e.workspaceID+"/restore-parked", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(httpapi.IdempotencyKeyHeader, key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := post("k1", `{"releaseSetLocalCommitId":"no-such-operation"}`); r.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown operation: status = %d, want 404", r.StatusCode)
	}
	if r := post("k2", `{}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing operation id: status = %d, want 400", r.StatusCode)
	}
	if r := post("k3", `{"releaseSetLocalCommitId":"x","extra":1}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: status = %d, want 400", r.StatusCode)
	}
}
