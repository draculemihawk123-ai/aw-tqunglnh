package scopeguard

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V9-09: ValidateDiffs' error is a *ViolationsError — the structured list a
// caller can report — while staying an ErrScopeViolation with the exact
// message it always had.
func TestValidateDiffs_ErrorCarriesTheSortedViolations(t *testing.T) {
	scope, err := work.NewRepositoryScope("family-1", 1, "repo-1", work.RepositoryWrite, []string{"allowed"}, "task", "actor", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewRepositoryScope: %v", err)
	}
	diffErr := ValidateDiffs([]work.RepositoryScope{scope}, []ports.WorkspaceDiff{{
		RepositoryID: "repo-1",
		Files:        []ports.FileStatus{{Code: "A", Path: "z.txt"}, {Code: "A", Path: "allowed/ok.txt"}, {Code: "A", Path: "a.txt"}},
	}})
	if !errors.Is(diffErr, ErrScopeViolation) {
		t.Fatalf("err = %v, want ErrScopeViolation", diffErr)
	}
	var typed *ViolationsError
	if !errors.As(fmt.Errorf("wrapped: %w", diffErr), &typed) {
		t.Fatalf("err = %v, want a *ViolationsError even when wrapped", diffErr)
	}
	if len(typed.Violations) != 2 || typed.Violations[0].Path != "a.txt" || typed.Violations[1].Path != "z.txt" {
		t.Fatalf("Violations = %+v, want [a.txt z.txt] sorted, with the in-scope file left out", typed.Violations)
	}
	want := "workspace diff exceeds effective write scope: repo-1:a.txt: path is not covered by a WRITE repository scope; repo-1:z.txt: path is not covered by a WRITE repository scope"
	if diffErr.Error() != want {
		t.Fatalf("message changed:\n got  %q\n want %q", diffErr.Error(), want)
	}
	if !strings.HasPrefix(diffErr.Error(), ErrScopeViolation.Error()) {
		t.Fatalf("message must keep the ErrScopeViolation prefix")
	}
}

func TestNewViolationsError_EmptyListIsNil(t *testing.T) {
	if err := NewViolationsError(nil); err != nil {
		t.Fatalf("NewViolationsError(nil) = %v, want nil", err)
	}
}
