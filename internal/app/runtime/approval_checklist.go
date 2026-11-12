package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V12-31 (ADR-063): an APPROVAL node with a checklist is a QA gate. Its items are the acceptance criteria of the run's work item
// (when the node asks for them) and the node's own items; the approver marks each one PASSED or FAILED (a note — required for a
// failed item — and images attached to the work item's conversation), and the outcome follows: the node's passedOutcome when
// every item passed, its failedOutcome otherwise. The decision stores the filled checklist as the gate's QA_CHECKLIST record.

// EvidenceKindQAChecklist is the kind of the record a checklist approval leaves.
const EvidenceKindQAChecklist = "QA_CHECKLIST"

// Checklist item results and sources.
const (
	ChecklistPassed = "PASSED"
	ChecklistFailed = "FAILED"

	ChecklistSourceAcceptance = "ACCEPTANCE_CRITERION"
	ChecklistSourceManual     = "MANUAL"

	maxChecklistNote        = 2000
	maxChecklistAttachments = 10
)

// Errors of a checklist decision.
var (
	ErrChecklistRequired   = errors.New("runtime: this approval is a QA checklist: give a result for every item")
	ErrChecklistNotOffered = errors.New("runtime: this approval has no checklist")
	ErrChecklistInvalid    = errors.New("runtime: invalid checklist result")
	ErrChecklistOutcome    = errors.New("runtime: the outcome does not follow the checklist")
)

// ChecklistItemResult is the approver's result for one item.
type ChecklistItemResult struct {
	ID     string `json:"id"`
	Result string `json:"result"`
	Note   string `json:"note,omitempty"`
	// AttachmentMessageIDs are attachments (images) of the work item's conversation shown as this item's evidence.
	AttachmentMessageIDs []string `json:"attachmentMessageIds,omitempty"`
}

// ChecklistItemView is one item, with its result once decided.
type ChecklistItemView struct {
	ID                   string   `json:"id"`
	Text                 string   `json:"text"`
	Source               string   `json:"source"`
	Result               string   `json:"result,omitempty"`
	Note                 string   `json:"note,omitempty"`
	AttachmentMessageIDs []string `json:"attachmentMessageIds,omitempty"`
}

// ApprovalChecklistView is the checklist of a checklist approval: its items (with results once decided) and the two outcomes.
type ApprovalChecklistView struct {
	PassedOutcome string              `json:"passedOutcome"`
	FailedOutcome string              `json:"failedOutcome"`
	Items         []ChecklistItemView `json:"items"`
	// Verdict is PASSED or FAILED once decided (empty while pending, and for an escalated request).
	Verdict   string     `json:"verdict,omitempty"`
	DecidedBy string     `json:"decidedBy,omitempty"`
	DecidedAt *time.Time `json:"decidedAt,omitempty"`
}

// ChecklistItems lists a checklist's items for the work item under QA.
func ChecklistItems(cfg *workflow.ApprovalChecklistConfig, item workdomain.WorkItem) []ChecklistItemView {
	if cfg == nil {
		return nil
	}
	out := []ChecklistItemView{}
	if cfg.FromAcceptanceCriteria {
		for i, ac := range item.AcceptanceCriteria {
			out = append(out, ChecklistItemView{ID: fmt.Sprintf("ac-%d", i+1), Text: ac.Description, Source: ChecklistSourceAcceptance})
		}
	}
	for i, text := range cfg.Items {
		out = append(out, ChecklistItemView{ID: fmt.Sprintf("item-%d", i+1), Text: text, Source: ChecklistSourceManual})
	}
	return out
}

// applyChecklist checks the results against the items and the outcome, and returns the decided items and the verdict.
func applyChecklist(items []ChecklistItemView, cfg *workflow.ApprovalChecklistConfig, outcome string, results []ChecklistItemResult) ([]ChecklistItemView, string, error) {
	byID := map[string]ChecklistItemResult{}
	for _, r := range results {
		if _, dup := byID[r.ID]; dup {
			return nil, "", fmt.Errorf("%w: item %s is given twice", ErrChecklistInvalid, r.ID)
		}
		byID[r.ID] = r
	}
	allPassed := true
	decided := make([]ChecklistItemView, 0, len(items))
	for _, item := range items {
		r, ok := byID[item.ID]
		if !ok {
			return nil, "", fmt.Errorf("%w (missing %s: %s)", ErrChecklistRequired, item.ID, item.Text)
		}
		delete(byID, item.ID)
		note := strings.TrimSpace(r.Note)
		switch {
		case r.Result != ChecklistPassed && r.Result != ChecklistFailed:
			return nil, "", fmt.Errorf("%w: item %s result must be PASSED or FAILED", ErrChecklistInvalid, item.ID)
		case r.Result == ChecklistFailed && note == "":
			return nil, "", fmt.Errorf("%w: failed item %s needs a note saying what is wrong", ErrChecklistInvalid, item.ID)
		case len(note) > maxChecklistNote:
			return nil, "", fmt.Errorf("%w: item %s note is longer than %d characters", ErrChecklistInvalid, item.ID, maxChecklistNote)
		case len(r.AttachmentMessageIDs) > maxChecklistAttachments:
			return nil, "", fmt.Errorf("%w: item %s has more than %d attachments", ErrChecklistInvalid, item.ID, maxChecklistAttachments)
		}
		if r.Result == ChecklistFailed {
			allPassed = false
		}
		item.Result, item.Note, item.AttachmentMessageIDs = r.Result, note, append([]string(nil), r.AttachmentMessageIDs...)
		decided = append(decided, item)
	}
	for id := range byID {
		return nil, "", fmt.Errorf("%w: unknown item %s", ErrChecklistInvalid, id)
	}
	if want := cfg.OutcomeFor(allPassed); outcome != want {
		return nil, "", fmt.Errorf("%w: with these results the outcome is %q, not %q", ErrChecklistOutcome, want, outcome)
	}
	verdict := ChecklistFailed
	if allPassed {
		verdict = ChecklistPassed
	}
	return decided, verdict, nil
}

// checkedChecklist is what ResolveApproval stores after a checklist decision.
type checkedChecklist struct {
	items      []ChecklistItemView
	verdict    string
	workItemID string
}

// checkApprovalChecklist validates a decision against the node's checklist, before anything is written.
func checkApprovalChecklist(ctx context.Context, tx ports.Tx, node workflow.Node, run runtimedomain.WorkflowRun, req ResolveApprovalRequest) (*checkedChecklist, error) {
	cfg := node.Approval.Checklist
	if cfg == nil {
		if len(req.Checklist) > 0 {
			return nil, ErrChecklistNotOffered
		}
		return nil, nil
	}
	item, err := tx.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		return nil, err
	}
	decided, verdict, err := applyChecklist(ChecklistItems(cfg, item), cfg, req.Outcome, req.Checklist)
	if err != nil {
		return nil, err
	}
	return &checkedChecklist{items: decided, verdict: verdict, workItemID: string(item.ID)}, nil
}

func storeApprovalChecklist(ctx context.Context, tx ports.Tx, cmd ports.Command, request runtimedomain.ApprovalRequest, c *checkedChecklist) error {
	body, err := json.Marshal(c.items)
	if err != nil {
		return err
	}
	return tx.Approvals().PutApprovalChecklist(ctx, ports.ApprovalChecklistRecord{
		ApprovalRequestID: string(request.ID), ProjectID: string(request.ProjectID), RunID: string(request.RunID), NodeRunID: string(request.NodeRunID),
		WorkItemID: c.workItemID, Verdict: c.verdict, ItemsJSON: string(body), DecidedBy: cmd.Actor, CreatedAt: cmd.RequestedAt,
	})
}

// approvalChecklistView builds the checklist of one request: the decided record when there is one, else the pending items.
func approvalChecklistView(node workflow.Node, item *workdomain.WorkItem, record *ports.ApprovalChecklistRecord) *ApprovalChecklistView {
	if node.Approval == nil || node.Approval.Checklist == nil {
		return nil
	}
	cfg := node.Approval.Checklist
	view := &ApprovalChecklistView{PassedOutcome: cfg.PassedOutcome, FailedOutcome: cfg.FailedOutcome, Items: []ChecklistItemView{}}
	if record != nil {
		_ = json.Unmarshal([]byte(record.ItemsJSON), &view.Items)
		at := record.CreatedAt
		view.Verdict, view.DecidedBy, view.DecidedAt = record.Verdict, record.DecidedBy, &at
		return view
	}
	if item != nil {
		view.Items = ChecklistItems(cfg, *item)
	}
	return view
}

// checklistContext is what the approval views need to show checklists: the decided records and the work item under QA.
type checklistContext struct {
	records map[string]ports.ApprovalChecklistRecord
	item    *workdomain.WorkItem
}

// loadChecklistContext reads it only when the workflow has a checklist approval.
func loadChecklistContext(ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, document workflow.WorkflowDocument) (checklistContext, error) {
	has := false
	for _, n := range document.Nodes {
		if n.Approval != nil && n.Approval.Checklist != nil {
			has = true
			break
		}
	}
	if !has {
		return checklistContext{}, nil
	}
	records, err := tx.Approvals().ListApprovalChecklistsForRun(ctx, string(run.ID))
	if err != nil {
		return checklistContext{}, err
	}
	qa := checklistContext{records: map[string]ports.ApprovalChecklistRecord{}}
	for _, r := range records {
		qa.records[r.ApprovalRequestID] = r
	}
	item, err := tx.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		return checklistContext{}, err
	}
	qa.item = &item
	return qa, nil
}
