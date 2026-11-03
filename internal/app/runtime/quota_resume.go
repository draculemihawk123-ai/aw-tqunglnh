// V12-03 (docs/design/15-v12-ui-operations.md, ADR-046): an AGENT attempt that hits the provider's usage limit no longer fails the
// run. Before, PROVIDER_QUOTA_EXCEEDED (V11-02) was a FAILED attempt with a never-retryable code, so the run FAILED and a rerun
// started again from START on a worktree full of half-made changes (chochuyennha P1-01: `fix` hit the 5-hour limit, the rerun
// began at `repro`, whose agent may not touch product code, and looped). Now the attempt ends BLOCKED, the NodeRun waits BLOCKED
// behind a PROVIDER_QUOTA_EXCEEDED blocker, and the SAME node runs again as a new activation — by itself when the provider's
// reset time passes (a PROVIDER_QUOTA_RESUME job, available at that time), or when the operator retries the blocked node.
//
// The resume goes through RetryBlockedActivation, the one path that may resolve an admission-style blocker: it re-runs the
// admission checks against the same pins (never a repin) and creates the new activation in one transaction. Iteration is copied,
// so waiting on a limit never consumes a cycle budget.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// ProviderQuotaResumeJobKind is the durable job that retries a NodeRun BLOCKED by PROVIDER_QUOTA_EXCEEDED once the provider's
// limit has reset.
const ProviderQuotaResumeJobKind = "PROVIDER_QUOTA_RESUME"

const (
	defaultProviderQuotaResumeJobMaxClaims = 3
	// DefaultQuotaResumeDelay is how long the engine waits before retrying when the provider's words name no reset time.
	DefaultQuotaResumeDelay = time.Hour
	// quotaResumeMargin is added to a reset time the provider named, so the retry does not land a few seconds early.
	quotaResumeMargin = 2 * time.Minute
	// MaxAutomaticQuotaResumes bounds how often one node of one run is resumed by itself. A limit that is not a time window
	// ("credit balance is too low") would otherwise be retried every hour for ever; after this many automatic resumes the
	// NodeRun stays BLOCKED until the operator retries it.
	MaxAutomaticQuotaResumes = 6
	// QuotaResumeActor is the actor recorded on a blocker the engine resolved by itself.
	QuotaResumeActor = "system:provider-quota-resume"
	// maxBlockedDetailBytes bounds the provider's words stored in the blocker reason.
	maxBlockedDetailBytes = 600
)

// ProviderQuotaResumeJobPayload is what a ProviderQuotaResumeJobKind job carries.
type ProviderQuotaResumeJobPayload struct {
	RunID     string `json:"runId"`
	NodeRunID string `json:"nodeRunId"`
}

// resetPattern matches the reset clause of the providers' limit messages: "resets 5pm", "resets 2:30am (Europe/Paris)",
// "resets at 17:00", "reset at 9:05 PM (Asia/Ho_Chi_Minh)".
var resetPattern = regexp.MustCompile(`(?i)\bresets?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?(?:\s*\(([A-Za-z_]+(?:/[A-Za-z0-9_+\-]+)*)\))?`)

// ParseQuotaReset reads the time a provider's usage limit resets from its own words, as the next such wall-clock time after
// now (in the time zone the message names, else now's location). ok is false when the words name no reset time.
func ParseQuotaReset(message string, now time.Time) (resetAt time.Time, ok bool) {
	match := resetPattern.FindStringSubmatch(message)
	if match == nil {
		return time.Time{}, false
	}
	hour, err := strconv.Atoi(match[1])
	if err != nil {
		return time.Time{}, false
	}
	minute := 0
	if match[2] != "" {
		if minute, err = strconv.Atoi(match[2]); err != nil || minute > 59 {
			return time.Time{}, false
		}
	}
	switch strings.ToLower(match[3]) {
	case "am":
		if hour < 1 || hour > 12 {
			return time.Time{}, false
		}
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour < 1 || hour > 12 {
			return time.Time{}, false
		}
		if hour != 12 {
			hour += 12
		}
	default:
		if hour > 23 || match[2] == "" { // a bare "resets 5" is not a time
			return time.Time{}, false
		}
	}
	location := now.Location()
	if match[4] != "" {
		if loaded, err := time.LoadLocation(match[4]); err == nil {
			location = loaded
		}
	}
	local := now.In(location)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, location)
	if !candidate.After(local) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate, true
}

// quotaResumeTime is when a PROVIDER_QUOTA_RESUME job becomes available: the reset time plus a margin, or now plus the default
// delay when none is known.
func quotaResumeTime(resumeAt *time.Time, now time.Time) time.Time {
	if resumeAt == nil || resumeAt.IsZero() {
		return now.Add(DefaultQuotaResumeDelay)
	}
	at := resumeAt.Add(quotaResumeMargin)
	if at.Before(now) {
		return now
	}
	return at.UTC()
}

// quotaBlockedDetail is the blocker reason: the provider's words (bounded) and when aw will try again.
func quotaBlockedDetail(providerWords string, resumeAt time.Time, automatic bool) string {
	words := strings.TrimSpace(providerWords)
	if len(words) > maxBlockedDetailBytes {
		words = words[:maxBlockedDetailBytes] + "…"
	}
	if words == "" {
		words = "the provider refused the work because the account hit a usage limit"
	}
	if !automatic {
		return fmt.Sprintf("%s — automatic resumes for this node are used up (%d); retry the blocked node when the limit has reset", words, MaxAutomaticQuotaResumes)
	}
	return fmt.Sprintf("%s — aw resumes this node by itself at %s, or retry it now", words, resumeAt.UTC().Format(time.RFC3339))
}

// blockForProviderQuotaTx is FinalizeExecutionAttempt's BLOCKED branch for PROVIDER_QUOTA_EXCEEDED: the NodeRun goes
// RUNNING -> BLOCKED, a PROVIDER_QUOTA_EXCEEDED blocker opens under the admission-blocker id RetryBlockedActivation reads, and a
// PROVIDER_QUOTA_RESUME job is enqueued for the reset time — unless this node of this run has already been resumed
// MaxAutomaticQuotaResumes times, in which case it waits for the operator.
func blockForProviderQuotaTx(
	ctx context.Context, tx ports.Tx, ids idsource.Source, now time.Time,
	req FinalizeExecutionAttemptRequest, run runtimedomain.WorkflowRun,
) error {
	nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
	if err != nil {
		return err
	}
	if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
		NodeRunID: req.NodeRunID, ExpectedState: runtimedomain.NodeRunRunning, ExpectedVersion: nodeRun.Version,
		NextState: runtimedomain.NodeRunBlocked,
	}); err != nil {
		return err
	}

	earlier, err := quotaBlocksOfNode(ctx, tx, run, nodeRun.NodeKey, req.AttemptID)
	if err != nil {
		return err
	}
	automatic := earlier < MaxAutomaticQuotaResumes
	resumeAt := quotaResumeTime(req.ResumeAt, now)
	if _, err := openWorkItemBlockerTx(
		ctx, tx, run.ProjectID, string(run.WorkItemID), req.AttemptID+"-admission-blocker", workdomain.BlockerProviderQuotaExceeded,
		req.RunID, req.NodeRunID, req.AttemptID, quotaBlockedDetail(req.BlockedDetail, resumeAt, automatic), req.CorrelationID, string(req.JobLease.JobID),
	); err != nil {
		return err
	}
	if automatic {
		payload, err := json.Marshal(ProviderQuotaResumeJobPayload{RunID: req.RunID, NodeRunID: req.NodeRunID})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", ProviderQuotaResumeJobKind, err)
		}
		if _, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(ids.NewID()), ProjectID: run.ProjectID, Kind: ProviderQuotaResumeJobKind,
			AggregateType: "NodeRun", AggregateID: req.NodeRunID, Payload: payload, AvailableAt: resumeAt,
			MaxClaims: defaultProviderQuotaResumeJobMaxClaims, IdempotencyKey: "provider-quota-resume:" + req.NodeRunID,
			RunID: req.RunID,
		}); err != nil {
			return err
		}
	}
	version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
	if err != nil {
		return err
	}
	return reconcileRunTerminalityTx(ctx, tx, run, version.Document(), req.CorrelationID, string(req.JobLease.JobID))
}

// quotaBlocksOfNode counts the attempts of nodeKey in run, other than exceptAttemptID, that ended BLOCKED for the provider's
// usage limit.
func quotaBlocksOfNode(ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, nodeKey, exceptAttemptID string) (int, error) {
	nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, string(run.ID))
	if err != nil {
		return 0, err
	}
	ofNode := map[runtimedomain.NodeRunID]bool{}
	for _, nr := range nodeRuns {
		if nr.NodeKey == nodeKey {
			ofNode[nr.ID] = true
		}
	}
	attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, string(run.ID))
	if err != nil {
		return 0, err
	}
	count := 0
	for _, attempt := range attempts {
		if string(attempt.ID) != exceptAttemptID && ofNode[attempt.NodeRunID] &&
			attempt.State == runtimedomain.ExecutionAttemptBlocked &&
			attempt.TerminationReason == runtimedomain.TerminationReasonProviderQuotaExceeded {
			count++
		}
	}
	return count, nil
}

// ProviderQuotaResumeHandler implements workerpool.Handler for ProviderQuotaResumeJobKind: it retries the blocked NodeRun through
// RetryBlockedActivation. A NodeRun that is no longer blocked (the operator already retried it), or a run that ended meanwhile,
// is a no-op; an admission check that now fails leaves the blocker open with that reason, exactly like a manual retry.
type ProviderQuotaResumeHandler struct {
	retry *RetryBlockedActivationHandler
}

// NewProviderQuotaResumeHandler returns a ready-to-register handler.
func NewProviderQuotaResumeHandler(retry *RetryBlockedActivationHandler) *ProviderQuotaResumeHandler {
	return &ProviderQuotaResumeHandler{retry: retry}
}

var _ workerpool.Handler = (*ProviderQuotaResumeHandler)(nil)

// Handle implements workerpool.Handler.
func (h *ProviderQuotaResumeHandler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload ProviderQuotaResumeJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.NodeRunID == "" {
		return fmt.Errorf("runtime: %s job %s has no nodeRunId", ProviderQuotaResumeJobKind, job.ID)
	}
	_, err := h.retry.Retry(ctx, RetryBlockedActivationRequest{
		NodeRunID: payload.NodeRunID, Actor: QuotaResumeActor, CorrelationID: string(job.ID),
		Reason: "the provider's usage limit has reset; resuming the same node",
	})
	switch {
	case err == nil, errors.Is(err, ErrNodeRunNotBlocked), errors.Is(err, ErrRunNotRetryable):
		return nil // the pool completes the job
	default:
		return err
	}
}
