package agentevents

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// This file is V11-01's answer to "a gate fails with VALIDATION_FAILED and
// nothing else": MACHINE_GATE re-reads the worktree's HEAD and refuses to
// evaluate a revision that is not the one the run pinned. When someone
// committed in the worktree outside aw (an IDE, a terminal) that comparison
// fails, and the attempt row keeps only the error code. The reason — which
// repository, which two revisions, which commits are the extra ones, what to
// do next — is therefore written into the attempt's own agent_events stream as
// one orchestrator-authored DIAGNOSTIC event, exactly like V9-09's scope
// violation list, and read back by the run timeline as the attempt's
// failureDetail.

const (
	// WorkspaceHeadDriftDiagnosticCode is the AgentDiagnostic.Code of the event
	// RecordWorkspaceHeadDrift writes, and the typed prefix of its message.
	WorkspaceHeadDriftDiagnosticCode = "WORKSPACE_HEAD_DRIFT"

	// maxDriftCommitsNamed bounds how many drift commits the detail names; the
	// rest are summarized as "and N more".
	maxDriftCommitsNamed = 5

	maxDriftSubjectRunes = 80
	maxDriftAuthorRunes  = 40
	shortCommitLength    = 8
)

// HeadDriftFacts is what the detail line is rendered from.
type HeadDriftFacts struct {
	RepositoryID string
	// Pinned is the revision the run pinned for the attempt; Head the worktree's
	// live HEAD.
	Pinned string
	Head   string
	// Audited reports whether the commit list below was read. A provider that
	// cannot audit (or an audit that failed) leaves it false and the detail
	// states only that HEAD differs.
	Audited      bool
	FastForward  bool
	TotalCommits int
	Commits      []ports.HeadAuditCommit
}

// WorkspaceHeadDriftDetail renders facts as one bounded, operator-readable
// line (a typed WORKSPACE_HEAD_DRIFT prefix, the two revisions, the commits
// made outside aw, and the way out).
func WorkspaceHeadDriftDetail(facts HeadDriftFacts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: repository %s: the worktree HEAD %s is not the revision aw pinned for this run (%s).",
		WorkspaceHeadDriftDiagnosticCode, sanitize(facts.RepositoryID), shortCommit(facts.Head), shortCommit(facts.Pinned))
	if facts.Audited {
		switch {
		case facts.TotalCommits == 0:
			b.WriteString(" No commit sits between them: HEAD is behind or beside the pin (a reset or a checkout).")
		default:
			fmt.Fprintf(&b, " %d commit(s) were made outside aw:", facts.TotalCommits)
			shown := facts.Commits
			if len(shown) > maxDriftCommitsNamed {
				shown = shown[len(shown)-maxDriftCommitsNamed:]
			}
			for i, commit := range shown {
				sep := ";"
				if i == 0 {
					sep = ""
				}
				fmt.Fprintf(&b, "%s %s %s %q", sep, shortCommit(commit.CommitID),
					truncateRunes(sanitize(commit.AuthorName), maxDriftAuthorRunes), truncateRunes(sanitize(commit.Subject), maxDriftSubjectRunes))
			}
			if hidden := facts.TotalCommits - len(shown); hidden > 0 {
				fmt.Fprintf(&b, "; and %d more", hidden)
			}
			b.WriteString(".")
		}
		if facts.FastForward {
			b.WriteString(" To keep them, run `aw repository-workspace adopt-head` on this repository workspace; this run keeps its old pin, so start the run again afterwards.")
		} else {
			fmt.Fprintf(&b, " HEAD no longer descends from the pinned revision (a reset or rebase), so aw cannot adopt it: put the worktree back on %s or reconcile the workspace.", shortCommit(facts.Pinned))
		}
	} else {
		b.WriteString(" aw could not list the commits. Inspect the worktree with `aw repository-workspace log`, then adopt HEAD or put it back on the pinned revision.")
	}
	return b.String()
}

func shortCommit(id string) string {
	if len(id) > shortCommitLength {
		return id[:shortCommitLength]
	}
	return id
}

// RecordWorkspaceHeadDrift appends one DIAGNOSTIC agent event (code
// WorkspaceHeadDriftDiagnosticCode, message WorkspaceHeadDriftDetail(facts)) to
// the attempt's stream. Like RecordScopeViolation it is a best-effort
// annotation of a failure that is already decided: the caller classifies the
// attempt regardless of what this returns.
func RecordWorkspaceHeadDrift(ctx context.Context, record ScopeViolationRecord, facts HeadDriftFacts) error {
	if record.AttemptID == "" || record.UOW == nil || record.IDs == nil || record.Clock == nil {
		return errors.New("agentevents: RecordWorkspaceHeadDrift requires AttemptID, UOW, IDs and Clock")
	}
	return recordOrchestratorDiagnostic(ctx, record, WorkspaceHeadDriftDiagnosticCode, WorkspaceHeadDriftDetail(facts))
}

// WorkspaceHeadDriftDetailFromRecords is the read side: the message of the LAST
// WorkspaceHeadDriftDiagnosticCode DIAGNOSTIC event in records, or "" when it
// has none.
func WorkspaceHeadDriftDetailFromRecords(records []ports.AgentEventRecord) string {
	return lastDiagnosticMessage(records, WorkspaceHeadDriftDiagnosticCode)
}
