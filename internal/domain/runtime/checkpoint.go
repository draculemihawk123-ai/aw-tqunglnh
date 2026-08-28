package runtime

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

type Checkpoint struct {
	ID                     CheckpointID
	RunID                  WorkflowRunID
	NodeRunID              NodeRunID
	AttemptID              ExecutionAttemptID
	Sequence               uint64
	CanonicalEventSequence uint64
	ContextSnapshotID      ContextSnapshotID
	Revisions              workspace.RevisionSet
	SharedStateHash        string
	ArtifactReferences     []string
	CreatedAt              time.Time
}

func NewCheckpoint(
	id CheckpointID,
	runID WorkflowRunID,
	nodeRunID NodeRunID,
	attemptID ExecutionAttemptID,
	sequence uint64,
	canonicalEventSequence uint64,
	contextSnapshotID ContextSnapshotID,
	revisions workspace.RevisionSet,
	sharedStateHash string,
	artifactReferences []string,
	createdAt time.Time,
) (Checkpoint, error) {
	if id == "" || runID == "" || nodeRunID == "" || attemptID == "" || contextSnapshotID == "" {
		return Checkpoint{}, errors.New("checkpoint identities are required")
	}
	if sequence == 0 || canonicalEventSequence == 0 || createdAt.IsZero() {
		return Checkpoint{}, errors.New("checkpoint sequence, event sequence and timestamp are required")
	}
	sharedStateHash = strings.TrimSpace(sharedStateHash)
	if sharedStateHash == "" {
		return Checkpoint{}, errors.New("checkpoint shared state hash is required")
	}
	copyOfRevisions, err := workspace.NewRevisionSet(revisions.Entries())
	if err != nil {
		return Checkpoint{}, err
	}
	references, err := normalizeArtifactReferences(artifactReferences)
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{
		ID:                     id,
		RunID:                  runID,
		NodeRunID:              nodeRunID,
		AttemptID:              attemptID,
		Sequence:               sequence,
		CanonicalEventSequence: canonicalEventSequence,
		ContextSnapshotID:      contextSnapshotID,
		Revisions:              copyOfRevisions,
		SharedStateHash:        sharedStateHash,
		ArtifactReferences:     references,
		CreatedAt:              createdAt.UTC(),
	}, nil
}

func normalizeArtifactReferences(references []string) ([]string, error) {
	result := append([]string(nil), references...)
	for index := range result {
		result[index] = strings.TrimSpace(result[index])
		if result[index] == "" {
			return nil, errors.New("checkpoint artifact reference is empty")
		}
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index-1] == result[index] {
			return nil, errors.New("duplicate checkpoint artifact reference")
		}
	}
	return result, nil
}
