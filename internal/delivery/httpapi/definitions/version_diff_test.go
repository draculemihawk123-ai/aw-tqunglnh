package definitions_test

import (
	"net/http"
	"testing"
)

const validSkillDocumentJSON = `{
  "resources": [
    {
      "key": "go-error-wrapping",
      "instruction": "wrap errors with fmt.Errorf",
      "priority": "HARD_CONSTRAINT",
      "selector": {"componentTags": ["backend"], "taskKinds": ["code-change"]},
      "provenance": {"owner": "platform-team", "source": "docs/style/go-errors.md"}
    }
  ]
}`

func publishBlock(t *testing.T, e *testEnv, pathPrefix, definitionID, idempotencyKey, content string) string {
	t.Helper()
	e.do(t, http.MethodPost, pathPrefix+"/definitions/BLOCK", "create-"+definitionID, map[string]any{"definitionId": definitionID, "name": "n"}).Body.Close()
	resp := e.do(t, http.MethodPost, pathPrefix+"/definitions/BLOCK/"+definitionID+"/publish", idempotencyKey, map[string]any{"content": content})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish %s status = %d, want 201", definitionID, resp.StatusCode)
	}
	var view struct {
		ID string `json:"id"`
	}
	decodeInto(t, resp, &view)
	return view.ID
}

func TestGetDefinitionVersion_KindAgnostic_Global(t *testing.T) {
	e := newTestEnv(t)
	versionID := publishBlock(t, e, "", "blk-1", "publish-1", validBlockDocumentJSON)

	resp := e.do(t, http.MethodGet, "/definitions/versions/"+versionID, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var view struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	decodeInto(t, resp, &view)
	if view.ID != versionID || view.Kind != "BLOCK" {
		t.Fatalf("got %+v", view)
	}
}

func TestGetDefinitionVersion_WrongScope_404(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")
	versionID := publishBlock(t, e, "/projects/proj-1", "blk-1", "publish-1", validBlockDocumentJSON)

	resp := e.do(t, http.MethodGet, "/definitions/versions/"+versionID, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (project-scoped version must not be visible via the global route)", resp.StatusCode)
	}
}

func TestGetDefinitionVersion_Unknown_404(t *testing.T) {
	e := newTestEnv(t)
	resp := e.do(t, http.MethodGet, "/definitions/versions/does-not-exist", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDiffDefinitionVersions_Identical(t *testing.T) {
	e := newTestEnv(t)
	a := publishBlock(t, e, "", "blk-a", "publish-a", validBlockDocumentJSON)
	b := publishBlock(t, e, "", "blk-b", "publish-b", validBlockDocumentJSON)

	resp := e.do(t, http.MethodGet, "/definitions/versions/diff?a="+a+"&b="+b, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var view struct {
		Identical  bool `json:"identical"`
		SourceDiff []struct {
			Op string `json:"op"`
		} `json:"sourceDiff"`
	}
	decodeInto(t, resp, &view)
	if !view.Identical {
		t.Fatalf("expected identical=true for byte-identical content, got %+v", view)
	}
	for _, line := range view.SourceDiff {
		if line.Op != "equal" {
			t.Fatalf("expected every diff line to be 'equal' for identical content, got %+v", view.SourceDiff)
		}
	}
}

func TestDiffDefinitionVersions_Different(t *testing.T) {
	e := newTestEnv(t)
	altBlock := `{
  "compatibleNodeTypes": ["COMMAND"],
  "requiredCapabilities": [],
  "timeoutSeconds": 30,
  "scopeSelector": {"access": "READ", "pathScopes": ["docs/**"]},
  "doneCondition": "result.ok",
  "executorRef": {"kind": "COMMAND", "definitionId": "cmd-2", "versionId": "cmd-2-v1"},
  "outcomes": ["done"]
}`
	a := publishBlock(t, e, "", "blk-a", "publish-a", validBlockDocumentJSON)
	b := publishBlock(t, e, "", "blk-b", "publish-b", altBlock)

	resp := e.do(t, http.MethodGet, "/definitions/versions/diff?a="+a+"&b="+b, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var view struct {
		Identical  bool `json:"identical"`
		SourceDiff []struct {
			Op string `json:"op"`
		} `json:"sourceDiff"`
	}
	decodeInto(t, resp, &view)
	if view.Identical {
		t.Fatal("expected identical=false for genuinely different content")
	}
	hasChange := false
	for _, line := range view.SourceDiff {
		if line.Op != "equal" {
			hasChange = true
		}
	}
	if !hasChange {
		t.Fatal("expected at least one add/remove diff line for different content")
	}
}

// TestDiffDefinitionVersions_CrossScope_404 proves "diff operands phải
// cùng scope": one operand global, one project-scoped, via the global
// diff route — rejected leakage-normalized 404, never a mixed-scope diff.
func TestDiffDefinitionVersions_CrossScope_404(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")
	a := publishBlock(t, e, "", "blk-a", "publish-a", validBlockDocumentJSON)
	b := publishBlock(t, e, "/projects/proj-1", "blk-b", "publish-b", validBlockDocumentJSON)

	resp := e.do(t, http.MethodGet, "/definitions/versions/diff?a="+a+"&b="+b, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (cross-scope diff must be rejected)", resp.StatusCode)
	}
}

// TestDiffDefinitionVersions_CrossKind_Rejected proves diff refuses to
// compare two different Kinds' versions.
func TestDiffDefinitionVersions_CrossKind_Rejected(t *testing.T) {
	e := newTestEnv(t)
	a := publishBlock(t, e, "", "blk-a", "publish-a", validBlockDocumentJSON)

	e.do(t, http.MethodPost, "/definitions/SKILL", "create-skl", map[string]any{"definitionId": "skl-1", "name": "n"}).Body.Close()
	skillResp := e.do(t, http.MethodPost, "/definitions/SKILL/skl-1/publish", "publish-skl", map[string]any{"content": validSkillDocumentJSON})
	var skillView struct {
		ID string `json:"id"`
	}
	decodeInto(t, skillResp, &skillView)

	resp := e.do(t, http.MethodGet, "/definitions/versions/diff?a="+a+"&b="+skillView.ID, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (cross-kind diff must be rejected)", resp.StatusCode)
	}
}

func TestDiffDefinitionVersions_MissingParam_Rejected(t *testing.T) {
	e := newTestEnv(t)
	a := publishBlock(t, e, "", "blk-a", "publish-a", validBlockDocumentJSON)

	resp := e.do(t, http.MethodGet, "/definitions/versions/diff?a="+a, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestDiffDefinitionVersions_Project_HappyPath(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")
	a := publishBlock(t, e, "/projects/proj-1", "blk-a", "publish-a", validBlockDocumentJSON)
	b := publishBlock(t, e, "/projects/proj-1", "blk-b", "publish-b", validBlockDocumentJSON)

	resp := e.do(t, http.MethodGet, "/projects/proj-1/definitions/versions/diff?a="+a+"&b="+b, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
