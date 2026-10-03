package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// baselineNoteAt (V9-08, gap G8) tells a maker sent back by a failing check how
// each repository it may write stood BEFORE the task: HE-12-M03, "regression
// không được che bởi lỗi có sẵn". For every write repository with a baseline
// attempt it says either that the baseline passed — so a check that fails now
// was passing, and the failure comes from this task's changes — or that the
// baseline had already failed (and whether an operator accepted that), so a
// failure that matches it is not the task's doing.
//
// Everything is read as of `at`, the creation time of the maker's snapshot: the
// baseline attempts and exceptions are append-only and time-stamped, so a later
// `verify` does not change the prompt the same snapshot renders (V5-08B0). The
// result is "" when no write repository has a baseline attempt by then.
func baselineNoteAt(ctx context.Context, tx ports.Tx, familyID string, scopes []workdomain.RepositoryScope, at time.Time) (string, error) {
	repositories := make(map[string]bool)
	for _, scope := range scopes {
		if scope.Access() == workdomain.RepositoryWrite {
			repositories[string(scope.RepositoryID())] = true
		}
	}
	ids := make([]string, 0, len(repositories))
	for id := range repositories {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var lines []string
	for _, repositoryID := range ids {
		rw, found, err := readinesscheck.LatestRepositoryWorkspace(ctx, tx, familyID, repositoryID)
		if err != nil {
			return "", err
		}
		if !found {
			continue
		}
		attempts, err := tx.Readiness().ListBaselineAttempts(ctx, string(rw.ID))
		if err != nil {
			return "", err
		}
		var latest *ports.BaselineAttempt
		for i := len(attempts) - 1; i >= 0; i-- {
			if !attempts[i].CreatedAt.After(at) {
				latest = &attempts[i]
				break
			}
		}
		if latest == nil {
			continue
		}
		if latest.Outcome.Passed() {
			lines = append(lines, fmt.Sprintf(
				"repository %s: its baseline passed before this task started, so a check that fails now was passing — the failure comes from the changes made during this task.", repositoryID))
			continue
		}
		line := fmt.Sprintf(
			"repository %s: its baseline had already failed before this task started (%s, attempt %s)", repositoryID, latest.Outcome.FailureKind(), latest.ID)
		exception, err := tx.Readiness().GetBaselineException(ctx, latest.ID)
		switch {
		case err == nil && !exception.AcceptedAt.After(at):
			line += fmt.Sprintf(" and an operator accepted it (%s)", strings.TrimSpace(exception.Reason))
		case err != nil && !errors.Is(err, ports.ErrPersistenceNotFound):
			return "", err
		}
		line += ". A failure that matches the baseline is not caused by this task; fix what this task changed and leave the rest."
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}
