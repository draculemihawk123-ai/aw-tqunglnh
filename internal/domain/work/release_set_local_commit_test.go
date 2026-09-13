package work_test

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func validReleaseSetLocalCommitArgs() (
	work.ReleaseSetLocalCommitID, project.ProjectID, work.ReleaseSetID, uint64,
	string, project.RepositoryID, uint64, uint64, string, string, string, string, string, string, time.Time,
) {
	return "rslc-1", "project-1", "rs-1", 1,
		"rw-1", "repo-1", 1, 1,
		"actor-1", "record repository result", "Release Bot", "release-bot@example.invalid",
		"sha256:message-hash", "sha256:marker", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func TestNewReleaseSetLocalCommit_ValidInputs_BuildsRequestedRecord(t *testing.T) {
	id, projectID, releaseSetID, releaseSetVersion, rwID, repoID, generation, workspaceVersion,
		actor, message, authorName, authorEmail, messageHash, marker, createdAt := validReleaseSetLocalCommitArgs()

	intent, err := work.NewReleaseSetLocalCommit(id, projectID, releaseSetID, releaseSetVersion, rwID, repoID, generation, workspaceVersion,
		actor, message, authorName, authorEmail, messageHash, marker, createdAt)
	if err != nil {
		t.Fatalf("NewReleaseSetLocalCommit: %v", err)
	}
	if intent.State != work.ReleaseSetLocalCommitRequested || intent.Version != 1 {
		t.Fatalf("intent = %+v, want REQUESTED@1", intent)
	}
	if intent.ID != id || intent.Marker != marker || intent.RepositoryWorkspaceID != rwID {
		t.Fatalf("intent = %+v, did not round-trip its own constructor arguments", intent)
	}
}

func TestNewReleaseSetLocalCommit_RejectsMissingOrInvalidFields(t *testing.T) {
	id, projectID, releaseSetID, releaseSetVersion, rwID, repoID, generation, workspaceVersion,
		actor, message, authorName, authorEmail, messageHash, marker, createdAt := validReleaseSetLocalCommitArgs()

	cases := []struct {
		name   string
		mutate func() (work.ReleaseSetLocalCommitID, project.ProjectID, work.ReleaseSetID, uint64, string, project.RepositoryID, uint64, uint64, string, string, string, string, string, string, time.Time)
	}{
		{"blank id", func() (work.ReleaseSetLocalCommitID, project.ProjectID, work.ReleaseSetID, uint64, string, project.RepositoryID, uint64, uint64, string, string, string, string, string, string, time.Time) {
			return "", projectID, releaseSetID, releaseSetVersion, rwID, repoID, generation, workspaceVersion, actor, message, authorName, authorEmail, messageHash, marker, createdAt
		}},
		{"zero expected release set version", func() (work.ReleaseSetLocalCommitID, project.ProjectID, work.ReleaseSetID, uint64, string, project.RepositoryID, uint64, uint64, string, string, string, string, string, string, time.Time) {
			return id, projectID, releaseSetID, 0, rwID, repoID, generation, workspaceVersion, actor, message, authorName, authorEmail, messageHash, marker, createdAt
		}},
		{"zero generation", func() (work.ReleaseSetLocalCommitID, project.ProjectID, work.ReleaseSetID, uint64, string, project.RepositoryID, uint64, uint64, string, string, string, string, string, string, time.Time) {
			return id, projectID, releaseSetID, releaseSetVersion, rwID, repoID, 0, workspaceVersion, actor, message, authorName, authorEmail, messageHash, marker, createdAt
		}},
		{"blank message", func() (work.ReleaseSetLocalCommitID, project.ProjectID, work.ReleaseSetID, uint64, string, project.RepositoryID, uint64, uint64, string, string, string, string, string, string, time.Time) {
			return id, projectID, releaseSetID, releaseSetVersion, rwID, repoID, generation, workspaceVersion, actor, "  ", authorName, authorEmail, messageHash, marker, createdAt
		}},
		{"blank marker", func() (work.ReleaseSetLocalCommitID, project.ProjectID, work.ReleaseSetID, uint64, string, project.RepositoryID, uint64, uint64, string, string, string, string, string, string, time.Time) {
			return id, projectID, releaseSetID, releaseSetVersion, rwID, repoID, generation, workspaceVersion, actor, message, authorName, authorEmail, messageHash, "", createdAt
		}},
		{"zero created at", func() (work.ReleaseSetLocalCommitID, project.ProjectID, work.ReleaseSetID, uint64, string, project.RepositoryID, uint64, uint64, string, string, string, string, string, string, time.Time) {
			return id, projectID, releaseSetID, releaseSetVersion, rwID, repoID, generation, workspaceVersion, actor, message, authorName, authorEmail, messageHash, marker, time.Time{}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b, c, d, e, f, g, h, i, j, k, l, m, n, o := tc.mutate()
			if _, err := work.NewReleaseSetLocalCommit(a, b, c, d, e, f, g, h, i, j, k, l, m, n, o); err == nil {
				t.Fatal("NewReleaseSetLocalCommit() = nil error, want rejection")
			}
		})
	}
}

func TestReleaseSetLocalCommitFailureReason_IsValid(t *testing.T) {
	valid := []work.ReleaseSetLocalCommitFailureReason{work.FailureWorkspaceQuarantined, work.FailureMarkerDrift}
	for _, reason := range valid {
		if !reason.IsValid() {
			t.Fatalf("%q.IsValid() = false, want true", reason)
		}
	}
	if work.ReleaseSetLocalCommitFailureReason("NOT_A_REAL_REASON").IsValid() {
		t.Fatal("unrecognized failure reason reported valid")
	}
}
