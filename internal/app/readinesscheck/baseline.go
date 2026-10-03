package readinesscheck

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// BaselineState is where one RepositoryWorkspace stands against its
// repository's readiness profile (V9-08, gap G8: "khởi tạo và baseline trước
// khi agent ghi"). Only PASS and EXCEPTION_ACCEPTED, and NOT_REQUIRED for a
// repository that declared no profile, admit a writer.
type BaselineState string

const (
	// BaselineNotRequired: the repository has no readiness profile, so there
	// is nothing to check (HE-06-M05 automatic discovery is not part of Alpha).
	BaselineNotRequired BaselineState = "NOT_REQUIRED"
	// BaselinePending: the repository has a profile but no baseline attempt has
	// run for its current version yet (or the workspace is not READY).
	BaselinePending BaselineState = "PENDING"
	// BaselinePass: the latest attempt for the current profile version passed.
	BaselinePass BaselineState = "PASS"
	// BaselineFail: the latest attempt for the current profile version did not
	// pass and no exception was accepted.
	BaselineFail BaselineState = "FAIL"
	// BaselineExceptionAccepted: the latest attempt failed and an operator
	// accepted that failure.
	BaselineExceptionAccepted BaselineState = "EXCEPTION_ACCEPTED"
)

// AdmitsWriters reports whether a RepositoryWorkspace in state s may be
// written to by a task.
func (s BaselineState) AdmitsWriters() bool {
	return s == BaselineNotRequired || s == BaselinePass || s == BaselineExceptionAccepted
}

// BaselineEvaluation is EvaluateWorkspaceBaseline's result.
type BaselineEvaluation struct {
	State BaselineState
	// Attempt is the latest baseline attempt for the current profile version;
	// nil while PENDING or NOT_REQUIRED.
	Attempt *ports.BaselineAttempt
	// Exception is set only for EXCEPTION_ACCEPTED.
	Exception *ports.BaselineException
	// FailureKind classifies a failed attempt (FAIL or EXCEPTION_ACCEPTED):
	// a failure of the repository or its environment, never of a task.
	FailureKind readiness.FailureKind
	// Reason says why the state is not PASS, for an operator.
	Reason string
}

// EvaluateWorkspaceBaseline decides rw's baseline state against profile (nil
// when the repository has none). It only reads: the baseline attempts, and for
// a failed one the exception recorded for it.
func EvaluateWorkspaceBaseline(ctx context.Context, tx ports.Tx, profile *readiness.Profile, rw workspace.RepositoryWorkspace) (BaselineEvaluation, error) {
	if profile == nil {
		return BaselineEvaluation{State: BaselineNotRequired}, nil
	}
	if rw.State != workspace.RepositoryWorkspaceReady {
		return BaselineEvaluation{
			State:  BaselinePending,
			Reason: fmt.Sprintf("the repository workspace is %s, not READY, so no baseline has run", rw.State),
		}, nil
	}
	attempts, err := tx.Readiness().ListBaselineAttempts(ctx, string(rw.ID))
	if err != nil {
		return BaselineEvaluation{}, err
	}
	var latest *ports.BaselineAttempt
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].ProfileVersion == profile.Version {
			latest = &attempts[i]
			break
		}
	}
	if latest == nil {
		return BaselineEvaluation{
			State:  BaselinePending,
			Reason: fmt.Sprintf("no baseline has run yet for readiness profile version %d", profile.Version),
		}, nil
	}
	if latest.Outcome.Passed() {
		return BaselineEvaluation{State: BaselinePass, Attempt: latest}, nil
	}
	evaluation := BaselineEvaluation{
		State: BaselineFail, Attempt: latest, FailureKind: latest.Outcome.FailureKind(),
		Reason: fmt.Sprintf("the %s baseline of readiness profile version %d failed (%s)", latest.Stage, profile.Version, latest.Outcome.FailureKind()),
	}
	exception, err := tx.Readiness().GetBaselineException(ctx, latest.ID)
	if err == nil {
		evaluation.State = BaselineExceptionAccepted
		evaluation.Exception = &exception
		return evaluation, nil
	}
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		return BaselineEvaluation{}, err
	}
	return evaluation, nil
}

// LatestRepositoryWorkspace returns the highest-generation RepositoryWorkspace
// of repositoryID in the WorkspaceSet of familyID, or ok=false when the family
// has no WorkspaceSet or no workspace for the repository yet.
func LatestRepositoryWorkspace(ctx context.Context, tx ports.Tx, familyID, repositoryID string) (rw workspace.RepositoryWorkspace, ok bool, err error) {
	set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, familyID)
	if errors.Is(err, ports.ErrPersistenceNotFound) {
		return workspace.RepositoryWorkspace{}, false, nil
	}
	if err != nil {
		return workspace.RepositoryWorkspace{}, false, err
	}
	all, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, string(set.ID))
	if err != nil {
		return workspace.RepositoryWorkspace{}, false, err
	}
	for _, candidate := range all {
		if string(candidate.RepositoryID) != repositoryID {
			continue
		}
		if !ok || candidate.Generation > rw.Generation {
			rw, ok = candidate, true
		}
	}
	return rw, ok, nil
}

// AdmissionScopes returns the repository scopes a work item may act within, for
// the writer-admission question: its own effective scopes — what a child work
// item is created with — and, when it has none, the scopes of its task family,
// which is what a root work item is granted (CreateRootWorkItem persists the
// initial scope on the family and gives the root no effective-scope rows).
func AdmissionScopes(ctx context.Context, tx ports.Tx, workItemID, familyID string) ([]work.RepositoryScope, error) {
	scopes, err := tx.Work().ListWorkItemEffectiveScopes(ctx, workItemID)
	if err != nil || len(scopes) > 0 {
		return scopes, err
	}
	return tx.Work().ListFamilyRepositoryScopes(ctx, familyID)
}

// WriterAdmissionProblems lists, in plain words, every repository a work item
// of familyID may WRITE to (scopes) whose baseline does not admit a writer
// (V9-08): the readiness profile has no baseline for its current version yet,
// or the baseline failed and no exception was accepted. Empty means writers
// are admitted. A repository without a readiness profile never contributes a
// problem, and neither does read-only access.
//
// The text is what MarkWorkItemReady and ExplainWorkItemReadiness return in
// their problem list, so an operator reads exactly why the work item is not
// READY and what to do about it.
func WriterAdmissionProblems(ctx context.Context, tx ports.Tx, familyID string, scopes []work.RepositoryScope) ([]string, error) {
	writable := make(map[string]bool)
	for _, scope := range scopes {
		if scope.Access() == work.RepositoryWrite {
			writable[string(scope.RepositoryID())] = true
		}
	}
	repositories := make([]string, 0, len(writable))
	for repositoryID := range writable {
		repositories = append(repositories, repositoryID)
	}
	sort.Strings(repositories)

	var problems []string
	for _, repositoryID := range repositories {
		profile, err := loadProfile(ctx, tx, repositoryID)
		if err != nil {
			return nil, err
		}
		if profile == nil {
			continue
		}
		rw, found, err := LatestRepositoryWorkspace(ctx, tx, familyID, repositoryID)
		if err != nil {
			return nil, err
		}
		if !found {
			problems = append(problems, fmt.Sprintf(
				"repository %s: the baseline has not run — its workspace is not provisioned yet (readiness profile version %d)", repositoryID, profile.Version))
			continue
		}
		evaluation, err := EvaluateWorkspaceBaseline(ctx, tx, profile, rw)
		if err != nil {
			return nil, err
		}
		switch evaluation.State {
		case BaselinePending:
			problems = append(problems, fmt.Sprintf("repository %s: baseline pending — %s", repositoryID, evaluation.Reason))
		case BaselineFail:
			problems = append(problems, fmt.Sprintf(
				"repository %s: baseline FAILED (%s, attempt %s) before any change — fix the repository and run `aw repository readiness verify`, or accept the failure with `aw repository readiness accept-exception`",
				repositoryID, evaluation.FailureKind, evaluation.Attempt.ID))
		}
	}
	return problems, nil
}

func loadProfile(ctx context.Context, tx ports.Tx, repositoryID string) (*readiness.Profile, error) {
	profile, err := tx.Readiness().GetReadinessProfile(ctx, repositoryID)
	if errors.Is(err, ports.ErrPersistenceNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}
