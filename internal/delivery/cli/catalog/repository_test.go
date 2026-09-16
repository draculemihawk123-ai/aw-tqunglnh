package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	clicatalog "github.com/taQuangLing/agent-workflow/internal/delivery/cli/catalog"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func TestRunRepositoryRegister_ReturnsRegisteringStatusNeverFakedActive(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")

	var stdout, stderr bytes.Buffer
	body := `{"repositoryId":"repo-1","name":"svc","remoteLocator":"https://example.invalid/repo.git","defaultRef":"main"}`
	args := []string{"--project-id", projectID, "--idempotency-key", "key-repo"}
	if err := clicatalog.RunRepositoryRegister(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryRegister() error = %v, stderr = %s", err, stderr.String())
	}
	var result appcatalog.RegisterRepositoryResult
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Status != string(project.RepositoryRegistering) {
		t.Fatalf("Status = %q, want REGISTERING", result.Status)
	}
	if result.ProbeJobID == "" {
		t.Fatal("ProbeJobID is empty, want a freshly enqueued probe job id")
	}
}

// TestRunRepositoryOnboarding_NoProbeYet_ShowsInProgressWithEmptyAttempts
// is half of the "async onboarding" Verify bullet: right after
// registration (before any REPOSITORY_PROBE job has run), the onboarding
// view must show the repository still in flight (REGISTERING) with no
// probe-history evidence yet — never a faked ACTIVE/BLOCKED verdict.
func TestRunRepositoryOnboarding_NoProbeYet_ShowsInProgressWithEmptyAttempts(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")

	var stdout bytes.Buffer
	if err := clicatalog.RunRepositoryOnboarding(context.Background(), deps, []string{"repo-1"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRepositoryOnboarding() error = %v", err)
	}
	var view struct {
		RepositoryID string        `json:"repositoryId"`
		Status       string        `json:"status"`
		Attempts     []interface{} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if view.RepositoryID != "repo-1" || view.Status != string(project.RepositoryRegistering) {
		t.Fatalf("view = %+v, want RepositoryID=repo-1 Status=REGISTERING", view)
	}
	if len(view.Attempts) != 0 {
		t.Fatalf("attempts = %+v, want empty (no probe has run yet)", view.Attempts)
	}
}

// TestRunRepositoryOnboarding_ProbeCompleted_ShowsActiveWithAttemptEvidence
// is the other half of "async onboarding": once a probe has actually
// completed (simulated here via completeRepositoryProbeActive, standing in
// for V3-02's own REPOSITORY_PROBE worker), the SAME onboarding query must
// now report the terminal status plus the attempt(s) that produced it —
// proving this leaf reads live state on every call, never a cached
// snapshot from registration time.
func TestRunRepositoryOnboarding_ProbeCompleted_ShowsActiveWithAttemptEvidence(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")
	completeRepositoryProbeActive(t, deps.UoW, projectID, "repo-1", "probe-job-1")

	var stdout bytes.Buffer
	if err := clicatalog.RunRepositoryOnboarding(context.Background(), deps, []string{"repo-1"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRepositoryOnboarding() error = %v", err)
	}
	var view struct {
		Status   string `json:"status"`
		Attempts []struct {
			JobID  string `json:"jobId"`
			State  string `json:"state"`
			Result string `json:"result"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if view.Status != string(project.RepositoryActive) {
		t.Fatalf("Status = %q, want ACTIVE", view.Status)
	}
	if len(view.Attempts) != 1 {
		t.Fatalf("attempts = %+v, want exactly 1", view.Attempts)
	}
	if view.Attempts[0].JobID != "probe-job-1" || view.Attempts[0].State != "SUCCEEDED" || view.Attempts[0].Result != string(project.RepositoryActive) {
		t.Fatalf("attempt = %+v, want JobID=probe-job-1 State=SUCCEEDED Result=ACTIVE", view.Attempts[0])
	}
}

// TestRunRepositoryRetryProbe_NotBlocked_ReturnsErrorAndNeverDispatches is
// this task's own "Retry" Verify bullet's negative half, proven against a
// real side effect rather than a mock: a REGISTERING repository's own
// Version/Status must be byte-for-byte unchanged after a rejected
// retry-probe call, which is only true if the real
// appcatalog.RetryRepositoryProbe command was never actually invoked.
func TestRunRepositoryRetryProbe_NotBlocked_ReturnsErrorAndNeverDispatches(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")

	var stdout, stderr bytes.Buffer
	// Flags must precede the positional repositoryId: Go's stdlib flag
	// package stops parsing flags at the first non-flag token, so
	// "repo-1" would otherwise swallow everything after it as extra
	// positional arguments.
	args := []string{"--expected-version", "1", "--idempotency-key", "key-retry", "repo-1"}
	err := clicatalog.RunRepositoryRetryProbe(context.Background(), deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryRetryProbe() against a REGISTERING repository returned nil error, want a conflict error")
	}
	if isUsageError(err) {
		t.Fatalf("RunRepositoryRetryProbe() returned a cli.UsageError (%v), want a state-conflict error (not a usage error)", err)
	}

	repo, err := appcatalog.GetRepository(context.Background(), deps.UoW, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryRegistering || repo.Version != 1 {
		t.Fatalf("repository = %+v, want unchanged Status=REGISTERING Version=1", repo)
	}
}

func TestRunRepositoryRetryProbe_MissingExpectedVersion_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")

	var stdout, stderr bytes.Buffer
	err := clicatalog.RunRepositoryRetryProbe(context.Background(), deps, []string{"repo-1"}, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunRepositoryRetryProbe() with no --expected-version returned %v, want a cli.UsageError", err)
	}
}

// TestRunRepositoryRetryProbe_Blocked_TransitionsToProbingAndEnqueuesNewJob
// is "Retry"'s positive half: a genuinely BLOCKED repository transitions
// to PROBING and a fresh probe job is enqueued (never the old failed job's
// row).
func TestRunRepositoryRetryProbe_Blocked_TransitionsToProbingAndEnqueuesNewJob(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")
	version := moveRepositoryToBlocked(t, deps.UoW, "repo-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", fmt.Sprintf("%d", version), "--idempotency-key", "key-retry", "repo-1"}
	if err := clicatalog.RunRepositoryRetryProbe(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryRetryProbe() error = %v, stderr = %s", err, stderr.String())
	}
	var result appcatalog.RetryRepositoryProbeResult
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Status != string(project.RepositoryProbing) {
		t.Fatalf("Status = %q, want PROBING", result.Status)
	}
	if result.ProbeJobID == "" {
		t.Fatal("ProbeJobID is empty, want a freshly enqueued probe job id")
	}

	repo, err := appcatalog.GetRepository(context.Background(), deps.UoW, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryProbing || repo.Version != version+1 {
		t.Fatalf("repository = %+v, want Status=PROBING Version=%d", repo, version+1)
	}
}

// TestRunRepositoryRetryProbe_ReplaySameKey_WinsOverStateDrift is this
// task's own "Replay" Verify bullet applied to retry-probe: the FIRST
// retry-probe call (BLOCKED->PROBING) succeeds; a SECOND call with the
// identical --idempotency-key — now that the repository has already moved
// past BLOCKED — must still replay the original accepted result, never
// re-run the CAS or reject with "not BLOCKED". This is only true because
// RunRepositoryRetryProbe's own "is this BLOCKED" precondition check lives
// INSIDE the cli.Dispatch execute closure, reached only on a genuinely
// fresh (non-replayed) attempt.
func TestRunRepositoryRetryProbe_ReplaySameKey_WinsOverStateDrift(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo", "repo-1")
	version := moveRepositoryToBlocked(t, deps.UoW, "repo-1")

	args := []string{"--expected-version", fmt.Sprintf("%d", version), "--idempotency-key", "key-retry", "repo-1"}

	var first bytes.Buffer
	if err := clicatalog.RunRepositoryRetryProbe(context.Background(), deps, args, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunRepositoryRetryProbe() error = %v", err)
	}
	firstJobID := jsonField(t, resultField(t, first.String()), "probeJobId")

	// The repository is now PROBING (not BLOCKED); a genuinely new
	// retry-probe attempt against it would fail, per
	// TestRunRepositoryRetryProbe_NotBlocked_ReturnsErrorAndNeverDispatches
	// above. The SAME idempotency key must not hit that path.
	var second bytes.Buffer
	if err := clicatalog.RunRepositoryRetryProbe(context.Background(), deps, args, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second (replay) RunRepositoryRetryProbe() error = %v, want nil (must replay, not re-check BLOCKED)", err)
	}
	var secondEnvelope struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode second stdout %s: %v", second.String(), err)
	}
	if !secondEnvelope.Replayed {
		t.Fatal("second call reported Replayed = false, want true")
	}
	secondJobID := jsonField(t, resultField(t, second.String()), "probeJobId")
	if secondJobID != firstJobID {
		t.Fatalf("replay probeJobId = %q, want the exact original %q", secondJobID, firstJobID)
	}

	repo, err := appcatalog.GetRepository(context.Background(), deps.UoW, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Version != version+1 {
		t.Fatalf("repository Version = %d, want %d (the replay must never re-run the CAS a second time)", repo.Version, version+1)
	}
}

func TestRunRepositoryList_ReturnsRegisteredRepositories(t *testing.T) {
	deps := newTestDeps(t)
	projectID := mustCreateProject(t, deps, "key-project", "widget")
	mustRegisterRepository(t, deps, projectID, "key-repo-a", "repo-a")
	mustRegisterRepository(t, deps, projectID, "key-repo-b", "repo-b")

	var stdout bytes.Buffer
	if err := clicatalog.RunRepositoryList(context.Background(), deps, []string{projectID}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRepositoryList() error = %v", err)
	}
	var body struct {
		Repositories []struct {
			ID string `json:"id"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if len(body.Repositories) != 2 {
		t.Fatalf("repositories = %+v, want exactly 2", body.Repositories)
	}
}

func TestRunRepositoryList_UnknownProject_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clicatalog.RunRepositoryList(context.Background(), deps, []string{"does-not-exist"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryList() for an unknown project returned nil error")
	}
}
