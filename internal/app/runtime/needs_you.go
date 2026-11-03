// V12-16 (docs/design/15-v12-ui-operations.md, UX-28, ADR-052): "Needs you" — every thing waiting for a person in a project, across
// its tasks: an approval waiting for a decision (a review gate, a needs-info question, a size review), and an open blocker (a failed
// or cancelled run, a node blocked by drift, a usage limit, a lost sign-in, a scope expansion, a failed commit). A read only: each item
// names what the task page already offers to act on it.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// Kinds of NeedsYouItem.
const (
	NeedsYouApproval = "APPROVAL_PENDING"
	NeedsYouBlocker  = "BLOCKER"
)

// NeedsYouItem is one thing that waits for a person.
type NeedsYouItem struct {
	Kind          string `json:"kind"`
	WorkItemID    string `json:"workItemId"`
	WorkItemTitle string `json:"workItemTitle"`
	RunID         string `json:"runId,omitempty"`
	NodeRunID     string `json:"nodeRunId,omitempty"`
	NodeKey       string `json:"nodeKey,omitempty"`
	// Type is the blocker type for a BLOCKER item; the node key's approval for an APPROVAL item.
	Type string `json:"type,omitempty"`
	// Outcomes are the decisions an APPROVAL item offers (resolveApproval with one of them).
	Outcomes          []string  `json:"outcomes,omitempty"`
	ApprovalRequestID string    `json:"approvalRequestId,omitempty"`
	ApprovalVersion   uint64    `json:"approvalVersion,omitempty"`
	AuthorizedRoles   []string  `json:"authorizedRoles,omitempty"`
	BlockerID         string    `json:"blockerId,omitempty"`
	Message           string    `json:"message"`
	Since             time.Time `json:"since"`
}

// NeedsYou is the list, newest first, and its count.
type NeedsYou struct {
	ProjectID string         `json:"projectId"`
	Count     int            `json:"count"`
	Items     []NeedsYouItem `json:"items"`
}

// ListNeedsYou reads what waits for a person in projectID (work items that are not DONE or CANCELLED).
func ListNeedsYou(ctx context.Context, uow ports.UnitOfWork, matcher redact.Matcher, projectID string) (NeedsYou, error) {
	if strings.TrimSpace(projectID) == "" {
		return NeedsYou{}, errors.New("runtime: projectID is required")
	}
	result := NeedsYou{ProjectID: projectID, Items: []NeedsYouItem{}}
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().GetProject(ctx, projectID); err != nil {
			return err
		}
		items, err := tx.Work().ListWorkItemsByProject(ctx, projectID)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Status == workdomain.WorkItemDone || item.Status == workdomain.WorkItemCancelled {
				continue
			}
			blockers, err := tx.Work().ListWorkItemBlockersForWorkItem(ctx, string(item.ID))
			if err != nil {
				return err
			}
			for _, b := range blockers {
				if b.State != workdomain.BlockerOpen {
					continue
				}
				result.Items = append(result.Items, NeedsYouItem{
					Kind: NeedsYouBlocker, WorkItemID: string(item.ID), WorkItemTitle: item.Title, RunID: b.SourceRunID,
					NodeRunID: b.SourceNodeRunID, Type: string(b.Type), BlockerID: string(b.ID),
					Message: bound(redactText(matcher, b.Reason)), Since: b.OpenedAt,
				})
			}
			runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, string(item.ID))
			if err != nil {
				return err
			}
			for _, run := range runs {
				if run.State != runtimedomain.WorkflowRunRunning && run.State != runtimedomain.WorkflowRunWaiting {
					continue
				}
				requests, err := tx.Approvals().ListApprovalRequestsForRun(ctx, string(run.ID))
				if err != nil {
					return err
				}
				var outcomesOf map[string][]string
				since := time.Time{}
				if run.StartedAt != nil {
					since = *run.StartedAt
				}
				for _, r := range requests {
					if r.State != runtimedomain.ApprovalRequestPending {
						continue
					}
					if outcomesOf == nil {
						outcomesOf = map[string][]string{}
						if version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID)); err == nil {
							for _, n := range version.Document().Nodes {
								outcomesOf[n.Key] = n.Outcomes
							}
						}
					}
					result.Items = append(result.Items, NeedsYouItem{
						Kind: NeedsYouApproval, WorkItemID: string(item.ID), WorkItemTitle: item.Title, RunID: string(run.ID),
						NodeRunID: string(r.NodeRunID), NodeKey: r.NodeKey, Outcomes: outcomesOf[r.NodeKey],
						ApprovalRequestID: string(r.ID), ApprovalVersion: r.Version, AuthorizedRoles: r.AuthorizedRoles,
						Message: fmt.Sprintf("%s is waiting for a decision (due %s)", r.NodeKey, r.DueAt.UTC().Format(time.RFC3339)), Since: since,
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		return NeedsYou{}, err
	}
	sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].Since.After(result.Items[j].Since) })
	result.Count = len(result.Items)
	return result, nil
}
