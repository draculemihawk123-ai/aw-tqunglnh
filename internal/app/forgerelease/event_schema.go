package forgerelease

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// The audit events of V12-28 (ADR-060).
const (
	FamilyBranchPushedEventType     = "FamilyBranchPushed"
	FamilyBranchPushedSchemaVersion = 1
	PullRequestOpenedEventType      = "PullRequestOpened"
	PullRequestOpenedSchemaVersion  = 1
	PullRequestMergedEventType      = "PullRequestMerged"
	PullRequestMergedSchemaVersion  = 1
)

// familyBranchPushedPayload is FamilyBranchPushed v1.
type familyBranchPushedPayload struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	Remote                string `json:"remote"`
	RemoteBranch          string `json:"remoteBranch"`
	Revision              string `json:"revision"`
	Created               bool   `json:"created"`
	Actor                 string `json:"actor"`
}

// pullRequestOpenedPayload is PullRequestOpened v1.
type pullRequestOpenedPayload struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	Number                int    `json:"number"`
	URL                   string `json:"url"`
	Draft                 bool   `json:"draft"`
	Base                  string `json:"base"`
	Actor                 string `json:"actor"`
}

// pullRequestMergedPayload is PullRequestMerged v1.
type pullRequestMergedPayload struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	Number                int    `json:"number"`
	URL                   string `json:"url"`
	Method                string `json:"method"`
	Head                  string `json:"head"`
	NoChecks              bool   `json:"noChecks"`
	Actor                 string `json:"actor"`
}

func decode[T any](payloadJSON string) (any, error) {
	var payload T
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// DecodeFamilyBranchPushedV1 decodes FamilyBranchPushed v1.
func DecodeFamilyBranchPushedV1(payloadJSON string) (any, error) {
	return decode[familyBranchPushedPayload](payloadJSON)
}

// DecodePullRequestOpenedV1 decodes PullRequestOpened v1.
func DecodePullRequestOpenedV1(payloadJSON string) (any, error) {
	return decode[pullRequestOpenedPayload](payloadJSON)
}

// DecodePullRequestMergedV1 decodes PullRequestMerged v1.
func DecodePullRequestMergedV1(payloadJSON string) (any, error) {
	return decode[pullRequestMergedPayload](payloadJSON)
}

// RegisterEventSchemas registers the events this package produces.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(FamilyBranchPushedEventType, FamilyBranchPushedSchemaVersion, DecodeFamilyBranchPushedV1)
	registry.Register(PullRequestOpenedEventType, PullRequestOpenedSchemaVersion, DecodePullRequestOpenedV1)
	registry.Register(PullRequestMergedEventType, PullRequestMergedSchemaVersion, DecodePullRequestMergedV1)
}
