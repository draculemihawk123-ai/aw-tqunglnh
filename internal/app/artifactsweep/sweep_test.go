package artifactsweep

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// mustArtifact mirrors artifact.NewArtifact but panics on error — this
// file's own tests only ever construct deliberately-valid fixtures, so a
// construction failure would itself be a test bug, not a case to assert
// on.
func mustArtifact(t *testing.T, id string, class artifact.RetentionClass, state artifact.AttachState, hold bool, contentHash string, size int64, createdAt time.Time) artifact.Artifact {
	t.Helper()
	a, err := artifact.NewArtifact(
		artifact.ID(id), project.ProjectID("project-1"), "sha256:"+contentHash, contentHash, size, "text/plain",
		redact.Public, false, class, state, hold, artifact.ComputeExpiresAt(class, createdAt), createdAt, 1,
	)
	if err != nil {
		t.Fatalf("construct fixture artifact: %v", err)
	}
	return a
}

// TestClassifyLocatorGroup_DecisionTable exercises every branch this
// package's own doc comment names — pure, no I/O, mirroring
// workspacereconcile.ClassifyReconciliation's own identical
// TestClassifyReconciliation_DecisionTable style for a sibling job.
func TestClassifyLocatorGroup_DecisionTable(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := older.Add(48 * time.Hour)
	olderThan := older.Add(24 * time.Hour)

	tests := []struct {
		name       string
		rows       []artifact.Artifact
		wantResult locatorGroupDecision
	}{
		{
			name:       "empty group blocks",
			rows:       nil,
			wantResult: locatorGroupBlocked,
		},
		{
			name: "single orphan past grace purges",
			rows: []artifact.Artifact{
				mustArtifact(t, "a1", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 10, older),
			},
			wantResult: locatorGroupPurge,
		},
		{
			name: "all orphan past grace, sharing locator, purges",
			rows: []artifact.Artifact{
				mustArtifact(t, "a1", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 10, older),
				mustArtifact(t, "a2", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 10, older),
			},
			wantResult: locatorGroupPurge,
		},
		{
			name: "one row attached blocks the whole group",
			rows: []artifact.Artifact{
				mustArtifact(t, "a1", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 10, older),
				mustArtifact(t, "a2", artifact.RetentionCanonicalContext, artifact.Attached, false, "aaaa", 10, older),
			},
			wantResult: locatorGroupBlocked,
		},
		{
			name: "one row held blocks the whole group",
			rows: []artifact.Artifact{
				mustArtifact(t, "a1", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 10, older),
				mustArtifact(t, "a2", artifact.RetentionRawOutputTemp, artifact.Orphan, true, "aaaa", 10, older),
			},
			wantResult: locatorGroupBlocked,
		},
		{
			name: "one row not yet past grace blocks the whole group",
			rows: []artifact.Artifact{
				mustArtifact(t, "a1", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 10, older),
				mustArtifact(t, "a2", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 10, fresh),
			},
			wantResult: locatorGroupBlocked,
		},
		{
			name: "content hash mismatch is corrupt",
			rows: []artifact.Artifact{
				mustArtifact(t, "a1", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 10, older),
				mustArtifact(t, "a2", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "bbbb", 10, older),
			},
			wantResult: locatorGroupCorrupt,
		},
		{
			name: "size mismatch is corrupt",
			rows: []artifact.Artifact{
				mustArtifact(t, "a1", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 10, older),
				mustArtifact(t, "a2", artifact.RetentionRawOutputTemp, artifact.Orphan, false, "aaaa", 20, older),
			},
			wantResult: locatorGroupCorrupt,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := classifyLocatorGroup(tt.rows, olderThan)
			if got != tt.wantResult {
				t.Fatalf("classifyLocatorGroup() = %s (reason=%q), want %s", got, reason, tt.wantResult)
			}
			if got != locatorGroupPurge && reason == "" {
				t.Fatalf("classifyLocatorGroup() = %s with an empty reason, want a non-empty explanation", got)
			}
		})
	}
}
