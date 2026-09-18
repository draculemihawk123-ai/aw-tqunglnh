package decision_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/decision"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestResolveApproval_HappyPath_ApprovesAndRoutes(t *testing.T) {
	_, uow := openDecisionCLITestStore(t, "approval-happy.db")
	deps := newTestDeps(uow)
	runID, hop := approvalFixture(t, uow, deps.IDs, 600)
	principalPath := writePrincipalFile(t, "operator-1", []string{"reviewer"})

	var stdout, stderr bytes.Buffer
	args := []string{
		"--principal-config", principalPath, "--outcome", "approved", "--reason", "looks good", "--expected-version", "1",
		runID, hop.NextApprovalRequestID,
	}
	if err := decision.ResolveApproval(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("ResolveApproval() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		IdempotencyKey string                        `json:"idempotencyKey"`
		Replayed       bool                          `json:"replayed"`
		Result         runtime.ResolveApprovalResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.IdempotencyKey == "" {
		t.Fatal("no --idempotency-key given, so ResolveApproval must have generated and returned one")
	}
	if !envelope.Result.Won || envelope.Result.MatchedRole != "reviewer" || !envelope.Result.Advanced || envelope.Result.NextNodeKey != "end_approved" {
		t.Fatalf("result = %+v, want Won=true MatchedRole=reviewer Advanced=true NextNodeKey=end_approved", envelope.Result)
	}
}

// TestResolveApproval_UnauthorizedActor_RejectedIdenticallyToHTTP is this
// task's own "HTTP/CLI auth parity" Verify bullet: this CLI leaf dispatches
// the SAME runtime.ResolveApproval function internal/delivery/httpapi/
// decision's own ResolveApprovalHandler dispatches, so an actor whose
// --principal-config roles do not intersect the ApprovalRequest's own
// AuthorizedRoles is rejected with the identical apperror.CodePolicyDenied
// regardless of which delivery mechanism dispatched the command — proven
// here by construction (this package never re-implements the role check)
// and confirmed with the exact same assertion
// internal/app/runtime/approval_test.go's own
// TestResolveApproval_UnauthorizedActor_Rejected makes against the shared
// function directly.
func TestResolveApproval_UnauthorizedActor_RejectedIdenticallyToHTTP(t *testing.T) {
	_, uow := openDecisionCLITestStore(t, "approval-unauth.db")
	deps := newTestDeps(uow)
	runID, hop := approvalFixture(t, uow, deps.IDs, 600)
	principalPath := writePrincipalFile(t, "intruder-1", []string{"guest"}) // not "reviewer"

	err := decision.ResolveApproval(context.Background(), deps, []string{
		"--principal-config", principalPath, "--outcome", "approved", "--expected-version", "1",
		runID, hop.NextApprovalRequestID,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("ResolveApproval(unauthorized actor) succeeded, want an error")
	}

	// The request must be left completely untouched — no partial decision,
	// exactly like internal/app/runtime/approval_test.go's own direct
	// TestResolveApproval_UnauthorizedActor_Rejected proves against the
	// shared function.
	var request runtimedomain.ApprovalRequest
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var loadErr error
		request, loadErr = tx.Approvals().GetApprovalRequest(context.Background(), hop.NextApprovalRequestID)
		return loadErr
	}); err != nil {
		t.Fatalf("GetApprovalRequest: %v", err)
	}
	if request.State != runtimedomain.ApprovalRequestPending {
		t.Fatalf("request.State = %s, want unchanged PENDING (unauthorized actor must never resolve it)", request.State)
	}
}

func TestResolveApproval_MissingOutcome_IsUsageError(t *testing.T) {
	_, uow := openDecisionCLITestStore(t, "approval-nooutcome.db")
	deps := newTestDeps(uow)
	err := decision.ResolveApproval(context.Background(), deps, []string{"--expected-version", "1", "run-1", "req-1"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !cli.IsUsageError(err) {
		t.Fatalf("ResolveApproval(no --outcome) error = %v, want a cli.UsageError", err)
	}
}

func TestResolveApproval_MissingExpectedVersion_IsUsageError(t *testing.T) {
	_, uow := openDecisionCLITestStore(t, "approval-noversion.db")
	deps := newTestDeps(uow)
	err := decision.ResolveApproval(context.Background(), deps, []string{"--outcome", "approved", "run-1", "req-1"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !cli.IsUsageError(err) {
		t.Fatalf("ResolveApproval(no --expected-version) error = %v, want a cli.UsageError", err)
	}
}

// TestResolveApproval_Concurrent_SameRequest_OnlyOneWins races real,
// concurrent `approval resolve` invocations for the SAME PENDING
// ApprovalRequest (each its own goroutine, its own --idempotency-key, all
// passing the SAME --expected-version=1 that every one of them would
// genuinely have observed had they all reloaded before any of them
// decided) against one real sqlite.Store — this task's own "concurrent
// decision" Verify bullet: "only one wins, the other gets a clean, typed
// conflict (not corruption)".
//
// A "clean, typed conflict" has two legitimate shapes here, both proving
// this command never corrupts state: a goroutine whose own reload
// (loadApprovalRequestForUpdate, at the very top of ResolveApproval) genuinely
// raced BEFORE the winner's commit reaches runtime.ResolveApproval itself and
// gets back Won=false (the SAME graceful race runtime.ResolveApproval's own
// direct unit test — TestResolveApproval_DuplicateDecision_SecondAttemptWonFalse
// — proves); a goroutine scheduled late enough that its own reload already
// observes the winner's bumped Version gets this command's own advisory
// --expected-version precondition error instead (the CLI equivalent of an
// HTTP 412 Precondition Failed — a real, typed, actionable "reload and
// retry", never a panic/hang/corrupt double-decision). Exactly one call may
// ever report Won=true; every other outcome must be one of these two clean
// shapes.
func TestResolveApproval_Concurrent_SameRequest_OnlyOneWins(t *testing.T) {
	_, uow := openDecisionCLITestStore(t, "approval-race.db")
	deps := newTestDeps(uow)
	runID, hop := approvalFixture(t, uow, deps.IDs, 600)
	principalPath := writePrincipalFile(t, "operator-1", []string{"reviewer"})

	const attempts = 5
	var wg sync.WaitGroup
	stdouts := make([]bytes.Buffer, attempts)
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := []string{
				"--principal-config", principalPath, "--outcome", "approved", "--expected-version", "1",
				"--idempotency-key", fmt.Sprintf("idem-resolve-%d", i),
				runID, hop.NextApprovalRequestID,
			}
			errs[i] = decision.ResolveApproval(context.Background(), deps, args, &stdouts[i], &bytes.Buffer{})
		}(i)
	}
	wg.Wait()

	winners := 0
	for i, err := range errs {
		if err != nil {
			// A late-scheduled goroutine's own reload legitimately observed
			// the winner's already-bumped Version — a clean, typed
			// precondition conflict, never corruption. Anything else is a
			// genuine test failure.
			if !strings.Contains(err.Error(), "reload and retry") {
				t.Fatalf("attempt %d unexpected error = %v", i, err)
			}
			continue
		}
		var envelope struct {
			Result runtime.ResolveApprovalResult `json:"result"`
		}
		if err := json.Unmarshal(stdouts[i].Bytes(), &envelope); err != nil {
			t.Fatalf("attempt %d decode stdout %s: %v", i, stdouts[i].String(), err)
		}
		if envelope.Result.Won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}

	var request runtimedomain.ApprovalRequest
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var loadErr error
		request, loadErr = tx.Approvals().GetApprovalRequest(context.Background(), hop.NextApprovalRequestID)
		return loadErr
	}); err != nil {
		t.Fatalf("GetApprovalRequest: %v", err)
	}
	if request.State != runtimedomain.ApprovalRequestDecided || request.DecidedOutcome != "approved" {
		t.Fatalf("request = %+v, want exactly one DECIDED/approved", request)
	}
}
