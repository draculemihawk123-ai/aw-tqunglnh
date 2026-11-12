package forgerelease

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

func TestForgeEvents_GoldenFixturesDecode(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)
	cases := []struct {
		eventType string
		version   int
		file      string
		want      any
	}{
		{FamilyBranchPushedEventType, FamilyBranchPushedSchemaVersion, "family_branch_pushed_v1.json",
			familyBranchPushedPayload{RepositoryWorkspaceID: "rw-1", Remote: "origin", RemoteBranch: "agentkit/w-1", Revision: "abc", Created: true, Actor: "operator-1"}},
		{PullRequestOpenedEventType, PullRequestOpenedSchemaVersion, "pull_request_opened_v1.json",
			pullRequestOpenedPayload{RepositoryWorkspaceID: "rw-1", Number: 5, URL: "https://github.com/o/r/pull/5", Base: "main", Actor: "operator-1"}},
		{PullRequestMergedEventType, PullRequestMergedSchemaVersion, "pull_request_merged_v1.json",
			pullRequestMergedPayload{RepositoryWorkspaceID: "rw-1", Number: 5, URL: "https://github.com/o/r/pull/5", Method: "merge", Head: "abc", NoChecks: true, Actor: "operator-1"}},
	}
	for _, c := range cases {
		raw, err := os.ReadFile(filepath.Join("testdata", "golden", c.file))
		if err != nil {
			t.Fatal(err)
		}
		got, err := registry.Decode(c.eventType, c.version, string(raw))
		if err != nil || got != c.want {
			t.Fatalf("Decode(%s) = %+v, %v; want %+v", c.eventType, got, err, c.want)
		}
	}
}
