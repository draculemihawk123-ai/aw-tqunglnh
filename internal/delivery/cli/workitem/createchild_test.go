package workitem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
)

func decodeCreateChildResult(t *testing.T, stdout *bytes.Buffer) workapp.CreateChildWorkItemResult {
	t.Helper()
	var result workapp.CreateChildWorkItemResult
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &result); err != nil {
		t.Fatalf("decode CreateChildWorkItemResult: %v", err)
	}
	return result
}

func TestRunWorkItemCreateChild_InheritsParentProjectAndFamily(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"title":"Sub-task","parentJoinPolicy":"ALL_OF","effectiveScope":[{"repositoryId":"repo-1","access":"WRITE","pathScopes":["**"],"reason":"child task"}]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--idempotency-key", "key-child-1", root.WorkItemID}
	if err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr); err != nil {
		t.Fatalf("RunWorkItemCreateChild() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeCreateChildResult(t, &stdout)
	if result.ProjectID != "project-1" || result.FamilyID != root.FamilyID || result.ParentWorkItemID != root.WorkItemID {
		t.Fatalf("result = %+v, want ProjectID=project-1 FamilyID=%s ParentWorkItemID=%s", result, root.FamilyID, root.WorkItemID)
	}
	if result.Status != "BACKLOG" {
		t.Fatalf("result.Status = %s, want BACKLOG", result.Status)
	}
}

// TestRunWorkItemCreateChild_NoProjectIDFlagNeeded_DerivedFromParent proves
// this leaf never requires (or even accepts a meaningfully-checked)
// --project-id: passing a bogus one via a positional/extra-arg attempt is
// rejected as a usage error, not silently accepted as a second project
// scope — the flag genuinely does not exist on this subcommand's own
// FlagSet.
func TestRunWorkItemCreateChild_NoProjectIDFlagNeeded_DerivedFromParent(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"title":"Sub-task","parentJoinPolicy":"ALL_OF","effectiveScope":[{"repositoryId":"repo-1","access":"WRITE","pathScopes":["**"],"reason":"child task"}]}`
	var stdout, stderr bytes.Buffer
	// --project-id is not a flag this subcommand registers at all — passing
	// it must fail flag parsing (an unrecognized flag), never silently
	// resolve to some other scope.
	args := []string{"--project-id", "project-1", root.WorkItemID}
	err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr)
	if err == nil {
		t.Fatal("RunWorkItemCreateChild() with an unrecognized --project-id flag returned nil error, want a flag-parse failure")
	}
}

// TestRunWorkItemCreateChild_EffectiveScopeExceedsFamilyScope_Rejected
// proves the READ->WRITE escalation / ungranted-repository rejection this
// task's own reused ValidateEffectiveScopes already enforces is surfaced
// cleanly through this CLI leaf.
func TestRunWorkItemCreateChild_EffectiveScopeExceedsFamilyScope_Rejected(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")
	mustCreateActiveRepository(t, u, deps.IDs, "project-1", "repo-2")

	body := `{"title":"Sub-task","parentJoinPolicy":"ALL_OF","effectiveScope":[{"repositoryId":"repo-2","access":"WRITE","pathScopes":["**"],"reason":"never granted to the family"}]}`
	var stdout, stderr bytes.Buffer
	args := []string{root.WorkItemID}
	err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, args, strings.NewReader(body), &stdout, &stderr)
	if !errors.Is(err, workapp.ErrEffectiveScopeExceedsFamilyScope) {
		t.Fatalf("error = %v, want ErrEffectiveScopeExceedsFamilyScope", err)
	}
}

func TestRunWorkItemCreateChild_MissingParentJoinPolicy_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"title":"Sub-task","effectiveScope":[{"repositoryId":"repo-1","access":"WRITE","reason":"child task"}]}`
	var stdout, stderr bytes.Buffer
	err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, []string{root.WorkItemID}, strings.NewReader(body), &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunWorkItemCreateChild() with no parentJoinPolicy returned %v, want a cli.UsageError", err)
	}
}

func TestRunWorkItemCreateChild_MissingParentArgument_IsError(t *testing.T) {
	deps := newTestDeps(t)
	body := `{"title":"Sub-task","parentJoinPolicy":"ALL_OF","effectiveScope":[{"repositoryId":"repo-1","access":"WRITE","reason":"child task"}]}`
	var stdout, stderr bytes.Buffer
	err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, nil, strings.NewReader(body), &stdout, &stderr)
	if err == nil {
		t.Fatal("RunWorkItemCreateChild() with no <parentWorkItemId> argument returned nil error")
	}
}

// TestRunWorkItemCreateChild_ReplaySameIdempotencyKey_ReturnsIdenticalResult
// mirrors TestRunWorkItemCreate_ReplaySameIdempotencyKey_ReturnsIdenticalResult
// for create-child.
func TestRunWorkItemCreateChild_ReplaySameIdempotencyKey_ReturnsIdenticalResult(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	body := `{"title":"Sub-task","parentJoinPolicy":"ALL_OF","effectiveScope":[{"repositoryId":"repo-1","access":"WRITE","pathScopes":["**"],"reason":"child task"}]}`
	args := []string{"--idempotency-key", "key-child-replay", root.WorkItemID}

	var first bytes.Buffer
	if err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, args, strings.NewReader(body), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunWorkItemCreateChild() error = %v", err)
	}
	firstResult := decodeCreateChildResult(t, &first)

	var second bytes.Buffer
	if err := cliworkitem.RunWorkItemCreateChild(context.Background(), deps, args, strings.NewReader(body), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second (replay) RunWorkItemCreateChild() error = %v", err)
	}
	if !replayedField(t, second.String()) {
		t.Fatal("second call reported Replayed = false, want true")
	}
	secondResult := decodeCreateChildResult(t, &second)
	if secondResult.WorkItemID != firstResult.WorkItemID {
		t.Fatalf("replay result WorkItemID = %s, want the exact original %s", secondResult.WorkItemID, firstResult.WorkItemID)
	}

	children, err := u.Snapshot.Work().ListChildWorkItems(context.Background(), root.WorkItemID)
	if err != nil {
		t.Fatalf("ListChildWorkItems: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("children after replay = %d, want exactly 1 (the replay must never create a second one)", len(children))
	}
}
