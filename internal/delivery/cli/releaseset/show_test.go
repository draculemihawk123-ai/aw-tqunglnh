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

func TestRunShow_ReturnsReleaseSetDetail(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	if err := cliReleaseSet.RunShow(context.Background(), deps, []string{rs.ReleaseSetID}, &stdout, &stderr); err != nil {
		t.Fatalf("RunShow() error = %v, stderr = %s", err, stderr.String())
	}

	var detail workapp.ReleaseSetDetail
	if err := json.Unmarshal(stdout.Bytes(), &detail); err != nil {
		t.Fatalf("decode ReleaseSetDetail: %v", err)
	}
	if detail.ReleaseSetID != rs.ReleaseSetID || detail.ProjectID != "project-1" || detail.State != "CREATED" {
		t.Fatalf("detail = %+v, want ReleaseSetID=%s ProjectID=project-1 State=CREATED", detail, rs.ReleaseSetID)
	}
	if len(detail.Entries) != 1 || detail.Entries[0].RepositoryID != "repo-1" {
		t.Fatalf("detail.Entries = %+v, want exactly one entry for repo-1", detail.Entries)
	}
}

func TestRunShow_UnknownID_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	if err := cliReleaseSet.RunShow(context.Background(), deps, []string{"no-such-release-set"}, &stdout, &stderr); err == nil {
		t.Fatal("RunShow() with an unknown id succeeded, want an error")
	}
}

func TestRunShow_MissingArgument_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := cliReleaseSet.RunShow(context.Background(), deps, nil, &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunShow() with no <releaseSetId> argument returned %v, want a cli.UsageError", err)
	}
}
