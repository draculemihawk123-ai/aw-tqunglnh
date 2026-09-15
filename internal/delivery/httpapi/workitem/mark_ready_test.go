package workitem_test

// Real HTTP round-trip coverage for V6-04A (docs/design/08-v6-api-projections.md
// V6-04A; ADR-028 §30): POST /work-items/{workItemId}/mark-ready. Mirrors
// workitem_test.go's own discipline exactly — a REAL httpapi.Server backed by
// a REAL *sqlite.Store, never a mock.

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// seedReadyEligibleWorkItem persists (via the REAL tx.Work().CreateTaskFamily/
// CreateWorkItem repository methods) a root WorkItem whose contract is
// complete enough to pass workdomain.ValidateReadinessGate — mirrors
// internal/app/work/mark_ready_sqlite_test.go's own seedReadyEligibleRootSQLite
// exactly (see that function's own doc comment for why this bypasses the
// thin CreateRootWorkItem command, which never populates contract fields).
func (e *testEnv) seedReadyEligibleWorkItem(t *testing.T, projectID, workItemID, familyID string) {
	t.Helper()
	ctx := context.Background()
	item := workdomain.WorkItem{
		ID: workdomain.WorkItemID(workItemID), ProjectID: project.ProjectID(projectID), Kind: workdomain.WorkItemRoot,
		FamilyID: workdomain.TaskFamilyID(familyID), SchemaVersion: 1, Title: "Ship the thing",
		Behavior: "Users can do the thing end to end",
		AcceptanceCriteria: []workdomain.AcceptanceCriterion{
			{Description: "the thing works", VerificationRef: "go test ./..."},
		},
		VerificationSpec: "run the full suite locally and in CI", RiskLevel: workdomain.RiskLevel("MEDIUM"),
		Status: workdomain.WorkItemBacklog, Version: 1,
	}
	family, err := workdomain.NewTaskFamily(workdomain.TaskFamilyID(familyID), item)
	if err != nil {
		t.Fatalf("NewTaskFamily: %v", err)
	}
	err = e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}
		_, err := tx.Work().CreateWorkItem(ctx, item)
		return err
	})
	if err != nil {
		t.Fatalf("seed ready-eligible work item %s: %v", workItemID, err)
	}
}

// TestMarkWorkItemReady_HappyPath_TransitionsToReady is the core positive
// path over real HTTP: a genuinely ready-eligible WorkItem transitions
// BACKLOG->READY, 200 OK, ETag reflects the bumped version.
func TestMarkWorkItemReady_HappyPath_TransitionsToReady(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedReadyEligibleWorkItem(t, "project-1", "work-item-1", "family-1")

	resp := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "idem-mark-1", `"1"`, map[string]any{})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("mark-ready status = %d, want 200, body=%s", resp.StatusCode, body)
	}
	if etag := resp.Header.Get("ETag"); etag != `"2"` {
		t.Fatalf("ETag = %q, want \"2\"", etag)
	}
	var result workapp.MarkWorkItemReadyResult
	decodeInto(t, resp, &result)
	if result.Status != "READY" || result.Version != 2 || result.WorkItemID != "work-item-1" || result.ProjectID != "project-1" {
		t.Fatalf("result = %+v, want Status=READY Version=2 WorkItemID=work-item-1 ProjectID=project-1", result)
	}

	getResp := env.do(t, http.MethodGet, "/projects/project-1/work-items/work-item-1", "", "", nil)
	var detail workapp.WorkItemDetail
	decodeInto(t, getResp, &detail)
	if detail.Status != "READY" || detail.Version != 2 {
		t.Fatalf("reloaded detail = %+v, want Status=READY Version=2", detail)
	}
}

// TestMarkWorkItemReady_Replay_SameKeySameResult proves a retry with the
// identical Idempotency-Key/If-Match/body replays the first call's own
// stored 200 result rather than re-dispatching (which would fail — the
// WorkItem is no longer BACKLOG the second time around).
func TestMarkWorkItemReady_Replay_SameKeySameResult(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedReadyEligibleWorkItem(t, "project-1", "work-item-1", "family-1")

	first := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "idem-mark-1", `"1"`, map[string]any{})
	var firstResult workapp.MarkWorkItemReadyResult
	decodeInto(t, first, &firstResult)
	if firstResult.Status != "READY" {
		t.Fatalf("first call Status = %q, want READY", firstResult.Status)
	}

	second := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "idem-mark-1", `"1"`, map[string]any{})
	if second.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("replayed call status = %d, want 200, body=%s", second.StatusCode, body)
	}
	var secondResult workapp.MarkWorkItemReadyResult
	decodeInto(t, second, &secondResult)
	if secondResult != firstResult {
		t.Fatalf("replayed result = %+v, want identical to first = %+v", secondResult, firstResult)
	}
}

// TestMarkWorkItemReady_StaleIfMatch_PreconditionFailed mirrors
// TestApproveScopeExpansion_StaleIfMatch_PreconditionFailed exactly: a fresh
// Idempotency-Key quoting a stale, already-superseded If-Match is rejected
// at the HTTP layer (412) before the application command ever dispatches.
func TestMarkWorkItemReady_StaleIfMatch_PreconditionFailed(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedReadyEligibleWorkItem(t, "project-1", "work-item-1", "family-1")

	first := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "idem-mark-1", `"1"`, map[string]any{})
	if first.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("first mark-ready status = %d, want 200, body=%s", first.StatusCode, body)
	}

	staleResp := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "idem-mark-stale", `"1"`, map[string]any{})
	if staleResp.StatusCode != http.StatusPreconditionFailed {
		body, _ := io.ReadAll(staleResp.Body)
		t.Fatalf("stale If-Match status = %d, want 412, body=%s", staleResp.StatusCode, body)
	}
}

// TestMarkWorkItemReady_AlreadyReady_FreshKeyCurrentIfMatchStillConflicts is
// this task's own explicit "READY conflict" spec line: a FRESH
// Idempotency-Key quoting the CURRENT (not stale) If-Match against an
// already-READY WorkItem still fails — this is not a version race, it is a
// genuine eligibility conflict the domain command itself catches
// (work.ErrWorkItemNotEligibleForReady), mapped to 409.
func TestMarkWorkItemReady_AlreadyReady_FreshKeyCurrentIfMatchStillConflicts(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedReadyEligibleWorkItem(t, "project-1", "work-item-1", "family-1")

	first := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "idem-mark-1", `"1"`, map[string]any{})
	if first.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("first mark-ready status = %d, want 200, body=%s", first.StatusCode, body)
	}

	// A fresh key, quoting the NOW-current version "2" — a well-behaved
	// client that correctly reloaded first. Still a real conflict: there is
	// no BACKLOG left to transition.
	secondResp := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "idem-mark-2", `"2"`, map[string]any{})
	if secondResp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(secondResp.Body)
		t.Fatalf("already-READY fresh-key status = %d, want 409, body=%s", secondResp.StatusCode, body)
	}
}

// TestMarkWorkItemReady_UnknownWorkItem_ReturnsNotFound covers this task's
// own "cross-project" Verify-line requirement adapted to this route's own
// shape: mark-ready has no {projectId} path segment at all (routes.go's own
// doc comment explains why) — ProjectID is derived SOLELY by reloading the
// WorkItem itself, so there is no client-claimed project to get wrong in the
// first place. The practical equivalent leakage-normalization proof is that
// a WorkItemID belonging to no project this caller can see (here, simply
// nonexistent) is indistinguishable from any other hidden resource: a plain
// 404, never a 500 or a schema/validation error that would leak whether the
// ID is well-formed.
func TestMarkWorkItemReady_UnknownWorkItem_ReturnsNotFound(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(t, http.MethodPost, "/work-items/does-not-exist/mark-ready", "idem-mark-1", `"1"`, map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unknown work item status = %d, want 404, body=%s", resp.StatusCode, body)
	}
}

// TestMarkWorkItemReady_ReadinessGateFailure_Returns409WithProblems is this
// task's own explicit "readiness" Verify-line requirement over real HTTP: a
// WorkItem created via the REAL createRootWorkItem endpoint (which, per
// commands.go's own doc comment, always starts with an empty contract) is
// rejected with a 409 whose error details carry the SAME problem vocabulary
// GetWorkItemReadiness would report for the identical WorkItem.
func TestMarkWorkItemReady_ReadinessGateFailure_Returns409WithProblems(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-create-1", "", map[string]any{
		"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")},
	})
	var root workapp.CreateRootWorkItemResult
	decodeInto(t, createResp, &root)

	readinessResp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/readiness", "", "", nil)
	var readiness workapp.WorkItemReadiness
	decodeInto(t, readinessResp, &readiness)
	if readiness.Ready {
		t.Fatal("freshly created root work item unexpectedly reports Ready=true")
	}

	markResp := env.do(t, http.MethodPost, "/work-items/"+root.WorkItemID+"/mark-ready", "idem-mark-1", `"1"`, map[string]any{})
	if markResp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(markResp.Body)
		t.Fatalf("readiness-failing mark-ready status = %d, want 409, body=%s", markResp.StatusCode, body)
	}
	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Details []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	decodeInto(t, markResp, &errBody)
	if len(errBody.Error.Details) != len(readiness.Problems) {
		t.Fatalf("error details = %+v, want exactly one per readiness problem = %v", errBody.Error.Details, readiness.Problems)
	}
	for i, want := range readiness.Problems {
		if errBody.Error.Details[i].Field != "readiness" || errBody.Error.Details[i].Message != want {
			t.Fatalf("error detail[%d] = %+v, want {field: readiness, message: %q}", i, errBody.Error.Details[i], want)
		}
	}
}

// TestMarkWorkItemReady_PayloadWithTargetStatusField_Rejected is this task's
// own explicit "payload target-status" Verify-line requirement: a client
// attempting to smuggle a status-like field into the body is rejected
// outright (strict JSON decode — DisallowUnknownFields), never silently
// accepted or ignored-but-still-succeeding in a way that could mislead a
// caller into thinking it had any effect.
func TestMarkWorkItemReady_PayloadWithTargetStatusField_Rejected(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedReadyEligibleWorkItem(t, "project-1", "work-item-1", "family-1")

	resp := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "idem-mark-1", `"1"`, map[string]any{
		"targetStatus": "DONE",
	})
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("targetStatus payload status = %d, want 400, body=%s", resp.StatusCode, body)
	}

	// And the WorkItem itself is entirely untouched — still BACKLOG,
	// Version 1.
	getResp := env.do(t, http.MethodGet, "/projects/project-1/work-items/work-item-1", "", "", nil)
	var detail workapp.WorkItemDetail
	decodeInto(t, getResp, &detail)
	if detail.Status != "BACKLOG" || detail.Version != 1 {
		t.Fatalf("work item after rejected targetStatus payload = %+v, want unchanged Status=BACKLOG Version=1", detail)
	}
}

// TestMarkWorkItemReady_RequiresIdempotencyKeyAndIfMatch proves this route
// follows the identical UPDATE-shaped preamble every other mutation on an
// existing resource in this package already requires
// (scope_expansion_commands.go's own prepareUpdateCommand): both headers are
// mandatory, missing either is a 400 before any real target reload/dispatch
// even matters.
func TestMarkWorkItemReady_RequiresIdempotencyKeyAndIfMatch(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedReadyEligibleWorkItem(t, "project-1", "work-item-1", "family-1")

	noKey := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "", `"1"`, map[string]any{})
	if noKey.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key status = %d, want 400", noKey.StatusCode)
	}

	noIfMatch := env.do(t, http.MethodPost, "/work-items/work-item-1/mark-ready", "idem-mark-1", "", map[string]any{})
	if noIfMatch.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing If-Match status = %d, want 400", noIfMatch.StatusCode)
	}
}
