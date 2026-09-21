package workitem_test

// V6-04B's CLI coverage: `aw work-item create` and `create-child` accept the
// same optional "contract" object as the HTTP request body (inside the same
// --file/stdin JSON document), dispatch it through the same application
// command, and let the WorkItem reach READY through the same named commands —
// the terminal half of the sequence V6-14 proved impossible through the
// public surface before this task.

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// The same contract document the HTTP tests send (see
// internal/delivery/httpapi/workitem/contract_test.go's contractJSON).
const contractDoc = `{"schemaVersion":1,"behavior":"Users can export a report as CSV",` +
	`"acceptanceCriteria":[{"description":"the CSV has a header row","verificationRef":"go test ./report/..."}],` +
	`"verificationSpec":"run the report tests","riskLevel":"LOW","exclusions":["PDF export"]}`

func createBody(contract string) string {
	body := `{"title":"Root task","initialScope":[{"repositoryId":"repo-a","access":"WRITE","reason":"root scope"}]`
	if contract != "" {
		body += `,"contract":` + contract
	}
	return body + `}`
}

func createChildBody(contract string) string {
	body := `{"title":"Child task","parentJoinPolicy":"ALL","effectiveScope":[{"repositoryId":"repo-a","access":"WRITE","pathScopes":["**"],"reason":"child scope"}]`
	if contract != "" {
		body += `,"contract":` + contract
	}
	return body + `}`
}

func assertContractStored(t *testing.T, item workdomain.WorkItem) {
	t.Helper()
	wantCriteria := []workdomain.AcceptanceCriterion{{Description: "the CSV has a header row", VerificationRef: "go test ./report/..."}}
	if item.SchemaVersion != 1 || item.Behavior != "Users can export a report as CSV" ||
		!reflect.DeepEqual(item.AcceptanceCriteria, wantCriteria) || item.VerificationSpec != "run the report tests" ||
		item.RiskLevel != "LOW" || !reflect.DeepEqual(item.Exclusions, []string{"PDF export"}) || item.WorkflowVersionID != nil {
		t.Fatalf("stored work item = %+v, want the full contract from contractDoc", item)
	}
}

// TestRunWorkItemCreateChild_WithContract_JourneyToReady is the terminal
// counterpart of the HTTP journey: create-child with a contract, readiness
// says ready, mark-ready transitions BACKLOG -> READY.
func TestRunWorkItemCreateChild_WithContract_JourneyToReady(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	body := strings.ReplaceAll(createChildBody(contractDoc), `"repo-a"`, `"repo-1"`)
	var stdout, stderr bytes.Buffer
	if err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, []string{"--idempotency-key", "key-child", root.WorkItemID}, strings.NewReader(body), &stdout, &stderr); err != nil {
		t.Fatalf("create-child with a contract: %v (stderr %s)", err, stderr.String())
	}
	child := decodeCreateChildResult(t, &stdout)
	if child.Status != "BACKLOG" {
		t.Fatalf("child.Status = %s, want BACKLOG", child.Status)
	}
	assertContractStored(t, workItemState(t, u, child.WorkItemID))

	stdout.Reset()
	if err := cliworkitem.RunWorkItemReadiness(context.Background(), deps, []string{"--project-id", "project-1", child.WorkItemID}, &stdout, &stderr); err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if readiness := decodeReadiness(t, &stdout); !readiness.Ready || len(readiness.Problems) != 0 {
		t.Fatalf("readiness = %+v, want Ready with no problems", readiness)
	}

	stdout.Reset()
	if err := cliworkitem.RunWorkItemMarkReady(context.Background(), deps, []string{"--expected-version", "1", "--idempotency-key", "key-mark", child.WorkItemID}, &stdout, &stderr); err != nil {
		t.Fatalf("mark-ready: %v (stderr %s)", err, stderr.String())
	}
	if marked := decodeMarkReadyResult(t, &stdout); marked.Status != string(workdomain.WorkItemReady) || marked.Version != 2 {
		t.Fatalf("mark-ready result = %+v, want READY@2", marked)
	}
	if got := workItemState(t, u, child.WorkItemID); got.Status != workdomain.WorkItemReady {
		t.Fatalf("child status = %s, want READY", got.Status)
	}
}

// TestRunWorkItemCreate_WithContract_StoresContract: the root leaf carries the
// contract too.
func TestRunWorkItemCreate_WithContract_StoresContract(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-a")

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1", "--idempotency-key", "key-root"}
	if err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(createBody(contractDoc)), &stdout, &stderr); err != nil {
		t.Fatalf("create with a contract: %v (stderr %s)", err, stderr.String())
	}
	root := decodeCreateRootResult(t, &stdout)
	assertContractStored(t, workItemState(t, u, root.WorkItemID))

	stdout.Reset()
	if err := cliworkitem.RunWorkItemReadiness(context.Background(), deps, []string{"--project-id", "project-1", root.WorkItemID}, &stdout, &stderr); err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if readiness := decodeReadiness(t, &stdout); !readiness.Ready {
		t.Fatalf("readiness = %+v, want Ready", readiness)
	}
}

// TestRunWorkItemCreate_WithoutContract_StaysEmpty: no "contract" key is
// exactly the pre-V6-04B behaviour.
func TestRunWorkItemCreate_WithoutContract_StaysEmpty(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-a")

	var stdout, stderr bytes.Buffer
	if err := cliworkitem.RunWorkItemCreate(context.Background(), deps, []string{"--project-id", "project-1"}, strings.NewReader(createBody("")), &stdout, &stderr); err != nil {
		t.Fatalf("create: %v", err)
	}
	item := workItemState(t, u, decodeCreateRootResult(t, &stdout).WorkItemID)
	if item.SchemaVersion != 0 || item.Behavior != "" || len(item.AcceptanceCriteria) != 0 || item.VerificationSpec != "" ||
		item.RiskLevel != "" || len(item.Exclusions) != 0 || item.WorkflowVersionID != nil {
		t.Fatalf("stored work item = %+v, want an entirely empty contract", item)
	}
}

// TestRunWorkItemCreate_ContractSameKeyDifferentContract_ConflictsAndReplays:
// the CLI's own proof that the contract participates in the request hash —
// the same --idempotency-key with a body differing only in the contract is a
// hash conflict (never a silent replay of the first result) and the stored
// contract is untouched; the identical body replays.
func TestRunWorkItemCreate_ContractSameKeyDifferentContract_ConflictsAndReplays(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-a")
	args := []string{"--project-id", "project-1", "--idempotency-key", "key-conflict"}

	var first bytes.Buffer
	if err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(createBody(contractDoc)), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	created := decodeCreateRootResult(t, &first)

	changed := strings.Replace(contractDoc, "CSV", "XLSX", 1)
	for name, body := range map[string]string{
		"changed behavior": createBody(changed),
		"contract removed": createBody(""),
	} {
		err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(body), &bytes.Buffer{}, &bytes.Buffer{})
		if !errors.Is(err, cli.ErrReceiptHashConflict) {
			t.Fatalf("%s: err = %v, want cli.ErrReceiptHashConflict", name, err)
		}
	}
	assertContractStored(t, workItemState(t, u, created.WorkItemID))

	var replay bytes.Buffer
	if err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(createBody(contractDoc)), &replay, &bytes.Buffer{}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayedField(t, replay.String()) || decodeCreateRootResult(t, &replay).WorkItemID != created.WorkItemID {
		t.Fatalf("replay = %s, want Replayed=true with the original WorkItemID %s", replay.String(), created.WorkItemID)
	}
	items, err := u.Snapshot.Work().ListWorkItemsByProject(context.Background(), "project-1")
	if err != nil || len(items) != 1 {
		t.Fatalf("work items = %d (err %v), want exactly 1", len(items), err)
	}
}

// TestRunWorkItemCreateChild_ContractSameKeyDifferentContract_Conflicts is the
// same guard for create-child.
func TestRunWorkItemCreateChild_ContractSameKeyDifferentContract_Conflicts(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-a")
	args := []string{"--idempotency-key", "key-child-conflict", root.WorkItemID}

	if err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, args, strings.NewReader(createChildBody(contractDoc)), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first create-child: %v", err)
	}
	changed := strings.Replace(contractDoc, "CSV", "XLSX", 1)
	err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, args, strings.NewReader(createChildBody(changed)), &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, cli.ErrReceiptHashConflict) {
		t.Fatalf("err = %v, want cli.ErrReceiptHashConflict", err)
	}
}

// TestRunWorkItemCreate_RequestHashCanonicalForms pins the CLI's canonical
// bytes with the very same literals the HTTP test
// (TestCreateRootWorkItem_RequestHashCanonicalForms) pins, so an HTTP call and
// `aw work-item create` for the same request hash — and therefore replay —
// identically; and pins that a contract-less body still hashes over its
// pre-V6-04B bytes.
func TestRunWorkItemCreate_RequestHashCanonicalForms(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-a")
	scope := ports.ProjectScope("project-1")

	storedHash := func(key string) string {
		t.Helper()
		receipt, found, err := httpapi.LookupReceipt(context.Background(), deps.UoW, "local-operator", scope, key, "CreateRootWorkItem")
		if err != nil || !found {
			t.Fatalf("LookupReceipt(%s) = found %v, err %v", key, found, err)
		}
		return receipt.RequestHash
	}
	create := func(key, body string) {
		t.Helper()
		args := []string{"--project-id", "project-1", "--idempotency-key", key}
		if err := cliworkitem.RunWorkItemCreate(context.Background(), deps, args, strings.NewReader(body), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
	}

	const legacyCanonical = `{"title":"Root task","initialScope":[{"repositoryId":"repo-a","access":"WRITE","reason":"root scope"}]}`
	create("key-hash-legacy", createBody(""))
	if got, want := storedHash("key-hash-legacy"), httpapi.SemanticHash("CreateRootWorkItem", scope, []byte(legacyCanonical), "", 0); got != want {
		t.Fatalf("hash of a contract-less body = %s, want %s (the pre-V6-04B canonical form must be untouched)", got, want)
	}

	const withContractCanonical = `{"title":"Root task","initialScope":[{"repositoryId":"repo-a","access":"WRITE","reason":"root scope"}],` +
		`"contract":{"schemaVersion":1,"behavior":"Users can export a report as CSV",` +
		`"acceptanceCriteria":[{"description":"the CSV has a header row","verificationRef":"go test ./report/..."}],` +
		`"verificationSpec":"run the report tests","riskLevel":"LOW","exclusions":["PDF export"]}}`
	create("key-hash-contract", createBody(contractDoc))
	if got, want := storedHash("key-hash-contract"), httpapi.SemanticHash("CreateRootWorkItem", scope, []byte(withContractCanonical), "", 0); got != want {
		t.Fatalf("hash of a contract body = %s, want %s (the contract must be part of what is hashed, identically to HTTP)", got, want)
	}
}

// TestRunWorkItemCreate_MalformedContract_IsUsageError: shapes that can never
// be valid are usage errors naming every problem, raised before any command
// is dispatched — for both leaves.
func TestRunWorkItemCreate_MalformedContract_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-a")
	itemsBefore, _ := u.Snapshot.Work().ListWorkItemsByProject(context.Background(), "project-1")

	bad := `{"schemaVersion":-1,"acceptanceCriteria":[{"description":"ok"},{"description":"  ","verificationRef":"go test"}],"exclusions":["fine",""],"workflowVersionId":" "}`
	for name, run := range map[string]func() error{
		"create": func() error {
			return cliworkitem.RunWorkItemCreate(context.Background(), deps, []string{"--project-id", "project-1"}, strings.NewReader(createBody(bad)), &bytes.Buffer{}, &bytes.Buffer{})
		},
		"create-child": func() error {
			return cliworkitem.RunWorkItemCreateChild(context.Background(), deps, []string{root.WorkItemID}, strings.NewReader(createChildBody(bad)), &bytes.Buffer{}, &bytes.Buffer{})
		},
	} {
		err := run()
		if !isUsageError(err) {
			t.Fatalf("%s: err = %v, want a cli.UsageError", name, err)
		}
		for _, field := range []string{"contract.schemaVersion", "contract.acceptanceCriteria[1].description", "contract.exclusions[1]", "contract.workflowVersionId"} {
			if !strings.Contains(err.Error(), field) {
				t.Errorf("%s: error %q does not name %s", name, err.Error(), field)
			}
		}
	}
	itemsAfter, _ := u.Snapshot.Work().ListWorkItemsByProject(context.Background(), "project-1")
	if len(itemsAfter) != len(itemsBefore) {
		t.Fatalf("work items %d -> %d, want unchanged (a malformed contract must create nothing)", len(itemsBefore), len(itemsAfter))
	}
}

// TestRunWorkItemCreate_ContractUnknownKeys_IsUsageError: the contract object
// is decoded strictly, like the HTTP body — a status-like smuggle, a typo, or
// an unknown key inside a criterion is a usage error rather than being
// silently dropped (which would leave a WorkItem half-specified).
func TestRunWorkItemCreate_ContractUnknownKeys_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-a")

	for name, contract := range map[string]string{
		"status inside contract": `{"status":"READY","behavior":"x"}`,
		"typo'd key":             `{"acceptanceCriterias":[{"description":"x"}]}`,
		"unknown criterion key":  `{"acceptanceCriteria":[{"description":"x","passed":true}]}`,
	} {
		err := cliworkitem.RunWorkItemCreate(context.Background(), deps, []string{"--project-id", "project-1"}, strings.NewReader(createBody(contract)), &bytes.Buffer{}, &bytes.Buffer{})
		if !isUsageError(err) {
			t.Fatalf("%s: err = %v, want a cli.UsageError", name, err)
		}
	}
	items, _ := u.Snapshot.Work().ListWorkItemsByProject(context.Background(), "project-1")
	if len(items) != 0 {
		t.Fatalf("work items = %d, want 0", len(items))
	}
}

// TestRunWorkItemCreate_UnknownWorkflowVersion_IsTypedError: pinning a
// WorkflowVersion that does not exist surfaces the application command's own
// typed error and creates nothing.
func TestRunWorkItemCreate_UnknownWorkflowVersion_IsTypedError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-a")

	err := cliworkitem.RunWorkItemCreate(context.Background(), deps, []string{"--project-id", "project-1"},
		strings.NewReader(createBody(`{"schemaVersion":1,"workflowVersionId":"wf-does-not-exist"}`)), &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, workapp.ErrUnknownWorkflowVersion) {
		t.Fatalf("err = %v, want workapp.ErrUnknownWorkflowVersion", err)
	}
	items, _ := u.Snapshot.Work().ListWorkItemsByProject(context.Background(), "project-1")
	if len(items) != 0 {
		t.Fatalf("work items = %d, want 0", len(items))
	}
}
