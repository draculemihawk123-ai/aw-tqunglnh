package scopeexpansion_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/scopeexpansion"
)

func TestWithdraw_HappyPath(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "withdraw-happy.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1", "--expected-version", "1", requestID}
	if err := scopeexpansion.Withdraw(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("Withdraw() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		Result work.WithdrawScopeExpansionResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.Result.RequestID != requestID || envelope.Result.Status != "WITHDRAWN" {
		t.Fatalf("result = %+v, want RequestID=%s Status=WITHDRAWN", envelope.Result, requestID)
	}
}

// TestWithdraw_AlreadyWithdrawn_IsBusinessIdempotent proves
// workapp.WithdrawScopeExpansion's own documented business-level
// idempotency (see withdraw.go's own doc comment) survives all the way
// through this CLI leaf: a second withdraw of an already-WITHDRAWN request
// (using its now-current --expected-version, exactly as an operator who
// reloaded first would) is a harmless no-op success, never an error.
func TestWithdraw_AlreadyWithdrawn_IsBusinessIdempotent(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "withdraw-idempotent.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	if err := scopeexpansion.Withdraw(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "1", requestID}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Withdraw(): %v", err)
	}

	detail, err := work.GetScopeExpansionRequest(context.Background(), uow, ports.ProjectScope("project-1"), requestID)
	if err != nil {
		t.Fatalf("GetScopeExpansionRequest: %v", err)
	}

	var stdout bytes.Buffer
	args := []string{"--project-id", "project-1", "--expected-version", strconv.FormatUint(detail.Version, 10), requestID}
	if err := scopeexpansion.Withdraw(context.Background(), deps, args, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("second Withdraw() (already WITHDRAWN) error = %v, want a harmless no-op success", err)
	}
	var envelope struct {
		Result work.WithdrawScopeExpansionResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode second stdout: %v", err)
	}
	if envelope.Result.Status != "WITHDRAWN" {
		t.Fatalf("second withdraw result = %+v, want unchanged WITHDRAWN", envelope.Result)
	}
}

// TestWithdrawRace_ConcurrentWithApprove_ResolvesSafely is this task's own
// explicit "withdraw race" Verify bullet: a real, concurrent `scope-
// expansion withdraw` racing a real, concurrent `scope-expansion approve`
// for the SAME PENDING request (both observing the SAME initial Version=1,
// exactly like two operators who both loaded the request before either
// decided) against one real sqlite.Store. Per work/scope_expansion.go's own
// WithdrawScopeExpansion doc comment, withdraw is idempotent only over an
// ALREADY-WITHDRAWN target, never over one that raced past it to APPROVED —
// so exactly one of the two calls must succeed and the other must fail
// cleanly with a typed error, never corrupt state, never let both "win".
func TestWithdrawRace_ConcurrentWithApprove_ResolvesSafely(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "withdraw-race.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	var approveErr, withdrawErr error
	done := make(chan struct{}, 2)
	go func() {
		approveErr = scopeexpansion.Approve(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "1", "--idempotency-key", "idem-approve-race", requestID}, &bytes.Buffer{}, &bytes.Buffer{})
		done <- struct{}{}
	}()
	go func() {
		withdrawErr = scopeexpansion.Withdraw(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "1", "--idempotency-key", "idem-withdraw-race", requestID}, &bytes.Buffer{}, &bytes.Buffer{})
		done <- struct{}{}
	}()
	<-done
	<-done

	approveWon := approveErr == nil
	withdrawWon := withdrawErr == nil
	if approveWon == withdrawWon {
		t.Fatalf("exactly one of approve/withdraw must win, got approveErr=%v withdrawErr=%v", approveErr, withdrawErr)
	}

	detail, err := work.GetScopeExpansionRequest(context.Background(), uow, ports.ProjectScope("project-1"), requestID)
	if err != nil {
		t.Fatalf("GetScopeExpansionRequest: %v", err)
	}
	if approveWon && detail.Status != "APPROVED" {
		t.Fatalf("approve won but request.Status = %s, want APPROVED", detail.Status)
	}
	if withdrawWon && detail.Status != "WITHDRAWN" {
		t.Fatalf("withdraw won but request.Status = %s, want WITHDRAWN", detail.Status)
	}
}
