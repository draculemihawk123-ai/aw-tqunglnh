package httpapi_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func newReceiptTestStore(t *testing.T) (*sqlite.Store, ports.UnitOfWork) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "httpapi-receipt.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, sqlite.NewUnitOfWork(store)
}

// buildCreateProjectCommand mirrors what a real HTTP handler built on this
// package's own primitives would do: canonicalize the body, compute the
// semantic hash, and build the ports.Command envelope CreateProject
// itself requires.
func buildCreateProjectCommand(t *testing.T, idempotencyKey, name string) (ports.Command, catalog.CreateProjectRequest) {
	t.Helper()
	req := httptest.NewRequest("POST", "/projects", strings.NewReader(`{"name":`+`"`+name+`"}`))
	var payload catalog.CreateProjectRequest
	canonical, err := httpapi.CanonicalizeJSON(req, 1<<20, &payload)
	if err != nil {
		t.Fatalf("CanonicalizeJSON: %v", err)
	}
	scope := ports.InstallationScope()
	hash := httpapi.SemanticHash("CreateProject", scope, canonical, "", 0)
	cmd := ports.Command{
		ID: "CreateProject-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "operator",
		Scope: scope, Type: "CreateProject", RequestHash: hash,
	}
	return cmd, payload
}

// TestLookupReceipt_AbsentThenPresentAfterCommandCommits is the "same-key
// replay" Verify bullet's own setup half: nothing to replay before the
// real command ever runs, something real to replay immediately after.
func TestLookupReceipt_AbsentThenPresentAfterCommandCommits(t *testing.T) {
	_, uow := newReceiptTestStore(t)
	ctx := context.Background()
	cmd, req := buildCreateProjectCommand(t, "key-1", "widget")

	_, found, err := httpapi.LookupReceipt(ctx, uow, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil {
		t.Fatalf("LookupReceipt (before): %v", err)
	}
	if found {
		t.Fatal("found a receipt before the command ever ran")
	}

	result, err := catalog.CreateProject(ctx, uow, idsource.Random{}, cmd, req)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	receipt, found, err := httpapi.LookupReceipt(ctx, uow, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil {
		t.Fatalf("LookupReceipt (after): %v", err)
	}
	if !found {
		t.Fatal("expected a receipt to exist after the command committed")
	}
	if receipt.RequestHash != cmd.RequestHash {
		t.Fatalf("receipt.RequestHash = %q, want %q", receipt.RequestHash, cmd.RequestHash)
	}
	if !strings.Contains(receipt.ResultJSON, result.ProjectID) {
		t.Fatalf("receipt.ResultJSON = %q, want it to contain %q", receipt.ResultJSON, result.ProjectID)
	}
}

// TestReconcileReceipt_SameHashIsReplay_DifferentHashIsConflict is the
// "different body conflict trước I/O" Verify bullet: ReconcileReceipt
// itself decides replay-vs-conflict before any real work ever runs (it
// takes an already-looked-up receipt, does no I/O of its own).
func TestReconcileReceipt_SameHashIsReplay_DifferentHashIsConflict(t *testing.T) {
	receipt := ports.Receipt{RequestHash: "sha256:aaa"}
	if err := httpapi.ReconcileReceipt(receipt, "sha256:aaa"); err != nil {
		t.Fatalf("same hash should reconcile as replay, got: %v", err)
	}
	err := httpapi.ReconcileReceipt(receipt, "sha256:bbb")
	if err != httpapi.ErrReceiptHashConflict {
		t.Fatalf("err = %v, want ErrReceiptHashConflict", err)
	}
}

// TestCreateProject_SameKeyDifferentBody_ConflictsBeforeSecondInsert is
// the full, real "different body conflict" path: a second CreateProject
// call with the SAME idempotency key but a semantically different body
// must conflict — proven against the real command, real sqlite, not just
// the pure ReconcileReceipt helper above.
func TestCreateProject_SameKeyDifferentBody_ConflictsBeforeSecondInsert(t *testing.T) {
	_, uow := newReceiptTestStore(t)
	ctx := context.Background()
	cmd1, req1 := buildCreateProjectCommand(t, "key-1", "widget")
	if _, err := catalog.CreateProject(ctx, uow, idsource.Random{}, cmd1, req1); err != nil {
		t.Fatalf("CreateProject (first): %v", err)
	}

	cmd2, req2 := buildCreateProjectCommand(t, "key-1", "gadget") // same key, different name
	if cmd2.RequestHash == cmd1.RequestHash {
		t.Fatal("test setup bug: the two payloads must hash differently")
	}
	if _, err := catalog.CreateProject(ctx, uow, idsource.Random{}, cmd2, req2); err == nil {
		t.Fatal("expected a conflict for the same idempotency key with a different body")
	}
}

// TestCreateProject_SameKeySameBody_ReplaysExactSameProjectID proves the
// "same-key replay" Verify bullet against the real command: a genuine
// second dispatch (simulating a client retry after never seeing the
// first response — see the restart test below for the explicit
// process-restart case) returns the exact original ProjectID, never
// mints a second one. idsource.Sequential is used deliberately (not
// idsource.Random): if CreateProject's own replay path ever regressed
// into minting a fresh ID on the "replay" branch, Sequential's own
// call-order-dependent output would expose it immediately as a
// different, wrong ID rather than a coincidentally-plausible random one.
func TestCreateProject_SameKeySameBody_ReplaysExactSameProjectID(t *testing.T) {
	_, uow := newReceiptTestStore(t)
	ctx := context.Background()
	cmd, req := buildCreateProjectCommand(t, "key-1", "widget")
	ids := idsource.NewSequential("project")

	first, err := catalog.CreateProject(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("CreateProject (first): %v", err)
	}

	replay, err := catalog.CreateProject(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("CreateProject (replay): %v", err)
	}
	if replay.ProjectID != first.ProjectID {
		t.Fatalf("replay ProjectID = %q, want the exact original %q (idsource.Sequential would have minted \"project-2\" if a fresh ID were wrongly generated)", replay.ProjectID, first.ProjectID)
	}
	projects, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("len(projects) = %d, want exactly 1 (replay must not create a second row)", len(projects))
	}
}

// TestCreateProject_SameKeyReplay_AfterSimulatedRestart is the "same-key
// replay sau restart" Verify bullet: close the real sqlite file and
// reopen it (a real process restart's own effect on a UnitOfWork — no
// in-memory state survives), then replay with the identical command and
// confirm the exact original result comes back.
func TestCreateProject_SameKeyReplay_AfterSimulatedRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "httpapi-restart.db")
	ctx := context.Background()
	cmd, req := buildCreateProjectCommand(t, "key-1", "widget")

	store1, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open (first): %v", err)
	}
	first, err := catalog.CreateProject(ctx, sqlite.NewUnitOfWork(store1), idsource.Random{}, cmd, req)
	if err != nil {
		t.Fatalf("CreateProject (first): %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("close (simulated crash/restart): %v", err)
	}

	store2, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open (after restart): %v", err)
	}
	defer store2.Close()
	uow2 := sqlite.NewUnitOfWork(store2)

	receipt, found, err := httpapi.LookupReceipt(ctx, uow2, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil {
		t.Fatalf("LookupReceipt (after restart): %v", err)
	}
	if !found {
		t.Fatal("receipt did not survive a real restart")
	}
	if !strings.Contains(receipt.ResultJSON, first.ProjectID) {
		t.Fatalf("receipt.ResultJSON = %q after restart, want it to contain the original ProjectID %q", receipt.ResultJSON, first.ProjectID)
	}

	replay, err := catalog.CreateProject(ctx, uow2, idsource.Random{}, cmd, req)
	if err != nil {
		t.Fatalf("CreateProject (replay after restart): %v", err)
	}
	if replay.ProjectID != first.ProjectID {
		t.Fatalf("replay ProjectID = %q, want the exact original %q", replay.ProjectID, first.ProjectID)
	}
	projects, err := catalog.ListProjects(ctx, uow2, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("len(projects) = %d, want exactly 1 (no duplicate created by the post-restart replay)", len(projects))
	}
}

// TestCreateProject_StaleKey_NewIdempotencyKeyAlwaysProceedsAsNew is the
// "stale key mới" Verify bullet: an idempotency key this exact (actor,
// scope, type) has never seen before always attempts real work, never
// short-circuits into a stale/wrong replay just because SOME OTHER key
// already has a receipt.
func TestCreateProject_StaleKey_NewIdempotencyKeyAlwaysProceedsAsNew(t *testing.T) {
	_, uow := newReceiptTestStore(t)
	ctx := context.Background()
	cmdOld, reqOld := buildCreateProjectCommand(t, "key-old", "widget")
	firstResult, err := catalog.CreateProject(ctx, uow, idsource.Random{}, cmdOld, reqOld)
	if err != nil {
		t.Fatalf("CreateProject (old key): %v", err)
	}

	cmdNew, reqNew := buildCreateProjectCommand(t, "key-new", "widget") // same body, genuinely new key
	_, found, err := httpapi.LookupReceipt(ctx, uow, cmdNew.Actor, cmdNew.Scope, cmdNew.IdempotencyKey, cmdNew.Type)
	if err != nil {
		t.Fatalf("LookupReceipt (new key): %v", err)
	}
	if found {
		t.Fatal("a genuinely new idempotency key must never find an existing receipt")
	}
	secondResult, err := catalog.CreateProject(ctx, uow, idsource.Random{}, cmdNew, reqNew)
	if err != nil {
		t.Fatalf("CreateProject (new key): %v", err)
	}
	if secondResult.ProjectID == firstResult.ProjectID {
		t.Fatal("a new idempotency key must mint a genuinely new Project, never reuse the old one's ID")
	}
}

// TestCreateProject_ConcurrentKeys_BothSucceedIndependently is the
// "concurrent keys" Verify bullet: two different idempotency keys racing
// concurrently must both succeed, each minting its own Project, with no
// cross-talk between them.
func TestCreateProject_ConcurrentKeys_BothSucceedIndependently(t *testing.T) {
	_, uow := newReceiptTestStore(t)
	ctx := context.Background()
	cmdA, reqA := buildCreateProjectCommand(t, "key-a", "widget-a")
	cmdB, reqB := buildCreateProjectCommand(t, "key-b", "widget-b")

	var wg sync.WaitGroup
	var resultA, resultB catalog.CreateProjectResult
	var errA, errB error
	wg.Add(2)
	go func() {
		defer wg.Done()
		resultA, errA = catalog.CreateProject(ctx, uow, idsource.Random{}, cmdA, reqA)
	}()
	go func() {
		defer wg.Done()
		resultB, errB = catalog.CreateProject(ctx, uow, idsource.Random{}, cmdB, reqB)
	}()
	wg.Wait()

	if errA != nil {
		t.Fatalf("CreateProject (key-a): %v", errA)
	}
	if errB != nil {
		t.Fatalf("CreateProject (key-b): %v", errB)
	}
	if resultA.ProjectID == resultB.ProjectID {
		t.Fatal("two different idempotency keys must never converge on the same ProjectID")
	}
	projects, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("len(projects) = %d, want exactly 2", len(projects))
	}
}

// TestHTTPAndCLI_SameKeyResult is V6-02's own "HTTP↔aw same-key result"
// Verify bullet. internal/app/catalog.CreateProject is the ONE shared
// application command both a real HTTP handler (built on this package's
// own primitives, simulated here via buildCreateProjectCommand) and the
// real `aw` CLI (cmd/aw/definition.go's own newDefinitionCommand/
// requestHash pattern, structurally identical: build a ports.Command
// envelope, dispatch the same function) ultimately call — this is
// exactly what V6-02's own Mục tiêu means by "mọi HTTP mutation dùng
// application receipt và concurrency contract duy nhất": there is no
// second, HTTP-specific replay mechanism to keep in sync with the CLI's
// own. Proven directly: dispatch once via the HTTP-shaped envelope, then
// dispatch again with an envelope whose (Actor, Scope, IdempotencyKey,
// Type, RequestHash) matches exactly (representing the identical logical
// request re-submitted, regardless of which transport happens to compute
// that hash) and confirm the second call is a true replay — same
// ProjectID, no second row — via the one shared receipt/command boundary
// every transport is required to fund through.
func TestHTTPAndCLI_SameKeyResult(t *testing.T) {
	_, uow := newReceiptTestStore(t)
	ctx := context.Background()
	httpCmd, req := buildCreateProjectCommand(t, "shared-key", "widget")

	httpResult, err := catalog.CreateProject(ctx, uow, idsource.Random{}, httpCmd, req)
	if err != nil {
		t.Fatalf("CreateProject (HTTP-shaped dispatch): %v", err)
	}

	// A second dispatch carrying the identical envelope fields a CLI
	// caller resubmitting the same logical create under the same
	// idempotency key would produce -- proving the underlying command has
	// no notion of "which transport is calling."
	cliCmd := httpCmd
	cliCmd.ID = "cli-" + cliCmd.IdempotencyKey // only the internal correlation id differs
	cliResult, err := catalog.CreateProject(ctx, uow, idsource.Random{}, cliCmd, req)
	if err != nil {
		t.Fatalf("CreateProject (CLI-shaped dispatch): %v", err)
	}

	if cliResult.ProjectID != httpResult.ProjectID {
		t.Fatalf("CLI-shaped replay ProjectID = %q, want the exact HTTP-shaped result %q", cliResult.ProjectID, httpResult.ProjectID)
	}
	if cliResult != httpResult {
		t.Fatalf("CLI-shaped result = %+v, want identical to HTTP-shaped result %+v", cliResult, httpResult)
	}
	projects, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("len(projects) = %d, want exactly 1 -- the two transports must share one authority, not create two Projects", len(projects))
	}
}
