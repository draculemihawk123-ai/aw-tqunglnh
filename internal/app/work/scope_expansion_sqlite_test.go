package work_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// This file is this task's own real-sqlite Verify line, made concrete
// against a real database: "duplicate approval, remove request, cross-project,
// concurrent scope update tests" — mirroring commands_sqlite_test.go's own
// rigor (real row-count/state assertions, never merely "the function
// returned an error").

func scopeExpansionCmd(idempotencyKey, requestHash string, projectID, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: commandType, RequestHash: requestHash,
	}
}

// TestRequestScopeExpansion_CrossProjectRepository_RejectedSQLite is this
// task's own explicit "cross-project" Verify-line requirement against real
// sqlite: a requested grant naming a repository outside the family's own
// project must be rejected, and must leave zero scope_expansion_requests
// rows behind.
func TestRequestScopeExpansion_CrossProjectRepository_RejectedSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-scope-expansion-cross-project.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	root := seedRootFixtureSQLite(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	seedProjectSQLite(t, uow, "project-2")
	seedActiveRepositorySQLite(t, uow, ids, "project-2", "repo-other")

	cmd := scopeExpansionCmd("idem-request-1", "hash-request-1", "project-1", "RequestScopeExpansion")
	_, err := work.RequestScopeExpansion(ctx, uow, ids, cmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "cross project ask",
		RequestedGrants: []work.ScopeGrantRequest{
			{RepositoryID: "repo-other", Access: string(workdomain.RepositoryRead), PathScopes: nil, Reason: "wrong project"},
		},
	})
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("RequestScopeExpansion err = %v, want ports.ErrCrossProjectReference (repo-other belongs to project-2)", err)
	}

	assertCount(t, "scope_expansion_requests", 0, func() (int, error) { return store.CountScopeExpansionRequests(ctx) })
	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-request-1")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("command receipt count = %d, want 0 (a failed command must remain retryable)", receiptCount)
	}
}

// TestApproveScopeExpansion_DuplicateApproval_RejectedSQLite is this task's
// own explicit "duplicate approval" Verify-line requirement against real
// sqlite: a second ApproveScopeExpansion against an already-APPROVED request
// fails cleanly and leaves the family's ScopeVersion/grants exactly where
// the first, successful approval left them — never double-incremented,
// never double-granted.
func TestApproveScopeExpansion_DuplicateApproval_RejectedSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-scope-expansion-duplicate-approval.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	root := seedRootFixtureSQLite(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-2")

	reqCmd := scopeExpansionCmd("idem-request-1", "hash-request-1", "project-1", "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2",
		RequestedGrants: []work.ScopeGrantRequest{
			{RepositoryID: "repo-2", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/repo-2"}, Reason: "expand"},
		},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	firstApproveCmd := scopeExpansionCmd("idem-approve-1", "hash-approve-1", "project-1", "ApproveScopeExpansion")
	firstResult, err := work.ApproveScopeExpansion(ctx, uow, ids, firstApproveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if err != nil {
		t.Fatalf("first ApproveScopeExpansion: %v", err)
	}
	if firstResult.NewScopeVersion != 2 {
		t.Fatalf("first approval NewScopeVersion = %d, want 2", firstResult.NewScopeVersion)
	}
	assertCount(t, "family_repository_scopes (after first approval)", 2, func() (int, error) { return store.CountFamilyRepositoryScopes(ctx) })

	secondApproveCmd := scopeExpansionCmd("idem-approve-2", "hash-approve-2", "project-1", "ApproveScopeExpansion")
	_, err = work.ApproveScopeExpansion(ctx, uow, ids, secondApproveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if !errors.Is(err, work.ErrScopeExpansionNotPending) {
		t.Fatalf("second ApproveScopeExpansion err = %v, want work.ErrScopeExpansionNotPending", err)
	}

	// The load-bearing proof: the second (failed) attempt must have left
	// EVERY table exactly where the first, successful approval left it.
	assertCount(t, "family_repository_scopes (after duplicate attempt)", 2, func() (int, error) { return store.CountFamilyRepositoryScopes(ctx) })
	approvedCount, err := store.CountScopeExpansionRequestsByStatus(ctx, string(workdomain.ScopeExpansionApproved))
	if err != nil {
		t.Fatalf("CountScopeExpansionRequestsByStatus: %v", err)
	}
	if approvedCount != 1 {
		t.Fatalf("APPROVED scope expansion requests = %d, want exactly 1", approvedCount)
	}

	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		family, err := tx.Work().GetTaskFamily(ctx, root.FamilyID)
		if err != nil {
			return err
		}
		if family.ScopeVersion != 2 {
			return fmt.Errorf("family.ScopeVersion = %d, want unchanged 2 (never double-incremented)", family.ScopeVersion)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-approve-2")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("command receipt count for the failed second approval = %d, want 0", receiptCount)
	}
}

// TestWithdrawScopeExpansion_RemovesPendingRequestSQLite is this task's own
// explicit "remove request" Verify-line requirement against real sqlite:
// withdrawing a still-PENDING request marks it WITHDRAWN (never deleted —
// this schema is append-only/audit-preserving throughout, the same
// discipline family_repository_scopes/work_items already follow), makes it
// permanently un-approvable, and creates no grant of any kind. A second,
// independent withdraw call against the same, now-WITHDRAWN request is a
// harmless no-op, go-core-spec's own explicit "idempotent" bar for this
// command.
func TestWithdrawScopeExpansion_RemovesPendingRequestSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-scope-expansion-withdraw.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	root := seedRootFixtureSQLite(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-2")

	reqCmd := scopeExpansionCmd("idem-request-1", "hash-request-1", "project-1", "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2",
		RequestedGrants: []work.ScopeGrantRequest{
			{RepositoryID: "repo-2", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/repo-2"}, Reason: "expand"},
		},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}
	assertCount(t, "scope_expansion_requests (before withdraw)", 1, func() (int, error) { return store.CountScopeExpansionRequests(ctx) })

	firstWithdrawCmd := scopeExpansionCmd("idem-withdraw-1", "hash-withdraw-1", "project-1", "WithdrawScopeExpansion")
	firstResult, err := work.WithdrawScopeExpansion(ctx, uow, firstWithdrawCmd, work.WithdrawScopeExpansionRequest{RequestID: reqResult.RequestID})
	if err != nil {
		t.Fatalf("first WithdrawScopeExpansion: %v", err)
	}
	if firstResult.Status != string(workdomain.ScopeExpansionWithdrawn) {
		t.Fatalf("first withdraw Status = %q, want WITHDRAWN", firstResult.Status)
	}

	// The row still exists (audit trail), just no longer actionable — never
	// deleted outright.
	assertCount(t, "scope_expansion_requests (after withdraw)", 1, func() (int, error) { return store.CountScopeExpansionRequests(ctx) })
	withdrawnCount, err := store.CountScopeExpansionRequestsByStatus(ctx, string(workdomain.ScopeExpansionWithdrawn))
	if err != nil {
		t.Fatalf("CountScopeExpansionRequestsByStatus: %v", err)
	}
	if withdrawnCount != 1 {
		t.Fatalf("WITHDRAWN scope expansion requests = %d, want exactly 1", withdrawnCount)
	}

	// A second, independent withdraw call succeeds as a no-op.
	secondWithdrawCmd := scopeExpansionCmd("idem-withdraw-2", "hash-withdraw-2", "project-1", "WithdrawScopeExpansion")
	secondResult, err := work.WithdrawScopeExpansion(ctx, uow, secondWithdrawCmd, work.WithdrawScopeExpansionRequest{RequestID: reqResult.RequestID})
	if err != nil {
		t.Fatalf("second WithdrawScopeExpansion (must be idempotent, not an error): %v", err)
	}
	if secondResult.Status != string(workdomain.ScopeExpansionWithdrawn) {
		t.Fatalf("second withdraw Status = %q, want WITHDRAWN (unchanged)", secondResult.Status)
	}

	// A withdrawn request creates no grant of any kind, ever.
	assertCount(t, "family_repository_scopes (after withdraw)", 1, func() (int, error) { return store.CountFamilyRepositoryScopes(ctx) })
	provisionJobsAfter, err := store.CountDurableJobsByKind(ctx, work.WorkspaceProvisionJobKind)
	if err != nil {
		t.Fatalf("CountDurableJobsByKind: %v", err)
	}
	if provisionJobsAfter != 1 { // only the root's own original repo-1 job
		t.Fatalf("WORKSPACE_PROVISION jobs after withdraw = %d, want unchanged 1 (from the root's own creation)", provisionJobsAfter)
	}

	// And it can never be approved.
	approveCmd := scopeExpansionCmd("idem-approve-1", "hash-approve-1", "project-1", "ApproveScopeExpansion")
	_, err = work.ApproveScopeExpansion(ctx, uow, ids, approveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if !errors.Is(err, work.ErrScopeExpansionNotPending) {
		t.Fatalf("ApproveScopeExpansion (after withdraw) err = %v, want work.ErrScopeExpansionNotPending", err)
	}
}

// TestApproveScopeExpansion_ConcurrentApprovals_BothSucceedWithDistinctScopeVersionsSQLite
// is this task's own explicit "concurrent scope update" Verify-line
// requirement: two DIFFERENT PENDING requests on the SAME family, approved
// from two goroutines at the same time, against a REAL sqlite database (real
// _txlock=immediate write-lock contention and retry — see
// ApproveScopeExpansion's own "Concurrency" doc-comment paragraph for
// exactly why this is safe), must both succeed with correct, non-colliding
// ScopeVersion increments — never a lost update, never two approvals landing
// on the same ScopeVersion.
func TestApproveScopeExpansion_ConcurrentApprovals_BothSucceedWithDistinctScopeVersionsSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-scope-expansion-concurrent.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	root := seedRootFixtureSQLite(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-2")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-3")

	req1Cmd := scopeExpansionCmd("idem-request-2", "hash-request-2", "project-1", "RequestScopeExpansion")
	req1, err := work.RequestScopeExpansion(ctx, uow, ids, req1Cmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2",
		RequestedGrants: []work.ScopeGrantRequest{
			{RepositoryID: "repo-2", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/repo-2"}, Reason: "expand"},
		},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion (repo-2): %v", err)
	}
	req2Cmd := scopeExpansionCmd("idem-request-3", "hash-request-3", "project-1", "RequestScopeExpansion")
	req2, err := work.RequestScopeExpansion(ctx, uow, ids, req2Cmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-3",
		RequestedGrants: []work.ScopeGrantRequest{
			{RepositoryID: "repo-3", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/repo-3"}, Reason: "expand"},
		},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion (repo-3): %v", err)
	}

	requestIDs := []string{req1.RequestID, req2.RequestID}
	const writers = 2
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]work.ApproveScopeExpansionResult, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cmd := scopeExpansionCmd(fmt.Sprintf("idem-approve-%d", i), fmt.Sprintf("hash-approve-%d", i), "project-1", "ApproveScopeExpansion")
			results[i], errs[i] = work.ApproveScopeExpansion(ctx, uow, ids, cmd, work.ApproveScopeExpansionRequest{RequestID: requestIDs[i]})
		}()
	}
	close(start)
	wg.Wait()

	seenScopeVersions := map[uint64]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d ApproveScopeExpansion: %v", i, err)
		}
		if seenScopeVersions[results[i].NewScopeVersion] {
			t.Fatalf("writer %d got a NewScopeVersion (%d) already seen from another writer — lost update", i, results[i].NewScopeVersion)
		}
		seenScopeVersions[results[i].NewScopeVersion] = true
	}
	if !seenScopeVersions[2] || !seenScopeVersions[3] {
		t.Fatalf("seen scope versions = %v, want exactly {2, 3} (both approvals landed, in some order, at distinct successive versions)", seenScopeVersions)
	}

	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		family, err := tx.Work().GetTaskFamily(ctx, root.FamilyID)
		if err != nil {
			return err
		}
		if family.ScopeVersion != 3 {
			return fmt.Errorf("final family.ScopeVersion = %d, want 3 (1 initial + 2 successful concurrent approvals)", family.ScopeVersion)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assertCount(t, "family_repository_scopes (after concurrent approvals)", 3, func() (int, error) { return store.CountFamilyRepositoryScopes(ctx) })
	approvedCount, err := store.CountScopeExpansionRequestsByStatus(ctx, string(workdomain.ScopeExpansionApproved))
	if err != nil {
		t.Fatalf("CountScopeExpansionRequestsByStatus: %v", err)
	}
	if approvedCount != 2 {
		t.Fatalf("APPROVED scope expansion requests = %d, want exactly 2", approvedCount)
	}

	for _, repositoryID := range []string{"repo-2", "repo-3"} {
		key := fmt.Sprintf("idem-approve-0-provision-%s", repositoryID)
		count0, err := store.CountDurableJobsByIdempotencyKey(ctx, key)
		if err != nil {
			t.Fatalf("CountDurableJobsByIdempotencyKey(%s, writer 0): %v", repositoryID, err)
		}
		key1 := fmt.Sprintf("idem-approve-1-provision-%s", repositoryID)
		count1, err := store.CountDurableJobsByIdempotencyKey(ctx, key1)
		if err != nil {
			t.Fatalf("CountDurableJobsByIdempotencyKey(%s, writer 1): %v", repositoryID, err)
		}
		if count0+count1 != 1 {
			t.Fatalf("provision job count for %s across both writers = %d, want exactly 1 (only the writer that actually approved that repository's own request enqueues its job)", repositoryID, count0+count1)
		}
	}
}

// TestApproveScopeExpansion_RollbackOnRepositoryNoLongerActive_NoOrphanRowsSQLite
// mirrors TestCreateRootWorkItem_RollbackOnMidTransactionFailure_NoOrphanRows
// for ApproveScopeExpansion: a REAL failure deep inside the transaction
// (repo-2 was ACTIVE when requested but has since been disabled — the
// defensive re-check ApproveScopeExpansion's own doc comment names) must
// roll back EVERYTHING already written earlier in that same transaction —
// the family's own ScopeVersion bump included — leaving the request still
// PENDING and the family's ScopeVersion still at its pre-attempt value.
func TestApproveScopeExpansion_RollbackOnRepositoryNoLongerActive_NoOrphanRowsSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-scope-expansion-rollback.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	root := seedRootFixtureSQLite(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-2")

	reqCmd := scopeExpansionCmd("idem-request-1", "hash-request-1", "project-1", "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2",
		RequestedGrants: []work.ScopeGrantRequest{
			{RepositoryID: "repo-2", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/repo-2"}, Reason: "expand"},
		},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	// Disable repo-2 in the window between request and approval.
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-2", ExpectedStatus: project.RepositoryActive, ExpectedVersion: 3,
			NextStatus: project.RepositoryDisabled,
		})
		return err
	}); err != nil {
		t.Fatalf("disable repo-2: %v", err)
	}

	approveCmd := scopeExpansionCmd("idem-approve-1", "hash-approve-1", "project-1", "ApproveScopeExpansion")
	_, err = work.ApproveScopeExpansion(ctx, uow, ids, approveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if !errors.Is(err, work.ErrRepositoryNotActive) {
		t.Fatalf("ApproveScopeExpansion err = %v, want work.ErrRepositoryNotActive (repo-2 was disabled after the request)", err)
	}

	// The family's own ScopeVersion bump happened EARLIER in this same
	// transaction than the repository-status check that ultimately failed
	// it — proving the whole transaction rolled back, not just the grant
	// write.
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		family, err := tx.Work().GetTaskFamily(ctx, root.FamilyID)
		if err != nil {
			return err
		}
		if family.ScopeVersion != 1 {
			return fmt.Errorf("family.ScopeVersion after rollback = %d, want unchanged 1", family.ScopeVersion)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assertCount(t, "family_repository_scopes (after rollback)", 1, func() (int, error) { return store.CountFamilyRepositoryScopes(ctx) })
	pendingCount, err := store.CountScopeExpansionRequestsByStatus(ctx, string(workdomain.ScopeExpansionPending))
	if err != nil {
		t.Fatalf("CountScopeExpansionRequestsByStatus: %v", err)
	}
	if pendingCount != 1 {
		t.Fatalf("PENDING scope expansion requests after rollback = %d, want 1 (the request itself must remain untouched, still approvable once repo-2 is re-activated)", pendingCount)
	}

	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-approve-1")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("command receipt count = %d, want 0 (a failed command must remain retryable)", receiptCount)
	}
}
