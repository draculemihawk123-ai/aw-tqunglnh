package recovery_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// TestResolveWorkItemBlocker_HTTP_RunFailed_ResolvedButNeverWaived is V9-06's
// (ADR-033) HTTP face of the RUN_FAILED authority: the operator resolves it
// (200, RESOLVED), but a waive attempt — with a policy grant, so the request
// itself is well-formed — is the typed "this blocker type can never be
// waived" conflict and leaves the blocker OPEN.
func TestResolveWorkItemBlocker_HTTP_RunFailed_ResolvedButNeverWaived(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "resolve-blocker-run-failed.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	workItemID, cancelledBlockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	// The fixture's run is terminal (CANCELLED); add the RUN_FAILED blocker a
	// failed run of this WorkItem would have opened, sourced from that run.
	const runFailedBlockerID = "run-failed-blocker-1"
	ctx := context.Background()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		cancelled, err := tx.Work().GetWorkItemBlocker(ctx, cancelledBlockerID)
		if err != nil {
			return err
		}
		blocker, err := workdomain.NewWorkItemBlocker(
			runFailedBlockerID, cancelled.ProjectID, workdomain.WorkItemID(workItemID), workdomain.BlockerRunFailed,
			cancelled.SourceRunID, "", "", "workflow run "+cancelled.SourceRunID+" failed (RUN_FAILED)", time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		_, err = tx.Work().CreateWorkItemBlocker(ctx, blocker)
		return err
	}); err != nil {
		t.Fatalf("seed the RUN_FAILED blocker: %v", err)
	}

	waive := postResolveBlocker(t, server.URL, runFailedBlockerID,
		`{"mode":"WAIVED","reason":"just skip it","policyGrantRef":"policy-grant-1"}`)
	if waive.StatusCode != http.StatusConflict {
		t.Fatalf("waive status = %d, want 409; body: %s", waive.StatusCode, mustReadAll(t, waive.Body))
	}
	if errBody := decodeErrorResponse(t, waive); errBody.Error.Code != httpapi.ErrorCodeConflict {
		t.Fatalf("waive error.code = %q, want CONFLICT", errBody.Error.Code)
	}
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		blocker, err := tx.Work().GetWorkItemBlocker(ctx, runFailedBlockerID)
		if err != nil {
			return err
		}
		if blocker.State != workdomain.BlockerOpen {
			t.Errorf("blocker state after the rejected waive = %s, want OPEN", blocker.State)
		}
		return nil
	}); err != nil {
		t.Fatalf("read the blocker: %v", err)
	}

	resolve := postResolveBlocker(t, server.URL, runFailedBlockerID, `{"mode":"RESOLVED","reason":"looked at the failed run"}`)
	if resolve.StatusCode != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200; body: %s", resolve.StatusCode, mustReadAll(t, resolve.Body))
	}
	got := decodeResolveBlockerResponse(t, resolve)
	if got.BlockerID != runFailedBlockerID || got.State != "RESOLVED" || got.AlreadyResolved {
		t.Fatalf("resolve response = %+v, want the RUN_FAILED blocker RESOLVED", got)
	}
	// The fixture's own RUN_CANCELLED blocker is still OPEN, so the WorkItem
	// stays BLOCKED until that one clears too.
	if got.WorkItemUnblocked {
		t.Fatal("WorkItemUnblocked = true, want false while the RUN_CANCELLED blocker is still open")
	}
}
