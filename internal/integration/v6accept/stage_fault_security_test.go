package v6accept

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// approvalOnlyWorkflowDocument mirrors internal/delivery/httpapi/decision's
// own fixture_test.go approvalDocument exactly (start -> gate(APPROVAL) ->
// end_approved|end_rejected|end_escalated), except AuthorizedRoles names
// "operator" — the exact role this stack's own default local-operator
// principal carries (client_test.go's bootstrap payload), so a REVOKED
// role scenario needs to change nothing else about the fixture, only the
// principal a later serve restart trusts.
func approvalOnlyWorkflowDocument(authorizedRole string, timeoutSeconds uint32) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "gate", Type: workflow.NodeApproval, Outcomes: []string{"approved", "rejected", "escalated"}, Approval: &workflow.ApprovalNodeConfig{
				AuthorizedRoles: []string{authorizedRole}, TimeoutSeconds: timeoutSeconds, EscalationOutcome: "escalated",
			}},
			{Key: "end_approved", Type: workflow.NodeEnd},
			{Key: "end_rejected", Type: workflow.NodeEnd},
			{Key: "end_escalated", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-gate", From: "start", Outcome: "next", To: "gate"},
			{Key: "gate-to-approved", From: "gate", Outcome: "approved", To: "end_approved"},
			{Key: "gate-to-rejected", From: "gate", Outcome: "rejected", To: "end_rejected"},
			{Key: "gate-to-escalated", From: "gate", Outcome: "escalated", To: "end_escalated"},
		},
	}
}

// writePrincipalConfig writes an ADR-028 trusted principal config file
// (the ONLY sanctioned way to change which actor/roles a real `aw serve`
// process trusts — cmd/aw/serve.go's own --principal-config doc comment)
// naming actor with roles, and returns its path.
func writePrincipalConfig(t *testing.T, dir, actor string, roles []string) string {
	t.Helper()
	quoted := make([]string, len(roles))
	for i, role := range roles {
		quoted[i] = `"` + role + `"`
	}
	content := `{"localPrincipal":{"actor":"` + actor + `","roles":[` + strings.Join(quoted, ",") + `]}}`
	path := filepath.Join(dir, "principal-"+strings.Join(roles, "-")+".json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write principal config %s: %v", path, err)
	}
	return path
}

// TestV6HTTPAcceptance_Fault_RoleDowngradeMidFlight is V6-14A scenario 8:
// "role downgrade mid-flight". There is no live role-mutation endpoint in
// this codebase (ADR-028: a principal is selected once, at process
// startup, from a trusted config file) — so "the role was revoked" is
// simulated the only real way it can happen: restarting the SAME serve
// process under a DIFFERENT --principal-config naming the SAME actor with
// a role that no longer authorizes the approval this actor itself
// resolved a moment ago, then REPLAYING that exact original decision's own
// receipt (same Idempotency-Key/body/If-Match) — never issuing a fresh
// call — under the downgraded principal.
//
// This is the identical property internal/delivery/httpapi/securitymatrix's
// own TestReceiptReplay_RevokedRoleCannotReplayItsOwnEarlierDecision
// already proved at the unit/in-process level (the real gap V6-13 found:
// both runtime.ResolveApproval's own in-transaction receipt lookup and the
// HTTP handler's own receipt fast path used to run BEFORE the
// ActorRoles/AuthorizedRoles intersection check) — this scenario proves
// the SAME property as a genuine two-process black-box journey instead,
// through a real `aw serve` restart under a real, different trusted
// principal file.
//
// Verify: the ORIGINAL resolve (under the "operator" role the approval's
// own AuthorizedRoles names) succeeds and actually moves the Run's
// APPROVAL node. The exact same replay under a restarted serve trusting
// only "viewer" for the SAME actor is rejected 403, and its body carries
// no decision content (contract §1 point 3: "role bị thu hồi không thể
// dùng replay để đọc/mutate").
func TestV6HTTPAcceptance_Fault_RoleDowngradeMidFlight(t *testing.T) {
	requireAcceptance(t)
	j := newFaultStack(t)
	j.createRootWorkItem(t, "fault-security-root")

	const authorizedRole = "operator" // the default local-operator principal's own role.
	wf := j.publishDefinition(t, "/projects/"+j.projectID, definition.KindWorkflow, "approval-workflow", "approval workflow",
		approvalOnlyWorkflowDocument(authorizedRole, 3600))
	childID := j.createChild(t, "fault-security-child", "v6-14a role downgrade fixture", wf)
	j.waitWorkspaceReady(t)
	j.markReady(t, childID)
	runID := j.startRun(t, childID, wf.versionID)

	type approvalView struct {
		ApprovalRequestID string `json:"approvalRequestId"`
		State             string `json:"state"`
		Version           uint64 `json:"version"`
	}
	var approval approvalView
	waitFor(t, "the run to reach a real, pending APPROVAL node", 30*time.Second, 200*time.Millisecond, func() bool {
		var detail struct {
			ApprovalRequests []approvalView `json:"approvalRequests"`
		}
		j.s.api.get(t, "/runs/"+runID).requireStatus(t, http.StatusOK).decode(t, &detail)
		for _, a := range detail.ApprovalRequests {
			if a.State == "PENDING" || a.State == "ESCALATED" {
				approval = a
				return true
			}
		}
		return false
	})

	resolvePath := "/runs/" + runID + "/approval-requests/" + approval.ApprovalRequestID + "/resolve"
	const key = "fault-security-resolve-key-1"
	body := map[string]string{"outcome": "approved", "reason": "v6-14a fault: original decision under the authorized role"}
	headers := withIfMatch(httpapi.ETagFromVersion(approval.Version))

	original := j.s.api.post(t, resolvePath, body, withIdempotencyKey(key), headers).requireStatus(t, http.StatusOK)
	if !strings.Contains(string(original.body), approval.ApprovalRequestID) {
		t.Fatalf("original resolve under the authorized role did not return the decision: %s", original.body)
	}

	// The role revocation: restart serve trusting the SAME actor with a
	// role that does NOT authorize this approval.
	j.s.principalConfigPath = writePrincipalConfig(t, j.s.root, "local-operator", []string{"viewer"})
	j.s.restartServeOnly(t)

	// The replay: the EXACT same Idempotency-Key/body/If-Match, never a
	// fresh call, now under the downgraded principal.
	replay := j.s.api.post(t, resolvePath, body, withIdempotencyKey(key), headers)
	if replay.status != http.StatusForbidden {
		t.Fatalf("replay of an already-committed approval decision under a REVOKED role: status = %d, want 403 (§1 contract point 3): %s",
			replay.status, tail(string(replay.body), 400))
	}
	if strings.Contains(string(replay.body), approval.ApprovalRequestID) || strings.Contains(string(replay.body), `"state"`) {
		t.Fatalf("replay under a REVOKED role returned decision content instead of a bare refusal: %s", tail(string(replay.body), 400))
	}

	// Restore the default principal so this stack's own t.Cleanup (a
	// graceful stop through the normal operator principal) behaves
	// exactly like every other scenario's.
	j.s.principalConfigPath = ""
	j.s.restartServeOnly(t)
}
