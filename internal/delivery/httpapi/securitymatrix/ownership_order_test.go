package securitymatrix

// "Reload target ownership BEFORE the receipt/stream" (V6-13's own Thực
// hiện line) has a second, less obvious half: a command must also reload
// ownership before consulting any OTHER authority whose answer depends on
// state the caller has not been authorized to observe. This file is the
// regression proof for the one place in the composed route set where that
// ordering was actually wrong.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// TestRequestWorkspaceSetRelease_ForeignFamilyIsIndistinguishableRegardlessOfItsReleaseSetState
// is the regression test for the one REAL gap this task's own matrix found
// (see baocaov6checklist.md's V6-13 section and
// internal/app/workspacerelease/commands.go's own ownership-pre-check
// comment).
//
// POST /projects/{projectId}/workspace-sets/{familyId}/release used to
// consult ports.ReleaseEligibilityAuthority — which takes a bare FamilyID
// and answers from EVERY ReleaseSet that family has, with no project
// scoping of its own — BEFORE the WorkspaceSet's own owner was ever checked
// against the projectId in the URL. That made the response depend on a
// foreign project's ReleaseSet state:
//
//   - a foreign family whose latest ReleaseSet was still CREATED (or a
//     family that does not exist at all) answered 403 FORBIDDEN, and
//   - a foreign family whose latest ReleaseSet had been SEALED sailed past
//     the eligibility check and only then hit the cross-project check
//     inside the transaction, answering the leakage-normalized 404.
//
// A caller guessing family identifiers could therefore tell, purely from
// 403-vs-404, that a guessed id names a real family in a project they may
// not see AND that its release has been sealed — exactly the enumeration
// V6-02A's leakage policy exists to prevent.
//
// The three probes below must now all answer identically.
func TestRequestWorkspaceSetRelease_ForeignFamilyIsIndistinguishableRegardlessOfItsReleaseSetState(t *testing.T) {
	e := newEnv(t)

	// beta's own ReleaseSet starts CREATED (the fixture's own
	// work.CreateReleaseSet). Seal it, so the "foreign family, sealed
	// release set" case — the one that used to answer differently — is
	// real rather than hypothetical.
	e.sealReleaseSet(t, e.beta)

	// Sanity: alpha's own ReleaseSet is deliberately left CREATED, so the
	// two foreign probes below differ in exactly the state that used to
	// change the answer.
	releasePath := func(projectID, familyID string) string {
		return "/projects/" + projectID + "/workspace-sets/" + familyID + "/release"
	}
	headers := map[string]string{httpapi.IfMatchHeader: httpapi.ETagFromVersion(1)}

	foreignSealed := e.do(t, req{
		Method: http.MethodPost, Path: releasePath(e.alpha.ProjectID, e.beta.FamilyID),
		IdempotencyKey: "sm-order-foreign-sealed", Body: `{}`, Headers: headers,
	})
	neverExisted := e.do(t, req{
		Method: http.MethodPost, Path: releasePath(e.alpha.ProjectID, fabricatedID),
		IdempotencyKey: "sm-order-absent", Body: `{}`, Headers: headers,
	})

	if foreignSealed.Status != neverExisted.Status || foreignSealed.Body != neverExisted.Body {
		t.Errorf("a foreign project's SEALED family answered %d %s but a never-issued family answered %d %s — "+
			"the difference is a cross-project existence/state oracle",
			foreignSealed.Status, truncate(foreignSealed.Body), neverExisted.Status, truncate(neverExisted.Body))
	}
	if foreignSealed.Status != http.StatusNotFound || strings.TrimSpace(foreignSealed.Body) != hiddenResourceBody {
		t.Errorf("a foreign project's family must answer the leakage-normalized 404, got %d %s",
			foreignSealed.Status, truncate(foreignSealed.Body))
	}

	// The in-project behaviour must be untouched: alpha's OWN family, whose
	// ReleaseSet is still CREATED, still gets the real, informative
	// eligibility refusal — the fix reorders the ownership check ahead of
	// the eligibility check, it does not remove or weaken the latter.
	ownUnsealed := e.do(t, req{
		Method: http.MethodPost, Path: releasePath(e.alpha.ProjectID, e.alpha.FamilyID),
		IdempotencyKey: "sm-order-own-unsealed", Body: `{}`, Headers: headers,
	})
	if ownUnsealed.Status != http.StatusForbidden {
		t.Errorf("a caller's OWN family with an unsealed release set must still get the real eligibility refusal, got %d %s",
			ownUnsealed.Status, truncate(ownUnsealed.Body))
	}
}

// sealReleaseSet drives fx's own ReleaseSet to SEALED through the real
// internal/app/work.SealReleaseSet command — never a direct state write.
func (e *env) sealReleaseSet(t *testing.T, fx projectFixture) {
	t.Helper()
	cmd := ports.Command{
		ID: "cmd-seal-" + fx.ProjectID, IdempotencyKey: "idem-seal-" + fx.ProjectID, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(fx.ProjectID), Type: "SealReleaseSet",
		RequestHash: "hash-seal-" + fx.ProjectID, ExpectedVersion: 1,
	}
	if _, err := workapp.SealReleaseSet(context.Background(), e.uow, cmd, workapp.SealReleaseSetRequest{
		ReleaseSetID: fx.ReleaseSetID,
	}); err != nil {
		t.Fatalf("SealReleaseSet(%s): %v", fx.ProjectID, err)
	}
}
