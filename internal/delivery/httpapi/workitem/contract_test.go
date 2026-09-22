package workitem_test

// Real HTTP coverage for V6-04B (a rework of V6-04, found by V6-14's black-box
// journey): POST /projects/{projectId}/work-items and
// POST /projects/{projectId}/work-items/{workItemId}/children accept an
// optional "contract" object, so a WorkItem can be given its readiness
// contract through the PUBLIC surface and reach READY — the sequence
// V6-04A's mark-ready route could never complete before, because no public
// call could set a contract. Same discipline as workitem_test.go: a REAL
// httpapi.Server over a REAL *sqlite.Store, no mocks and no hand-built
// WorkItem rows.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// contractJSON is the exact wire shape the V6-14 acceptance test sends: one
// executable acceptance criterion (non-empty verificationRef), every other
// field set except the optional workflowVersionId.
func contractJSON() map[string]any {
	return map[string]any{
		"schemaVersion": 1,
		"behavior":      "Users can export a report as CSV",
		"acceptanceCriteria": []any{
			map[string]any{"description": "the CSV has a header row", "verificationRef": "go test ./report/..."},
		},
		"verificationSpec": "run the report tests",
		"riskLevel":        "LOW",
		"exclusions":       []any{"PDF export"},
	}
}

// wantContractView is what contractJSON must read back as on the
// authoritative detail.
func wantContractView() *workapp.WorkItemContractView {
	return &workapp.WorkItemContractView{
		SchemaVersion: 1, Behavior: "Users can export a report as CSV",
		AcceptanceCriteria: []workapp.AcceptanceCriterionView{{Description: "the CSV has a header row", VerificationRef: "go test ./report/..."}},
		VerificationSpec:   "run the report tests", RiskLevel: "LOW", Exclusions: []string{"PDF export"},
	}
}

func rootBody(title string, contract map[string]any) map[string]any {
	body := map[string]any{"title": title, "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")}}
	if contract != nil {
		body["contract"] = contract
	}
	return body
}

func childBody(title string, contract map[string]any) map[string]any {
	body := map[string]any{
		"title": title, "parentJoinPolicy": "ALL",
		"effectiveScope": []any{scopeGrantJSON("repo-a", "WRITE", "child scope")},
	}
	if contract != nil {
		body["contract"] = contract
	}
	return body
}

func (e *testEnv) mustCreateRoot(t *testing.T, key string, body map[string]any) workapp.CreateRootWorkItemResult {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/projects/project-1/work-items", key, "", body)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create root status = %d, want 201, body=%s", resp.StatusCode, raw)
	}
	var result workapp.CreateRootWorkItemResult
	decodeInto(t, resp, &result)
	return result
}

func (e *testEnv) mustCreateChild(t *testing.T, parentID, key string, body map[string]any) workapp.CreateChildWorkItemResult {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/projects/project-1/work-items/"+parentID+"/children", key, "", body)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create child status = %d, want 201, body=%s", resp.StatusCode, raw)
	}
	var result workapp.CreateChildWorkItemResult
	decodeInto(t, resp, &result)
	return result
}

func (e *testEnv) detail(t *testing.T, workItemID string) workapp.WorkItemDetail {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/projects/project-1/work-items/"+workItemID, "", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET work-item status = %d, want 200", resp.StatusCode)
	}
	var detail workapp.WorkItemDetail
	decodeInto(t, resp, &detail)
	return detail
}

func (e *testEnv) readiness(t *testing.T, workItemID string) (workapp.WorkItemReadiness, string) {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/projects/project-1/work-items/"+workItemID+"/readiness", "", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET readiness status = %d, want 200", resp.StatusCode)
	}
	etag := resp.Header.Get(httpapi.ETagHeader)
	var readiness workapp.WorkItemReadiness
	decodeInto(t, resp, &readiness)
	return readiness, etag
}

func (e *testEnv) workItemCount(t *testing.T) int {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/projects/project-1/work-items", "", "", nil)
	var list struct {
		Items []workapp.WorkItemDetail `json:"items"`
	}
	decodeInto(t, resp, &list)
	return len(list.Items)
}

func errorDetails(t *testing.T, resp *http.Response) []httpapi.ErrorDetail {
	t.Helper()
	var body httpapi.ErrorResponse
	decodeInto(t, resp, &body)
	return body.Error.Details
}

// publishWorkflowVersion publishes a minimal real WorkflowVersion directly
// through the repository (test setup for a concern V6-05 exposes over HTTP,
// mirroring seedProject/seedActiveRepository's own reasoning).
func (e *testEnv) publishWorkflowVersion(t *testing.T, projectID, definitionID, versionID string) {
	t.Helper()
	pid := project.ProjectID(projectID)
	definition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(definitionID), ProjectID: &pid,
		Name: "workflow " + definitionID, Status: workflow.DefinitionActive, Version: 1,
	}
	candidate, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1,
		Document: workflow.WorkflowDocument{
			SchemaVersion: "1",
			Nodes: []workflow.Node{
				{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
				{Key: "end", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{{Key: "start-to-end", From: "start", Outcome: "next", To: "end"}},
		},
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "1", Hash: "sha256:dependency-1"},
		}},
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow %s: %v", versionID, err)
	}
	ctx := context.Background()
	err = e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Definitions().PublishWorkflowVersion(ctx, definition, candidate)
		return err
	})
	if err != nil {
		t.Fatalf("publish workflow version %s: %v", versionID, err)
	}
}

// TestCreateChildWithContract_JourneyToReady is the sequence V6-14 proved
// impossible before V6-04B, run end to end over real HTTP: create root ->
// create child WITH a contract -> readiness says ready -> mark-ready (with
// If-Match) answers 200 and the child is READY. The root, created without a
// contract, is proven to stay exactly as unready as it always was.
func TestCreateChildWithContract_JourneyToReady(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	root := env.mustCreateRoot(t, "idem-root", rootBody("Root task", nil))
	child := env.mustCreateChild(t, root.WorkItemID, "idem-child", childBody("Child task", contractJSON()))
	if child.Status != "BACKLOG" {
		t.Fatalf("child.Status = %q, want BACKLOG (a contract never moves a WorkItem)", child.Status)
	}

	// The stored contract reads back through the authoritative detail.
	detail := env.detail(t, child.WorkItemID)
	if !reflect.DeepEqual(detail.Contract, wantContractView()) {
		t.Fatalf("child detail.Contract = %+v, want %+v", detail.Contract, wantContractView())
	}
	if detail.Status != "BACKLOG" || detail.Version != 1 {
		t.Fatalf("child detail = %s@%d, want BACKLOG@1", detail.Status, detail.Version)
	}

	// Readiness reflects it.
	readiness, etag := env.readiness(t, child.WorkItemID)
	if !readiness.Ready || len(readiness.Problems) != 0 {
		t.Fatalf("child readiness = %+v, want Ready with no problems", readiness)
	}

	// mark-ready with If-Match: 200 and READY.
	markResp := env.do(t, http.MethodPost, "/work-items/"+child.WorkItemID+"/mark-ready", "idem-mark-child", etag, map[string]any{})
	if markResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(markResp.Body)
		t.Fatalf("mark-ready status = %d, want 200, body=%s", markResp.StatusCode, raw)
	}
	var marked workapp.MarkWorkItemReadyResult
	decodeInto(t, markResp, &marked)
	if marked.Status != "READY" || marked.Version != 2 || marked.WorkItemID != child.WorkItemID {
		t.Fatalf("mark-ready result = %+v, want READY@2 for the child", marked)
	}
	after := env.detail(t, child.WorkItemID)
	if after.Status != "READY" || after.Version != 2 {
		t.Fatalf("child after mark-ready = %s@%d, want READY@2", after.Status, after.Version)
	}
	if !reflect.DeepEqual(after.Contract, wantContractView()) {
		t.Fatalf("child contract changed across mark-ready: %+v", after.Contract)
	}

	// The root never got a contract: still unready, mark-ready still 409.
	rootReadiness, _ := env.readiness(t, root.WorkItemID)
	if rootReadiness.Ready {
		t.Fatal("root created without a contract must not report Ready")
	}
	if env.detail(t, root.WorkItemID).Contract != nil {
		t.Fatal("root created without a contract must report no contract (not inherited from, or shared with, the child)")
	}
	rootMark := env.do(t, http.MethodPost, "/work-items/"+root.WorkItemID+"/mark-ready", "idem-mark-root", `"1"`, map[string]any{})
	if rootMark.StatusCode != http.StatusConflict {
		t.Fatalf("mark-ready on the contract-less root status = %d, want 409 (unchanged behaviour)", rootMark.StatusCode)
	}
	rootMark.Body.Close()
}

// TestCreateRootWithContract_JourneyToReady is the same closure for a root:
// the contract rides on POST /projects/{projectId}/work-items too.
func TestCreateRootWithContract_JourneyToReady(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	root := env.mustCreateRoot(t, "idem-root", rootBody("Root task", contractJSON()))
	if got := env.detail(t, root.WorkItemID).Contract; !reflect.DeepEqual(got, wantContractView()) {
		t.Fatalf("root detail.Contract = %+v, want %+v", got, wantContractView())
	}
	readiness, etag := env.readiness(t, root.WorkItemID)
	if !readiness.Ready {
		t.Fatalf("root readiness = %+v, want Ready", readiness)
	}
	markResp := env.do(t, http.MethodPost, "/work-items/"+root.WorkItemID+"/mark-ready", "idem-mark-root", etag, map[string]any{})
	if markResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(markResp.Body)
		t.Fatalf("mark-ready status = %d, want 200, body=%s", markResp.StatusCode, raw)
	}
	markResp.Body.Close()
	if got := env.detail(t, root.WorkItemID).Status; got != "READY" {
		t.Fatalf("root status = %s, want READY", got)
	}
}

// TestCreateChildWithoutContract_MarkReadyStillConflicts: omitting the
// contract is exactly the pre-V6-04B behaviour — the child is created, stays
// BACKLOG with no contract, and mark-ready answers 409 carrying the same
// readiness problems the readiness route reports.
func TestCreateChildWithoutContract_MarkReadyStillConflicts(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	root := env.mustCreateRoot(t, "idem-root", rootBody("Root task", nil))
	child := env.mustCreateChild(t, root.WorkItemID, "idem-child", childBody("Child task", nil))

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+child.WorkItemID, "", "", nil)
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read detail body: %v", err)
	}
	if strings.Contains(string(raw), `"contract"`) {
		t.Fatalf("detail of a WorkItem created without a contract = %s, want no \"contract\" key at all (byte-for-byte the old shape)", raw)
	}

	readiness, etag := env.readiness(t, child.WorkItemID)
	if readiness.Ready || len(readiness.Problems) == 0 {
		t.Fatalf("readiness = %+v, want not Ready with concrete problems", readiness)
	}
	markResp := env.do(t, http.MethodPost, "/work-items/"+child.WorkItemID+"/mark-ready", "idem-mark", etag, map[string]any{})
	if markResp.StatusCode != http.StatusConflict {
		t.Fatalf("mark-ready status = %d, want 409", markResp.StatusCode)
	}
	details := errorDetails(t, markResp)
	if len(details) != len(readiness.Problems) {
		t.Fatalf("mark-ready error details = %+v, want one per readiness problem %v", details, readiness.Problems)
	}
}

// TestCreateWithContract_NoExecutableAcceptanceCriterion_ReadinessSaysSo: a
// contract whose only criterion has an empty verificationRef (and no approval
// exception exists on this surface) is stored as given, and readiness reports
// precisely the "no executable acceptance criterion" problem — nothing else,
// since every other field is complete.
func TestCreateWithContract_NoExecutableAcceptanceCriterion_ReadinessSaysSo(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	contract := contractJSON()
	contract["acceptanceCriteria"] = []any{map[string]any{"description": "a human checks the CSV", "verificationRef": ""}}
	root := env.mustCreateRoot(t, "idem-root", rootBody("Root task", contract))

	readiness, etag := env.readiness(t, root.WorkItemID)
	want := []string{"no executable acceptance criterion is present and no approval exception was granted"}
	if readiness.Ready || !reflect.DeepEqual(readiness.Problems, want) {
		t.Fatalf("readiness = %+v, want not Ready with exactly %v", readiness, want)
	}
	markResp := env.do(t, http.MethodPost, "/work-items/"+root.WorkItemID+"/mark-ready", "idem-mark", etag, map[string]any{})
	if markResp.StatusCode != http.StatusConflict {
		t.Fatalf("mark-ready status = %d, want 409", markResp.StatusCode)
	}
	details := errorDetails(t, markResp)
	if len(details) != 1 || details[0].Field != "readiness" || details[0].Message != want[0] {
		t.Fatalf("mark-ready details = %+v, want one {readiness, %q}", details, want[0])
	}
	// Stored as given: the descriptive-only criterion is there, with no ref.
	view := env.detail(t, root.WorkItemID).Contract
	if view == nil || len(view.AcceptanceCriteria) != 1 || view.AcceptanceCriteria[0].VerificationRef != "" {
		t.Fatalf("stored contract = %+v, want the single descriptive-only criterion", view)
	}
}

// TestCreateWithContract_PinsRealWorkflowVersion: the optional
// workflowVersionId key works over the wire and is reported on both the
// contract and the pre-existing top-level field.
func TestCreateWithContract_PinsRealWorkflowVersion(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	env.publishWorkflowVersion(t, "project-1", "wf-def-1", "wf-v-1")

	contract := contractJSON()
	contract["workflowVersionId"] = "wf-v-1"
	root := env.mustCreateRoot(t, "idem-root", rootBody("Root task", contract))
	detail := env.detail(t, root.WorkItemID)
	if detail.WorkflowVersionID != "wf-v-1" || detail.Contract == nil || detail.Contract.WorkflowVersionID != "wf-v-1" {
		t.Fatalf("detail = %+v, want workflowVersionId wf-v-1 on the top-level field and inside contract", detail)
	}
}

// TestCreateWithContract_UnknownWorkflowVersion_Returns400WithFieldDetail:
// pinning a WorkflowVersion that does not exist is a caller-fixable 400 (not
// the 500 the raw foreign-key failure would be), names the field, and leaves
// nothing behind — for root and child.
func TestCreateWithContract_UnknownWorkflowVersion_Returns400WithFieldDetail(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.mustCreateRoot(t, "idem-root", rootBody("Root task", nil))

	contract := contractJSON()
	contract["workflowVersionId"] = "wf-does-not-exist"

	rootResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-bad-root", "", rootBody("Another root", contract))
	if rootResp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(rootResp.Body)
		t.Fatalf("unknown workflow version (root) status = %d, want 400, body=%s", rootResp.StatusCode, raw)
	}
	if details := errorDetails(t, rootResp); len(details) != 1 || details[0].Field != "contract.workflowVersionId" {
		t.Fatalf("root details = %+v, want one for contract.workflowVersionId", details)
	}
	childResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/children", "idem-bad-child", "", childBody("Child", contract))
	if childResp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(childResp.Body)
		t.Fatalf("unknown workflow version (child) status = %d, want 400, body=%s", childResp.StatusCode, raw)
	}
	if details := errorDetails(t, childResp); len(details) != 1 || details[0].Field != "contract.workflowVersionId" {
		t.Fatalf("child details = %+v, want one for contract.workflowVersionId", details)
	}
	if n := env.workItemCount(t); n != 1 {
		t.Fatalf("work items = %d, want only the first root (rejected creates must leave nothing behind)", n)
	}
}

// TestCreateWithContract_MalformedContract_Returns400WithFieldDetails: shapes
// that can never be valid are rejected with one precise "contract.<field>"
// detail each, all at once, before anything is written.
func TestCreateWithContract_MalformedContract_Returns400WithFieldDetails(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.mustCreateRoot(t, "idem-root", rootBody("Root task", nil))

	bad := map[string]any{
		"schemaVersion":      -1,
		"acceptanceCriteria": []any{map[string]any{"description": "ok"}, map[string]any{"description": "  ", "verificationRef": "go test"}},
		"exclusions":         []any{"fine", ""},
	}
	wantFields := []string{"contract.schemaVersion", "contract.acceptanceCriteria[1].description", "contract.exclusions[1]"}

	for label, resp := range map[string]*http.Response{
		"root":  env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-bad-root", "", rootBody("Bad root", bad)),
		"child": env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/children", "idem-bad-child", "", childBody("Bad child", bad)),
	} {
		if resp.StatusCode != http.StatusBadRequest {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s: status = %d, want 400, body=%s", label, resp.StatusCode, raw)
		}
		var got []string
		for _, d := range errorDetails(t, resp) {
			got = append(got, d.Field)
		}
		if !reflect.DeepEqual(got, wantFields) {
			t.Fatalf("%s: detail fields = %v, want %v", label, got, wantFields)
		}
	}
	if n := env.workItemCount(t); n != 1 {
		t.Fatalf("work items = %d, want only the first root", n)
	}
}

// TestCreateWithContract_UnknownKeysRejected is the strict-decode guard: the
// body is decoded with DisallowUnknownFields, recursively, so a key the
// contract does not define — a status-like smuggle, a typo, or an unknown key
// inside an acceptance criterion — is a 400 and creates nothing, for root and
// child alike.
func TestCreateWithContract_UnknownKeysRejected(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.mustCreateRoot(t, "idem-root", rootBody("Root task", nil))

	mutations := map[string]func(c map[string]any){
		"status inside contract":        func(c map[string]any) { c["status"] = "READY" },
		"targetStatus inside contract":  func(c map[string]any) { c["targetStatus"] = "DONE" },
		"family inside contract":        func(c map[string]any) { c["family"] = "family-x" },
		"workspace inside contract":     func(c map[string]any) { c["workspace"] = "ws-x" },
		"approvalException (no column)": func(c map[string]any) { c["approvalException"] = map[string]any{"reason": "because"} },
		"typo'd key":                    func(c map[string]any) { c["acceptanceCriterias"] = []any{} },
		"unknown key inside a criterion": func(c map[string]any) {
			c["acceptanceCriteria"] = []any{map[string]any{"description": "x", "verificationRef": "y", "passed": true}}
		},
	}
	i := 0
	for name, mutate := range mutations {
		contract := contractJSON()
		mutate(contract)
		i++
		rootResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-unknown-root-"+string(rune('a'+i)), "", rootBody("Smuggler", contract))
		if rootResp.StatusCode != http.StatusBadRequest {
			raw, _ := io.ReadAll(rootResp.Body)
			t.Fatalf("%s (root): status = %d, want 400, body=%s", name, rootResp.StatusCode, raw)
		}
		rootResp.Body.Close()
		childResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/children", "idem-unknown-child-"+string(rune('a'+i)), "", childBody("Smuggler", contract))
		if childResp.StatusCode != http.StatusBadRequest {
			raw, _ := io.ReadAll(childResp.Body)
			t.Fatalf("%s (child): status = %d, want 400, body=%s", name, childResp.StatusCode, raw)
		}
		childResp.Body.Close()
	}
	if n := env.workItemCount(t); n != 1 {
		t.Fatalf("work items = %d, want only the first root (every rejected body must leave nothing behind)", n)
	}
}

// TestCreateRootWithContract_SameKeySameBody_ReplaysAndStoresOnce: a genuine
// retry of a contract-bearing create replays the stored result (200, same
// WorkItemID), never writes a second row, and the contract is stored once.
func TestCreateRootWithContract_SameKeySameBody_ReplaysAndStoresOnce(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	first := env.mustCreateRoot(t, "idem-replay", rootBody("Root task", contractJSON()))
	second := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-replay", "", rootBody("Root task", contractJSON()))
	if second.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(second.Body)
		t.Fatalf("replay status = %d, want 200, body=%s", second.StatusCode, raw)
	}
	var replayed workapp.CreateRootWorkItemResult
	decodeInto(t, second, &replayed)
	if replayed.WorkItemID != first.WorkItemID {
		t.Fatalf("replay WorkItemID = %s, want the original %s", replayed.WorkItemID, first.WorkItemID)
	}
	if n := env.workItemCount(t); n != 1 {
		t.Fatalf("work items after replay = %d, want exactly 1", n)
	}
	if got := env.detail(t, first.WorkItemID).Contract; !reflect.DeepEqual(got, wantContractView()) {
		t.Fatalf("stored contract after replay = %+v, want %+v", got, wantContractView())
	}
}

// TestCreateWithContract_SameKeyDifferentContract_Conflicts is the proof that
// the contract participates in the semantic request hash: the same
// Idempotency-Key with a body that differs ONLY in the contract (a changed
// field, an added contract, a removed contract) is a 409, never a silent
// replay of the first result — and the first contract stays untouched. Root
// and child.
func TestCreateWithContract_SameKeyDifferentContract_Conflicts(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	changed := contractJSON()
	changed["behavior"] = "Users can export a report as XLSX"
	extraCriterion := contractJSON()
	extraCriterion["acceptanceCriteria"] = append(extraCriterion["acceptanceCriteria"].([]any), map[string]any{"description": "another"})

	// --- root ---
	first := env.mustCreateRoot(t, "idem-conflict-root", rootBody("Root task", contractJSON()))
	for name, body := range map[string]map[string]any{
		"changed behavior":         rootBody("Root task", changed),
		"extra criterion":          rootBody("Root task", extraCriterion),
		"contract removed":         rootBody("Root task", nil),
		"same contract, new title": rootBody("Other title", contractJSON()),
	} {
		resp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-conflict-root", "", body)
		if resp.StatusCode != http.StatusConflict {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("root %s: status = %d, want 409, body=%s", name, resp.StatusCode, raw)
		}
		resp.Body.Close()
	}
	// A contract added to a body first sent WITHOUT one conflicts too.
	bare := env.mustCreateRoot(t, "idem-conflict-bare", rootBody("Bare root", nil))
	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-conflict-bare", "", rootBody("Bare root", contractJSON()))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("root contract added: status = %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()
	if got := env.detail(t, bare.WorkItemID).Contract; got != nil {
		t.Fatalf("a rejected replay attached a contract to the bare root: %+v", got)
	}
	if got := env.detail(t, first.WorkItemID).Contract; !reflect.DeepEqual(got, wantContractView()) {
		t.Fatalf("first root's contract after conflicting retries = %+v, want unchanged %+v", got, wantContractView())
	}

	// --- child ---
	firstChild := env.mustCreateChild(t, first.WorkItemID, "idem-conflict-child", childBody("Child task", contractJSON()))
	for name, body := range map[string]map[string]any{
		"changed behavior": childBody("Child task", changed),
		"contract removed": childBody("Child task", nil),
	} {
		resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+first.WorkItemID+"/children", "idem-conflict-child", "", body)
		if resp.StatusCode != http.StatusConflict {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("child %s: status = %d, want 409, body=%s", name, resp.StatusCode, raw)
		}
		resp.Body.Close()
	}
	replay := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+first.WorkItemID+"/children", "idem-conflict-child", "", childBody("Child task", contractJSON()))
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("child same-key same-body replay status = %d, want 200", replay.StatusCode)
	}
	var replayed workapp.CreateChildWorkItemResult
	decodeInto(t, replay, &replayed)
	if replayed.WorkItemID != firstChild.WorkItemID {
		t.Fatalf("child replay WorkItemID = %s, want %s", replayed.WorkItemID, firstChild.WorkItemID)
	}
	if got := env.detail(t, firstChild.WorkItemID).Contract; !reflect.DeepEqual(got, wantContractView()) {
		t.Fatalf("child contract after conflicting retries = %+v, want unchanged", got)
	}
}

// storedRequestHash reads back the RequestHash the real command recorded on
// its receipt — the value replay/conflict decisions are made against.
func (e *testEnv) storedRequestHash(t *testing.T, commandType, key string) string {
	t.Helper()
	receipt, found, err := httpapi.LookupReceipt(context.Background(), e.uow, testPrincipal().Actor, ports.ProjectScope("project-1"), key, commandType)
	if err != nil || !found {
		t.Fatalf("LookupReceipt(%s, %s) = found %v, err %v, want a stored receipt", commandType, key, found, err)
	}
	return receipt.RequestHash
}

// TestCreateRootWorkItem_RequestHashCanonicalForms pins the exact canonical
// bytes the semantic hash is computed over, on both sides of V6-04B's change:
//
//   - a body WITHOUT a contract hashes over the very same bytes it did before
//     V6-04B (no "contract" key, not even "contract":null), so a receipt a
//     pre-V6-04B binary wrote still replays after an upgrade; a "contract":null
//     body is the same request and hashes identically;
//   - a body WITH a contract hashes over bytes that include the whole contract,
//     in the declared field order — the identical literal the CLI's own test
//     (internal/delivery/cli/workitem/contract_test.go) pins, which is what
//     keeps an HTTP call and a `aw work-item create` call replay-equivalent.
func TestCreateRootWorkItem_RequestHashCanonicalForms(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	scope := ports.ProjectScope("project-1")

	const legacyCanonical = `{"title":"Root task","initialScope":[{"repositoryId":"repo-a","access":"WRITE","reason":"root scope"}]}`
	env.mustCreateRoot(t, "idem-hash-legacy", rootBody("Root task", nil))
	if got, want := env.storedRequestHash(t, "CreateRootWorkItem", "idem-hash-legacy"), httpapi.SemanticHash("CreateRootWorkItem", scope, []byte(legacyCanonical), "", 0); got != want {
		t.Fatalf("hash of a contract-less body = %s, want %s (the pre-V6-04B canonical form must be untouched)", got, want)
	}

	nullBody := rootBody("Root task", nil)
	nullBody["contract"] = nil
	env.mustCreateRoot(t, "idem-hash-null", nullBody)
	if got, want := env.storedRequestHash(t, "CreateRootWorkItem", "idem-hash-null"), httpapi.SemanticHash("CreateRootWorkItem", scope, []byte(legacyCanonical), "", 0); got != want {
		t.Fatalf("hash of a \"contract\":null body = %s, want %s (null is the same request as omitted)", got, want)
	}

	const withContractCanonical = `{"title":"Root task","initialScope":[{"repositoryId":"repo-a","access":"WRITE","reason":"root scope"}],` +
		`"contract":{"schemaVersion":1,"behavior":"Users can export a report as CSV",` +
		`"acceptanceCriteria":[{"description":"the CSV has a header row","verificationRef":"go test ./report/..."}],` +
		`"verificationSpec":"run the report tests","riskLevel":"LOW","exclusions":["PDF export"]}}`
	env.mustCreateRoot(t, "idem-hash-contract", rootBody("Root task", contractJSON()))
	if got, want := env.storedRequestHash(t, "CreateRootWorkItem", "idem-hash-contract"), httpapi.SemanticHash("CreateRootWorkItem", scope, []byte(withContractCanonical), "", 0); got != want {
		t.Fatalf("hash of a contract body = %s, want %s (the contract must be part of what is hashed)", got, want)
	}
	if env.storedRequestHash(t, "CreateRootWorkItem", "idem-hash-contract") == env.storedRequestHash(t, "CreateRootWorkItem", "idem-hash-legacy") {
		t.Fatal("a body with a contract and the same body without one hash identically — the contract is not in the hash")
	}

	// Formatting/ordering of the JSON the client happened to send must not
	// change the hash: a hand-written, differently ordered body is the same
	// request as the map-built one above.
	raw := `{"contract":{"exclusions":["PDF export"],"riskLevel":"LOW","verificationSpec":"run the report tests",` +
		`"acceptanceCriteria":[{"verificationRef":"go test ./report/...","description":"the CSV has a header row"}],` +
		`"behavior":"Users can export a report as CSV","schemaVersion":1},` +
		`"initialScope":[{"reason":"root scope","access":"WRITE","repositoryId":"repo-a"}],"title":"Root task"}`
	req, err := http.NewRequest(http.MethodPost, env.base+"/projects/project-1/work-items", strings.NewReader(raw))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, testSessionToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpapi.IdempotencyKeyHeader, "idem-hash-reordered")
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatalf("POST reordered body: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("reordered body status = %d, want 201", resp.StatusCode)
	}
	if env.storedRequestHash(t, "CreateRootWorkItem", "idem-hash-reordered") != env.storedRequestHash(t, "CreateRootWorkItem", "idem-hash-contract") {
		t.Fatal("the same contract sent with different key order/whitespace hashed differently")
	}
}

// TestContractOmittedAndZeroValuesCanonicalizeAlike: "given but zero" and
// "omitted" mean the same thing throughout the contract, so they must hash
// identically — a client that always sends every key, empty, must be able to
// retry a request that omitted them.
func TestContractOmittedAndZeroValuesCanonicalizeAlike(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	partial := map[string]any{"behavior": "just a behavior"}
	env.mustCreateRoot(t, "idem-zero", rootBody("Root task", partial))

	verbose := map[string]any{
		"schemaVersion": 0, "behavior": "just a behavior", "acceptanceCriteria": []any{}, "verificationSpec": "",
		"riskLevel": "", "exclusions": []any{}, "workflowVersionId": "",
	}
	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-zero", "", rootBody("Root task", verbose))
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("retry with explicit zero values: status = %d, want 200 (a replay — same request), body=%s", resp.StatusCode, raw)
	}
	resp.Body.Close()
}

// TestContractBodyDeclaresNoStateFields is a cheap wire-shape pin: the JSON a
// contract-bearing detail returns carries exactly the documented keys, and
// nothing status-shaped, so the request/response vocabulary stays the one the
// V6-14 acceptance test was written against.
func TestContractBodyDeclaresNoStateFields(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	env.publishWorkflowVersion(t, "project-1", "wf-def-1", "wf-v-1")

	contract := contractJSON()
	contract["workflowVersionId"] = "wf-v-1"
	root := env.mustCreateRoot(t, "idem-root", rootBody("Root task", contract))

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID, "", "", nil)
	var decoded struct {
		Contract map[string]json.RawMessage `json:"contract"`
	}
	decodeInto(t, resp, &decoded)
	var keys []string
	for k := range decoded.Contract {
		keys = append(keys, k)
	}
	want := map[string]bool{
		"schemaVersion": true, "behavior": true, "acceptanceCriteria": true, "verificationSpec": true,
		"riskLevel": true, "exclusions": true, "workflowVersionId": true,
	}
	if len(keys) != len(want) {
		t.Fatalf("contract keys = %v, want exactly %v", keys, want)
	}
	for _, k := range keys {
		if !want[k] {
			t.Fatalf("unexpected contract key %q in %v", k, keys)
		}
	}
}
