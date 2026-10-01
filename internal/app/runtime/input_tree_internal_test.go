package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// scriptedTreeSnapshotter answers SnapshotTree with a queue of tree IDs and
// DiffTrees with a fixed path list (or error).
type scriptedTreeSnapshotter struct {
	snapshots []string
	diff      []string
	diffErr   error
	diffCalls int
}

func (s *scriptedTreeSnapshotter) SnapshotTree(context.Context, ports.WorkspaceHandle) (string, error) {
	next := s.snapshots[0]
	s.snapshots = s.snapshots[1:]
	return next, nil
}

func (s *scriptedTreeSnapshotter) DiffTrees(context.Context, ports.WorkspaceHandle, string, string) ([]string, error) {
	s.diffCalls++
	return s.diff, s.diffErr
}

func oneMount(repository project.RepositoryID) []ports.AgentWorkspaceMount {
	return []ports.AgentWorkspaceMount{{RepositoryID: repository}}
}

// The strict check wraps scopeguard.ErrScopeViolation (so every caller's
// errors.Is handling covers it) and NAMES the changed paths.
func TestValidateStrictlyReadOnlyTrees_NamesChangedPaths(t *testing.T) {
	snapshotter := &scriptedTreeSnapshotter{snapshots: []string{"out"}, diff: []string{"docs/a b.md", "src/main.go"}}

	err := validateStrictlyReadOnlyTrees(context.Background(), snapshotter, oneMount("repo-1"), map[project.RepositoryID]string{"repo-1": "in"})
	if !errors.Is(err, scopeguard.ErrScopeViolation) {
		t.Fatalf("error = %v, want it to wrap scopeguard.ErrScopeViolation", err)
	}
	for _, want := range []string{"repo-1", `"docs/a b.md"`, `"src/main.go"`, "2 path(s)"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %s", err, want)
		}
	}
}

// Equal trees pass without even asking git for a diff.
func TestValidateStrictlyReadOnlyTrees_EqualTreesPass(t *testing.T) {
	snapshotter := &scriptedTreeSnapshotter{snapshots: []string{"same"}}

	if err := validateStrictlyReadOnlyTrees(context.Background(), snapshotter, oneMount("repo-1"), map[project.RepositoryID]string{"repo-1": "same"}); err != nil {
		t.Fatalf("error = %v, want nil for identical trees", err)
	}
	if snapshotter.diffCalls != 0 {
		t.Fatalf("DiffTrees called %d times for identical trees, want 0", snapshotter.diffCalls)
	}
}

// The path list in the message is capped.
func TestValidateStrictlyReadOnlyTrees_CapsTheReportedPaths(t *testing.T) {
	var changed []string
	for i := 0; i < maxReportedChangedPaths+7; i++ {
		changed = append(changed, fmt.Sprintf("file-%03d.txt", i))
	}
	snapshotter := &scriptedTreeSnapshotter{snapshots: []string{"out"}, diff: changed}

	err := validateStrictlyReadOnlyTrees(context.Background(), snapshotter, oneMount("repo-1"), map[project.RepositoryID]string{"repo-1": "in"})
	if !errors.Is(err, scopeguard.ErrScopeViolation) {
		t.Fatalf("error = %v, want ErrScopeViolation", err)
	}
	message := err.Error()
	if !strings.Contains(message, fmt.Sprintf("file-%03d.txt", maxReportedChangedPaths-1)) || strings.Contains(message, fmt.Sprintf("file-%03d.txt", maxReportedChangedPaths)) {
		t.Fatalf("message does not list exactly the first %d paths: %q", maxReportedChangedPaths, message)
	}
	if !strings.Contains(message, "(and 7 more)") || !strings.Contains(message, fmt.Sprintf("%d path(s)", maxReportedChangedPaths+7)) {
		t.Fatalf("message does not summarize the remaining paths: %q", message)
	}
}

// A missing InputTree (pruned, or never recorded for a mount) is
// ErrInputTreeMissing — a technical failure — and is NOT a scope violation.
func TestValidateStrictlyReadOnlyTrees_MissingInputTreeIsNotAScopeViolation(t *testing.T) {
	t.Run("pruned", func(t *testing.T) {
		snapshotter := &scriptedTreeSnapshotter{snapshots: []string{"out"}, diffErr: fmt.Errorf("%w: in", ports.ErrTreeNotFound)}
		err := validateStrictlyReadOnlyTrees(context.Background(), snapshotter, oneMount("repo-1"), map[project.RepositoryID]string{"repo-1": "in"})
		if !errors.Is(err, ErrInputTreeMissing) || errors.Is(err, scopeguard.ErrScopeViolation) {
			t.Fatalf("error = %v, want ErrInputTreeMissing and not a scope violation", err)
		}
	})
	t.Run("no tree recorded for the mount", func(t *testing.T) {
		snapshotter := &scriptedTreeSnapshotter{}
		err := validateStrictlyReadOnlyTrees(context.Background(), snapshotter, oneMount("repo-2"), map[project.RepositoryID]string{"repo-1": "in"})
		if !errors.Is(err, ErrInputTreeMissing) {
			t.Fatalf("error = %v, want ErrInputTreeMissing", err)
		}
	})
}

// V9-01: an AGENT execution's Evidence row is a RECORD of what the agent
// produced, never a verdict completion accepts — an agent's own claim must
// not be able to satisfy a CompletionPolicy's RequiredEvidenceKinds alone
// (the V5-15B false-completion bar), even if a policy names the AGENT kind.
func TestEvaluateCompletionRules_AgentExecutionRecordIsNeverPassingEvidence(t *testing.T) {
	rules := policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindAgentExecution}}
	agentRecord := runtimedomain.Evidence{Kind: runtimedomain.EvidenceKindAgentExecution, Verdict: runtimedomain.EvidenceVerdictRecorded}

	if satisfied, _ := evaluateCompletionRules(rules, []runtimedomain.Evidence{agentRecord}, nil); satisfied {
		t.Fatalf("a %s/%s row satisfied RequiredEvidenceKinds: an agent's own record must never count as passing evidence",
			agentRecord.Kind, agentRecord.Verdict)
	}
}
