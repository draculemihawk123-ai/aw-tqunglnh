// V6-14A — HTTP fault, replay, restart and concurrency acceptance
// (docs/design/08-v6-api-projections.md V6-14A). This file (and the
// fault_*_test.go / stage_fault_*_test.go files beside it) is the
// crash/race/security matrix OVER V6-14's own already-accepted happy path
// (journey_test.go, TestV6HTTPAcceptance_CleanDatabaseJourney, left
// unmodified). Every scenario reuses that journey's own real
// process/client/fixture machinery — newStack, createGitRepository,
// journey.projectAndRepository, journey.publishVerificationWorkflow,
// journey.createChild/markReady/startRun/waitRunSettled — rather than
// re-deriving it, and drives its own crash point through the SAME "real
// process, real SQLite, real Git, opt-in AW_HTTP_ACCEPTANCE=1" discipline.
//
// Each scenario is its own top-level Test function with its own fresh
// stack (rather than one shared stack reused via t.Run): a scenario here
// exists specifically to crash/hard-kill/restart one or both real
// processes, and a shared stack would leave one scenario's crash
// leftovers (a killed process, a changed --principal-config, a poisoned
// projection) for the NEXT scenario to silently inherit. The one-time cost
// this repeats (spawn two real processes, register the adapter build,
// create a project+repository) is a few hundred milliseconds to low
// seconds per scenario — see this file's own newFaultStack/newFaultRunner
// helpers, which do only what each family of scenario actually needs
// rather than the full 14-stage setup.
package v6accept

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// newFaultStack brings up a fresh installation (two real processes) with a
// real Git repository registered and probed ACTIVE — the minimum shared
// prerequisite every scenario below needs, built through the identical
// public HTTP flow journey_test.go's own happy path uses
// (createGitRepository, journey.projectAndRepository), never a shortcut or
// direct DB seed. configure, if given, is applied to the stack BEFORE it
// is started — for a scenario that needs a non-default startup flag (e.g.
// a shorter worker lease TTL, stack.workerLeaseTTL's own doc comment).
func newFaultStack(t *testing.T, configure ...func(*stack)) *journey {
	t.Helper()
	j := &journey{s: newStack(t)}
	j.repoPath = createGitRepository(t, filepath.Join(j.s.root, "origin-repo"))
	j.originHead = runGit(t, j.repoPath, "rev-parse", "HEAD")
	for _, fn := range configure {
		fn(j.s)
	}
	j.s.start(t)
	t.Cleanup(func() {
		if j.s.serve != nil {
			j.s.serve.dumpOnFailure(t)
		}
		if j.s.worker != nil {
			j.s.worker.dumpOnFailure(t)
		}
	})
	j.projectAndRepository(t)
	return j
}

// newFaultRunner extends newFaultStack with everything a scenario needs to
// drive a REAL Run to completion: the adapter build registered and the
// verification workflow (START -> AGENT(maker) -> COMMAND -> MACHINE_GATE
// -> AGENT(checker) -> END) published, exactly like journey stages 02 and
// 04 of the happy path.
func newFaultRunner(t *testing.T, configure ...func(*stack)) *journey {
	t.Helper()
	j := newFaultStack(t, configure...)
	j.adapterProbeRegister(t)
	j.verification = j.publishVerificationWorkflow(t)
	return j
}

// createRootWorkItem creates a root WorkItem carrying j's own scope grant
// and returns its id — everything a scenario needs when it wants a real
// WorkItem to attach messages/attachments to but has no need for the
// family's own child/workflow/run machinery (journey.workItemAndRun's own
// heavier setup). Also records j.rootWorkItemID/familyID/workspaceSetID,
// exactly like the happy-path journey does, so later helpers that read
// those fields (j.scopeGrant, j.createChild) keep working unchanged.
func (j *journey) createRootWorkItem(t *testing.T, title string) string {
	t.Helper()
	root := j.s.api.post(t, "/projects/"+j.projectID+"/work-items", map[string]any{
		"title": title, "initialScope": j.scopeGrant(),
	}).requireStatus(t, http.StatusCreated)
	var result struct {
		WorkItemID     string `json:"workItemId"`
		FamilyID       string `json:"familyId"`
		WorkspaceSetID string `json:"workspaceSetId"`
	}
	root.decode(t, &result)
	if result.WorkItemID == "" || result.FamilyID == "" || result.WorkspaceSetID == "" {
		t.Fatalf("root work item result is missing ids: %s", root.body)
	}
	j.rootWorkItemID, j.familyID, j.workspaceSetID = result.WorkItemID, result.FamilyID, result.WorkspaceSetID
	return result.WorkItemID
}

// soleWorktreeDir returns the one real Git worktree directory a stack's own
// gitworktree.Provider has provisioned under s.workspaceRoot/worktrees —
// found by directory listing rather than re-deriving the provider's own
// internal, unexported handle-hash (internal/adapters/gitworktree's own
// deriveHandle), which no scenario here needs to duplicate. Every fault
// scenario that uses this provisions exactly one workspace, so exactly one
// entry is expected.
func soleWorktreeDir(t *testing.T, s *stack) string {
	t.Helper()
	root := filepath.Join(s.workspaceRoot, "worktrees")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read worktrees root %s: %v", root, err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) != 1 {
		t.Fatalf("worktrees root %s has %d directories, want exactly 1: %v", root, len(dirs), dirs)
	}
	return filepath.Join(root, dirs[0])
}

// httpOutcome is one HTTP round trip's result — deliberately carrying its
// own error rather than ever calling a *testing.T failure method, so it
// can be built safely from ANY goroutine (Go's own testing.T contract:
// Fatal/FailNow/Fatalf may only be called from the goroutine running the
// test itself). Every concurrency scenario below (6, 7, and the journal
// bursts 4 and 11 use to build up real events fast) fires its requests via
// doRaw/doRawConcurrent instead of apiClient.do for exactly this reason,
// then asserts on the results back on the test's own goroutine.
type httpOutcome struct {
	status int
	header http.Header
	body   []byte
	err    error
}

// doRaw performs one real HTTP round trip against api's own server using
// api's own bearer session token — api.do's identical wire behavior
// (Idempotency-Key auto-generated for a mutation unless withIdempotencyKey
// supplies one, JSON-encoded body), but returning its outcome instead of
// failing the test, so it is safe to call from a background goroutine.
func doRaw(api *apiClient, method, path string, body any, options ...requestOption) httpOutcome {
	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(value)
	case string:
		reader = bytes.NewReader([]byte(value))
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return httpOutcome{err: fmt.Errorf("encode %s %s body: %w", method, path, err)}
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, api.baseURL+path, reader)
	if err != nil {
		return httpOutcome{err: fmt.Errorf("new request %s %s: %w", method, path, err)}
	}
	if reader != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if api.token != "" {
		request.Header.Set(httpapi.SessionTokenHeader, api.token)
	}
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set(httpapi.IdempotencyKeyHeader, fmt.Sprintf("acc-raw-%d-%d", time.Now().UnixNano(), api.keySeq.Add(1)))
	}
	for _, option := range options {
		option(request)
	}
	response, err := privateHTTPClient().Do(request)
	if err != nil {
		return httpOutcome{err: err}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return httpOutcome{err: fmt.Errorf("read %s %s response: %w", method, path, err)}
	}
	return httpOutcome{status: response.StatusCode, header: response.Header, body: payload}
}

// doPooled is doRaw's own read-only sibling for a tight polling loop (one
// or more goroutines repeatedly GETting the same status route): it reuses
// api's own pooled, keep-alive http.Client instead of a fresh
// keep-alives-disabled one, so a poller that fires many requests per
// second never opens a brand-new ephemeral-port connection for each one
// (confirmed, while building this suite, to exhaust Windows' own
// connection backlog when many goroutines each dial fresh). Safe to call
// from any goroutine — like doRaw, it never touches *testing.T.
func doPooled(api *apiClient, method, path string) httpOutcome {
	request, err := http.NewRequest(method, api.baseURL+path, nil)
	if err != nil {
		return httpOutcome{err: err}
	}
	if api.token != "" {
		request.Header.Set(httpapi.SessionTokenHeader, api.token)
	}
	response, err := api.http.Do(request)
	if err != nil {
		return httpOutcome{err: err}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return httpOutcome{err: err}
	}
	return httpOutcome{status: response.StatusCode, header: response.Header, body: payload}
}

// doRawConcurrent fires n concurrent requests built by build(i) and
// returns every outcome in issue order (index i is build(i)'s own
// result). Every request is genuinely concurrent (its own goroutine, its
// own real TCP connection via privateHTTPClient/doRaw's own
// keep-alives-disabled transport — never a shared pooled connection that
// could accidentally serialize two calls that must race for real).
func doRawConcurrent(n int, build func(i int) httpOutcome) []httpOutcome {
	outcomes := make([]httpOutcome, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			outcomes[i] = build(i)
		}(i)
	}
	wg.Wait()
	return outcomes
}

// privateHTTPClient returns an *http.Client with its OWN, non-shared
// Transport (keep-alives disabled) — for a goroutine that races a real
// hard-kill against one specific in-flight request. Every apiClient (and
// any http.Client left at its zero-value Transport) shares Go's own
// process-wide http.DefaultTransport connection pool; if such a racing
// request happens to reuse a POOLED keep-alive connection some EARLIER,
// unrelated apiClient call left idle, a response-read failure on that
// reused connection makes net/http's Transport silently retry the WHOLE
// request on a fresh connection — net/http's own documented "one silent
// retry for a request whose reused connection turned out to already be
// closed" behavior. Empirically (confirmed with net/http/httptrace while
// building this scenario) that automatic retry's own fresh dial can land
// AFTER the hard kill above has already closed the listening socket,
// reporting a misleading "connection refused" for what was actually a
// genuine mid-response crash. A private, keep-alive-disabled Transport
// makes every one of these racing requests exactly one real dial, one
// real write and at most one real response read — the true fate of THAT
// SPECIFIC request, never a transparently-retried stand-in for it.
func privateHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
}
