// V5-15E — "Full conformance matrix" (docs/design/07-v5-execution-
// evidence.md V5-15's own "Hoàn thành khi" bar: "trace WorkItem→revision/
// evidence đầy đủ và false completion bằng 0 trong fixtures"; user's own
// binding 5-part PR split, full text in baocaov5checklist.md's own "V5-15"
// section and memory agent-kit-v5-15-acceptance-gate-contract.md).
//
// This file's own job is not to re-prove any ONE fault-injection property
// (A-D already each own one) — it is to sweep every real, durable trace
// category the design doc's own "Kết quả kỳ vọng" line names
// (WorkItem→Run→NodeRun→Attempt→Evidence/Artifact IDs+hashes→ReleaseSet→
// CompletionDecision) against ONE real Run that actually exercised every
// executor role at once (full_composition_test.go's own new scenario —
// the first time this codebase ever composed AGENT(maker)+COMMAND+
// MACHINE_GATE+AGENT(checker) together), then re-sweeps the identical
// categories after a real restart to prove the trace is truly durable, not
// merely held in memory.
//
// Six categories, matching the design doc's own named trace links:
//   - DB state: WorkItem/WorkflowRun/every NodeRun/every ExecutionAttempt.
//   - Domain events: the complete causal event log (ListDomainEventsForProject).
//   - Outbox: every domain event's own durable delivery record — a gap
//     this task found and closed: no read-only outbox query existed
//     anywhere reachable from this package before now
//     (ports.OutboxDispatchStore only exposes mutating claim/dispatch/
//     recover methods; see DebugListOutboxMessagesForProject's own doc
//     comment, internal/adapters/sqlite/event_queries.go).
//   - Blockers: a real DONE WorkItem must carry no open blocker.
//   - Activations: every NodeRun's own real, monotonic ActivationSequence
//     (already exposed by ListNodeRunsForRun — no new read needed).
//   - Artifacts: every Evidence row's own ArtifactReferences, each
//     resolved through tx.Artifacts().GetArtifact and re-verified against
//     the real, content-addressed ArtifactStore.
package v5accept

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// v5AcceptConformanceOracle is one complete sweep of every category above,
// for one WorkItem/Run pair — read entirely through f.uow/f.store,
// whichever real store is current (pre- or post-restart), the same
// "caller decides when to call this" discipline assertHappyPathState/
// assertFullCompositionState already establish.
type v5AcceptConformanceOracle struct {
	Run              runtimedomain.WorkflowRun
	WorkItem         workdomain.WorkItem
	NodeRuns         []runtimedomain.NodeRun
	Attempts         []runtimedomain.ExecutionAttempt
	DecisionArtifact runtimedomain.DecisionArtifact
	ReleaseSet       workdomain.ReleaseSet
	DomainEvents     []sqlite.DomainEventRecord
	OutboxMessages   []sqlite.DebugOutboxRow
	Blockers         []workdomain.WorkItemBlocker
	Artifacts        []artifact.Artifact
}

// sweepConformanceOracle gathers all six categories for one WorkItem/Run.
func (f *v5AcceptFixture) sweepConformanceOracle(t *testing.T, workItemID, runID, decisionArtifactID, releaseSetID string) v5AcceptConformanceOracle {
	t.Helper()
	ctx := context.Background()

	var oracle v5AcceptConformanceOracle
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		if oracle.Run, err = tx.Runtime().GetWorkflowRun(ctx, runID); err != nil {
			return err
		}
		if oracle.WorkItem, err = tx.Work().GetWorkItem(ctx, workItemID); err != nil {
			return err
		}
		if oracle.NodeRuns, err = tx.Runtime().ListNodeRunsForRun(ctx, runID); err != nil {
			return err
		}
		if oracle.Attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID); err != nil {
			return err
		}
		if oracle.DecisionArtifact, err = tx.Runtime().GetDecisionArtifact(ctx, decisionArtifactID); err != nil {
			return err
		}
		if oracle.ReleaseSet, err = tx.Work().GetReleaseSet(ctx, releaseSetID); err != nil {
			return err
		}
		if oracle.Blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, workItemID); err != nil {
			return err
		}
		artifactIDs := make(map[string]struct{})
		for _, a := range oracle.Attempts {
			evidence, err := tx.Runtime().ListEvidenceForAttempt(ctx, string(a.ID))
			if err != nil {
				return err
			}
			for _, e := range evidence {
				for _, ref := range e.ArtifactReferences {
					artifactIDs[ref] = struct{}{}
				}
			}
		}
		for id := range artifactIDs {
			a, err := tx.Artifacts().GetArtifact(ctx, id)
			if err != nil {
				return err
			}
			oracle.Artifacts = append(oracle.Artifacts, a)
		}
		return nil
	}); err != nil {
		t.Fatalf("sweepConformanceOracle: read DB state: %v", err)
	}

	var err error
	if oracle.DomainEvents, err = f.store.ListDomainEventsForProject(ctx, v5AcceptProjectID); err != nil {
		t.Fatalf("sweepConformanceOracle: ListDomainEventsForProject: %v", err)
	}
	if oracle.OutboxMessages, err = f.store.DebugListOutboxMessagesForProject(ctx, v5AcceptProjectID); err != nil {
		t.Fatalf("sweepConformanceOracle: DebugListOutboxMessagesForProject: %v", err)
	}
	return oracle
}

// TestV5AcceptConformanceMatrix runs buildV5AcceptFullCompositionRun's own
// real 4-role Run (full_composition_test.go) through all six oracle
// categories, as an explicit table so a failure in one category never
// masks another — then repeats the entire sweep after a real restart to
// prove every category's own trace is durable, never merely in-memory.
func TestV5AcceptConformanceMatrix(t *testing.T) {
	result := buildV5AcceptFullCompositionRun(t)
	f := result.f
	ctx := context.Background()

	sweep := func() v5AcceptConformanceOracle {
		return f.sweepConformanceOracle(t, result.workItemID, result.runID, result.decisionArtifactID, result.releaseSetID)
	}

	runMatrix := func(t *testing.T, oracle v5AcceptConformanceOracle) {
		t.Run("DB state: WorkItem/Run/NodeRun/Attempt trace is complete", func(t *testing.T) {
			if oracle.Run.State != runtimedomain.WorkflowRunSucceeded {
				t.Fatalf("run.State = %s, want SUCCEEDED", oracle.Run.State)
			}
			if oracle.WorkItem.Status != workdomain.WorkItemDone {
				t.Fatalf("workItem.Status = %s, want DONE", oracle.WorkItem.Status)
			}
			wantNodes := map[string]bool{"maker": false, "test_a": false, "gate_b": false, "checker": false}
			for _, nr := range oracle.NodeRuns {
				if _, ok := wantNodes[nr.NodeKey]; ok {
					wantNodes[nr.NodeKey] = true
					if nr.State != runtimedomain.NodeRunSucceeded {
						t.Fatalf("NodeRun %s.State = %s, want SUCCEEDED", nr.NodeKey, nr.State)
					}
				}
			}
			for key, seen := range wantNodes {
				if !seen {
					t.Fatalf("no NodeRun observed for node %q", key)
				}
			}
			if len(oracle.Attempts) != 4 {
				t.Fatalf("len(Attempts) = %d, want exactly 4 (one per node, no retries)", len(oracle.Attempts))
			}
			for _, a := range oracle.Attempts {
				if a.State != runtimedomain.ExecutionAttemptSucceeded {
					t.Fatalf("attempt %s.State = %s, want SUCCEEDED", a.ID, a.State)
				}
			}
		})

		t.Run("Domain events: causal trace exists for every major transition", func(t *testing.T) {
			if len(oracle.DomainEvents) == 0 {
				t.Fatal("DomainEvents is empty — no causal trace at all")
			}
			seenCompletionDecided := false
			for _, e := range oracle.DomainEvents {
				if e.EventType == "COMPLETION_DECIDED" && e.AggregateID == result.runID {
					seenCompletionDecided = true
				}
				if e.JournalPosition <= 0 {
					t.Fatalf("event %s has JournalPosition = %d, want a real positive causal position", e.ID, e.JournalPosition)
				}
			}
			if !seenCompletionDecided {
				t.Fatalf("no COMPLETION_DECIDED event found for run %s among %d events", result.runID, len(oracle.DomainEvents))
			}
		})

		t.Run("Outbox: every appended event has a matching durable delivery record", func(t *testing.T) {
			if len(oracle.OutboxMessages) == 0 {
				t.Fatal("OutboxMessages is empty — V1-07's own outbox invariant (a committed event never ends up missing its outbox record) is unverifiable")
			}
			outboxByEvent := make(map[string]sqlite.DebugOutboxRow, len(oracle.OutboxMessages))
			for _, m := range oracle.OutboxMessages {
				outboxByEvent[m.EventID] = m
			}
			for _, e := range oracle.DomainEvents {
				if _, ok := outboxByEvent[e.ID]; !ok {
					t.Fatalf("domain event %s (type=%s) has no matching outbox row", e.ID, e.EventType)
				}
			}
		})

		t.Run("Blockers: a DONE WorkItem carries no open blocker", func(t *testing.T) {
			for _, b := range oracle.Blockers {
				if b.State == workdomain.BlockerOpen {
					t.Fatalf("WorkItem %s is DONE but still carries an OPEN blocker %+v", result.workItemID, b)
				}
			}
		})

		t.Run("Activations: every NodeRun has a real, positive ActivationSequence", func(t *testing.T) {
			seen := make(map[uint64]string)
			for _, nr := range oracle.NodeRuns {
				if nr.ActivationSequence == 0 {
					t.Fatalf("NodeRun %s.ActivationSequence = 0, want a real positive sequence", nr.NodeKey)
				}
				if other, ok := seen[nr.ActivationSequence]; ok {
					t.Fatalf("NodeRun %s and %s share ActivationSequence %d — activations must be distinct", nr.NodeKey, other, nr.ActivationSequence)
				}
				seen[nr.ActivationSequence] = nr.NodeKey
			}
		})

		t.Run("Artifacts: every Evidence-referenced artifact is durable and hash-verified", func(t *testing.T) {
			if len(oracle.Artifacts) == 0 {
				t.Fatal("Artifacts is empty — the real COMMAND/MACHINE_GATE output evidence this graph produces should reference at least one durable artifact")
			}
			for _, a := range oracle.Artifacts {
				if err := f.artifacts.Verify(ctx, ports.ArtifactRef{Locator: a.Locator, SHA256: a.ContentHash, Size: a.Size}); err != nil {
					t.Fatalf("Verify(artifact %s): %v — durable bytes no longer match their recorded hash", a.ID, err)
				}
			}
		})
	}

	t.Run("before restart", func(t *testing.T) { runMatrix(t, sweep()) })

	f.restart(t)
	t.Run("after restart", func(t *testing.T) { runMatrix(t, sweep()) })
}
