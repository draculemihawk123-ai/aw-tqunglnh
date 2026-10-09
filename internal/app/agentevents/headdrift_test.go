package agentevents_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

// V11-01: the WORKSPACE_HEAD_DRIFT diagnostic — typed, bounded, sanitized,
// redacted, fenced — like V9-09's scope violation list.

const (
	driftPinned = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	driftHead   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func driftCommits(n int) []ports.HeadAuditCommit {
	commits := make([]ports.HeadAuditCommit, 0, n)
	for i := 0; i < n; i++ {
		commits = append(commits, ports.HeadAuditCommit{
			CommitID: fmt.Sprintf("%08d%032d", i+1, 0), AuthorName: "Ide User", AuthorEmail: "ide@example.invalid",
			AuthoredAt: time.Date(2026, 10, 1, 0, 0, i, 0, time.UTC), Subject: fmt.Sprintf("change %d", i+1),
		})
	}
	return commits
}

func TestWorkspaceHeadDriftDetail_NamesTheCommitsAndTheWayOut(t *testing.T) {
	detail := agentevents.WorkspaceHeadDriftDetail(agentevents.HeadDriftFacts{
		RepositoryID: "repo-1", Pinned: driftPinned, Head: driftHead, Audited: true, FastForward: true,
		TotalCommits: 2, Commits: driftCommits(2),
	})
	for _, want := range []string{
		"WORKSPACE_HEAD_DRIFT", "repository repo-1", "bbbbbbbb", "aaaaaaaa", "2 commit(s) were made outside aw",
		"00000001 Ide User", `"change 1"`, `"change 2"`, "adopt-head", "start the run again",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail %q does not contain %q", detail, want)
		}
	}
}

func TestWorkspaceHeadDriftDetail_CapsTheListAndSaysHowManyMore(t *testing.T) {
	detail := agentevents.WorkspaceHeadDriftDetail(agentevents.HeadDriftFacts{
		RepositoryID: "repo-1", Pinned: driftPinned, Head: driftHead, Audited: true, FastForward: true,
		TotalCommits: 12, Commits: driftCommits(8),
	})
	if !strings.Contains(detail, "12 commit(s)") || !strings.Contains(detail, "and 7 more") {
		t.Fatalf("detail %q must name five commits and say 7 more of 12 exist", detail)
	}
	// The newest commits are the ones named.
	if !strings.Contains(detail, `"change 8"`) || strings.Contains(detail, `"change 1"`) {
		t.Fatalf("detail %q must name the newest five commits", detail)
	}
}

func TestWorkspaceHeadDriftDetail_NonFastForwardDoesNotOfferAdoption(t *testing.T) {
	detail := agentevents.WorkspaceHeadDriftDetail(agentevents.HeadDriftFacts{
		RepositoryID: "repo-1", Pinned: driftPinned, Head: driftHead, Audited: true, FastForward: false,
	})
	if strings.Contains(detail, "run `aw repository-workspace adopt-head`") || !strings.Contains(detail, "cannot adopt") || !strings.Contains(detail, "aaaaaaaa") {
		t.Fatalf("detail %q must explain that HEAD cannot be adopted and name the revision to return to", detail)
	}
}

func TestWorkspaceHeadDriftDetail_SanitizesAndBoundsHostileCommits(t *testing.T) {
	hostile := ports.HeadAuditCommit{
		CommitID: driftHead, AuthorName: "evil\x1b[31m" + strings.Repeat("a", 500),
		Subject: "line1\nline2 " + strings.Repeat("s", 5000),
	}
	detail := agentevents.WorkspaceHeadDriftDetail(agentevents.HeadDriftFacts{
		RepositoryID: "repo-1", Pinned: driftPinned, Head: driftHead, Audited: true, FastForward: true,
		TotalCommits: 1, Commits: []ports.HeadAuditCommit{hostile},
	})
	if strings.ContainsAny(detail, "\n\x1b") {
		t.Fatalf("detail %q carries a control character", detail)
	}
	if len([]rune(detail)) > 900 {
		t.Fatalf("detail is %d runes; a hostile commit must be truncated", len([]rune(detail)))
	}
}

func TestWorkspaceHeadDriftDetail_WithoutAuditSaysTheCommitsCouldNotBeListed(t *testing.T) {
	detail := agentevents.WorkspaceHeadDriftDetail(agentevents.HeadDriftFacts{RepositoryID: "repo-1", Pinned: driftPinned, Head: driftHead})
	if !strings.Contains(detail, "WORKSPACE_HEAD_DRIFT") || !strings.Contains(detail, "could not list") {
		t.Fatalf("detail %q must stay typed and say the commits could not be listed", detail)
	}
}

func TestRecordWorkspaceHeadDrift_AppendsOneOrchestratorDiagnostic(t *testing.T) {
	uow := fake.New()
	cfg := recordConfig(t, uow, "attempt-1", redact.NewMatcher())
	facts := agentevents.HeadDriftFacts{
		RepositoryID: "repo-1", Pinned: driftPinned, Head: driftHead, Audited: true, FastForward: true, TotalCommits: 1, Commits: driftCommits(1),
	}
	if err := agentevents.RecordWorkspaceHeadDrift(context.Background(), cfg, facts); err != nil {
		t.Fatalf("RecordWorkspaceHeadDrift: %v", err)
	}
	rows := agentEventRows(t, uow, "attempt-1")
	if len(rows) != 1 || rows[0].Kind != string(ports.AgentEventDiagnostic) {
		t.Fatalf("rows = %+v, want one DIAGNOSTIC", rows)
	}
	if !strings.Contains(rows[0].PayloadJSON, `"source":"orchestrator"`) {
		t.Fatalf("payload %s must be marked as orchestrator-authored", rows[0].PayloadJSON)
	}
	if got := agentevents.WorkspaceHeadDriftDetailFromRecords(rows); !strings.HasPrefix(got, "WORKSPACE_HEAD_DRIFT:") {
		t.Fatalf("detail read back = %q", got)
	}
	// A scope violation reader must not pick the drift diagnostic up, and vice versa.
	if got := agentevents.ScopeViolationDetailFromRecords(rows); got != "" {
		t.Fatalf("ScopeViolationDetailFromRecords = %q, want empty for a drift diagnostic", got)
	}
}

func TestRecordWorkspaceHeadDrift_IsFencedByTheJobLease(t *testing.T) {
	uow := fake.New()
	cfg := recordConfig(t, uow, "attempt-1", redact.NewMatcher())
	stolen := ports.JobLease{JobID: cfg.JobLease.JobID, Owner: "worker-2", Token: 2, LeaseUntil: time.Now().Add(time.Hour)}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(cfg.JobLease.JobID), stolen)

	err := agentevents.RecordWorkspaceHeadDrift(context.Background(), cfg, agentevents.HeadDriftFacts{RepositoryID: "repo-1", Pinned: driftPinned, Head: driftHead})
	if !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("error = %v, want ErrJobLeaseLost", err)
	}
}

func TestRecordWorkspaceHeadDrift_RedactsKnownSecrets(t *testing.T) {
	uow := fake.New()
	secret := "s3cr3t-token-value"
	facts := agentevents.HeadDriftFacts{
		RepositoryID: "repo-1", Pinned: driftPinned, Head: driftHead, Audited: true, FastForward: true, TotalCommits: 1,
		Commits: []ports.HeadAuditCommit{{CommitID: driftHead, AuthorName: "x", Subject: "rotate " + secret}},
	}
	if err := agentevents.RecordWorkspaceHeadDrift(context.Background(), recordConfig(t, uow, "attempt-1", redact.NewMatcher(secret)), facts); err != nil {
		t.Fatalf("RecordWorkspaceHeadDrift: %v", err)
	}
	rows := agentEventRows(t, uow, "attempt-1")
	if len(rows) != 1 || strings.Contains(rows[0].PayloadJSON, secret) || !strings.Contains(rows[0].PayloadJSON, "[REDACTED]") {
		t.Fatalf("stored payload %v must mask the secret in a commit subject", rows)
	}
}
