package work

import (
	"context"
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// readinessProblems is the one readiness question MarkWorkItemReady and
// ExplainWorkItemReadiness both ask (so they cannot disagree): the pure
// completeness problems of workdomain.ValidateReadinessGate, followed by
// V9-08's baseline problems — a WorkItem that may WRITE to a repository whose
// readiness profile has no passing baseline (and no accepted exception) is not
// READY (gap G8: "khi chạy thật không có gì chặn agent ghi trước baseline").
// Read-only access and repositories without a readiness profile add nothing,
// so a WorkItem that touched neither behaves exactly as before.
//
// The first return value is the combined list; nil means ready.
func readinessProblems(ctx context.Context, tx ports.Tx, item workdomain.WorkItem) ([]string, error) {
	var problems []string
	if gateErr := workdomain.ValidateReadinessGate(item); gateErr != nil {
		var readinessErr *workdomain.ReadinessError
		if !errors.As(gateErr, &readinessErr) {
			// ValidateReadinessGate only ever returns nil or a *ReadinessError;
			// a new error shape should fail loudly rather than be swallowed.
			return nil, gateErr
		}
		problems = append(problems, readinessErr.Problems...)
	}
	scopes, err := readinesscheck.AdmissionScopes(ctx, tx, string(item.ID), string(item.FamilyID))
	if err != nil {
		return nil, err
	}
	baseline, err := readinesscheck.WriterAdmissionProblems(ctx, tx, string(item.FamilyID), scopes)
	if err != nil {
		return nil, err
	}
	return append(problems, baseline...), nil
}
