package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// RuntimeRepository is an in-memory ports.RuntimeRepository — V4-01 gives
// this concern its first real behavior, so a later V4 command handler can
// be tested without sqlite (the same V1-05 discipline every other fake
// repository here already follows). workflowRuns/nodeRuns are populated now
// (V4-02, StartWorkflowRun's own first real caller for both) — before that,
// this fake did not cross-check RunID/WorkItemID against any other fake
// store, since no fake WorkflowRun/WorkItem persistence existed yet; the
// rest of this type's idempotency/immutability contracts remain
// self-consistent only, exactly what a caller can observe through
// ports.RuntimeRepository's own methods.
type RuntimeRepository struct {
	workflowRuns map[string]runtime.WorkflowRun                // by ID
	nodeRuns     map[string]runtime.NodeRun                    // by ID
	attempts     map[string]runtime.ExecutionAttempt           // by ID
	manifests    map[string]runtime.ExecutionManifest          // by RunID
	amendments   map[string][]runtime.RunManifestAmendment     // by RunID, Revision order
	branches     map[string]runtime.BranchToken                // by RunID+"\x00"+ForkKey+"\x00"+BranchKey
	decisions    map[string]runtime.DecisionArtifact           // by ID
	runIntents   map[string]runtime.RunCancellationIntent      // by RunID
	workIntents  map[string]runtime.WorkItemCancellationIntent // by WorkItemID
}

var _ ports.RuntimeRepository = (*RuntimeRepository)(nil)

func (r *RuntimeRepository) clone() *RuntimeRepository {
	workflowRuns := make(map[string]runtime.WorkflowRun, len(r.workflowRuns))
	for k, v := range r.workflowRuns {
		workflowRuns[k] = v
	}
	nodeRuns := make(map[string]runtime.NodeRun, len(r.nodeRuns))
	for k, v := range r.nodeRuns {
		nodeRuns[k] = v
	}
	attempts := make(map[string]runtime.ExecutionAttempt, len(r.attempts))
	for k, v := range r.attempts {
		attempts[k] = v
	}
	manifests := make(map[string]runtime.ExecutionManifest, len(r.manifests))
	for k, v := range r.manifests {
		manifests[k] = v
	}
	amendments := make(map[string][]runtime.RunManifestAmendment, len(r.amendments))
	for k, v := range r.amendments {
		amendments[k] = append([]runtime.RunManifestAmendment(nil), v...)
	}
	branches := make(map[string]runtime.BranchToken, len(r.branches))
	for k, v := range r.branches {
		branches[k] = v
	}
	decisions := make(map[string]runtime.DecisionArtifact, len(r.decisions))
	for k, v := range r.decisions {
		decisions[k] = v
	}
	runIntents := make(map[string]runtime.RunCancellationIntent, len(r.runIntents))
	for k, v := range r.runIntents {
		runIntents[k] = v
	}
	workIntents := make(map[string]runtime.WorkItemCancellationIntent, len(r.workIntents))
	for k, v := range r.workIntents {
		workIntents[k] = v
	}
	return &RuntimeRepository{
		workflowRuns: workflowRuns, nodeRuns: nodeRuns, attempts: attempts, manifests: manifests, amendments: amendments,
		branches: branches, decisions: decisions, runIntents: runIntents, workIntents: workIntents,
	}
}

func (r *RuntimeRepository) CreateWorkflowRun(_ context.Context, run runtime.WorkflowRun) (runtime.WorkflowRun, error) {
	key := string(run.ID)
	if _, exists := r.workflowRuns[key]; exists {
		return runtime.WorkflowRun{}, fmt.Errorf("fake: %w: workflow run %s", ports.ErrPersistenceAlreadyExists, key)
	}
	if r.workflowRuns == nil {
		r.workflowRuns = map[string]runtime.WorkflowRun{}
	}
	r.workflowRuns[key] = run
	return run, nil
}

func (r *RuntimeRepository) CreateNodeRun(_ context.Context, nodeRun runtime.NodeRun) (runtime.NodeRun, error) {
	if _, ok := r.workflowRuns[string(nodeRun.RunID)]; !ok {
		return runtime.NodeRun{}, fmt.Errorf("fake: %w: workflow run %s", ports.ErrPersistenceNotFound, nodeRun.RunID)
	}
	key := string(nodeRun.ID)
	if _, exists := r.nodeRuns[key]; exists {
		return runtime.NodeRun{}, fmt.Errorf("fake: %w: node run %s", ports.ErrPersistenceAlreadyExists, key)
	}
	if r.nodeRuns == nil {
		r.nodeRuns = map[string]runtime.NodeRun{}
	}
	r.nodeRuns[key] = nodeRun
	return nodeRun, nil
}

// GetWorkflowRun mirrors sqlite's GetWorkflowRun (V4-03).
func (r *RuntimeRepository) GetWorkflowRun(_ context.Context, id string) (runtime.WorkflowRun, error) {
	run, ok := r.workflowRuns[id]
	if !ok {
		return runtime.WorkflowRun{}, fmt.Errorf("fake: %w: workflow run %s", ports.ErrPersistenceNotFound, id)
	}
	return run, nil
}

// GetNodeRun mirrors sqlite's GetNodeRun (V4-03).
func (r *RuntimeRepository) GetNodeRun(_ context.Context, id string) (runtime.NodeRun, error) {
	nodeRun, ok := r.nodeRuns[id]
	if !ok {
		return runtime.NodeRun{}, fmt.Errorf("fake: %w: node run %s", ports.ErrPersistenceNotFound, id)
	}
	return nodeRun, nil
}

// TransitionNodeRun mirrors sqlite's transitionNodeRunTx (V4-03): a stale
// caller (wrong ExpectedState/ExpectedVersion) gets ErrOptimisticConflict,
// never a silent overwrite.
func (r *RuntimeRepository) TransitionNodeRun(_ context.Context, req ports.TransitionNodeRunRequest) (runtime.NodeRun, error) {
	nodeRun, ok := r.nodeRuns[req.NodeRunID]
	if !ok {
		return runtime.NodeRun{}, fmt.Errorf("fake: %w: node run %s", ports.ErrPersistenceNotFound, req.NodeRunID)
	}
	if nodeRun.State != req.ExpectedState || nodeRun.Version != req.ExpectedVersion {
		return runtime.NodeRun{}, fmt.Errorf(
			"fake: %w: node run %s expected %s@%d",
			ports.ErrOptimisticConflict, req.NodeRunID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	nodeRun.State = req.NextState
	if req.SelectedOutcome != "" {
		nodeRun.SelectedOutcome = req.SelectedOutcome
	}
	nodeRun.Version++
	r.nodeRuns[req.NodeRunID] = nodeRun
	return nodeRun, nil
}

// UpdateWorkflowRunSharedState mirrors sqlite's updateWorkflowRunSharedStateTx
// (V4-03 correction): a stale ExpectedVersion gets ErrOptimisticConflict,
// never a silent overwrite.
func (r *RuntimeRepository) UpdateWorkflowRunSharedState(_ context.Context, req ports.UpdateWorkflowRunSharedStateRequest) (runtime.WorkflowRun, error) {
	run, ok := r.workflowRuns[req.RunID]
	if !ok {
		return runtime.WorkflowRun{}, fmt.Errorf("fake: %w: workflow run %s", ports.ErrPersistenceNotFound, req.RunID)
	}
	if run.Version != req.ExpectedVersion {
		return runtime.WorkflowRun{}, fmt.Errorf(
			"fake: %w: workflow run %s expected version %d",
			ports.ErrOptimisticConflict, req.RunID, req.ExpectedVersion,
		)
	}
	sharedState := req.SharedState
	if len(sharedState) == 0 {
		sharedState = json.RawMessage(`{}`)
	}
	run.SharedState = append(json.RawMessage(nil), sharedState...)
	run.Version++
	r.workflowRuns[req.RunID] = run
	return run, nil
}

// ScheduleNodeRun mirrors sqlite's scheduleNodeRunTx (V4-04): PENDING->QUEUED
// CAS, pinning EffectiveScope/ExecutionProfileHash/ManifestRevision together.
func (r *RuntimeRepository) ScheduleNodeRun(_ context.Context, req ports.ScheduleNodeRunRequest) (runtime.NodeRun, error) {
	nodeRun, ok := r.nodeRuns[req.NodeRunID]
	if !ok {
		return runtime.NodeRun{}, fmt.Errorf("fake: %w: node run %s", ports.ErrPersistenceNotFound, req.NodeRunID)
	}
	if nodeRun.State != runtime.NodeRunPending || nodeRun.Version != req.ExpectedVersion {
		return runtime.NodeRun{}, fmt.Errorf(
			"fake: %w: node run %s expected PENDING@%d", ports.ErrOptimisticConflict, req.NodeRunID, req.ExpectedVersion,
		)
	}
	nodeRun.State = runtime.NodeRunQueued
	nodeRun.EffectiveScope = append([]work.RepositoryScope(nil), req.EffectiveScope...)
	nodeRun.ExecutionProfileHash = req.ExecutionProfileHash
	nodeRun.ManifestRevision = req.ManifestRevision
	nodeRun.Version++
	r.nodeRuns[req.NodeRunID] = nodeRun
	return nodeRun, nil
}

// CreateExecutionAttempt mirrors sqlite's createExecutionAttemptTx (V4-04).
func (r *RuntimeRepository) CreateExecutionAttempt(_ context.Context, attempt runtime.ExecutionAttempt) (runtime.ExecutionAttempt, error) {
	if _, ok := r.nodeRuns[string(attempt.NodeRunID)]; !ok {
		return runtime.ExecutionAttempt{}, fmt.Errorf("fake: %w: node run %s", ports.ErrPersistenceNotFound, attempt.NodeRunID)
	}
	key := string(attempt.ID)
	if _, exists := r.attempts[key]; exists {
		return runtime.ExecutionAttempt{}, fmt.Errorf("fake: %w: execution attempt %s", ports.ErrPersistenceAlreadyExists, key)
	}
	if r.attempts == nil {
		r.attempts = map[string]runtime.ExecutionAttempt{}
	}
	r.attempts[key] = attempt
	return attempt, nil
}

// Attempts returns every ExecutionAttempt this fake has recorded, keyed by
// ID — test-only introspection, mirroring JobsRepository.Items()/
// EventsRepository.Items().
func (r *RuntimeRepository) Attempts() map[string]runtime.ExecutionAttempt {
	items := make(map[string]runtime.ExecutionAttempt, len(r.attempts))
	for k, v := range r.attempts {
		items[k] = v
	}
	return items
}

func sameExecutionManifestContent(left, right runtime.ExecutionManifest) bool {
	leftManifest, errLeft := json.Marshal(left.DependencyManifest)
	rightManifest, errRight := json.Marshal(right.DependencyManifest)
	leftRevisions, errLeftRevisions := json.Marshal(left.BaseRevisionSet.Entries())
	rightRevisions, errRightRevisions := json.Marshal(right.BaseRevisionSet.Entries())
	return errLeft == nil && errRight == nil && errLeftRevisions == nil && errRightRevisions == nil &&
		left.WorkflowVersionID == right.WorkflowVersionID &&
		left.CompiledSnapshotHash == right.CompiledSnapshotHash &&
		string(leftManifest) == string(rightManifest) &&
		string(leftRevisions) == string(rightRevisions)
}

func (r *RuntimeRepository) CreateExecutionManifest(_ context.Context, manifest runtime.ExecutionManifest) (runtime.ExecutionManifest, error) {
	key := string(manifest.RunID)
	if existing, ok := r.manifests[key]; ok {
		if sameExecutionManifestContent(existing, manifest) {
			return existing, nil
		}
		return runtime.ExecutionManifest{}, fmt.Errorf("fake: %w: execution manifest for run %s", ports.ErrImmutableVersionConflict, key)
	}
	if r.manifests == nil {
		r.manifests = map[string]runtime.ExecutionManifest{}
	}
	r.manifests[key] = manifest
	return manifest, nil
}

func (r *RuntimeRepository) GetExecutionManifest(_ context.Context, runID string) (runtime.ExecutionManifest, error) {
	manifest, ok := r.manifests[runID]
	if !ok {
		return runtime.ExecutionManifest{}, fmt.Errorf("fake: %w: execution manifest for run %s", ports.ErrPersistenceNotFound, runID)
	}
	return manifest, nil
}

func (r *RuntimeRepository) AppendRunManifestAmendment(_ context.Context, amendment runtime.RunManifestAmendment) (runtime.RunManifestAmendment, error) {
	key := string(amendment.RunID)
	existing := r.amendments[key]
	var highest uint64
	if len(existing) > 0 {
		highest = existing[len(existing)-1].Revision
	}
	if amendment.PreviousRevision != highest {
		return runtime.RunManifestAmendment{}, fmt.Errorf(
			"fake: %w: run %s expected previous revision %d, got %d",
			ports.ErrOptimisticConflict, key, highest, amendment.PreviousRevision,
		)
	}
	if r.amendments == nil {
		r.amendments = map[string][]runtime.RunManifestAmendment{}
	}
	r.amendments[key] = append(r.amendments[key], amendment)
	return amendment, nil
}

func (r *RuntimeRepository) ListRunManifestAmendments(_ context.Context, runID string) ([]runtime.RunManifestAmendment, error) {
	return append([]runtime.RunManifestAmendment(nil), r.amendments[runID]...), nil
}

func branchTokenKey(runID, forkKey, branchKey string) string {
	return runID + "\x00" + forkKey + "\x00" + branchKey
}

func (r *RuntimeRepository) CreateBranchToken(_ context.Context, token runtime.BranchToken) (runtime.BranchToken, error) {
	key := branchTokenKey(string(token.RunID), token.ForkKey, token.BranchKey)
	if existing, ok := r.branches[key]; ok {
		if existing.CurrentNodeKey == token.CurrentNodeKey {
			return existing, nil
		}
		return runtime.BranchToken{}, fmt.Errorf("fake: %w: branch token %s", ports.ErrOptimisticConflict, key)
	}
	if r.branches == nil {
		r.branches = map[string]runtime.BranchToken{}
	}
	r.branches[key] = token
	return token, nil
}

func (r *RuntimeRepository) GetBranchToken(_ context.Context, runID, forkKey, branchKey string) (runtime.BranchToken, error) {
	token, ok := r.branches[branchTokenKey(runID, forkKey, branchKey)]
	if !ok {
		return runtime.BranchToken{}, fmt.Errorf("fake: %w: branch token %s/%s/%s", ports.ErrPersistenceNotFound, runID, forkKey, branchKey)
	}
	return token, nil
}

func (r *RuntimeRepository) ListBranchTokensForRun(_ context.Context, runID string) ([]runtime.BranchToken, error) {
	var tokens []runtime.BranchToken
	for _, token := range r.branches {
		if string(token.RunID) == runID {
			tokens = append(tokens, token)
		}
	}
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].ForkKey != tokens[j].ForkKey {
			return tokens[i].ForkKey < tokens[j].ForkKey
		}
		return tokens[i].BranchKey < tokens[j].BranchKey
	})
	return tokens, nil
}

func (r *RuntimeRepository) RecordDecisionArtifact(_ context.Context, artifact runtime.DecisionArtifact) (runtime.DecisionArtifact, error) {
	key := string(artifact.ID)
	if _, exists := r.decisions[key]; exists {
		return runtime.DecisionArtifact{}, fmt.Errorf("fake: %w: decision artifact %s", ports.ErrPersistenceAlreadyExists, key)
	}
	if r.decisions == nil {
		r.decisions = map[string]runtime.DecisionArtifact{}
	}
	r.decisions[key] = artifact
	return artifact, nil
}

func (r *RuntimeRepository) GetDecisionArtifact(_ context.Context, id string) (runtime.DecisionArtifact, error) {
	artifact, ok := r.decisions[id]
	if !ok {
		return runtime.DecisionArtifact{}, fmt.Errorf("fake: %w: decision artifact %s", ports.ErrPersistenceNotFound, id)
	}
	return artifact, nil
}

// Decisions returns every DecisionArtifact this fake has recorded, keyed by
// ID — test-only introspection, mirroring Attempts()/JobsRepository.Items().
func (r *RuntimeRepository) Decisions() map[string]runtime.DecisionArtifact {
	items := make(map[string]runtime.DecisionArtifact, len(r.decisions))
	for k, v := range r.decisions {
		items[k] = v
	}
	return items
}

func (r *RuntimeRepository) RecordRunCancellationIntent(_ context.Context, intent runtime.RunCancellationIntent) (runtime.RunCancellationIntent, error) {
	key := string(intent.RunID)
	if existing, ok := r.runIntents[key]; ok {
		return existing, nil
	}
	if r.runIntents == nil {
		r.runIntents = map[string]runtime.RunCancellationIntent{}
	}
	r.runIntents[key] = intent
	return intent, nil
}

func (r *RuntimeRepository) GetRunCancellationIntent(_ context.Context, runID string) (runtime.RunCancellationIntent, error) {
	intent, ok := r.runIntents[runID]
	if !ok {
		return runtime.RunCancellationIntent{}, fmt.Errorf("fake: %w: run cancellation intent for run %s", ports.ErrPersistenceNotFound, runID)
	}
	return intent, nil
}

func (r *RuntimeRepository) RecordWorkItemCancellationIntent(_ context.Context, intent runtime.WorkItemCancellationIntent) (runtime.WorkItemCancellationIntent, error) {
	key := string(intent.WorkItemID)
	if existing, ok := r.workIntents[key]; ok {
		return existing, nil
	}
	if r.workIntents == nil {
		r.workIntents = map[string]runtime.WorkItemCancellationIntent{}
	}
	r.workIntents[key] = intent
	return intent, nil
}

func (r *RuntimeRepository) GetWorkItemCancellationIntent(_ context.Context, workItemID string) (runtime.WorkItemCancellationIntent, error) {
	intent, ok := r.workIntents[workItemID]
	if !ok {
		return runtime.WorkItemCancellationIntent{}, fmt.Errorf("fake: %w: work item cancellation intent for work item %s", ports.ErrPersistenceNotFound, workItemID)
	}
	return intent, nil
}
