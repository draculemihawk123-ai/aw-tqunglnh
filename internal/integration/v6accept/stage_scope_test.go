package v6accept

import (
	"net/http"
	"testing"
)

// scopeExpansion requests, decides and verifies one scope expansion for the
// family: the grant is added only by an explicit human decision, the family's
// scope version advances, and an already-decided request cannot be decided
// again.
func (j *journey) scopeExpansion(t *testing.T) {
	api := j.s.api
	base := "/projects/" + j.projectID
	familyURL := base + "/task-families/" + j.familyID

	var before struct {
		ScopeVersion uint64 `json:"scopeVersion"`
	}
	api.get(t, familyURL).requireStatus(t, http.StatusOK).decode(t, &before)

	requested := api.post(t, familyURL+"/scope-expansions", map[string]any{
		"requestedGrants": []map[string]any{{"repositoryId": j.repositoryID, "access": "WRITE", "pathScopes": []string{"docs/"}, "reason": "documentation change"}},
		"reason":          "the change needs to touch docs/",
	}).requireStatus(t, http.StatusCreated)
	var request struct {
		RequestID string `json:"requestId"`
	}
	requested.decode(t, &request)
	if request.RequestID == "" {
		t.Fatalf("scope expansion request returned no requestId: %s", requested.body)
	}
	t.Logf("scope expansion requested: %s", tail(string(requested.body), 400))

	detail := api.get(t, base+"/scope-expansions/"+request.RequestID).requireStatus(t, http.StatusOK)
	approved := api.post(t, base+"/scope-expansions/"+request.RequestID+"/approve", map[string]any{}, withIfMatch(detail.etag())).requireStatus(t, http.StatusOK)
	t.Logf("scope expansion approved: %s", tail(string(approved.body), 400))

	var after struct {
		ScopeVersion uint64 `json:"scopeVersion"`
	}
	api.get(t, familyURL).requireStatus(t, http.StatusOK).decode(t, &after)
	if after.ScopeVersion != before.ScopeVersion+1 {
		t.Fatalf("family scopeVersion = %d after approval, want %d", after.ScopeVersion, before.ScopeVersion+1)
	}

	decided := api.get(t, base+"/scope-expansions/"+request.RequestID).requireStatus(t, http.StatusOK)
	if again := api.post(t, base+"/scope-expansions/"+request.RequestID+"/reject", map[string]any{"reason": "too late"}, withIfMatch(decided.etag())); again.status < 400 {
		t.Fatalf("rejecting an already-approved request succeeded (%d): %s", again.status, again.body)
	}
}
