package worker

import (
	"context"
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// FinalizationInput binds side-effect provenance to the fenced runtime
// decision. The worker must supply every repository diff it observed; omitted
// diffs are a caller bug, not an implicit success.
type FinalizationInput struct {
	Finalization    ports.WorkerWorkflowRunFinalization
	EffectiveScopes []work.RepositoryScope
	Diffs           []ports.WorkspaceDiff
}

type Finalizer struct {
	persistence ports.WorkflowPersistence
}

func NewFinalizer(persistence ports.WorkflowPersistence) (*Finalizer, error) {
	if persistence == nil {
		return nil, errors.New("workflow persistence is required")
	}
	return &Finalizer{persistence: persistence}, nil
}

func (f *Finalizer) Finalize(ctx context.Context, input FinalizationInput) (runtime.WorkflowRun, error) {
	if f == nil || f.persistence == nil {
		return runtime.WorkflowRun{}, errors.New("worker finalizer is not configured")
	}
	if err := scopeguard.ValidateDiffs(input.EffectiveScopes, input.Diffs); err != nil {
		return runtime.WorkflowRun{}, err
	}
	return f.persistence.FinalizeWorkflowRun(ctx, input.Finalization)
}
