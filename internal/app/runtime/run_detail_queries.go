// Authoritative Run detail/graph/timeline read queries (V6-06B,
// docs/design/08-v6-api-projections.md V6-06B: "authoritative Run detail và
// bounded graph/timeline cho V7... manifest revision, nodes/edges/
// activations, attempt/route/retry/checkpoint, correlation/causation,
// JournalPosition/freshness"). Placed alongside queries.go (V6-07B) per this
// task's own placement instruction — same package, same "command Result
// struct with json tags lives beside the command/query that produces it"
// convention, same uow.WithReadOnly-only discipline (never
// WithSerializedWrite: every function in this file is a pure read).
//
// Three query surfaces, one per route internal/delivery/httpapi/rundetail
// (this task's own HTTP package) wraps them into:
//
//   - GetRunDetail: the Run's own row plus its immutable ExecutionManifest
//     pin and RunManifestAmendment history (GET /runs/{id}).
//   - GetRunGraph: the compiled WorkflowVersion's own declared nodes/edges
//     (the "possible" graph) plus every real NodeRun activation and
//     BranchToken this Run has ever produced (GET /runs/{id}/graph) — the
//     STRUCTURAL view. "Taken" edges (which declared edge a given
//     activation's own SelectedOutcome actually resolved to) are derived by
//     the HTTP layer from PossibleEdges + the page of Activations it is
//     about to serve, never here: which activations belong to "the current
//     page" is a pagination concern this file deliberately knows nothing
//     about (see this file's own "why no cursor/paging here" note below).
//   - GetRunTimeline: a flattened, already-ordered (by NodeRun
//     ActivationSequence, then AttemptNumber — HE-11-M03's own "correlation/
//     causation ID hoặc sequence tương đương") chronological feed mixing one
//     NODE_RUN entry per activation (carrying its own routing/rework
//     context — the "route" half of this task's own "attempt/route/retry/
//     checkpoint" line) with one EXECUTION_ATTEMPT entry per attempt it
//     produced (the "attempt/retry/checkpoint" half) — the CHRONOLOGICAL
//     view (GET /runs/{id}/timeline).
//
// Why no cursor/paging here: every query in this file returns its FULL,
// already-sorted result for the Run — mirroring
// internal/delivery/httpapi/message's own handleListMessages/appmessage.
// ListMessages split exactly (that handler's own doc comment: "not itself
// paginated at the storage layer... slices that already-bounded,
// already-sorted slice in memory rather than adding a new SQL query").
// internal/delivery/httpapi/rundetail (this task's own HTTP package) is the
// ONLY place that ever touches httpapi.CursorCodec/httpapi.Bind/
// httpapi.Freshness — this package has no dependency on internal/delivery/
// httpapi at all (would invert this codebase's own layering, delivery
// depends on app, never the reverse) and never will.
//
// Why no raw event-journal read: ports.EventsRepository is Append-only by
// design (unitofwork.go's own doc comment) and ports.CheckpointsRepository
// is likewise write-only (InsertCheckpoint has no read sibling) — this
// task's own "Không làm: raw unbounded agent-event stream" line is enforced
// by construction here: every field returned by this file comes from a real
// ports.RuntimeRepository/DefinitionsRepository accessor already scoped to
// one Run, never from domain_events/agent_events. "Correlation/causation"
// (HE-11-M03) is satisfied by NodeRun's own ActivationSequence — a
// Run-scoped, strictly-increasing counter (completion_policy.go's own
// maxActivationSequence: every new activation is "highest ActivationSequence
// among nodeRuns + 1", allocated inside the same serialized-write
// transaction that creates it) — never a cross-run global journal_position,
// which no ports.Tx accessor exposes to this package. "JournalPosition/
// freshness" in the design doc's own wire vocabulary
// (internal/delivery/httpapi/freshness.go's own Freshness.AsOfJournalPosition)
// is populated by the HTTP layer from this SAME ActivationSequence
// watermark — see that package's own doc comment for the full reasoning
// on why that reuse is correct for an endpoint that reads authoritative
// source rows directly rather than a lagging V6-08 projection.
package runtime

import (
	"context"
	"sort"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// --- RunDetail (GET /runs/{id}) ---

// ExecutionManifestDetail is the bounded, read-only view of a Run's own
// immutable ExecutionManifest pin bundle (ADR-011, GC-INV-06) — the
// "manifest revision" this task's own design-doc line names, at its
// INITIAL revision; RunDetail.Amendments carries every approved revision
// past it.
type ExecutionManifestDetail struct {
	ID                   string                      `json:"id"`
	WorkflowVersionID    string                      `json:"workflowVersionId"`
	CompiledSnapshotHash string                      `json:"compiledSnapshotHash"`
	DependencyManifest   workflow.DependencyManifest `json:"dependencyManifest"`
	BaseRevisionSet      []RevisionView              `json:"baseRevisionSet,omitempty"`
	CreatedAt            time.Time                   `json:"createdAt"`
}

func toExecutionManifestDetail(m runtimedomain.ExecutionManifest) ExecutionManifestDetail {
	detail := ExecutionManifestDetail{
		ID: string(m.ID), WorkflowVersionID: string(m.WorkflowVersionID), CompiledSnapshotHash: m.CompiledSnapshotHash,
		DependencyManifest: m.DependencyManifest, CreatedAt: m.CreatedAt,
	}
	for _, rev := range m.BaseRevisionSet.Entries() {
		detail.BaseRevisionSet = append(detail.BaseRevisionSet, RevisionView{
			RepositoryID: string(rev.RepositoryID), VCSObjectID: rev.VCSObjectID, WorkspaceGeneration: rev.WorkspaceGeneration,
		})
	}
	return detail
}

// RunManifestAmendmentView mirrors runtime.RunManifestAmendment's own
// fields exactly, with json tags — one approved scope expansion recorded
// without ever mutating the immutable ExecutionManifest above (ADR-011).
type RunManifestAmendmentView struct {
	Revision             uint64    `json:"revision"`
	PreviousRevision     uint64    `json:"previousRevision"`
	ApprovedScopeVersion uint64    `json:"approvedScopeVersion"`
	Reason               string    `json:"reason"`
	ApprovedBy           string    `json:"approvedBy"`
	ApprovedAt           time.Time `json:"approvedAt"`
	ContentHash          string    `json:"contentHash"`
}

func toRunManifestAmendmentView(a runtimedomain.RunManifestAmendment) RunManifestAmendmentView {
	return RunManifestAmendmentView{
		Revision: a.Revision, PreviousRevision: a.PreviousRevision, ApprovedScopeVersion: a.ApprovedScopeVersion,
		Reason: a.Reason, ApprovedBy: a.ApprovedBy, ApprovedAt: a.ApprovedAt, ContentHash: a.ContentHash,
	}
}

// RunDetail is GetRunDetail's own authoritative, single-resource view of one
// WorkflowRun: its own row plus its pinned ExecutionManifest and amendment
// history, plus a cheap NodeRun/ExecutionAttempt count so a caller gets an
// at-a-glance size without a second call to GetRunGraph/GetRunTimeline.
// Deliberately excludes WorkflowRun.SharedState — by construction, like
// ArtifactSummary's own "Locator never a field" discipline
// (queries.go): SharedState is arbitrary caller-declared JSON this task's
// own citations never ask this route to expose, and doing so would need its
// own redaction policy this task has no real caller to design against yet.
type RunDetail struct {
	RunID               string     `json:"runId"`
	ProjectID           string     `json:"projectId"`
	WorkItemID          string     `json:"workItemId"`
	FamilyID            string     `json:"familyId"`
	State               string     `json:"state"`
	Version             uint64     `json:"version"`
	ScopeVersion        uint64     `json:"scopeVersion"`
	WorkflowVersionID   string     `json:"workflowVersionId"`
	WorkflowVersionHash string     `json:"workflowVersionHash"`
	StartedAt           *time.Time `json:"startedAt,omitempty"`
	FinishedAt          *time.Time `json:"finishedAt,omitempty"`
	// Cancelling reports whether CancelEpoch is set (ADR-020's quiesce
	// protocol) — a bool wire projection rather than exposing the raw
	// fence epoch number itself, which no caller of this read-only detail
	// route has any use for.
	Cancelling            bool                       `json:"cancelling"`
	Manifest              ExecutionManifestDetail    `json:"manifest"`
	Amendments            []RunManifestAmendmentView `json:"amendments,omitempty"`
	NodeRunCount          int                        `json:"nodeRunCount"`
	ExecutionAttemptCount int                        `json:"executionAttemptCount"`
}

// GetRunDetail returns runID's own authoritative detail — ErrPersistenceNotFound
// for an unknown Run, propagated unchanged (mirrors
// internal/delivery/httpapi/run's own requireRunExists: a Run either exists,
// with its own real ProjectID, or this whole call fails closed).
func GetRunDetail(ctx context.Context, uow ports.UnitOfWork, runID string) (RunDetail, error) {
	var detail RunDetail
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		manifest, err := tx.Runtime().GetExecutionManifest(ctx, runID)
		if err != nil {
			return err
		}
		amendments, err := tx.Runtime().ListRunManifestAmendments(ctx, runID)
		if err != nil {
			return err
		}
		nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, runID)
		if err != nil {
			return err
		}
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}

		amendmentViews := make([]RunManifestAmendmentView, 0, len(amendments))
		for _, a := range amendments {
			amendmentViews = append(amendmentViews, toRunManifestAmendmentView(a))
		}
		detail = RunDetail{
			RunID: string(run.ID), ProjectID: string(run.ProjectID), WorkItemID: string(run.WorkItemID),
			FamilyID: string(run.FamilyID), State: string(run.State), Version: run.Version, ScopeVersion: run.ScopeVersion,
			WorkflowVersionID: string(run.WorkflowVersionID), WorkflowVersionHash: run.WorkflowVersionHash,
			StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Cancelling: run.CancelEpoch != nil,
			Manifest: toExecutionManifestDetail(manifest), Amendments: amendmentViews,
			NodeRunCount: len(nodeRuns), ExecutionAttemptCount: len(attempts),
		}
		return nil
	})
	return detail, err
}

// --- RunGraph (GET /runs/{id}/graph) ---

// GraphNodeView mirrors workflow.Node's own structural fields (Key/Type/
// Outcomes/CyclePolicy) — deliberately NOT the full node (Agent/Command/
// MachineGate/Approval/Wait/Join typed executor configs are omitted by
// construction): this route's own "nodes/edges" scope is the graph's
// SHAPE, not a second surface for definition-authoring detail V6-05's own
// routes already own.
type GraphNodeView struct {
	Key         string                `json:"key"`
	Type        string                `json:"type"`
	Outcomes    []string              `json:"outcomes,omitempty"`
	CyclePolicy *workflow.CyclePolicy `json:"cyclePolicy,omitempty"`
}

func toGraphNodeViews(nodes []workflow.Node) []GraphNodeView {
	out := make([]GraphNodeView, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, GraphNodeView{
			Key: n.Key, Type: string(n.Type), Outcomes: append([]string(nil), n.Outcomes...), CyclePolicy: n.CyclePolicy,
		})
	}
	return out
}

// GraphEdgeView mirrors workflow.Edge's own fields exactly — the compiled
// WorkflowVersion's own declared "possible" edges, FLOW and
// COMPLETION_REWORK alike (Kind distinguishes them; a caller that only
// wants the scheduler-traversed subset filters on Kind=="" /"FLOW"
// client-side, the same "classification is the caller's own job"
// discipline ListNodeRunsForRun's own doc comment already establishes for
// NodeRun state).
type GraphEdgeView struct {
	Key          string                 `json:"key"`
	From         string                 `json:"from"`
	Outcome      string                 `json:"outcome"`
	To           string                 `json:"to"`
	Kind         string                 `json:"kind,omitempty"`
	ReworkPolicy *workflow.ReworkPolicy `json:"reworkPolicy,omitempty"`
}

func toGraphEdgeViews(edges []workflow.Edge) []GraphEdgeView {
	out := make([]GraphEdgeView, 0, len(edges))
	for _, e := range edges {
		out = append(out, GraphEdgeView{Key: e.Key, From: e.From, Outcome: e.Outcome, To: e.To, Kind: string(e.Kind), ReworkPolicy: e.ReworkPolicy})
	}
	return out
}

// BranchTokenView mirrors runtime.BranchToken's own fields exactly — the
// fork/join topology overlay this task's own "fork/join/rework fixtures"
// Verify bullet needs a real surface for. Always the FULL set for a Run
// (never itself paginated): mirrors ListBranchTokensForRun's own doc
// comment reasoning ("a real, direct column filter... small row set") —
// the number of FORK branches any real workflow declares is bounded by the
// document's own authored width, not by how long a Run has been running.
type BranchTokenView struct {
	BranchTokenID  string `json:"branchTokenId"`
	ForkNodeRunID  string `json:"forkNodeRunId"`
	ForkKey        string `json:"forkKey"`
	BranchKey      string `json:"branchKey"`
	CurrentNodeKey string `json:"currentNodeKey"`
	State          string `json:"state"`
}

func toBranchTokenViews(tokens []runtimedomain.BranchToken) []BranchTokenView {
	sorted := append([]runtimedomain.BranchToken(nil), tokens...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ForkNodeRunID != sorted[j].ForkNodeRunID {
			return sorted[i].ForkNodeRunID < sorted[j].ForkNodeRunID
		}
		return sorted[i].BranchKey < sorted[j].BranchKey
	})
	out := make([]BranchTokenView, 0, len(sorted))
	for _, tok := range sorted {
		out = append(out, BranchTokenView{
			BranchTokenID: string(tok.ID), ForkNodeRunID: string(tok.ForkNodeRunID), ForkKey: tok.ForkKey,
			BranchKey: tok.BranchKey, CurrentNodeKey: tok.CurrentNodeKey, State: string(tok.State),
		})
	}
	return out
}

// NodeActivationView mirrors runtime.NodeRun's own fields exactly — one
// entry per real activation this Run has ever had, across every node key
// (the "activations" this task's own design-doc line names). BlockReason
// is redacted through the caller-supplied redact.Matcher before ever
// reaching this DTO (AK-ARCH-024: "Secret fixture bị redact khỏi event,
// log tìm kiếm, conversation và retained artifact") — the one free-text
// field NodeRun carries; every other field is either an ID, a closed enum,
// or a number.
//
// KNOWN GAP (found while writing this task's own redaction test, confirmed
// by grep — no occurrence of "block_reason" anywhere under
// internal/adapters/sqlite): NodeRun.BlockReason has no durable column in
// the sqlite schema today, so a real production read of a BLOCKED NodeRun
// always observes "" here regardless of what was actually set at block
// time — the field, and this file's own redaction of it, are both wired
// correctly end to end and will start surfacing real content the moment a
// future task adds the missing column; this task does not add it (adding a
// migration/column was outside this task's own scope, confirmed with the
// user's own "This task likely needs NO new migration" brief).
type NodeActivationView struct {
	NodeRunID          string `json:"nodeRunId"`
	NodeKey            string `json:"nodeKey"`
	ActivationSequence uint64 `json:"activationSequence"`
	Iteration          uint32 `json:"iteration"`
	State              string `json:"state"`
	SelectedOutcome    string `json:"selectedOutcome,omitempty"`
	ManifestRevision   uint64 `json:"manifestRevision,omitempty"`
	BranchTokenID      string `json:"branchTokenId,omitempty"`
	ReactivationReason string `json:"reactivationReason,omitempty"`
	BlockReason        string `json:"blockReason,omitempty"`
}

func toNodeActivationView(nr runtimedomain.NodeRun, matcher redact.Matcher) NodeActivationView {
	view := NodeActivationView{
		NodeRunID: string(nr.ID), NodeKey: nr.NodeKey, ActivationSequence: nr.ActivationSequence,
		Iteration: nr.Iteration, State: string(nr.State), SelectedOutcome: nr.SelectedOutcome,
		ManifestRevision: nr.ManifestRevision, ReactivationReason: nr.ReactivationReason,
		BlockReason: matcher.String(nr.BlockReason),
	}
	if nr.BranchTokenID != nil {
		view.BranchTokenID = string(*nr.BranchTokenID)
	}
	return view
}

// sortedNodeRuns orders nodeRuns by ActivationSequence, breaking a tie by
// NodeRunID for a deterministic TOTAL order. Ties are a real, expected
// occurrence for this codebase's fork/join topology, not a bug to guard
// against: dispatchForkBranches (advance.go) assigns each branch's own
// first NodeRun a CONSECUTIVE sequence starting from the FORK's own
// ActivationSequence (forkRun.ActivationSequence, incremented once per
// branch), while a JOIN's own NodeRun — created independently, on
// whichever branch arrives FIRST — is pinned to forkRun.ActivationSequence+1
// (fixed relative to the FORK, never to "whatever the run-wide max
// happens to be at arrival time," so JOIN's own position is deterministic
// regardless of branch completion order). A JOIN can therefore legitimately
// share its own ActivationSequence with one branch's own first step, and
// the node downstream of JOIN can legitimately share ITS OWN sequence with
// another, later-created branch step. ActivationSequence is this Run's own
// causation/sequence backbone (HE-11-M03), not a claim that every
// activation ever gets a globally-unique position.
func sortedNodeRuns(nodeRuns []runtimedomain.NodeRun) []runtimedomain.NodeRun {
	sorted := append([]runtimedomain.NodeRun(nil), nodeRuns...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ActivationSequence != sorted[j].ActivationSequence {
			return sorted[i].ActivationSequence < sorted[j].ActivationSequence
		}
		return sorted[i].ID < sorted[j].ID
	})
	return sorted
}

// RunGraph is GetRunGraph's own bounded structural view: the compiled
// WorkflowVersion's own declared nodes/edges (Nodes/PossibleEdges — the
// "possible" graph), plus the FULL, ActivationSequence-ordered set of real
// NodeRun activations and BranchTokens this Run has ever produced.
// Activations is deliberately the COMPLETE set, never paginated here — see
// this file's own package doc comment for why pagination is entirely
// internal/delivery/httpapi/rundetail's own concern, mirroring
// internal/delivery/httpapi/message's identical split.
type RunGraph struct {
	RunID            string               `json:"runId"`
	ManifestRevision uint64               `json:"manifestRevision"`
	Nodes            []GraphNodeView      `json:"nodes"`
	PossibleEdges    []GraphEdgeView      `json:"possibleEdges"`
	Activations      []NodeActivationView `json:"activations"`
	BranchTokens     []BranchTokenView    `json:"branchTokens,omitempty"`
}

// GetRunGraph returns runID's own full structural graph. matcher redacts
// every NodeActivationView.BlockReason before it ever leaves this
// function — see NodeActivationView's own doc comment.
func GetRunGraph(ctx context.Context, uow ports.UnitOfWork, matcher redact.Matcher, runID string) (RunGraph, error) {
	var graph RunGraph
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if _, err := tx.Runtime().GetWorkflowRun(ctx, runID); err != nil {
			return err
		}
		manifest, err := tx.Runtime().GetExecutionManifest(ctx, runID)
		if err != nil {
			return err
		}
		version, err := tx.Definitions().GetWorkflowVersion(ctx, string(manifest.WorkflowVersionID))
		if err != nil {
			return err
		}
		amendments, err := tx.Runtime().ListRunManifestAmendments(ctx, runID)
		if err != nil {
			return err
		}
		nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, runID)
		if err != nil {
			return err
		}
		branchTokens, err := tx.Runtime().ListBranchTokensForRun(ctx, runID)
		if err != nil {
			return err
		}

		doc := version.Document()
		sorted := sortedNodeRuns(nodeRuns)
		activations := make([]NodeActivationView, 0, len(sorted))
		for _, nr := range sorted {
			activations = append(activations, toNodeActivationView(nr, matcher))
		}
		var maxRevision uint64
		for _, a := range amendments {
			if a.Revision > maxRevision {
				maxRevision = a.Revision
			}
		}
		graph = RunGraph{
			RunID: runID, ManifestRevision: maxRevision,
			Nodes: toGraphNodeViews(doc.Nodes), PossibleEdges: toGraphEdgeViews(doc.Edges),
			Activations: activations, BranchTokens: toBranchTokenViews(branchTokens),
		}
		return nil
	})
	return graph, err
}

// --- RunTimeline (GET /runs/{id}/timeline) ---

// TimelineEntryKind discriminates RunTimeline's own two entry shapes — a
// closed, tagged-union wire vocabulary rather than two separate response
// arrays, so a client renders one single chronologically-ordered feed.
type TimelineEntryKind string

const (
	// TimelineEntryNodeRun is one NodeRun activation — carries the "route"
	// half of this task's own "attempt/route/retry/checkpoint" line
	// (SelectedOutcome), and is the ONLY entry kind a purely structural
	// node (START/END/ROUTER/FORK/JOIN, which never dispatches an
	// ExecutionAttempt) ever produces.
	TimelineEntryNodeRun TimelineEntryKind = "NODE_RUN"
	// TimelineEntryExecutionAttempt is one ExecutionAttempt — carries the
	// "attempt/retry/checkpoint" half; AttemptNumber > 1 IS the retry
	// history for its own owning NodeRun.
	TimelineEntryExecutionAttempt TimelineEntryKind = "EXECUTION_ATTEMPT"
)

// TimelineEntryView is one chronologically-ordered timeline entry. Every
// entry carries its own owning NodeRun's context inline (NodeRunID/NodeKey/
// Iteration/ActivationSequence) — denormalized so a client never needs a
// second call to correlate an EXECUTION_ATTEMPT entry back to its own node
// (HE-11-M03's own "correlation/causation ID hoặc sequence tương đương",
// satisfied structurally: ActivationSequence IS that sequence). Fields
// below the blank-line group are populated only when
// Kind==EXECUTION_ATTEMPT.
type TimelineEntryView struct {
	Kind               TimelineEntryKind `json:"kind"`
	ActivationSequence uint64            `json:"activationSequence"`
	NodeRunID          string            `json:"nodeRunId"`
	NodeKey            string            `json:"nodeKey"`
	Iteration          uint32            `json:"iteration"`
	NodeState          string            `json:"nodeState,omitempty"`
	SelectedOutcome    string            `json:"selectedOutcome,omitempty"`
	BranchTokenID      string            `json:"branchTokenId,omitempty"`
	ReactivationReason string            `json:"reactivationReason,omitempty"`
	BlockReason        string            `json:"blockReason,omitempty"`

	AttemptID         string     `json:"attemptId,omitempty"`
	AttemptNumber     uint32     `json:"attemptNumber,omitempty"`
	AttemptState      string     `json:"attemptState,omitempty"`
	ProviderKey       string     `json:"providerKey,omitempty"`
	StartedAt         *time.Time `json:"startedAt,omitempty"`
	FinishedAt        *time.Time `json:"finishedAt,omitempty"`
	TerminationReason string     `json:"terminationReason,omitempty"`
	FailureCode       string     `json:"failureCode,omitempty"`
	LastCheckpointID  string     `json:"lastCheckpointId,omitempty"`
	ContextSnapshotID string     `json:"contextSnapshotId,omitempty"`
}

func nodeRunToTimelineEntry(nr runtimedomain.NodeRun, matcher redact.Matcher) TimelineEntryView {
	activation := toNodeActivationView(nr, matcher)
	return TimelineEntryView{
		Kind: TimelineEntryNodeRun, ActivationSequence: nr.ActivationSequence,
		NodeRunID: activation.NodeRunID, NodeKey: activation.NodeKey, Iteration: activation.Iteration,
		NodeState: activation.State, SelectedOutcome: activation.SelectedOutcome,
		BranchTokenID: activation.BranchTokenID, ReactivationReason: activation.ReactivationReason,
		BlockReason: activation.BlockReason,
	}
}

func attemptToTimelineEntry(nr runtimedomain.NodeRun, a runtimedomain.ExecutionAttempt) TimelineEntryView {
	entry := TimelineEntryView{
		Kind: TimelineEntryExecutionAttempt, ActivationSequence: nr.ActivationSequence,
		NodeRunID: string(nr.ID), NodeKey: nr.NodeKey, Iteration: nr.Iteration,
		AttemptID: string(a.ID), AttemptNumber: a.AttemptNumber, AttemptState: string(a.State),
		ProviderKey: a.ProviderKey, StartedAt: a.StartedAt, FinishedAt: a.FinishedAt,
		TerminationReason: string(a.TerminationReason), FailureCode: string(a.FailureCode),
	}
	if a.LastCheckpointID != nil {
		entry.LastCheckpointID = string(*a.LastCheckpointID)
	}
	if a.ContextSnapshotID != nil {
		entry.ContextSnapshotID = string(*a.ContextSnapshotID)
	}
	return entry
}

// buildTimelineEntries flattens nodeRuns (one NODE_RUN entry each) and
// their own attempts (one EXECUTION_ATTEMPT entry each, nested
// immediately after their owning NodeRun's own entry) into one list
// ordered by (ActivationSequence, then AttemptNumber — 0 for the NODE_RUN
// entry itself, so it always sorts before any of its own attempts).
func buildTimelineEntries(nodeRuns []runtimedomain.NodeRun, attempts []runtimedomain.ExecutionAttempt, matcher redact.Matcher) []TimelineEntryView {
	byNodeRun := make(map[runtimedomain.NodeRunID][]runtimedomain.ExecutionAttempt, len(attempts))
	for _, a := range attempts {
		byNodeRun[a.NodeRunID] = append(byNodeRun[a.NodeRunID], a)
	}
	sorted := sortedNodeRuns(nodeRuns)
	entries := make([]TimelineEntryView, 0, len(sorted)+len(attempts))
	for _, nr := range sorted {
		entries = append(entries, nodeRunToTimelineEntry(nr, matcher))
		nodeAttempts := append([]runtimedomain.ExecutionAttempt(nil), byNodeRun[nr.ID]...)
		sort.Slice(nodeAttempts, func(i, j int) bool { return nodeAttempts[i].AttemptNumber < nodeAttempts[j].AttemptNumber })
		for _, a := range nodeAttempts {
			entries = append(entries, attemptToTimelineEntry(nr, a))
		}
	}
	return entries
}

// RunTimeline is GetRunTimeline's own bounded chronological view — the
// FULL, already-ordered entry list for the Run (see this file's own
// package doc comment for why pagination stays entirely
// internal/delivery/httpapi/rundetail's own concern).
type RunTimeline struct {
	RunID   string              `json:"runId"`
	Entries []TimelineEntryView `json:"entries"`
}

// GetRunTimeline returns runID's own full chronological timeline. matcher
// redacts every NODE_RUN entry's BlockReason-derived content exactly like
// GetRunGraph (toNodeActivationView is the shared conversion both use).
func GetRunTimeline(ctx context.Context, uow ports.UnitOfWork, matcher redact.Matcher, runID string) (RunTimeline, error) {
	var timeline RunTimeline
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if _, err := tx.Runtime().GetWorkflowRun(ctx, runID); err != nil {
			return err
		}
		nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, runID)
		if err != nil {
			return err
		}
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		timeline = RunTimeline{RunID: runID, Entries: buildTimelineEntries(nodeRuns, attempts, matcher)}
		return nil
	})
	return timeline, err
}
