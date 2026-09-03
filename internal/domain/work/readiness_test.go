package work

import (
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// validReadyRootWorkItem returns a WorkItem that ValidateReadinessGate
// accepts outright — every table-driven "missing field" test below starts
// from a copy of this and breaks exactly one thing, so a failure always
// isolates to the field under test rather than some other incidental gap.
func validReadyRootWorkItem() WorkItem {
	return WorkItem{
		ID:            "wi-root",
		ProjectID:     "project-1",
		Kind:          WorkItemRoot,
		FamilyID:      "family-1",
		SchemaVersion: 1,
		Title:         "Ship the thing",
		Behavior:      "Users can do the thing end to end",
		AcceptanceCriteria: []AcceptanceCriterion{
			{Description: "the thing works", VerificationRef: "go test ./..."},
		},
		VerificationSpec: "run the full suite locally and in CI",
		RiskLevel:        RiskLevel("MEDIUM"),
		Exclusions:       []string{"does not cover the legacy importer"},
		Status:           WorkItemBacklog,
		Version:          1,
	}
}

func TestValidateReadinessGate_AcceptsCompleteContract(t *testing.T) {
	t.Parallel()
	if err := ValidateReadinessGate(validReadyRootWorkItem()); err != nil {
		t.Fatalf("complete work item rejected: %v", err)
	}
}

// TestValidateReadinessGate_MissingRequiredField is the task's own
// "missing field" table: each case starts from a valid WorkItem and clears
// exactly one required field, expecting ValidateReadinessGate to name that
// field's own problem.
func TestValidateReadinessGate_MissingRequiredField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		mutate      func(item *WorkItem)
		wantProblem string
	}{
		{
			name:        "missing project id",
			mutate:      func(item *WorkItem) { item.ProjectID = "" },
			wantProblem: "project id is required",
		},
		{
			name:        "missing family id",
			mutate:      func(item *WorkItem) { item.FamilyID = "" },
			wantProblem: "family id is required",
		},
		{
			name:        "missing title",
			mutate:      func(item *WorkItem) { item.Title = "   " },
			wantProblem: "title is required",
		},
		{
			name:        "missing schema version",
			mutate:      func(item *WorkItem) { item.SchemaVersion = 0 },
			wantProblem: "schema version must be positive",
		},
		{
			name:        "negative schema version",
			mutate:      func(item *WorkItem) { item.SchemaVersion = -1 },
			wantProblem: "schema version must be positive",
		},
		{
			name:        "missing behavior",
			mutate:      func(item *WorkItem) { item.Behavior = "" },
			wantProblem: "behavior is required",
		},
		{
			name:        "missing verification spec",
			mutate:      func(item *WorkItem) { item.VerificationSpec = "" },
			wantProblem: "verification spec is required",
		},
		{
			name:        "missing risk level",
			mutate:      func(item *WorkItem) { item.RiskLevel = "" },
			wantProblem: "risk level is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			item := validReadyRootWorkItem()
			tc.mutate(&item)

			err := ValidateReadinessGate(item)
			if err == nil {
				t.Fatalf("expected rejection for %s, got nil error", tc.name)
			}
			readinessErr, ok := err.(*ReadinessError)
			if !ok {
				t.Fatalf("error type = %T, want *ReadinessError", err)
			}
			if !containsProblem(readinessErr.Problems, tc.wantProblem) {
				t.Fatalf("problems = %v, want to contain %q", readinessErr.Problems, tc.wantProblem)
			}
		})
	}
}

// TestValidateReadinessGate_NoExecutableAcceptanceWithoutException is this
// task's own "Hoàn thành khi" bar, made concrete: a WorkItem with no
// executable acceptance criterion must never reach READY unless an
// ApprovalException is present. This test would fail if that rule were
// ever removed or weakened.
func TestValidateReadinessGate_NoExecutableAcceptanceWithoutException(t *testing.T) {
	t.Parallel()

	t.Run("zero acceptance criteria, no exception", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		item.AcceptanceCriteria = nil

		err := ValidateReadinessGate(item)
		assertRejectedWithExecutableAcceptanceProblem(t, err)
	})

	t.Run("only descriptive (non-executable) criteria, no exception", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		item.AcceptanceCriteria = []AcceptanceCriterion{
			{Description: "looks right"},
			{Description: "feels done", VerificationRef: "   "},
		}

		err := ValidateReadinessGate(item)
		assertRejectedWithExecutableAcceptanceProblem(t, err)
	})

	t.Run("zero acceptance criteria WITH a valid approval exception is accepted", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		item.AcceptanceCriteria = nil
		exception, err := NewApprovalException("subjective design review, no automatable check exists", "operator@example.com", time.Now())
		if err != nil {
			t.Fatalf("build approval exception: %v", err)
		}
		item.ApprovalException = &exception

		if err := ValidateReadinessGate(item); err != nil {
			t.Fatalf("work item with approval exception rejected: %v", err)
		}
	})

	t.Run("at least one executable criterion needs no exception", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		item.AcceptanceCriteria = []AcceptanceCriterion{
			{Description: "descriptive only"},
			{Description: "machine-checkable", VerificationRef: "make verify"},
		}
		item.ApprovalException = nil

		if err := ValidateReadinessGate(item); err != nil {
			t.Fatalf("work item with one executable criterion rejected: %v", err)
		}
	})
}

func assertRejectedWithExecutableAcceptanceProblem(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected rejection for missing executable acceptance without an approval exception, got nil error")
	}
	readinessErr, ok := err.(*ReadinessError)
	if !ok {
		t.Fatalf("error type = %T, want *ReadinessError", err)
	}
	want := "no executable acceptance criterion is present and no approval exception was granted"
	if !containsProblem(readinessErr.Problems, want) {
		t.Fatalf("problems = %v, want to contain %q", readinessErr.Problems, want)
	}
}

// TestValidateReadinessGate_InvalidWorkflowVersionReference is the pure,
// structural subset of the task's own "invalid workflow" verify line:
// this package has no registry/UnitOfWork to confirm a WorkflowVersionID
// actually resolves to a real, published, compiled WorkflowVersion (see
// ValidateReadinessGate's own doc comment for that boundary) — it can only
// check the reference is well-formed (non-blank) when present, and that a
// nil reference (no pin at all, which go-core-spec's own
// "WorkflowVersionID?" marks optional) is not itself a violation.
func TestValidateReadinessGate_InvalidWorkflowVersionReference(t *testing.T) {
	t.Parallel()

	t.Run("nil workflow version id is valid (optional field)", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		item.WorkflowVersionID = nil
		if err := ValidateReadinessGate(item); err != nil {
			t.Fatalf("nil workflow version id rejected: %v", err)
		}
	})

	t.Run("blank workflow version id is rejected", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		blank := workflow.WorkflowVersionID("   ")
		item.WorkflowVersionID = &blank

		err := ValidateReadinessGate(item)
		if err == nil {
			t.Fatal("expected rejection for blank workflow version id, got nil error")
		}
		readinessErr, ok := err.(*ReadinessError)
		if !ok {
			t.Fatalf("error type = %T, want *ReadinessError", err)
		}
		if !containsProblem(readinessErr.Problems, "workflow version id is blank") {
			t.Fatalf("problems = %v, want to contain %q", readinessErr.Problems, "workflow version id is blank")
		}
	})

	t.Run("non-blank workflow version id is valid", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		pinned := workflow.WorkflowVersionID("wfv-1")
		item.WorkflowVersionID = &pinned
		if err := ValidateReadinessGate(item); err != nil {
			t.Fatalf("well-formed workflow version id rejected: %v", err)
		}
	})
}

// TestValidateReadinessGate_InvalidProjectAndScopeStructure is the pure,
// structural subset of the task's own "invalid project/scope" verify
// line: Kind/ParentID consistency (re-deriving NewChildWorkItem's own
// invariant, checked again here because a WorkItem handed to this
// validator need not have come through that constructor) and a blank
// entry inside a declared Exclusions list. Real cross-project reference
// correctness (does ParentID/FamilyID actually resolve against a real
// Project/TaskFamily row) needs I/O this pure validator does not have —
// see ValidateReadinessGate's own doc comment.
func TestValidateReadinessGate_InvalidProjectAndScopeStructure(t *testing.T) {
	t.Parallel()

	t.Run("root work item must not carry a parent id", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		parent := WorkItemID("some-other-item")
		item.ParentID = &parent

		err := ValidateReadinessGate(item)
		if err == nil {
			t.Fatal("expected rejection for a root work item with a parent id")
		}
		readinessErr := err.(*ReadinessError)
		if !containsProblem(readinessErr.Problems, "root work item must not have a parent id") {
			t.Fatalf("problems = %v, want the root/parent inconsistency problem", readinessErr.Problems)
		}
	})

	t.Run("child work item must carry a parent id", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		item.Kind = WorkItemChild
		item.ParentID = nil

		err := ValidateReadinessGate(item)
		if err == nil {
			t.Fatal("expected rejection for a child work item with no parent id")
		}
		readinessErr := err.(*ReadinessError)
		if !containsProblem(readinessErr.Problems, "child work item must have a parent id") {
			t.Fatalf("problems = %v, want the child/parent inconsistency problem", readinessErr.Problems)
		}
	})

	t.Run("unknown kind is rejected", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		item.Kind = WorkItemKind("SIBLING")

		err := ValidateReadinessGate(item)
		if err == nil {
			t.Fatal("expected rejection for an unknown work item kind")
		}
		readinessErr := err.(*ReadinessError)
		if !containsProblem(readinessErr.Problems, `unknown work item kind "SIBLING"`) {
			t.Fatalf("problems = %v, want the unknown-kind problem", readinessErr.Problems)
		}
	})

	t.Run("empty exclusions list is valid (nothing declared out of scope)", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		item.Exclusions = nil
		if err := ValidateReadinessGate(item); err != nil {
			t.Fatalf("nil exclusions rejected: %v", err)
		}
	})

	t.Run("blank exclusion entry is rejected", func(t *testing.T) {
		t.Parallel()
		item := validReadyRootWorkItem()
		item.Exclusions = []string{"legacy importer", "   "}

		err := ValidateReadinessGate(item)
		if err == nil {
			t.Fatal("expected rejection for a blank exclusion entry")
		}
		readinessErr := err.(*ReadinessError)
		if !containsProblem(readinessErr.Problems, "exclusion at index 1 is blank") {
			t.Fatalf("problems = %v, want the blank-exclusion problem", readinessErr.Problems)
		}
	})
}

// TestValidateReadinessGate_CollectsEveryProblem confirms
// ValidateReadinessGate reports every violation in one pass rather than
// stopping at the first, the same "collect everything" discipline
// workflow.ValidationError already established.
func TestValidateReadinessGate_CollectsEveryProblem(t *testing.T) {
	t.Parallel()
	item := WorkItem{Kind: WorkItemRoot}

	err := ValidateReadinessGate(item)
	if err == nil {
		t.Fatal("expected rejection for an entirely empty work item")
	}
	readinessErr, ok := err.(*ReadinessError)
	if !ok {
		t.Fatalf("error type = %T, want *ReadinessError", err)
	}
	for _, want := range []string{
		"project id is required",
		"family id is required",
		"title is required",
		"schema version must be positive",
		"behavior is required",
		"verification spec is required",
		"risk level is required",
		"no executable acceptance criterion is present and no approval exception was granted",
	} {
		if !containsProblem(readinessErr.Problems, want) {
			t.Fatalf("problems = %v, want to also contain %q", readinessErr.Problems, want)
		}
	}
}

// TestNewApprovalException_RequiresReasonActorAndTimestamp mirrors
// NewRepositoryScope's own "scope reason, actor and timestamp are
// required" coverage for the same audit-record shape, applied to
// ApprovalException.
func TestNewApprovalException_RequiresReasonActorAndTimestamp(t *testing.T) {
	t.Parallel()

	if _, err := NewApprovalException("", "operator", time.Now()); err == nil {
		t.Fatal("expected rejection for a blank reason")
	}
	if _, err := NewApprovalException("reason", "", time.Now()); err == nil {
		t.Fatal("expected rejection for a blank approver")
	}
	if _, err := NewApprovalException("reason", "operator", time.Time{}); err == nil {
		t.Fatal("expected rejection for a zero timestamp")
	}

	approvedAt := time.Now()
	exception, err := NewApprovalException("  needs human sign-off  ", "  operator  ", approvedAt)
	if err != nil {
		t.Fatalf("valid approval exception rejected: %v", err)
	}
	if exception.Reason() != "needs human sign-off" || exception.ApprovedBy() != "operator" {
		t.Fatalf("exception fields not trimmed: reason=%q approvedBy=%q", exception.Reason(), exception.ApprovedBy())
	}
	if !exception.ApprovedAt().Equal(approvedAt.UTC()) {
		t.Fatalf("ApprovedAt() = %v, want %v", exception.ApprovedAt(), approvedAt.UTC())
	}
}

func containsProblem(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}
