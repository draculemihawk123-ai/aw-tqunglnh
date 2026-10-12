package runtime

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// GetNodeRunDetail (V11-11, ADR-042) answers the question the graph never did: what was this node asked to do, what is it doing, what came
// of it, and if it failed, why and what next. It reads only what already exists — the pinned definition of the node, its NodeRun, its
// attempts (through the run timeline, so failure details and usage are the same facts the timeline shows), its Evidence rows and the
// stored artifacts they reference — and summarizes the artifacts a person wants to read at a glance (the agent's last words, a command's
// output, the files an agent changed). Everything is redacted and bounded; nothing is written.

const (
	maxResultTextBytes = 16 << 10
	maxChangedFiles    = 200
)

// NodePinView is a pinned dependency of a node (profile, command, gate or policy).
type NodePinView struct {
	Kind         string `json:"kind"`
	DefinitionID string `json:"definitionId"`
	VersionID    string `json:"versionId"`
}

// NodeDefinitionView is what the pinned workflow says this node is.
type NodeDefinitionView struct {
	Type            string        `json:"type"`
	Outcomes        []string      `json:"outcomes,omitempty"`
	Role            string        `json:"role,omitempty"`
	Pins            []NodePinView `json:"pins,omitempty"`
	FailureOutcome  string        `json:"failureOutcome,omitempty"`
	AuthorizedRoles []string      `json:"authorizedRoles,omitempty"`
	TimeoutSeconds  uint32        `json:"timeoutSeconds,omitempty"`
}

// NodeArtifactView describes a stored artifact an Evidence row references.
type NodeArtifactView struct {
	ArtifactID string `json:"artifactId"`
	MediaType  string `json:"mediaType"`
	SizeBytes  int64  `json:"sizeBytes"`
}

// NodeEvidenceView is one Evidence row of the node run.
type NodeEvidenceView struct {
	EvidenceID string             `json:"evidenceId"`
	AttemptID  string             `json:"attemptId"`
	Kind       string             `json:"kind"`
	Verdict    string             `json:"verdict"`
	CreatedAt  time.Time          `json:"createdAt"`
	Artifacts  []NodeArtifactView `json:"artifacts"`
}

// NodeTextView is a bounded, redacted text.
type NodeTextView struct {
	Text       string `json:"text"`
	Truncated  bool   `json:"truncated,omitempty"`
	EvidenceID string `json:"evidenceId,omitempty"`
	// Partial marks the words of an attempt that did not succeed: what the agent had said when it stopped.
	Partial bool `json:"partial,omitempty"`
}

// NodeChangedFile is one file an agent attempt changed.
type NodeChangedFile struct {
	RepositoryID string `json:"repositoryId"`
	Path         string `json:"path"`
	Status       string `json:"status"`
}

// NodeCommandResult is a COMMAND node's recorded execution.
type NodeCommandResult struct {
	ExitCode       int      `json:"exitCode"`
	DurationMillis int64    `json:"durationMillis"`
	Truncated      bool     `json:"truncated,omitempty"`
	Argv           []string `json:"argv,omitempty"`
	Stdout         string   `json:"stdout,omitempty"`
	Stderr         string   `json:"stderr,omitempty"`
	EvidenceID     string   `json:"evidenceId,omitempty"`
	TextTruncated  bool     `json:"textTruncated,omitempty"`
}

// NodeCriterionResult is one MACHINE_GATE criterion's verdict.
type NodeCriterionResult struct {
	EvidenceID string `json:"evidenceId"`
	Kind       string `json:"kind"`
	Verdict    string `json:"verdict"`
}

// NodeResultView is the node's result by kind of node.
type NodeResultView struct {
	// Kind is AGENT, COMMAND, MACHINE_GATE, APPROVAL, WAIT or STRUCTURAL.
	Kind             string                `json:"kind"`
	SelectedOutcome  string                `json:"selectedOutcome,omitempty"`
	FinalMessage     *NodeTextView         `json:"finalMessage,omitempty"`
	ChangedFiles     []NodeChangedFile     `json:"changedFiles,omitempty"`
	ChangedTruncated bool                  `json:"changedFilesTruncated,omitempty"`
	Command          *NodeCommandResult    `json:"command,omitempty"`
	Criteria         []NodeCriterionResult `json:"criteria,omitempty"`
	Usage            *UsageView            `json:"usage,omitempty"`
	DurationMillis   int64                 `json:"durationMillis,omitempty"`
	// Note is one line for nodes with no output of their own ("passed through, outcome next").
	Note string `json:"note,omitempty"`
}

// NodeProgressView is the live line of the newest attempt: how far it got and what it did last.
type NodeProgressView struct {
	AttemptID    string     `json:"attemptId"`
	AttemptState string     `json:"attemptState"`
	Running      bool       `json:"running"`
	EventCount   int        `json:"eventCount"`
	ToolCalls    int        `json:"toolCalls"`
	Messages     int        `json:"messages"`
	LastKind     string     `json:"lastKind,omitempty"`
	LastActivity string     `json:"lastActivity,omitempty"`
	LastAt       *time.Time `json:"lastAt,omitempty"`
}

// NodeRunDetail is GetNodeRunDetail's result.
type NodeRunDetail struct {
	NodeRunID          string `json:"nodeRunId"`
	RunID              string `json:"runId"`
	WorkItemID         string `json:"workItemId"`
	ProjectID          string `json:"projectId"`
	NodeKey            string `json:"nodeKey"`
	NodeType           string `json:"nodeType"`
	State              string `json:"state"`
	Iteration          uint32 `json:"iteration"`
	ActivationSequence uint64 `json:"activationSequence"`
	SelectedOutcome    string `json:"selectedOutcome,omitempty"`
	BlockReason        string `json:"blockReason,omitempty"`
	ReactivationReason string `json:"reactivationReason,omitempty"`

	Definition NodeDefinitionView   `json:"definition"`
	Attempts   []TimelineEntryView  `json:"attempts"`
	Evidence   []NodeEvidenceView   `json:"evidence"`
	Approval   *ApprovalRequestView `json:"approval,omitempty"`
	Result     NodeResultView       `json:"result"`
	Progress   *NodeProgressView    `json:"progress,omitempty"`
	Failure    *ExplainedFailure    `json:"failure,omitempty"`
}

type nodeRunBase struct {
	nodeRun   runtimedomain.NodeRun
	run       runtimedomain.WorkflowRun
	node      workflow.Node
	evidence  []runtimedomain.Evidence
	artifacts map[string]artifact.Artifact
	approval  *ApprovalRequestView
	progress  *NodeProgressView
	attempts  []runtimedomain.ExecutionAttempt
}

// GetNodeRunDetail returns the detail of nodeRunID, or ports.ErrPersistenceNotFound.
func GetNodeRunDetail(ctx context.Context, uow ports.UnitOfWork, store ports.ArtifactStore, matcher redact.Matcher, nodeRunID string) (NodeRunDetail, error) {
	var base nodeRunBase
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, nodeRunID)
		if err != nil {
			return err
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, string(nodeRun.RunID))
		if err != nil {
			return err
		}
		version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
		if err != nil {
			return err
		}
		document := version.Document()
		var node workflow.Node
		for _, candidate := range document.Nodes {
			if candidate.Key == nodeRun.NodeKey {
				node = candidate
			}
		}
		base = nodeRunBase{nodeRun: nodeRun, run: run, node: node, artifacts: map[string]artifact.Artifact{}}

		allEvidence, err := tx.Runtime().ListEvidenceForWorkItem(ctx, string(run.WorkItemID))
		if err != nil {
			return err
		}
		for _, e := range allEvidence {
			if string(e.NodeRunID) == nodeRunID {
				base.evidence = append(base.evidence, e)
			}
		}
		sort.SliceStable(base.evidence, func(i, j int) bool { return base.evidence[i].CreatedAt.Before(base.evidence[j].CreatedAt) })
		for _, e := range base.evidence {
			for _, id := range e.ArtifactReferences {
				if _, seen := base.artifacts[id]; seen {
					continue
				}
				record, err := tx.Artifacts().GetArtifact(ctx, id)
				if errors.Is(err, ports.ErrPersistenceNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				base.artifacts[id] = record
			}
		}

		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, string(run.ID))
		if err != nil {
			return err
		}
		for _, a := range attempts {
			if string(a.NodeRunID) == nodeRunID {
				base.attempts = append(base.attempts, a)
			}
		}
		sort.Slice(base.attempts, func(i, j int) bool { return base.attempts[i].AttemptNumber < base.attempts[j].AttemptNumber })

		if node.Type == workflow.NodeApproval {
			requests, err := tx.Approvals().ListApprovalRequestsForRun(ctx, string(run.ID))
			if err != nil {
				return err
			}
			nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, string(run.ID))
			if err != nil {
				return err
			}
			views := toApprovalRequestViews(requests, activationOrder(nodeRuns), document)
			for i := range views {
				if views[i].NodeRunID == nodeRunID {
					base.approval = &views[i]
				}
			}
		}

		if n := len(base.attempts); n > 0 {
			latest := base.attempts[n-1]
			records, err := tx.AgentEvents().ListByAttempt(ctx, string(latest.ID))
			if err != nil {
				return err
			}
			base.progress = progressOf(latest, records, matcher)
		}
		return nil
	}); err != nil {
		return NodeRunDetail{}, err
	}

	timeline, err := GetRunTimeline(ctx, uow, matcher, string(base.run.ID))
	if err != nil {
		return NodeRunDetail{}, err
	}
	detail := NodeRunDetail{
		NodeRunID: nodeRunID, RunID: string(base.run.ID), WorkItemID: string(base.run.WorkItemID), ProjectID: string(base.run.ProjectID),
		NodeKey: base.nodeRun.NodeKey, NodeType: string(base.node.Type), State: string(base.nodeRun.State),
		Iteration: base.nodeRun.Iteration, ActivationSequence: base.nodeRun.ActivationSequence,
		SelectedOutcome: base.nodeRun.SelectedOutcome, BlockReason: redactText(matcher, base.nodeRun.BlockReason),
		ReactivationReason: base.nodeRun.ReactivationReason,
		Definition:         definitionView(base.node), Attempts: []TimelineEntryView{}, Evidence: []NodeEvidenceView{},
		Approval: base.approval, Progress: base.progress,
	}
	for i := range timeline.Entries {
		entry := timeline.Entries[i]
		if entry.NodeRunID != nodeRunID {
			continue
		}
		if entry.Kind == TimelineEntryExecutionAttempt {
			detail.Attempts = append(detail.Attempts, entry)
		}
	}
	for _, e := range base.evidence {
		view := NodeEvidenceView{EvidenceID: string(e.ID), AttemptID: string(e.AttemptID), Kind: e.Kind, Verdict: e.Verdict, CreatedAt: e.CreatedAt, Artifacts: []NodeArtifactView{}}
		for _, id := range e.ArtifactReferences {
			if record, ok := base.artifacts[id]; ok {
				view.Artifacts = append(view.Artifacts, NodeArtifactView{ArtifactID: id, MediaType: record.MediaType, SizeBytes: record.Size})
			}
		}
		detail.Evidence = append(detail.Evidence, view)
	}
	detail.Result = resultOf(ctx, store, matcher, base, detail)
	detail.Failure = failureOf(detail)
	return detail, nil
}
