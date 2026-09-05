package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V4-08's own WAIT persistence/signal fixtures and tests.

// waitDurationDocument is start -> pause(WAIT, DURATION) -> end: a single
// declared outcome ("resumed"), so CompletionOutcome is inferred per
// WaitNodeConfig's own documented rule — never needs pinning explicitly.
func waitDurationDocument(durationSeconds uint32) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "pause", Type: workflow.NodeWait, Outcomes: []string{"resumed"}, Wait: &workflow.WaitNodeConfig{
				Mode: workflow.WaitModeDuration, DurationSeconds: durationSeconds,
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-pause", From: "start", Outcome: "next", To: "pause"},
			{Key: "pause-to-end", From: "pause", Outcome: "resumed", To: "end"},
		},
	}
}

// waitSignalDocument is start -> pause(WAIT, SIGNAL) -> end_resumed|end_expired:
// two declared outcomes, so CompletionOutcome/TimeoutOutcome must both be
// pinned explicitly (validateWaitConfig's own ambiguity rule).
// timeoutSeconds == 0 means no timeout ceiling at all (TimeoutOutcome must
// then stay empty too).
func waitSignalDocument(timeoutSeconds uint32) workflow.WorkflowDocument {
	waitCfg := &workflow.WaitNodeConfig{
		Mode: workflow.WaitModeSignal, SignalName: "ci-passed", TimeoutSeconds: timeoutSeconds,
		CompletionOutcome: "resumed",
	}
	outcomes := []string{"resumed", "expired"}
	if timeoutSeconds > 0 {
		waitCfg.TimeoutOutcome = "expired"
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "pause", Type: workflow.NodeWait, Outcomes: outcomes, Wait: waitCfg},
			{Key: "end_resumed", Type: workflow.NodeEnd},
			{Key: "end_expired", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-pause", From: "start", Outcome: "next", To: "pause"},
			{Key: "pause-to-end-resumed", From: "pause", Outcome: "resumed", To: "end_resumed"},
			{Key: "pause-to-end-expired", From: "pause", Outcome: "expired", To: "end_expired"},
		},
	}
}

// waitFixture starts a run over document and advances it exactly one hop
// (start -> pause), returning the hop's own AdvanceRunResult — NextNodeRunID
// is the WAIT node's own NodeRunID, NextWaitRegistrationID its freshly
// created registration.
func waitFixture(t *testing.T, document workflow.WorkflowDocument) (uow *fake.UnitOfWork, ids idsource.Source, runID string, hop runtime.AdvanceRunResult) {
	t.Helper()
	ctx := context.Background()
	u, seq, rid, startNodeRunID := startWorkflowRunFixture(t, document)
	result, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: rid, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->pause): %v", err)
	}
	if result.NextNodeKey != "pause" || result.NextWaitRegistrationID == "" {
		t.Fatalf("hop = %+v, want NextNodeKey=pause with a minted NextWaitRegistrationID", result)
	}
	return u, seq, rid, result
}

// --- advanceRunTx: WAIT node activation itself ---

func TestAdvanceRun_WaitNode_Duration_CreatesRegistrationAndTimerJob(t *testing.T) {
	ctx := context.Background()
	before := time.Now().UTC()
	uow, _, _, hop := waitFixture(t, waitDurationDocument(600))
	after := time.Now().UTC()

	if hop.NextWaitTimerJobID == "" {
		t.Fatalf("hop = %+v, want a minted NextWaitTimerJobID (DURATION always has a due time)", hop)
	}

	pauseNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(pause): %v", err)
	}
	if pauseNodeRun.State != runtimedomain.NodeRunWaiting {
		t.Fatalf("pause node run = %+v, want WAITING", pauseNodeRun)
	}

	registration, err := uow.Snapshot.Wait().GetWaitRegistration(ctx, hop.NextWaitRegistrationID)
	if err != nil {
		t.Fatalf("GetWaitRegistration: %v", err)
	}
	if registration.State != runtimedomain.WaitRegistrationActive || registration.CompletionOutcome != "resumed" || registration.TimeoutOutcome != "" {
		t.Fatalf("registration = %+v, want ACTIVE/resumed with no timeout outcome", registration)
	}
	if registration.DueAt == nil {
		t.Fatal("registration.DueAt is nil, want a due time ~600s out")
	}
	wantMin := before.Add(600 * time.Second)
	wantMax := after.Add(600 * time.Second)
	if registration.DueAt.Before(wantMin) || registration.DueAt.After(wantMax) {
		t.Fatalf("registration.DueAt = %v, want within [%v, %v]", registration.DueAt, wantMin, wantMax)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	found := false
	for _, j := range jobs {
		if j.Kind == runtime.WaitTimerJobKind && j.AggregateID == hop.NextWaitRegistrationID {
			found = true
			if j.AvailableAt.Before(wantMin) || j.AvailableAt.After(wantMax) {
				t.Fatalf("timer job AvailableAt = %v, want within [%v, %v]", j.AvailableAt, wantMin, wantMax)
			}
		}
	}
	if !found {
		t.Fatalf("no %s job found for registration %s among %+v", runtime.WaitTimerJobKind, hop.NextWaitRegistrationID, jobs)
	}
}

func TestAdvanceRun_WaitNode_Signal_NoTimeout_CreatesRegistrationWithoutTimerJob(t *testing.T) {
	ctx := context.Background()
	uow, _, _, hop := waitFixture(t, waitSignalDocument(0))

	if hop.NextWaitTimerJobID != "" {
		t.Fatalf("hop = %+v, want no NextWaitTimerJobID (no timeout ceiling declared)", hop)
	}
	registration, err := uow.Snapshot.Wait().GetWaitRegistration(ctx, hop.NextWaitRegistrationID)
	if err != nil {
		t.Fatalf("GetWaitRegistration: %v", err)
	}
	if registration.DueAt != nil {
		t.Fatalf("registration.DueAt = %v, want nil", registration.DueAt)
	}
	if registration.SignalName != "ci-passed" || registration.CompletionOutcome != "resumed" {
		t.Fatalf("registration = %+v, want signalName=ci-passed completionOutcome=resumed", registration)
	}
}

// --- SignalWait: happy path ---

func TestSignalWait_ResolvesRegistrationAndRoutesForward(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := waitFixture(t, waitSignalDocument(600))

	cmd := testCommand("idem-signal-1", "hash-signal-1", ports.ProjectScope("project-1"), "SignalWait")
	result, err := runtime.SignalWait(ctx, uow, ids, cmd, runtime.SignalWaitRequest{
		RunID: runID, WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-1", Payload: json.RawMessage(`{"status":"green"}`),
	})
	if err != nil {
		t.Fatalf("SignalWait: %v", err)
	}
	if !result.Won || !result.Advanced || result.NextNodeKey != "end_resumed" {
		t.Fatalf("result = %+v, want Won=true Advanced=true NextNodeKey=end_resumed", result)
	}

	registration, err := uow.Snapshot.Wait().GetWaitRegistration(ctx, hop.NextWaitRegistrationID)
	if err != nil {
		t.Fatalf("GetWaitRegistration: %v", err)
	}
	if registration.State != runtimedomain.WaitRegistrationConsumed || registration.ConsumedSignalID == nil {
		t.Fatalf("registration = %+v, want CONSUMED with a ConsumedSignalID", registration)
	}

	pauseNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(pause): %v", err)
	}
	if pauseNodeRun.State != runtimedomain.NodeRunSucceeded || pauseNodeRun.SelectedOutcome != "resumed" {
		t.Fatalf("pause node run = %+v, want SUCCEEDED/resumed", pauseNodeRun)
	}
}

// --- SignalWait: duplicate/wrong signal ---

func TestSignalWait_DuplicateSignalKeyDifferentCommand_IdempotentReplay(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := waitFixture(t, waitSignalDocument(600))
	payload := json.RawMessage(`{"status":"green"}`)

	first, err := runtime.SignalWait(ctx, uow, ids, testCommand("idem-signal-1", "hash-1", ports.ProjectScope("project-1"), "SignalWait"), runtime.SignalWaitRequest{
		RunID: runID, WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-1", Payload: payload,
	})
	if err != nil {
		t.Fatalf("first SignalWait: %v", err)
	}

	// A DIFFERENT command invocation (different IdempotencyKey — e.g. a
	// second webhook delivery attempt) reporting the exact same real
	// event (same SignalKey, same payload) must be recognized as one
	// signal, never re-consumed a second time.
	second, err := runtime.SignalWait(ctx, uow, ids, testCommand("idem-signal-2", "hash-2", ports.ProjectScope("project-1"), "SignalWait"), runtime.SignalWaitRequest{
		RunID: runID, WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-1", Payload: payload,
	})
	if err != nil {
		t.Fatalf("second SignalWait: %v", err)
	}
	if second.Won {
		t.Fatalf("second result = %+v, want Won=false (already resolved by the first)", second)
	}
	if second.State != first.State {
		t.Fatalf("second State = %s, want %s (same final state as the winner)", second.State, first.State)
	}

	// Exactly one NODE_ROUTED event was ever appended for "resumed" — the
	// replayed signal must never route a second time.
	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	routedCount := 0
	for _, e := range events {
		if e.EventType == runtime.NodeRoutedEventType && e.AggregateID == hop.NextNodeRunID {
			routedCount++
		}
	}
	if routedCount != 1 {
		t.Fatalf("NODE_ROUTED count for pause node = %d, want exactly 1 (no duplicate routing from the replayed signal)", routedCount)
	}
}

func TestSignalWait_SameKeyDifferentPayload_ReturnsIdempotencyConflict(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := waitFixture(t, waitSignalDocument(600))

	if _, err := runtime.SignalWait(ctx, uow, ids, testCommand("idem-signal-1", "hash-1", ports.ProjectScope("project-1"), "SignalWait"), runtime.SignalWaitRequest{
		RunID: runID, WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-1", Payload: json.RawMessage(`{"status":"green"}`),
	}); err != nil {
		t.Fatalf("first SignalWait: %v", err)
	}

	_, err := runtime.SignalWait(ctx, uow, ids, testCommand("idem-signal-2", "hash-2", ports.ProjectScope("project-1"), "SignalWait"), runtime.SignalWaitRequest{
		RunID: runID, WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-1", Payload: json.RawMessage(`{"status":"red"}`),
	})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != errorcode.CodeIdempotencyConflict {
		t.Fatalf("err = %v, want an *apperror.Error with CodeIdempotencyConflict", err)
	}
}

func TestSignalWait_WrongRun_Rejected(t *testing.T) {
	ctx := context.Background()
	uow, ids, _, hop := waitFixture(t, waitSignalDocument(600))

	_, err := runtime.SignalWait(ctx, uow, ids, testCommand("idem-signal-1", "hash-1", ports.ProjectScope("project-1"), "SignalWait"), runtime.SignalWaitRequest{
		RunID: "some-other-run", WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-1", Payload: json.RawMessage(`{}`),
	})
	if !errors.Is(err, runtime.ErrNodeRunMismatch) {
		t.Fatalf("err = %v, want ErrNodeRunMismatch", err)
	}
}

func TestSignalWait_AfterAlreadyConsumed_WonFalseNoReRoute(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := waitFixture(t, waitSignalDocument(600))

	if _, err := runtime.SignalWait(ctx, uow, ids, testCommand("idem-signal-1", "hash-1", ports.ProjectScope("project-1"), "SignalWait"), runtime.SignalWaitRequest{
		RunID: runID, WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-1", Payload: json.RawMessage(`{"status":"green"}`),
	}); err != nil {
		t.Fatalf("first SignalWait: %v", err)
	}

	// A genuinely DIFFERENT signal (different key/payload) arriving after
	// the registration is already CONSUMED — durably recorded, but never
	// wins, never re-routes.
	late, err := runtime.SignalWait(ctx, uow, ids, testCommand("idem-signal-2", "hash-2", ports.ProjectScope("project-1"), "SignalWait"), runtime.SignalWaitRequest{
		RunID: runID, WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-2-late", Payload: json.RawMessage(`{"status":"green"}`),
	})
	if err != nil {
		t.Fatalf("late SignalWait: %v", err)
	}
	if late.Won || late.State != string(runtimedomain.WaitRegistrationConsumed) {
		t.Fatalf("late result = %+v, want Won=false State=CONSUMED", late)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	routedCount := 0
	for _, e := range events {
		if e.EventType == runtime.NodeRoutedEventType && e.AggregateID == hop.NextNodeRunID {
			routedCount++
		}
	}
	if routedCount != 1 {
		t.Fatalf("NODE_ROUTED count = %d, want exactly 1 (the late signal must never re-route)", routedCount)
	}
}

// --- WaitTimeoutHandler ---

func TestWaitTimeoutHandler_Duration_RoutesViaCompletionOutcome(t *testing.T) {
	ctx := context.Background()
	uow, ids, _, hop := waitFixture(t, waitDurationDocument(600))

	handler := runtime.NewWaitTimeoutHandler(uow, ids)
	job := claimableWaitTimerJob(t, uow, hop.NextWaitRegistrationID)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	registration, err := uow.Snapshot.Wait().GetWaitRegistration(ctx, hop.NextWaitRegistrationID)
	if err != nil {
		t.Fatalf("GetWaitRegistration: %v", err)
	}
	if registration.State != runtimedomain.WaitRegistrationElapsed {
		t.Fatalf("registration = %+v, want ELAPSED", registration)
	}
	pauseNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(pause): %v", err)
	}
	if pauseNodeRun.State != runtimedomain.NodeRunSucceeded || pauseNodeRun.SelectedOutcome != "resumed" {
		t.Fatalf("pause node run = %+v, want SUCCEEDED/resumed", pauseNodeRun)
	}
}

func TestWaitTimeoutHandler_Signal_TimesOutAndRoutesViaTimeoutOutcome(t *testing.T) {
	ctx := context.Background()
	uow, ids, _, hop := waitFixture(t, waitSignalDocument(600))

	handler := runtime.NewWaitTimeoutHandler(uow, ids)
	job := claimableWaitTimerJob(t, uow, hop.NextWaitRegistrationID)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	registration, err := uow.Snapshot.Wait().GetWaitRegistration(ctx, hop.NextWaitRegistrationID)
	if err != nil {
		t.Fatalf("GetWaitRegistration: %v", err)
	}
	if registration.State != runtimedomain.WaitRegistrationTimedOut {
		t.Fatalf("registration = %+v, want TIMED_OUT", registration)
	}
	pauseNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(pause): %v", err)
	}
	if pauseNodeRun.State != runtimedomain.NodeRunSucceeded || pauseNodeRun.SelectedOutcome != "expired" {
		t.Fatalf("pause node run = %+v, want SUCCEEDED/expired", pauseNodeRun)
	}
}

// TestWaitTimeoutHandler_ReplayAfterAlreadyConsumed_IsNoOp proves a
// redelivered/replayed timer job never consumes a registration a real
// signal already won — GC-INV-31's own "durable job chỉ đánh thức timer và
// không phải authority của signal", and this task's own explicit Verify
// line ("xóa/replay timer job không consume thêm lần nào").
func TestWaitTimeoutHandler_ReplayAfterAlreadyConsumed_IsNoOp(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := waitFixture(t, waitSignalDocument(600))

	if _, err := runtime.SignalWait(ctx, uow, ids, testCommand("idem-signal-1", "hash-1", ports.ProjectScope("project-1"), "SignalWait"), runtime.SignalWaitRequest{
		RunID: runID, WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-1", Payload: json.RawMessage(`{"status":"green"}`),
	}); err != nil {
		t.Fatalf("SignalWait: %v", err)
	}

	handler := runtime.NewWaitTimeoutHandler(uow, ids)
	job := claimableWaitTimerJob(t, uow, hop.NextWaitRegistrationID)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle (replayed timer job): %v", err)
	}

	registration, err := uow.Snapshot.Wait().GetWaitRegistration(ctx, hop.NextWaitRegistrationID)
	if err != nil {
		t.Fatalf("GetWaitRegistration: %v", err)
	}
	if registration.State != runtimedomain.WaitRegistrationConsumed {
		t.Fatalf("registration = %+v, want unchanged CONSUMED (replayed timer must never overwrite it)", registration)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	routedCount := 0
	for _, e := range events {
		if e.EventType == runtime.NodeRoutedEventType && e.AggregateID == hop.NextNodeRunID {
			routedCount++
		}
	}
	if routedCount != 1 {
		t.Fatalf("NODE_ROUTED count = %d, want exactly 1 (replayed timer must never re-route)", routedCount)
	}
}

// claimableWaitTimerJob builds a ports.DurableJob for the real WAIT_TIMER
// job the WAIT dispatch already enqueued for waitRegistrationID — test
// setup standing in for a real workerpool.Pool.ClaimJob call, mirroring
// execute_test.go's own claimableExecuteNodeJob.
func claimableWaitTimerJob(t *testing.T, uow *fake.UnitOfWork, waitRegistrationID string) ports.DurableJob {
	t.Helper()
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var found *ports.EnqueueJobRequest
	for i := range jobs {
		if jobs[i].Kind == runtime.WaitTimerJobKind && jobs[i].AggregateID == waitRegistrationID {
			found = &jobs[i]
		}
	}
	if found == nil {
		t.Fatalf("no %s job found for registration %s among %+v", runtime.WaitTimerJobKind, waitRegistrationID, jobs)
	}
	leaseUntil := time.Now().Add(time.Minute)
	lease := ports.JobLease{JobID: found.ID, Owner: "worker-1", Token: 1, LeaseUntil: leaseUntil}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(found.ID), lease)
	return ports.DurableJob{
		ID: found.ID, AggregateType: found.AggregateType, AggregateID: found.AggregateID,
		Payload: found.Payload, LeaseOwner: lease.Owner, LeaseToken: lease.Token, LeaseUntil: &leaseUntil,
	}
}
