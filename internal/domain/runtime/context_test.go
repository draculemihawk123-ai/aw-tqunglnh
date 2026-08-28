package runtime

import (
	"bytes"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func TestContextSnapshotIsCanonicalAndImmutable(t *testing.T) {
	revisions, err := workspace.NewRevisionSet([]workspace.Revision{{
		RepositoryID:        project.RepositoryID("repo-a"),
		VCSObjectID:         "abc123",
		WorkspaceGeneration: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	base := ContextSnapshotInput{
		ID:        "context-1",
		AttemptID: "attempt-1",
		Messages: []ContextMessage{
			{Role: ContextRoleSystem, Content: "Follow the pinned workflow."},
			{Role: ContextRoleUser, Content: "Implement task."},
		},
		Resources: []ContextResource{
			{Kind: "skill", Reference: "implement", ContentHash: "sha256:skill"},
			{Kind: "layer", Reference: "go", ContentHash: "sha256:layer"},
		},
		Revisions: revisions,
		CreatedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	}
	first, err := NewContextSnapshot(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Resources[0], base.Resources[1] = base.Resources[1], base.Resources[0]
	second, err := NewContextSnapshot(ContextSnapshotInput{
		ID: "context-2", AttemptID: "attempt-2", Messages: first.Messages(), Resources: base.Resources,
		Revisions: revisions, CreatedAt: base.CreatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentHash() != second.ContentHash() || !bytes.Equal(first.CanonicalContent(), second.CanonicalContent()) {
		t.Fatal("resource ordering changed canonical context snapshot")
	}
	messages := first.Messages()
	messages[0].Content = "mutated"
	if first.Messages()[0].Content == "mutated" {
		t.Fatal("message accessor leaked mutable state")
	}
}

func TestContextSnapshotRejectsMissingMessage(t *testing.T) {
	if _, err := NewContextSnapshot(ContextSnapshotInput{
		ID: "context-1", AttemptID: "attempt-1", CreatedAt: time.Now(),
	}); err == nil {
		t.Fatal("NewContextSnapshot() accepted no messages")
	}
}
