package securitymatrix

// Replay and principal half of V6-13's own matrix: "reload target ownership
// before receipt/stream", "current role on replay" and ADR-028's own
// non-spoofable LocalPrincipalSnapshot.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// approverRole is the role the seeded ApprovalRequest below authorizes —
// deliberately the SAME role testPrincipal() already carries, so the
// "authorized" and "revoked" servers differ in exactly one thing.
const approverRole = "operator"

// seedEscalatedApprovalRequest creates one real ApprovalRequest on fx's own
// Run/NodeRun (tx.Approvals().CreateApprovalRequest — the same accessor
// advanceRunTx uses in production) and then drives it to ESCALATED through
// the real tx.Approvals().TransitionApprovalRequest CAS the production
// runtime.ApprovalTimeoutHandler itself performs.
//
// ESCALATED rather than PENDING is deliberate: runtime.ResolveApproval's own
// non-PENDING branch records a real receipt without needing a real APPROVAL
// node/outcome allow-list to route through (advanceRunTx is never reached),
// so this fixture gets a genuine, command-written receipt — the thing every
// replay assertion below needs — out of a real code path rather than a
// hand-inserted receipt row. Returns the ApprovalRequest id and its current
// version.
func (e *env) seedEscalatedApprovalRequest(t *testing.T, fx projectFixture, suffix string) (string, uint64) {
	t.Helper()
	ctx := context.Background()
	id := "approval-" + suffix
	request, err := runtimedomain.NewApprovalRequest(
		runtimedomain.ApprovalRequestID(id), project.ProjectID(fx.ProjectID),
		runtimedomain.WorkflowRunID(fx.RunID), runtimedomain.NodeRunID(fx.NodeRunID), "approval-node",
		[]string{approverRole}, nil, time.Now().UTC().Add(time.Hour), "escalated",
	)
	if err != nil {
		t.Fatalf("NewApprovalRequest(%s): %v", suffix, err)
	}
	var version uint64
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Approvals().CreateApprovalRequest(ctx, request); err != nil {
			return err
		}
		updated, err := tx.Approvals().TransitionApprovalRequest(ctx, ports.TransitionApprovalRequestRequest{
			ApprovalRequestID: id, ExpectedState: runtimedomain.ApprovalRequestPending, ExpectedVersion: 1,
			NextState: runtimedomain.ApprovalRequestEscalated,
		})
		if err != nil {
			return err
		}
		version = updated.Version
		return nil
	}); err != nil {
		t.Fatalf("seed escalated approval request(%s): %v", suffix, err)
	}
	return id, version
}

func resolveApprovalPath(runID, approvalRequestID string) string {
	return "/runs/" + runID + "/approval-requests/" + approvalRequestID + "/resolve"
}

// TestReceiptReplay_RevokedRoleCannotReplayItsOwnEarlierDecision is the
// regression test for the SECOND real gap this task's matrix found (see
// baocaov6checklist.md's V6-13 section, plus the ownership/authorization
// ordering comments in internal/app/runtime/approval.go and
// internal/delivery/httpapi/decision/approval.go).
//
// The design doc's own §1 contract point 3 is explicit: "Authorization chạy
// lại cả khi receipt replay, nên role bị thu hồi không thể dùng replay để
// đọc/mutate." Both the HTTP handler's own receipt fast path AND
// runtime.ResolveApproval's own in-transaction receipt lookup used to run
// BEFORE the ActorRoles/AuthorizedRoles intersection check, so an actor
// whose authorizing role had since been revoked could still resend their
// original Idempotency-Key and read the stored decision back verbatim.
//
// Both orderings are now reversed, and this test drives the whole thing
// over real HTTP against two real servers differing only in their bound
// principal's Roles.
func TestReceiptReplay_RevokedRoleCannotReplayItsOwnEarlierDecision(t *testing.T) {
	e := newEnv(t)
	approvalID, version := e.seedEscalatedApprovalRequest(t, e.alpha, "alpha")

	const key = "sm-replay-approval-key"
	body := `{"outcome":"approved","reason":"looks good"}`
	headers := map[string]string{httpapi.IfMatchHeader: httpapi.ETagFromVersion(version)}
	path := resolveApprovalPath(e.alpha.RunID, approvalID)

	first := e.do(t, req{Method: http.MethodPost, Path: path, Body: body, IdempotencyKey: key, Headers: headers})
	if first.Status != http.StatusOK {
		t.Fatalf("first resolve: status = %d body = %s, want 200", first.Status, truncate(first.Body))
	}

	// Same actor, same role: a genuine replay still works exactly as before
	// — the fix reorders authorization ahead of the receipt, it does not
	// break idempotency. The replayed body is the receipt's own stored
	// ResultJSON verbatim (httpapi.WriteReceiptReplay), which is the
	// command result WITHOUT the fresh response's own advisory
	// `validActions` wrapper — pre-existing, documented behaviour, so the
	// assertion is on the decision content both must carry.
	replay := e.do(t, req{Method: http.MethodPost, Path: path, Body: body, IdempotencyKey: key, Headers: headers})
	if replay.Status != http.StatusOK {
		t.Fatalf("replay under the SAME role: status = %d body = %s, want 200", replay.Status, truncate(replay.Body))
	}
	for _, want := range []string{`"approvalRequestId":"` + approvalID + `"`, `"state":"ESCALATED"`} {
		if !strings.Contains(replay.Body, want) {
			t.Fatalf("replay under the SAME role: body = %s, want it to contain %s", truncate(replay.Body), want)
		}
	}

	// Same actor, role revoked (a restart under an edited trusted principal
	// config — the only way a principal can ever change, ADR-028).
	revoked := e.forkAs(t, httpapi.LocalPrincipalSnapshot{Actor: testPrincipal().Actor, Roles: []string{"viewer"}})
	denied := revoked.do(t, req{Method: http.MethodPost, Path: path, Body: body, IdempotencyKey: key, Headers: headers})
	if denied.Status != http.StatusForbidden {
		t.Errorf("replay under a REVOKED role: status = %d body = %s, want 403 — a revoked role must not be able to "+
			"use replay to read back its own earlier decision (§1 contract point 3)", denied.Status, truncate(denied.Body))
	}
	if strings.Contains(denied.Body, `"state"`) || strings.Contains(denied.Body, approvalID) {
		t.Errorf("replay under a REVOKED role returned decision content: %s", truncate(denied.Body))
	}
}

// TestReceiptReplay_TargetOwnershipIsReloadedBeforeTheReceiptIsConsulted is
// V6-13's own "reload target ownership before receipt/stream" bullet: a
// caller holding a perfectly valid Idempotency-Key whose receipt this
// server really has stored must still be refused when the URL names a
// target that does not belong where the URL claims — the reload has to
// happen first, otherwise the receipt would answer for a resource the
// caller was never authorized against.
func TestReceiptReplay_TargetOwnershipIsReloadedBeforeTheReceiptIsConsulted(t *testing.T) {
	e := newEnv(t)
	approvalID, version := e.seedEscalatedApprovalRequest(t, e.alpha, "alpha")

	const key = "sm-replay-ownership-key"
	body := `{"outcome":"approved"}`
	headers := map[string]string{httpapi.IfMatchHeader: httpapi.ETagFromVersion(version)}

	stored := e.do(t, req{
		Method: http.MethodPost, Path: resolveApprovalPath(e.alpha.RunID, approvalID),
		Body: body, IdempotencyKey: key, Headers: headers,
	})
	if stored.Status != http.StatusOK {
		t.Fatalf("seeding the receipt: status = %d body = %s, want 200", stored.Status, truncate(stored.Body))
	}

	// Exactly the same actor, scope, Idempotency-Key, body and If-Match —
	// only the {runId} segment is another project's Run. The stored receipt
	// must never be reached.
	crossRun := e.do(t, req{
		Method: http.MethodPost, Path: resolveApprovalPath(e.beta.RunID, approvalID),
		Body: body, IdempotencyKey: key, Headers: headers,
	})
	if crossRun.Status != http.StatusNotFound || strings.TrimSpace(crossRun.Body) != hiddenResourceBody {
		t.Errorf("a stored Idempotency-Key replayed against a FOREIGN run: status = %d body = %s, want the "+
			"leakage-normalized 404 — the target reload must run before the receipt lookup",
			crossRun.Status, truncate(crossRun.Body))
	}

	// And against a Run that never existed at all — byte-identical, so the
	// two cases are indistinguishable.
	unknownRun := e.do(t, req{
		Method: http.MethodPost, Path: resolveApprovalPath(fabricatedID, approvalID),
		Body: body, IdempotencyKey: key, Headers: headers,
	})
	if unknownRun.Status != crossRun.Status || unknownRun.Body != crossRun.Body {
		t.Errorf("foreign run answered %d %s but never-issued run answered %d %s — the difference is an existence oracle",
			crossRun.Status, truncate(crossRun.Body), unknownRun.Status, truncate(unknownRun.Body))
	}
}

// TestReceiptReplay_IsScopedToTheCommittingActor proves a receipt is keyed
// by the CURRENT actor (httpapi.LookupReceipt's own actor argument), so a
// different operator can never read back — or be silently short-circuited
// by — someone else's committed result under the same Idempotency-Key.
func TestReceiptReplay_IsScopedToTheCommittingActor(t *testing.T) {
	e := newEnv(t)
	approvalID, version := e.seedEscalatedApprovalRequest(t, e.alpha, "alpha")

	const key = "sm-replay-actor-key"
	body := `{"outcome":"approved"}`
	headers := map[string]string{httpapi.IfMatchHeader: httpapi.ETagFromVersion(version)}
	path := resolveApprovalPath(e.alpha.RunID, approvalID)

	if got := e.do(t, req{Method: http.MethodPost, Path: path, Body: body, IdempotencyKey: key, Headers: headers}); got.Status != http.StatusOK {
		t.Fatalf("first resolve: status = %d body = %s, want 200", got.Status, truncate(got.Body))
	}

	// A DIFFERENT actor, still holding the authorizing role: the command
	// runs again for real (it is idempotent and the request is already
	// non-PENDING, so the outcome is the same shape) rather than replaying
	// the other actor's receipt. What matters here is that it is not
	// SHORT-CIRCUITED by a receipt that was never this actor's.
	other := e.forkAs(t, httpapi.LocalPrincipalSnapshot{Actor: "a-different-operator", Roles: []string{approverRole}})
	got := other.do(t, req{Method: http.MethodPost, Path: path, Body: body, IdempotencyKey: key, Headers: headers})
	if got.Status != http.StatusOK {
		t.Fatalf("second actor: status = %d body = %s, want 200", got.Status, truncate(got.Body))
	}
	assertReceiptExistsForActor(t, e, "a-different-operator", e.alpha.ProjectID, key)
}

// assertReceiptExistsForActor reads the real receipt store directly (a
// read-only ports.Tx, never a fabricated row) to prove the second actor's
// call committed its OWN receipt rather than reusing the first actor's.
func assertReceiptExistsForActor(t *testing.T, e *env, actor, projectID, key string) {
	t.Helper()
	ctx := context.Background()
	var found bool
	if err := e.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		_, ok, err := tx.Receipts().Load(ctx, actor, ports.ProjectScope(projectID), key, "ResolveApproval")
		found = ok
		return err
	}); err != nil {
		t.Fatalf("load receipt for %s: %v", actor, err)
	}
	if !found {
		t.Errorf("actor %q has no receipt of its own for key %q — its call was short-circuited by another actor's receipt", actor, key)
	}
}

// TestPrincipalCannotBeInfluencedByTheRequest is ADR-028's own "Actor và
// ActorRoles là authentication context, không phải input tự khai... HTTP
// body/header và CLI flag MUST NOT được phép override actor/roles",
// exercised end to end: a caller that sends an actor/role of its own — in
// the body, in a header, or in a query parameter — gets exactly the same
// answer as one that sends none, because httpapi.BindPrincipal never reads
// the request at all.
func TestPrincipalCannotBeInfluencedByTheRequest(t *testing.T) {
	e := newEnv(t)
	approvalID, version := e.seedEscalatedApprovalRequest(t, e.alpha, "alpha")

	// The bound principal's roles do NOT include "approver-elevated"; the
	// seeded request authorizes only approverRole. Start from a server whose
	// principal has been stripped of it, so any successful spoof would be
	// unmistakable.
	revoked := e.forkAs(t, httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"viewer"}})
	headers := map[string]string{httpapi.IfMatchHeader: httpapi.ETagFromVersion(version)}
	path := resolveApprovalPath(e.alpha.RunID, approvalID)

	baseline := revoked.do(t, req{
		Method: http.MethodPost, Path: path, Body: `{"outcome":"approved"}`,
		IdempotencyKey: "sm-spoof-baseline", Headers: headers,
	})
	if baseline.Status != http.StatusForbidden {
		t.Fatalf("baseline (no spoof attempt): status = %d body = %s, want 403", baseline.Status, truncate(baseline.Body))
	}

	spoofs := []struct {
		name    string
		body    string
		path    string
		headers map[string]string
	}{
		{
			name: "body actor/roles fields",
			body: `{"outcome":"approved","actor":"local-operator","roles":["` + approverRole + `"],"actorRoles":["` + approverRole + `"]}`,
			path: path, headers: headers,
		},
		{
			name: "X-Actor / X-Actor-Roles headers",
			body: `{"outcome":"approved"}`, path: path,
			headers: map[string]string{
				httpapi.IfMatchHeader: httpapi.ETagFromVersion(version),
				"X-Actor":             "local-operator",
				"X-Actor-Roles":       approverRole,
				"X-Roles":             approverRole,
			},
		},
		{
			name: "actor/roles query parameters",
			body: `{"outcome":"approved"}`,
			path: path + "?actor=local-operator&roles=" + approverRole, headers: headers,
		},
	}
	for i, s := range spoofs {
		t.Run(s.name, func(t *testing.T) {
			got := revoked.do(t, req{
				Method: http.MethodPost, Path: s.path, Body: s.body,
				IdempotencyKey: "sm-spoof-" + string(rune('a'+i)), Headers: s.headers,
			})
			if got.Status == http.StatusOK {
				t.Fatalf("a caller-supplied actor/role was HONORED: status 200 body = %s", truncate(got.Body))
			}
			if got.Status != http.StatusForbidden && got.Status != http.StatusBadRequest {
				t.Errorf("status = %d body = %s, want the same 403 the un-spoofed baseline got (or a 400 for a body this "+
					"route strictly rejects — either way, never a success)", got.Status, truncate(got.Body))
			}
		})
	}
}
