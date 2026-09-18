package releaseset_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliReleaseSet "github.com/taQuangLing/agent-workflow/internal/delivery/cli/releaseset"
)

func TestRunList_ListsReleaseSetsForFamily(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1", root.FamilyID}
	if err := cliReleaseSet.RunList(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunList() error = %v, stderr = %s", err, stderr.String())
	}

	var view struct {
		Items []workapp.ReleaseSetDetail `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode list result: %v", err)
	}
	if len(view.Items) != 1 || view.Items[0].ReleaseSetID != rs.ReleaseSetID {
		t.Fatalf("view.Items = %+v, want exactly one entry for %s", view.Items, rs.ReleaseSetID)
	}
	if view.Items[0].State != "CREATED" {
		t.Fatalf("view.Items[0].State = %q, want CREATED", view.Items[0].State)
	}
}

func TestRunList_UnknownFamily_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	mustCreateProject(t, u, "project-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1", "no-such-family"}
	if err := cliReleaseSet.RunList(context.Background(), deps, args, &stdout, &stderr); err == nil {
		t.Fatal("RunList() with an unknown family succeeded, want an error")
	}
}

func TestRunList_MissingProjectID_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	args := []string{"family-1"}
	err := cliReleaseSet.RunList(context.Background(), deps, args, &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunList() with no --project-id returned %v, want a cli.UsageError", err)
	}
}

func TestRunList_MissingFamilyArgument_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1"}
	err := cliReleaseSet.RunList(context.Background(), deps, args, &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunList() with no <familyId> argument returned %v, want a cli.UsageError", err)
	}
}
