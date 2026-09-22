package v6accept

import (
	"net/http"
	"testing"
	"time"
)

// TestV6HTTPAcceptance_Fault_CrashAfterGitCommit is V6-14A scenario 3:
// "crash after Git commit". internal/app/releasesetcommit/execute.go's own
// doc comment documents EXACTLY this crash window and its own recovery
// design: the real `git commit` (ExecuteReleaseSetLocalCommit's own
// deps.Creator.CreateLocalCommit call) runs OUTSIDE any DB transaction;
// only afterward does a SEPARATE finalize transaction record COMMITTED.
// Before ever calling CreateLocalCommit it durably pins the exact parent
// it is about to build on top of, and on any later attempt (a real retry
// after a real crash) FindLocalCommitByMarker re-reads the workspace's own
// real HEAD and, if its message carries this exact operation's own
// deterministic marker trailer, REUSES that commit rather than creating a
// second one — the marker-based reconciliation protocol this scenario
// proves end-to-end from outside the process.
//
// The real signal polled for: `git rev-parse HEAD` run directly against
// the real managed workspace directory (found via soleWorktreeDir — a
// plain directory listing, not a re-derivation of any internal business
// logic) changing away from the base revision captured right before the
// local-commit request. Since CreateLocalCommit's real `git commit` is the
// ONLY thing in this whole flow that ever moves that workspace's HEAD, an
// observed HEAD change is genuine, unambiguous, real proof the commit
// already happened — this is exactly the same observation technique
// stage_release_test.go's own assertOriginUntouched/inspectCommittedWorkspace
// already use (a direct, read-only `git` call against a real repository),
// applied to the MANAGED workspace instead of the operator's own origin
// repository. The worker is hard-killed the instant that change is
// observed, racing to land as close as possible to the real commit-vs-
// finalize boundary; how tight that race genuinely is (and what it proves
// either way it lands) is documented in baocaov6checklist.md's own V6-14A
// section.
//
// Verify: after the worker restarts and the crashed local commit
// eventually reaches COMMITTED, the repository log carries EXACTLY ONE new
// commit beyond the base revision — never two (the marker reused, not a
// second real `git commit`) — and the local-commit's own recorded
// parent/result revisions match the real repository state exactly.
func TestV6HTTPAcceptance_Fault_CrashAfterGitCommit(t *testing.T) {
	requireAcceptance(t)
	// A short worker job-lease TTL/heartbeat AND a short local-commit
	// write-lease TTL: after the hard kill below, BOTH the crashed
	// ReleaseSetLocalCommitJobKind job's own workerpool lease and the
	// SEPARATE write lease releasesetcommit's own worker held across the
	// real `git commit` must genuinely expire before a fresh worker can
	// reclaim and retry — the production defaults (30s/10s job lease, 2
	// minute write lease) would only make this test slow without proving
	// anything different. See stack.workerLeaseTTL/localCommitWriteLeaseTTL's
	// own doc comments.
	j := newFaultRunner(t, func(s *stack) {
		s.workerLeaseTTL, s.workerLeaseHeartbeat = 2*time.Second, 500*time.Millisecond
		s.localCommitWriteLeaseTTL = 3 * time.Second
	})
	j.createRootWorkItem(t, "fault-localcommit-root")
	j.waitWorkspaceReady(t)
	j.releaseRun(t)

	api := j.s.api
	project := "/projects/" + j.projectID
	ws := j.workspace(t)
	worktreeDir := soleWorktreeDir(t, j.s)
	baseHead := runGit(t, worktreeDir, "rev-parse", "HEAD")

	created := api.post(t, project+"/task-families/"+j.familyID+"/release-sets", map[string]any{
		"repositories": []map[string]any{{
			"repositoryId": j.repositoryID, "baseVcsObjectId": ws.CurrentRevision, "resultVcsObjectId": ws.CurrentRevision, "verdict": "PASS",
		}},
	}).requireStatus(t, http.StatusCreated)
	var releaseSet struct {
		ReleaseSetID string `json:"releaseSetId"`
	}
	created.decode(t, &releaseSet)
	if releaseSet.ReleaseSetID == "" {
		t.Fatalf("create release set returned no id: %s", created.body)
	}

	detail := api.get(t, project+"/release-sets/"+releaseSet.ReleaseSetID).requireStatus(t, http.StatusOK)
	sealed := api.post(t, project+"/release-sets/"+releaseSet.ReleaseSetID+"/seal", map[string]any{}, withIfMatch(detail.etag())).requireStatus(t, http.StatusOK)
	var afterSeal struct {
		Version uint64 `json:"version"`
	}
	sealed.decode(t, &afterSeal)

	const idemKey = "fault-localcommit-crash-1"
	requested := api.post(t, project+"/release-sets/"+releaseSet.ReleaseSetID+"/local-commits", map[string]any{
		"expectedReleaseSetVersion": afterSeal.Version,
		"repositoryWorkspaceId":     ws.ID,
		"expectedWorkspaceVersion":  ws.Version,
		"message":                   "v6-14a fault: crash after git commit",
		"authorName":                "Fault Journey",
		"authorEmail":               "fault@example.invalid",
	}, withIdempotencyKey(idemKey)).requireStatus(t, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	var commit struct {
		LocalCommitID string `json:"releaseSetLocalCommitId"`
	}
	requested.decode(t, &commit)
	if commit.LocalCommitID == "" {
		t.Fatalf("local commit request returned no id: %s", requested.body)
	}

	// The real, externally-observable proof CreateLocalCommit's real `git
	// commit` has already run: HEAD, read directly from the real managed
	// workspace, moved away from the base revision captured before the
	// request. A tight, non-sleeping poll to keep the race against the
	// finalize transaction as close as this black-box test can make it.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if head := runGit(t, worktreeDir, "rev-parse", "HEAD"); head != baseHead {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("workspace HEAD never moved away from the base revision — the real git commit never happened")
		}
	}

	// The real crash: hard-kill the WORKER (never serve — this is entirely
	// a worker-driven job) the instant the real commit is observed.
	j.s.hardKillWorker(t)
	j.s.startWorker(t)

	type commitStatus struct {
		State             string `json:"state"`
		ParentVCSObjectID string `json:"parentVcsObjectId"`
		ResultVCSObjectID string `json:"resultVcsObjectId"`
	}
	var final commitStatus
	waitFor(t, "the restarted worker to finish the crashed local commit", 30*time.Second, 300*time.Millisecond, func() bool {
		api.get(t, project+"/release-sets/"+releaseSet.ReleaseSetID+"/local-commits/"+commit.LocalCommitID).requireStatus(t, http.StatusOK).decode(t, &final)
		return final.State == "COMMITTED" || final.State == "FAILED" || final.State == "QUARANTINED"
	})
	if final.State != "COMMITTED" {
		t.Fatalf("crashed local commit converged to %s, want COMMITTED", final.State)
	}
	if final.ParentVCSObjectID != baseHead {
		t.Fatalf("recorded parent %s != the real base revision %s", final.ParentVCSObjectID, baseHead)
	}

	// Exact-count proof of "no duplicate commit": the real repository log,
	// read directly, has EXACTLY ONE commit reachable from HEAD beyond the
	// base revision, and it is the exact commit the recovered local-commit
	// itself recorded as its result.
	finalHead := runGit(t, worktreeDir, "rev-parse", "HEAD")
	if finalHead != final.ResultVCSObjectID {
		t.Fatalf("real workspace HEAD %s != the local commit's own recorded result %s", finalHead, final.ResultVCSObjectID)
	}
	newCommitCount := runGit(t, worktreeDir, "rev-list", "--count", finalHead, "^"+baseHead)
	if newCommitCount != "1" {
		t.Fatalf("commits between base %s and result %s = %s, want exactly 1 (a second value here would mean the crash recovery created a DUPLICATE real git commit instead of reusing the marker-matched one)",
			baseHead, finalHead, newCommitCount)
	}
}
