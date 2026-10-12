// RetryWorkItem and RecreateWorkItem (V11-10, docs/design/14-v11-bugfix.md, ADR-041) are the two typed commands that let an
// operator deal with a task whose run failed without leaving the web: before them "run it again" was a chain of separate
// calls (send a note, resolve the RUN_FAILED blocker, start a run), and "run it with the definition I just fixed" meant
// cancelling the WorkItem and building a new one by hand. A browser that issued those calls one by one could stop halfway
// (blocker resolved, no run started; old WorkItem cancelled, no new one), so each command does all of its database writes in ONE
// transaction and records one idempotency receipt: it either happened completely or not at all, and a replay returns the first
// result, including the new WorkItem's id.
//
//   - RetryWorkItem runs the SAME WorkItem again, on the workflow version it is pinned to: an optional note becomes a USER message
//     (the next prompt carries it), the open blockers of the failed or cancelled run are resolved, and a new run starts.
//   - RecreateWorkItem cancels a WorkItem and creates a sibling with the same content (title with a " (lần N)" suffix, effective
//     scope, contract) that is pinned to the NEWEST version of the same workflow definition, marks it READY and starts its run. The
//     pinned version of an existing WorkItem never changes (a WorkItem pins one version for good); the old WorkItem is left
//     CANCELLED with a WorkItemSupersession pointing at its replacement.
//
// Both refuse, with a typed error and no change, anything that could leave state half-done or two writers on one worktree: a
// WorkItem with a run that is not finished, a family with a QUARANTINED workspace, a blocker the commands may not resolve.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// RedoDeps is what the two redo commands need beyond the unit of work: the artifact store and clock that store an operator note
// (written before the transaction, like every message), the id source and the redaction matcher.
type RedoDeps struct {
	UnitOfWork    ports.UnitOfWork
	ArtifactStore ports.ArtifactStore
	IDs           idsource.Source
	Clock         clock.Clock
	Matcher       redact.Matcher
}

const (
	WorkItemRetriedEventType     = "WORK_ITEM_RETRIED"
	WorkItemRetriedSchemaVersion = 1

	WorkItemRecreatedEventType     = "WORK_ITEM_RECREATED"
	WorkItemRecreatedSchemaVersion = 1

	// redoNoteContentType is the media type of an operator's note, the same default `aw message append` uses.
	redoNoteContentType = "text/plain"
)

var (
	// ErrWorkItemNotRetryable is returned when the WorkItem is not waiting on a failed run: only a BLOCKED or READY WorkItem can be
	// run again (ACTIVE is running, BACKLOG never ran, DONE and CANCELLED are finished).
	ErrWorkItemNotRetryable = errors.New("runtime: work item cannot be retried in its current status")
	// ErrWorkItemNeverRan is returned when the WorkItem has no run at all; start one instead of retrying.
	ErrWorkItemNeverRan = errors.New("runtime: work item has no run to retry; start a run instead")
	// ErrLatestRunNotRetryable is returned when the latest run did not end FAILED or CANCELLED (it succeeded).
	ErrLatestRunNotRetryable = errors.New("runtime: the latest run did not fail or get cancelled")
	// ErrWorkItemStillBlocked is returned when, after resolving the failed run's blockers, the WorkItem is still blocked by something
	// the command may not resolve (a scope expansion, an admission blocker, a failed release). Nothing is changed.
	ErrWorkItemStillBlocked = errors.New("runtime: work item is still blocked by a blocker retry does not resolve")
	// ErrRecreateRootUnsupported is returned for a root WorkItem: a replacement is created as a sibling under the same parent, and a
	// root has none (its family and workspace are its own).
	ErrRecreateRootUnsupported = errors.New("runtime: a root work item cannot be recreated; only a child work item can")
	// ErrWorkItemNotRecreatable is returned when the WorkItem's status does not allow a redo (BACKLOG never ran, DONE is finished).
	ErrWorkItemNotRecreatable = errors.New("runtime: work item cannot be recreated in its current status")
	// ErrWorkItemAlreadySuperseded is returned when another WorkItem already replaced this one; redo the newest instead.
	ErrWorkItemAlreadySuperseded = errors.New("runtime: work item was already replaced by a newer work item")
	// ErrNoPinnedWorkflowVersion is returned when neither the WorkItem nor any of its runs names a workflow version, so there is no
	// definition to take the newest version of.
	ErrNoPinnedWorkflowVersion = errors.New("runtime: work item has no pinned workflow version")
)

// RedoParkedSet records files the caller moved out of a worktree (parked under a hidden Git ref, not deleted) before the redo
// command ran, so a run starting from a clean state does not trip over a previous run's stray files. The command only records
// it, in its result, its receipt and its event; moving the files is the caller's job (internal/app/workitemredo), done before the
// transaction because Git never runs inside one.
type RedoParkedSet struct {
	RepositoryID          string   `json:"repositoryId"`
	RepositoryWorkspaceID string   `json:"repositoryWorkspaceId"`
	Ref                   string   `json:"ref"`
	Paths                 []string `json:"paths"`
}

// RetryWorkItemRequest is what a caller supplies to RetryWorkItem.
type RetryWorkItemRequest struct {
	ProjectID  string
	WorkItemID string
	// Note is the optional message for the agent; it is appended as a USER message before the new run starts, so the run's prompt
	// carries it. It is also the resolution note recorded on the resolved blockers.
	Note string
	// OutOfScope (KEEP or PARK, empty when there was nothing out of scope) and Parked record how the caller settled files a
	// previous run left outside the WorkItem's scope; see RedoParkedSet.
	OutOfScope string
	Parked     []RedoParkedSet
}

// RetryWorkItemResult is what RetryWorkItem returns and what a replayed receipt reconstructs.
type RetryWorkItemResult struct {
	WorkItemID         string          `json:"workItemId"`
	PreviousRunID      string          `json:"previousRunId"`
	RunID              string          `json:"runId"`
	RunState           string          `json:"runState"`
	NodeRunID          string          `json:"nodeRunId"`
	JobID              string          `json:"jobId"`
	WorkflowVersionID  string          `json:"workflowVersionId"`
	ResolvedBlockerIDs []string        `json:"resolvedBlockerIds"`
	NoteMessageID      string          `json:"noteMessageId,omitempty"`
	OutOfScope         string          `json:"outOfScope,omitempty"`
	Parked             []RedoParkedSet `json:"parked,omitempty"`
}

type workItemRetriedEventPayload struct {
	WorkItemID         string          `json:"workItemId"`
	PreviousRunID      string          `json:"previousRunId"`
	RunID              string          `json:"runId"`
	WorkflowVersionID  string          `json:"workflowVersionId"`
	ResolvedBlockerIDs []string        `json:"resolvedBlockerIds"`
	NoteMessageID      string          `json:"noteMessageId,omitempty"`
	Actor              string          `json:"actor"`
	OutOfScope         string          `json:"outOfScope,omitempty"`
	Parked             []RedoParkedSet `json:"parked,omitempty"`
}

// retryResolvableBlockerTypes are the blockers a failed or cancelled run leaves behind and that RetryWorkItem resolves itself.
func retryResolvableBlockerType(t workdomain.BlockerType) bool {
	switch t {
	case workdomain.BlockerRunFailed, workdomain.BlockerRunCancelled, workdomain.BlockerCompletionPolicyFailed:
		return true
	default:
		return false
	}
}

// RetryWorkItem runs a WorkItem again after its latest run failed or was cancelled, on the workflow version the WorkItem is pinned
// to. The whole database part (note, blockers, new run) is one transaction. See the file comment.
func RetryWorkItem(ctx context.Context, deps RedoDeps, cmd ports.Command, req RetryWorkItemRequest) (RetryWorkItemResult, error) {
	projectID := strings.TrimSpace(req.ProjectID)
	workItemID := strings.TrimSpace(req.WorkItemID)
	note := strings.TrimSpace(req.Note)
	if projectID == "" || workItemID == "" {
		return RetryWorkItemResult{}, errors.New("runtime: ProjectID and WorkItemID are required")
	}

	// A replay or a conflicting key is decided before any store I/O, exactly as AppendMessage does.
	if result, found, err := loadRedoReceipt[RetryWorkItemResult](ctx, deps.UnitOfWork, cmd); err != nil || found {
		return result, err
	}

	var prepared *artifact.Artifact
	noteRequest := appmessage.AppendMessageRequest{
		ProjectID: projectID, WorkItemID: workItemID, Role: message.RoleUser, Content: []byte(note),
		ContentType: redoNoteContentType, Sensitivity: redact.Public, Matcher: deps.Matcher,
	}
	if note != "" {
		stored, err := appmessage.PrepareMessage(ctx, deps.ArtifactStore, deps.IDs, deps.Clock, noteRequest)
		if err != nil {
			return RetryWorkItemResult{}, err
		}
		prepared = &stored
	}

	var result RetryWorkItemResult
	err := deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, found, err := loadRedoReceiptTx[RetryWorkItemResult](ctx, tx, cmd); err != nil {
			return err
		} else if found {
			result = replayed
			return nil
		}

		plan, err := planRetryTx(ctx, tx, projectID, workItemID)
		if err != nil {
			return err
		}
		last, versionID := plan.last, plan.versionID
		reason := note
		if reason == "" {
			reason = "retried from the operator console"
		}
		resolved := []string{}
		for _, blocker := range plan.toResolve {
			if _, err := resolveWorkItemBlockerTx(ctx, tx, ResolutionModeResolved, string(blocker.ID), cmd.Actor, reason, "", cmd.CorrelationID); err != nil {
				return err
			}
			resolved = append(resolved, string(blocker.ID))
		}
		afterResolve, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		if afterResolve.Status != workdomain.WorkItemReady {
			return fmt.Errorf("%w: work item %s is %s%s", ErrWorkItemStillBlocked, workItemID, afterResolve.Status, openBlockerSummary(ctx, tx, workItemID))
		}

		noteMessageID := ""
		if prepared != nil {
			appended, err := appmessage.AppendPreparedMessageTx(ctx, tx, deps.IDs, subCommand(cmd, "note"), noteRequest, *prepared)
			if err != nil {
				return err
			}
			noteMessageID = appended.MessageID
		}

		started, err := startWorkflowRunTx(ctx, tx, deps.IDs, subCommand(cmd, "run"), StartWorkflowRunRequest{
			ProjectID: projectID, WorkItemID: workItemID, WorkflowVersionID: versionID,
		})
		if err != nil {
			return err
		}

		result = RetryWorkItemResult{
			WorkItemID: workItemID, PreviousRunID: string(last.ID), RunID: started.RunID, RunState: started.State,
			NodeRunID: started.NodeRunID, JobID: started.JobID, WorkflowVersionID: versionID,
			ResolvedBlockerIDs: resolved, NoteMessageID: noteMessageID, OutOfScope: req.OutOfScope, Parked: req.Parked,
		}
		payload, err := json.Marshal(workItemRetriedEventPayload{
			WorkItemID: workItemID, PreviousRunID: string(last.ID), RunID: started.RunID, WorkflowVersionID: versionID,
			ResolvedBlockerIDs: resolved, NoteMessageID: noteMessageID, Actor: cmd.Actor, OutOfScope: req.OutOfScope, Parked: req.Parked,
		})
		if err != nil {
			return fmt.Errorf("marshal %s event payload: %w", WorkItemRetriedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-retried", ProjectID: projectID,
			AggregateType: "WorkItemRetry", AggregateID: cmd.ID, Sequence: 1,
			EventType: WorkItemRetriedEventType, SchemaVersion: WorkItemRetriedSchemaVersion, PayloadJSON: string(payload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}
		return recordRedoReceipt(ctx, tx, cmd, result)
	})
	return result, err
}

// RecreateWorkItemRequest is what a caller supplies to RecreateWorkItem.
type RecreateWorkItemRequest struct {
	ProjectID  string
	WorkItemID string
	// Reason is required: it is recorded on the old WorkItem's cancellation and on the supersession link.
	Reason string
	// Note is an optional extra USER message for the new WorkItem's agent.
	Note string
	// CarryMessages copies the operator's (USER) messages of the old WorkItem to the new one. The caller chooses it; the product
	// default is to carry them.
	CarryMessages bool
	// OutOfScope and Parked: see RetryWorkItemRequest.
	OutOfScope string
	Parked     []RedoParkedSet
}

// RecreateWorkItemResult is what RecreateWorkItem returns and what a replayed receipt reconstructs.
type RecreateWorkItemResult struct {
	OldWorkItemID             string          `json:"oldWorkItemId"`
	NewWorkItemID             string          `json:"newWorkItemId"`
	ParentWorkItemID          string          `json:"parentWorkItemId"`
	Title                     string          `json:"title"`
	RedoNumber                int             `json:"redoNumber"`
	PreviousWorkflowVersionID string          `json:"previousWorkflowVersionId"`
	WorkflowVersionID         string          `json:"workflowVersionId"`
	RunID                     string          `json:"runId"`
	RunState                  string          `json:"runState"`
	NodeRunID                 string          `json:"nodeRunId"`
	JobID                     string          `json:"jobId"`
	OldWorkItemStatus         string          `json:"oldWorkItemStatus"`
	CarriedMessageCount       int             `json:"carriedMessageCount"`
	NoteMessageID             string          `json:"noteMessageId,omitempty"`
	OutOfScope                string          `json:"outOfScope,omitempty"`
	Parked                    []RedoParkedSet `json:"parked,omitempty"`
}

type workItemRecreatedEventPayload struct {
	OldWorkItemID             string          `json:"oldWorkItemId"`
	NewWorkItemID             string          `json:"newWorkItemId"`
	RunID                     string          `json:"runId"`
	PreviousWorkflowVersionID string          `json:"previousWorkflowVersionId"`
	WorkflowVersionID         string          `json:"workflowVersionId"`
	RedoNumber                int             `json:"redoNumber"`
	CarriedMessageCount       int             `json:"carriedMessageCount"`
	Reason                    string          `json:"reason"`
	Actor                     string          `json:"actor"`
	OutOfScope                string          `json:"outOfScope,omitempty"`
	Parked                    []RedoParkedSet `json:"parked,omitempty"`
}

// redoTitleSuffix matches the " (lần N)" suffix a previous redo appended, so redoing a redo does not stack suffixes.
var redoTitleSuffix = regexp.MustCompile(`\s*\(lần \d+\)\s*$`)

// RecreateWorkItem cancels a WorkItem and creates a sibling with the same content that runs on the newest version of the same
// workflow definition. See the file comment.
func RecreateWorkItem(ctx context.Context, deps RedoDeps, cmd ports.Command, req RecreateWorkItemRequest) (RecreateWorkItemResult, error) {
	projectID := strings.TrimSpace(req.ProjectID)
	workItemID := strings.TrimSpace(req.WorkItemID)
	reason := strings.TrimSpace(req.Reason)
	note := strings.TrimSpace(req.Note)
	if projectID == "" || workItemID == "" {
		return RecreateWorkItemResult{}, errors.New("runtime: ProjectID and WorkItemID are required")
	}
	if reason == "" {
		return RecreateWorkItemResult{}, errors.New("runtime: Reason is required")
	}

	if result, found, err := loadRedoReceipt[RecreateWorkItemResult](ctx, deps.UnitOfWork, cmd); err != nil || found {
		return result, err
	}

	// The note belongs to a WorkItem that does not exist yet, so its content is stored first and the message row is written, for
	// the new WorkItem's id, inside the transaction.
	var prepared *artifact.Artifact
	noteRequest := appmessage.AppendMessageRequest{
		ProjectID: projectID, Role: message.RoleUser, Content: []byte(note),
		ContentType: redoNoteContentType, Sensitivity: redact.Public, Matcher: deps.Matcher,
	}
	if note != "" {
		probe := noteRequest
		probe.WorkItemID = workItemID // PrepareMessage validates the request; the real target id is set below
		stored, err := appmessage.PrepareMessage(ctx, deps.ArtifactStore, deps.IDs, deps.Clock, probe)
		if err != nil {
			return RecreateWorkItemResult{}, err
		}
		prepared = &stored
	}

	var result RecreateWorkItemResult
	err := deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayed, found, err := loadRedoReceiptTx[RecreateWorkItemResult](ctx, tx, cmd); err != nil {
			return err
		} else if found {
			result = replayed
			return nil
		}

		plan, err := planRecreateTx(ctx, tx, projectID, workItemID)
		if err != nil {
			return err
		}
		old, pinnedID, latestID, redoNumber := plan.old, plan.pinnedID, plan.latestID, plan.redoNumber

		scopes, err := tx.Work().ListWorkItemEffectiveScopes(ctx, workItemID)
		if err != nil {
			return err
		}
		grants := make([]work.ScopeGrantRequest, 0, len(scopes))
		for _, scope := range scopes {
			grants = append(grants, work.ScopeGrantRequest{
				RepositoryID: string(scope.RepositoryID()), Access: string(scope.Access()),
				PathScopes: scope.PathScopes(), Reason: scope.Reason(),
			})
		}
		criteria := make([]work.AcceptanceCriterionRequest, 0, len(old.AcceptanceCriteria))
		for _, criterion := range old.AcceptanceCriteria {
			criteria = append(criteria, work.AcceptanceCriterionRequest{Description: criterion.Description, VerificationRef: criterion.VerificationRef})
		}
		sourceNodeRunID := ""
		if old.SourceNodeRunID != nil {
			sourceNodeRunID = string(*old.SourceNodeRunID)
		}
		title := fmt.Sprintf("%s (lần %d)", strings.TrimSpace(redoTitleSuffix.ReplaceAllString(old.Title, "")), redoNumber)

		// Everything below changes state, in one transaction: the old WorkItem stops being a candidate first, then the replacement is
		// created, made READY and started, and the link between them is recorded.
		oldStatus := string(old.Status)
		if old.Status != workdomain.WorkItemCancelled {
			cancelled, err := cancelWorkItemTx(ctx, tx, deps.IDs, workItemID, cmd.Actor, reason, cmd.CorrelationID)
			if err != nil {
				return err
			}
			if cancelled.Status != string(workdomain.WorkItemCancelled) {
				return fmt.Errorf("runtime: work item %s did not reach CANCELLED (status %s)", workItemID, cancelled.Status)
			}
			oldStatus = cancelled.Status
		}

		created, err := work.CreateChildWorkItemTx(ctx, tx, deps.IDs, subCommand(cmd, "child"), work.CreateChildWorkItemRequest{
			ParentWorkItemID: string(*old.ParentID), Title: title, ParentJoinPolicy: string(old.ParentJoinPolicy),
			SourceNodeRunID: sourceNodeRunID, EffectiveScope: grants,
			Contract: &work.WorkItemContractRequest{
				SchemaVersion: old.SchemaVersion, Behavior: old.Behavior, AcceptanceCriteria: criteria,
				VerificationSpec: old.VerificationSpec, RiskLevel: string(old.RiskLevel), Exclusions: old.Exclusions,
				WorkflowVersionID: latestID,
			},
		})
		if err != nil {
			return err
		}
		if _, err := work.MarkWorkItemReadyTx(ctx, tx, subCommand(cmd, "ready"), work.MarkWorkItemReadyRequest{WorkItemID: created.WorkItemID}); err != nil {
			return err
		}

		carried := 0
		if req.CarryMessages {
			messages, err := tx.Messages().ListMessagesForWorkItem(ctx, workItemID)
			if err != nil {
				return err
			}
			for _, m := range messages {
				if m.Role != message.RoleUser {
					continue
				}
				if _, err := appmessage.CopyMessageTx(ctx, tx, deps.IDs, subCommand(cmd, fmt.Sprintf("carry-%d", carried)), m, created.WorkItemID); err != nil {
					return err
				}
				carried++
			}
		}
		noteMessageID := ""
		if prepared != nil {
			noteRequest.WorkItemID = created.WorkItemID
			appended, err := appmessage.AppendPreparedMessageTx(ctx, tx, deps.IDs, subCommand(cmd, "note"), noteRequest, *prepared)
			if err != nil {
				return err
			}
			noteMessageID = appended.MessageID
		}

		started, err := startWorkflowRunTx(ctx, tx, deps.IDs, subCommand(cmd, "run"), StartWorkflowRunRequest{
			ProjectID: projectID, WorkItemID: created.WorkItemID, WorkflowVersionID: latestID,
		})
		if err != nil {
			return err
		}

		supersession, err := workdomain.NewWorkItemSupersession(old.ProjectID, old.ID, workdomain.WorkItemID(created.WorkItemID), cmd.Actor, reason, cmd.RequestedAt)
		if err != nil {
			return err
		}
		if _, err := tx.Work().RecordWorkItemSupersession(ctx, supersession); err != nil {
			return err
		}

		result = RecreateWorkItemResult{
			OldWorkItemID: workItemID, NewWorkItemID: created.WorkItemID, ParentWorkItemID: created.ParentWorkItemID, Title: title,
			RedoNumber: redoNumber, PreviousWorkflowVersionID: pinnedID, WorkflowVersionID: latestID,
			RunID: started.RunID, RunState: started.State, NodeRunID: started.NodeRunID, JobID: started.JobID,
			OldWorkItemStatus: oldStatus, CarriedMessageCount: carried, NoteMessageID: noteMessageID, OutOfScope: req.OutOfScope, Parked: req.Parked,
		}
		payload, err := json.Marshal(workItemRecreatedEventPayload{
			OldWorkItemID: workItemID, NewWorkItemID: created.WorkItemID, RunID: started.RunID,
			PreviousWorkflowVersionID: pinnedID, WorkflowVersionID: latestID, RedoNumber: redoNumber,
			CarriedMessageCount: carried, Reason: reason, Actor: cmd.Actor, OutOfScope: req.OutOfScope, Parked: req.Parked,
		})
		if err != nil {
			return fmt.Errorf("marshal %s event payload: %w", WorkItemRecreatedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-recreated", ProjectID: projectID,
			AggregateType: "WorkItemRecreation", AggregateID: cmd.ID, Sequence: 1,
			EventType: WorkItemRecreatedEventType, SchemaVersion: WorkItemRecreatedSchemaVersion, PayloadJSON: string(payload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}
		return recordRedoReceipt(ctx, tx, cmd, result)
	})
	return result, err
}

// retryPlan is everything RetryWorkItem (and the read-only assessment behind the redo preview) derives from the stored state.
type retryPlan struct {
	item      workdomain.WorkItem
	last      runtimedomain.WorkflowRun
	versionID string
	toResolve []workdomain.WorkItemBlocker
}

// planRetryTx decides whether a retry can run and what it would do, changing nothing. Every refusal of RetryWorkItem is made here.
func planRetryTx(ctx context.Context, tx ports.Tx, projectID, workItemID string) (retryPlan, error) {
	item, err := loadRedoTarget(ctx, tx, projectID, workItemID)
	if err != nil {
		return retryPlan{}, err
	}
	if item.Status != workdomain.WorkItemBlocked && item.Status != workdomain.WorkItemReady {
		return retryPlan{}, fmt.Errorf("%w: work item %s is %s", ErrWorkItemNotRetryable, workItemID, item.Status)
	}
	runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, workItemID)
	if err != nil {
		return retryPlan{}, err
	}
	if len(runs) == 0 {
		return retryPlan{}, fmt.Errorf("%w: work item %s", ErrWorkItemNeverRan, workItemID)
	}
	last, err := requireFinishedRuns(runs, workItemID)
	if err != nil {
		return retryPlan{}, err
	}
	if last.State != runtimedomain.WorkflowRunFailed && last.State != runtimedomain.WorkflowRunCancelled {
		return retryPlan{}, fmt.Errorf("%w: run %s is %s", ErrLatestRunNotRetryable, last.ID, last.State)
	}
	// The run uses the version the WorkItem is pinned to; a WorkItem that pins nothing keeps the version its last run used.
	versionID := string(last.WorkflowVersionID)
	if item.WorkflowVersionID != nil {
		versionID = string(*item.WorkflowVersionID)
	}
	blockers, err := tx.Work().ListWorkItemBlockersForWorkItem(ctx, workItemID)
	if err != nil {
		return retryPlan{}, err
	}
	plan := retryPlan{item: item, last: last, versionID: versionID}
	var others []string
	for _, blocker := range blockers {
		if blocker.State != workdomain.BlockerOpen {
			continue
		}
		if retryResolvableBlockerType(blocker.Type) {
			plan.toResolve = append(plan.toResolve, blocker)
		} else {
			others = append(others, string(blocker.Type))
		}
	}
	if len(others) > 0 {
		return retryPlan{}, fmt.Errorf("%w: work item %s has open blockers retry does not resolve: %s", ErrWorkItemStillBlocked, workItemID, strings.Join(others, ", "))
	}
	if err := ensureNoQuarantinedWorkspaceTx(ctx, tx, item); err != nil {
		return retryPlan{}, err
	}
	return plan, nil
}

// redoVersions are the pinned workflow version of a WorkItem and the newest version of the same workflow definition.
type redoVersions struct {
	pinnedID, latestID         string
	pinnedNumber, latestNumber uint64
}

// redoVersionsTx resolves the version a WorkItem is pinned to (a WorkItem that pins nothing keeps the version its last run used) and
// the newest version of the same definition. The newest is at least the pinned one, so a redo with no newer version is a clean redo
// of the same one. ErrNoPinnedWorkflowVersion when neither the WorkItem nor a run names a version.
func redoVersionsTx(ctx context.Context, tx ports.Tx, item workdomain.WorkItem, runs []runtimedomain.WorkflowRun) (redoVersions, error) {
	var out redoVersions
	switch {
	case item.WorkflowVersionID != nil:
		out.pinnedID = string(*item.WorkflowVersionID)
	case len(runs) > 0:
		out.pinnedID = string(runs[len(runs)-1].WorkflowVersionID)
	default:
		return redoVersions{}, fmt.Errorf("%w: work item %s", ErrNoPinnedWorkflowVersion, item.ID)
	}
	pinned, err := tx.Definitions().GetWorkflowVersion(ctx, out.pinnedID)
	if err != nil {
		return redoVersions{}, err
	}
	versions, err := tx.Definitions().ListVersions(ctx, definition.KindWorkflow, string(pinned.DefinitionID()))
	if err != nil {
		return redoVersions{}, err
	}
	out.pinnedNumber = pinned.VersionNumber()
	out.latestID, out.latestNumber = out.pinnedID, out.pinnedNumber
	for _, version := range versions {
		if version.VersionNumber() > out.latestNumber {
			out.latestID, out.latestNumber = version.ID(), version.VersionNumber()
		}
	}
	return out, nil
}

// recreatePlan is what RecreateWorkItem derives from the stored state.
type recreatePlan struct {
	old          workdomain.WorkItem
	runs         []runtimedomain.WorkflowRun
	pinnedID     string
	latestID     string
	latestNumber uint64
	pinnedNumber uint64
	redoNumber   int
}

// planRecreateTx decides whether a redo can run and with which versions, changing nothing. Every refusal of RecreateWorkItem is made here.
func planRecreateTx(ctx context.Context, tx ports.Tx, projectID, workItemID string) (recreatePlan, error) {
	old, err := loadRedoTarget(ctx, tx, projectID, workItemID)
	if err != nil {
		return recreatePlan{}, err
	}
	if old.ParentID == nil {
		return recreatePlan{}, fmt.Errorf("%w: work item %s", ErrRecreateRootUnsupported, workItemID)
	}
	switch old.Status {
	case workdomain.WorkItemBlocked, workdomain.WorkItemReady, workdomain.WorkItemActive, workdomain.WorkItemCancelled:
	default:
		return recreatePlan{}, fmt.Errorf("%w: work item %s is %s", ErrWorkItemNotRecreatable, workItemID, old.Status)
	}
	if successor, err := tx.Work().GetWorkItemSupersessionByOld(ctx, workItemID); err == nil {
		return recreatePlan{}, fmt.Errorf("%w: work item %s was replaced by %s", ErrWorkItemAlreadySuperseded, workItemID, successor.NewWorkItemID)
	} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
		return recreatePlan{}, err
	}
	runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, workItemID)
	if err != nil {
		return recreatePlan{}, err
	}
	if _, err := requireFinishedRuns(runs, workItemID); err != nil {
		return recreatePlan{}, err
	}
	if err := ensureNoQuarantinedWorkspaceTx(ctx, tx, old); err != nil {
		return recreatePlan{}, err
	}

	plan := recreatePlan{old: old, runs: runs, redoNumber: 2}
	versions, err := redoVersionsTx(ctx, tx, old, runs)
	if err != nil {
		return recreatePlan{}, err
	}
	plan.pinnedID, plan.pinnedNumber, plan.latestID, plan.latestNumber = versions.pinnedID, versions.pinnedNumber, versions.latestID, versions.latestNumber
	for cursor := workItemID; ; {
		previous, err := tx.Work().GetWorkItemSupersessionByNew(ctx, cursor)
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			break
		}
		if err != nil {
			return recreatePlan{}, err
		}
		plan.redoNumber++
		cursor = string(previous.OldWorkItemID)
	}
	return plan, nil
}

// loadRedoTarget reloads the WorkItem and checks it belongs to the project the caller named, never trusting the request beyond ids.
func loadRedoTarget(ctx context.Context, tx ports.Tx, projectID, workItemID string) (workdomain.WorkItem, error) {
	item, err := tx.Work().GetWorkItem(ctx, workItemID)
	if err != nil {
		return workdomain.WorkItem{}, err
	}
	if string(item.ProjectID) != projectID {
		return workdomain.WorkItem{}, fmt.Errorf("runtime: %w: work item %s belongs to another project", ports.ErrScopeMismatch, workItemID)
	}
	return item, nil
}

// requireFinishedRuns returns the most recently started run, or ErrWorkItemHasNonTerminalRun when any run is not finished.
func requireFinishedRuns(runs []runtimedomain.WorkflowRun, workItemID string) (runtimedomain.WorkflowRun, error) {
	var last runtimedomain.WorkflowRun
	for i, run := range runs {
		if !isWorkflowRunTerminal(run.State) {
			return runtimedomain.WorkflowRun{}, fmt.Errorf("%w: work item %s run %s is %s", ErrWorkItemHasNonTerminalRun, workItemID, run.ID, run.State)
		}
		if i == 0 || startedAfter(run, last) {
			last = run
		}
	}
	return last, nil
}

// startedAfter reports whether a started strictly later than b; runs are listed oldest first, so a tie keeps the later one.
func startedAfter(a, b runtimedomain.WorkflowRun) bool {
	if a.StartedAt == nil || b.StartedAt == nil {
		return true
	}
	return !a.StartedAt.Before(*b.StartedAt)
}

// openBlockerSummary lists the types of the WorkItem's still-open blockers for an error message.
func openBlockerSummary(ctx context.Context, tx ports.Tx, workItemID string) string {
	blockers, err := tx.Work().ListWorkItemBlockersForWorkItem(ctx, workItemID)
	if err != nil {
		return ""
	}
	var types []string
	for _, blocker := range blockers {
		if blocker.State == workdomain.BlockerOpen {
			types = append(types, string(blocker.Type))
		}
	}
	if len(types) == 0 {
		return ""
	}
	return " (open blockers: " + strings.Join(types, ", ") + ")"
}

// subCommand derives the command a sub-step of a composed command runs under: the same actor, scope and timestamp, with its own
// id (event ids and aggregate ids are keyed by it) and idempotency key (a derived job key).
func subCommand(cmd ports.Command, step string) ports.Command {
	sub := cmd
	sub.ID = cmd.ID + "-" + step
	sub.IdempotencyKey = cmd.IdempotencyKey + "-" + step
	return sub
}

func loadRedoReceipt[T any](ctx context.Context, uow ports.UnitOfWork, cmd ports.Command) (T, bool, error) {
	var result T
	var found bool
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		result, found, err = loadRedoReceiptTx[T](ctx, tx, cmd)
		return err
	})
	return result, found, err
}

func loadRedoReceiptTx[T any](ctx context.Context, tx ports.Tx, cmd ports.Command) (T, bool, error) {
	var result T
	receipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil || !found {
		return result, false, err
	}
	if receipt.RequestHash != cmd.RequestHash {
		return result, false, ports.ErrReceiptConflict
	}
	if err := json.Unmarshal([]byte(receipt.ResultJSON), &result); err != nil {
		return result, false, err
	}
	return result, true, nil
}

func recordRedoReceipt(ctx context.Context, tx ports.Tx, cmd ports.Command, result any) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal receipt result: %w", err)
	}
	return tx.Receipts().Record(ctx, ports.Receipt{
		Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
		CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
		CreatedAt: cmd.RequestedAt,
	})
}
