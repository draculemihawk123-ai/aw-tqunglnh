package runtime

import (
	"errors"
	"strings"
)

// BranchTokenID identifies one persisted fork branch.
type BranchTokenID string

// BranchTokenState is a branch's own completion status, independent of
// queue/worker state (HE-14-M04, V4-11's "queue empty không ảnh hưởng join
// verdict"). V4-01 only persists tokens; the FORK activation logic that
// creates them and the JOIN policy that reads them are V4-10/V4-11's own
// scope.
type BranchTokenState string

const (
	BranchTokenActive    BranchTokenState = "ACTIVE"
	BranchTokenSucceeded BranchTokenState = "SUCCEEDED"
	BranchTokenFailed    BranchTokenState = "FAILED"
	BranchTokenCancelled BranchTokenState = "CANCELLED"
)

// BranchToken is one persisted, queue-order-independent branch identity for
// a FORK node (HE-14-M09, design/01-system-design.md §6.3: "run/fork/branch/
// current node/state/version; unique run+fork+branch"). ForkKey is the
// FORK NodeRun's own NodeKey; BranchKey is the declared branch identifier
// within that fork.
type BranchToken struct {
	ID             BranchTokenID
	RunID          WorkflowRunID
	ForkKey        string
	BranchKey      string
	CurrentNodeKey string
	State          BranchTokenState
	Version        uint64
}

// NewBranchToken validates and builds a new, ACTIVE branch token.
func NewBranchToken(
	id BranchTokenID,
	runID WorkflowRunID,
	forkKey string,
	branchKey string,
	currentNodeKey string,
) (BranchToken, error) {
	forkKey = strings.TrimSpace(forkKey)
	branchKey = strings.TrimSpace(branchKey)
	currentNodeKey = strings.TrimSpace(currentNodeKey)
	if id == "" || runID == "" {
		return BranchToken{}, errors.New("branch token identities are required")
	}
	if forkKey == "" || branchKey == "" || currentNodeKey == "" {
		return BranchToken{}, errors.New("branch token fork key, branch key and current node key are required")
	}
	return BranchToken{
		ID:             id,
		RunID:          runID,
		ForkKey:        forkKey,
		BranchKey:      branchKey,
		CurrentNodeKey: currentNodeKey,
		State:          BranchTokenActive,
		Version:        1,
	}, nil
}
