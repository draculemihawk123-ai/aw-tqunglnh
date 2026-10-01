package scopeexpansion_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/scopeexpansion"
)

func TestShow_ReportsTheRequestAndItsCurrentVersion(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "show-happy.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	var stdout, stderr bytes.Buffer
	if err := scopeexpansion.Show(context.Background(), deps, []string{"--project-id", "project-1", requestID}, &stdout, &stderr); err != nil {
		t.Fatalf("Show: %v, stderr=%s", err, stderr.String())
	}
	var detail work.ScopeExpansionRequestDetail
	if err := json.Unmarshal(stdout.Bytes(), &detail); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if detail.RequestID != requestID || detail.FamilyID != root.FamilyID || detail.Status != "PENDING" || detail.Version != 1 {
		t.Fatalf("detail = %+v, want request %s of family %s PENDING at version 1", detail, requestID, root.FamilyID)
	}

	// The Version `show` reports is exactly what `withdraw --expected-version`
	// accepts: reading it first and acting on it is the operator's real flow.
	if err := scopeexpansion.Withdraw(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "1", requestID}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	stdout.Reset()
	if err := scopeexpansion.Show(context.Background(), deps, []string{"--project-id", "project-1", requestID}, &stdout, &stderr); err != nil {
		t.Fatalf("Show after withdraw: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &detail); err != nil || detail.Status != "WITHDRAWN" || detail.Version != 2 {
		t.Fatalf("after withdraw: detail = %+v err = %v, want WITHDRAWN at version 2", detail, err)
	}
}

func TestShow_UnknownRequestAndForeignProjectAreErrors(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "show-scope.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	requestID := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")

	for name, args := range map[string][]string{
		"unknown request": {"--project-id", "project-1", "no-such-request"},
		"another project": {"--project-id", "project-2", requestID},
	} {
		var stdout, stderr bytes.Buffer
		if err := scopeexpansion.Show(context.Background(), deps, args, &stdout, &stderr); err == nil {
			t.Errorf("%s: want an error", name)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: wrote %q to stdout", name, stdout.String())
		}
	}
}

func TestList_ReturnsEveryRequestOfTheFamilyAcrossStatuses(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "list-happy.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")
	seedActiveRepository(t, uow, deps.IDs, "project-1", "repo-2")
	withdrawn := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-1")
	if err := scopeexpansion.Withdraw(context.Background(), deps, []string{"--project-id", "project-1", "--expected-version", "1", withdrawn}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	pending := mustRequestScopeExpansion(t, deps, root.FamilyID, "project-1", "repo-2", "idem-request-2")

	var stdout, stderr bytes.Buffer
	if err := scopeexpansion.List(context.Background(), deps, []string{"--project-id", "project-1", root.FamilyID}, &stdout, &stderr); err != nil {
		t.Fatalf("List: %v, stderr=%s", err, stderr.String())
	}
	var body struct {
		Items []work.ScopeExpansionRequestDetail `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	status := map[string]string{}
	for _, item := range body.Items {
		status[item.RequestID] = item.Status
	}
	if len(body.Items) != 2 || status[withdrawn] != "WITHDRAWN" || status[pending] != "PENDING" {
		t.Fatalf("items = %v, want the WITHDRAWN and the PENDING request of the family", status)
	}

	// Exactly what the application query returns — the list is not a CLI-local
	// reinterpretation of it.
	want, err := work.ListFamilyScopeExpansionRequests(context.Background(), uow, ports.ProjectScope("project-1"), root.FamilyID)
	if err != nil || len(want) != len(body.Items) {
		t.Fatalf("app query = %d items (err %v), CLI = %d", len(want), err, len(body.Items))
	}
}

func TestList_EmptyFamilyIsAnEmptyItemsArrayNotNull(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "list-empty.db")
	deps := newTestDeps(uow)
	root := familyFixture(t, uow, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	if err := scopeexpansion.List(context.Background(), deps, []string{"--project-id", "project-1", root.FamilyID}, &stdout, &stderr); err != nil {
		t.Fatalf("List: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if string(raw["items"]) != "[]" {
		t.Fatalf("items = %s, want [] (a client iterating the result must never see null)", raw["items"])
	}
}

func TestShowAndList_UsageErrors(t *testing.T) {
	_, uow := openScopeExpansionCLITestStore(t, "queries-usage.db")
	deps := newTestDeps(uow)
	for name, args := range map[string][]string{
		"no project id": {"r1"},
		"no argument":   {"--project-id", "p1"},
		"blank id":      {"--project-id", "p1", " "},
		"two arguments": {"--project-id", "p1", "r1", "r2"},
	} {
		var stdout, stderr bytes.Buffer
		if err := scopeexpansion.Show(context.Background(), deps, args, &stdout, &stderr); err == nil || !cli.IsUsageError(err) {
			t.Errorf("Show %s: err = %v, want a usage error", name, err)
		}
		if err := scopeexpansion.List(context.Background(), deps, args, &stdout, &stderr); err == nil || !cli.IsUsageError(err) {
			t.Errorf("List %s: err = %v, want a usage error", name, err)
		}
	}
}
