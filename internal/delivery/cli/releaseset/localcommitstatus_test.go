package releaseset_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliReleaseSet "github.com/taQuangLing/agent-workflow/internal/delivery/cli/releaseset"
)

func TestRunLocalCommitStatus_ReportsEachStateOfTheOperation(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, rw, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")
	requested := requestLocalCommitFixture(t, u, deps.IDs, "project-1", rs, rw, "key-status")

	status := func() (state string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		err := cliReleaseSet.RunLocalCommitStatus(context.Background(), deps, []string{"--project-id", "project-1", rs.ReleaseSetID, requested.ReleaseSetLocalCommitID}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("RunLocalCommitStatus: %v, stderr=%s", err, stderr.String())
		}
		var result struct {
			ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
			ReleaseSetID            string `json:"releaseSetId"`
			ProjectID               string `json:"projectId"`
			State                   string `json:"state"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("decode %q: %v", stdout.String(), err)
		}
		if result.ReleaseSetLocalCommitID != requested.ReleaseSetLocalCommitID || result.ReleaseSetID != rs.ReleaseSetID || result.ProjectID != "project-1" {
			t.Fatalf("status identities = %+v", result)
		}
		return result.State
	}

	if got := status(); got != "REQUESTED" {
		t.Fatalf("state = %s, want REQUESTED", got)
	}
	if err := forceLocalCommitCommitted(u, requested.ReleaseSetLocalCommitID); err != nil {
		t.Fatalf("force COMMITTED: %v", err)
	}
	if got := status(); got != "COMMITTED" {
		t.Fatalf("state = %s, want COMMITTED after the operation completed", got)
	}
}

func TestRunLocalCommitStatus_WrongProjectOrReleaseSetIsNotFoundNotALeak(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, rw, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")
	requested := requestLocalCommitFixture(t, u, deps.IDs, "project-1", rs, rw, "key-status-scope")

	for name, args := range map[string][]string{
		"another project":     {"--project-id", "project-2", rs.ReleaseSetID, requested.ReleaseSetLocalCommitID},
		"another release set": {"--project-id", "project-1", "other-release-set", requested.ReleaseSetLocalCommitID},
	} {
		var stdout, stderr bytes.Buffer
		err := cliReleaseSet.RunLocalCommitStatus(context.Background(), deps, args, &stdout, &stderr)
		if err == nil {
			t.Errorf("%s: the status must not be readable", name)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: wrote %q to stdout", name, stdout.String())
		}
	}
}

func TestRunLocalCommitStatus_UnknownIDAndUsageErrors(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	if err := cliReleaseSet.RunLocalCommitStatus(context.Background(), deps, []string{"--project-id", "project-1", "rs", "no-such-commit"}, &stdout, &stderr); err == nil {
		t.Fatal("an unknown local commit id must be an error")
	}
	for name, args := range map[string][]string{
		"no project id": {"rs", "lc"},
		"one argument":  {"--project-id", "project-1", "rs"},
		"blank id":      {"--project-id", "project-1", "rs", " "},
	} {
		err := cliReleaseSet.RunLocalCommitStatus(context.Background(), deps, args, &stdout, &stderr)
		if err == nil || !cli.IsUsageError(err) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}
