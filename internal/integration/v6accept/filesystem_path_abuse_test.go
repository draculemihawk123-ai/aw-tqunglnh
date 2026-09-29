// V8-04A — Filesystem and path abuse suite
// (docs/design/10-v8-alpha-hardening.md V8-04A, AK-ARCH-027): a real-topology
// negative suite proving the product's existing path-containment code
// (internal/adapters/gitworktree.Provider's validateLocalRepository/
// validateManagedDirectory/ensureLexicallyWithin, already unit-tested at the
// package level in inspection_test.go/prober_test.go/provider_test.go) still
// rejects an abusive repository path — nested under the real
// --workspace-root, or disguised behind a symlink that canonically resolves
// there — when driven through the real public HTTP surface end to end,
// against real `aw serve`/`aw worker` processes, with a safe (non-path-
// leaking) diagnostic surfaced as real, queryable evidence
// (workspace.RepositoryWorkspace.LastProvisionErrorCode).
//
// Two of V8-04A's five design scenarios are deliberately NOT duplicated
// here:
//   - "artifact/workspace root containment" (the artifact-store half) is
//     already airtight and already unit-tested twice over
//     (internal/adapters/artifactstore: TestPut/Open/Delete_MalformedLocator_
//     RejectedBeforeTouchingFilesystem) — a Locator is validated against
//     `^sha256:[0-9a-f]{64}$` before any path is ever built from it, which
//     structurally cannot admit "..", "/", "\" or a NUL byte. There is no
//     real-topology surface that could exercise this differently: an
//     artifact's Locator is never caller-supplied free text in the real
//     product, it is always the digest the store itself computed on Put.
//   - plain traversal ("..") is the same lexical case ensureLexicallyWithin
//     already covers and inspection_test.go already proves at the unit
//     level; TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected below
//     is the strictly stronger real-topology case (a symlink whose raw,
//     lexical path looks completely unrelated to the workspace root, but
//     canonically resolves inside it) — proving canonicalization, not a
//     lexical prefix check, is what the real system enforces end to end.
package v6accept

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// pathAbuseFixture is the minimal real state V8-04A's own assertions
// need: a project and one repository, registered through the real HTTP
// surface. Unlike journey/golden_workload's own fixtures, this suite never
// needs a workflow, run, or command/agent definition at all — Provision
// (internal/adapters/gitworktree.Provider.Provision, the exact call
// validateLocalRepository guards) runs as soon as a child WorkItem's own
// effectiveScope names the repository, well before any run is started
// (golden_workload_test.go's own "workspace set to be provisioned READY"
// wait already proves this ordering).
type pathAbuseFixture struct {
	s             *stack
	api           *apiClient
	projectID     string
	projectPrefix string
}

func newPathAbuseFixture(t *testing.T) *pathAbuseFixture {
	t.Helper()
	s := newStack(t)
	s.start(t)
	t.Cleanup(func() {
		s.serve.dumpOnFailure(t)
		s.worker.dumpOnFailure(t)
	})
	api := s.api

	var created struct {
		ProjectID string `json:"projectId"`
	}
	api.post(t, "/projects", map[string]string{"name": "path-abuse"}).requireStatus(t, http.StatusCreated).decode(t, &created)
	if created.ProjectID == "" {
		t.Fatal("project create returned no id")
	}
	return &pathAbuseFixture{s: s, api: api, projectID: created.ProjectID, projectPrefix: "/projects/" + created.ProjectID}
}

// registerRepositoryAt registers repositoryPath as repositoryID and waits
// for the worker to probe it — ACTIVE for a path a real `git` can open, or
// BLOCKED for one it can't. It deliberately does NOT require ACTIVE the way
// registerGoldenRepository does: repoprobe.Prober has no --workspace-root
// awareness at all (identity/dedup only; confirmed by reading
// internal/adapters/repoprobe/prober.go), so a path nested under, or
// symlinked into, the workspace root probes ACTIVE exactly like any other
// real repository — the rejection this suite is actually proving happens
// strictly later, at real workspace Provision time.
func (f *pathAbuseFixture) registerRepositoryAt(t *testing.T, repositoryID, repositoryPath string) {
	t.Helper()
	registered := f.api.post(t, f.projectPrefix+"/repositories", map[string]string{
		"repositoryId": repositoryID, "name": repositoryID, "remoteLocator": repositoryPath, "defaultRef": "main",
	}).requireStatus(t, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	var repository struct {
		RepositoryID string `json:"repositoryId"`
	}
	registered.decode(t, &repository)
	if repository.RepositoryID == "" {
		t.Fatalf("register repository %s returned no id: %s", repositoryID, registered.body)
	}
	waitFor(t, "repository "+repositoryID+" to leave PROBING", 60*time.Second, 200*time.Millisecond, func() bool {
		var view struct {
			Status string `json:"status"`
		}
		f.api.get(t, "/repositories/"+repositoryID).requireStatus(t, http.StatusOK).decode(t, &view)
		return view.Status == "ACTIVE" || view.Status == "BLOCKED"
	})
}

// createFamilyScopedTo creates a root WorkItem plus the one child WorkItem
// that actually carries effectiveScope (journey.workItemAndRun's own
// root+child pattern, generalized) — the child's creation is what triggers
// real WorkspaceSet provisioning against repositoryID. Returns the family id
// GET .../workspace-sets/{familyId} polls.
func (f *pathAbuseFixture) createFamilyScopedTo(t *testing.T, repositoryID string) (familyID string) {
	t.Helper()
	grant := []map[string]any{{"repositoryId": repositoryID, "access": "WRITE", "pathScopes": []string{"src/"}, "reason": "v8-04a path abuse fixture"}}

	root := f.api.post(t, f.projectPrefix+"/work-items", map[string]any{
		"title": "path-abuse-root", "initialScope": grant,
	}).requireStatus(t, http.StatusCreated)
	var rootResult struct {
		WorkItemID string `json:"workItemId"`
		FamilyID   string `json:"familyId"`
	}
	root.decode(t, &rootResult)
	if rootResult.WorkItemID == "" || rootResult.FamilyID == "" {
		t.Fatalf("root work item result is missing ids: %s", root.body)
	}

	child := f.api.post(t, f.projectPrefix+"/work-items/"+rootResult.WorkItemID+"/children", map[string]any{
		"title": "path-abuse-child", "parentJoinPolicy": "v8-path-abuse-child", "effectiveScope": grant,
		"contract": map[string]any{
			"schemaVersion": 1, "behavior": "n/a — this suite never starts a run", "riskLevel": "LOW",
			"acceptanceCriteria": []map[string]any{}, "verificationSpec": "n/a", "exclusions": []string{"no network access"},
		},
	}).requireStatus(t, http.StatusCreated)
	var childResult struct {
		WorkItemID string `json:"workItemId"`
	}
	child.decode(t, &childResult)
	if childResult.WorkItemID == "" {
		t.Fatalf("child work item result has no id: %s", child.body)
	}
	return rootResult.FamilyID
}

// workspaceSetState is the subset of GET
// /projects/{projectId}/workspace-sets/{familyId}'s response body
// (httpapi.workspaceSetStateResponse) this suite reads.
type workspaceSetState struct {
	State                string `json:"state"`
	RepositoryWorkspaces []struct {
		State                  string  `json:"state"`
		LastProvisionErrorCode *string `json:"lastProvisionErrorCode"`
	} `json:"repositoryWorkspaces"`
}

// requireSafeProvisionFailure asserts state is a real, diagnosable rejection
// (V8-04A's own "Hoàn thành khi: mọi từ chối có safe diagnostic và
// evidence"): the set is BLOCKED (never READY), exactly the one abusive
// repository workspace is itself FAILED with the product's real, short,
// typed PROVISION_FAILED code (workspace.RepositoryWorkspace.
// LastProvisionErrorCode — deliberately a coarse code, never a raw error
// string, so the guarantee this asserts is itself "safe": rawRepositoryPath,
// the attacker-controlled absolute filesystem path, must never appear
// anywhere in the response the client actually receives).
func requireSafeProvisionFailure(t *testing.T, raw response, state workspaceSetState, rawRepositoryPath string) {
	t.Helper()
	if state.State != "BLOCKED" {
		t.Fatalf("workspace set state = %q, want BLOCKED (abusive repository path must never provision READY): %s", state.State, raw.body)
	}
	if len(state.RepositoryWorkspaces) != 1 {
		t.Fatalf("repositoryWorkspaces = %+v, want exactly one", state.RepositoryWorkspaces)
	}
	rw := state.RepositoryWorkspaces[0]
	if rw.State != "FAILED" {
		t.Fatalf("repository workspace state = %q, want FAILED", rw.State)
	}
	if rw.LastProvisionErrorCode == nil || *rw.LastProvisionErrorCode != "PROVISION_FAILED" {
		t.Fatalf("lastProvisionErrorCode = %v, want PROVISION_FAILED", rw.LastProvisionErrorCode)
	}
	if strings.Contains(string(raw.body), rawRepositoryPath) {
		t.Fatalf("rejection response leaked the raw attacker-controlled repository path %q — not a safe diagnostic:\n%s", rawRepositoryPath, raw.body)
	}
}

// TestV8PathAbuse_RepositoryNestedUnderWorkspaceRoot_Rejected is V8-04A
// scenario 3 (Nguồn: AK-ARCH-027): registering a repository whose real path
// lives INSIDE the real --workspace-root must never let that repository
// provision — gitworktree.Provider.validateLocalRepository's own bidirectional
// isWithin(root, repo) || isWithin(repo, root) check
// (internal/adapters/gitworktree/provider.go) exists precisely to catch
// this, previously only proven at the unit level; this proves it end to end
// through the real public HTTP surface, including that the real rejection
// never leaks the nested path back to the caller.
func TestV8PathAbuse_RepositoryNestedUnderWorkspaceRoot_Rejected(t *testing.T) {
	requireAcceptance(t)
	f := newPathAbuseFixture(t)

	nestedRepoPath := createGitRepository(t, filepath.Join(f.s.workspaceRoot, "nested-under-workspace-root"))
	f.registerRepositoryAt(t, "repo-nested", nestedRepoPath)

	familyID := f.createFamilyScopedTo(t, "repo-nested")
	var raw response
	var state workspaceSetState
	waitFor(t, "workspace set "+familyID+" to reach READY or BLOCKED", 60*time.Second, 200*time.Millisecond, func() bool {
		raw = f.api.get(t, f.projectPrefix+"/workspace-sets/"+familyID).requireStatus(t, http.StatusOK)
		raw.decode(t, &state)
		return state.State == "READY" || state.State == "BLOCKED"
	})

	requireSafeProvisionFailure(t, raw, state, nestedRepoPath)
}

// TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected is V8-04A
// scenario 2 (symlink/reparse escape) proven the way that actually matters
// for this containment check: a repository path whose raw, LEXICAL string is
// completely unrelated to --workspace-root (so a naive strings.HasPrefix
// check would wave it through), but which is a real symlink resolving
// (canonicalExistingDirectory -> canonicalExistingPath, the same
// cross-platform real-handle resolution GetFinalPathNameByHandle/
// EvalSymlinks repoprobe's own TestProbe_PathIsSymlinkToRepository_
// ResolvesToCanonicalTarget already exercises for identity) to a real
// repository INSIDE the workspace root. Proves validateLocalRepository's own
// containment check runs against the CANONICAL path, not the caller-supplied
// one.
//
// Mirrors this repo's own established convention
// (internal/adapters/repoprobe/prober_test.go's identical symlink test) for
// treating "os.Symlink unavailable outside Developer Mode/admin" as a
// Windows CI environment limitation to skip past, not a product bug.
func TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected(t *testing.T) {
	requireAcceptance(t)
	f := newPathAbuseFixture(t)

	realNestedRepoPath := createGitRepository(t, filepath.Join(f.s.workspaceRoot, "disguised-nested-repo"))
	symlinkPath := filepath.Join(f.s.root, "innocent-looking-repo")
	if err := os.Symlink(realNestedRepoPath, symlinkPath); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("os.Symlink unavailable in this Windows test environment (needs Developer Mode or admin): %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
	f.registerRepositoryAt(t, "repo-symlink-nested", symlinkPath)

	familyID := f.createFamilyScopedTo(t, "repo-symlink-nested")
	var state workspaceSetState
	var raw response
	waitFor(t, "workspace set "+familyID+" to reach READY or BLOCKED", 60*time.Second, 200*time.Millisecond, func() bool {
		raw = f.api.get(t, f.projectPrefix+"/workspace-sets/"+familyID).requireStatus(t, http.StatusOK)
		raw.decode(t, &state)
		return state.State == "READY" || state.State == "BLOCKED"
	})

	requireSafeProvisionFailure(t, raw, state, symlinkPath)
	requireSafeProvisionFailure(t, raw, state, realNestedRepoPath)
}

// Scenario 5 ("cleanup chỉ chạm owned path") is deliberately NOT a v6accept
// HTTP-level test: releasing a WorkspaceSet through the real public API
// requires a real, sealed-or-abandoned ReleaseSet first
// (ports.ReleaseEligibilityAuthority, GC-INV-26) — confirmed by this
// suite's own first attempt at an HTTP-level release test, which the real
// server correctly rejected with 403 FORBIDDEN ("release is not authorized
// until this family's release set is sealed or abandoned"). Reproducing
// journey_test.go's own full release-set-seal chain here just to reach a
// releasable WorkspaceSet would dwarf this suite's own scope for no added
// containment coverage. See
// TestProviderRelease_BystanderSiblingUnderManagedRootSurvives
// (internal/adapters/gitworktree/provider_test.go) instead: it drives the
// exact same production Provision/Release code this suite's other two tests
// already exercise through the real HTTP surface, directly, against a real
// git repository and a real bystander directory.
