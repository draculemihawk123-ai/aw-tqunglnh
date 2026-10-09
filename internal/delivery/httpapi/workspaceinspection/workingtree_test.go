package workspaceinspection_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V11-06: GET .../diff with result=WORKING_TREE returns the worktree's uncommitted
// state on top of base (an approval gate has no commit to diff).

type workingTreeBody struct {
	Uncommitted     bool   `json:"uncommitted"`
	TotalFiles      int    `json:"totalFiles"`
	ChangeSetDigest string `json:"changeSetDigest"`
	Files           []struct {
		Path        string `json:"path"`
		Status      string `json:"status"`
		Untracked   bool   `json:"untracked"`
		Additions   int64  `json:"additions"`
		ContentHash string `json:"contentHash"`
	} `json:"files"`
	ResultRevision struct {
		VCSObjectID string `json:"vcsObjectId"`
	} `json:"resultRevision"`
	Patch []byte `json:"patch"`
}

func workingTreeQuery(env *testEnv) map[string]string {
	query := scopeQuery()
	query["base"] = env.currentRevision.VCSObjectID
	query["baseGeneration"] = strconv.FormatUint(env.currentRevision.WorkspaceGeneration, 10)
	query["result"] = "WORKING_TREE"
	return query
}

func (e *testEnv) workingDirectory(t *testing.T) string {
	t.Helper()
	dir, err := e.provider.WorkingDirectory(context.Background(), e.handle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	return dir
}

func TestGetWorkspaceDiff_WorkingTree_ShowsChangesThatAreNotCommitted(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)
	dir := env.workingDirectory(t)
	if err := os.WriteFile(filepath.Join(dir, "service.txt"), []byte("line-1\nline-2\n"), 0o644); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), []byte("{\"openapi\":\"3.0\"}\n"), 0o644); err != nil {
		t.Fatalf("add untracked file: %v", err)
	}

	resp := env.do(t, env.diffURL(workingTreeQuery(env)))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body workingTreeBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Uncommitted || body.TotalFiles != 2 || len(body.Files) != 2 || body.ChangeSetDigest == "" {
		t.Fatalf("body = %+v, want an uncommitted diff of 2 files with a digest", body)
	}
	if body.ResultRevision.VCSObjectID != "" {
		t.Fatalf("resultRevision = %+v, want none: there is no result commit", body.ResultRevision)
	}
	byPath := map[string]int{}
	for i, f := range body.Files {
		byPath[f.Path] = i
	}
	spec := body.Files[byPath["spec.json"]]
	if !spec.Untracked || spec.Status != "ADDED" || spec.Additions != 1 || spec.ContentHash == "" {
		t.Fatalf("spec.json = %+v, want an untracked added file with a hash", spec)
	}
	if service := body.Files[byPath["service.txt"]]; service.Status != "MODIFIED" || service.Untracked {
		t.Fatalf("service.txt = %+v, want a tracked modification", service)
	}
	if patch := string(body.Patch); !strings.Contains(patch, "+line-2") || !strings.Contains(patch, "+{\"openapi\":\"3.0\"}") {
		t.Fatalf("patch lacks the changes:\n%s", patch)
	}
}

// Nothing to show is a valid answer, and a committed diff's response is unchanged
// (none of the working-tree fields appear).
func TestGetWorkspaceDiff_WorkingTree_CleanWorktreeIsEmptyAndCommittedDiffIsUnchanged(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)
	resp := env.do(t, env.diffURL(workingTreeQuery(env)))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body workingTreeBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Uncommitted || body.TotalFiles != 0 || len(body.Files) != 0 {
		t.Fatalf("body = %+v, want an empty uncommitted diff", body)
	}

	committed := scopeQuery()
	committed["base"] = env.baseRevision.VCSObjectID
	committed["baseGeneration"] = strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10)
	committed["result"] = env.currentRevision.VCSObjectID
	committed["resultGeneration"] = strconv.FormatUint(env.currentRevision.WorkspaceGeneration, 10)
	resp2 := env.do(t, env.diffURL(committed))
	defer resp2.Body.Close()
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp2.Body).Decode(&raw); err != nil {
		t.Fatalf("decode committed diff: %v", err)
	}
	for _, field := range []string{"uncommitted", "totalFiles", "changeSetDigest"} {
		if _, present := raw[field]; present {
			t.Errorf("a committed diff carries %q", field)
		}
	}
}

func TestGetWorkspaceDiff_WorkingTree_RefusedWhileAnAttemptIsWriting(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)
	// The ownership chain has the task family but not its root work item row, which
	// the live-lease fixture's workflow run references.
	if err := env.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		root, err := work.NewRootWorkItem(work.WorkItemID("wi-root"), project.ProjectID(testProjectID), work.TaskFamilyID(testFamilyID), "root")
		if err != nil {
			return err
		}
		_, err = tx.Work().CreateWorkItem(context.Background(), root)
		return err
	}); err != nil {
		t.Fatalf("create the root work item: %v", err)
	}
	if err := sqlite.SeedFixtureWriteLease(context.Background(), env.store, testProjectID, testFamilyID, "wi-root", testRepositoryID, testRepositoryWorkspaceID, 1); err != nil {
		t.Fatalf("seed a live write lease: %v", err)
	}
	resp := env.do(t, env.diffURL(workingTreeQuery(env)))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while an attempt holds the write lease", resp.StatusCode)
	}
}

func TestGetWorkspaceDiff_WorkingTree_StillRequiresTheScopeAndAnAuthorizedBase(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, workspace.RepositoryWorkspaceReady)
	query := workingTreeQuery(env)
	query["base"] = "0000000000000000000000000000000000000000"
	resp := env.do(t, env.diffURL(query))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unauthorized base: status = %d, want 400", resp.StatusCode)
	}

	query = workingTreeQuery(env)
	delete(query, "repositoryId")
	resp2 := env.do(t, env.diffURL(query))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing scope: status = %d, want 400", resp2.StatusCode)
	}
}
