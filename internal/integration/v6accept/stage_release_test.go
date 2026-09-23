package v6accept

import (
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// releaseScript is the COMMAND that makes the one change this journey later
// commits: it runs with the repository working tree as its cwd and writes a
// file inside src/, the only path the WorkItem's grant covers. It is the last
// mutating step of its Run — nothing read-only runs after it, so the
// "empty diff" rule for GATE/CHECKER attempts is not in play.
func releaseScript() (key, script string) {
	if runtime.GOOS == "windows" {
		return "release.bat", "@echo off\r\nif not exist src mkdir src\r\necho v6 release note> src\\release-note.txt\r\nexit /b 0\r\n"
	}
	return "release.sh", "#!/bin/sh\nmkdir -p src\necho 'v6 release note' > src/release-note.txt\nexit 0\n"
}

// releaseWorkflow is START → COMMAND (writes the release note) → END.
type releaseWorkflow struct {
	workflow publishedDefinition
}

func (j *journey) publishReleaseWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	key, script := releaseScript()
	provenance := skill.Provenance{Owner: "v6accept", Source: "fixture", Revision: "v1"}
	skillDocument := skill.SkillDocument{Resources: []skill.Resource{
		{Key: key, Instruction: script, Priority: definition.PriorityGuidance, Global: true, Provenance: provenance},
	}}
	scripts := j.publish(t, "", definition.KindSkill, "release-scripts", "release scripts", skillDocument)
	identities, err := skill.ResourceIdentities(skill.SkillVersionID(scripts.versionID), skillDocument)
	if err != nil || len(identities) != 1 {
		t.Fatalf("skill.ResourceIdentities: %v (%d identities)", err, len(identities))
	}
	releaseCommand := j.publish(t, "", definition.KindCommand, "release-command", "release command", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: scripts.versionID, ResourceKey: key, ContentHash: identities[0].Identity.ContentHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: j.repositoryID,
		Compatibility:       command.Compatibility{OS: []string{runtime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})
	completion := j.publishCompletionPolicy(t, "release-completion-policy")

	attemptRefs := []definition.DependencyPin{j.attemptPolicy.pin(definition.KindPolicy), j.permissionPolicy.pin(definition.KindPolicy)}
	completionRef := completion.pin(definition.KindPolicy)
	graph := workflow.WorkflowDocument{
		SchemaVersion:       "1",
		CompletionPolicyRef: &completionRef,
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "release_prep", Type: workflow.NodeCommand, Outcomes: []string{"passed"}, Command: &workflow.CommandNodeConfig{
				CommandRef: releaseCommand.pin(definition.KindCommand), PolicyRefs: attemptRefs,
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-release", From: "start", Outcome: "next", To: "release_prep"},
			{Key: "release-end", From: "release_prep", Outcome: "passed", To: "end"},
		},
	}
	wf := j.publish(t, "/projects/"+j.projectID, definition.KindWorkflow, "release-workflow", "release workflow", graph)
	return releaseWorkflow{workflow: wf}
}

// releaseRun runs the release workflow on its own child WorkItem — after the
// verification Run has finished, because that Run's read-only GATE/CHECKER
// nodes need a repository diff that is still empty.
func (j *journey) releaseRun(t *testing.T) {
	j.release = j.publishReleaseWorkflow(t)
	j.releaseChildID = j.createChild(t, "release-child", "the release note is written into src/", j.release.workflow)
	j.markReady(t, j.releaseChildID)
	j.releaseRunID = j.startRun(t, j.releaseChildID, j.release.workflow.versionID)
	if state := j.waitRunSettled(t, j.releaseRunID); state != "SUCCEEDED" {
		t.Fatalf("release run settled in %s, want SUCCEEDED", state)
	}
	j.extraSnapshotPaths = append(j.extraSnapshotPaths,
		"/runs/"+j.releaseRunID, "/runs/"+j.releaseRunID+"/graph", "/runs/"+j.releaseRunID+"/timeline",
		"/projects/"+j.projectID+"/work-items/"+j.releaseChildID)
}

// repositoryWorkspace is the one repository workspace of the family, as the
// authoritative workspace-set query reports it.
type repositoryWorkspace struct {
	ID              string `json:"repositoryWorkspaceId"`
	RepositoryID    string `json:"repositoryId"`
	Generation      uint64 `json:"generation"`
	State           string `json:"state"`
	Version         uint64 `json:"version"`
	CurrentRevision string `json:"currentRevision"`
}

func (j *journey) workspace(t *testing.T) repositoryWorkspace {
	t.Helper()
	var set struct {
		Workspaces []repositoryWorkspace `json:"repositoryWorkspaces"`
	}
	j.s.api.get(t, "/projects/"+j.projectID+"/workspace-sets/"+j.familyID).requireStatus(t, http.StatusOK).decode(t, &set)
	if len(set.Workspaces) != 1 {
		t.Fatalf("workspace set has %d repository workspaces, want 1", len(set.Workspaces))
	}
	return set.Workspaces[0]
}

// releaseChain creates, seals and locally commits a ReleaseSet — the last
// step of the core journey. The commit is made by the WORKER (real `git`
// against the managed workspace); this test only requests it and observes.
func (j *journey) releaseChain(t *testing.T) {
	api := j.s.api
	project := "/projects/" + j.projectID
	ws := j.workspace(t)

	created := api.post(t, project+"/task-families/"+j.familyID+"/release-sets", map[string]any{
		"repositories": []map[string]any{{
			"repositoryId": j.repositoryID, "baseVcsObjectId": ws.CurrentRevision, "resultVcsObjectId": ws.CurrentRevision, "verdict": "PASS",
		}},
	}).requireStatus(t, http.StatusCreated)
	t.Logf("release set created: %s", tail(string(created.body), 600))
	var releaseSet struct {
		ReleaseSetID string `json:"releaseSetId"`
		Version      uint64 `json:"version"`
		State        string `json:"state"`
	}
	created.decode(t, &releaseSet)
	if releaseSet.ReleaseSetID == "" {
		t.Fatalf("create release set returned no id: %s", created.body)
	}
	j.releaseSetID = releaseSet.ReleaseSetID

	detail := api.get(t, project+"/release-sets/"+j.releaseSetID).requireStatus(t, http.StatusOK)
	sealed := api.post(t, project+"/release-sets/"+j.releaseSetID+"/seal", map[string]any{}, withIfMatch(detail.etag()))
	t.Logf("seal: %d %s", sealed.status, tail(string(sealed.body), 500))
	sealed.requireStatus(t, http.StatusOK)
	var afterSeal struct {
		Version uint64 `json:"version"`
		State   string `json:"state"`
	}
	sealed.decode(t, &afterSeal)

	requested := api.post(t, project+"/release-sets/"+j.releaseSetID+"/local-commits", map[string]any{
		"expectedReleaseSetVersion": afterSeal.Version,
		"repositoryWorkspaceId":     ws.ID,
		"expectedWorkspaceVersion":  ws.Version,
		"message":                   "v6 acceptance: add the release note",
		"authorName":                "Acceptance Journey",
		"authorEmail":               "journey@example.invalid",
	}, withIdempotencyKey("acc-local-commit-1"))
	t.Logf("local commit requested: %d %s", requested.status, tail(string(requested.body), 700))
	requested.requireStatus(t, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	var commit struct {
		LocalCommitID string `json:"releaseSetLocalCommitId"`
		State         string `json:"state"`
	}
	requested.decode(t, &commit)
	if commit.LocalCommitID == "" {
		t.Fatalf("local commit request returned no id: %s", requested.body)
	}

	j.localCommitID = commit.LocalCommitID

	type commitStatus struct {
		State             string `json:"state"`
		ParentVCSObjectID string `json:"parentVcsObjectId"`
		ResultVCSObjectID string `json:"resultVcsObjectId"`
	}
	var final commitStatus
	waitFor(t, "the worker to finish the local commit", 90*time.Second, 300*time.Millisecond, func() bool {
		api.get(t, project+"/release-sets/"+j.releaseSetID+"/local-commits/"+commit.LocalCommitID).requireStatus(t, http.StatusOK).decode(t, &final)
		return final.State == "COMMITTED" || final.State == "FAILED" || final.State == "QUARANTINED"
	})
	if final.State != "COMMITTED" {
		t.Fatalf("local commit ended in %s, want COMMITTED", final.State)
	}
	if final.ResultVCSObjectID == "" || final.ResultVCSObjectID == final.ParentVCSObjectID {
		t.Fatalf("local commit result %q must be a new object distinct from its parent %q", final.ResultVCSObjectID, final.ParentVCSObjectID)
	}
	j.baseRevision, j.resultRevision = final.ParentVCSObjectID, final.ResultVCSObjectID
	j.extraSnapshotPaths = append(j.extraSnapshotPaths,
		project+"/release-sets/"+j.releaseSetID,
		project+"/release-sets/"+j.releaseSetID+"/local-commits/"+commit.LocalCommitID,
		project+"/task-families/"+j.familyID+"/release-sets")

	// Replay: the same Idempotency-Key and body must return the stored
	// result, not request a second commit.
	replay := api.post(t, project+"/release-sets/"+j.releaseSetID+"/local-commits", map[string]any{
		"expectedReleaseSetVersion": afterSeal.Version,
		"repositoryWorkspaceId":     ws.ID,
		"expectedWorkspaceVersion":  ws.Version,
		"message":                   "v6 acceptance: add the release note",
		"authorName":                "Acceptance Journey",
		"authorEmail":               "journey@example.invalid",
	}, withIdempotencyKey("acc-local-commit-1")).requireStatus(t, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	var replayed struct {
		LocalCommitID string `json:"releaseSetLocalCommitId"`
	}
	replay.decode(t, &replayed)
	if replayed.LocalCommitID != commit.LocalCommitID {
		t.Fatalf("replay returned local commit %q, want the original %q", replayed.LocalCommitID, commit.LocalCommitID)
	}
	// A NEW key with the now-stale versions is refused: the commit already
	// advanced the workspace, so nothing can commit the same change twice.
	if again := api.post(t, project+"/release-sets/"+j.releaseSetID+"/local-commits", map[string]any{
		"expectedReleaseSetVersion": afterSeal.Version,
		"repositoryWorkspaceId":     ws.ID,
		"expectedWorkspaceVersion":  ws.Version,
		"message":                   "v6 acceptance: add the release note",
		"authorName":                "Acceptance Journey",
		"authorEmail":               "journey@example.invalid",
	}, withIdempotencyKey("acc-local-commit-2")); again.status < 400 {
		t.Fatalf("a second local commit request with stale versions succeeded (%d): %s", again.status, again.body)
	}

	j.inspectCommittedWorkspace(t, ws)
	j.assertOriginUntouched(t)
}

// inspectCommittedWorkspace reads the committed change back through the
// bounded workspace-inspection routes: the diff between the commit's parent
// and result names the release note, the source at the result revision has
// its content, and the repository log ends at the new commit.
func (j *journey) inspectCommittedWorkspace(t *testing.T, ws repositoryWorkspace) {
	t.Helper()
	api := j.s.api
	base := "/projects/" + j.projectID + "/repository-workspaces/" + ws.ID
	scope := "repositoryId=" + j.repositoryID + "&workspaceSetId=" + j.workspaceSetID
	gen := itoa(ws.Generation)

	var diff struct {
		Files []struct {
			Path      string `json:"path"`
			Additions int64  `json:"additions"`
		} `json:"files"`
	}
	api.get(t, base+"/diff?"+scope+"&base="+j.baseRevision+"&baseGeneration="+gen+"&result="+j.resultRevision+"&resultGeneration="+gen).
		requireStatus(t, http.StatusOK).decode(t, &diff)
	if len(diff.Files) != 1 || diff.Files[0].Path != "src/release-note.txt" || diff.Files[0].Additions != 1 {
		t.Fatalf("diff between the commit's parent and result = %+v, want exactly src/release-note.txt (+1)", diff.Files)
	}

	source := api.get(t, base+"/source?"+scope+"&path=src/release-note.txt&revision="+j.resultRevision+"&generation="+gen).requireStatus(t, http.StatusOK)
	if !strings.Contains(string(source.body), "v6 release note") {
		t.Fatalf("source of src/release-note.txt at the result revision does not contain the note: %s", tail(string(source.body), 400))
	}

	var log struct {
		Entries []struct {
			CommitID   string   `json:"commitId"`
			ParentIDs  []string `json:"parentIds"`
			AuthorName string   `json:"authorName"`
			Subject    string   `json:"subject"`
		} `json:"entries"`
	}
	api.get(t, base+"/repository-log?"+scope+"&anchor="+j.resultRevision+"&anchorGeneration="+gen).requireStatus(t, http.StatusOK).decode(t, &log)
	if len(log.Entries) < 2 || log.Entries[0].CommitID != j.resultRevision || log.Entries[0].AuthorName != "Acceptance Journey" {
		t.Fatalf("repository log = %+v, want the new commit by the requested author first, on top of the original history", log.Entries)
	}
	if len(log.Entries[0].ParentIDs) != 1 || log.Entries[0].ParentIDs[0] != j.baseRevision {
		t.Fatalf("new commit parents = %v, want exactly [%s]", log.Entries[0].ParentIDs, j.baseRevision)
	}
}

// assertOriginUntouched proves the local-commit path never published anything
// to, or disturbed, the operator's own checkout. The managed workspace is a
// `git worktree` of that repository, so the new commit legitimately lives in
// its object store on the workspace's own branch — but the operator's default
// branch and working tree must be exactly as they were, the repository has no
// remote to push to, and the commit is reachable only from the managed branch.
func (j *journey) assertOriginUntouched(t *testing.T) {
	t.Helper()
	if head := runGit(t, j.repoPath, "rev-parse", "HEAD"); head != j.originHead {
		t.Fatalf("the operator's checked-out HEAD moved from %s to %s", j.originHead, head)
	}
	if main := runGit(t, j.repoPath, "rev-parse", "main"); main != j.originHead {
		t.Fatalf("the operator's main branch moved from %s to %s", j.originHead, main)
	}
	if status := runGit(t, j.repoPath, "status", "--porcelain"); status != "" {
		t.Fatalf("the operator's working tree was modified:\n%s", status)
	}
	if remotes := runGit(t, j.repoPath, "remote"); remotes != "" {
		t.Fatalf("the operator's repository has remotes %q: a push target must not exist", remotes)
	}
	branches := runGit(t, j.repoPath, "branch", "--contains", j.resultRevision, "--format=%(refname:short)")
	if branches == "" || strings.Contains(branches, "main") {
		t.Fatalf("the committed revision is reachable from %q, want only the managed workspace branch (never main)", branches)
	}
}
