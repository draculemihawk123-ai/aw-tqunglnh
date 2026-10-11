package work

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// WorkItemSupersession (V11-10, ADR-041) records that one WorkItem replaced another: the operator redid a task with the
// newest workflow version by cancelling the old WorkItem and creating a new one with the same content. The link is the only
// thing that connects the two (a WorkItem pins one workflow version for good, so "redo with a newer definition" is a new
// WorkItem, never a change to the old one). The old WorkItem can be superseded at most once; a WorkItem can supersede at
// most one other, so redos form a chain the Board follows both ways.
type WorkItemSupersession struct {
	ProjectID     project.ProjectID
	OldWorkItemID WorkItemID
	NewWorkItemID WorkItemID
	Actor         string
	Reason        string
	CreatedAt     time.Time
}

// NewWorkItemSupersession validates and builds a supersession link.
func NewWorkItemSupersession(projectID project.ProjectID, oldID, newID WorkItemID, actor, reason string, createdAt time.Time) (WorkItemSupersession, error) {
	if projectID == "" || oldID == "" || newID == "" {
		return WorkItemSupersession{}, errors.New("work item supersession identities are required")
	}
	if oldID == newID {
		return WorkItemSupersession{}, errors.New("a work item cannot supersede itself")
	}
	if strings.TrimSpace(actor) == "" {
		return WorkItemSupersession{}, errors.New("work item supersession actor is required")
	}
	if strings.TrimSpace(reason) == "" {
		return WorkItemSupersession{}, errors.New("work item supersession reason is required")
	}
	if createdAt.IsZero() {
		return WorkItemSupersession{}, errors.New("work item supersession created timestamp is required")
	}
	return WorkItemSupersession{
		ProjectID: projectID, OldWorkItemID: oldID, NewWorkItemID: newID,
		Actor: strings.TrimSpace(actor), Reason: strings.TrimSpace(reason), CreatedAt: createdAt.UTC(),
	}, nil
}
