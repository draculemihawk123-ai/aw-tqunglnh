package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// V9-09 / B2: through the real one-shot `aw` entry point against a real
// SQLite database, `definition create` with an existing definitionId and
// `repository register` with an existing repositoryId (LIM-06) fail with a
// readable "already exists" message naming the id and exit code 1 — not the
// opaque "sqlite: unexpected error" — while the same idempotency key still
// replays the first result (exit 0, replayed=true).
func TestOneShot_DefinitionCreate_ExistingID_IsReadableConflict(t *testing.T) {
	a := newAWInstall(t)
	body := `{"definitionId":"skill-1","name":"S1"}`
	a.mustOK(body, "definition", "create", "--kind", "SKILL", "--idempotency-key", "d1")

	for _, args := range [][]string{
		{"definition", "create", "--kind", "SKILL", "--idempotency-key", "d2"},
		{"definition", "create", "--kind", "BLOCK", "--idempotency-key", "d3"},
	} {
		stdout, stderr, code := a.aw(body, args...)
		if code != exitFailure {
			t.Fatalf("aw %v: exit=%d, want %d; stderr=%q", args, code, exitFailure, stderr)
		}
		if stdout != "" {
			t.Errorf("aw %v: stdout = %q, want empty on a failed create", args, stdout)
		}
		if !strings.Contains(stderr, "already exists") || !strings.Contains(stderr, "skill-1") || strings.Contains(stderr, "unexpected error") {
			t.Errorf("aw %v: stderr = %q, want an 'already exists' message naming skill-1", args, stderr)
		}
	}

	// A WORKFLOW definition twice: the duplicate used to fail one step later.
	wf := `{"definitionId":"wf-1","name":"W1"}`
	a.mustOK(wf, "definition", "create", "--kind", "WORKFLOW", "--idempotency-key", "w1")
	if _, stderr, code := a.aw(wf, "definition", "create", "--kind", "WORKFLOW", "--idempotency-key", "w2"); code != exitFailure || !strings.Contains(stderr, "already exists") {
		t.Fatalf("duplicate WORKFLOW create: exit=%d stderr=%q, want exit 1 and an 'already exists' message", code, stderr)
	}

	// The idempotent re-create with the first key is untouched.
	replayed := decodeResult(t, a.mustOK(body, "definition", "create", "--kind", "SKILL", "--idempotency-key", "d1"))
	if !replayed.Replayed {
		t.Errorf("re-create with the same idempotency key: replayed = false, want true")
	}
}

func TestOneShot_RepositoryRegister_ExistingID_IsReadableConflict(t *testing.T) {
	a := newAWInstall(t)
	var project struct {
		Result struct {
			ProjectID string `json:"projectId"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(a.mustOK(`{"name":"demo"}`, "project", "create", "--idempotency-key", "p1")), &project); err != nil || project.Result.ProjectID == "" {
		t.Fatalf("project create: %v %+v", err, project)
	}
	projectID := project.Result.ProjectID
	body := `{"repositoryId":"repo-1","name":"svc","remoteLocator":"https://example.invalid/repo.git","defaultRef":"main"}`
	a.mustOK(body, "repository", "register", "--project-id", projectID, "--idempotency-key", "r1")

	_, stderr, code := a.aw(body, "repository", "register", "--project-id", projectID, "--idempotency-key", "r2")
	if code != exitFailure {
		t.Fatalf("duplicate register: exit=%d stderr=%q, want %d", code, stderr, exitFailure)
	}
	if !strings.Contains(stderr, "already exists") || !strings.Contains(stderr, "repo-1") || strings.Contains(stderr, "unexpected error") {
		t.Errorf("duplicate register stderr = %q, want an 'already exists' message naming repo-1", stderr)
	}

	replayed := decodeResult(t, a.mustOK(body, "repository", "register", "--project-id", projectID, "--idempotency-key", "r1"))
	if !replayed.Replayed {
		t.Errorf("re-register with the same idempotency key: replayed = false, want true")
	}
}
