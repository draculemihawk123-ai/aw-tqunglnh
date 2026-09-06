package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

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
	scopeOrigins map[string]runtime.ScopeExpansionOrigin       // by AttemptID
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
	scopeOrigins := make(map[string]runtime.ScopeExpansionOrigin, len(r.scopeOrigins))
	for k, v := range r.scopeOrigins {
		scopeOrigins[k] = v
	}
	return &RuntimeRepository{
		workflowRuns: workflowRuns, nodeRuns: nodeRuns, attempts: attempts, manifests: manifests, amendments: amendments,
		branches: branches, decisions: decisions, runIntents: runIntents, workIntents: workIntents, scopeOrigins: scopeOrigins,
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

// ListNodeRunsForRun mirrors sqlite's ListNodeRunsForRun (V4-12), ordered
// by ActivationSequence for deterministic output.
func (r *RuntimeRepository) ListNodeRunsForRun(_ context.Context, runID string) ([]runtime.NodeRun, error) {
	var nodeRuns []runtime.NodeRun
	for _, nodeRun := range r.nodeRuns {
		if string(nodeRun.RunID) == runID {
			nodeRuns = append(nodeRuns, nodeRun)
		}
	}
	sort.Slice(nodeRuns, func(i, j int) bool { return nodeRuns[i].ActivationSequence < nodeRuns[j].ActivationSequence })
	return nodeRuns, nil
}

// TransitionWorkflowRunState mirrors sqlite's TransitionWorkflowRunState
// (V4-12, extended V4-12B with NextCancelEpoch): a stale caller gets
// ErrOptimisticConflict, never a silent overwrite.
func (r *RuntimeRepository) TransitionWorkflowRunState(_ context.Context, req ports.TransitionWorkflowRunStateRequest) (runtime.WorkflowRun, error) {
	run, ok := r.workflowRuns[req.RunID]
	if !ok {
		return runtime.WorkflowRun{}, fmt.Errorf("fake: %w: workflow run %s", ports.ErrPersistenceNotFound, req.RunID)
	}
	if run.State != req.ExpectedState || run.Version != req.ExpectedVersion {
		return runtime.WorkflowRun{}, fmt.Errorf(
			"fake: %w: workflow run %s expected %s@%d",
			ports.ErrOptimisticConflict, req.RunID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	run.State = req.NextState
	run.Version++
	if req.NextState == runtime.WorkflowRunFailed || req.NextState == runtime.WorkflowRunCancelled {
		now := time.Now().UTC()
		run.FinishedAt = &now
	}
	if req.NextCancelEpoch != nil {
		epoch := *req.NextCancelEpoch
		run.CancelEpoch = &epoch
	}
	r.workflowRuns[req.RunID] = run
	return run, nil
}

// ListExecutionAttemptsForRun mirrors sqlite's ListExecutionAttemptsForRun
// (V4-12B): every ExecutionAttempt whose own NodeRun belongs to runID —
// joined here in Go against r.nodeRuns since this fake's own attempts map
// (like the real schema) carries no RunID column of its own.
func (r *RuntimeRepository) ListExecutionAttemptsForRun(_ context.Context, runID string) ([]runtime.ExecutionAttempt, error) {
	nodeRunIDs := make(map[string]bool)
	for _, nodeRun := range r.nodeRuns {
		if string(nodeRun.RunID) == runID {
			nodeRunIDs[string(nodeRun.ID)] = true
		}
	}
	var attempts []runtime.ExecutionAttempt
	for _, attempt := range r.attempts {
		if nodeRunIDs[string(attempt.NodeRunID)] {
			attempts = append(attempts, attempt)
		}
	}
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].ID < attempts[j].ID })
	return attempts, nil
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

// GetMaxNodeIteration mirrors sqlite's identical MAX-over-existing-rows
// query (V4-07) — a linear scan over r.nodeRuns is fine for this fake's own
// scale (see this package's own doc comment on why fakes never need to be
// performant).
func (r *RuntimeRepository) GetMaxNodeIteration(_ context.Context, runID, nodeKey string) (uint32, bool, error) {
	var max uint32
	found := false
	for _, nodeRun := range r.nodeRuns {
		if string(nodeRun.RunID) != runID || nodeRun.NodeKey != nodeKey {
			continue
		}
		if !found || nodeRun.Iteration > max {
			max = nodeRun.Iteration
		}
		found = true
	}
	return max, found, nil
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

// GetExecutionAttempt mirrors sqlite's GetExecutionAttempt (V4-05).
func (r *RuntimeRepository) GetExecutionAttempt(_ context.Context, id string) (runtime.ExecutionAttempt, error) {
	attempt, ok := r.attempts[id]
	if !ok {
		return runtime.ExecutionAttempt{}, fmt.Errorf("fake: %w: execution attempt %s", ports.ErrPersistenceNotFound, id)
	}
	return attempt, nil
}

// TransitionExecutionAttempt mirrors sqlite's transitionExecutionAttemptTx
// (V4-05): a stale caller (wrong ExpectedState/ExpectedVersion) gets
// ErrOptimisticConflict, never a silent overwrite. Performs no fencing —
// see ports.RuntimeRepository.TransitionExecutionAttempt's own doc comment
// for why that is FinalizeExecutionAttempt's own concern, not this method's.
func (r *RuntimeRepository) TransitionExecutionAttempt(_ context.Context, req ports.TransitionExecutionAttemptRequest) (runtime.ExecutionAttempt, error) {
	attempt, ok := r.attempts[req.AttemptID]
	if !ok {
		return runtime.ExecutionAttempt{}, fmt.Errorf("fake: %w: execution attempt %s", ports.ErrPersistenceNotFound, req.AttemptID)
	}
	if attempt.State != req.ExpectedState || attempt.Version != req.ExpectedVersion {
		return runtime.ExecutionAttempt{}, fmt.Errorf(
			"fake: %w: execution attempt %s expected %s@%d",
			ports.ErrOptimisticConflict, req.AttemptID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	attempt.State = req.NextState
	if req.TerminationReason != "" {
		attempt.TerminationReason = req.TerminationReason
	}
	if req.FailureCode != "" {
		attempt.FailureCode = req.FailureCode
	}
	attempt.Version++
	r.attempts[req.AttemptID] = attempt
	return attempt, nil
}

// ValidateWriteLeaseFencing is a trivial pass-through on this fake (V4-05):
// this fake never models write_leases state at all, and deep WriteLease
// fencing edge cases (expired lease, wrong owner/token/generation/fence)
// are SQLite-only per this task's own test-layering decision — a fake test
// that wants a write-lease rejection path is testing the wrong layer.
func (r *RuntimeRepository) ValidateWriteLeaseFencing(_ context.Context, _ ports.JobLease, _ ports.WriteLeaseGrant) error {
	return nil
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

func branchTokenKey(forkNodeRunID, branchKey string) string {
	return forkNodeRunID + "\x00" + branchKey
}

func (r *RuntimeRepository) CreateBranchToken(_ context.Context, token runtime.BranchToken) (runtime.BranchToken, error) {
	key := branchTokenKey(string(token.ForkNodeRunID), token.BranchKey)
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

func (r *RuntimeRepository) GetBranchToken(_ context.Context, forkNodeRunID, branchKey string) (runtime.BranchToken, error) {
	token, ok := r.branches[branchTokenKey(forkNodeRunID, branchKey)]
	if !ok {
		return runtime.BranchToken{}, fmt.Errorf("fake: %w: branch token %s/%s", ports.ErrPersistenceNotFound, forkNodeRunID, branchKey)
	}
	return token, nil
}

// GetBranchTokenByID implements ports.RuntimeRepository (V4-10).
func (r *RuntimeRepository) GetBranchTokenByID(_ context.Context, id string) (runtime.BranchToken, error) {
	for _, token := range r.branches {
		if string(token.ID) == id {
			return token, nil
		}
	}
	return runtime.BranchToken{}, fmt.Errorf("fake: %w: branch token %s", ports.ErrPersistenceNotFound, id)
}

// TransitionBranchToken mirrors sqlite's identical fenced CAS — a stale
// caller (wrong ExpectedVersion) gets ErrOptimisticConflict, never a
// silent overwrite.
func (r *RuntimeRepository) TransitionBranchToken(_ context.Context, req ports.TransitionBranchTokenRequest) (runtime.BranchToken, error) {
	for key, token := range r.branches {
		if string(token.ID) != req.BranchTokenID {
			continue
		}
		if token.Version != req.ExpectedVersion {
			return runtime.BranchToken{}, fmt.Errorf("fake: %w: branch token %s expected version %d", ports.ErrOptimisticConflict, req.BranchTokenID, req.ExpectedVersion)
		}
		token.State = req.NextState
		token.CurrentNodeKey = req.NextCurrentNodeKey
		token.Version++
		r.branches[key] = token
		return token, nil
	}
	return runtime.BranchToken{}, fmt.Errorf("fake: %w: branch token %s", ports.ErrPersistenceNotFound, req.BranchTokenID)
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

// ListBranchTokensForFork implements ports.RuntimeRepository (V4-11): see
// that interface method's own doc comment for why this is scoped to one
// fork occurrence rather than the whole Run.
func (r *RuntimeRepository) ListBranchTokensForFork(_ context.Context, forkNodeRunID string) ([]runtime.BranchToken, error) {
	var tokens []runtime.BranchToken
	for _, token := range r.branches {
		if string(token.ForkNodeRunID) == forkNodeRunID {
			tokens = append(tokens, token)
		}
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].BranchKey < tokens[j].BranchKey })
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

// TransitionRunCancellationIntentState mirrors sqlite's
// TransitionRunCancellationIntentState (V4-12B): fenced purely by
// (RunID, ExpectedState) — RunCancellationIntent carries no Version.
func (r *RuntimeRepository) TransitionRunCancellationIntentState(_ context.Context, req ports.TransitionRunCancellationIntentStateRequest) (runtime.RunCancellationIntent, error) {
	intent, ok := r.runIntents[req.RunID]
	if !ok {
		return runtime.RunCancellationIntent{}, fmt.Errorf("fake: %w: run cancellation intent for run %s", ports.ErrPersistenceNotFound, req.RunID)
	}
	if intent.State != req.ExpectedState {
		return runtime.RunCancellationIntent{}, fmt.Errorf(
			"fake: %w: run cancellation intent for run %s expected %s", ports.ErrOptimisticConflict, req.RunID, req.ExpectedState,
		)
	}
	intent.State = req.NextState
	r.runIntents[req.RunID] = intent
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

// CreateScopeExpansionOrigin mirrors sqlite's identical method (V4-12A).
func (r *RuntimeRepository) CreateScopeExpansionOrigin(_ context.Context, origin runtime.ScopeExpansionOrigin) (runtime.ScopeExpansionOrigin, error) {
	key := string(origin.AttemptID)
	if _, exists := r.scopeOrigins[key]; exists {
		return runtime.ScopeExpansionOrigin{}, fmt.Errorf("fake: %w: scope expansion origin %s", ports.ErrPersistenceAlreadyExists, key)
	}
	if r.scopeOrigins == nil {
		r.scopeOrigins = map[string]runtime.ScopeExpansionOrigin{}
	}
	r.scopeOrigins[key] = origin
	return origin, nil
}

// GetScopeExpansionOriginByAttemptID mirrors sqlite's identical method (V4-12A).
func (r *RuntimeRepository) GetScopeExpansionOriginByAttemptID(_ context.Context, attemptID string) (runtime.ScopeExpansionOrigin, error) {
	origin, ok := r.scopeOrigins[attemptID]
	if !ok {
		return runtime.ScopeExpansionOrigin{}, fmt.Errorf("fake: %w: scope expansion origin attempt_id=%s", ports.ErrPersistenceNotFound, attemptID)
	}
	return origin, nil
}

// GetScopeExpansionOriginByRequestID mirrors sqlite's identical method (V4-12A).
func (r *RuntimeRepository) GetScopeExpansionOriginByRequestID(_ context.Context, requestID string) (runtime.ScopeExpansionOrigin, error) {
	for _, origin := range r.scopeOrigins {
		if origin.RequestID == requestID {
			return origin, nil
		}
	}
	return runtime.ScopeExpansionOrigin{}, fmt.Errorf("fake: %w: scope expansion origin request_id=%s", ports.ErrPersistenceNotFound, requestID)
}

// TransitionScopeExpansionOrigin mirrors sqlite's identical method (V4-12A):
// a stale caller (wrong ExpectedVersion) gets ErrOptimisticConflict, never
// a silent overwrite.
func (r *RuntimeRepository) TransitionScopeExpansionOrigin(_ context.Context, req ports.TransitionScopeExpansionOriginRequest) (runtime.ScopeExpansionOrigin, error) {
	origin, ok := r.scopeOrigins[req.AttemptID]
	if !ok {
		return runtime.ScopeExpansionOrigin{}, fmt.Errorf("fake: %w: scope expansion origin %s", ports.ErrPersistenceNotFound, req.AttemptID)
	}
	if origin.Version != req.ExpectedVersion {
		return runtime.ScopeExpansionOrigin{}, fmt.Errorf(
			"fake: %w: scope expansion origin %s expected version %d",
			ports.ErrOptimisticConflict, req.AttemptID, req.ExpectedVersion,
		)
	}
	origin.ReconcileStatus = req.NextReconcileStatus
	if req.NextPollGeneration != nil {
		origin.PollGeneration = *req.NextPollGeneration
	}
	if req.NextReactivatedNodeRunID != nil {
		ref := runtime.NodeRunID(*req.NextReactivatedNodeRunID)
		origin.ReactivatedNodeRunID = &ref
	}
	origin.Version++
	r.scopeOrigins[req.AttemptID] = origin
	return origin, nil
}
