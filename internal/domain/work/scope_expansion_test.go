package work

import (
	"testing"
	"time"
)

func validGrant() RequestedGrant {
	return RequestedGrant{RepositoryID: "repo-2", Access: RepositoryWrite, PathScopes: []string{"services/billing"}, Reason: "need billing repo"}
}

func TestNewScopeExpansionRequest_HappyPath(t *testing.T) {
	t.Parallel()

	family, _ := familyFixture(t)
	req, err := NewScopeExpansionRequest("req-1", family, []RequestedGrant{validGrant()}, "expand for billing work", nil, "requester-1", time.Now())
	if err != nil {
		t.Fatalf("NewScopeExpansionRequest: %v", err)
	}
	if req.Status != ScopeExpansionPending {
		t.Fatalf("Status = %q, want PENDING", req.Status)
	}
	if req.FamilyID != family.ID || req.ProjectID != family.ProjectID {
		t.Fatalf("request did not inherit family ownership: %#v", req)
	}
	if req.Version != 1 {
		t.Fatalf("Version = %d, want 1", req.Version)
	}
	if len(req.RequestedGrants) != 1 || req.RequestedGrants[0].RepositoryID != "repo-2" {
		t.Fatalf("RequestedGrants = %#v, want exactly one entry for repo-2", req.RequestedGrants)
	}
}

func TestNewScopeExpansionRequest_NormalizesPathScopes(t *testing.T) {
	t.Parallel()

	family, _ := familyFixture(t)
	grant := RequestedGrant{
		RepositoryID: "repo-2", Access: RepositoryRead,
		PathScopes: []string{"services\\billing/./api", "services/billing/api"},
		Reason:     "read billing api",
	}
	req, err := NewScopeExpansionRequest("req-1", family, []RequestedGrant{grant}, "reason", nil, "requester-1", time.Now())
	if err != nil {
		t.Fatalf("NewScopeExpansionRequest: %v", err)
	}
	paths := req.RequestedGrants[0].PathScopes
	if len(paths) != 1 || paths[0] != "services/billing/api" {
		t.Fatalf("normalized path scopes = %#v, want deduplicated [services/billing/api]", paths)
	}
}

func TestNewScopeExpansionRequest_OptionalReferencedWorkItem(t *testing.T) {
	t.Parallel()

	family, _ := familyFixture(t)
	workItemID := WorkItemID("work-item-1")
	req, err := NewScopeExpansionRequest("req-1", family, []RequestedGrant{validGrant()}, "reason", &workItemID, "requester-1", time.Now())
	if err != nil {
		t.Fatalf("NewScopeExpansionRequest: %v", err)
	}
	if req.ReferencedWorkItemID == nil || *req.ReferencedWorkItemID != workItemID {
		t.Fatalf("ReferencedWorkItemID = %v, want %q", req.ReferencedWorkItemID, workItemID)
	}

	// The field itself is optional — no reference at all is equally valid,
	// since no runtime engine exists yet to ever produce a real "run
	// reference" (this file's own doc comment / this package's established
	// V4 boundary discipline).
	noRef, err := NewScopeExpansionRequest("req-2", family, []RequestedGrant{validGrant()}, "reason", nil, "requester-1", time.Now())
	if err != nil {
		t.Fatalf("NewScopeExpansionRequest (no reference): %v", err)
	}
	if noRef.ReferencedWorkItemID != nil {
		t.Fatalf("ReferencedWorkItemID = %v, want nil", noRef.ReferencedWorkItemID)
	}
}

func TestNewScopeExpansionRequest_RequiresAtLeastOneGrant(t *testing.T) {
	t.Parallel()

	family, _ := familyFixture(t)
	if _, err := NewScopeExpansionRequest("req-1", family, nil, "reason", nil, "requester-1", time.Now()); err == nil {
		t.Fatal("expected rejection for zero requested grants")
	}
}

func TestNewScopeExpansionRequest_RejectsDuplicateRepositoryInGrants(t *testing.T) {
	t.Parallel()

	family, _ := familyFixture(t)
	grants := []RequestedGrant{validGrant(), validGrant()}
	if _, err := NewScopeExpansionRequest("req-1", family, grants, "reason", nil, "requester-1", time.Now()); err == nil {
		t.Fatal("expected rejection for a duplicate repository across requested grants")
	}
}

func TestNewScopeExpansionRequest_RejectsMissingReasonActorOrTimestamp(t *testing.T) {
	t.Parallel()

	family, _ := familyFixture(t)
	cases := []struct {
		name        string
		reason      string
		requestedBy string
		requestedAt time.Time
	}{
		{"missing reason", "", "requester-1", time.Now()},
		{"missing requestedBy", "reason", "", time.Now()},
		{"zero requestedAt", "reason", "requester-1", time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewScopeExpansionRequest("req-1", family, []RequestedGrant{validGrant()}, tc.reason, nil, tc.requestedBy, tc.requestedAt); err == nil {
				t.Fatalf("%s: expected rejection", tc.name)
			}
		})
	}
}

func TestNewScopeExpansionRequest_RejectsInvalidAccess(t *testing.T) {
	t.Parallel()

	family, _ := familyFixture(t)
	grant := RequestedGrant{RepositoryID: "repo-2", Access: RepositoryAccess("DELETE"), Reason: "bad access"}
	if _, err := NewScopeExpansionRequest("req-1", family, []RequestedGrant{grant}, "reason", nil, "requester-1", time.Now()); err == nil {
		t.Fatal("expected rejection for an unsupported access level")
	}
}

// --- CanTransitionScopeExpansionStatus ---

func TestCanTransitionScopeExpansionStatus_LegalEdges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		from, to ScopeExpansionStatus
	}{
		{ScopeExpansionPending, ScopeExpansionApproved},
		{ScopeExpansionPending, ScopeExpansionRejected},
		{ScopeExpansionPending, ScopeExpansionWithdrawn},
	}
	for _, tc := range cases {
		if err := CanTransitionScopeExpansionStatus(tc.from, tc.to); err != nil {
			t.Fatalf("CanTransitionScopeExpansionStatus(%s, %s) = %v, want nil", tc.from, tc.to, err)
		}
	}
}

func TestCanTransitionScopeExpansionStatus_RejectsIllegalEdges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		from, to ScopeExpansionStatus
	}{
		{ScopeExpansionApproved, ScopeExpansionRejected},
		{ScopeExpansionRejected, ScopeExpansionApproved},
		{ScopeExpansionWithdrawn, ScopeExpansionApproved},
		{ScopeExpansionPending, ScopeExpansionPending},
		{ScopeExpansionApproved, ScopeExpansionApproved},
		{ScopeExpansionStatus("BOGUS"), ScopeExpansionApproved},
	}
	for _, tc := range cases {
		if err := CanTransitionScopeExpansionStatus(tc.from, tc.to); err == nil {
			t.Fatalf("CanTransitionScopeExpansionStatus(%s, %s) = nil, want an error", tc.from, tc.to)
		}
	}
}
