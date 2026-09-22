package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// completionSystemActor is the actor recorded on a completion decision the
// system takes itself, mirroring scopeExpansionSystemActor.
const completionSystemActor = "aw-orchestrator"

const evaluateCompletionCommandType = "EvaluateCompletionCandidate"

// CompletionSweepReport summarizes one CompletionOrchestrator.Sweep.
type CompletionSweepReport struct {
	// Evaluated is how many VERIFYING runs were decided (or replayed) this sweep.
	Evaluated int
	// Outcomes counts those decisions by CompletionOutcome.
	Outcomes map[CompletionOutcome]int
	// ReworkNodeRunIDs are the PENDING NodeRuns a REWORK decision created.
	// EvaluateCompletionCandidate deliberately leaves them unscheduled (its
	// package doc: production wiring that schedules a node after a hop is a
	// known gap), so nothing runs them yet; the caller surfaces them.
	ReworkNodeRunIDs []string
}

// CompletionOrchestrator is the system component ADR-011/ADR-021 and the V6
// UX artifact ("orchestrator") assume exists: a WorkflowRun that reached END
// sits in VERIFYING as a completion candidate, and EvaluateCompletionCandidate
// is the only thing that may move it on — but no route, CLI verb or job ever
// called it, so in production every Run stayed in VERIFYING forever. Sweep
// finds each such Run and decides it once.
type CompletionOrchestrator struct {
	uow ports.UnitOfWork
	ids idsource.Source
	clk clock.Clock
}

// NewCompletionOrchestrator returns an orchestrator over uow.
func NewCompletionOrchestrator(uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock) *CompletionOrchestrator {
	return &CompletionOrchestrator{uow: uow, ids: ids, clk: clk}
}

type completionCandidate struct {
	projectID string
	runID     string
	version   uint64
}

// Sweep evaluates every VERIFYING WorkflowRun of every ACTIVE project once.
//
// The scan uses only existing read ports (a run is VERIFYING only while its
// WorkItem is ACTIVE, ADR-021, so ACTIVE WorkItems bound it) and, like every
// runtime decision, never reads a projection. Each decision is a normal
// EvaluateCompletionCandidate command: its idempotency key names the run
// version this sweep observed, so a retry of the same observation replays the
// stored decision, while a run that changed underneath (cancelled, decided by
// another sweeper) fails its version CAS and is simply picked up, or not, on
// the next sweep. One run failing does not stop the others; the errors are
// joined.
func (o *CompletionOrchestrator) Sweep(ctx context.Context) (CompletionSweepReport, error) {
	report := CompletionSweepReport{Outcomes: map[CompletionOutcome]int{}}

	var candidates []completionCandidate
	if err := o.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		projects, err := tx.Catalog().ListProjects(ctx)
		if err != nil {
			return err
		}
		for _, p := range projects {
			if p.Status != project.ProjectActive {
				continue
			}
			items, err := tx.Work().ListWorkItemsByProject(ctx, string(p.ID))
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.Status != workdomain.WorkItemActive {
					continue
				}
				runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, string(item.ID))
				if err != nil {
					return err
				}
				for _, run := range runs {
					if run.State == runtimedomain.WorkflowRunVerifying {
						candidates = append(candidates, completionCandidate{projectID: string(p.ID), runID: string(run.ID), version: run.Version})
					}
				}
			}
		}
		return nil
	}); err != nil {
		return report, fmt.Errorf("runtime: scan for completion candidates: %w", err)
	}

	var failures []error
	for _, c := range candidates {
		if ctx.Err() != nil {
			break
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", c.runID, c.version)))
		result, err := EvaluateCompletionCandidate(ctx, o.uow, o.ids, o.clk, ports.Command{
			ID: o.ids.NewID(), IdempotencyKey: fmt.Sprintf("evaluate-completion:%s:v%d", c.runID, c.version),
			Actor: completionSystemActor, CorrelationID: o.ids.NewID(), Scope: ports.ProjectScope(c.projectID),
			RequestedAt: o.clk.Now(), Type: evaluateCompletionCommandType,
			RequestHash: hex.EncodeToString(digest[:]), ExpectedVersion: c.version,
		}, EvaluateCompletionCandidateRequest{RunID: c.runID})
		switch {
		case err == nil:
			report.Evaluated++
			report.Outcomes[result.Outcome]++
			if result.Outcome == CompletionOutcomeRework && result.ReworkNodeRunID != "" {
				report.ReworkNodeRunIDs = append(report.ReworkNodeRunIDs, result.ReworkNodeRunID)
			}
		case errors.Is(err, ports.ErrOptimisticConflict), errors.Is(err, ErrRunNotVerifying):
			// The run moved on between the scan and the decision.
		default:
			failures = append(failures, fmt.Errorf("evaluate completion of run %s: %w", c.runID, err))
		}
	}
	return report, errors.Join(failures...)
}
