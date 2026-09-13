package releasesetcommit

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure" convention,
// applied from the start (unlike workspacerelease's own event, which
// V6-00A had to retrofit): every event type this package produces is named
// and wired through eventschema.Registry from its first commit.

const (
	ReleaseSetLocalCommitRequestedEventType     = "ReleaseSetLocalCommitRequested"
	ReleaseSetLocalCommitRequestedSchemaVersion = 1

	ReleaseSetLocalCommitCommittedEventType     = "ReleaseSetLocalCommitCommitted"
	ReleaseSetLocalCommitCommittedSchemaVersion = 1

	ReleaseSetLocalCommitFailedEventType     = "ReleaseSetLocalCommitFailed"
	ReleaseSetLocalCommitFailedSchemaVersion = 1
)

// releaseSetLocalCommitRequestedEventPayload is
// ReleaseSetLocalCommitRequested v1's own shape (RequestReleaseSetLocalCommit,
// commands.go).
type releaseSetLocalCommitRequestedEventPayload struct {
	ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
	ReleaseSetID            string `json:"releaseSetId"`
	RepositoryWorkspaceID   string `json:"repositoryWorkspaceId"`
	Marker                  string `json:"marker"`
	JobID                   string `json:"jobId"`
}

// releaseSetLocalCommitCommittedEventPayload is
// ReleaseSetLocalCommitCommitted v1's own shape (execute.go's own finalize
// step) — appended in the SAME transaction that stores the real commit
// result (GC-INV-15).
type releaseSetLocalCommitCommittedEventPayload struct {
	ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
	ParentVCSObjectID       string `json:"parentVcsObjectId"`
	ResultVCSObjectID       string `json:"resultVcsObjectId"`
	JobID                   string `json:"jobId,omitempty"`
}

// releaseSetLocalCommitFailedEventPayload is ReleaseSetLocalCommitFailed
// v1's own shape (execute.go's own typed terminal failure path).
type releaseSetLocalCommitFailedEventPayload struct {
	ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
	FailureReason           string `json:"failureReason"`
	JobID                   string `json:"jobId,omitempty"`
}

func DecodeReleaseSetLocalCommitRequestedV1(payloadJSON string) (any, error) {
	var payload releaseSetLocalCommitRequestedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeReleaseSetLocalCommitCommittedV1(payloadJSON string) (any, error) {
	var payload releaseSetLocalCommitCommittedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeReleaseSetLocalCommitFailedV1(payloadJSON string) (any, error) {
	var payload releaseSetLocalCommitFailedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/workspacerelease/event_schema.go's
// own RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(ReleaseSetLocalCommitRequestedEventType, ReleaseSetLocalCommitRequestedSchemaVersion, DecodeReleaseSetLocalCommitRequestedV1)
	registry.Register(ReleaseSetLocalCommitCommittedEventType, ReleaseSetLocalCommitCommittedSchemaVersion, DecodeReleaseSetLocalCommitCommittedV1)
	registry.Register(ReleaseSetLocalCommitFailedEventType, ReleaseSetLocalCommitFailedSchemaVersion, DecodeReleaseSetLocalCommitFailedV1)
}
