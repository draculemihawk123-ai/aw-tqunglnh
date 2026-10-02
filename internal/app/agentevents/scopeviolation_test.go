package agentevents_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V9-09 / B3: the violation list an operator reads back from the run
// timeline — bounded, sanitized, redacted, fenced.

func violations(n int) error {
	list := make([]scopeguard.Violation, 0, n)
	for i := 0; i < n; i++ {
		list = append(list, scopeguard.Violation{RepositoryID: "repo-1", Path: fmt.Sprintf("out/file-%02d.txt", i), Reason: "path is not covered by a WRITE repository scope"})
	}
	return scopeguard.NewViolationsError(list)
}

func TestScopeViolationDetail_NamesEveryPathUpToTheCap(t *testing.T) {
	detail := agentevents.ScopeViolationDetail(violations(2))
	if detail != "2 path(s) outside the granted scope: repo-1:out/file-00.txt; repo-1:out/file-01.txt" {
		t.Fatalf("detail = %q", detail)
	}
	exactlyCap := agentevents.ScopeViolationDetail(violations(agentevents.MaxReportedViolations))
	if strings.Contains(exactlyCap, "more") || strings.Count(exactlyCap, "repo-1:") != agentevents.MaxReportedViolations {
		t.Fatalf("detail at exactly the cap = %q, want every path and no 'more' suffix", exactlyCap)
	}
}

func TestScopeViolationDetail_CapsLongListsAndSaysHowManyMore(t *testing.T) {
	detail := agentevents.ScopeViolationDetail(violations(agentevents.MaxReportedViolations + 7))
	if !strings.HasPrefix(detail, "27 path(s) outside the granted scope: ") || !strings.HasSuffix(detail, "; and 7 more") {
		t.Fatalf("detail = %q, want the 27-path summary ending in '; and 7 more'", detail)
	}
	if strings.Count(detail, "repo-1:") != agentevents.MaxReportedViolations {
		t.Fatalf("detail names %d paths, want exactly %d", strings.Count(detail, "repo-1:"), agentevents.MaxReportedViolations)
	}
}

func TestScopeViolationDetail_SanitizesAndBoundsHostilePaths(t *testing.T) {
	hostile := scopeguard.NewViolationsError([]scopeguard.Violation{
		{RepositoryID: "repo-1", Path: "a\nFAKE LINE\x1b[31m.txt", Reason: "x"},
		{RepositoryID: "repo-1", Path: strings.Repeat("p", 5000), Reason: "x"},
	})
	detail := agentevents.ScopeViolationDetail(hostile)
	if strings.ContainsAny(detail, "\n\x1b") {
		t.Fatalf("detail %q carries a control character", detail)
	}
	if len([]rune(detail)) > 600 {
		t.Fatalf("detail is %d runes; a 5000-character path must be truncated", len([]rune(detail)))
	}
}

// An error that wraps ErrScopeViolation without the structured list (any
// other producer) still reports its own message, bounded.
func TestScopeViolationDetail_FallsBackToTheMessageOfAnUnstructuredError(t *testing.T) {
	unstructured := fmt.Errorf("%w: mount repo-1 changed 3 file(s) despite being read-only by design", scopeguard.ErrScopeViolation)
	if detail := agentevents.ScopeViolationDetail(unstructured); !strings.Contains(detail, "repo-1 changed 3 file(s)") {
		t.Fatalf("detail = %q, want the unstructured message", detail)
	}
	long := fmt.Errorf("%w: %s", scopeguard.ErrScopeViolation, strings.Repeat("x", 10000))
	if got := len([]rune(agentevents.ScopeViolationDetail(long))); got > 1100 {
		t.Fatalf("fallback detail is %d runes, want it bounded", got)
	}
	if agentevents.ScopeViolationDetail(nil) != "" {
		t.Fatal("a nil error must have no detail")
	}
}

func recordConfig(t *testing.T, uow *fake.UnitOfWork, attemptID string, matcher redact.Matcher) agentevents.ScopeViolationRecord {
	t.Helper()
	return recordConfigWithLease(uow, attemptID, matcher, testJobLease(t, uow, attemptID))
}

func recordConfigWithLease(uow *fake.UnitOfWork, attemptID string, matcher redact.Matcher, lease ports.JobLease) agentevents.ScopeViolationRecord {
	return agentevents.ScopeViolationRecord{
		AttemptID: attemptID, JobLease: lease, UOW: uow,
		IDs: idsource.NewSequential("diag"), Clock: clock.NewFixed(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)), Matcher: matcher,
	}
}

func TestRecordScopeViolation_AppendsOneDiagnosticAfterTheProviderEvents(t *testing.T) {
	uow := fake.New()
	sink, lease := newSinkOn(t, uow, "attempt-1")
	ctx := context.Background()
	if err := sink.Accept(ctx, ports.AgentEvent{AttemptID: "attempt-1", Sequence: 1, Kind: ports.AgentEventExecutionStarted, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := sink.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	cause := fmt.Errorf("consume events: %w", fmt.Errorf("checkpoint at event 7: %w", violations(2)))
	if err := agentevents.RecordScopeViolation(ctx, recordConfigWithLease(uow, "attempt-1", redact.NewMatcher(), lease), cause); err != nil {
		t.Fatalf("RecordScopeViolation: %v", err)
	}

	rows := agentEventRows(t, uow, "attempt-1")
	if len(rows) != 2 || rows[1].Sequence != 2 || rows[1].Kind != string(ports.AgentEventDiagnostic) {
		t.Fatalf("rows = %+v, want the provider event then one DIAGNOSTIC at sequence 2", rows)
	}
	detail := agentevents.ScopeViolationDetailFromRecords(rows)
	if !strings.Contains(detail, "repo-1:out/file-00.txt") || !strings.Contains(detail, "repo-1:out/file-01.txt") {
		t.Fatalf("detail read back = %q, want both violating paths", detail)
	}
	if !strings.Contains(rows[1].PayloadJSON, `"source":"orchestrator"`) {
		t.Fatalf("payload %s must be marked as orchestrator-authored, not provider output", rows[1].PayloadJSON)
	}
}

func TestRecordScopeViolation_IsFencedByTheJobLease(t *testing.T) {
	uow := fake.New()
	cfg := recordConfig(t, uow, "attempt-1", redact.NewMatcher())
	stolen := ports.JobLease{JobID: cfg.JobLease.JobID, Owner: "worker-2", Token: 2, LeaseUntil: time.Now().Add(time.Hour)}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(cfg.JobLease.JobID), stolen)

	err := agentevents.RecordScopeViolation(context.Background(), cfg, violations(1))
	if !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("RecordScopeViolation error = %v, want ErrJobLeaseLost: a stale worker must not annotate an attempt it no longer owns", err)
	}
	if rows := agentEventRows(t, uow, "attempt-1"); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none after a fenced write", rows)
	}
}

func TestRecordScopeViolation_RedactsKnownSecretsInPaths(t *testing.T) {
	uow := fake.New()
	secret := "s3cr3t-token-value"
	cause := scopeguard.NewViolationsError([]scopeguard.Violation{{RepositoryID: "repo-1", Path: "dir/" + secret + ".txt", Reason: "x"}})
	if err := agentevents.RecordScopeViolation(context.Background(), recordConfig(t, uow, "attempt-1", redact.NewMatcher(secret)), cause); err != nil {
		t.Fatalf("RecordScopeViolation: %v", err)
	}
	rows := agentEventRows(t, uow, "attempt-1")
	if len(rows) != 1 || strings.Contains(rows[0].PayloadJSON, secret) || !strings.Contains(rows[0].PayloadJSON, "[REDACTED]") {
		t.Fatalf("stored payload %v must mask the secret embedded in the path", rows)
	}
}

func TestScopeViolationDetailFromRecords_IgnoresOtherDiagnosticsAndTakesTheLast(t *testing.T) {
	uow := fake.New()
	cfg := recordConfig(t, uow, "attempt-1", redact.NewMatcher())
	ctx := context.Background()
	if err := agentevents.RecordScopeViolation(ctx, cfg, violations(1)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := agentevents.RecordScopeViolation(ctx, cfg, violations(2)); err != nil {
		t.Fatalf("second: %v", err)
	}
	rows := agentEventRows(t, uow, "attempt-1")
	if got := agentevents.ScopeViolationDetailFromRecords(rows); !strings.HasPrefix(got, "2 path(s)") {
		t.Fatalf("detail = %q, want the latest recorded violation", got)
	}
	if got := agentevents.ScopeViolationDetailFromRecords(nil); got != "" {
		t.Fatalf("detail of an empty stream = %q, want empty", got)
	}
	other := []ports.AgentEventRecord{{Kind: string(ports.AgentEventDiagnostic), PayloadJSON: `{"diagnostic":{"code":"PROVIDER_REPORTED_FAILURE","message":"nope"}}`}}
	if got := agentevents.ScopeViolationDetailFromRecords(other); got != "" {
		t.Fatalf("detail = %q, want empty: only SCOPE_VIOLATION diagnostics count", got)
	}
}

// TestSink_MidRunCheckpointViolation_CarriesTheTypedPathList proves the Sink
// rejects an out-of-scope checkpoint with the structured list (not just
// text), wrapped with %w so an adapter that keeps %w hands the executor
// something it can both classify and report.
func TestSink_MidRunCheckpointViolation_CarriesTheTypedPathList(t *testing.T) {
	scope, err := work.NewRepositoryScope("family-1", 1, project.RepositoryID("repo-1"), work.RepositoryWrite, []string{"allowed"},
		"root task", "actor-1", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewRepositoryScope: %v", err)
	}
	uow := fake.New()
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	diff := ports.WorkspaceDiff{RepositoryID: "repo-1", Files: []ports.FileStatus{{Code: "A", Path: "leaked.txt"}, {Code: "A", Path: "allowed/ok.txt"}}}
	sink, err := agentevents.NewSink(context.Background(), agentevents.Config{
		RunID: "run-1", NodeRunID: "node-run-1", AttemptID: "attempt-1", ContextSnapshotID: "snapshot-1",
		EffectiveScope: []work.RepositoryScope{scope}, Mounts: []agentevents.Mount{{RepositoryID: "repo-1"}},
		Workspaces: &fakeWorkspaceProvider{diff: diff}, Registry: registry, Matcher: redact.NewMatcher(),
		UOW: uow, Checkpoints: &fakeCheckpointStore{}, IDs: idsource.NewSequential("evt"), Clock: clock.NewFixed(time.Now()),
		JobLease: testJobLease(t, uow, "attempt-1"),
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	acceptErr := sink.Accept(context.Background(), ports.AgentEvent{AttemptID: "attempt-1", Sequence: 1, Kind: ports.AgentEventCheckpointProposed})
	if !errors.Is(acceptErr, scopeguard.ErrScopeViolation) {
		t.Fatalf("Accept error = %v, want ErrScopeViolation", acceptErr)
	}
	var typed *scopeguard.ViolationsError
	if !errors.As(acceptErr, &typed) || len(typed.Violations) != 1 || typed.Violations[0].Path != "leaked.txt" {
		t.Fatalf("Accept error = %v, want a *scopeguard.ViolationsError naming exactly the one out-of-scope path leaked.txt", acceptErr)
	}
}

func newSinkOn(t *testing.T, uow *fake.UnitOfWork, attemptID string) (*agentevents.Sink, ports.JobLease) {
	t.Helper()
	lease := testJobLease(t, uow, attemptID)
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	sink, err := agentevents.NewSink(context.Background(), agentevents.Config{
		RunID: "run-1", NodeRunID: "node-run-1", AttemptID: attemptID, ContextSnapshotID: "snapshot-1",
		Workspaces: &fakeWorkspaceProvider{}, Registry: registry, Matcher: redact.NewMatcher(),
		UOW: uow, Checkpoints: &fakeCheckpointStore{}, IDs: idsource.NewSequential("evt"), Clock: clock.NewFixed(time.Now()),
		JobLease: lease,
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	return sink, lease
}
