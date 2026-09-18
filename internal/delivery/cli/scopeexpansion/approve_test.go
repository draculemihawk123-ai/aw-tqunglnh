package scopeexpansion_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/scopeexpansion"
)

func TestApprove_HappyPath(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "approve-happy.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	var stdout, stderr bytes.Buffer
	err := scopeexpansion.Approve(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "1", requestID}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Approve() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		Result work.ApproveScopeExpansionResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.Result.RequestID != requestID || envelope.Result.NewScopeVersion != 2 {
		t.Fatalf("result = %+v, want RequestID=%s NewScopeVersion=2", envelope.Result, requestID)
	}
}

func TestApprove_DuplicateApproval_RejectedCleanly(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "approve-dup.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	argsWithKey := func(idempotencyKey string) []string {
		return []string{"--project-id", "project-1", "--expected-version", "1", "--idempotency-key", idempotencyKey, requestID}
	}
	if err := scopeexpansion.Approve(context.Background(), deps, argsWithKey("idem-approve-1"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Approve(): %v", err)
	}

	// A genuinely different command invocation (different idempotency key)
	// targeting the SAME now-already-APPROVED request must fail cleanly,
	// never silently re-approve/double-provision.
	err := scopeexpansion.Approve(context.Background(), deps, argsWithKey("idem-approve-2"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("second, distinct Approve() of an already-APPROVED request succeeded, want an error")
	}
}

func TestApprove_StaleExpectedVersion_RejectedBeforeDispatch(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "approve-stale.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	err := scopeexpansion.Approve(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "99", requestID}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Approve() with a stale --expected-version succeeded, want an error")
	}
}

func TestApprove_MissingExpectedVersion_IsUsageError(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "approve-noversion.db")
	deps := newTestDeps(uow)
	err := scopeexpansion.Approve(context.Background(), deps, []string{"--project-id", "project-1", "req-1"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !cli.IsUsageError(err) {
		t.Fatalf("Approve(no --expected-version) error = %v, want a cli.UsageError", err)
	}
}

// TestApprove_Concurrent_SameRequest_OnlyOneWins races two real, concurrent
// `scope-expansion approve` invocations for the SAME PENDING request (each
// its own goroutine, its own --idempotency-key) against one real
// sqlite.Store — this task's own "concurrent decision" Verify bullet.
// Exactly one must succeed; the other must get a clean, typed error, never
// corrupt state or double-provision.
func TestApprove_Concurrent_SameRequest_OnlyOneWins(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "approve-race.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	const attempts = 5
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := []string{"--project-id", "project-1", "--expected-version", "1", "--idempotency-key", fmt.Sprintf("idem-approve-%d", i), requestID}
			errs[i] = scopeexpansion.Approve(context.Background(), deps, args, &bytes.Buffer{}, &bytes.Buffer{})
		}(i)
	}
	wg.Wait()

	winners := 0
	for _, err := range errs {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1 (errs: %+v)", winners, errs)
	}

	detail, err := work.GetScopeExpansionRequest(context.Background(), uow, ports.ProjectScope("project-1"), requestID)
	if err != nil {
		t.Fatalf("GetScopeExpansionRequest: %v", err)
	}
	if detail.Status != "APPROVED" {
		t.Fatalf("request.Status = %s, want exactly one APPROVED", detail.Status)
	}
}
