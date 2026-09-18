package scopeexpansion_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/scopeexpansion"
)

func TestReject_HappyPath(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "reject-happy.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1", "--expected-version", "1", "--note", "not needed", requestID}
	if err := scopeexpansion.Reject(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("Reject() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		Result work.RejectScopeExpansionResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.Result.RequestID != requestID || envelope.Result.Status != "REJECTED" {
		t.Fatalf("result = %+v, want RequestID=%s Status=REJECTED", envelope.Result, requestID)
	}
}

func TestReject_MissingNote_IsUsageError(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "reject-nonote.db")
	deps := newTestDeps(uow)
	err := scopeexpansion.Reject(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "1", "req-1"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !cli.IsUsageError(err) {
		t.Fatalf("Reject(no --note) error = %v, want a cli.UsageError", err)
	}
}

func TestReject_AlreadyApproved_RejectedCleanly(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "reject-alreadyapproved.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	if err := scopeexpansion.Approve(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "1", requestID}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Approve(): %v", err)
	}

	err := scopeexpansion.Reject(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "2", "--note", "too late", requestID}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Reject() of an already-APPROVED request succeeded, want an error")
	}
}
