package diagnostics_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func sequentialIDs() idsource.Source { return idsource.NewSequential("id") }

func mustReadBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// forceAttemptRunning forces attemptID from whatever state it is currently
// in to RUNNING via a direct TransitionExecutionAttempt poke — mirrors
// internal/app/runtime's own diagnostics_test.go orphanedRunningAttemptFixture
// exactly (see that file's own doc comment for the full reasoning: no
// command in this codebase reaches "RUNNING with an abandoned lease" except
// a genuine process crash under a live worker pool).
func forceAttemptRunning(t *testing.T, uow ports.UnitOfWork, attemptID string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionExecutionAttempt(context.Background(), ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: attempt.State, ExpectedVersion: attempt.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		return err
	})
	if err != nil {
		t.Fatalf("force attempt %s to RUNNING: %v", attemptID, err)
	}
}

// diagnosticsResponse is a locally-decoded wire shape mirroring
// diagnostics.RunDiagnosticsResponse's own JSON tags — deliberately a
// SEPARATE type from that package's own (unexported) response types, so
// this test file proves the ACTUAL wire contract (case-sensitive JSON key
// names) rather than merely re-using the production Go struct.
type diagnosticsResponse struct {
	RunID          string `json:"runId"`
	ProjectID      string `json:"projectId"`
	WorkItemID     string `json:"workItemId"`
	WorkItemStatus string `json:"workItemStatus"`
	RunState       string `json:"runState"`
	Blockers       []struct {
		BlockerID       string `json:"blockerId"`
		Type            string `json:"type"`
		State           string `json:"state"`
		SourceNodeRunID string `json:"sourceNodeRunId"`
		AdmissionReason bool   `json:"admissionReason"`
		ValidActions    []httpapi.ValidAction `json:"validActions"`
	} `json:"blockers"`
	OrphanedAttempts []struct {
		AttemptID             string `json:"attemptId"`
		NodeRunID             string `json:"nodeRunId"`
		HasWriteLease         bool   `json:"hasWriteLease"`
		RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	} `json:"orphanedAttempts"`
	OrphanedAttemptsTruncated bool `json:"orphanedAttemptsTruncated"`
	Providers                 []struct {
		AdapterBuildID     string `json:"adapterBuildId"`
		ProviderKey        string `json:"providerKey"`
		ProviderConfigured bool   `json:"providerConfigured"`
	} `json:"providers"`
	Isolation []struct {
		Tier        string `json:"tier"`
		Enforceable bool   `json:"enforceable"`
	} `json:"isolation"`
	RepositoryWorkspaces []struct {
		RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
		RepositoryID          string `json:"repositoryId"`
		State                  string `json:"state"`
		Generation             uint64 `json:"generation"`
	} `json:"repositoryWorkspaces"`
	ValidActions []httpapi.ValidAction `json:"validActions"`
}

// TestGetRunDiagnostics_HTTP_AdmissionBlocked_Success is this task's own
// real, end-to-end, sqlite-backed "genuinely blocked NodeRun" proof: a real
// GET through this package's own registered route, against
// blockedAdmissionFixture's own real admission-BLOCKED NodeRun.
func TestGetRunDiagnostics_HTTP_AdmissionBlocked_Success(t *testing.T) {
	fixture := blockedAdmissionFixture(t, "admission-blocked")
	server := newTestServer(t, fixture.UOW, process.NewIsolationChecker(), fixture.Registry)

	resp, err := http.Get(server.URL + "/projects/" + fixture.ProjectID + "/runs/" + fixture.RunID + "/diagnostics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, mustReadBody(t, resp))
	}
	var body diagnosticsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.RunID != fixture.RunID || body.ProjectID != fixture.ProjectID || body.WorkItemID != fixture.WorkItemID {
		t.Fatalf("body = %+v, want RunID=%s ProjectID=%s WorkItemID=%s", body, fixture.RunID, fixture.ProjectID, fixture.WorkItemID)
	}
	if len(body.Blockers) != 1 {
		t.Fatalf("len(body.Blockers) = %d, want 1: %+v", len(body.Blockers), body.Blockers)
	}
	b := body.Blockers[0]
	if b.BlockerID != fixture.BlockerID || b.Type != "ISOLATION_ENFORCEMENT_UNAVAILABLE" || b.State != "OPEN" || !b.AdmissionReason {
		t.Fatalf("blocker = %+v, want BlockerID=%s Type=ISOLATION_ENFORCEMENT_UNAVAILABLE State=OPEN AdmissionReason=true", b, fixture.BlockerID)
	}
	if b.SourceNodeRunID != fixture.NodeRunID {
		t.Fatalf("blocker.SourceNodeRunID = %s, want %s", b.SourceNodeRunID, fixture.NodeRunID)
	}
	if len(b.ValidActions) != 1 || b.ValidActions[0].OperationID != "retryBlockedActivation" {
		t.Fatalf("blocker.ValidActions = %+v, want exactly one retryBlockedActivation advisory action", b.ValidActions)
	}

	if len(body.Isolation) != 1 || body.Isolation[0].Enforceable {
		t.Fatalf("body.Isolation = %+v, want exactly one entry with Enforceable=false", body.Isolation)
	}
	if len(body.Providers) != 1 || !body.Providers[0].ProviderConfigured || body.Providers[0].ProviderKey != "claude" {
		t.Fatalf("body.Providers = %+v, want exactly one entry ProviderKey=claude ProviderConfigured=true", body.Providers)
	}

	// A live Run's own top-level advisory: cancelRun (WorkItem is BLOCKED,
	// not terminal) is offered; cancelWorkItem (WorkItem is BLOCKED, not
	// DONE/CANCELLED) is offered too.
	var sawCancelRun, sawCancelWorkItem bool
	for _, a := range body.ValidActions {
		switch a.OperationID {
		case "cancelRun":
			sawCancelRun = true
		case "cancelWorkItem":
			sawCancelWorkItem = true
		}
	}
	if !sawCancelRun || !sawCancelWorkItem {
		t.Fatalf("body.ValidActions = %+v, want both cancelRun and cancelWorkItem advisory actions", body.ValidActions)
	}

	if resp.Header.Get("ETag") == "" {
		t.Fatalf("ETag header is empty, want a value derived from the Run's own Version")
	}
}

// TestGetRunDiagnostics_HTTP_UnknownRun_ReturnsResourceHidden proves an
// unknown RunID gets the same leakage-normalized 404 every other query
// route in this codebase already gives (V6-02A's own policy).
func TestGetRunDiagnostics_HTTP_UnknownRun_ReturnsResourceHidden(t *testing.T) {
	store, uow := openDiagnosticsTestStore(t, "unknown-run")
	_ = store
	seedProject(t, uow, "project-1")
	server := newTestServer(t, uow, process.NewIsolationChecker(), agentregistry.Empty())

	resp, err := http.Get(server.URL + "/projects/project-1/runs/does-not-exist/diagnostics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeNotFound {
		t.Fatalf("error code = %s, want %s", errBody.Error.Code, httpapi.ErrorCodeNotFound)
	}
}

// TestGetRunDiagnostics_HTTP_WrongProject_ReturnsResourceHidden is this
// task's own "role/project matrix" Verify bullet, "wrong project" half: a
// Run that genuinely exists, but under a DIFFERENT project than the path
// names, is indistinguishable from a genuine not-found.
func TestGetRunDiagnostics_HTTP_WrongProject_ReturnsResourceHidden(t *testing.T) {
	store, uow := openDiagnosticsTestStore(t, "wrong-project")
	ids := sequentialIDs()
	started := startedRunFixture(t, uow, ids, "project-1", "repo-1")
	seedProject(t, uow, "project-2")
	_ = store
	server := newTestServer(t, uow, process.NewIsolationChecker(), agentregistry.Empty())

	resp, err := http.Get(server.URL + "/projects/project-2/runs/" + started.RunID + "/diagnostics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (scope-mismatch, leakage-normalized)", resp.StatusCode)
	}
}

// TestGetRunDiagnostics_HTTP_QuarantinedWorkspaceAndRunCancelledBlocker
// covers this task's own "quarantined" fixture, plus the non-admission
// "blocked" fixture (RUN_CANCELLED), end to end through a real HTTP GET.
func TestGetRunDiagnostics_HTTP_QuarantinedWorkspaceAndRunCancelledBlocker(t *testing.T) {
	store, uow := openDiagnosticsTestStore(t, "quarantined")
	ids := sequentialIDs()
	workItemID, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	var familyID, workspaceSetID string
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(context.Background(), workItemID)
		if err != nil {
			return err
		}
		familyID = string(item.FamilyID)
		set, err := tx.Work().GetWorkspaceSetByFamilyID(context.Background(), familyID)
		if err != nil {
			return err
		}
		workspaceSetID = string(set.ID)
		return nil
	}); err != nil {
		t.Fatalf("load work item/workspace set: %v", err)
	}
	quarantineExtraRepositoryWorkspace(t, uow, ids, "project-1", workspaceSetID)

	runID := strings.TrimSuffix(blockerID, "-run-cancelled-blocker")
	server := newTestServer(t, uow, process.NewIsolationChecker(), agentregistry.Empty())

	resp, err := http.Get(server.URL + "/projects/project-1/runs/" + runID + "/diagnostics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, mustReadBody(t, resp))
	}
	var body diagnosticsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(body.Blockers) != 1 || body.Blockers[0].BlockerID != blockerID || body.Blockers[0].Type != "RUN_CANCELLED" {
		t.Fatalf("blockers = %+v, want exactly one RUN_CANCELLED blocker %s", body.Blockers, blockerID)
	}
	if len(body.Blockers[0].ValidActions) != 1 || body.Blockers[0].ValidActions[0].OperationID != "resolveWorkItemBlocker" {
		t.Fatalf("blocker.ValidActions = %+v, want exactly one resolveWorkItemBlocker advisory action", body.Blockers[0].ValidActions)
	}

	var quarantined bool
	for _, rw := range body.RepositoryWorkspaces {
		if rw.State == "QUARANTINED" && rw.RepositoryID == "repo-2" {
			quarantined = true
		}
	}
	if !quarantined {
		t.Fatalf("repositoryWorkspaces = %+v, want a QUARANTINED entry for repo-2", body.RepositoryWorkspaces)
	}

	// The Run itself is already CANCELLED — cancelRun is no longer offered;
	// the WorkItem is BLOCKED (not DONE/CANCELLED) — cancelWorkItem still is.
	var sawCancelRun, sawCancelWorkItem bool
	for _, a := range body.ValidActions {
		switch a.OperationID {
		case "cancelRun":
			sawCancelRun = true
		case "cancelWorkItem":
			sawCancelWorkItem = true
		}
	}
	if sawCancelRun {
		t.Fatalf("body.ValidActions = %+v, want no cancelRun advisory action for an already-CANCELLED run", body.ValidActions)
	}
	if !sawCancelWorkItem {
		t.Fatalf("body.ValidActions = %+v, want a cancelWorkItem advisory action", body.ValidActions)
	}
}

// TestGetRunDiagnostics_HTTP_OrphanedAttempt_RealSqliteEvidence covers this
// task's own "lost" fixture against REAL sqlite queue/job/lease evidence
// (ListOrphanedRunningExecutionAttempts/GetWriteLeaseRepositoryWorkspaceForAttempt)
// — the SAME direct TransitionExecutionAttempt-to-RUNNING-with-an-unclaimed-job
// poke internal/app/runtime's own diagnostics_test.go already establishes
// and documents at length (no command in this codebase reaches this state
// except a genuine process crash under a live worker pool), run here
// against real sqlite instead of the fake so
// GetWriteLeaseRepositoryWorkspaceForAttempt's own REAL implementation
// (a documented stub in the fake — see that package's own doc comment) is
// exercised for real too, even though this particular attempt never
// acquired one (HasWriteLease=false is itself the real, correctly-computed
// answer here, not a stub default).
func TestGetRunDiagnostics_HTTP_OrphanedAttempt_RealSqliteEvidence(t *testing.T) {
	fixture := blockedAdmissionFixture(t, "orphaned-real")
	// The attempt is already BLOCKED (fixture setup) — force it to RUNNING
	// directly; ListOrphanedRunningExecutionAttempts only ever considers the
	// CURRENT State, so forcing RUNNING is sufficient regardless of prior
	// history. Its own EXECUTE_NODE job was claimed with a deliberately
	// short lease (claimJobLeaseTTL, fixture_test.go) that nothing in this
	// test ever renews — real wall-clock time is what eventually makes it
	// genuinely expired, exactly the real condition this query's own
	// evidence-based definition depends on.
	forceAttemptRunning(t, fixture.UOW, fixture.AttemptID)

	server := newTestServer(t, fixture.UOW, process.NewIsolationChecker(), fixture.Registry)

	deadline := time.Now().Add(15 * time.Second)
	var body diagnosticsResponse
	for {
		resp, err := http.Get(server.URL + "/projects/" + fixture.ProjectID + "/runs/" + fixture.RunID + "/diagnostics")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, mustReadBody(t, resp))
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		if len(body.OrphanedAttempts) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no orphaned attempt appeared within the deadline; last body = %+v", body)
		}
		time.Sleep(200 * time.Millisecond)
	}

	if len(body.OrphanedAttempts) != 1 {
		t.Fatalf("len(body.OrphanedAttempts) = %d, want 1: %+v", len(body.OrphanedAttempts), body.OrphanedAttempts)
	}
	oa := body.OrphanedAttempts[0]
	if oa.AttemptID != fixture.AttemptID || oa.NodeRunID != fixture.NodeRunID {
		t.Fatalf("orphaned attempt = %+v, want AttemptID=%s NodeRunID=%s", oa, fixture.AttemptID, fixture.NodeRunID)
	}
	if oa.HasWriteLease || oa.RepositoryWorkspaceID != "" {
		t.Fatalf("orphaned attempt = %+v, want HasWriteLease=false (real sqlite answer, no lease was ever acquired)", oa)
	}
}

// TestGetRunDiagnostics_HTTP_ArbitraryRoleStillReads is this task's own
// "role/project matrix" Verify bullet, "wrong role" half: this package's
// own doc comment documents WHY this query is intentionally not role-gated
// (no per-project member-role concept exists anywhere in this codebase to
// gate a read against) — this test proves that documented intent directly,
// with a principal holding a role that has no relationship whatsoever to
// this Run/WorkItem/Project.
func TestGetRunDiagnostics_HTTP_ArbitraryRoleStillReads(t *testing.T) {
	store, uow := openDiagnosticsTestStore(t, "arbitrary-role")
	ids := sequentialIDs()
	started := startedRunFixture(t, uow, ids, "project-1", "repo-1")
	_ = store

	principal := httpapi.LocalPrincipalSnapshot{Actor: "some-other-actor", Roles: []string{"unrelated-role-never-granted-anything"}}
	server := newTestServerWithPrincipal(t, uow, process.NewIsolationChecker(), agentregistry.Empty(), principal)

	resp, err := http.Get(server.URL + "/projects/project-1/runs/" + started.RunID + "/diagnostics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (this query is not role-gated); body = %s", resp.StatusCode, mustReadBody(t, resp))
	}
}

// TestGetRunDiagnostics_HTTP_NeverExposesProcessOrSecretShapedFields is this
// task's own explicit, named prohibition proved directly against the RAW
// wire JSON (not just the Go struct field names internal/app/runtime's own
// diagnostics_test.go already scans) — a genuinely admission-blocked run
// (the richest fixture this package has) must never contain a JSON key or
// string value naming a PID, argv, cwd/working directory, or a secret.
func TestGetRunDiagnostics_HTTP_NeverExposesProcessOrSecretShapedFields(t *testing.T) {
	fixture := blockedAdmissionFixture(t, "redaction-proof")
	server := newTestServer(t, fixture.UOW, process.NewIsolationChecker(), fixture.Registry)

	resp, err := http.Get(server.URL + "/projects/" + fixture.ProjectID + "/runs/" + fixture.RunID + "/diagnostics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	raw := mustReadBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, raw)
	}

	lower := strings.ToLower(raw)
	forbidden := []string{
		`"pid"`, `"argv"`, `"cwd"`, `"workingdirectory"`, `"workingdir"`,
		`"secret"`, `"credential"`, `"password"`, `"apikey"`, `"executablepath"`,
	}
	for _, bad := range forbidden {
		if strings.Contains(lower, bad) {
			t.Fatalf("response body contains forbidden key/value %q — this task's own explicit PID/argv/cwd/secret prohibition; body = %s", bad, raw)
		}
	}
}
